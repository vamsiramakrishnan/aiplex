package entra

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type identityClaimsFunc func(context.Context, string) (map[string]any, error)

func (f identityClaimsFunc) Verify(ctx context.Context, token string) (map[string]any, error) {
	return f(ctx, token)
}

type nilIdentityVerifier struct{}

func (*nilIdentityVerifier) Verify(context.Context, string) (map[string]any, error) {
	panic("typed nil verifier called")
}

func nativeIdentityConfig() IdentityConfig {
	return IdentityConfig{TenantID: tenantID, AllowedAgents: []AgentBinding{{ObjectID: agentID, AppID: agentAppID}}, AllowDelegated: true, RequiredScopes: []string{"quota.read", "quota.use"}, RequiredRoles: []string{"Quota.Invoke"}}
}

func nativeIdentityClaims(delegated bool) map[string]any {
	claims := map[string]any{
		"tid": tenantID, "ver": "2.0", "azp": agentAppID, "oid": agentID,
		"sub": "native-pairwise-subject", "idtyp": "app", "xms_act_fct": "3 9 11", "xms_sub_fct": "9 3 11",
		"roles": []string{"Quota.Invoke", "Unrelated.Role"}, "scp": "/", "xms_par_app_azp": blueprintAppID,
		"groups": []string{"verified-native-group"}, "nested": map[string]any{"items": []any{"original"}},
		"act": map[string]any{"sub": "must-not-be-trusted-as-native-actor"},
	}
	if delegated {
		claims["oid"], claims["idtyp"], claims["scp"] = userID, "user", "quota.read quota.use extra.scope"
		delete(claims, "xms_sub_fct")
		delete(claims, "roles")
	}
	return claims
}

func TestEntraIdentityNormalizesAutonomousAndDelegatedWithoutMutation(t *testing.T) {
	for _, delegated := range []bool{false, true} {
		name := "autonomous"
		if delegated {
			name = "delegated"
		}
		t.Run(name, func(t *testing.T) {
			original := nativeIdentityClaims(delegated)
			config := nativeIdentityConfig()
			calls := 0
			verifier, err := NewIdentityVerifier(config, identityClaimsFunc(func(_ context.Context, token string) (map[string]any, error) {
				calls++
				if token != "incoming-resource-token" {
					t.Fatal("cryptographic verifier did not receive original credential")
				}
				return original, nil
			}))
			if err != nil {
				t.Fatal(err)
			}
			// Every operator-owned slice must be frozen by the constructor.
			config.AllowedAgents[0] = AgentBinding{ObjectID: foreignID, AppID: foreignID}
			config.RequiredScopes[0], config.RequiredRoles[0] = "unauthorized.scope", "Unauthorized.Role"
			normalized, err := verifier.Verify(context.Background(), "incoming-resource-token")
			if err != nil || calls != 1 || normalized["azp"] != agentAppID || normalized["entra_native_sub"] != "native-pairwise-subject" || normalized["xms_par_app_azp"] != blueprintAppID {
				t.Fatalf("native normalization: %v %v calls=%d", normalized, err, calls)
			}
			if delegated {
				actor, ok := normalized["act"].(map[string]any)
				if normalized["sub"] != userID || !ok || actor["sub"] != agentID {
					t.Fatalf("delegated user and native agent collapsed: %v", normalized)
				}
			} else if normalized["sub"] != agentID || normalized["act"] != nil {
				t.Fatalf("autonomous actor or subject was inherited from untrusted semantics: %v", normalized)
			}
			normalized["nested"].(map[string]any)["items"].([]any)[0] = "changed"
			normalized["groups"].([]any)[0] = "changed"
			if original["sub"] != "native-pairwise-subject" || original["act"].(map[string]any)["sub"] != "must-not-be-trusted-as-native-actor" || original["nested"].(map[string]any)["items"].([]any)[0] != "original" || original["groups"].([]string)[0] != "verified-native-group" {
				t.Fatal("normalization or downstream consumer modified shared verified claims")
			}
		})
	}
}

func TestEntraIdentityVersionOneAndFacetOrdering(t *testing.T) {
	claims := nativeIdentityClaims(false)
	claims["ver"], claims["appid"], claims["xms_act_fct"], claims["xms_sub_fct"] = "1.0", agentAppID, "101 11 3", "11 9 99"
	delete(claims, "azp")
	v, err := NewIdentityVerifier(nativeIdentityConfig(), identityClaimsFunc(func(context.Context, string) (map[string]any, error) { return claims, nil }))
	if err != nil {
		t.Fatal(err)
	}
	got, err := v.Verify(context.Background(), "token")
	if err != nil || got["sub"] != agentID || got["azp"] != agentAppID || got["appid"] != agentAppID {
		t.Fatalf("native v1 actor or unordered facets rejected: %v %v", got, err)
	}
}

func TestEntraIdentityRejectsNativeIdentityAndPermissionConfusion(t *testing.T) {
	for _, test := range []struct {
		name      string
		delegated bool
		change    func(map[string]any, *IdentityConfig)
	}{
		{"wrong tenant", false, func(c map[string]any, _ *IdentityConfig) { c["tid"] = foreignID }},
		{"wrong application", false, func(c map[string]any, _ *IdentityConfig) { c["azp"] = foreignID }},
		{"parent blueprint does not grant child", false, func(c map[string]any, _ *IdentityConfig) { c["azp"] = foreignID; c["xms_par_app_azp"] = agentAppID }},
		{"wrong agent object", false, func(c map[string]any, _ *IdentityConfig) { c["oid"] = foreignID }},
		{"missing object", false, func(c map[string]any, _ *IdentityConfig) { delete(c, "oid") }},
		{"object email alias", false, func(c map[string]any, _ *IdentityConfig) { c["oid"] = "agent@example.com" }},
		{"zero object", false, func(c map[string]any, _ *IdentityConfig) { c["oid"] = "00000000-0000-0000-0000-000000000000" }},
		{"missing token version", false, func(c map[string]any, _ *IdentityConfig) { delete(c, "ver") }},
		{"conflicting application claims", false, func(c map[string]any, _ *IdentityConfig) { c["appid"] = foreignID }},
		{"missing actor facet", false, func(c map[string]any, _ *IdentityConfig) { delete(c, "xms_act_fct") }},
		{"actor facet substring", false, func(c map[string]any, _ *IdentityConfig) { c["xms_act_fct"] = "3 111" }},
		{"actor facet array", false, func(c map[string]any, _ *IdentityConfig) { c["xms_act_fct"] = []string{"11"} }},
		{"actor facet numeric", false, func(c map[string]any, _ *IdentityConfig) { c["xms_act_fct"] = 11 }},
		{"missing autonomous subject facet", false, func(c map[string]any, _ *IdentityConfig) { delete(c, "xms_sub_fct") }},
		{"agent user mixed autonomous facet", false, func(c map[string]any, _ *IdentityConfig) { c["xms_sub_fct"] = "11 13" }},
		{"application subject contains delegated scopes", false, func(c map[string]any, _ *IdentityConfig) { c["scp"] = "quota.use" }},
		{"required role missing", false, func(c map[string]any, _ *IdentityConfig) { c["roles"] = []string{"Quota.Read"} }},
		{"required role case mismatch", false, func(c map[string]any, _ *IdentityConfig) { c["roles"] = []string{"quota.invoke"} }},
		{"role string instead of array", false, func(c map[string]any, _ *IdentityConfig) { c["roles"] = "Quota.Invoke" }},
		{"unsupported identity type", false, func(c map[string]any, _ *IdentityConfig) { c["idtyp"] = "agent" }},
		{"delegation disabled", true, func(_ map[string]any, c *IdentityConfig) { c.AllowDelegated = false }},
		{"agent-user account", true, func(c map[string]any, _ *IdentityConfig) { c["xms_sub_fct"] = "9 13" }},
		{"agent-user padded facet", true, func(c map[string]any, _ *IdentityConfig) { c["xms_sub_fct"] = "013" }},
		{"agent object impersonates human", true, func(c map[string]any, _ *IdentityConfig) { c["oid"] = agentID }},
		{"agent subject facet on user", true, func(c map[string]any, _ *IdentityConfig) { c["xms_sub_fct"] = "11" }},
		{"opaque subject facet typing", true, func(c map[string]any, _ *IdentityConfig) { c["xms_sub_fct"] = []any{13} }},
		{"missing scope", true, func(c map[string]any, _ *IdentityConfig) { delete(c, "scp") }},
		{"missing required scope", true, func(c map[string]any, _ *IdentityConfig) { c["scp"] = "quota.read" }},
		{"scope case mismatch", true, func(c map[string]any, _ *IdentityConfig) { c["scp"] = "quota.read Quota.Use" }},
		{"unscoped delegated token", true, func(c map[string]any, _ *IdentityConfig) { c["scp"] = "/" }},
		{"array scopes", true, func(c map[string]any, _ *IdentityConfig) { c["scp"] = []string{"quota.read", "quota.use"} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			claims, config := nativeIdentityClaims(test.delegated), nativeIdentityConfig()
			test.change(claims, &config)
			calls := 0
			verifier, err := NewIdentityVerifier(config, identityClaimsFunc(func(context.Context, string) (map[string]any, error) { calls++; return claims, nil }))
			if err != nil {
				t.Fatal(err)
			}
			if normalized, err := verifier.Verify(context.Background(), "sensitive-incoming-token"); err == nil || normalized != nil || calls != 1 || strings.Contains(err.Error(), "sensitive-incoming-token") {
				t.Fatalf("native policy bypass or credential disclosure: claims=%v err=%v calls=%d", normalized, err, calls)
			}
		})
	}
}

func TestEntraIdentityRequiresCryptographicVerifierAndValidFrozenBindings(t *testing.T) {
	base := identityClaimsFunc(func(context.Context, string) (map[string]any, error) { return nativeIdentityClaims(false), nil })
	var typedNil *nilIdentityVerifier
	var nilFunc identityClaimsFunc
	for _, dependency := range []ClaimsVerifier{nil, typedNil, nilFunc} {
		if _, err := NewIdentityVerifier(nativeIdentityConfig(), dependency); err == nil {
			t.Fatal("nil cryptographic verifier accepted")
		}
	}
	for _, change := range []func(*IdentityConfig){
		func(c *IdentityConfig) { c.TenantID = "not-a-tenant-guid" },
		func(c *IdentityConfig) { c.AllowedAgents = nil },
		func(c *IdentityConfig) {
			c.AllowedAgents = append(c.AllowedAgents, AgentBinding{ObjectID: foreignID, AppID: agentAppID})
		},
		func(c *IdentityConfig) {
			c.AllowedAgents = append(c.AllowedAgents, AgentBinding{ObjectID: agentID, AppID: foreignID})
		},
		func(c *IdentityConfig) { c.RequiredScopes = []string{"quota.*"} },
		func(c *IdentityConfig) { c.RequiredRoles = []string{"Quota.Invoke", "Quota.Invoke"} },
	} {
		config := nativeIdentityConfig()
		change(&config)
		if _, err := NewIdentityVerifier(config, base); err == nil {
			t.Fatal("invalid native binding or grant policy accepted")
		}
	}
	v, err := NewIdentityVerifier(nativeIdentityConfig(), identityClaimsFunc(func(context.Context, string) (map[string]any, error) {
		return nativeIdentityClaims(false), errors.New("base verifier failed for sensitive-incoming-token")
	}))
	if err != nil {
		t.Fatal(err)
	}
	if claims, err := v.Verify(context.Background(), "sensitive-incoming-token"); err == nil || claims != nil || strings.Contains(err.Error(), "sensitive-incoming-token") {
		t.Fatalf("failed cryptographic verification returned identity or leaked token: %v %v", claims, err)
	}
}

func TestEntraIdentityUsesPinnedCryptographicResourceVerifier(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	const issuer = "https://login.microsoftonline.com/" + tenantID + "/v2.0"
	const audience = "api://aiplex-agent-resource"
	base := identityClaimsFunc(func(_ context.Context, token string) (map[string]any, error) {
		verified, err := jwt.Parse(token, func(*jwt.Token) (any, error) { return public, nil }, jwt.WithValidMethods([]string{"EdDSA"}), jwt.WithIssuer(issuer), jwt.WithAudience(audience), jwt.WithExpirationRequired())
		if err != nil || !verified.Valid {
			return nil, errors.New("signature, resource or lifetime rejected")
		}
		return map[string]any(verified.Claims.(jwt.MapClaims)), nil
	})
	verifier, err := NewIdentityVerifier(nativeIdentityConfig(), base)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		change func(map[string]any)
		valid  bool
	}{
		{"valid", func(map[string]any) {}, true},
		{"Graph audience", func(c map[string]any) { c["aud"] = "https://graph.microsoft.com" }, false},
		{"foreign issuer", func(c map[string]any) { c["iss"] = "https://login.microsoftonline.com/" + foreignID + "/v2.0" }, false},
		{"expired", func(c map[string]any) { c["exp"] = time.Now().Add(-time.Minute).Unix() }, false},
		{"future token", func(c map[string]any) { c["nbf"] = time.Now().Add(time.Hour).Unix() }, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			claims := nativeIdentityClaims(false)
			claims["iss"], claims["aud"], claims["exp"], claims["nbf"] = issuer, audience, time.Now().Add(time.Hour).Unix(), time.Now().Add(-time.Minute).Unix()
			test.change(claims)
			token, err := jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.MapClaims(claims)).SignedString(private)
			if err != nil {
				t.Fatal(err)
			}
			got, err := verifier.Verify(context.Background(), token)
			if (err == nil) != test.valid || (test.valid && got["sub"] != agentID) {
				t.Fatalf("cryptographic boundary bypass: valid=%v claims=%v err=%v", test.valid, got, err)
			}
			parts := strings.Split(token, ".")
			parts[2] = strings.Repeat("A", len(parts[2]))
			if _, err := verifier.Verify(context.Background(), strings.Join(parts, ".")); err == nil {
				t.Fatal("forged native claims were accepted")
			}
		})
	}
}
