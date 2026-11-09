package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/vamsiramakrishnan/aiplex/internal/api"
	"github.com/vamsiramakrishnan/aiplex/internal/deploy"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
)

type llmK8sRecorder struct {
	mu          sync.Mutex
	beforeApply func(deploy.Manifest)
	applied     []deploy.Manifest
	deleted     []string
	failApply   string
	failDelete  string
}

func (k *llmK8sRecorder) Apply(_ context.Context, m deploy.Manifest) error {
	k.mu.Lock()
	k.applied = append(k.applied, m)
	before, fail := k.beforeApply, m.Name == k.failApply
	k.mu.Unlock()
	if before != nil {
		before(m)
	}
	if fail {
		return errors.New("sensitive upstream error must not escape")
	}
	return nil
}
func (k *llmK8sRecorder) Delete(_ context.Context, version, kind, name, namespace string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.deleted = append(k.deleted, kind+"/"+name)
	if version != "aigateway.envoyproxy.io/v1alpha1" || namespace != "aiplex-system" {
		return errors.New("wrong resource ownership")
	}
	if name == k.failDelete {
		return errors.New("sensitive upstream error must not escape")
	}
	return nil
}

func llmCohesionRouter(store registry.LLMPlexStore, k8s deploy.K8sClient) chi.Router {
	h := api.NewLLMHandler(store, k8s, "gateway", nil)
	r := chi.NewRouter()
	// Existing workflow fixtures act as revision-aware clients. Dedicated CAS
	// tests use the raw handlers to exercise missing/stale preconditions.
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if (r.Method == "PUT" || r.Method == "DELETE") && r.Header.Get("If-Match") == "" {
				parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
				if len(parts) == 2 {
					kind := ""
					if parts[0] == "routes" {
						kind = registry.ConfigRoute
					}
					if parts[0] == "providers" {
						kind = registry.ConfigProvider
					}
					if versioned, ok := store.(registry.VersionedConfigurationStore); ok && kind != "" {
						record, _ := versioned.GetConfiguration(r.Context(), kind, parts[1])
						r.Header.Set("If-Match", strconv.FormatInt(record.Revision, 10))
					}
				}
			}
			next.ServeHTTP(w, r)
		})
	})
	r.Get("/routes/{modelId}", h.GetRouteConfig)
	r.Put("/routes/{modelId}", h.PutRouteConfig)
	r.Delete("/routes/{modelId}", h.DeleteRouteConfig)
	r.Put("/providers/{provider}", h.PutProvider)
	r.Post("/usage", h.RecordUsage)
	r.Get("/usage", h.ListUsageRecords)
	r.Get("/summary", h.GetUsageSummary)
	return r
}
func llmCohesionRequest(router http.Handler, method, path, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
	return w
}
func llmCohesionBody(provider string) string {
	return `{"backends":[{"provider":"` + provider + `","model_id":"native-model","weight":100,"enabled":true}]}`
}

func TestLLMCohesionDeploymentFailureKeepsDesiredAndDoesNotClaimActivation(t *testing.T) {
	store := registry.NewMemoryStore()
	k8s := &llmK8sRecorder{failApply: llmCohesionName("model-one", "google")}
	router := llmCohesionRouter(store, k8s)
	w := llmCohesionRequest(router, "PUT", "/routes/model-one", llmCohesionBody("google"))
	if w.Code != 502 || w.Header().Get("X-LLM-Configuration") != "saved" || w.Header().Get("X-LLM-Activation") != "failed" || strings.Contains(w.Body.String(), "sensitive") {
		t.Fatalf("submission failure hidden or leaked: %d %v %s", w.Code, w.Header(), w.Body.String())
	}
	saved, err := store.GetRouteConfig(context.Background(), "model-one")
	if err != nil || saved.ModelID != "model-one" || len(k8s.applied) != 1 || k8s.applied[0].Kind != "AIServiceBackend" {
		t.Fatalf("desired path identity or dependency order lost: %+v %v %v", saved, err, k8s.applied)
	}
	k8s.failApply = ""
	w = llmCohesionRequest(router, "PUT", "/routes/model-one", llmCohesionBody("google"))
	if w.Code != 200 || w.Header().Get("X-LLM-Activation") != "submitted-unverified" || k8s.applied[len(k8s.applied)-1].Kind != "LLMRoute" {
		t.Fatalf("saved configuration could not be retried: %d %s", w.Code, w.Body.String())
	}
}

func TestLLMCohesionRetiredBackendInventorySurvivesRestartAndRejectsCallerAuthority(t *testing.T) {
	store := registry.NewMemoryStore()
	k8s := &llmK8sRecorder{}
	router := llmCohesionRouter(store, k8s)
	if w := llmCohesionRequest(router, "PUT", "/routes/model-one", llmCohesionBody("google")); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	k8s.failDelete = llmCohesionName("model-one", "google")
	update := strings.TrimSuffix(llmCohesionBody("openai"), "}") + `,"retired_backends":["foreign-backend","another-model-google-backend"]}`
	w := llmCohesionRequest(router, "PUT", "/routes/model-one", update)
	if w.Code != 502 || w.Header().Get("X-LLM-Activation") != "cleanup-pending" {
		t.Fatalf("cleanup failure hidden: %d %s", w.Code, w.Body.String())
	}
	saved, err := store.GetRouteConfig(context.Background(), "model-one")
	if err != nil || saved.Backends[0].Provider != "openai" || len(saved.RetiredBackends) != 1 || saved.RetiredBackends[0] != llmCohesionName("model-one", "google") {
		t.Fatalf("cleanup inventory lost or supplied by caller: %+v %v", saved, err)
	}
	// A fresh host must recover from persisted inventory; GET->PUT metadata is
	// accepted while deletion authority is still reconstructed server-side.
	k8s.failDelete = ""
	router = llmCohesionRouter(store, k8s)
	raw, _ := json.Marshal(saved)
	w = llmCohesionRequest(router, "PUT", "/routes/model-one", string(raw))
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	saved, _ = store.GetRouteConfig(context.Background(), "model-one")
	if len(saved.RetiredBackends) != 0 {
		t.Fatal("completed cleanup inventory remained pending")
	}
	for _, deleted := range k8s.deleted {
		if deleted != "AIServiceBackend/"+llmCohesionName("model-one", "google") {
			t.Fatalf("caller selected a deletion target: %s", deleted)
		}
	}
}

func TestLLMCohesionDeleteRetainsConfigurationUntilOwnedResourcesRemoved(t *testing.T) {
	store := registry.NewMemoryStore()
	k8s := &llmK8sRecorder{}
	router := llmCohesionRouter(store, k8s)
	if w := llmCohesionRequest(router, "PUT", "/routes/model-one", llmCohesionBody("google")); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	k8s.failDelete = "llm-model-one"
	if w := llmCohesionRequest(router, "DELETE", "/routes/model-one", ""); w.Code != 502 {
		t.Fatal(w.Code, w.Body.String())
	}
	if _, err := store.GetRouteConfig(context.Background(), "model-one"); err != nil || len(k8s.deleted) != 1 {
		t.Fatal("failed route deletion erased retry inventory")
	}
	k8s.failDelete = ""
	if w := llmCohesionRequest(router, "DELETE", "/routes/model-one", ""); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	if _, err := store.GetRouteConfig(context.Background(), "model-one"); !errors.Is(err, registry.ErrNotFound) || k8s.deleted[len(k8s.deleted)-1] != "AIServiceBackend/"+llmCohesionName("model-one", "google") {
		t.Fatal("successful deletion left desired state or missed owned backend")
	}
}

func TestLLMCohesionFallbackReferencesResolveAndProtectDependencies(t *testing.T) {
	store := registry.NewMemoryStore()
	k8s := &llmK8sRecorder{}
	router := llmCohesionRouter(store, k8s)
	primary := strings.TrimSuffix(llmCohesionBody("google"), "}") + `,"fallbacks":["fallback-model"]}`
	if w := llmCohesionRequest(router, "PUT", "/routes/model-one", primary); w.Code != 400 || len(k8s.applied) != 0 {
		t.Fatal("unresolved fallback rendered a guessed resource")
	}
	if w := llmCohesionRequest(router, "PUT", "/routes/fallback-model", llmCohesionBody("openai")); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := llmCohesionRequest(router, "PUT", "/routes/model-one", primary); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if !strings.Contains(k8s.applied[len(k8s.applied)-1].YAML, "name: "+llmCohesionName("fallback-model", "openai")) {
		t.Fatal("fallback did not reference real provider backend name")
	}
	if w := llmCohesionRequest(router, "DELETE", "/routes/fallback-model", ""); w.Code != 409 || len(k8s.deleted) != 0 {
		t.Fatal("referenced fallback deleted")
	}
	before := len(k8s.applied)
	if w := llmCohesionRequest(router, "PUT", "/routes/fallback-model", llmCohesionBody("anthropic")); w.Code != 409 || len(k8s.applied) != before {
		t.Fatal("referenced fallback backend names changed")
	}
}

func TestLLMCohesionProviderCredentialInheritanceAndDisabledRoutes(t *testing.T) {
	store := registry.NewMemoryStore()
	k8s := &llmK8sRecorder{}
	router := llmCohesionRouter(store, k8s)
	if w := llmCohesionRequest(router, "PUT", "/providers/google", `{"enabled":true,"secret_ref":"operator-google-key"}`); w.Code != 200 || w.Header().Get("X-LLM-Activation") != "configuration-only" {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := llmCohesionRequest(router, "PUT", "/routes/model-one", llmCohesionBody("google")); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	saved, _ := store.GetRouteConfig(context.Background(), "model-one")
	if saved.Backends[0].SecretRef != "operator-google-key" || !strings.Contains(k8s.applied[0].YAML, "operator-google-key") {
		t.Fatal("registered provider credential reference ignored")
	}
	if w := llmCohesionRequest(router, "PUT", "/providers/google", `{"enabled":false}`); w.Code != 409 {
		t.Fatal("in-use provider disable misleadingly accepted")
	}
	if w := llmCohesionRequest(router, "PUT", "/providers/openai", `{"enabled":false}`); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	before := len(k8s.applied)
	if w := llmCohesionRequest(router, "PUT", "/routes/other-model", llmCohesionBody("openai")); w.Code != 400 || len(k8s.applied) != before {
		t.Fatal("disabled provider was routed")
	}
}

func TestLLMCohesionProviderDisableRetainsPendingRuntimeDependency(t *testing.T) {
	store := registry.NewMemoryStore()
	k8s := &llmK8sRecorder{}
	router := llmCohesionRouter(store, k8s)
	if w := llmCohesionRequest(router, "PUT", "/routes/model-one", llmCohesionBody("google")); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	// A backend failure leaves the old native route active even though the new
	// desired model uses another provider. Retired inventory still protects it.
	k8s.failApply = llmCohesionName("model-one", "openai")
	if w := llmCohesionRequest(router, "PUT", "/routes/model-one", llmCohesionBody("openai")); w.Code != 502 {
		t.Fatal(w.Body.String())
	}
	if w := llmCohesionRequest(router, "PUT", "/providers/google", `{"enabled":false}`); w.Code != 409 {
		t.Fatal("pending native dependency was hidden by replacement desired state")
	}
}

func llmCohesionName(model, provider string) string {
	name, err := deploy.LLMBackendName(model, provider, 2)
	if err != nil {
		panic(err)
	}
	return name
}

func TestLLMCohesionTwoHostsUseDistinctBackendIDsForAmbiguousLegacyTuples(t *testing.T) {
	store := registry.NewMemoryStore()
	k8s := &llmK8sRecorder{}
	first, second := llmCohesionRouter(store, k8s), llmCohesionRouter(store, k8s)
	// Independent hosts may conflict while validating the shared dependency
	// generation. Retry that known-undispatched conflict using a fresh read.
	type result struct {
		path, provider string
		response       *httptest.ResponseRecorder
	}
	results := make(chan result, 2)
	go func() {
		results <- result{"/routes/foo", "bar-baz", llmCohesionRequest(first, "PUT", "/routes/foo", llmCohesionBody("bar-baz"))}
	}()
	go func() {
		results <- result{"/routes/foo-bar", "baz", llmCohesionRequest(second, "PUT", "/routes/foo-bar", llmCohesionBody("baz"))}
	}()
	for range 2 {
		result := <-results
		w := result.response
		if w.Code == 412 {
			w = llmCohesionRequest(first, "PUT", result.path, llmCohesionBody(result.provider))
		}
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	names := map[string]bool{}
	for _, m := range k8s.applied {
		if m.Kind == "AIServiceBackend" {
			names[m.Name] = true
			if len(m.Name) > 63 {
				t.Fatal("backend ID exceeds DNS label bound")
			}
		}
	}
	if len(names) != 2 || !names[llmCohesionName("foo", "bar-baz")] || !names[llmCohesionName("foo-bar", "baz")] {
		t.Fatalf("cross-host resource aliasing: %v", names)
	}
}

func TestLLMCohesionLegacyCollisionCleanupStillFailsClosed(t *testing.T) {
	store := registry.NewMemoryStore()
	for _, pair := range [][2]string{{"foo", "bar-baz"}, {"foo-bar", "baz"}} {
		_ = store.PutRouteConfig(context.Background(), &models.LLMRouteConfig{ModelID: pair[0], Backends: []models.LLMBackend{{Provider: pair[1], ModelID: "native-model", Weight: 100, Enabled: true}}})
	}
	k8s := &llmK8sRecorder{}
	w := llmCohesionRequest(llmCohesionRouter(store, k8s), "DELETE", "/routes/foo", "")
	if w.Code != 409 || len(k8s.deleted) != 0 {
		t.Fatal("legacy collision permitted deleting shared backend ownership")
	}
}

func TestLLMCohesionLegacyMigrationKeepsCleanupAndIgnoresNamingSpoofs(t *testing.T) {
	store := registry.NewMemoryStore()
	_ = store.PutRouteConfig(context.Background(), &models.LLMRouteConfig{ModelID: "legacy-model", Backends: []models.LLMBackend{{Provider: "google", ModelID: "native-model", Weight: 100, Enabled: true}}})
	k8s := &llmK8sRecorder{failDelete: "legacy-model-google-backend"}
	router := llmCohesionRouter(store, k8s)
	body := strings.TrimSuffix(llmCohesionBody("google"), "}") + `,"backend_naming_version":999,"retired_backends":["foreign-google-backend"]}`
	w := llmCohesionRequest(router, "PUT", "/routes/legacy-model", body)
	if w.Code != 502 {
		t.Fatal(w.Code, w.Body.String())
	}
	saved, _ := store.GetRouteConfig(context.Background(), "legacy-model")
	if saved.BackendNamingVersion != 2 || len(saved.RetiredBackends) != 1 || saved.RetiredBackends[0] != "legacy-model-google-backend" || k8s.applied[0].Name != llmCohesionName("legacy-model", "google") {
		t.Fatalf("unsafe naming migration: %+v %v", saved, k8s.applied)
	}
	k8s.failDelete = ""
	raw, _ := json.Marshal(saved)
	w = llmCohesionRequest(llmCohesionRouter(store, k8s), "PUT", "/routes/legacy-model", string(raw))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, name := range k8s.deleted {
		if name != "AIServiceBackend/legacy-model-google-backend" {
			t.Fatal("caller controlled cleanup target", name)
		}
	}
}

func TestLLMCohesionLegacyFallbackUsesStoredNamingAndProtectsMigration(t *testing.T) {
	store := registry.NewMemoryStore()
	_ = store.PutRouteConfig(context.Background(), &models.LLMRouteConfig{ModelID: "fallback-model", Backends: []models.LLMBackend{{Provider: "openai", ModelID: "native-model", Weight: 100, Enabled: true}}})
	k8s := &llmK8sRecorder{}
	router := llmCohesionRouter(store, k8s)
	primary := strings.TrimSuffix(llmCohesionBody("google"), "}") + `,"fallbacks":["fallback-model"]}`
	if w := llmCohesionRequest(router, "PUT", "/routes/model-one", primary); w.Code != 200 || !strings.Contains(k8s.applied[len(k8s.applied)-1].YAML, "name: fallback-model-openai-backend") {
		t.Fatal("fallback resolver changed the stored legacy target identity")
	}
	before := len(k8s.applied)
	if w := llmCohesionRequest(router, "PUT", "/routes/fallback-model", llmCohesionBody("openai")); w.Code != 409 || len(k8s.applied) != before || len(k8s.deleted) != 0 {
		t.Fatal("legacy migration deleted an active fallback reference")
	}
	if w := llmCohesionRequest(router, "PUT", "/routes/model-one", llmCohesionBody("google")); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := llmCohesionRequest(router, "PUT", "/routes/fallback-model", llmCohesionBody("openai")); w.Code != 200 || k8s.deleted[len(k8s.deleted)-1] != "AIServiceBackend/fallback-model-openai-backend" {
		t.Fatal("unreferenced legacy backend migration did not complete")
	}
}

type llmSummaryFailureStore struct{ *registry.MemoryStore }

func (*llmSummaryFailureStore) GetUsageSummary(context.Context, string, string, string) (*models.UsageSummary, error) {
	return nil, errors.New("reporting summary unavailable")
}

func TestLLMCohesionInvalidUsageRejectedAndSummaryFailureDoesNotLoseReports(t *testing.T) {
	store := &llmSummaryFailureStore{registry.NewMemoryStore()}
	_ = store.PutRouteConfig(context.Background(), &models.LLMRouteConfig{ModelID: "model", Budget: &models.UsageBudget{MaxDailyCostUSD: 1}})
	router := llmCohesionRouter(store, &llmK8sRecorder{})
	for _, body := range []string{
		`{"model_id":"model","provider":"google","input_tokens":-1}`,
		`{"model_id":"model","provider":"google","output_tokens":-1}`,
		`{"model_id":"model","provider":"google","cost_usd":-1}`,
		`{"model_id":"model","provider":"google","latency_ms":-1}`,
		`{"model_id":"model","provider":"google","input_tokens":9223372036854775807,"output_tokens":1}`,
		`{"model_id":"model","provider":"google","id":"invalid/document"}`,
	} {
		if w := llmCohesionRequest(router, "POST", "/usage", body); w.Code != 400 {
			t.Fatalf("invalid report accepted: %d %s", w.Code, body)
		}
	}
	w := llmCohesionRequest(router, "POST", "/usage", `{"model_id":"model","provider":"google","cost_usd":5,"input_tokens":10}`)
	if w.Code != 201 || w.Header().Get("X-LLM-Reporting-Warning") != "budget_summary_unavailable" {
		t.Fatalf("summary outage lost a valid report: %d %v", w.Code, w.Header())
	}
	records, err := store.ListUsageRecords(context.Background(), "model", "", time.Time{}, 0)
	if err != nil || len(records) != 1 || records[0].CostUSD != 5 {
		t.Fatal("valid reporting evidence was not retained")
	}
}

func TestLLMCohesionUsageQueryValidationAndBounds(t *testing.T) {
	store := registry.NewMemoryStore()
	router := llmCohesionRouter(store, &llmK8sRecorder{})
	for _, path := range []string{"/usage?limit=0", "/usage?limit=-1", "/usage?limit=1001", "/usage?since=yesterday", "/summary?period=year"} {
		if w := llmCohesionRequest(router, "GET", path, ""); w.Code != 400 {
			t.Fatalf("invalid reporting query accepted: %s %d", path, w.Code)
		}
	}
	for _, id := range []string{"one", "two"} {
		_ = store.AppendUsage(context.Background(), &models.UsageRecord{ID: id, ModelID: "model", AgentID: "agent", Timestamp: time.Now()})
	}
	w := llmCohesionRequest(router, "GET", "/usage?model_id=model&agent_id=agent&limit=1&since=2020-01-01T00:00:00Z", "")
	var records []models.UsageRecord
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &records) != nil || len(records) != 1 {
		t.Fatal(w.Code, w.Body.String())
	}
}
