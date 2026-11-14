package endpointpolicy

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestPinnedOriginRedirectAndMetadata(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "http://169.254.169.254/", 302) }))
	defer s.Close()
	c, e := NewClient(Config{AllowedOrigins: []string{s.URL}, AllowLoopback: true})
	if e != nil {
		t.Fatal(e)
	}
	r, e := c.Get(s.URL)
	if e != nil {
		t.Fatal(e)
	}
	r.Body.Close()
	if r.StatusCode != 302 {
		t.Fatal(r.StatusCode)
	}
	if _, e = c.Get("http://169.254.169.254/"); e == nil {
		t.Fatal("metadata accepted")
	}
	p, _ := New(Config{AllowedOrigins: []string{"https://example.com"}, AllowPrivateCIDRs: []string{"0.0.0.0/0", "::/0"}})
	for _, s := range []string{"169.254.169.254", "100.100.100.200", "168.63.129.16", "fd00:ec2::254", "fd20:ce::254", "127.0.0.1"} {
		if p.allowed(netip.MustParseAddr(s)) {
			t.Fatal(s)
		}
	}
}

type fakeDNS struct{}

func (fakeDNS) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
}
func TestRebindingIsValidatedAtDial(t *testing.T) {
	p, e := New(Config{AllowedOrigins: []string{"https://example.com"}, Resolver: fakeDNS{}})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.DialContext(context.Background(), "tcp", "example.com:443"); e == nil {
		t.Fatal("DNS rebinding accepted")
	}
}

type stubDNS []string

func (s stubDNS) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	var out []netip.Addr
	for _, a := range s {
		out = append(out, netip.MustParseAddr(a))
	}
	return out, nil
}

// A plain-http origin allowed by allow_loopback is dialed only when every
// address its name resolves to is loopback.
func TestPlainHTTPOriginNeedsAllLoopbackAnswers(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer s.Close()
	_, port, _ := net.SplitHostPort(s.Listener.Addr().String())
	origin := "http://localhost:" + port
	dial := func(answers ...string) error {
		p, e := New(Config{AllowedOrigins: []string{origin}, AllowLoopback: true, Resolver: stubDNS(answers)})
		if e != nil {
			t.Fatal(e)
		}
		c, e := p.DialContext(context.Background(), "tcp", "localhost:"+port)
		if e == nil {
			c.Close()
		}
		return e
	}
	if err := dial("127.0.0.1"); err != nil {
		t.Fatalf("all loopback refused: %v", err)
	}
	if err := dial("127.0.0.1", "::1"); err != nil {
		t.Fatalf("loopback pair refused: %v", err)
	}
	for name, answers := range map[string][]string{
		"one public among loopback": {"127.0.0.1", "8.8.8.8"},
		"public first":              {"8.8.8.8", "127.0.0.1"},
		"only public":               {"8.8.8.8"},
		"private":                   {"10.0.0.5"},
	} {
		if err := dial(answers...); err == nil {
			t.Errorf("%s: dialed", name)
		}
	}
}
