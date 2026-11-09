package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/vamsiramakrishnan/aiplex/internal/api"
	"github.com/vamsiramakrishnan/aiplex/pkg/clientplex"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
	sdk "github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

// Exercise the SDK through the actual module mount. Supplying PathValue by hand
// hides differences between the module's Chi router and net/http handlers.
func TestBugbashClientSDKThroughMountedModule(t *testing.T) {
	ctx := context.Background()
	store := resources.NewMemoryStore()
	tenant := func(*http.Request) (string, error) { return "tenant", nil }
	clients := api.NewClientHandler(store, tenant, clientplex.ResolverFunc(func(_ context.Context, tenant, id string) (clientplex.Destination, error) {
		if tenant != "tenant" || id != "models" {
			return clientplex.Destination{}, errors.New("wrong destination namespace")
		}
		return clientplex.Destination{ID: id, Endpoint: "https://models.example/v1", Protocols: []string{clientplex.Responses}}, nil
	}))
	available := builtinModules(nil, nil, nil)
	extendConnectedModules(available, api.NewConnectedHandler(store, nil, tenant, nil, true), clients)
	router := chi.NewRouter()
	router.Route("/api/v1", available["clientplex"].mount)
	server := httptest.NewServer(router)
	defer server.Close()
	client := sdk.NewClient(server.URL)
	registration := sdk.ClientRegistration{ID: "developer", ProfileID: "codex", Mode: "gateway", Protocol: clientplex.Responses, PrincipalBinding: "principal:user", DestinationRef: "models", ModelID: "declared-model", AuthMode: "gateway-key", CredentialEnv: "MODEL_TOKEN", CredentialRef: "credentials/user"}
	created, err := client.PutClientRegistration(ctx, registration, 0)
	if err != nil || created.ID != registration.ID || created.Revision != 1 {
		t.Fatalf("SDK cannot create through mounted ClientPlex: %+v %v", created, err)
	}
	read, err := client.GetClientRegistration(ctx, registration.ID)
	if err != nil || read.Revision != created.Revision || read.ID != created.ID {
		t.Fatal("mounted read lost registration/revision", read, err)
	}
	plan, err := client.PlanClientConnection(ctx, registration.ID)
	if err != nil || plan.RegistrationID != registration.ID || len(plan.Artifacts) == 0 {
		t.Fatal("mounted plan does not resolve saved registration", plan, err)
	}
	registration.ModelID = "another-declared-model"
	updated, err := client.PutClientRegistration(ctx, registration, created.Revision)
	if err != nil || updated.Revision != 2 {
		t.Fatal("mounted update lost CAS", updated, err)
	}
	if _, err := client.PutClientRegistration(ctx, registration, created.Revision); err == nil {
		t.Fatal("mounted route accepted a stale SDK revision")
	}
	if err := client.DeleteClientRegistration(ctx, registration.ID, updated.Revision); err != nil {
		t.Fatal("mounted delete could not resolve registration", err)
	}
	if _, err := client.GetClientRegistration(ctx, registration.ID); err == nil {
		t.Fatal("deleted registration still readable")
	}
}
