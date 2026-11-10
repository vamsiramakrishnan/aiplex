package api

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	collogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"

	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/pkg/otlpingest"
	"github.com/vamsiramakrishnan/aiplex/pkg/telemetry"
	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

// OTLPHandler receives OTLP/HTTP exports from coding harnesses
// (POST /otlp/v1/logs, /otlp/v1/metrics, /otlp/v1/traces).
//
// The sender is authenticated like any caller: the Bearer token the harness
// puts in OTEL_EXPORTER_OTLP_HEADERS is verified, and tenant, principal,
// client and caller kind come from that token only. Resource and record
// attributes are untrusted labels.
//
// Only log events are turned into usage records (see pkg/otlpingest);
// metrics and traces are accepted, counted and dropped, or forwarded to the
// operator's collector when AIPLEX_OTLP_FORWARD=true.
type OTLPHandler struct {
	Ledger usageledger.Store
	Tenant TenantResolver
	// Caller derives the caller kind from the verified identity.
	Caller func(ctx context.Context, tenant string, identity *models.WIFIdentity) (accessPointCaller, error)
	// Prices supplies model-price resources to estimate cost for events that
	// reported none; nil leaves such cost unknown.
	Prices      func(context.Context, string) ([]usageledger.Price, error)
	Tags        *TagPolicy
	Forwarder   *otlpingest.Forwarder
	Limiter     *otlpingest.Limiter
	Development bool

	// MaxRecords bounds the log records, metrics or spans in one export.
	MaxRecords int
	// MaxWireBytes and MaxDecodedBytes bound the body as sent and decompressed.
	MaxWireBytes, MaxDecodedBytes int64
	Retention                     time.Duration
	Now                           func() time.Time

	recent recentIDs
}

// Environment variables of the receiver.
const (
	EnvOTLPMaxRecords    = "AIPLEX_OTLP_MAX_RECORDS_PER_REQUEST"
	EnvOTLPMaxBodyBytes  = "AIPLEX_OTLP_MAX_BODY_BYTES"
	EnvOTLPRequestsPerS  = "AIPLEX_OTLP_REQUESTS_PER_SECOND"
	EnvOTLPRecordsPerS   = "AIPLEX_OTLP_RECORDS_PER_SECOND"
	defaultOTLPMaxRecord = 1000
	defaultOTLPMaxWire   = 4 << 20
	otlpDecodedFactor    = 4
)

// NewOTLPHandler builds the receiver from the environment: record and body
// bounds and per-principal rates (see the Env constants).
func NewOTLPHandler(ledger usageledger.Store, tenant TenantResolver, development bool, getenv func(string) string) (*OTLPHandler, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	intEnv := func(name string, fallback int64, min, max int64) (int64, error) {
		raw := strings.TrimSpace(getenv(name))
		if raw == "" {
			return fallback, nil
		}
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < min || n > max {
			return 0, errors.New(name + " must be an integer between " + strconv.FormatInt(min, 10) + " and " + strconv.FormatInt(max, 10))
		}
		return n, nil
	}
	maxRecords, err := intEnv(EnvOTLPMaxRecords, defaultOTLPMaxRecord, 1, 100_000)
	if err != nil {
		return nil, err
	}
	maxWire, err := intEnv(EnvOTLPMaxBodyBytes, defaultOTLPMaxWire, 1024, 64<<20)
	if err != nil {
		return nil, err
	}
	rps, err := intEnv(EnvOTLPRequestsPerS, 0, 0, 10_000)
	if err != nil {
		return nil, err
	}
	recps, err := intEnv(EnvOTLPRecordsPerS, 0, 0, 1_000_000)
	if err != nil {
		return nil, err
	}
	cfg := otlpingest.LimiterConfig{RequestsPerSecond: float64(rps), RecordsPerSecond: float64(recps)}
	if recps > 0 && recps*2 > int64(otlpingest.DefaultLimiterConfig().RecordBurst) {
		cfg.RecordBurst = int(recps * 2)
	}
	return &OTLPHandler{
		Ledger: ledger, Tenant: tenant, Development: development,
		Limiter:    otlpingest.NewLimiter(cfg, 0),
		MaxRecords: int(maxRecords), MaxWireBytes: maxWire, MaxDecodedBytes: maxWire * otlpDecodedFactor,
		Retention: usageledger.DefaultRetention, Now: time.Now,
	}, nil
}

// NewOTLPHandler builds the receiver for this deployment: it derives caller
// kind, prices and tag policy from the connected handler.
func (h *ConnectedHandler) NewOTLPHandler(ledger usageledger.Store, getenv func(string) string) (*OTLPHandler, error) {
	o, err := NewOTLPHandler(ledger, h.Tenant, h.Development, getenv)
	if err != nil {
		return nil, err
	}
	o.Caller, o.Prices, o.Tags = h.resolveAccessPointCaller, h.ModelPrices, h.Tags
	return o, nil
}

// Mount registers the three receiver routes under /otlp/v1.
func (h *OTLPHandler) Mount(r interface {
	Post(pattern string, handlerFn http.HandlerFunc)
}) {
	r.Post("/otlp/v1/logs", h.Logs)
	r.Post("/otlp/v1/metrics", h.Metrics)
	r.Post("/otlp/v1/traces", h.Traces)
}

type otlpRequest struct {
	signal otlpingest.Signal
	kind   string
	body   []byte
	sender otlpingest.Sender
	// key identifies the sender for rate limits.
	key string
}

func (h *OTLPHandler) fail(w http.ResponseWriter, kind string, status int, code int32, message string, retryAfter time.Duration) {
	contentType, body := otlpingest.EncodeStatus(kind, code, message)
	w.Header().Set("Content-Type", contentType)
	if retryAfter > 0 {
		secs := int((retryAfter + time.Second - 1) / time.Second)
		if secs < 1 {
			secs = 1
		}
		w.Header().Set("Retry-After", strconv.Itoa(secs))
	}
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// prepare authenticates the sender, enforces content type, size and request
// rate, and reads the body.
func (h *OTLPHandler) prepare(w http.ResponseWriter, r *http.Request, signal otlpingest.Signal) (*otlpRequest, bool) {
	kind, err := otlpingest.ContentKind(r.Header.Get("Content-Type"))
	if err != nil {
		h.fail(w, otlpingest.KindJSON, http.StatusUnsupportedMediaType, otlpingest.CodeInvalidArgument, "Content-Type must be application/x-protobuf or application/json", 0)
		return nil, false
	}
	if h.Ledger == nil || h.Tenant == nil {
		h.fail(w, kind, http.StatusServiceUnavailable, otlpingest.CodeUnavailable, "usage ledger is not configured", 10*time.Second)
		return nil, false
	}
	tenant, err := h.Tenant(r)
	if err != nil || tenant == "" {
		h.fail(w, kind, http.StatusForbidden, otlpingest.CodePermissionDenied, "verified tenant binding required", 0)
		return nil, false
	}
	sender := otlpingest.Sender{TenantID: tenant}
	key := "anonymous"
	identity := GetWIFIdentity(r.Context())
	switch {
	case identity != nil && identity.PrincipalID != "":
		caller := accessPointCaller{}
		if h.Caller != nil {
			if caller, err = h.Caller(r.Context(), tenant, identity); err != nil {
				h.fail(w, kind, http.StatusServiceUnavailable, otlpingest.CodeUnavailable, "caller resolution is unavailable", 5*time.Second)
				return nil, false
			}
		}
		sender.PrincipalID, sender.PrincipalDisplay = identity.PrincipalID, principalDisplay(identity)
		sender.ActorID, sender.ActorDisplay = identity.ActorID, boundedDisplay(identity.ActorSubject)
		sender.ClientID, sender.CallerKind = identity.AuthorizedParty, caller.Kind
		key = identity.PrincipalID
	case h.Development:
		// Loopback development mode is unauthenticated by design; its records
		// have no principal.
	default:
		h.fail(w, kind, http.StatusUnauthorized, otlpingest.CodeUnauthenticated, "verified identity required", 0)
		return nil, false
	}
	if ok, wait := h.Limiter.AllowRequest(key); !ok {
		telemetry.Metrics().RecordOTLP(r.Context(), string(signal), otlpingest.HarnessUnknown, telemetry.OTLPRateLimited, 1)
		h.fail(w, kind, http.StatusTooManyRequests, otlpingest.CodeResourceExhausted, "request rate limit exceeded", wait)
		return nil, false
	}
	body, err := otlpingest.ReadBody(r.Body, r.Header.Get("Content-Encoding"), h.MaxWireBytes, h.MaxDecodedBytes)
	switch {
	case errors.Is(err, otlpingest.ErrTooLarge):
		telemetry.Metrics().RecordOTLP(r.Context(), string(signal), otlpingest.HarnessUnknown, telemetry.OTLPTooLarge, 1)
		h.fail(w, kind, http.StatusRequestEntityTooLarge, otlpingest.CodeInvalidArgument, "export is too large; send smaller batches", 0)
		return nil, false
	case errors.Is(err, otlpingest.ErrUnsupportedEncoding):
		h.fail(w, kind, http.StatusUnsupportedMediaType, otlpingest.CodeInvalidArgument, "Content-Encoding must be gzip or identity", 0)
		return nil, false
	case err != nil:
		telemetry.Metrics().RecordOTLP(r.Context(), string(signal), otlpingest.HarnessUnknown, telemetry.OTLPMalformed, 1)
		h.fail(w, kind, http.StatusBadRequest, otlpingest.CodeInvalidArgument, "export could not be read", 0)
		return nil, false
	}
	return &otlpRequest{signal: signal, kind: kind, body: body, sender: sender, key: key}, true
}

func (h *OTLPHandler) decode(w http.ResponseWriter, r *http.Request, req *otlpRequest, msg proto.Message) bool {
	if err := otlpingest.Unmarshal(req.kind, req.body, msg); err != nil {
		telemetry.Metrics().RecordOTLP(r.Context(), string(req.signal), otlpingest.HarnessUnknown, telemetry.OTLPMalformed, 1)
		h.fail(w, req.kind, http.StatusBadRequest, otlpingest.CodeInvalidArgument, "export is not a valid OTLP "+string(req.signal)+" request", 0)
		return false
	}
	return true
}

// admitCount enforces the per-request item bound and the record rate.
func (h *OTLPHandler) admitCount(w http.ResponseWriter, r *http.Request, req *otlpRequest, n int) bool {
	if n > h.MaxRecords {
		telemetry.Metrics().RecordOTLP(r.Context(), string(req.signal), otlpingest.HarnessUnknown, telemetry.OTLPTooLarge, int64(n))
		h.fail(w, req.kind, http.StatusRequestEntityTooLarge, otlpingest.CodeInvalidArgument, "export has more than "+strconv.Itoa(h.MaxRecords)+" items; send smaller batches", 0)
		return false
	}
	if ok, wait := h.Limiter.AllowRecords(req.key, n); !ok {
		telemetry.Metrics().RecordOTLP(r.Context(), string(req.signal), otlpingest.HarnessUnknown, telemetry.OTLPRateLimited, int64(n))
		h.fail(w, req.kind, http.StatusTooManyRequests, otlpingest.CodeResourceExhausted, "record rate limit exceeded", wait)
		return false
	}
	return true
}

func (h *OTLPHandler) succeed(w http.ResponseWriter, req *otlpRequest, rejected int64, message string) {
	contentType, body, err := otlpingest.EncodeSuccess(req.kind, req.signal, rejected, message)
	if err != nil {
		h.fail(w, req.kind, http.StatusInternalServerError, otlpingest.CodeInternal, "response could not be encoded", 0)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// Logs records the usage events of an OTLP logs export.
// POST /otlp/v1/logs
func (h *OTLPHandler) Logs(w http.ResponseWriter, r *http.Request) {
	req, ok := h.prepare(w, r, otlpingest.SignalLogs)
	if !ok {
		return
	}
	var export collogs.ExportLogsServiceRequest
	if !h.decode(w, r, req, &export) {
		return
	}
	total := 0
	for _, rl := range export.GetResourceLogs() {
		for _, sl := range rl.GetScopeLogs() {
			total += len(sl.GetLogRecords())
		}
	}
	if !h.admitCount(w, r, req, total) {
		return
	}
	now := time.Now()
	if h.Now != nil {
		now = h.Now()
	}
	res := otlpingest.MapLogs(&export, req.sender, now, h.Retention)
	tags := h.Tags.requestTags(r, req.sender.TenantID, nil)
	var prices []usageledger.Price
	if h.Prices != nil && len(res.Records) > 0 {
		if list, err := h.Prices(r.Context(), req.sender.TenantID); err == nil {
			prices = list
		}
	}
	for i := range res.Records {
		rec := &res.Records[i]
		if len(tags) > 0 {
			rec.Tags = tags
		}
		if rec.Cost.Basis == usageledger.BasisUnknown && rec.ModelID != "" && len(prices) > 0 {
			rec.Cost = usageledger.Estimate(prices, *rec)
		}
	}
	recorded, duplicate, err := h.store(r.Context(), res.Records)
	m := telemetry.Metrics()
	if err != nil {
		log.Error().Err(err).Str("tenant", req.sender.TenantID).Msg("otlp logs not stored")
		h.fail(w, req.kind, http.StatusServiceUnavailable, otlpingest.CodeUnavailable, "usage ledger unavailable; retry", 5*time.Second)
		return
	}
	harness := otlpingest.HarnessUnknown
	if len(res.Records) > 0 {
		harness = res.Records[0].Harness
	}
	m.RecordOTLP(r.Context(), "logs", harness, telemetry.OTLPRecorded, recorded)
	m.RecordOTLP(r.Context(), "logs", harness, telemetry.OTLPDuplicate, duplicate)
	m.RecordOTLP(r.Context(), "logs", harness, telemetry.OTLPRejected, res.Rejected)
	for h2, n := range res.UnmappedByHarness {
		m.RecordOTLP(r.Context(), "logs", h2, telemetry.OTLPDropped, n)
	}
	if res.Forward != nil && h.Forwarder != nil {
		if h.Forwarder.Forward(req.sender, otlpingest.SignalLogs, res.Forward) {
			m.RecordOTLP(r.Context(), "logs", otlpingest.HarnessUnknown, telemetry.OTLPForwarded, res.Unmapped)
		}
	}
	message := ""
	if res.Rejected > 0 {
		message = strings.Join(res.Errors, "; ")
	}
	h.succeed(w, req, res.Rejected, message)
}

// Metrics accepts an OTLP metrics export. Metrics are not recorded: events
// are the source of truth for usage, and a counter cannot be added to them
// without counting a request twice. They are forwarded when forwarding is on.
// POST /otlp/v1/metrics
func (h *OTLPHandler) Metrics(w http.ResponseWriter, r *http.Request) {
	req, ok := h.prepare(w, r, otlpingest.SignalMetrics)
	if !ok {
		return
	}
	var export colmetrics.ExportMetricsServiceRequest
	if !h.decode(w, r, req, &export) {
		return
	}
	total := 0
	for _, rm := range export.GetResourceMetrics() {
		for _, sm := range rm.GetScopeMetrics() {
			total += len(sm.GetMetrics())
		}
	}
	if !h.admitCount(w, r, req, total) {
		return
	}
	h.passThrough(r, req, &export, total)
	h.succeed(w, req, 0, "")
}

// Traces accepts an OTLP traces export. Spans are not recorded (see Metrics).
// POST /otlp/v1/traces
func (h *OTLPHandler) Traces(w http.ResponseWriter, r *http.Request) {
	req, ok := h.prepare(w, r, otlpingest.SignalTraces)
	if !ok {
		return
	}
	var export coltrace.ExportTraceServiceRequest
	if !h.decode(w, r, req, &export) {
		return
	}
	total := 0
	for _, rs := range export.GetResourceSpans() {
		for _, ss := range rs.GetScopeSpans() {
			total += len(ss.GetSpans())
		}
	}
	if !h.admitCount(w, r, req, total) {
		return
	}
	h.passThrough(r, req, &export, total)
	h.succeed(w, req, 0, "")
}

func (h *OTLPHandler) passThrough(r *http.Request, req *otlpRequest, msg proto.Message, total int) {
	m := telemetry.Metrics()
	m.RecordOTLP(r.Context(), string(req.signal), otlpingest.HarnessUnknown, telemetry.OTLPDropped, int64(total))
	if total > 0 && h.Forwarder != nil && h.Forwarder.Forward(req.sender, req.signal, msg) {
		m.RecordOTLP(r.Context(), string(req.signal), otlpingest.HarnessUnknown, telemetry.OTLPForwarded, int64(total))
	}
}

// store appends records with bounded concurrency. A conflict is a retried
// event already stored (record ids are deterministic) and counts as a
// duplicate. Any other failure fails the request so the exporter retries.
func (h *OTLPHandler) store(ctx context.Context, records []usageledger.Record) (recorded, duplicate int64, err error) {
	var mu sync.Mutex
	var firstErr error
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for _, rec := range records {
		if h.recent.seen(rec.ID) {
			mu.Lock()
			duplicate++
			mu.Unlock()
			continue
		}
		sem <- struct{}{}
		wg.Add(1)
		go func(rec usageledger.Record) {
			defer wg.Done()
			defer func() { <-sem }()
			appendErr := h.Ledger.Append(ctx, rec)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case appendErr == nil:
				recorded++
				h.recent.add(rec.ID)
			case errors.Is(appendErr, usageledger.ErrConflict):
				duplicate++
				h.recent.add(rec.ID)
			default:
				if firstErr == nil {
					firstErr = appendErr
				}
			}
		}(rec)
	}
	wg.Wait()
	return recorded, duplicate, firstErr
}

// recentIDs is a bounded in-process window of stored record ids. It saves a
// store round trip for an immediate retry; the store's own conflict check is
// the durable guarantee.
type recentIDs struct {
	mu  sync.Mutex
	ids map[string]time.Time
}

const (
	recentIDsTTL = 30 * time.Minute
	recentIDsMax = 100_000
)

func (c *recentIDs) seen(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	at, ok := c.ids[id]
	return ok && time.Since(at) < recentIDsTTL
}

func (c *recentIDs) add(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ids == nil {
		c.ids = map[string]time.Time{}
	}
	if len(c.ids) >= recentIDsMax {
		for k, at := range c.ids {
			if time.Since(at) >= recentIDsTTL {
				delete(c.ids, k)
			}
		}
		if len(c.ids) >= recentIDsMax {
			c.ids = map[string]time.Time{}
		}
	}
	c.ids[id] = time.Now()
}
