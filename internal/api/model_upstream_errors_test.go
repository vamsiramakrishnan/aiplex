package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/internal/connections"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/pkg/endpointpolicy"
	"github.com/vamsiramakrishnan/aiplex/pkg/modelapi"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

// A provider's 401 is the gateway's credential being refused: the caller gets a
// 502 in the provider's own error shape, the ledger records error_class
// upstream_auth (status error, not a caller denial), and only an operator sees
// the surface id. An OpenAI insufficient_quota 429 is relayed unchanged and
// classed upstream_quota_exhausted rather than rate_limited.
func TestUpstreamAuthAndQuotaAreClassedInTheLedger(t *testing.T) {
	status, body := 401, `{"error":{"message":"Incorrect API key provided: sk-live-PLANTED","type":"invalid_request_error","code":"invalid_api_key"}}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
	defer upstream.Close()
	runtime, err := connections.New(connections.Config{Network: endpointpolicy.Config{AllowedOrigins: []string{upstream.URL}, AllowLoopback: true}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	store := resources.NewMemoryStore()
	h := NewConnectedHandler(store, runtime, func(*http.Request) (string, error) { return "tenant", nil }, nil, true)
	installProviderCredentials(t, h)
	connection := providerConnection(t, upstream.URL, modelapi.Provider{ID: modelapi.ProviderOpenAI}, "openai-chat", bearerRef, "openai.v1.chat-completions")
	if err = h.validate(context.Background(), "tenant", "model-surface", "surface", providerSpecJSON(t, connection)); err != nil {
		t.Fatal(err)
	}
	putProviderSurface(t, store, connection)
	ledger := usageLedgerFor(t, h)
	call := func(roles []models.IAMRole) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/api/v1/llm/native/surface/v1/chat/completions", strings.NewReader(`{"model":"model-v1","messages":[]}`))
		r.SetPathValue("id", "surface")
		identity := &models.WIFIdentity{PrincipalID: "alice"}
		access := &models.ResolvedAccess{Identity: *identity, Scopes: []string{"llm:surface:surface:*", "llm:model:model-v1"}, Roles: roles}
		r = r.WithContext(context.WithValue(context.WithValue(r.Context(), keyWIFIdentity, identity), keyWIFAccess, access))
		r = r.WithContext(context.WithValue(r.Context(), keyRequestID, "upstream-auth-request"))
		w := httptest.NewRecorder()
		h.NativeModel(w, r)
		return w
	}
	member := call(nil)
	operator := call([]models.IAMRole{models.RoleDeployer})
	for name, w := range map[string]*httptest.ResponseRecorder{"member": member, "operator": operator} {
		if w.Code != 502 || !strings.Contains(w.Body.String(), `"code":"upstream_credential_rejected"`) || strings.Contains(w.Body.String(), "sk-live-PLANTED") || strings.Contains(w.Body.String(), "invalid_api_key") {
			t.Fatalf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}
	if strings.Contains(member.Body.String(), "surface_id") || !strings.Contains(operator.Body.String(), `"surface_id":"surface"`) {
		t.Fatalf("surface id visibility: member %s operator %s", member.Body.String(), operator.Body.String())
	}
	records := ledger()
	if len(records) != 2 {
		t.Fatalf("records: %+v", records)
	}
	for _, r := range records {
		if r.ErrorClass != "upstream_auth" || r.Status != usageledger.StatusError || r.HTTPStatus != 502 {
			t.Fatalf("record %+v", r)
		}
	}
	// insufficient_quota.
	status, body = 429, `{"error":{"message":"You exceeded your current quota","type":"insufficient_quota","code":"insufficient_quota"}}`
	w := call(nil)
	if w.Code != 429 || w.Body.String() != body {
		t.Fatalf("quota 429 must be relayed unchanged: %d %s", w.Code, w.Body.String())
	}
	var quota usageledger.Record
	for _, r := range ledger() {
		if r.HTTPStatus == 429 {
			quota = r
		}
	}
	if quota.ErrorClass != "upstream_quota_exhausted" || quota.Status != usageledger.StatusDenied {
		t.Fatalf("quota record %+v", quota)
	}
}
