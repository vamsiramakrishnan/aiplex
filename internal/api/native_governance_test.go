package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/vamsiramakrishnan/aiplex/internal/connections"
	"github.com/vamsiramakrishnan/aiplex/pkg/cloudagents"
	"github.com/vamsiramakrishnan/aiplex/pkg/endpointpolicy"
	"github.com/vamsiramakrishnan/aiplex/pkg/governance"
	"github.com/vamsiramakrishnan/aiplex/pkg/nativeapi"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
)

const nativeTestResource = "projects/project-a/locations/us-central1/services/agent-one"
const nativeTestPayload = `{"resource":{"displayName":"reviewed-native-display","agentSpec":{"type":"NO_SPEC"}}}`

type nativeCountingStore struct {
	*resources.MemoryStore
	puts atomic.Int32
}

func (s *nativeCountingStore) Put(ctx context.Context, record resources.Record, revision uint64) (resources.Record, error) {
	s.puts.Add(1)
	return s.MemoryStore.Put(ctx, record, revision)
}

type nativeHostFixture struct {
	handler *ConnectedHandler
	router  http.Handler
	store   *nativeCountingStore
	calls   atomic.Int32
}

func newNativeHostFixture(t *testing.T) *nativeHostFixture {
	t.Helper()
	fixture := &nativeHostFixture{store: &nativeCountingStore{MemoryStore: resources.NewMemoryStore()}}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fixture.calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer operator-native-token" {
			t.Errorf("native credential was not resolved from host identity: %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			if r.URL.Path != "/v1/"+nativeTestResource || r.URL.RawQuery != "" {
				t.Errorf("unexpected native read path %q query %q", r.URL.Path, r.URL.RawQuery)
			}
			_, _ = io.WriteString(w, `{"name":"`+nativeTestResource+`","displayName":"native-observation"}`)
		case http.MethodPost:
			if r.URL.Path != "/v1/projects/project-a/locations/us-central1/services" || r.URL.Query().Get("serviceId") != "agent-one" || len(r.URL.Query()) != 1 {
				t.Errorf("unexpected native creation path %q query %q", r.URL.Path, r.URL.RawQuery)
			}
			var body map[string]json.RawMessage
			if json.NewDecoder(r.Body).Decode(&body) != nil || string(body["name"]) != `"`+nativeTestResource+`"` || string(body["displayName"]) != `"reviewed-native-display"` {
				t.Errorf("native creation did not retain reviewed payload: %v", body)
			}
			_, _ = io.WriteString(w, `{"name":"projects/project-a/locations/us-central1/operations/op-one","done":false}`)
		default:
			t.Errorf("unreviewed native method %q", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(upstream.Close)
	t.Setenv("AIPLEX_NATIVE_HOST_TEST_TOKEN", "operator-native-token")
	options := json.RawMessage(`{"project":"project-a","location":"us-central1"}`)
	providers := []connections.GovernanceProvider{
		{ID: "google-native", TenantID: "tenant-a", Type: "google-agent-registry", Endpoint: upstream.URL, IdentityRef: "native", Options: options, AllowApply: true},
		{ID: "google-readonly", TenantID: "tenant-a", Type: "google-agent-registry", Endpoint: upstream.URL, IdentityRef: "native", Options: options},
		{ID: "auth-native", TenantID: "tenant-a", Type: "google-auth-manager", Endpoint: upstream.URL, IdentityRef: "native", Options: options},
		{ID: "other-tenant", TenantID: "tenant-b", Type: "google-agent-registry", Endpoint: upstream.URL, IdentityRef: "native", Options: options, AllowApply: true},
	}
	runtime, err := connections.New(connections.Config{
		Network: endpointpolicy.Config{AllowedOrigins: []string{upstream.URL}, AllowLoopback: true},
		Identities: []connections.Identity{
			{ID: "native", TenantID: "tenant-a", Type: "bearer-env", TokenEnv: "AIPLEX_NATIVE_HOST_TEST_TOKEN", AllowedOrigins: []string{upstream.URL}},
			{ID: "native", TenantID: "tenant-b", Type: "bearer-env", TokenEnv: "AIPLEX_NATIVE_HOST_TEST_TOKEN", AllowedOrigins: []string{upstream.URL}},
		},
		GovernanceProviders: providers,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	fixture.handler = NewConnectedHandler(fixture.store, runtime, func(*http.Request) (string, error) { return "tenant-a", nil }, nil, false)
	if err = fixture.handler.ConfigureNativeGovernance(); err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	router.Route("/api/v1/iam", fixture.handler.MountNativeGovernance)
	fixture.router = router
	return fixture
}

func nativeTestBinding(id, provider string) governance.AgentBinding {
	return governance.AgentBinding{
		ID: id, TenantID: "tenant-a", DriverRef: provider,
		Agent:   governance.PrincipalRef{Provider: "google", TenantID: "project-a", ImmutableID: "native-agent-immutable-id", Kind: "agent"},
		Allowed: []governance.Grant{{Operation: "services.get", Resource: nativeTestResource}, {Operation: "services.create", Resource: nativeTestResource}},
		Desired: []governance.DesiredOperation{{ID: "create", Operation: nativeapi.Operation{Name: "services.create", Resource: nativeTestResource, Payload: json.RawMessage(nativeTestPayload)}}},
	}
}

func (f *nativeHostFixture) request(t *testing.T, method, path string, body any, scopes ...string) *httptest.ResponseRecorder {
	t.Helper()
	var raw []byte
	if body != nil {
		var err error
		raw, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	request := trustedConnectedRequest(path, string(raw), scopes)
	request.Method = method
	request.Header.Set("Authorization", "Bearer caller-must-not-be-relayed")
	recorder := httptest.NewRecorder()
	f.router.ServeHTTP(recorder, request)
	return recorder
}

func (f *nativeHostFixture) saveBinding(t *testing.T, binding governance.AgentBinding) resources.Record {
	t.Helper()
	response := f.request(t, http.MethodPut, "/api/v1/iam/native/bindings/"+binding.ID, binding)
	if response.Code != http.StatusOK {
		t.Fatalf("save binding: %d %s", response.Code, response.Body.String())
	}
	var record resources.Record
	if err := json.Unmarshal(response.Body.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	return record
}

func (f *nativeHostFixture) plan(t *testing.T, binding string, revision uint64) governance.Plan {
	t.Helper()
	response := f.request(t, http.MethodPost, "/api/v1/iam/native/bindings/"+binding+"/plan", map[string]any{"binding_revision": revision, "desired_id": "create"}, "aiplex:governance:plan:"+binding)
	if response.Code != http.StatusOK {
		t.Fatalf("plan: %d %s", response.Code, response.Body.String())
	}
	var plan governance.Plan
	if err := json.Unmarshal(response.Body.Bytes(), &plan); err != nil || plan.ID == "" || plan.Digest == "" || plan.Revision == 0 {
		t.Fatalf("invalid plan: %v %s", err, response.Body.String())
	}
	return plan
}

func TestNativeGovernanceHostRequiresReadGrantBeforeNativeRPC(t *testing.T) {
	f := newNativeHostFixture(t)
	record := f.saveBinding(t, nativeTestBinding("binding-a", "google-native"))
	input := map[string]any{"binding_revision": record.Revision, "operation": nativeapi.Operation{Name: "services.get", Resource: nativeTestResource}}
	path := "/api/v1/iam/native/bindings/binding-a/read"
	denied := f.request(t, http.MethodPost, path, input, "aiplex:governance:plan:binding-a")
	if denied.Code != http.StatusForbidden || f.calls.Load() != 0 {
		t.Fatalf("read called cloud without read grant: %d calls=%d %s", denied.Code, f.calls.Load(), denied.Body.String())
	}
	anonymous := httptest.NewRecorder()
	f.router.ServeHTTP(anonymous, httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`)))
	if anonymous.Code != http.StatusUnauthorized || f.calls.Load() != 0 {
		t.Fatalf("anonymous native read reached RPC: %d calls=%d", anonymous.Code, f.calls.Load())
	}
	allowed := f.request(t, http.MethodPost, path, input, "aiplex:governance:read:binding-a")
	if allowed.Code != http.StatusOK || f.calls.Load() != 1 || !strings.Contains(allowed.Body.String(), "native-observation") {
		t.Fatalf("governed read failed: %d calls=%d %s", allowed.Code, f.calls.Load(), allowed.Body.String())
	}
	input["operation"] = nativeapi.Operation{Name: "services.get", Resource: "projects/other-project/locations/us-central1/services/agent-one"}
	outside := f.request(t, http.MethodPost, path, input, "aiplex:governance:read:binding-a")
	if outside.Code == http.StatusOK || f.calls.Load() != 1 {
		t.Fatalf("native resource escaped exact binding: %d calls=%d", outside.Code, f.calls.Load())
	}
}

func TestNativeGovernanceBindingRejectsSecretsBeforePersistence(t *testing.T) {
	f := newNativeHostFixture(t)
	for _, secret := range []string{`"client_secret":"never-store-this"`, `"credentials":{"private_key":"never-store-this"}`, `"desired":[{"id":"create","operation":{"name":"services.create","resource":"` + nativeTestResource + `","payload":{"resource":{"agentSpec":{"type":"A2A","content":{"access_token":"never-store-this"}}}}}}]`} {
		binding, _ := json.Marshal(nativeTestBinding("binding-a", "google-native"))
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(binding, &fields); err != nil {
			t.Fatal(err)
		}
		var additional map[string]json.RawMessage
		if err := json.Unmarshal([]byte("{"+secret+"}"), &additional); err != nil {
			t.Fatal(err)
		}
		for key, value := range additional {
			fields[key] = value
		}
		before := f.store.puts.Load()
		response := f.request(t, http.MethodPut, "/api/v1/iam/native/bindings/binding-a", fields)
		if response.Code != http.StatusBadRequest || f.store.puts.Load() != before || f.calls.Load() != 0 || strings.Contains(response.Body.String(), "never-store-this") {
			t.Fatalf("secret payload reached persistence/RPC or response: %d puts=%d calls=%d %s", response.Code, f.store.puts.Load()-before, f.calls.Load(), response.Body.String())
		}
	}
	if _, err := f.store.Get(context.Background(), "tenant-a", governance.BindingKind, "binding-a"); !errors.Is(err, resources.ErrNotFound) {
		t.Fatalf("rejected binding persisted: %v", err)
	}
	binding := nativeTestBinding("binding-a", "other-tenant")
	before := f.store.puts.Load()
	response := f.request(t, http.MethodPut, "/api/v1/iam/native/bindings/binding-a", binding)
	if response.Code != http.StatusBadRequest || f.store.puts.Load() != before || f.calls.Load() != 0 {
		t.Fatalf("cross-tenant driver binding persisted: %d", response.Code)
	}
}

func TestNativeGovernancePlanGetReturnsOnlyPublicPlan(t *testing.T) {
	f := newNativeHostFixture(t)
	record := f.saveBinding(t, nativeTestBinding("binding-a", "google-native"))
	denied := f.request(t, http.MethodPost, "/api/v1/iam/native/bindings/binding-a/plan", map[string]any{"binding_revision": record.Revision, "desired_id": "create"}, "aiplex:governance:read:binding-a")
	plans, err := f.store.List(context.Background(), "tenant-a", governance.PlanKind)
	if denied.Code != http.StatusForbidden || err != nil || len(plans) != 0 || f.calls.Load() != 0 {
		t.Fatalf("plan without grant: %d plans=%d calls=%d err=%v", denied.Code, len(plans), f.calls.Load(), err)
	}
	plan := f.plan(t, "binding-a", record.Revision)
	private, err := f.store.Get(context.Background(), "tenant-a", governance.PlanKind, plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	var saved struct {
		Plan      governance.Plan          `json:"plan"`
		Operation struct{ Payload string } `json:"operation"`
	}
	if json.Unmarshal(private.Spec, &saved) != nil || saved.Operation.Payload != base64.StdEncoding.EncodeToString([]byte(nativeTestPayload)) {
		t.Fatalf("test did not exercise sealed operation storage: %s", private.Spec)
	}
	path := "/api/v1/iam/native/plans/" + plan.ID
	denied = f.request(t, http.MethodGet, path, nil)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("plan get without read grant: %d", denied.Code)
	}
	response := f.request(t, http.MethodGet, path, nil, "aiplex:governance:read:binding-a")
	var public map[string]json.RawMessage
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &public) != nil || string(public["id"]) != fmt.Sprintf("%q", plan.ID) || string(public["operation"]) != `"services.create"` || public["plan"] != nil || public["payload"] != nil || strings.Contains(response.Body.String(), saved.Operation.Payload) || strings.Contains(response.Body.String(), "reviewed-native-display") || f.calls.Load() != 0 {
		t.Fatalf("private operation escaped plan get: %d %s", response.Code, response.Body.String())
	}
}

func TestNativeGovernanceApplyRequiresExactDigestAndProviderOptIn(t *testing.T) {
	f := newNativeHostFixture(t)
	record := f.saveBinding(t, nativeTestBinding("binding-a", "google-native"))
	plan := f.plan(t, "binding-a", record.Revision)
	path := "/api/v1/iam/native/plans/" + plan.ID + "/apply"
	for _, input := range []map[string]any{
		{"plan_revision": plan.Revision},
		{"plan_revision": plan.Revision, "approval_digest": "approval-is-not-this-plan-digest"},
	} {
		response := f.request(t, http.MethodPost, path, input, "aiplex:governance:apply:binding-a")
		if response.Code != http.StatusForbidden || f.calls.Load() != 0 {
			t.Fatalf("unapproved apply reached native RPC: %d calls=%d %s", response.Code, f.calls.Load(), response.Body.String())
		}
	}
	if _, err := f.store.Get(context.Background(), "tenant-a", governance.ReceiptKind, plan.ID); !errors.Is(err, resources.ErrNotFound) {
		t.Fatalf("denied approval claimed native execution: %v", err)
	}
	input := map[string]any{"plan_revision": plan.Revision, "approval_digest": plan.Digest}
	denied := f.request(t, http.MethodPost, path, input, "aiplex:governance:plan:binding-a")
	if denied.Code != http.StatusForbidden || f.calls.Load() != 0 {
		t.Fatalf("apply without grant reached native RPC: %d", denied.Code)
	}
	for i := 0; i < 2; i++ {
		response := f.request(t, http.MethodPost, path, input, "aiplex:governance:apply:binding-a")
		var receipt governance.Receipt
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &receipt) != nil || receipt.State != "succeeded" || receipt.Readiness != "unverified" || receipt.PlanDigest != plan.Digest || f.calls.Load() != 1 {
			t.Fatalf("approved execution or idempotent replay failed: %d calls=%d %s", response.Code, f.calls.Load(), response.Body.String())
		}
	}
	readonly := f.saveBinding(t, nativeTestBinding("binding-readonly", "google-readonly"))
	readonlyPlan := f.plan(t, "binding-readonly", readonly.Revision)
	response := f.request(t, http.MethodPost, "/api/v1/iam/native/plans/"+readonlyPlan.ID+"/apply", map[string]any{"plan_revision": readonlyPlan.Revision, "approval_digest": readonlyPlan.Digest}, "aiplex:governance:apply:binding-readonly")
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "APPLY_DISABLED") || f.calls.Load() != 1 {
		t.Fatalf("global apply flag bypassed provider opt-in: %d calls=%d %s", response.Code, f.calls.Load(), response.Body.String())
	}
}

func TestNativeGovernanceCredentialOperationsCannotEnterPublicBindings(t *testing.T) {
	f := newNativeHostFixture(t)
	binding := nativeTestBinding("credential-binding", "auth-native")
	binding.Allowed = []governance.Grant{{Operation: "authProviders.get", Resource: "projects/project-a/locations/us-central1/authProviders/credential-one"}}
	binding.Desired = nil
	before := f.store.puts.Load()
	response := f.request(t, http.MethodPut, "/api/v1/iam/native/bindings/credential-binding", binding)
	if response.Code != http.StatusBadRequest || f.store.puts.Load() != before || f.calls.Load() != 0 {
		t.Fatalf("credential operation persisted publicly: %d calls=%d", response.Code, f.calls.Load())
	}
	// Even a preexisting record inserted outside HTTP cannot enable credential reads.
	raw, _ := json.Marshal(binding)
	record, err := f.store.MemoryStore.Put(context.Background(), resources.Record{TenantID: binding.TenantID, Kind: governance.BindingKind, ID: binding.ID, Spec: raw}, 0)
	if err != nil {
		t.Fatal(err)
	}
	response = f.request(t, http.MethodPost, "/api/v1/iam/native/bindings/credential-binding/read", map[string]any{"binding_revision": record.Revision, "operation": nativeapi.Operation{Name: "authProviders.get", Resource: binding.Allowed[0].Resource}}, "aiplex:governance:*")
	if response.Code == http.StatusOK || f.calls.Load() != 0 {
		t.Fatalf("credential RPC enabled by saved binding: %d calls=%d", response.Code, f.calls.Load())
	}
}

func TestNativeGovernanceProviderDiscoveryFiltersTenant(t *testing.T) {
	f := newNativeHostFixture(t)
	path := "/api/v1/iam/native/providers"
	denied := f.request(t, http.MethodGet, path, nil)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("ungranted discovery: %d", denied.Code)
	}
	response := f.request(t, http.MethodGet, path, nil, "aiplex:governance:providers:read")
	var providers []NativeProviderInfo
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &providers) != nil {
		t.Fatalf("provider discovery: %d %s", response.Code, response.Body.String())
	}
	ids := make([]string, len(providers))
	for i, provider := range providers {
		ids[i] = provider.ID
	}
	if !slices.Equal(ids, []string{"auth-native", "google-native", "google-readonly"}) || strings.Contains(response.Body.String(), "tenant_id") || strings.Contains(response.Body.String(), "operator-native-token") || f.calls.Load() != 0 {
		t.Fatalf("cross-tenant or sensitive provider discovery: %s calls=%d", response.Body.String(), f.calls.Load())
	}
}

func TestNativeGovernanceRuntimeLinkIsExactTenantScopedAndRevocable(t *testing.T) {
	f := newNativeHostFixture(t)
	binding := nativeTestBinding("binding-a", "google-native")
	binding.Registry = &governance.RegistryRef{Provider: "google", TenantID: "project-a", NativeResourceName: nativeTestResource, RuntimeRef: "native-runtime-immutable-id"}
	record := f.saveBinding(t, binding)
	connection := cloudagents.Connection{GovernanceBindingRef: "binding-a", ResourceID: "native-runtime-immutable-id"}
	if err := f.handler.checkAgentGovernanceRef(context.Background(), "tenant-a", connection); err != nil {
		t.Fatalf("declared exact runtime binding rejected: %v", err)
	}
	connection.ResourceID = "different-native-runtime"
	if err := f.handler.checkAgentGovernanceRef(context.Background(), "tenant-a", connection); err == nil {
		t.Fatal("binding was reused for a different immutable runtime")
	}
	connection.ResourceID = "native-runtime-immutable-id"
	if err := f.handler.checkAgentGovernanceRef(context.Background(), "tenant-b", connection); err == nil {
		t.Fatal("runtime relationship crossed Aiplex tenant namespaces")
	}
	if err := f.store.Delete(context.Background(), "tenant-a", governance.BindingKind, binding.ID, record.Revision); err != nil {
		t.Fatal(err)
	}
	if err := f.handler.checkAgentGovernanceRef(context.Background(), "tenant-a", connection); err == nil || f.calls.Load() != 0 {
		t.Fatalf("removed runtime relationship remained usable or triggered native RPC: %v calls=%d", err, f.calls.Load())
	}
}
