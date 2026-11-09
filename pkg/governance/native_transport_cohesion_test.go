package governance

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/pkg/nativeapi"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
)

func TestApplyNativeHTTPRefusalVersusAmbiguousFailure(t *testing.T) {
	for _, test := range []struct {
		status int
		state  string
	}{{http.StatusForbidden, "failed"}, {http.StatusInternalServerError, "unknown"}} {
		t.Run(test.state, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodPatch || r.URL.Path != "/agents/123" {
					t.Errorf("unexpected actual native request %s %s", r.Method, r.URL.Path)
				}
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(`{"clientSecret":"never-in-public-receipt"}`))
			}))
			defer server.Close()
			client, err := nativeapi.NewClient(server.URL, server.Client(), nativeapi.AuthFunc(func(context.Context, *http.Request) error { return nil }))
			if err != nil {
				t.Fatal(err)
			}
			driver := &fixtureDriver{execute: func(ctx context.Context, operation nativeapi.Operation) (nativeapi.Result, error) {
				return client.Do(ctx, http.MethodPatch, operation.Resource, nil, operation.Payload)
			}}
			store := resources.NewMemoryStore()
			controller, binding := fixtureController(t, store, driver, nil)
			plan := fixturePlan(t, controller, binding)
			receipt, err := controller.Apply(context.Background(), fixtureApply(plan))
			if err == nil || receipt.State != test.state || receipt.StatusCode != test.status {
				t.Fatalf("HTTP%d became %+v error=%v", test.status, receipt, err)
			}
			if strings.Contains(err.Error(), "never-in-public-receipt") {
				t.Fatal("upstream error body leaked")
			}
			persisted, err := store.Get(context.Background(), plan.TenantID, ReceiptKind, plan.ID)
			if err != nil {
				t.Fatal(err)
			}
			var stored Receipt
			if json.Unmarshal(persisted.Spec, &stored) != nil || stored.State != test.state || strings.Contains(string(persisted.Spec), "never-in-public-receipt") {
				t.Fatalf("invalid persisted failure classification: %s", persisted.Spec)
			}
			replayed, err := controller.Apply(context.Background(), fixtureApply(plan))
			if err != nil || replayed.State != test.state || requests.Load() != 1 {
				t.Fatal("classified outcome replay caused repeated native HTTP action")
			}
		})
	}
}
