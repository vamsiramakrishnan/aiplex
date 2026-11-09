package clientplex

import (
	"fmt"
	"net"
	"net/url"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

type GatewayModel struct {
	ID            string `json:"id"`
	VertexModelID string `json:"vertex_model_id"`
}
type GroupModelRule struct {
	Groups  []string `json:"groups"`
	Models  []string `json:"models"`
	Desktop bool     `json:"desktop,omitempty"`
}

// GatewayServerSpec renders configuration for Anthropic's separately supplied
// Claude apps gateway binary. Secret values are never accepted in this schema.
type GatewayServerSpec struct {
	OIDCExtraAuthParams map[string]string  `json:"oidc_extra_auth_params,omitempty"`
	PublicURL           string             `json:"public_url"`
	ProjectID           string             `json:"project_id"`
	Location            string             `json:"location"`
	OIDCIssuer          string             `json:"oidc_issuer"`
	OIDCClientID        string             `json:"oidc_client_id"`
	GroupsClaim         string             `json:"groups_claim"`
	OIDCScopes          []string           `json:"oidc_scopes"`
	OIDCClientSecret    CredentialBinding  `json:"oidc_client_secret"`
	SessionSecret       CredentialBinding  `json:"session_secret"`
	PostgresURL         CredentialBinding  `json:"postgres_url"`
	PostgresHost        string             `json:"postgres_host"`
	AllowedCIDRs        []string           `json:"allowed_cidrs"`
	TrustedProxies      []string           `json:"trusted_proxies,omitempty"`
	Models              []GatewayModel     `json:"models"`
	Policies            []GroupModelRule   `json:"policies"`
	TelemetryURL        string             `json:"telemetry_url"`
	TelemetryToken      *CredentialBinding `json:"telemetry_token,omitempty"`
	TelemetryLogs       bool               `json:"telemetry_logs,omitempty"`
	TelemetryTraces     bool               `json:"telemetry_traces,omitempty"`
}
type GatewayServerPlan struct {
	Artifact           Artifact            `json:"artifact"`
	Command            []string            `json:"command"`
	CredentialBindings []CredentialBinding `json:"credential_bindings"`
	Services           []ServiceEndpoint   `json:"services"`
	Warnings           []string            `json:"warnings"`
	Sources            []string            `json:"sources"`
}

func (s GatewayServerSpec) Validate() error {
	public, e := secureEndpoint(s.PublicURL)
	if e != nil || public.Path != "" && public.Path != "/" {
		return fmt.Errorf("gateway public_url must be an HTTPS origin")
	}
	if !projectPattern.MatchString(s.ProjectID) || !locationPattern.MatchString(s.Location) {
		return fmt.Errorf("gateway needs valid Google Cloud project and location")
	}
	if _, e = secureEndpoint(s.OIDCIssuer); e != nil || s.OIDCClientID == "" || s.GroupsClaim == "" {
		return fmt.Errorf("gateway needs pinned HTTPS OIDC issuer, client id and groups claim")
	}
	if !slices.Contains(s.OIDCScopes, "openid") {
		return fmt.Errorf("OIDC scopes must explicitly include openid")
	}
	if len(s.OIDCScopes) > 20 {
		return fmt.Errorf("too many OIDC scopes")
	}
	for _, scope := range s.OIDCScopes {
		if scope == "" || strings.ContainsAny(scope, " \r\n\x00") {
			return fmt.Errorf("invalid OIDC scope")
		}
	}
	if len(s.OIDCExtraAuthParams) > 10 {
		return fmt.Errorf("too many OIDC authorization parameters")
	}
	for key, value := range s.OIDCExtraAuthParams {
		if slices.Contains([]string{"client_id", "redirect_uri", "response_type", "state", "nonce", "code_challenge", "code_challenge_method"}, key) {
			return fmt.Errorf("OIDC authorization parameter %q is managed by the gateway", key)
		}
		if key == "" || strings.ContainsAny(key+value, "\x00\r\n") {
			return fmt.Errorf("invalid OIDC authorization parameter")
		}
	}
	bindings := []CredentialBinding{s.OIDCClientSecret, s.SessionSecret, s.PostgresURL}
	if s.TelemetryToken != nil {
		bindings = append(bindings, *s.TelemetryToken)
	}
	seenEnv := map[string]bool{}
	for _, binding := range bindings {
		if binding.Reference == "" || !envPattern.MatchString(binding.Environment) || seenEnv[binding.Environment] {
			return fmt.Errorf("distinct secret references and environment-variable names required")
		}
		seenEnv[binding.Environment] = true
	}
	if s.PostgresHost == "" || strings.ContainsAny(s.PostgresHost, "/:?#@\r\n ") {
		return fmt.Errorf("explicit PostgreSQL hostname required for networking plan")
	}
	if len(s.AllowedCIDRs) == 0 {
		return fmt.Errorf("explicit gateway client CIDR allowlist required")
	}
	for _, cidr := range append(slices.Clone(s.AllowedCIDRs), s.TrustedProxies...) {
		if _, _, e = net.ParseCIDR(cidr); e != nil {
			return fmt.Errorf("invalid gateway/proxy CIDR")
		}
	}
	if len(s.Models) == 0 || len(s.Models) > 100 || len(s.Policies) == 0 || len(s.Policies) > 100 {
		return fmt.Errorf("bounded explicit model catalog and group policies required")
	}
	models := map[string]bool{}
	for _, model := range s.Models {
		if model.ID == "" || model.VertexModelID == "" || len(model.ID) > 256 || len(model.VertexModelID) > 256 || strings.ContainsAny(model.ID+model.VertexModelID, "\r\n\x00") || models[model.ID] {
			return fmt.Errorf("invalid or duplicate gateway model")
		}
		models[model.ID] = true
	}
	for _, policy := range s.Policies {
		if len(policy.Groups) == 0 || len(policy.Models) == 0 {
			return fmt.Errorf("group policy needs explicit groups and models")
		}
		for _, group := range policy.Groups {
			if group == "" || strings.ContainsAny(group, "\r\n\x00") {
				return fmt.Errorf("invalid gateway group")
			}
		}
		for _, model := range policy.Models {
			if !models[model] {
				return fmt.Errorf("group policy references undeclared model %q", model)
			}
		}
	}
	if _, e = secureEndpoint(s.TelemetryURL); e != nil {
		return fmt.Errorf("explicit HTTPS OTLP destination required")
	}
	return nil
}
func RenderClaudeGateway(s GatewayServerSpec) (GatewayServerPlan, error) {
	if e := s.Validate(); e != nil {
		return GatewayServerPlan{}, e
	}
	env := func(b CredentialBinding) string { return "${" + b.Environment + "}" }
	listen := map[string]any{"host": "0.0.0.0", "port": 8080, "public_url": strings.TrimSuffix(s.PublicURL, "/")}
	if len(s.TrustedProxies) > 0 {
		listen["trusted_proxies"] = s.TrustedProxies
	}
	allowedGroups := []string{}
	policies := []map[string]any{}
	for _, p := range s.Policies {
		for _, g := range p.Groups {
			if !slices.Contains(allowedGroups, g) {
				allowedGroups = append(allowedGroups, g)
			}
		}
		entry := map[string]any{"match": map[string]any{"groups": p.Groups}, "cli": map[string]any{"availableModels": p.Models, "enforceAvailableModels": true}}
		if p.Desktop {
			entry["desktop"] = map[string]any{}
		}
		policies = append(policies, entry)
	}
	models := []map[string]any{}
	for _, m := range s.Models {
		models = append(models, map[string]any{"id": m.ID, "upstream_model": map[string]string{"vertex": m.VertexModelID}})
	}
	destination := map[string]any{"url": s.TelemetryURL, "metrics": true, "logs": s.TelemetryLogs, "traces": s.TelemetryTraces}
	if s.TelemetryToken != nil {
		destination["headers"] = map[string]string{"Authorization": "Bearer " + env(*s.TelemetryToken)}
	}
	document := map[string]any{
		"listen":                      listen,
		"oidc":                        map[string]any{"issuer": s.OIDCIssuer, "client_id": s.OIDCClientID, "client_secret": env(s.OIDCClientSecret), "groups_claim": s.GroupsClaim, "scopes": s.OIDCScopes, "allowed_groups": allowedGroups},
		"session":                     map[string]string{"jwt_secret": env(s.SessionSecret)},
		"store":                       map[string]string{"postgres_url": env(s.PostgresURL)},
		"upstreams":                   []map[string]any{{"provider": "vertex", "project_id": s.ProjectID, "region": s.Location, "auth": map[string]any{}}},
		"auto_include_builtin_models": false, "models": models, "managed": map[string]any{"policies": policies},
		"access_control": map[string]any{"allow_cidrs": s.AllowedCIDRs}, "telemetry": map[string]any{"forward_to": []map[string]any{destination}},
	}
	if len(s.OIDCExtraAuthParams) > 0 {
		document["oidc"].(map[string]any)["extra_auth_params"] = s.OIDCExtraAuthParams
	}
	payload, e := yaml.Marshal(document)
	if e != nil {
		return GatewayServerPlan{}, e
	}
	issuer, _ := url.Parse(s.OIDCIssuer)
	collector, _ := url.Parse(s.TelemetryURL)
	bindings := []CredentialBinding{s.OIDCClientSecret, s.SessionSecret, s.PostgresURL}
	if s.TelemetryToken != nil {
		bindings = append(bindings, *s.TelemetryToken)
	}
	return GatewayServerPlan{Artifact: Artifact{"gateway.yaml", "yaml", "new-file-reviewed", string(payload)}, Command: []string{"claude", "gateway", "--config", "gateway.yaml"}, CredentialBindings: bindings, Services: []ServiceEndpoint{{googleModelHost(s.Location), "Vertex upstream inference with attached service account / ADC", true}, {issuer.Hostname(), "corporate OIDC discovery and browser sign-in", true}, {s.PostgresHost, "PostgreSQL session store (TCP 5432)", true}, {collector.Hostname(), "OTLP/HTTP exports", true}}, Warnings: []string{"Requires an appropriately licensed and supported Claude binary; Aiplex does not implement or distribute Anthropic's gateway sign-in service.", "Configuration was generated without executing claude, reaching cloud services, reading credentials or applying infrastructure.", "Provide TLS termination and private ingress separately. The gateway's service account needs model access and aiplatform permissions; generated model IDs do not grant access.", "OIDC provider must emit the configured group claim, including refreshed membership. Google Workspace ID tokens do not contain groups; configure a separately reviewed directory integration rather than assuming group claims exist.", "Native gateway policies compose according to the installed Claude gateway version; verify multi-group behavior against its /protocol and a throwaway PostgreSQL deployment.", "Metrics are enabled; logs and traces are included only when explicitly requested because they can contain commands and tool inputs."}, Sources: []string{"https://code.claude.com/docs/en/claude-apps-gateway-config", "https://code.claude.com/docs/en/claude-apps-gateway-on-gcp"}}, nil
}
