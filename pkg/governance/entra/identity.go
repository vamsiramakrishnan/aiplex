package entra

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
)

// ClaimsVerifier is the cryptographic boundary. Before returning claims it must
// verify the signature, exact Entra issuer, Aiplex resource audience and token
// lifetime. An outbound Graph token is not an incoming Aiplex credential.
type ClaimsVerifier interface {
	Verify(context.Context, string) (map[string]any, error)
}

// IdentityConfig is resource-server policy, separate from Graph credentials and
// the native token-acquisition broker. Agent application and object GUIDs are
// explicit bindings; a blueprint or sponsor does not grant child identities.
// RequiredScopes applies to delegated human flows; RequiredRoles to app-only.
// Every configured permission is required, with exact case-sensitive matching.
type IdentityConfig struct {
	TenantID       string         `json:"tenant_id"`
	AllowedAgents  []AgentBinding `json:"allowed_agents"`
	AllowDelegated bool           `json:"allow_delegated,omitempty"`
	RequiredScopes []string       `json:"required_scopes,omitempty"`
	RequiredRoles  []string       `json:"required_roles,omitempty"`
}

type IdentityVerifier struct {
	config   IdentityConfig
	verifier ClaimsVerifier
	agents   map[string]AgentBinding
	objects  map[string]bool
}

func NewIdentityVerifier(cfg IdentityConfig, verifier ClaimsVerifier) (*IdentityVerifier, error) {
	if !identityGUID(cfg.TenantID) || len(cfg.AllowedAgents) == 0 || len(cfg.AllowedAgents) > 4096 || nilClaimsVerifier(verifier) {
		return nil, errors.New("Entra resource identity requires native tenant, explicit agents and a cryptographic claims verifier")
	}
	if !validPermissions(cfg.RequiredScopes) || !validPermissions(cfg.RequiredRoles) {
		return nil, errors.New("Entra required permissions must be bounded, exact and distinct")
	}
	cfg.TenantID = strings.ToLower(cfg.TenantID)
	cfg.AllowedAgents = append([]AgentBinding(nil), cfg.AllowedAgents...)
	cfg.RequiredScopes = append([]string(nil), cfg.RequiredScopes...)
	cfg.RequiredRoles = append([]string(nil), cfg.RequiredRoles...)
	v := &IdentityVerifier{config: cfg, verifier: verifier, agents: map[string]AgentBinding{}, objects: map[string]bool{}}
	for i, agent := range cfg.AllowedAgents {
		if !identityGUID(agent.ObjectID) || !identityGUID(agent.AppID) {
			return nil, errors.New("native agent object and application GUIDs required")
		}
		agent.ObjectID, agent.AppID = strings.ToLower(agent.ObjectID), strings.ToLower(agent.AppID)
		if _, ok := v.agents[agent.AppID]; ok || v.objects[agent.ObjectID] {
			return nil, errors.New("duplicate native agent object or application binding")
		}
		v.agents[agent.AppID] = agent
		v.objects[agent.ObjectID] = true
		v.config.AllowedAgents[i] = agent
	}
	return v, nil
}

func nilClaimsVerifier(v ClaimsVerifier) bool {
	if v == nil {
		return true
	}
	r := reflect.ValueOf(v)
	switch r.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return r.IsNil()
	}
	return false
}

func identityGUID(s string) bool {
	return validID(s) && s != "00000000-0000-0000-0000-000000000000"
}

func validPermissions(permissions []string) bool {
	if len(permissions) > 256 {
		return false
	}
	seen := map[string]bool{}
	for _, permission := range permissions {
		if permission == "" || permission == "/" || len(permission) > 512 || strings.ContainsAny(permission, "* \t\r\n\x00") || seen[permission] {
			return false
		}
		seen[permission] = true
	}
	return true
}

// Verify normalizes only cryptographically verified Entra Agent ID claims.
// Autonomous subject=agent object ID. Delegated subject=human object ID and
// act.sub=agent object ID; azp always remains the agent application ID. Original
// native subject is retained for audit, and parent blueprint claims remain
// audit-only. The verifier never promotes native permissions to Aiplex grants.
func (v *IdentityVerifier) Verify(ctx context.Context, token string) (map[string]any, error) {
	if v == nil || nilClaimsVerifier(v.verifier) || token == "" || len(token) > 64<<10 || strings.ContainsAny(token, " \t\r\n\x00") {
		return nil, errors.New("bounded incoming Entra access token required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	verified, err := v.verifier.Verify(ctx, token)
	if err != nil || verified == nil {
		return nil, errors.New("incoming Entra access token verification failed")
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	// Clone all claims before canonicalization: a shared verifier cache must not
	// observe changes to sub/act or mutations by downstream request consumers.
	encoded, err := json.Marshal(verified)
	if err != nil || len(encoded) > 64<<10 {
		return nil, errors.New("invalid or oversized verified Entra claims")
	}
	var claims map[string]any
	if json.Unmarshal(encoded, &claims) != nil {
		return nil, errors.New("invalid verified Entra claims")
	}
	tenant, _ := claims["tid"].(string)
	oid, _ := claims["oid"].(string)
	if !strings.EqualFold(tenant, v.config.TenantID) || !identityGUID(oid) {
		return nil, errors.New("incoming Entra tenant or immutable object identity denied")
	}
	appID, err := nativeActorApplication(claims)
	if err != nil {
		return nil, err
	}
	agent, allowed := v.agents[strings.ToLower(appID)]
	actorFacets, err := nativeFacets(claims["xms_act_fct"])
	if !allowed || err != nil || !actorFacets[11] || actorFacets[13] {
		return nil, errors.New("incoming Entra agent actor denied")
	}
	subjectFacets, err := nativeFacets(claims["xms_sub_fct"])
	if err != nil || subjectFacets[13] {
		return nil, errors.New("Entra agent-user account identities are not supported")
	}
	idtyp, _ := claims["idtyp"].(string)
	switch idtyp {
	case "app":
		if !subjectFacets[11] || !strings.EqualFold(oid, agent.ObjectID) {
			return nil, errors.New("autonomous Entra subject does not match the allowed agent")
		}
		if scopes, ok := claims["scp"]; ok && scopes != "" && scopes != "/" {
			return nil, errors.New("autonomous Entra identity contains delegated scopes")
		}
		roles, err := nativeRoles(claims["roles"])
		if err != nil || !hasPermissions(roles, v.config.RequiredRoles) {
			return nil, errors.New("required Entra application roles are missing")
		}
		delete(claims, "act")
	case "user":
		if !v.config.AllowDelegated || subjectFacets[11] || v.objects[strings.ToLower(oid)] {
			return nil, errors.New("delegated human Entra identity is not allowed")
		}
		scope, ok := claims["scp"].(string)
		if !ok || strings.TrimSpace(scope) == "" || strings.TrimSpace(scope) == "/" || !hasPermissions(strings.Fields(scope), v.config.RequiredScopes) {
			return nil, errors.New("required Entra delegated scopes are missing")
		}
		claims["act"] = map[string]any{"sub": agent.ObjectID}
	default:
		return nil, errors.New("unsupported Entra identity type")
	}
	claims["entra_native_sub"] = claims["sub"]
	claims["sub"], claims["oid"], claims["tid"], claims["azp"] = strings.ToLower(oid), strings.ToLower(oid), v.config.TenantID, agent.AppID
	return claims, nil
}

func nativeActorApplication(claims map[string]any) (string, error) {
	version, _ := claims["ver"].(string)
	key, other := "azp", "appid"
	if version == "1.0" {
		key, other = "appid", "azp"
	} else if version != "2.0" {
		return "", errors.New("unsupported Entra access token version")
	}
	app, _ := claims[key].(string)
	if !identityGUID(app) {
		return "", errors.New("native Entra agent application GUID required")
	}
	if extra, exists := claims[other]; exists {
		value, ok := extra.(string)
		if !ok || !strings.EqualFold(value, app) {
			return "", errors.New("conflicting native Entra actor applications")
		}
	}
	return app, nil
}

// Microsoft documents facets as space-separated integer strings. Ignore
// unrelated values and ordering, while never treating substring 111 as facet11.
func nativeFacets(value any) (map[uint64]bool, error) {
	facets := map[uint64]bool{}
	if value == nil {
		return facets, nil
	}
	raw, ok := value.(string)
	if !ok || len(raw) > 2048 {
		return nil, errors.New("invalid native Entra facets")
	}
	for _, field := range strings.Fields(raw) {
		facet, err := strconv.ParseUint(field, 10, 32)
		if err != nil {
			return nil, errors.New("invalid native Entra facets")
		}
		facets[facet] = true
	}
	return facets, nil
}

func nativeRoles(value any) ([]string, error) {
	if value == nil {
		return nil, nil
	}
	roles, ok := value.([]any)
	if !ok || len(roles) > 256 {
		return nil, errors.New("invalid Entra roles claim")
	}
	out := make([]string, len(roles))
	for i, role := range roles {
		var valid bool
		out[i], valid = role.(string)
		if !valid || out[i] == "" {
			return nil, errors.New("invalid Entra application role")
		}
	}
	return out, nil
}

func hasPermissions(granted, required []string) bool {
	set := make(map[string]bool, len(granted))
	for _, permission := range granted {
		set[permission] = true
	}
	for _, permission := range required {
		if !set[permission] {
			return false
		}
	}
	return true
}
