package aiplex

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/pkg/nativeapi"
)

func TestNativeGovernanceSDKUsesReviewedRoutesAndBodies(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("missing API authentication")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/iam/native/providers":
			if r.Method != http.MethodGet {
				t.Error("providers must GET")
			}
			json.NewEncoder(w).Encode([]NativeProviderInfo{{ID: "google", Type: "google-registry", Status: "configured"}})
		case "/api/v1/iam/native/bindings/binding-1/read":
			var req NativeReadRequest
			if json.NewDecoder(r.Body).Decode(&req) != nil || req.BindingRevision != 7 || req.Operation.Name != "services.get" || r.Method != http.MethodPost {
				t.Error("read route or revision lost")
			}
			json.NewEncoder(w).Encode(NativeObservation{ID: "obs-1", BindingRevision: 7})
		case "/api/v1/iam/native/bindings/binding-1/plan":
			var req NativePlanRequest
			if json.NewDecoder(r.Body).Decode(&req) != nil || req.BindingRevision != 7 || req.DesiredID != "desired-1" || req.TTLSeconds != 300 {
				t.Error("plan review inputs lost")
			}
			json.NewEncoder(w).Encode(NativePlan{ID: "plan-1", Revision: 2, Digest: "sha256:reviewed"})
		case "/api/v1/iam/native/plans/plan-1/apply":
			var req NativeApplyRequest
			if json.NewDecoder(r.Body).Decode(&req) != nil || req.PlanRevision != 2 || req.ApprovalDigest != "sha256:reviewed" {
				t.Error("apply approval inputs lost")
			}
			json.NewEncoder(w).Encode(NativeReceipt{PlanID: "plan-1", State: "succeeded"})
		case "/api/v1/iam/native/plans/plan-1":
			json.NewEncoder(w).Encode(NativePlan{ID: "plan-1", Revision: 2})
		case "/api/v1/iam/native/receipts/plan-1":
			json.NewEncoder(w).Encode(NativeReceipt{PlanID: "plan-1", State: "succeeded"})
		case "/api/v1/iam/native/bindings/binding-1":
			if r.Method != http.MethodPut || r.Header.Get("If-Match") != "7" {
				t.Error("binding revision guard lost")
			}
			json.NewEncoder(w).Encode(map[string]any{"id": "binding-1", "revision": 8, "spec": map[string]string{"id": "binding-1"}})
		default:
			t.Errorf("unexpected native route: %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	client := NewClient(server.URL)
	client.SetToken("test-token")
	ctx := context.Background()
	providers, e := client.ListNativeProviders(ctx)
	if e != nil || len(providers) != 1 || providers[0].ID != "google" {
		t.Fatal(providers, e)
	}
	observation, e := client.ReadNativeBinding(ctx, "binding-1", NativeReadRequest{Operation: nativeapi.Operation{Name: "services.get", Resource: "projects/p/locations/l/services/s"}, BindingRevision: 7})
	if e != nil || observation.ID != "obs-1" {
		t.Fatal(observation, e)
	}
	plan, e := client.PlanNativeBinding(ctx, "binding-1", NativePlanRequest{DesiredID: "desired-1", BindingRevision: 7, TTLSeconds: 300})
	if e != nil || plan.Digest != "sha256:reviewed" {
		t.Fatal(plan, e)
	}
	receipt, e := client.ApplyNativePlan(ctx, "plan-1", NativeApplyRequest{PlanRevision: 2, ApprovalDigest: plan.Digest})
	if e != nil || receipt.State != "succeeded" {
		t.Fatal(receipt, e)
	}
	if _, e = client.GetNativePlan(ctx, "plan-1"); e != nil {
		t.Fatal(e)
	}
	if _, e = client.GetNativeReceipt(ctx, "plan-1"); e != nil {
		t.Fatal(e)
	}
	if record, e := client.PutResource(ctx, AgentBindings, "binding-1", map[string]string{"id": "binding-1"}, 7); e != nil || record.Revision != 8 {
		t.Fatal(record, e)
	}
	if calls != 7 {
		t.Fatalf("unexpected request count %d", calls)
	}
}

func TestNativeGovernanceSDKRejectsUnreviewedInputsBeforeNetwork(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(500) }))
	defer server.Close()
	client := NewClient(server.URL)
	ctx := context.Background()
	for _, id := range []string{"../other", "a/b", "a%2fb", ""} {
		if _, err := client.GetNativePlan(ctx, id); err == nil {
			t.Fatalf("unsafe ID %q", id)
		}
	}
	if _, err := client.ApplyNativePlan(ctx, "plan-1", NativeApplyRequest{PlanRevision: 1}); err == nil {
		t.Fatal("missing digest accepted")
	}
	if _, err := client.ApplyNativePlan(ctx, "plan-1", NativeApplyRequest{ApprovalDigest: "digest"}); err == nil {
		t.Fatal("missing revision accepted")
	}
	if _, err := client.PlanNativeBinding(ctx, "binding-1", NativePlanRequest{BindingRevision: 1, TTLSeconds: 300}); err == nil {
		t.Fatal("missing desired action accepted")
	}
	if _, err := client.ReadNativeBinding(ctx, "binding-1", NativeReadRequest{BindingRevision: 1, Operation: nativeapi.Operation{Name: "read", Resource: "resource", Payload: json.RawMessage(`{"access_token":"secret"}`)}}); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatal("credential payload accepted or leaked", err)
	}
	if calls != 0 {
		t.Fatalf("invalid input made %d HTTP calls", calls)
	}
}
