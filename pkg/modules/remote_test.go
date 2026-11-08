package modules

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRemoteStandaloneModuleLifecycle(t *testing.T) {
	d := descriptor("external-meter")
	d.Capabilities = []string{"usage.meter"}
	ready := true
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/module/v1/descriptor":
			_ = json.NewEncoder(w).Encode(d)
		case "/readyz":
			if !ready {
				w.WriteHeader(http.StatusServiceUnavailable)
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	m, err := NewRemote(RemoteConfig{Descriptor: d, Endpoint: server.URL, Transport: server.Client().Transport})
	if err != nil {
		t.Fatal(err)
	}
	r := NewRegistry()
	register(t, r, m)
	runtime := NewRuntime(r)
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	ready = false
	if err := runtime.Ready(context.Background()); err == nil {
		t.Fatal("unready external module accepted")
	}
	if err := runtime.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := m.Ready(context.Background()); err == nil {
		t.Fatal("stopped remote module ready")
	}
}

func TestRemoteTrustBoundary(t *testing.T) {
	for _, endpoint := range []string{"http://example.com", "https://user:secret@example.com", "https://example.com/private", "https://example.com?redirect=x", "file:///etc/passwd", "https://example.com/#fragment"} {
		if _, err := NewRemote(RemoteConfig{Descriptor: descriptor("external"), Endpoint: endpoint, AllowInsecureLocalhost: true}); err == nil {
			t.Fatalf("accepted endpoint %s", endpoint)
		}
	}
	if _, err := NewRemote(RemoteConfig{Descriptor: descriptor("external"), Endpoint: "http://127.0.0.1:8080"}); err == nil {
		t.Fatal("HTTP enabled without opt-in")
	}
	if _, err := NewRemote(RemoteConfig{Descriptor: descriptor("external"), Endpoint: "http://127.0.0.1:8080", AllowInsecureLocalhost: true}); err != nil {
		t.Fatal(err)
	}
	for _, timeout := range []time.Duration{-time.Second, 31 * time.Second} {
		if _, err := NewRemote(RemoteConfig{Descriptor: descriptor("external"), Endpoint: "https://example.com", Timeout: timeout}); err == nil {
			t.Fatal("unbounded timeout accepted")
		}
	}
}

func TestRemoteRejectsRedirectMismatchAndOversize(t *testing.T) {
	for _, mode := range []string{"redirect", "mismatch", "oversize", "trailing"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch mode {
				case "redirect":
					http.Redirect(w, r, "http://169.254.169.254/", http.StatusFound)
				case "mismatch":
					_ = json.NewEncoder(w).Encode(descriptor("different"))
				case "oversize":
					_, _ = w.Write([]byte(strings.Repeat(" ", 1<<20) + "{}"))
				case "trailing":
					_ = json.NewEncoder(w).Encode(descriptor("external"))
					_, _ = w.Write([]byte("{}"))
				}
			}))
			defer server.Close()
			m, err := NewRemote(RemoteConfig{Descriptor: descriptor("external"), Endpoint: server.URL, AllowInsecureLocalhost: true})
			if err != nil {
				t.Fatal(err)
			}
			if err := m.Start(context.Background()); err == nil {
				t.Fatalf("accepted %s", mode)
			}
		})
	}
}

func TestRemoteHonorsProbeDeadline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer server.Close()
	m, err := NewRemote(RemoteConfig{Descriptor: descriptor("external"), Endpoint: server.URL, AllowInsecureLocalhost: true, Timeout: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Start(context.Background()); err == nil {
		t.Fatal("probe timeout ignored")
	}
}
