package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

type catalogAPI struct {
	mu       sync.Mutex
	existing string // JSON array body for GET /api/v1/usage/prices/
	puts     map[string]json.RawMessage
	ifMatch  map[string]string
}

func newCatalogAPI(existing string) (*catalogAPI, *httptest.Server) {
	api := &catalogAPI{existing: existing, puts: map[string]json.RawMessage{}, ifMatch: map[string]string{}}
	return api, httptest.NewServer(api)
}

func (a *catalogAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/usage/prices/":
		w.Write([]byte(a.existing))
	case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/api/v1/usage/prices/"):
		body, _ := io.ReadAll(r.Body)
		id := strings.TrimPrefix(r.URL.Path, "/api/v1/usage/prices/")
		a.puts[id] = body
		a.ifMatch[id] = r.Header.Get("If-Match")
		w.Write([]byte(`{"id":"` + id + `","revision":7,"spec":` + string(body) + `}`))
	case r.URL.Path == "/api/v1/access-points/team":
		w.Write([]byte(`{"id":"team","revision":1,"spec":{"id":"team","caller_kinds":["user"],"subjects":{},"models":[
 {"name":"fast","surface_ref":"oai","native_model":"gpt-4o-mini"},
 {"name":"claude","surface_ref":"vtx","native_model":"publishers/anthropic/models/claude-haiku-4-5@20251001"},
 {"name":"mine","surface_ref":"compat","native_model":"my-local-model"},
 {"name":"odd","surface_ref":"oai","native_model":"gpt-not-in-catalogue"}]}}`))
	case r.URL.Path == "/api/v1/llm/surfaces/oai":
		w.Write([]byte(`{"id":"oai","revision":1,"spec":{"surface":{"id":"oai","protocol":"openai-chat","origin":"https://api.openai.com","provider":{"id":"openai"}}}}`))
	case r.URL.Path == "/api/v1/llm/surfaces/compat":
		w.Write([]byte(`{"id":"compat","revision":1,"spec":{"surface":{"id":"compat","protocol":"openai-chat","origin":"https://example.test","provider":{"id":"openai-compatible"}}}}`))
	case r.URL.Path == "/api/v1/llm/surfaces/vtx":
		w.Write([]byte(`{"id":"vtx","revision":1,"spec":{"surface":{"id":"vtx","protocol":"anthropic-messages","origin":"https://x","project":"p","location":"us-east5","publisher":"anthropic"}}}`))
	default:
		http.NotFound(w, r)
	}
}

func TestCatalogSearchAndShow(t *testing.T) {
	_, server := newCatalogAPI("[]")
	defer server.Close()
	out, err := runUsage(t, server, "table", "prices", "catalog", "search", "gpt-4o-mini", "--provider", "openai")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"openai/gpt-4o-mini", "0.15", "0.6", "third-party snapshot", "litellm snapshot 2026-11-10"} {
		if !strings.Contains(out, want) {
			t.Errorf("search output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "azure-openai/") {
		t.Errorf("provider filter leaked:\n%s", out)
	}
	out, err = runUsage(t, server, "table", "prices", "catalog", "show", "openai/gpt-4o-mini")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"input_tokens", "USD 0.15 per 1M tokens", "3/20", "cache_read_tokens", "USD 0.075", "retrieved from https://raw.githubusercontent.com/BerriAI/litellm/", "catalog import openai/gpt-4o-mini"} {
		if !strings.Contains(out, want) {
			t.Errorf("show output lacks %q:\n%s", want, out)
		}
	}
	if _, err := runUsage(t, server, "table", "prices", "catalog", "show", "openai/gpt-4o-min"); err == nil {
		t.Fatal("a near miss must not resolve: no fuzzy matching")
	}
	if _, err := runUsage(t, server, "table", "prices", "catalog", "show", "gpt-4o-mini"); err == nil {
		t.Fatal("an id without a provider must be refused")
	}
}

func TestCatalogImportDryRunWritesNothing(t *testing.T) {
	api, server := newCatalogAPI("[]")
	defer server.Close()
	out, err := runUsage(t, server, "table", "prices", "catalog", "import", "openai/gpt-4o-mini", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	t.Log("\n" + out)
	if len(api.puts) != 0 || !strings.Contains(out, "will create") || !strings.Contains(out, "Dry run: nothing was written") || !strings.Contains(out, "USD 0.15 per 1M tokens") {
		t.Fatalf("puts=%v\n%s", api.puts, out)
	}
}

func TestCatalogImportWritesPriceWithProvenance(t *testing.T) {
	api, server := newCatalogAPI("[]")
	defer server.Close()
	if _, err := runUsage(t, server, "table", "prices", "catalog", "import", "openai/gpt-4o-mini"); err == nil {
		t.Fatal("writing without a terminal needs --yes")
	}
	if len(api.puts) != 0 {
		t.Fatal("nothing may be written without confirmation")
	}
	out, err := runUsage(t, server, "table", "prices", "catalog", "import", "openai/gpt-4o-mini", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "created   price openai-gpt-4o-mini (revision 7)") {
		t.Fatalf("output:\n%s", out)
	}
	if api.ifMatch["openai-gpt-4o-mini"] != "0" {
		t.Fatalf("If-Match = %q, a new price must be created at revision 0", api.ifMatch["openai-gpt-4o-mini"])
	}
	var p usageledger.Price
	if err := json.Unmarshal(api.puts["openai-gpt-4o-mini"], &p); err != nil {
		t.Fatal(err)
	}
	if p.Provider != "openai" || p.ModelID != "gpt-4o-mini" || p.Currency != "USD" || p.Scale != 6 {
		t.Fatalf("price = %+v", p)
	}
	if p.Meters[usageledger.MeterInput] != (usageledger.Rational{Numerator: 3, Denominator: 20}) || p.Meters[usageledger.MeterOutput] != (usageledger.Rational{Numerator: 3, Denominator: 5}) {
		t.Fatalf("meters = %+v", p.Meters)
	}
	if _, set := p.Meters[usageledger.MeterReasoning]; set {
		t.Fatal("reasoning must not be priced unless asked")
	}
	if p.Source == nil || p.Source.Catalog != "litellm" || len(p.Source.Commit) != 40 || p.Source.RetrievedAt != "2026-11-10" || !strings.HasPrefix(p.Source.SourceURL, "https://") {
		t.Fatalf("provenance = %+v", p.Source)
	}
	if _, ok := api.puts["openai-gpt-4o-mini-batch"]; !ok {
		t.Fatal("the catalogue states batch rates: a batch price is expected")
	}
}

func TestCatalogImportNeverOverwritesWithoutReplace(t *testing.T) {
	existing := `[{"id":"openai-gpt-4o-mini","revision":4,"spec":{"id":"openai-gpt-4o-mini","model_id":"gpt-4o-mini","provider":"openai","currency":"USD","scale":6,"meters":{"input_tokens":{"numerator":9,"denominator":1}}}},
 {"id":"mine","revision":2,"spec":{"id":"mine","model_id":"gpt-4o-mini","provider":"openai","batch":true,"currency":"USD","scale":6,"meters":{"input_tokens":{"numerator":1,"denominator":1}}}}]`
	api, server := newCatalogAPI(existing)
	defer server.Close()
	out, err := runUsage(t, server, "table", "prices", "catalog", "import", "openai/gpt-4o-mini", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	if len(api.puts) != 0 || !strings.Contains(out, "skipped: exists, use --replace") || !strings.Contains(out, `price "mine" already covers openai/gpt-4o-mini`) {
		t.Fatalf("puts=%v\n%s", api.puts, out)
	}
	if _, err := runUsage(t, server, "table", "prices", "catalog", "import", "openai/gpt-4o-mini", "--yes", "--replace"); err != nil {
		t.Fatal(err)
	}
	if api.ifMatch["openai-gpt-4o-mini"] != "4" {
		t.Fatalf("replace must send the current revision, got %q", api.ifMatch["openai-gpt-4o-mini"])
	}
	if _, conflicting := api.puts["openai-gpt-4o-mini-batch"]; conflicting {
		t.Fatal("a batch price must not be added beside another price that already covers it")
	}
}

func TestCatalogImportRefusesPartialPricingWithoutAcceptance(t *testing.T) {
	api, server := newCatalogAPI("[]")
	defer server.Close()
	_, err := runUsage(t, server, "table", "prices", "catalog", "import", "gemini-developer/gemini-2.5-flash", "--yes")
	if err == nil || !strings.Contains(err.Error(), "cannot express") {
		t.Fatalf("err = %v", err)
	}
	if len(api.puts) != 0 {
		t.Fatal("a refused import must write nothing")
	}
	out, err := runUsage(t, server, "table", "prices", "catalog", "import", "gemini-developer/gemini-2.5-flash", "--yes", "--accept-partial")
	if err != nil {
		t.Fatal(err)
	}
	if len(api.puts) == 0 || !strings.Contains(out, "NOT EXPRESSIBLE") || !strings.Contains(out, "input_cost_per_audio_token") {
		t.Fatalf("puts=%v\n%s", api.puts, out)
	}
}

func TestCatalogImportReasoningIsExplicit(t *testing.T) {
	api, server := newCatalogAPI("[]")
	defer server.Close()
	if _, err := runUsage(t, server, "table", "prices", "catalog", "import", "anthropic/claude-haiku-4-5", "--yes", "--reasoning-included"); err != nil {
		t.Fatal(err)
	}
	var p usageledger.Price
	if err := json.Unmarshal(api.puts["anthropic-claude-haiku-4-5"], &p); err != nil {
		t.Fatal(err)
	}
	if r, set := p.Meters[usageledger.MeterReasoning]; !set || r.Numerator != 0 {
		t.Fatalf("meters = %+v", p.Meters)
	}
}

func TestCatalogImportRejectsOtherCurrencies(t *testing.T) {
	_, server := newCatalogAPI("[]")
	defer server.Close()
	if _, err := runUsage(t, server, "table", "prices", "catalog", "import", "openai/gpt-4o-mini", "--currency", "EUR", "--dry-run"); err == nil {
		t.Fatal("the catalogue is USD only")
	}
}

func TestCatalogImportAllForAccessPoint(t *testing.T) {
	api, server := newCatalogAPI("[]")
	defer server.Close()
	out, err := runUsage(t, server, "table", "prices", "catalog", "import", "--all-for-access-point", "team", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	t.Log("\n" + out)
	if _, ok := api.puts["openai-gpt-4o-mini"]; !ok {
		t.Fatalf("gpt-4o-mini must be imported:\n%s", out)
	}
	if _, ok := api.puts["vertex-anthropic-claude-haiku-4-5-20251001"]; !ok {
		t.Fatalf("the Vertex Claude model must resolve to the vertex-anthropic entry:\n%s", out)
	}
	for _, want := range []string{"Not in the catalogue (left unknown)", "my-local-model", "OpenAI-compatible", "gpt-not-in-catalogue"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	var p usageledger.Price
	if err := json.Unmarshal(api.puts["vertex-anthropic-claude-haiku-4-5-20251001"], &p); err != nil {
		t.Fatal(err)
	}
	// Claude on Vertex is recorded under the anthropic publisher.
	if p.Provider != "anthropic" || p.ModelID != "claude-haiku-4-5@20251001" {
		t.Fatalf("price = %+v", p)
	}
}

func TestSurfaceCatalogIdentity(t *testing.T) {
	for _, c := range []struct {
		surface        aiplex.ModelSurface
		native         string
		wantP, wantM   string
		wantReasonPart string
	}{
		{aiplex.ModelSurface{Provider: &aiplex.ModelProvider{ID: "bedrock"}}, "anthropic.claude-sonnet-4-6", "bedrock", "anthropic.claude-sonnet-4-6", ""},
		{aiplex.ModelSurface{Provider: &aiplex.ModelProvider{ID: "gemini-developer"}}, "gemini-2.5-flash", "gemini-developer", "gemini-2.5-flash", ""},
		{aiplex.ModelSurface{Publisher: "google", Project: "p", Location: "us-central1"}, "projects/p/locations/us-central1/publishers/google/models/gemini-2.5-flash", "google", "gemini-2.5-flash", ""},
		{aiplex.ModelSurface{Publisher: "google"}, "models/gemini-2.5-flash", "google", "gemini-2.5-flash", ""},
		{aiplex.ModelSurface{Provider: &aiplex.ModelProvider{ID: "openai-compatible"}}, "x", "", "", "OpenAI-compatible"},
		{aiplex.ModelSurface{Publisher: "meta"}, "llama", "", "", "no catalogue"},
	} {
		p, m, reason := surfaceCatalogIdentity(c.surface, c.native)
		if p != c.wantP || m != c.wantM || !strings.Contains(reason, c.wantReasonPart) {
			t.Errorf("%+v %s = %q %q %q", c.surface, c.native, p, m, reason)
		}
	}
}

func TestCatalogHintLine(t *testing.T) {
	_, server := newCatalogAPI("[]")
	defer server.Close()
	oldURL, oldToken := apiURL, token
	apiURL, token = server.URL, "session"
	defer func() { apiURL, token = oldURL, oldToken }()
	client := newClient()
	line := usageCatalogHintLine(context.Background(), client, []catalogHintTarget{{provider: "openai", model: "gpt-4o-mini"}, {provider: "openai", model: "gpt-4o-mini"}})
	want := "aiplex usage prices catalog import openai/gpt-4o-mini"
	if !strings.Contains(line, want) || strings.Contains(line, "more") || strings.Count(line, "\n") != 0 {
		t.Fatalf("hint = %q", line)
	}
	for _, t2 := range []catalogHintTarget{{provider: "openai-compatible", model: "gpt-4o-mini"}, {provider: "openai", model: "no-such-model"}, {provider: "mcp"}} {
		if got := usageCatalogHintLine(context.Background(), client, []catalogHintTarget{t2}); got != "" {
			t.Errorf("%+v: unexpected hint %q", t2, got)
		}
	}
	// A model that already has a price of any kind gets no hint.
	api2, server2 := newCatalogAPI(`[{"id":"gpt-4o-mini","revision":1,"spec":{"id":"gpt-4o-mini","model_id":"gpt-4o-mini","currency":"USD","scale":6,"meters":{"input_tokens":{"numerator":1,"denominator":1}}}}]`)
	_ = api2
	defer server2.Close()
	apiURL = server2.URL
	if got := usageCatalogHintLine(context.Background(), newClient(), []catalogHintTarget{{provider: "openai", model: "gpt-4o-mini"}}); got != "" {
		t.Fatalf("priced model got hint %q", got)
	}
}

func TestCatalogHintMakesNoRequestOffATerminalOrForMachineOutput(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.Write([]byte("[]")) }))
	defer server.Close()
	oldURL, oldToken, oldOutput := apiURL, token, output
	apiURL, token = server.URL, "session"
	defer func() { apiURL, token, output = oldURL, oldToken, oldOutput }()
	target := []catalogHintTarget{{provider: "openai", model: "gpt-4o-mini"}}
	// The test process has no terminal on stderr, so nothing is looked up or printed.
	usageCatalogHint(context.Background(), target)
	output = "json"
	usageCatalogHint(context.Background(), target)
	if calls != 0 {
		t.Fatalf("hint made %d requests", calls)
	}
}

func TestDevPriceHint(t *testing.T) {
	if h := devPriceHint("openai", "gpt-4o-mini"); !strings.Contains(h, "aiplex usage prices catalog import openai/gpt-4o-mini") {
		t.Fatalf("hint = %q", h)
	}
	if h := devPriceHint("gemini", "gemini-2.5-flash"); !strings.Contains(h, "import gemini-developer/gemini-2.5-flash") {
		t.Fatalf("hint = %q", h)
	}
	if devPriceHint("openai", "mock-gpt") != "" || devPriceHint("mock", "gpt-4o-mini") != "" {
		t.Fatal("unlisted models and the mock provider get no hint")
	}
}
