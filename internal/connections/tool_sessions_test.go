package connections

import (
	"context"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/pkg/toolauth"
)

func TestSecretRegistrationPersistsRefreshGrantCapability(t *testing.T) {
	backend := &fakeSecrets{}
	store := SecretCredentials{Manager: backend}
	key := toolauth.CredentialKey{TenantID: "tenant", Issuer: "https://issuer.example", ProfileID: "profile"}
	if _, e := store.Put(context.Background(), key, toolauth.Credential{ClientID: "client", GrantTypes: []string{"authorization_code", "refresh_token"}}); e != nil {
		t.Fatal(e)
	}
	value, _, e := store.Get(context.Background(), key)
	if e != nil || len(value.GrantTypes) != 2 || value.GrantTypes[1] != "refresh_token" {
		t.Fatal(value, e)
	}
}
