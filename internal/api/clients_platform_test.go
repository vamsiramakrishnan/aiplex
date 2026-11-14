package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/pkg/clientplex"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
)

// platform was removed from registrations: a PUT that carries it is refused as
// an unknown field, and a record stored while it existed still reads, lists and
// plans, and can be replaced once the field is dropped from the document.
func TestClientRegistrationPlatformFieldIsRefusedOnPutButLegacyRecordsStillWork(t *testing.T) {
	store := resources.NewMemoryStore()
	h := clientTestHandler("a", store)
	withPlatform := strings.Replace(clientTestBody, `"id":"dev"`, `"platform":"macos","id":"dev"`, 1)
	w := httptest.NewRecorder()
	h.Put(w, clientTestRequest("PUT", withPlatform, ""))
	if w.Code != 400 || !strings.Contains(w.Body.String(), "INVALID_REGISTRATION") || !strings.Contains(w.Body.String(), "platform is no longer a registration field and must be removed") {
		t.Fatalf("platform accepted or the refusal does not say what to do: %d %s", w.Code, w.Body.String())
	}
	// Every other unknown member keeps the generic decoder message.
	w = httptest.NewRecorder()
	h.Put(w, clientTestRequest("PUT", strings.Replace(clientTestBody, `"id":"dev"`, `"colour":"red","id":"dev"`, 1), ""))
	if w.Code != 400 || !strings.Contains(w.Body.String(), `unknown field \"colour\"`) || strings.Contains(w.Body.String(), "no longer") {
		t.Fatalf("generic unknown-field message changed: %d %s", w.Code, w.Body.String())
	}

	// A record stored before the field was removed.
	legacy := strings.Replace(clientTestBody, `"id":"dev"`, `"platform":"linux","id":"dev"`, 1)
	if _, e := store.Put(context.Background(), resources.Record{TenantID: "a", Kind: clientplex.Kind, ID: "dev", Spec: json.RawMessage(legacy)}, 0); e != nil {
		t.Fatal(e)
	}
	w = httptest.NewRecorder()
	h.Get(w, clientTestRequest("GET", "", ""))
	if w.Code != 200 {
		t.Fatalf("legacy record unreadable: %d %s", w.Code, w.Body.String())
	}
	var envelope struct {
		Spec json.RawMessage `json:"spec"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &envelope)
	w = httptest.NewRecorder()
	h.List(w, clientTestRequest("GET", "", ""))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"dev"`) {
		t.Fatalf("legacy record missing from the list: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	h.Plan(w, clientTestRequest("POST", "", ""))
	if w.Code != 200 {
		t.Fatalf("legacy record does not plan: %d %s", w.Code, w.Body.String())
	}
	// Writing the document back exactly as read is refused until platform is dropped.
	w = httptest.NewRecorder()
	h.Put(w, clientTestRequest("PUT", string(envelope.Spec), `"1"`))
	if w.Code != 400 {
		t.Fatalf("read-back document with platform: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	h.Put(w, clientTestRequest("PUT", clientTestBody, `"1"`))
	if w.Code != 200 {
		t.Fatalf("replacement without platform: %d %s", w.Code, w.Body.String())
	}
	record, e := store.Get(context.Background(), "a", clientplex.Kind, "dev")
	if e != nil || strings.Contains(string(record.Spec), "platform") {
		t.Fatalf("platform survived the replacement: %v %s", e, record.Spec)
	}
}

// managed on Codex promises a policy Codex has no managed configuration to carry.
// A record stored earlier reads, its plan is refused naming the field, and a PUT
// that keeps the field is refused the same way.
func TestClientRegistrationWithNewlyRefusedFieldReadsButDoesNotPlan(t *testing.T) {
	store := resources.NewMemoryStore()
	h := clientTestHandler("a", store)
	stored := strings.Replace(clientTestBody, `"id":"dev"`, `"managed":true,"id":"dev"`, 1)
	if _, e := store.Put(context.Background(), resources.Record{TenantID: "a", Kind: clientplex.Kind, ID: "dev", Spec: json.RawMessage(stored)}, 0); e != nil {
		t.Fatal(e)
	}
	w := httptest.NewRecorder()
	h.Get(w, clientTestRequest("GET", "", ""))
	if w.Code != 200 {
		t.Fatalf("unreadable: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	h.Plan(w, clientTestRequest("POST", "", ""))
	if w.Code != 409 || !strings.Contains(w.Body.String(), "managed") {
		t.Fatalf("plan: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	h.Put(w, clientTestRequest("PUT", stored, `"1"`))
	if w.Code != 400 || !strings.Contains(w.Body.String(), "managed") {
		t.Fatalf("put: %d %s", w.Code, w.Body.String())
	}
}

// The Console starter is a direct Codex registration with project_id and
// location. Switched to gateway mode and saved, it planned before the Gemini
// work and must still plan, with a warning for each field that no longer applies.
func TestClientConsoleStarterSwitchedToGatewayStillPlansWithWarnings(t *testing.T) {
	store := resources.NewMemoryStore()
	h := clientTestHandler("a", store)
	body := `{"id":"dev","profile_id":"codex","mode":"gateway","protocol":"openai-responses","destination_ref":"models","principal_binding":"replace-with-canonical-principal","project_id":"your-project","location":"global","model_id":"replace-with-enabled-model-id","auth_mode":"gateway-key","credential_env":"AIPLEX_TOKEN","credential_ref":"credentials/user"}`
	w := httptest.NewRecorder()
	h.Put(w, clientTestRequest("PUT", body, ""))
	if w.Code != 201 {
		t.Fatalf("put: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	h.Plan(w, clientTestRequest("POST", "", ""))
	if w.Code != 200 {
		t.Fatalf("plan: %d %s", w.Code, w.Body.String())
	}
	for _, field := range []string{"project_id", "location"} {
		if !strings.Contains(w.Body.String(), "Registration field "+field+" has no effect") {
			t.Errorf("no warning for %s: %s", field, w.Body.String())
		}
	}
}

// allowed_models in direct mode promises a restriction no gateway enforces: a
// new write is refused, and a record stored earlier reads but gets no plan, each
// naming the field and what to remove.
func TestClientAllowedModelsInDirectModeIsRefusedOnWriteAndPlan(t *testing.T) {
	store := resources.NewMemoryStore()
	h := clientTestHandler("a", store)
	direct := `{"id":"dev","profile_id":"codex","mode":"direct","protocol":"openai-responses","principal_binding":"principal:user","project_id":"example-project","location":"global","auth_mode":"adc","allowed_models":["gpt-example"]}`
	w := httptest.NewRecorder()
	h.Put(w, clientTestRequest("PUT", direct, ""))
	if w.Code != 400 || !strings.Contains(w.Body.String(), "allowed_models") || !strings.Contains(w.Body.String(), "remove allowed_models") {
		t.Fatalf("write: %d %s", w.Code, w.Body.String())
	}
	if _, e := store.Put(context.Background(), resources.Record{TenantID: "a", Kind: clientplex.Kind, ID: "dev", Spec: json.RawMessage(direct)}, 0); e != nil {
		t.Fatal(e)
	}
	w = httptest.NewRecorder()
	h.Get(w, clientTestRequest("GET", "", ""))
	if w.Code != 200 {
		t.Fatalf("read: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	h.Plan(w, clientTestRequest("POST", "", ""))
	if w.Code != 409 || !strings.Contains(w.Body.String(), "allowed_models") || !strings.Contains(w.Body.String(), "remove allowed_models") {
		t.Fatalf("plan: %d %s", w.Code, w.Body.String())
	}
}
