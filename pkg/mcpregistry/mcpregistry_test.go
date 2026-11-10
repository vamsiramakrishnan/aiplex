package mcpregistry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// registry serves the recorded answers the way the official registry does.
func registry(t *testing.T) *httptest.Server {
	t.Helper()
	routes := map[string]string{
		"/v0.1/servers/ai.adside/adside/versions/latest":     "latest_adside.json",
		"/v0.1/servers/ai.adside/adside/versions/1.0.0":      "latest_adside.json",
		"/v0.1/servers/ai.alphabazaar/arena/versions/1.0.1":  "server_arena.json",
		"/v0.1/servers/ai.biel/biel-ai/versions/2.0.0":       "server_biel.json",
		"/v0.1/servers/ai.2kw/mcp-server/versions/8.0.3":     "server_2kw_package.json",
		"/v0.1/servers/ai.aetherya/mcp/versions/1.0.5":       "server_aetherya_mixed.json",
		"/v0.1/servers/ai.duvera/gateway/versions/1.0.0":     "server_duvera.json",
		"/v0.1/servers/ai.alphabazaar/arena/versions/latest": "server_arena.json",
		"/v0.1/servers/ai.adside/wrong-name/versions/latest": "server_arena.json",
		"/v0.1/servers/ai.adside/adside/versions/9.9.9":      "latest_adside.json",
		"/v0.1/servers/ai.biel/biel-ai/versions/latest":      "server_biel.json",
		"/v0.1/servers/ai.duvera/gateway/versions/latest":    "server_duvera.json",
		"/v0.1/servers/ai.aetherya/mcp/versions/latest":      "server_aetherya_mixed.json",
		"/v0.1/servers/ai.2kw/mcp-server/versions/latest":    "server_2kw_package.json",
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v0.1/servers", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "search_adside.json"))
	})
	mux.HandleFunc("/v0.1/servers/", func(w http.ResponseWriter, r *http.Request) {
		file, ok := routes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write(fixture(t, file))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func client(t *testing.T, base string) *Client {
	t.Helper()
	c, err := NewClient(base, nil)
	if err != nil {
		t.Fatal(err)
	}
	c.now = func() time.Time { return time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC) }
	return c
}

func TestSearchAndGetRecordedAnswers(t *testing.T) {
	srv := registry(t)
	c := client(t, srv.URL)
	page, skipped, err := c.Search(context.Background(), "adside", 3, "")
	if err != nil || skipped != 0 || len(page.Entries) != 1 {
		t.Fatalf("%v %d %+v", err, skipped, page)
	}
	e := page.Entries[0]
	if e.Server.Name != "ai.adside/adside" || e.Meta.Status != "active" || !e.Meta.IsLatest || len(e.Server.Classify().Remotes) != 1 {
		t.Fatalf("%+v", e)
	}
	got, err := c.Get(context.Background(), "ai.adside/adside", "")
	if err != nil {
		t.Fatal(err)
	}
	prov := got.Provenance(srv.URL + "/")
	sum := sha256.Sum256(got.Raw)
	if prov.SHA256 != hex.EncodeToString(sum[:]) || prov.Registry != srv.URL || prov.Version != "1.0.0" || prov.RetrievedAt != "2026-11-10T12:00:00Z" || prov.Validate() != nil {
		t.Fatalf("%+v", prov)
	}
	if _, err := c.Get(context.Background(), "ai.adside/adside", "1.0.0"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get(context.Background(), "ai.adside/nothing", ""); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("%v", err)
	}
}

func TestGetRefusesWrongIdentityRedirectsAndBadContent(t *testing.T) {
	srv := registry(t)
	c := client(t, srv.URL)
	// The registry answered a different server than asked.
	if _, err := c.Get(context.Background(), "ai.adside/wrong-name", ""); err == nil || !strings.Contains(err.Error(), "answered") {
		t.Fatalf("name mismatch accepted: %v", err)
	}
	if _, err := c.Get(context.Background(), "ai.adside/adside", "9.9.9"); err == nil || !strings.Contains(err.Error(), "version") {
		t.Fatalf("version mismatch accepted: %v", err)
	}
	for _, bad := range []string{"no-slash", "a/b/c", "ai.x/ y", ""} {
		if _, err := c.Get(context.Background(), bad, ""); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if _, err := c.Get(context.Background(), "ai.adside/adside", "^1.0.0"); err == nil {
		t.Error("version range accepted")
	}

	hit := false
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit = true }))
	defer elsewhere.Close()
	redirecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/v0.1/servers", http.StatusFound)
	}))
	defer redirecting.Close()
	if _, _, err := client(t, redirecting.URL).Search(context.Background(), "x", 1, ""); err == nil || !strings.Contains(err.Error(), "redirect") || hit {
		t.Fatalf("redirect followed or accepted: %v hit=%v", err, hit)
	}
	html := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>"))
	}))
	defer html.Close()
	if _, _, err := client(t, html.URL).Search(context.Background(), "x", 1, ""); err == nil {
		t.Fatal("non-JSON accepted")
	}
	huge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"server":{"name":"` + strings.Repeat("a", 2<<20) + `"}}`))
	}))
	defer huge.Close()
	if _, err := client(t, huge.URL).Get(context.Background(), "ai.adside/adside", ""); err == nil {
		t.Fatal("oversize answer accepted")
	}
}

func TestValidateBase(t *testing.T) {
	for _, ok := range []string{"https://registry.example", "https://registry.example/mcp/", "http://127.0.0.1:8080", "http://localhost:9"} {
		if _, err := ValidateBase(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"http://registry.example", "ftp://x", "https://u:p@registry.example", "https://registry.example?x=1", "https://registry.example#f", "registry.example", "https://"} {
		if _, err := ValidateBase(bad); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
}

func TestServerValidation(t *testing.T) {
	var base Server
	if err := json.Unmarshal(fixture(t, "latest_adside.json"), &struct{ Server *Server }{&base}); err != nil {
		t.Fatal(err)
	}
	if err := base.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Server){
		"unknown schema date": func(s *Server) {
			s.Schema = "https://static.modelcontextprotocol.io/schemas/2031-01-01/server.schema.json"
		},
		"foreign schema":  func(s *Server) { s.Schema = "https://evil.example/schemas/2025-12-11/server.schema.json" },
		"bad name":        func(s *Server) { s.Name = "no-namespace" },
		"range version":   func(s *Server) { s.Version = ">=1.0" },
		"escape in text":  func(s *Server) { s.Description = "hi \x1b[31mred" },
		"bad header name": func(s *Server) { s.Remotes[0].Headers[0].Name = "X Bad" },
		"url with space":  func(s *Server) { s.Remotes[0].URL = "https://a.example/ b" },
		"too many remotes": func(s *Server) {
			s.Remotes = make([]Remote, maxRemotes+1)
			for i := range s.Remotes {
				s.Remotes[i] = Remote{Type: "streamable-http", URL: "https://a.example/mcp"}
			}
		},
	} {
		s := base
		s.Remotes = append([]Remote(nil), base.Remotes...)
		s.Remotes[0].Headers = append([]Header(nil), base.Remotes[0].Headers...)
		mutate(&s)
		if err := s.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if got := Safe("a\x1b[31mb‮c", 50); strings.ContainsAny(got, "\x1b‮") {
		t.Fatalf("Safe left control characters: %q", got)
	}
}

func get(t *testing.T, c *Client, name, version string) Entry {
	t.Helper()
	e, err := c.Get(context.Background(), name, version)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestPlanMapsDeclaredCredentialsToReferences(t *testing.T) {
	c := client(t, registry(t).URL)
	t.Run("secret header without a value", func(t *testing.T) {
		p, err := NewPlan(get(t, c, "ai.adside/adside", ""), PlanOptions{Remote: -1, ConnectionID: "adside"})
		if err != nil {
			t.Fatal(err)
		}
		if p.URL != "https://api.adside.ai/mcp" || p.Origin != "https://api.adside.ai" || p.Auth.Type != "bearer" || p.Auth.SecretRef != "adside-token" || len(p.Secrets) != 1 || len(p.Notes) == 0 {
			t.Fatalf("%+v", p)
		}
		raw, _ := json.Marshal(p.Auth)
		if strings.Contains(string(raw), "sk_live") {
			t.Fatalf("auth block carries registry example text as a value: %s", raw)
		}
	})
	t.Run("Bearer template with a secret variable", func(t *testing.T) {
		p, err := NewPlan(get(t, c, "ai.alphabazaar/arena", "1.0.1"), PlanOptions{Remote: -1, ConnectionID: "arena"})
		if err != nil || p.Auth.Type != "bearer" || len(p.Notes) != 0 || p.Secrets[0].Description != "Your Alpha Bazaar API key" {
			t.Fatalf("%+v %v", p, err)
		}
	})
	t.Run("secret header with an undeclared template variable", func(t *testing.T) {
		p, err := NewPlan(get(t, c, "ai.duvera/gateway", ""), PlanOptions{Remote: -1, ConnectionID: "duvera"})
		if err != nil || p.Auth.Type != "bearer" || len(p.Secrets) != 1 {
			t.Fatalf("%+v %v", p, err)
		}
	})
	t.Run("operator says no auth", func(t *testing.T) {
		p, err := NewPlan(get(t, c, "ai.adside/adside", ""), PlanOptions{Remote: -1, ConnectionID: "adside", NoAuth: true})
		if err != nil || p.Auth.Type != "none" || len(p.Secrets) != 0 || len(p.Notes) == 0 {
			t.Fatalf("%+v %v", p, err)
		}
	})
	t.Run("package-only server", func(t *testing.T) {
		_, err := NewPlan(get(t, c, "ai.2kw/mcp-server", ""), PlanOptions{Remote: -1})
		if err == nil || !strings.Contains(err.Error(), "local package") {
			t.Fatalf("%v", err)
		}
	})
	t.Run("package and remote: the remote is imported, the package is not", func(t *testing.T) {
		e := get(t, c, "ai.aetherya/mcp", "")
		if cl := e.Server.Classify(); len(cl.Remotes) != 1 || cl.Packages != 1 {
			t.Fatalf("%+v", cl)
		}
		p, err := NewPlan(e, PlanOptions{Remote: -1, ConnectionID: "aeth"})
		if err != nil || p.URL != "https://mcp.aetherya.ai/mcp" || p.Auth.Type != "bearer" {
			t.Fatalf("%+v %v", p, err)
		}
	})
	t.Run("sse remote is not importable and variables are required", func(t *testing.T) {
		e := get(t, c, "ai.biel/biel-ai", "")
		if _, err := NewPlan(e, PlanOptions{Remote: 1}); err == nil {
			t.Fatal("sse remote planned")
		}
		_, err := NewPlan(e, PlanOptions{Remote: -1})
		var missing *MissingVariablesError
		if err == nil || !asMissing(err, &missing) || missing.Names[0] != "project_slug" {
			t.Fatalf("%v", err)
		}
		p, err := NewPlan(e, PlanOptions{Remote: -1, Vars: map[string]string{"project_slug": "my-docs"}})
		if err != nil || p.URL != "https://mcp.biel.ai/v2/my-docs/mcp" || p.Auth.Type != "none" || p.Variables[0].Source != "flag" {
			t.Fatalf("%+v %v", p, err)
		}
		for _, bad := range []string{"../admin", "a/b", "..", "x?y"} {
			if got, err := NewPlan(e, PlanOptions{Remote: -1, Vars: map[string]string{"project_slug": bad}}); err == nil && strings.Contains(got.URL, "/../") {
				t.Errorf("%q escaped the path: %s", bad, got.URL)
			}
		}
		if _, err := NewPlan(e, PlanOptions{Remote: -1, Vars: map[string]string{"project_slug": "x", "nope": "y"}}); err == nil {
			t.Fatal("--var for an unknown variable accepted")
		}
	})
}

func asMissing(err error, target **MissingVariablesError) bool {
	m, ok := err.(*MissingVariablesError)
	if ok {
		*target = m
	}
	return ok
}

func remote(url string, headers ...Header) Entry {
	s := Server{Schema: "https://static.modelcontextprotocol.io/schemas/2025-12-11/server.schema.json", Name: "x.example/s", Description: "d", Version: "1", Remotes: []Remote{{Type: "streamable-http", URL: url, Headers: headers}}}
	return Entry{Server: s}
}

func TestPlanHeaderMappingRules(t *testing.T) {
	secret := Input{IsSecret: true, IsRequired: true}
	cases := []struct {
		name     string
		url      string
		headers  []Header
		wantType string
		wantName string
		prefix   string
		unmapped int
		err      string
	}{
		{"api key header", "https://a.example/mcp", []Header{{Name: "X-Api-Key", Input: secret}}, "api_key", "X-Api-Key", "", 0, ""},
		{"api key with scheme", "https://a.example/mcp", []Header{{Name: "Authorization", Input: Input{Value: "Token {k}", IsSecret: true}, Variables: map[string]Input{"k": {IsSecret: true}}}}, "api_key", "Authorization", "Token", 0, ""},
		{"static header is not imported", "https://a.example/mcp", []Header{{Name: "X-Static", Input: Input{Value: "literal-secret-looking"}}}, "none", "", "", 1, ""},
		{"literal secret value is never imported", "https://a.example/mcp", []Header{{Name: "Authorization", Input: Input{Value: "Bearer abc123", IsSecret: true}}}, "none", "", "", 1, ""},
		{"non-secret user header", "https://a.example/mcp", []Header{{Name: "X-Tenant"}}, "none", "", "", 1, ""},
		{"two credentials", "https://a.example/mcp", []Header{{Name: "X-One", Input: secret}, {Name: "X-Two", Input: Input{IsSecret: true}}}, "api_key", "X-One", "", 1, ""},
		{"text after the secret", "https://a.example/mcp", []Header{{Name: "X-K", Input: Input{Value: "{k}-suffix", IsSecret: true}}}, "none", "", "", 1, ""},
		{"secret in the url path", "https://a.example/{k}/mcp", nil, "", "", "", 0, "query parameter"},
		{"plain http to a public host", "http://a.example/mcp", nil, "", "", "", 0, "plain http"},
		{"credentials in the url", "https://u:p@a.example/mcp", nil, "", "", "", 0, "credentials"},
		{"fixed query is not pinned", "https://a.example/mcp?x=1", nil, "", "", "", 0, "query"},
		{"loopback http", "http://127.0.0.1:9000/mcp", nil, "none", "", "", 0, ""},
	}
	for _, c := range cases {
		e := remote(c.url, c.headers...)
		if strings.Contains(c.url, "{k}") {
			e.Server.Remotes[0].Variables = map[string]Input{"k": {IsSecret: true}}
		}
		p, err := NewPlan(e, PlanOptions{Remote: -1, ConnectionID: "c"})
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%s: %v", c.name, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if p.Auth.Type != c.wantType || p.Auth.Name != c.wantName || p.Auth.Prefix != c.prefix || len(p.Unmapped) != c.unmapped {
			t.Errorf("%s: %+v unmapped=%v", c.name, p.Auth, p.Unmapped)
		}
	}
}

func TestSecretQueryParameterBecomesAPIKey(t *testing.T) {
	e := remote("https://a.example/mcp?key={api_key}")
	e.Server.Remotes[0].Variables = map[string]Input{"api_key": {IsSecret: true, IsRequired: true, Description: "the key"}}
	p, err := NewPlan(e, PlanOptions{Remote: -1, ConnectionID: "q"})
	if err != nil || p.URL != "https://a.example/mcp" || p.Auth.Type != "api_key" || p.Auth.In != "query" || p.Auth.Name != "key" || p.Auth.SecretRef != "q-api_key" {
		t.Fatalf("%+v %v", p, err)
	}
	if _, err := NewPlan(e, PlanOptions{Remote: -1, NoAuth: true}); err == nil {
		t.Fatal("--auth none accepted for a URL that needs a secret")
	}
}

func TestHostVariablesAreConstrained(t *testing.T) {
	e := remote("https://{region}.a.example/mcp")
	e.Server.Remotes[0].Variables = map[string]Input{"region": {IsRequired: true, Choices: []string{"us", "eu"}}}
	p, err := NewPlan(e, PlanOptions{Remote: -1, Vars: map[string]string{"region": "eu"}})
	if err != nil || p.URL != "https://eu.a.example/mcp" || p.Origin != "https://eu.a.example" {
		t.Fatalf("%+v %v", p, err)
	}
	if _, err := NewPlan(e, PlanOptions{Remote: -1, Vars: map[string]string{"region": "evil.example/x"}}); err == nil {
		t.Fatal("value outside the choices accepted")
	}
	e.Server.Remotes[0].Variables["region"] = Input{IsRequired: true}
	for _, bad := range []string{"evil.example/", "a@b", "x y", "", "a..b"} {
		if _, err := NewPlan(e, PlanOptions{Remote: -1, Vars: map[string]string{"region": bad}}); err == nil {
			t.Errorf("host variable %q accepted", bad)
		}
	}
}

func TestProvenanceValidate(t *testing.T) {
	good := Provenance{Registry: "https://registry.modelcontextprotocol.io", Name: "ai.adside/adside", Version: "1.0.0", RetrievedAt: "2026-11-10T00:00:00Z", SHA256: strings.Repeat("a", 64)}
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Provenance){
		"registry http": func(p *Provenance) { p.Registry = "http://registry.example" },
		"name":          func(p *Provenance) { p.Name = "x" },
		"version":       func(p *Provenance) { p.Version = "" },
		"time":          func(p *Provenance) { p.RetrievedAt = "yesterday" },
		"sha":           func(p *Provenance) { p.SHA256 = "ABC" },
	} {
		p := good
		mutate(&p)
		if p.Validate() == nil {
			t.Errorf("%s accepted", name)
		}
	}
}
