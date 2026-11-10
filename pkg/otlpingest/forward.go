package otlpingest

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	collogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/protobuf/proto"
)

// EnvForward turns forwarding of signals AIPlex does not record on. Default
// off.
const EnvForward = "AIPLEX_OTLP_FORWARD"

// Resource attributes AIPlex adds to forwarded telemetry. They come from the
// verified token. Any aiplex.* resource attribute the sender supplied is
// removed first, so a sender cannot claim another identity.
const (
	ResourcePrincipal = "aiplex.principal.id"
	ResourceTenant    = "aiplex.tenant.id"
	ResourceCaller    = "aiplex.caller.kind"
	ResourceClient    = "aiplex.client.id"
)

// Forwarder sends telemetry AIPlex does not record to the operator's OTLP
// collector (OTEL_EXPORTER_OTLP_ENDPOINT and friends) over http/protobuf. It
// sends the operator's configured headers only: the sender's Authorization
// header is never forwarded. Forwarding is best effort: a full queue or a
// failing collector drops the data and never fails the sender's export.
type Forwarder struct {
	endpoints map[Signal]string
	headers   map[string]string
	client    *http.Client
	queue     chan forwardJob
	onResult  func(Signal, bool)
	wg        sync.WaitGroup
	once      sync.Once
}

type forwardJob struct {
	signal Signal
	body   []byte
}

// NewForwarder reads AIPLEX_OTLP_FORWARD and the standard OTEL_EXPORTER_OTLP_*
// variables. It returns nil when forwarding is off. Forwarding on without an
// http/protobuf endpoint is an error, so a misconfiguration does not silently
// discard telemetry. onResult (optional) is told the outcome of each send.
func NewForwarder(getenv func(string) string, client *http.Client, onResult func(Signal, bool)) (*Forwarder, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	raw := strings.TrimSpace(getenv(EnvForward))
	if raw == "" {
		return nil, nil
	}
	on, err := strconv.ParseBool(raw)
	if err != nil {
		return nil, fmt.Errorf("%s must be true or false", EnvForward)
	}
	if !on {
		return nil, nil
	}
	f := &Forwarder{endpoints: map[Signal]string{}, headers: map[string]string{}, client: client, queue: make(chan forwardJob, 256), onResult: onResult}
	if f.client == nil {
		f.client = &http.Client{Timeout: 5 * time.Second}
	}
	for _, s := range []Signal{SignalLogs, SignalMetrics, SignalTraces} {
		protocol := firstNonEmpty(getenv("OTEL_EXPORTER_OTLP_"+strings.ToUpper(string(s))+"_PROTOCOL"), getenv("OTEL_EXPORTER_OTLP_PROTOCOL"))
		if protocol != "" && protocol != "http/protobuf" {
			return nil, fmt.Errorf("%s=true needs the http/protobuf protocol; %s is not supported for forwarding", EnvForward, protocol)
		}
		endpoint := strings.TrimSpace(getenv("OTEL_EXPORTER_OTLP_" + strings.ToUpper(string(s)) + "_ENDPOINT"))
		if endpoint == "" {
			base := strings.TrimRight(strings.TrimSpace(getenv("OTEL_EXPORTER_OTLP_ENDPOINT")), "/")
			if base == "" {
				return nil, fmt.Errorf("%s=true needs OTEL_EXPORTER_OTLP_ENDPOINT (or a per-signal endpoint)", EnvForward)
			}
			endpoint = base + "/v1/" + string(s)
		}
		u, err := url.Parse(endpoint)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return nil, fmt.Errorf("%s: the OTLP endpoint for %s must be an http(s) URL", EnvForward, s)
		}
		f.endpoints[s] = endpoint
	}
	for _, pair := range strings.Split(firstNonEmpty(getenv("OTEL_EXPORTER_OTLP_HEADERS")), ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(pair), "=")
		if !ok || strings.TrimSpace(k) == "" {
			continue
		}
		if value, err := url.PathUnescape(strings.TrimSpace(v)); err == nil {
			v = value
		}
		f.headers[strings.TrimSpace(k)] = v
	}
	for i := 0; i < 2; i++ {
		f.wg.Add(1)
		go f.run()
	}
	return f, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// Endpoint returns the URL a signal is forwarded to (for the startup log).
// It may contain a path but never headers.
func (f *Forwarder) Endpoint(s Signal) string {
	if f == nil {
		return ""
	}
	u, err := url.Parse(f.endpoints[s])
	if err != nil {
		return ""
	}
	u.User = nil
	u.RawQuery = ""
	return u.String()
}

// Forward annotates a copy of msg with the verified sender and queues it. It
// reports whether the data was queued.
func (f *Forwarder) Forward(sender Sender, signal Signal, msg proto.Message) bool {
	if f == nil || msg == nil {
		return false
	}
	clone := proto.Clone(msg)
	annotate(clone, sender)
	body, err := proto.Marshal(clone)
	if err != nil {
		return false
	}
	select {
	case f.queue <- forwardJob{signal: signal, body: body}:
		return true
	default:
		if f.onResult != nil {
			f.onResult(signal, false)
		}
		return false
	}
}

func (f *Forwarder) run() {
	defer f.wg.Done()
	for job := range f.queue {
		ok := f.send(job)
		if f.onResult != nil {
			f.onResult(job.signal, ok)
		}
	}
}

func (f *Forwarder) send(job forwardJob) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.endpoints[job.signal], bytes.NewReader(job.body))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/x-protobuf")
	for k, v := range f.headers {
		req.Header.Set(k, v)
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

// Close stops accepting data and waits for queued sends.
func (f *Forwarder) Close() {
	if f == nil {
		return
	}
	f.once.Do(func() {
		close(f.queue)
		f.wg.Wait()
	})
}

// annotate removes sender-supplied aiplex.* resource attributes and adds the
// verified identity.
func annotate(msg proto.Message, sender Sender) {
	each := func(r *resourcepb.Resource) *resourcepb.Resource {
		if r == nil {
			r = &resourcepb.Resource{}
		}
		kept := r.Attributes[:0:0]
		for _, kv := range r.Attributes {
			if !strings.HasPrefix(kv.GetKey(), "aiplex.") {
				kept = append(kept, kv)
			}
		}
		add := func(k, v string) {
			if v != "" {
				kept = append(kept, &commonpb.KeyValue{Key: k, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: v}}})
			}
		}
		add(ResourcePrincipal, sender.PrincipalID)
		add(ResourceTenant, sender.TenantID)
		add(ResourceCaller, sender.CallerKind)
		add(ResourceClient, sender.ClientID)
		r.Attributes = kept
		return r
	}
	switch m := msg.(type) {
	case *collogs.ExportLogsServiceRequest:
		for _, rl := range m.ResourceLogs {
			rl.Resource = each(rl.Resource)
		}
	case *colmetrics.ExportMetricsServiceRequest:
		for _, rm := range m.ResourceMetrics {
			rm.Resource = each(rm.Resource)
		}
	case *coltrace.ExportTraceServiceRequest:
		for _, rs := range m.ResourceSpans {
			rs.Resource = each(rs.Resource)
		}
	}
}
