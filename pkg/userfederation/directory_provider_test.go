package userfederation

import (
	"strings"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/pkg/directory"
)

const testTID = "72f988bf-86f1-41af-91ab-2d7cd011db47"

func entraReq() ProviderRequest {
	return ProviderRequest{Preset: PresetEntraID, Inputs: map[string]string{"tenant_id": testTID, "audience": "api://aiplex", "groups_from": "claim"}}
}

func TestClientClaimDefaultsPerPreset(t *testing.T) {
	cases := map[string]struct {
		req  ProviderRequest
		want string
	}{
		"entra v2": {entraReq(), "azp"},
		"entra v1": {ProviderRequest{Preset: PresetEntraID, Inputs: map[string]string{"tenant_id": testTID, "audience": "api://aiplex", "token_version": "1.0", "groups_from": "claim"}}, "appid"},
		"okta":     {ProviderRequest{Preset: PresetOkta, Inputs: map[string]string{"issuer": "https://acme.okta.com/oauth2/default", "jwks_url": "https://acme.okta.com/oauth2/default/v1/keys", "audience": "api://aiplex", "groups_from": "claim"}}, "cid"},
		"google":   {ProviderRequest{Preset: PresetGoogleWorkspace, Inputs: map[string]string{"hosted_domain": "example.com", "audience": "x.apps.googleusercontent.com", "groups_from": "membership-authority"}}, ""},
		"oidc":     {ProviderRequest{Preset: PresetOIDC, Inputs: map[string]string{"issuer": "https://idp.test", "jwks_url": "https://idp.test/jwks", "audience": "a", "groups_from": "claim", "group_claim": "groups"}}, ""},
	}
	for name, tc := range cases {
		p, err := NewProvider("p", tc.req)
		if err != nil || p.ClientClaim != tc.want {
			t.Fatalf("%s: client claim %q, want %q (%v)", name, p.ClientClaim, tc.want, err)
		}
	}
	for _, p := range Catalog() {
		if p.ClientClaimDefault == "" {
			t.Fatalf("%s documents no client claim default", p.ID)
		}
	}
	// Explicit claim, allowlist, none, reserved names.
	req := entraReq()
	req.ClientClaim, req.ClientIDs = "appid", []string{"b", "a", "a"}
	p, err := NewProvider("p", req)
	if err != nil || p.ClientClaim != "appid" || strings.Join(p.ClientIDs, ",") != "a,b" {
		t.Fatalf("explicit = %+v %v", p.Trust, err)
	}
	req.ClientClaim = ClientClaimNone
	if _, err := NewProvider("p", req); err == nil {
		t.Fatal("client_ids accepted without a client claim")
	}
	for _, reserved := range []string{"sub", "iss", "groups", "oid", "a.b"} {
		req := entraReq()
		req.ClientClaim = reserved
		if _, err := NewProvider("p", req); err == nil {
			t.Fatalf("client_claim %q accepted", reserved)
		}
	}
}

func TestProviderDirectoryValidation(t *testing.T) {
	entraDir := &directory.Spec{Kind: directory.KindEntraGraph, Credential: directory.Credential{ClientID: "99998888-aaaa-7777-bbbb-666655554444", ClientSecretRef: "graph-reader"}}
	req := entraReq()
	req.Directory = entraDir
	p, err := NewProvider("p", req)
	if err != nil || p.Directory.Mode != directory.ModeFallback || p.DirectoryMode() != directory.ModeFallback {
		t.Fatalf("entra directory = %+v %v", p.Directory, err)
	}
	// Wrong kind for the preset.
	req.Directory = &directory.Spec{Kind: directory.KindOkta, OrgURL: "https://acme.okta.com", Credential: directory.Credential{APITokenRef: "t"}}
	if _, err := NewProvider("p", req); err == nil || !strings.Contains(err.Error(), "entra-graph only") {
		t.Fatalf("okta directory on entra preset: %v", err)
	}
	// The generic OIDC preset has no directory.
	oidc := ProviderRequest{Preset: PresetOIDC, Inputs: map[string]string{"issuer": "https://idp.test", "jwks_url": "https://idp.test/jwks", "audience": "a", "groups_from": "claim", "group_claim": "groups"}, Directory: entraDir}
	if _, err := NewProvider("p", oidc); err == nil {
		t.Fatal("directory accepted on oidc preset")
	}
	// Okta fallback must say which identifier matches the token's groups.
	okta := ProviderRequest{Preset: PresetOkta, Inputs: map[string]string{"issuer": "https://acme.okta.com/oauth2/default", "jwks_url": "https://acme.okta.com/oauth2/default/v1/keys", "audience": "api://aiplex", "groups_from": "claim"},
		Directory: &directory.Spec{Kind: directory.KindOkta, OrgURL: "https://acme.okta.com", Credential: directory.Credential{APITokenRef: "okta-token"}}}
	if _, err := NewProvider("p", okta); err == nil || !strings.Contains(err.Error(), "group_id") {
		t.Fatalf("okta fallback without group_id: %v", err)
	}
	okta.Directory.GroupID = directory.GroupIDName
	if p, err := NewProvider("p", okta); err != nil || p.Directory.GroupID != "name" {
		t.Fatalf("okta fallback with group_id: %+v %v", p.Directory, err)
	}
	okta.Directory.GroupID, okta.Directory.Mode = "", directory.ModeAlways
	if p, err := NewProvider("p", okta); err != nil || p.Directory.GroupID != "id" {
		t.Fatalf("okta always: %+v %v", p.Directory, err)
	}
}
