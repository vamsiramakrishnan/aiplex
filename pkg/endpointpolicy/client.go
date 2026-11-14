// Package endpointpolicy supplies an operator-pinned outbound network boundary.
// It validates origins and pins validated DNS answers at dial time, retaining
// normal TLS hostname verification. It never uses ambient HTTP proxies.
package endpointpolicy

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

type Resolver interface {
	LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
}
type Config struct {
	AllowedOrigins    []string      `json:"allowed_origins"`
	AllowPrivateCIDRs []string      `json:"allow_private_cidrs,omitempty"`
	AllowLoopback     bool          `json:"allow_loopback,omitempty"`
	Timeout           time.Duration `json:"timeout,omitempty"`
	Resolver          Resolver      `json:"-"`
	TLSConfig         *tls.Config   `json:"-"`
}
type Policy struct {
	origins  map[string]bool
	private  []netip.Prefix
	loopback bool
	resolver Resolver
	timeout  time.Duration
	tls      *tls.Config
}

func New(cfg Config) (*Policy, error) {
	p := &Policy{origins: map[string]bool{}, loopback: cfg.AllowLoopback, resolver: cfg.Resolver, timeout: cfg.Timeout, tls: cfg.TLSConfig}
	if p.resolver == nil {
		p.resolver = net.DefaultResolver
	}
	if p.timeout == 0 {
		p.timeout = 15 * time.Second
	}
	if p.timeout < 0 {
		return nil, fmt.Errorf("negative timeout")
	}
	if p.tls != nil {
		p.tls = p.tls.Clone()
	}
	if p.tls != nil && p.tls.InsecureSkipVerify {
		return nil, fmt.Errorf("TLS verification cannot be disabled")
	}
	for _, raw := range cfg.AllowedOrigins {
		u, e := parse(raw, cfg.AllowLoopback)
		if e != nil {
			return nil, e
		}
		if u.Path != "" && u.Path != "/" || u.RawQuery != "" {
			return nil, fmt.Errorf("allowed origin cannot include a path/query")
		}
		p.origins[origin(u)] = true
	}
	if len(p.origins) == 0 {
		return nil, fmt.Errorf("at least one explicit origin required")
	}
	for _, raw := range cfg.AllowPrivateCIDRs {
		v, e := netip.ParsePrefix(raw)
		if e != nil {
			return nil, e
		}
		p.private = append(p.private, v)
	}
	return p, nil
}
func parse(raw string, local bool) (*url.URL, error) {
	u, e := url.Parse(raw)
	if e != nil || u.Host == "" || u.User != nil || u.Fragment != "" || u.Opaque != "" || strings.ContainsAny(raw, "\r\n\\") {
		return nil, fmt.Errorf("invalid endpoint URL")
	}
	if u.Scheme != "https" {
		ip, e := netip.ParseAddr(u.Hostname())
		isLocal := u.Hostname() == "localhost" || (e == nil && ip.IsLoopback())
		if u.Scheme != "http" || !local || !isLocal {
			return nil, fmt.Errorf("HTTPS required; loopback HTTP requires explicit opt-in")
		}
	}
	if strings.Contains(u.Hostname(), "%") {
		return nil, fmt.Errorf("scoped addresses unsupported")
	}
	return u, nil
}
func origin(u *url.URL) string {
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return u.Scheme + "://" + net.JoinHostPort(host, port)
}
func (p *Policy) ValidateURL(raw string) error {
	u, e := parse(raw, p.loopback)
	if e != nil {
		return e
	}
	if !p.origins[origin(u)] {
		return fmt.Errorf("origin not approved")
	}
	return nil
}
func (p *Policy) allowed(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return false
	}
	// Metadata and platform-reserved endpoints remain prohibited even if a broad
	// corporate CIDR was approved. Never turn private networking into metadata access.
	for _, block := range []string{"169.254.0.0/16", "100.100.100.200/32", "168.63.129.16/32", "fd00:ec2::254/128", "fd20:ce::254/128"} {
		if netip.MustParsePrefix(block).Contains(ip) {
			return false
		}
	}
	if ip.IsLoopback() {
		return p.loopback
	}
	if ip.IsPrivate() {
		for _, prefix := range p.private {
			if prefix.Contains(ip) {
				return true
			}
		}
		return false
	}
	return ip.IsGlobalUnicast()
}
func (p *Policy) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, e := net.SplitHostPort(address)
	if e != nil {
		return nil, e
	}
	// Dialing must also be origin-authorized; hostname alone is insufficient.
	authorized, plain := false, true
	for o := range p.origins {
		u, _ := url.Parse(o)
		if strings.EqualFold(u.Hostname(), host) && u.Port() == port {
			authorized = true
			if u.Scheme == "https" {
				plain = false
			}
		}
	}
	if !authorized {
		return nil, fmt.Errorf("dial destination not approved")
	}
	var ips []netip.Addr
	if ip, e := netip.ParseAddr(host); e == nil {
		ips = []netip.Addr{ip}
	} else {
		ips, e = p.resolver.LookupNetIP(ctx, "ip", host)
		if e != nil {
			return nil, e
		}
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("empty DNS answer")
	}
	for _, ip := range ips {
		if !p.allowed(ip) {
			return nil, fmt.Errorf("DNS answer includes disallowed address")
		}
		// Plain http is permitted only to loopback (allow_loopback): a name such
		// as localhost must resolve to loopback addresses only, never to a public
		// or private one, which would carry the request over the network
		// unencrypted. The https rules are unchanged.
		if plain && !ip.Unmap().IsLoopback() {
			return nil, fmt.Errorf("plain http origin resolved to a non-loopback address")
		}
	}
	d := net.Dialer{Timeout: p.timeout}
	var last error
	for _, ip := range ips {
		conn, e := d.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if e == nil {
			return conn, nil
		}
		last = e
	}
	return nil, last
}

type transport struct {
	policy *Policy
	next   http.RoundTripper
}

func (t transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if e := t.policy.ValidateURL(req.URL.String()); e != nil {
		return nil, e
	}
	if req.Host != "" && !strings.EqualFold(req.Host, req.URL.Host) {
		return nil, fmt.Errorf("Host override prohibited")
	}
	return t.next.RoundTrip(req)
}
func (p *Policy) Client() *http.Client {
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.Proxy = nil
	base.DialContext = p.DialContext
	base.ResponseHeaderTimeout = p.timeout
	if p.tls != nil {
		base.TLSClientConfig = p.tls.Clone()
	}
	return &http.Client{Transport: transport{p, base}, Timeout: p.timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}
func NewClient(cfg Config) (*http.Client, error) {
	p, e := New(cfg)
	if e != nil {
		return nil, e
	}
	return p.Client(), nil
}

// StreamingClient retains connect/header timeouts but leaves the body lifetime
// to request cancellation and the protocol adapter's frame/session bounds.
func (p *Policy) StreamingClient() *http.Client { c := p.Client(); c.Timeout = 0; return c }
