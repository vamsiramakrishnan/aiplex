package endpointpolicy

import (
	"context"
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
