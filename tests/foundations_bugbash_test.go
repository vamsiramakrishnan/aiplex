package tests

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/internal/api"
	"github.com/vamsiramakrishnan/aiplex/internal/auth"
	"github.com/vamsiramakrishnan/aiplex/internal/deploy"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
)

// This adapter represents an identity source after verification. Arbitrary
// request tokens, including caller-supplied IAP assertions, remain untrusted.
type bugbashVerifier struct{}

func (bugbashVerifier) Verify(_ context.Context, token string) (map[string]any, error) {
	switch token {
	case "verified-admin", "verified-deployer", "verified-viewer":
		return map[string]any{"iss": "https://trusted.example", "sub": token, "groups": []any{token}}, nil
	default:
		return nil, errors.New("unverified credential")
	}
}

func TestFoundationsControlPlaneMutationBoundaries(t *testing.T) {
	store := registry.NewMemoryStore()
	for _, role := range []models.IAMRole{models.RoleAdmin, models.RoleDeployer, models.RoleViewer} {
		if err := store.PutRoleBinding(context.Background(), &models.RoleBinding{
			ID: string(role), Group: "verified-" + string(role), Role: role,
			Provider: "https://trusted.example", PoolID: "people",
		}); err != nil {
			t.Fatal(err)
		}
	}
	validator := auth.NewWIFValidator(store, auth.WIFConfig{
		TrustedIssuers: []string{"https://trusted.example"}, WorkforcePoolID: "people",
		IsWorkforce: true, Verifier: bugbashVerifier{},
	})
	leaf := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	handler := api.WIFAuth(validator)(api.RequireIdentity(false)(api.ControlPlaneAccess(false)(leaf)))
	for _, tc := range []struct {
		name, method, path, token, assertion string
		want                                 int
	}{
		{"missing credential", http.MethodPost, "/api/v1/instances", "", "", http.StatusUnauthorized},
		{"forged bearer", http.MethodPost, "/api/v1/iam/role-bindings", "e30.eyJncm91cHMiOlsiYWRtaW4iXX0.fake", "", http.StatusUnauthorized},
		{"forged IAP assertion", http.MethodPost, "/api/v1/iam/role-bindings", "verified-admin", "forged-assertion", http.StatusUnauthorized},
		{"viewer cannot grant roles", http.MethodPost, "/api/v1/iam/role-bindings", "verified-viewer", "", http.StatusForbidden},
		{"deployer cannot grant roles", http.MethodPost, "/api/v1/iam/role-bindings", "verified-deployer", "", http.StatusForbidden},
		{"admin can grant roles", http.MethodPost, "/api/v1/iam/role-bindings", "verified-admin", "", http.StatusNoContent},
		{"deployer cannot alter usage", http.MethodPost, "/api/v1/llm/usage", "verified-deployer", "", http.StatusForbidden},
		{"admin can report usage", http.MethodPost, "/api/v1/llm/usage", "verified-admin", "", http.StatusNoContent},
		{"viewer cannot deploy", http.MethodPost, "/api/v1/instances", "verified-viewer", "", http.StatusForbidden},
		{"deployer can deploy", http.MethodPost, "/api/v1/instances", "verified-deployer", "", http.StatusNoContent},
		{"viewer can discover modules", http.MethodGet, "/api/v1/modules", "verified-viewer", "", http.StatusNoContent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			if tc.token != "" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
			}
			if tc.assertion != "" {
				req.Header.Set("X-Goog-Iap-Jwt-Assertion", tc.assertion)
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, req)
			if recorder.Code != tc.want {
				t.Fatalf("got %d, want %d: %s", recorder.Code, tc.want, recorder.Body.String())
			}
		})
	}
}

// Exercise the same shared deployment entry point used by every Plex. Merely
// hiding a disabled module's own router is insufficient to prevent deployment.
func TestFoundationsDisabledPlaneCannotPersistDeployment(t *testing.T) {
	ctx := context.Background()
	store := registry.NewMemoryStore()
	if err := store.PutTemplate(ctx, &models.Template{
		ID: "disabled-model", Plane: models.PlaneLLMPlex,
		ModelID: "disabled-model", Provider: "google",
	}); err != nil {
		t.Fatal(err)
	}
	engine := deploy.NewEngine(store, "bugbash.local").WithRouteRegistry(deploy.NewRouteRegistry())
	_, err := engine.Deploy(ctx, models.PlaneLLMPlex, "disabled-model", nil, "user", "disabled")
	if !errors.Is(err, deploy.ErrRouteGeneratorMissing) {
		t.Fatalf("disabled plane should reject deployment, got %v", err)
	}
	instances, err := store.ListInstances(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(instances) != 0 {
		t.Fatalf("rejected deployment persisted %d instances", len(instances))
	}
	scopes, err := store.GetUserScopes(ctx, "user")
	if err != nil {
		t.Fatal(err)
	}
	if len(scopes) != 0 {
		t.Fatalf("rejected deployment granted permissions: %v", scopes)
	}
}

// A person belongs to several groups, leaves one, and changes their email.
// Their manual grant survives, the departed group's grant disappears, and
// another identity with the same email receives neither grant.
func TestFoundationsMultiGroupRevocationAndIdentityIsolation(t *testing.T) {
	ctx := context.Background()
	store := registry.NewMemoryStore()
	for _, binding := range []models.RoleBinding{
		{ID: "engineering", Group: "engineering", Provider: "https://issuer.example", PoolID: "people", Role: models.RoleDeployer, Scopes: []string{"llm:model:engineering"}},
		{ID: "research", Group: "research", Provider: "https://issuer.example", PoolID: "people", Role: models.RoleViewer, Scopes: []string{"llm:model:research"}},
	} {
		binding := binding
		if err := store.PutRoleBinding(ctx, &binding); err != nil {
			t.Fatal(err)
		}
	}
	identity := &models.WIFIdentity{
		Subject: "immutable-user-1", Provider: "https://issuer.example", PoolID: "people",
		Email: "person@example.com", Groups: []string{"research", "engineering", "research"},
	}
	principalID := auth.CanonicalPrincipalID(identity.Provider, identity.PoolID, identity.Subject)
	manual := []string{"llm:model:personal"}
	if err := store.SetUserScopes(ctx, principalID, manual); err != nil {
		t.Fatal(err)
	}
	validator := auth.NewWIFValidator(store, auth.WIFConfig{})
	access, err := validator.SyncUserScopes(ctx, identity)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(access.Scopes, []string{"llm:model:engineering", "llm:model:personal", "llm:model:research"}) {
		t.Fatalf("unexpected multi-group grant: %v", access.Scopes)
	}
	identity.Groups = []string{"engineering"}
	identity.Email = "renamed@example.com"
	access, err = validator.SyncUserScopes(ctx, identity)
	if err != nil {
		t.Fatal(err)
	}
	if access.Identity.PrincipalID != principalID || !reflect.DeepEqual(access.Scopes, []string{"llm:model:engineering", "llm:model:personal"}) {
		t.Fatalf("revocation/email rename changed identity or retained grants: %+v", access)
	}
	persisted, err := store.GetUserScopes(ctx, principalID)
	if err != nil || !reflect.DeepEqual(persisted, manual) {
		t.Fatalf("derived grants contaminated manual store: %v, %v", persisted, err)
	}
	other := *identity
	other.Subject = "immutable-user-2"
	other.Groups = nil
	access, err = validator.SyncUserScopes(ctx, &other)
	if err != nil {
		t.Fatal(err)
	}
	if access.Identity.PrincipalID == principalID || len(access.Scopes) != 0 {
		t.Fatalf("same email linked distinct subjects: %+v", access)
	}
	other = *identity
	other.PoolID = "different-people"
	access, err = validator.SyncUserScopes(ctx, &other)
	if err != nil {
		t.Fatal(err)
	}
	if access.Identity.PrincipalID == principalID || len(access.Scopes) != 0 {
		t.Fatalf("pool-scoped group leaked across pools: %+v", access)
	}
}
