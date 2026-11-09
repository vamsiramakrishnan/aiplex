package modules

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"time"
)

// RemoteConfig is administrator-owned configuration. Endpoints must never be
// accepted from an inference request or a remotely advertised descriptor.
type RemoteConfig struct {
	Descriptor Descriptor
	Endpoint   string
	Timeout    time.Duration
	// HTTP is permitted only for literal loopback addresses and localhost,
	// and only with this explicit development option.
	AllowInsecureLocalhost bool
	// Transport allows a host-owned mTLS or audience-token transport. Credentials
	// remain in the transport and are never exposed through module discovery.
	Transport http.RoundTripper
}

// Remote adapts an independently deployed service to the lifecycle contract.
// Start verifies the configured descriptor; Stop disconnects locally and never
// shuts down a service which may be shared by other hosts.
type Remote struct {
	descriptor Descriptor
	endpoint   string
	client     *http.Client
	running    atomic.Bool
}

func NewRemote(cfg RemoteConfig) (*Remote, error) {
	if err := cfg.Descriptor.Validate(); err != nil {
		return nil, err
	}
	u, err := url.Parse(cfg.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid module endpoint: %w", err)
	}
	if u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" || (u.Path != "" && u.Path != "/") || u.Opaque != "" {
		return nil, fmt.Errorf("module endpoint must be an HTTP(S) origin without credentials, path, query, or fragment")
	}
	if u.Scheme != "https" {
		ip := net.ParseIP(u.Hostname())
		local := u.Hostname() == "localhost" || (ip != nil && ip.IsLoopback())
		if u.Scheme != "http" || !cfg.AllowInsecureLocalhost || !local {
			return nil, fmt.Errorf("module endpoint requires HTTPS (HTTP loopback needs explicit development opt-in)")
		}
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 2 * time.Second
	}
	if cfg.Timeout < 0 || cfg.Timeout > 30*time.Second {
		return nil, fmt.Errorf("module timeout must be between zero and 30 seconds")
	}
	return &Remote{descriptor: clone(cfg.Descriptor), endpoint: strings.TrimSuffix(u.String(), "/"), client: &http.Client{
		Timeout: cfg.Timeout, Transport: cfg.Transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

func (r *Remote) Descriptor() Descriptor { return clone(r.descriptor) }
func (r *Remote) Start(ctx context.Context) error {
	r.running.Store(false)
	response, err := r.get(ctx, "/module/v1/descriptor")
	if err != nil {
		return err
	}
	defer response.Body.Close()
	var actual Descriptor
	payload, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil {
		return fmt.Errorf("read remote descriptor: %w", err)
	}
	if len(payload) > 1<<20 {
		return fmt.Errorf("remote descriptor exceeds 1 MiB")
	}
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&actual); err != nil {
		return fmt.Errorf("decode remote descriptor: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("remote descriptor contains trailing data")
	}
	if err := actual.Validate(); err != nil {
		return err
	}
	if !reflect.DeepEqual(normalize(actual), normalize(r.descriptor)) {
		return fmt.Errorf("remote module descriptor does not match configured descriptor")
	}
	r.running.Store(true)
	return nil
}
func normalize(d Descriptor) Descriptor {
	// JSON roundtrip makes nil and empty optional collections equivalent.
	b, _ := json.Marshal(d)
	var out Descriptor
	_ = json.Unmarshal(b, &out)
	return out
}
func (r *Remote) Ready(ctx context.Context) error {
	if !r.running.Load() {
		return fmt.Errorf("remote module has not started")
	}
	response, err := r.get(ctx, "/readyz")
	if err != nil {
		return err
	}
	response.Body.Close()
	return nil
}
func (r *Remote) Stop(context.Context) error { r.running.Store(false); return nil }
func (r *Remote) get(ctx context.Context, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.endpoint+path, nil)
	if err != nil {
		return nil, err
	}
	response, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("probe module %s: %w", r.descriptor.Name, err)
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return nil, fmt.Errorf("module %s probe returned HTTP %d", r.descriptor.Name, response.StatusCode)
	}
	return response, nil
}
