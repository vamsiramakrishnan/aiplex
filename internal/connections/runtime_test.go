package connections

import (
	"context"
	"errors"
	"github.com/vamsiramakrishnan/aiplex/pkg/endpointpolicy"
	"github.com/vamsiramakrishnan/aiplex/pkg/toolauth"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"testing"
)

type fakeSecrets struct {
	data    []byte
	version string
	err     error
	writes  int
}

func (s *fakeSecrets) GetLatestVersion(context.Context, string) ([]byte, string, error) {
	return append([]byte(nil), s.data...), s.version, s.err
}
func (s *fakeSecrets) PutVersion(_ context.Context, _ string, b []byte) (string, error) {
	s.writes++
	s.data = append([]byte(nil), b...)
	s.version = "projects/p/secrets/x/versions/1"
	return s.version, s.err
}
func TestSecretBackendFailureAndVersionedBinding(t *testing.T) {
	ctx := context.Background()
	key := toolauth.CredentialKey{TenantID: "a", Issuer: "https://issuer.example", ProfileID: "cli"}
	s := &fakeSecrets{err: status.Error(codes.PermissionDenied, "denied")}
	store := SecretCredentials{Manager: s}
	if _, _, e := store.Get(ctx, key); e == nil || errors.Is(e, toolauth.ErrCredentialsNotFound) {
		t.Fatal("backend failure mistaken for absence", e)
	}
	s.err = status.Error(codes.NotFound, "missing")
	if _, _, e := store.Get(ctx, key); !errors.Is(e, toolauth.ErrCredentialsNotFound) {
		t.Fatal(e)
	}
	s.err = nil
	ref, e := store.Put(ctx, key, toolauth.Credential{ClientID: "id", ClientSecret: "secret"})
	if e != nil || ref != s.version {
		t.Fatal(ref, e)
	}
	got, ref2, e := store.Get(ctx, key)
	if e != nil || got.ClientSecret != "secret" || ref2 != ref {
		t.Fatal(e, ref2)
	}
	key.TenantID = "b"
	if _, _, e = store.Get(ctx, key); e == nil {
		t.Fatal("cross tenant credential read")
	}
}
func TestIdentityDestinationBindingAndCopies(t *testing.T) {
	cfg := Config{Network: endpointpolicy.Config{AllowedOrigins: []string{"https://a.example", "https://b.example"}}, Identities: []Identity{{ID: "identity", TenantID: "a", Type: "bearer-env", TokenEnv: "TEST_KEY", AllowedOrigins: []string{"https://a.example"}}}}
	runtime, e := New(cfg, nil)
	if e != nil {
		t.Fatal(e)
	}
	cfg.Identities[0].AllowedOrigins[0] = "https://b.example"
	if e = runtime.CheckDestination("a", "identity", "https://a.example/path"); e != nil {
		t.Fatal(e)
	}
	if e = runtime.CheckDestination("a", "identity", "https://b.example/"); e == nil {
		t.Fatal("credential origin confused with global network allowlist")
	}
	if e = runtime.CheckDestination("b", "identity", "https://a.example"); e == nil {
		t.Fatal("cross tenant credential")
	}
}
