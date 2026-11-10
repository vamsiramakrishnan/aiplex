package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/internal/connections"
	"github.com/vamsiramakrishnan/aiplex/pkg/endpointpolicy"
	"github.com/vamsiramakrishnan/aiplex/pkg/modelapi"
	"github.com/vamsiramakrishnan/aiplex/pkg/toolcredential"
	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

var geminiKeyRef = &toolcredential.Auth{Type: toolcredential.APIKey, In: toolcredential.InHeader, Name: "x-goog-api-key", SecretRef: "provider-key"}

type utilityStep struct {
	op, method, path, query, body, response string
	// usage is the ledger usage that must be recorded (input, output, total);
	// nil means the ledger must hold no token usage for the exchange.
	usage map[string]int64
}

// TestUtilityOperationsSettleRequestCountThroughFacade runs every new metering
// kind through the managed path: request-count settlement of exactly one
// request, the provider's own usage (or none) in the ledger, the tenant
// credential in the documented header only, and no replay of an attempt.
func TestUtilityOperationsSettleRequestCountThroughFacade(t *testing.T) {
	const respObject = `{"id":"resp_1","model":"model-v1","status":"completed","usage":{"input_tokens":12,"output_tokens":9,"total_tokens":21}}`
	cases := []struct {
		name, protocol, header string
		settings               modelapi.Provider
		auth                   *toolcredential.Auth
		steps                  []utilityStep
	}{
		{"gemini-developer", "google-genai", "X-Goog-Api-Key", modelapi.Provider{ID: modelapi.ProviderGeminiDeveloper}, geminiKeyRef, []utilityStep{
			{"gemini.models.generateContent", "POST", "/v1beta/models/model-v1:generateContent", "key=caller-key", `{"contents":[{"parts":[{"text":"hi"}]}]}`, `{"candidates":[{"index":0,"finishReason":"STOP"}],"modelVersion":"model-v1","usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5,"totalTokenCount":15}}`, map[string]int64{"input": 10, "output": 5, "total": 15}},
			{"gemini.models.countTokens", "POST", "/v1beta/models/model-v1:countTokens", "", `{"contents":[{"parts":[{"text":"hi"}]}]}`, `{"totalTokens":1316}`, nil},
			{"gemini.models.embedContent", "POST", "/v1beta/models/model-v1:embedContent", "", `{"content":{"parts":[{"text":"hi"}]}}`, `{"embedding":{"values":[0.1]},"usageMetadata":{"promptTokenCount":18}}`, map[string]int64{"input": 18}},
			{"gemini.cachedContents.create", "POST", "/v1beta/cachedContents", "", `{"model":"models/model-v1","contents":[]}`, `{"name":"cachedContents/c1","usageMetadata":{"totalTokenCount":4096}}`, nil},
			{"gemini.cachedContents.get", "GET", "/v1beta/cachedContents/c1", "", ``, `{"name":"cachedContents/c1","usageMetadata":{"totalTokenCount":4096}}`, nil},
		}},
		{"openai", "openai-responses", "Authorization", modelapi.Provider{ID: modelapi.ProviderOpenAI}, bearerRef, []utilityStep{
			{"openai.v1.responses", "POST", "/v1/responses", "", `{"model":"model-v1","input":"hi"}`, respObject, map[string]int64{"input": 12, "output": 9, "total": 21}},
			// The same response, retrieved later: owner-only, request-count, usage never read.
			{"openai.v1.responses-retrieve", "GET", "/v1/responses/resp_1", "key=caller-key", ``, respObject, nil},
			{"openai.v1.responses-cancel", "POST", "/v1/responses/resp_1/cancel", "", ``, respObject, nil},
		}},
		{"openai-embeddings", "openai-embeddings", "Authorization", modelapi.Provider{ID: modelapi.ProviderOpenAI}, bearerRef, []utilityStep{
			{"openai.v1.embeddings", "POST", "/v1/embeddings", "", `{"model":"model-v1","input":"hi"}`, `{"object":"list","data":[],"model":"model-v1","usage":{"prompt_tokens":8,"total_tokens":8}}`, map[string]int64{"input": 8, "total": 8}},
		}},
		{"anthropic", "anthropic-messages", "X-Api-Key", modelapi.Provider{ID: modelapi.ProviderAnthropic, AnthropicVersion: "2023-06-01"}, anthropicRef, []utilityStep{
			{"anthropic.messages-count-tokens", "POST", "/v1/messages/count_tokens", "", `{"model":"model-v1","messages":[]}`, `{"input_tokens":403}`, nil},
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			var mu sync.Mutex
			var seen []*http.Request
			current := &test.steps[0]
			f := quotaHostModelNew(t, false, func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				seen = append(seen, r.Clone(r.Context()))
				response := current.response
				mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, response)
			}, func(cfg *QuotaHostConfig) {
				base := cfg.Bindings[0]
				cfg.Bindings = nil
				for i, step := range test.steps {
					binding := base
					binding.ID, binding.OperationID, binding.Provider = fmt.Sprintf("util-%d", i), step.op, test.settings.ID
					binding.BillingAccount, binding.Region, binding.ModelVersion, binding.Profile = "", "", "", quotaProfileRequests
					binding.Model = "model-v1"
					if strings.Contains(step.path, "/responses/") || strings.Contains(step.path, "cachedContents/") {
						binding.Model = "" // model-less: addressed by resource ID
					}
					cfg.Bindings = append(cfg.Bindings, binding)
				}
			})
			installProviderCredentials(t, f.handler)
			record, _ := f.resources.Get(context.Background(), "tenant", "model-surface", "surface")
			var google ModelConnection
			_ = json.Unmarshal(record.Spec, &google)
			ids := make([]string, len(test.steps))
			for i, step := range test.steps {
				ids[i] = step.op
			}
			connection := providerConnection(t, google.Surface.Origin, test.settings, test.protocol, test.auth, ids...)
			if err := f.handler.validate(context.Background(), "tenant", "model-surface", "surface", providerSpecJSON(t, connection)); err != nil {
				t.Fatal("provider surface rejected:", err)
			}
			putProviderSurface(t, f.resources, connection)
			ledger := usageLedgerFor(t, f.handler)
			for i, step := range test.steps {
				mu.Lock()
				current = &test.steps[i]
				mu.Unlock()
				r := f.request("generateContent", fmt.Sprintf("attempt-%d", i), step.body, true)
				r.Method = step.method
				r.URL.Path, r.URL.RawQuery = "/api/v1/llm/gateway/surface"+step.path, step.query
				r.Header.Set("X-Api-Key", "caller-key-never-forwarded")
				r.Header.Set("X-Goog-Api-Key", "caller-key-never-forwarded")
				r.Header.Set("Anthropic-Version", "2023-06-01")
				before := f.calls.Load()
				w := httptest.NewRecorder()
				f.handler.ClientGateway(w, r)
				if w.Code != 200 || w.Body.String() != step.response || f.calls.Load() != before+1 {
					t.Fatalf("%s: %d %s calls=%d", step.op, w.Code, w.Body.String(), f.calls.Load())
				}
				upstream := seen[len(seen)-1]
				want := providerTestSecret
				if test.header == "Authorization" {
					want = "Bearer " + providerTestSecret
				}
				if upstream.Header.Get(test.header) != want || strings.Contains(upstream.URL.RawQuery, "caller") {
					t.Fatalf("%s: credential or query wrong: %v %q", step.op, upstream.Header, upstream.URL.RawQuery)
				}
				for _, name := range []string{"Authorization", "X-Api-Key", "X-Goog-Api-Key"} {
					if name != test.header && upstream.Header.Get(name) != "" {
						t.Fatalf("%s: caller header %s crossed upstream", step.op, name)
					}
				}
				info := quotaHostModelAttempt(t, f, w.Header().Get("X-Aiplex-Attempt-ID"))
				if info.Decision.State != "settled" || info.Actual["requests"] != 1 || len(info.Actual) != 1 {
					t.Fatalf("%s: want exactly one settled request, got %s %v", step.op, info.Decision.State, info.Actual)
				}
				var rec *usageledger.Record
				records := ledger()
				for j := range records {
					if records[j].Operation == step.op {
						rec = &records[j]
					}
				}
				if rec == nil || rec.Status != usageledger.StatusOK {
					t.Fatalf("%s: no ok ledger record in %+v", step.op, records)
				}
				if step.usage == nil {
					if rec.Usage.Known() {
						t.Fatalf("%s: ledger recorded usage for a non-consuming exchange: %+v", step.op, rec.Usage)
					}
				} else {
					for counter, want := range map[string]*int64{"input": rec.Usage.InputTokens, "output": rec.Usage.OutputTokens, "total": rec.Usage.TotalTokens} {
						expected, expectedOK := step.usage[counter]
						if expectedOK != (want != nil) || (expectedOK && *want != expected) {
							t.Fatalf("%s: ledger %s=%v want %v", step.op, counter, want, step.usage)
						}
					}
				}
				raw, _ := json.Marshal(rec)
				if strings.Contains(string(raw), providerTestSecret) {
					t.Fatal("credential reached the ledger")
				}
				// The same attempt never redispatches.
				retry := httptest.NewRecorder()
				again := r.Clone(r.Context())
				again.Body = io.NopCloser(strings.NewReader(step.body))
				f.handler.ClientGateway(retry, again)
				if retry.Code != 409 || f.calls.Load() != before+1 {
					t.Fatalf("%s: replay %d calls=%d", step.op, retry.Code, f.calls.Load())
				}
			}
			if test.name == "openai" {
				// An unowned response never reaches the provider or the quota host.
				attempts, calls := len(quotaHostTestAttempts(t, f.store)), f.calls.Load()
				r := f.request("generateContent", "unowned", "", true)
				r.Method = http.MethodGet
				r.URL.Path = "/api/v1/llm/gateway/surface/v1/responses/resp_other"
				w := httptest.NewRecorder()
				f.handler.ClientGateway(w, r)
				if w.Code != 403 || f.calls.Load() != calls || len(quotaHostTestAttempts(t, f.store)) != attempts {
					t.Fatal("unowned response retrieval was dispatched", w.Code)
				}
			}
		})
	}
}

// TestAccessPointUtilityRoutesAndModelLessRule covers the access-point wires
// of the utility operations and the rule that serves operations naming no
// model.
func TestAccessPointUtilityRoutesAndModelLessRule(t *testing.T) {
	f := newAPFixture(t)
	var mu sync.Mutex
	var seen []*http.Request
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		seen = append(seen, r.Clone(r.Context()))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v1/responses" && r.Method == "POST":
			id := "resp_a"
			if strings.Contains(string(body), "model-b") {
				id = "resp_b"
			}
			fmt.Fprintf(w, `{"id":%q,"model":"m","status":"completed","usage":{"input_tokens":3,"output_tokens":2,"total_tokens":5}}`, id)
		case r.URL.Path == "/v1/embeddings":
			fmt.Fprint(w, `{"object":"list","data":[],"usage":{"prompt_tokens":4,"total_tokens":4}}`)
		case r.URL.Path == "/v1/moderations":
			fmt.Fprint(w, `{"id":"modr-1","model":"omni-moderation-latest","results":[]}`)
		case r.URL.Path == "/v1/messages/count_tokens":
			fmt.Fprint(w, `{"input_tokens":9}`)
		case r.URL.Path == "/v1beta/cachedContents" && r.Method == "POST":
			fmt.Fprint(w, `{"name":"cachedContents/c1","usageMetadata":{"totalTokenCount":10}}`)
		case strings.HasPrefix(r.URL.Path, "/v1beta/cachedContents"):
			fmt.Fprintf(w, `{"path":%q,"method":%q}`, r.URL.Path, r.Method)
		case strings.HasPrefix(r.URL.Path, "/v1/responses/"):
			fmt.Fprintf(w, `{"id":%q,"status":"completed","usage":{"input_tokens":3,"output_tokens":2,"total_tokens":5}}`, strings.Split(strings.TrimPrefix(r.URL.Path, "/v1/responses/"), "/")[0])
		default:
			http.NotFound(w, r)
		}
	}))
	defer up.Close()
	t.Setenv("AWS_ACCESS_KEY_ID", "x")
	runtime, err := connections.New(connections.Config{Network: endpointpolicy.Config{AllowedOrigins: []string{up.URL}, AllowLoopback: true}, Identities: []connections.Identity{{ID: "host", TenantID: "tenant", Type: "bearer-env", TokenEnv: "COHESION_HOST_CREDENTIAL", AllowedOrigins: []string{up.URL}}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.h.Runtime = runtime
	installProviderCredentials(t, f.h)
	surface := func(id, protocol string, p modelapi.Provider, auth *toolcredential.Auth, models []string, ops ...string) {
		o, err := modelapi.SelectProviderOperations(p.ID, ops...)
		if err != nil {
			t.Fatal(err)
		}
		c := ModelConnection{Surface: modelapi.Surface{ID: id, Protocol: protocol, Origin: up.URL, Models: models, Operations: o, Provider: &p}}
		if auth != nil {
			c.Auth = auth
		} else {
			c.IdentityRef = "host"
		}
		f.put("model-surface", id, c)
	}
	openai := modelapi.Provider{ID: modelapi.ProviderOpenAI}
	lifecycle := []string{"openai.v1.responses", "openai.v1.responses-retrieve", "openai.v1.responses-cancel", "openai.v1.responses-delete"}
	surface("resp-a", "openai-responses", openai, nil, []string{"model-a"}, lifecycle...)
	surface("resp-b", "openai-responses", openai, nil, []string{"model-b"}, lifecycle...)
	surface("emb", "openai-embeddings", openai, nil, []string{"emb-1"}, "openai.v1.embeddings")
	surface("mod", "openai-moderations", openai, nil, []string{modelapi.DefaultModerationModel}, "openai.v1.moderations")
	surface("claude-d", "anthropic-messages", modelapi.Provider{ID: modelapi.ProviderAnthropic, AnthropicVersion: "2023-06-01"}, anthropicRef, []string{"claude-x"}, "anthropic.messages", "anthropic.messages-count-tokens")
	da := modelapi.Provider{ID: modelapi.ProviderGeminiDeveloper}
	daOps := []string{"gemini.models.generateContent", "gemini.cachedContents.create", "gemini.cachedContents.list", "gemini.cachedContents.get", "gemini.cachedContents.update", "gemini.cachedContents.delete"}
	surface("gem", "google-genai", da, geminiKeyRef, []string{"gem-x"}, daOps...)
	spec := AccessPoint{ID: "util", CallerKinds: []string{CallerUser}, Models: []AccessPointModel{
		{Name: "model-a", SurfaceRef: "resp-a", NativeModel: "model-a"}, {Name: "model-b", SurfaceRef: "resp-b", NativeModel: "model-b"},
		{Name: "emb-1", SurfaceRef: "emb", NativeModel: "emb-1"}, {Name: modelapi.DefaultModerationModel, SurfaceRef: "mod", NativeModel: modelapi.DefaultModerationModel},
		{Name: "claude-x", SurfaceRef: "claude-d", NativeModel: "claude-x"}, {Name: "gem-x", SurfaceRef: "gem", NativeModel: "gem-x"}}}
	f.put(AccessPointKind, "util", spec)
	scopes := []string{"llm:model:model-a", "llm:model:model-b", "llm:model:emb-1", "llm:model:" + modelapi.DefaultModerationModel, "llm:model:claude-x", "llm:model:gem-x"}
	for _, s := range []string{"resp-a", "resp-b", "emb", "mod", "claude-d"} {
		scopes = append(scopes, "llm:surface:"+s+":*")
	}
	// Explicit operation grants (a wildcard would include shared-admin).
	for _, s := range []string{"gem", "gem2"} {
		for _, op := range daOps {
			scopes = append(scopes, "llm:surface:"+s+":"+op)
		}
	}
	scopes = append(scopes, "llm:model:gem-y")
	f.as(apUser("alice"), scopes...)
	records := attachLedger(t, f.h)
	jsonHeader := "Content-Type"
	app := "application/json"

	if w := f.do("POST", "/ap/util/v1/embeddings", `{"model":"emb-1","input":"x"}`, jsonHeader, app); w.Code != 200 {
		t.Fatal("embeddings", w.Code, w.Body.String())
	}
	if w := f.do("POST", "/ap/util/v1/moderations", `{"input":"x"}`, jsonHeader, app); w.Code != 200 {
		t.Fatal("moderations without model", w.Code, w.Body.String())
	}
	if w := f.do("POST", "/ap/util/v1/messages/count_tokens", `{"model":"claude-x","messages":[]}`, jsonHeader, app, "Anthropic-Version", "2023-06-01"); w.Code != 200 || w.Body.String() != `{"input_tokens":9}` {
		t.Fatal("count_tokens", w.Code, w.Body.String())
	}
	if w := f.do("POST", "/ap/util/v1beta/cachedContents", `{"model":"models/gem-x","contents":[]}`, jsonHeader, app); w.Code != 200 {
		t.Fatal("cache create", w.Code, w.Body.String())
	}
	// Model-less, ID-addressed: served by the surface that created the resource.
	for _, id := range []string{"resp_a", "resp_b"} {
		model := "model-" + strings.TrimPrefix(id, "resp_")
		if w := f.do("POST", "/ap/util/v1/responses", `{"model":"`+model+`","input":"x"}`, jsonHeader, app); w.Code != 200 {
			t.Fatal("create", model, w.Code, w.Body.String())
		}
	}
	for _, id := range []string{"resp_a", "resp_b"} {
		before := len(seen)
		if w := f.do("GET", "/ap/util/v1/responses/"+id, "", jsonHeader, app); w.Code != 200 || !strings.Contains(w.Body.String(), id) {
			t.Fatal("retrieve", id, w.Code, w.Body.String())
		}
		if w := f.do("POST", "/ap/util/v1/responses/"+id+"/cancel", "", jsonHeader, app); w.Code != 200 {
			t.Fatal("cancel", id, w.Code, w.Body.String())
		}
		if len(seen) != before+2 {
			t.Fatal("expected two upstream calls")
		}
	}
	// Records: retrieval and cancel never carry usage.
	for _, rec := range records() {
		if (rec.Operation == "openai.v1.responses-retrieve" || rec.Operation == "openai.v1.responses-cancel" || rec.Operation == "anthropic.messages-count-tokens" || rec.Operation == "gemini.cachedContents.create") && rec.Usage.Known() {
			t.Fatalf("%s recorded usage: %+v", rec.Operation, rec.Usage)
		}
		if rec.AccessPointID != "util" {
			t.Fatalf("access point dimension missing: %+v", rec)
		}
	}
	// An unknown resource is a native 404 and reaches nobody.
	before := len(seen)
	w := f.do("GET", "/ap/util/v1/responses/resp_unknown", "", jsonHeader, app)
	if w.Code != 404 || !strings.Contains(w.Body.String(), `"type":"not_found_error"`) || len(seen) != before {
		t.Fatalf("unknown resource: %d %s", w.Code, w.Body.String())
	}
	// Another principal cannot reach alice's response through the same access point.
	f.as(apUser("bob"), scopes...)
	if w = f.do("GET", "/ap/util/v1/responses/resp_a", "", jsonHeader, app); w.Code != 404 || len(seen) != before {
		t.Fatalf("foreign principal: %d %s", w.Code, w.Body.String())
	}
	f.as(apUser("alice"), scopes...)
	// Cache get/update/delete: owner-resolved to the one surface that created it.
	for _, call := range [][2]string{{"GET", "/v1beta/cachedContents/c1"}, {"PATCH", "/v1beta/cachedContents/c1?updateMask=ttl&key=caller"}, {"DELETE", "/v1beta/cachedContents/c1"}} {
		body := ""
		if call[0] == "PATCH" {
			body = `{"ttl":"60s"}`
		}
		if w = f.do(call[0], "/ap/util"+call[1], body, jsonHeader, app); w.Code != 200 {
			t.Fatal(call, w.Code, w.Body.String())
		}
	}
	if last := seen[len(seen)-2]; last.Method != "PATCH" || last.URL.RawQuery != "updateMask=ttl" || last.Header.Get("X-Goog-Api-Key") != providerTestSecret {
		t.Fatalf("cache patch upstream: %s ?%s %v", last.Method, last.URL.RawQuery, last.Header)
	}
	// Neither model nor ID (cache list): exactly one qualifying surface is
	// required, and the shared-listing grant is still enforced natively.
	if w = f.do("GET", "/ap/util/v1beta/cachedContents", "", jsonHeader, app); w.Code != 403 {
		t.Fatalf("list without the shared grant: %d %s", w.Code, w.Body.String())
	}
	f.as(apUser("alice"), append(append([]string(nil), scopes...), "llm:surface:gem:shared-admin")...)
	if w = f.do("GET", "/ap/util/v1beta/cachedContents?pageSize=2", "", jsonHeader, app); w.Code != 200 {
		t.Fatalf("list: %d %s", w.Code, w.Body.String())
	}
	surface("gem2", "google-genai", da, geminiKeyRef, []string{"gem-y"}, daOps...)
	spec.Models = append(spec.Models, AccessPointModel{Name: "gem-y", SurfaceRef: "gem2", NativeModel: "gem-y"})
	setSpec(f, spec)
	if w = f.do("GET", "/ap/util/v1beta/cachedContents", "", jsonHeader, app); w.Code != 400 || !strings.Contains(w.Body.String(), "several surfaces") || !strings.Contains(w.Body.String(), `"status":"INVALID_ARGUMENT"`) {
		t.Fatalf("ambiguous list: %d %s", w.Code, w.Body.String())
	}
}
