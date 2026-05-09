package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/auth"
	"github.com/vamsiramakrishnan/aiplex/internal/capability"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
)

// TestUp_RouterServesCatalogAndDeploysCap verifies that the router built
// by `aiplex up` (a) requires a valid local-mode token, (b) returns the
// seeded catalog, and (c) deploys a memory namespace end-to-end including
// the in-process broker registration. The whole point of `aiplex up`:
// proving the Capability primitive is real on a single binary.
func TestUp_RouterServesCatalogAndDeploysCap(t *testing.T) {
	dir := t.TempDir()
	signer, err := auth.NewLocalSigner(filepath.Join(dir, "k"), "aiplex://local")
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	store := registry.NewMemoryStore()
	ctx := context.Background()

	seedCatalog(ctx, store)
	store.SetUserCaps(ctx, "alice@local", wildcardCapsForKinds())

	// Pick a free port so the in-process workflow invoker (which calls
	// back at the gateway URL) has somewhere real to land. The router
	// hangs off an httptest.Server so we don't actually need to listen.
	port := freePort(t)
	r, _, _ := buildLocalRouter(ctx, store, signer, port)
	srv := httptest.NewServer(r)
	defer srv.Close()

	// Mint a wildcard token for the test user.
	tok, err := signer.Mint("alice@local", wildcardCapsForKinds(), "", time.Hour)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}

	// 1. /healthz is public.
	resp, _ := http.Get(srv.URL + "/healthz")
	if resp.StatusCode != 200 {
		t.Errorf("healthz: status=%d", resp.StatusCode)
	}

	// 2. Protected endpoint without token = 401.
	resp, _ = http.Get(srv.URL + "/api/v1/catalog?kind=memory")
	if resp.StatusCode != 401 {
		t.Errorf("catalog without token: status=%d, want 401", resp.StatusCode)
	}

	// 3. With token: returns built-in memory templates.
	req, _ := http.NewRequest("GET", srv.URL+"/api/v1/catalog?kind=memory", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("catalog with token: status=%d body=%s", resp.StatusCode, body)
	}
	var page struct {
		Total int `json:"total"`
	}
	json.NewDecoder(resp.Body).Decode(&page)
	if page.Total < 1 {
		t.Errorf("expected built-in memory templates, got total=%d", page.Total)
	}

	// 4. Deploy a memory namespace via the unified API. The KindHook on the
	//    deploy engine should register it with the broker; a write through
	//    /cap/memory/.../_invoke should succeed.
	deployBody := `{"kind":"memory","template_id":"scratch","display_name":"Scratch"}`
	req, _ = http.NewRequest("POST", srv.URL+"/api/v1/instances", strings.NewReader(deployBody))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != 201 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("deploy memory: status=%d body=%s", resp.StatusCode, body)
	}
	var inst struct {
		Capabilities []map[string]any `json:"capabilities"`
	}
	json.NewDecoder(resp.Body).Decode(&inst)
	if len(inst.Capabilities) == 0 {
		t.Fatalf("deployed instance has no capabilities")
	}
	uri := inst.Capabilities[0]["uri"].(string)

	// 5. Write to the deployed namespace via the universal _invoke action.
	rest := strings.TrimPrefix(uri, "cap://memory/")
	writeBody := `{"action":"write","input":{"key":"k1","data":{"score":88}}}`
	req, _ = http.NewRequest("POST", srv.URL+"/cap/memory/"+rest+"/_invoke", strings.NewReader(writeBody))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("memory write: status=%d body=%s", resp.StatusCode, body)
	}

	// 6. Read it back.
	readBody := `{"action":"read","input":{"key":"k1"}}`
	req, _ = http.NewRequest("POST", srv.URL+"/cap/memory/"+rest+"/_invoke", strings.NewReader(readBody))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("memory read: status=%d body=%s", resp.StatusCode, body)
	}
	var got struct {
		Data map[string]any `json:"data"`
	}
	json.NewDecoder(resp.Body).Decode(&got)
	if v, ok := got.Data["score"].(float64); !ok || v != 88 {
		t.Errorf("read back data = %+v, want score=88", got.Data)
	}
}

func TestWildcardCapsForKinds_GrantsEveryKind(t *testing.T) {
	caps := wildcardCapsForKinds()
	if len(caps) == 0 {
		t.Fatal("no caps generated")
	}
	seen := map[capability.Kind]bool{}
	for _, c := range caps {
		u, err := capability.ParseURI(c.URI)
		if err != nil {
			t.Errorf("invalid wildcard URI %q: %v", c.URI, err)
			continue
		}
		seen[u.Kind] = true
	}
	for _, k := range capability.AllKinds() {
		if !seen[k] {
			t.Errorf("missing wildcard cap for kind %s", k)
		}
	}
}

// freePort returns an OS-assigned available port. We don't actually bind to
// it for serving (httptest does that), but the workflow invoker URL has to
// be a real-looking endpoint so the executor wires up cleanly.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}
