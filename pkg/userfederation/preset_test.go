package userfederation

import (
	"strings"
	"testing"
)

func TestCatalogStable(t *testing.T) {
	want := []string{PresetGoogleWorkforcePool, PresetGoogleWorkspace, PresetEntraID, PresetOkta, PresetOIDC}
	got := Catalog()
	if len(got) != len(want) {
		t.Fatalf("catalog = %d presets", len(got))
	}
	for i, p := range got {
		if p.ID != want[i] || p.DisplayName == "" || p.Description == "" || len(p.Algorithms) != 3 || len(p.GroupSources) == 0 {
			t.Fatalf("preset %d = %+v", i, p)
		}
		for _, f := range p.Inputs {
			if f.Name == "" || f.Label == "" || f.Description == "" {
				t.Fatalf("%s input %+v lacks label or description", p.ID, f)
			}
		}
		groups := false
		for _, f := range p.Inputs {
			if f.Name == "groups_from" {
				groups = f.Required && f.Default == ""
			}
		}
		if !groups && p.ID != PresetGoogleWorkspace {
			t.Fatalf("%s must require an explicit groups_from", p.ID)
		}
	}
}

func TestDeriveTable(t *testing.T) {
	const tenant = "72f988bf-86f1-41af-91ab-2d7cd011db47"
	type want struct {
		issuer, jwks, subjectClaim, groupsFrom, groupClaim string
		required                                           map[string]string
	}
	ok := []struct {
		name   string
		preset string
		in     map[string]string
		extra  map[string]string
		want   want
	}{
		{"workspace", PresetGoogleWorkspace, map[string]string{"hosted_domain": "example.com", "audience": "client.apps.googleusercontent.com", "groups_from": "membership-authority"}, nil,
			want{"https://accounts.google.com", "https://www.googleapis.com/oauth2/v3/certs", "sub", "membership-authority", "", map[string]string{"hd": "example.com"}}},
		{"entra v2 default group claim", PresetEntraID, map[string]string{"tenant_id": tenant, "audience": "client-id", "groups_from": "claim"}, nil,
			want{"https://login.microsoftonline.com/" + tenant + "/v2.0", "https://login.microsoftonline.com/" + tenant + "/discovery/v2.0/keys", "oid", "claim", "groups", map[string]string{"tid": tenant}}},
		{"entra v1 roles", PresetEntraID, map[string]string{"tenant_id": tenant, "audience": "api://aiplex", "token_version": "1.0", "groups_from": "claim", "group_claim": "roles"}, nil,
			want{"https://sts.windows.net/" + tenant + "/", "https://login.microsoftonline.com/" + tenant + "/discovery/v2.0/keys", "oid", "claim", "roles", map[string]string{"tid": tenant}}},
		{"okta", PresetOkta, map[string]string{"issuer": "https://acme.okta.com/oauth2/default", "jwks_url": "https://acme.okta.com/oauth2/default/v1/keys", "audience": "api://aiplex", "groups_from": "claim"}, nil,
			want{"https://acme.okta.com/oauth2/default", "https://acme.okta.com/oauth2/default/v1/keys", "sub", "claim", "groups", nil}},
		{"workforce pool nested claim", PresetGoogleWorkforcePool, map[string]string{"issuer": "https://idp.example.com", "jwks_url": "https://idp.example.com/keys", "audience": "//iam.googleapis.com/locations/global/workforcePools/p/providers/q", "pool_id": "acme-employees", "groups_from": "claim", "group_claim": "google.groups"}, nil,
			want{"https://idp.example.com", "https://idp.example.com/keys", "sub", "claim", "google.groups", nil}},
		{"oidc oid with tenant claim", PresetOIDC, map[string]string{"issuer": "https://idp.example.com", "jwks_url": "https://idp.example.com/keys", "audience": "aiplex", "subject_claim": "oid", "groups_from": "membership-authority"}, map[string]string{"org": "acme"},
			want{"https://idp.example.com", "https://idp.example.com/keys", "oid", "membership-authority", "", map[string]string{"org": "acme"}}},
	}
	for _, c := range ok {
		t.Run(c.name, func(t *testing.T) {
			got, err := Derive(c.preset, c.in, c.extra)
			if err != nil {
				t.Fatal(err)
			}
			if got.Issuer != c.want.issuer || got.JWKSURL != c.want.jwks || got.SubjectClaim != c.want.subjectClaim || got.GroupsFrom != c.want.groupsFrom || got.GroupClaim != c.want.groupClaim || got.Audience != c.in["audience"] {
				t.Fatalf("derived %+v", got)
			}
			if len(got.RequiredClaims) != len(c.want.required) {
				t.Fatalf("required claims = %v", got.RequiredClaims)
			}
			for k, v := range c.want.required {
				if got.RequiredClaims[k] != v {
					t.Fatalf("required claim %s = %q", k, got.RequiredClaims[k])
				}
			}
		})
	}
}

func TestDeriveRejects(t *testing.T) {
	const tenant = "72f988bf-86f1-41af-91ab-2d7cd011db47"
	entra := func(m map[string]string) map[string]string {
		base := map[string]string{"tenant_id": tenant, "audience": "client-id", "groups_from": "claim"}
		for k, v := range m {
			if v == "" {
				delete(base, k)
			} else {
				base[k] = v
			}
		}
		return base
	}
	okta := func(m map[string]string) map[string]string {
		base := map[string]string{"issuer": "https://acme.okta.com", "jwks_url": "https://acme.okta.com/oauth2/v1/keys", "audience": "aiplex", "groups_from": "claim"}
		for k, v := range m {
			if v == "" {
				delete(base, k)
			} else {
				base[k] = v
			}
		}
		return base
	}
	cases := []struct {
		name   string
		preset string
		in     map[string]string
		extra  map[string]string
		want   string
	}{
		{"unknown preset", "nope", nil, nil, "unknown"},
		{"unknown input", PresetEntraID, entra(map[string]string{"extra": "x"}), nil, "not defined"},
		{"entra common endpoint", PresetEntraID, entra(map[string]string{"tenant_id": "common"}), nil, "GUID"},
		{"entra upper-case tenant", PresetEntraID, entra(map[string]string{"tenant_id": strings.ToUpper(tenant)}), nil, "GUID"},
		{"entra missing audience", PresetEntraID, entra(map[string]string{"audience": ""}), nil, "audience"},
		{"wildcard audience", PresetEntraID, entra(map[string]string{"audience": "api://*"}), nil, "audience"},
		{"missing groups_from", PresetEntraID, entra(map[string]string{"groups_from": ""}), nil, "groups_from"},
		{"bad groups_from", PresetEntraID, entra(map[string]string{"groups_from": "guess"}), nil, "one of"},
		{"authority with claim name", PresetEntraID, entra(map[string]string{"groups_from": "membership-authority", "group_claim": "groups"}), nil, "must be empty"},
		{"reserved group claim", PresetEntraID, entra(map[string]string{"group_claim": "sub"}), nil, "group_claim"},
		{"group claim traversal", PresetEntraID, entra(map[string]string{"group_claim": "a..b"}), nil, "group_claim"},
		{"tid conflict", PresetEntraID, entra(nil), map[string]string{"tid": "other"}, "conflicts"},
		{"workspace claim groups", PresetGoogleWorkspace, map[string]string{"hosted_domain": "example.com", "audience": "c", "groups_from": "claim"}, nil, "membership-authority"},
		{"workspace domain upper", PresetGoogleWorkspace, map[string]string{"hosted_domain": "Example.com", "audience": "c", "groups_from": "membership-authority"}, nil, "domain"},
		{"workspace wildcard domain", PresetGoogleWorkspace, map[string]string{"hosted_domain": "*.example.com", "audience": "c", "groups_from": "membership-authority"}, nil, "domain"},
		{"workspace missing domain", PresetGoogleWorkspace, map[string]string{"audience": "c", "groups_from": "membership-authority"}, nil, "hosted_domain"},
		{"workspace group_claim input", PresetGoogleWorkspace, map[string]string{"hosted_domain": "example.com", "audience": "c", "groups_from": "membership-authority", "group_claim": "groups"}, nil, "not defined"},
		{"okta http issuer", PresetOkta, okta(map[string]string{"issuer": "http://acme.okta.com"}), nil, "https"},
		{"okta wildcard issuer", PresetOkta, okta(map[string]string{"issuer": "https://*.okta.com"}), nil, "https"},
		{"okta issuer query", PresetOkta, okta(map[string]string{"issuer": "https://acme.okta.com/?x=1"}), nil, "https"},
		{"okta missing jwks", PresetOkta, okta(map[string]string{"jwks_url": ""}), nil, "jwks_url"},
		{"okta jwks http", PresetOkta, okta(map[string]string{"jwks_url": "http://acme.okta.com/keys"}), nil, "https"},
		{"workforce missing issuer", PresetGoogleWorkforcePool, map[string]string{"jwks_url": "https://x.example/keys", "audience": "a", "groups_from": "membership-authority"}, nil, "issuer"},
		{"workforce claim without name", PresetGoogleWorkforcePool, map[string]string{"issuer": "https://x.example", "jwks_url": "https://x.example/keys", "audience": "a", "groups_from": "claim"}, nil, "group_claim"},
		{"workforce bad pool", PresetGoogleWorkforcePool, map[string]string{"issuer": "https://x.example", "jwks_url": "https://x.example/keys", "audience": "a", "groups_from": "membership-authority", "pool_id": "Bad_Pool"}, nil, "pool"},
		{"oidc bad subject claim", PresetOIDC, map[string]string{"issuer": "https://x.example", "jwks_url": "https://x.example/keys", "audience": "a", "groups_from": "membership-authority", "subject_claim": "email"}, nil, "one of"},
		{"oidc reserved required claim", PresetOIDC, map[string]string{"issuer": "https://x.example", "jwks_url": "https://x.example/keys", "audience": "a", "groups_from": "membership-authority"}, map[string]string{"sub": "x"}, "reserved"},
		{"oidc wildcard required claim", PresetOIDC, map[string]string{"issuer": "https://x.example", "jwks_url": "https://x.example/keys", "audience": "a", "groups_from": "membership-authority"}, map[string]string{"org": "ac*"}, "wildcards"},
		{"padded value", PresetOIDC, map[string]string{"issuer": " https://x.example", "jwks_url": "https://x.example/keys", "audience": "a", "groups_from": "membership-authority"}, nil, "whitespace"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Derive(c.preset, c.in, c.extra)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want error containing %q, got %v", c.want, err)
			}
		})
	}
}

func TestProviderIDValidation(t *testing.T) {
	for _, id := range []string{"verify", "", "Upper", "-a", "a_b", strings.Repeat("a", 65)} {
		if ValidProviderID(id) {
			t.Fatalf("%q accepted", id)
		}
	}
	if !ValidProviderID("corp-sso") {
		t.Fatal("valid id rejected")
	}
}
