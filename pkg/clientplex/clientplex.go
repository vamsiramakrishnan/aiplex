// Package clientplex registers developer clients independently of model routing.
// Plans render reviewed configuration fragments; they never write workstation
// files, fetch credentials, execute helpers, or promise upstream model access.
package clientplex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"
)

const (
	Kind            = "client-registration"
	GenAI           = "google-genai"
	OpenAIChat      = "openai-chat"
	Responses       = "openai-responses"
	Messages        = "anthropic-messages"
	VertexAnthropic = "vertex-anthropic"
	ClaudeApps      = "claude-apps"
)

type Profile struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Surface   string   `json:"surface"`
	Modes     []string `json:"modes"`
	Protocols []string `json:"protocols"`
	Sources   []string `json:"sources"`
}

var builtin = []Profile{
	{"antigravity", "Antigravity App / CLI", "app-cli", []string{"direct", "gateway"}, []string{GenAI, OpenAIChat}, []string{"https://antigravity.google/docs/enterprise/", "https://docs.cloud.google.com/gemini/enterprise/docs/ai-developer-tools/deploy-enterprise-gateway"}},
	{"antigravity-acp", "Antigravity ACP", "acp", []string{"direct", "gateway"}, []string{GenAI}, []string{"https://docs.cloud.google.com/gemini/enterprise/docs/ai-developer-tools/deploy-enterprise-gateway"}},
	{"codex", "Codex", "cli", []string{"direct", "gateway"}, []string{Responses}, []string{"https://learn.chatgpt.com/docs/config-file/config-advanced", "https://learn.chatgpt.com/docs/enterprise/connect-to-a-gateway"}},
	{"claude-code", "Claude Code", "cli", []string{"direct", "gateway", "claude-apps-gateway"}, []string{Messages, VertexAnthropic, ClaudeApps}, []string{"https://code.claude.com/docs/en/google-vertex-ai", "https://code.claude.com/docs/en/llm-gateway-connect", "https://code.claude.com/docs/en/claude-apps-gateway"}},
	{"claude-desktop", "Claude Desktop", "desktop", []string{"claude-apps-gateway"}, []string{ClaudeApps}, []string{"https://code.claude.com/docs/en/claude-apps-gateway"}},
}

func Profiles() []Profile                    { return defaultRegistry.Profiles() }
func FindProfile(id string) (Profile, error) { p, _, e := defaultRegistry.lookup(id); return p, e }

type TokenHelper struct {
	Command           string   `json:"command"`
	Args              []string `json:"args,omitempty"`
	TimeoutMS         int      `json:"timeout_ms,omitempty"`
	RefreshIntervalMS int      `json:"refresh_interval_ms,omitempty"`
}

type Registration struct {
	NetworkProfileRef string             `json:"network_profile_ref,omitempty"`
	GatewayServer     *GatewayServerSpec `json:"gateway_server,omitempty"`
	ID                string             `json:"id"`
	ProfileID         string             `json:"profile_id"`
	Mode              string             `json:"mode"`
	Protocol          string             `json:"protocol"`
	DestinationRef    string             `json:"destination_ref,omitempty"`
	PrincipalBinding  string             `json:"principal_binding"`
	ProjectID         string             `json:"project_id,omitempty"`
	Location          string             `json:"location,omitempty"`
	ModelID           string             `json:"model_id,omitempty"`
	AllowedModels     []string           `json:"allowed_models,omitempty"`
	AuthMode          string             `json:"auth_mode"`
	CredentialEnv     string             `json:"credential_env,omitempty"`
	CredentialRef     string             `json:"credential_ref,omitempty"`
	CredentialFile    string             `json:"credential_file,omitempty"`
	TokenHelper       *TokenHelper       `json:"token_helper,omitempty"`
	Managed           bool               `json:"managed,omitempty"`
	Platform          string             `json:"platform,omitempty"`
}

type Destination struct {
	ID           string   `json:"id"`
	Provider     string   `json:"provider"`
	Endpoint     string   `json:"endpoint"`
	Protocols    []string `json:"protocols"`
	ProjectID    string   `json:"project_id,omitempty"`
	Location     string   `json:"location,omitempty"`
	Availability string   `json:"availability"` // configured, unverified, unavailable
	ModelIDs     []string `json:"model_ids,omitempty"`
}
type DestinationResolver interface {
	ResolveDestination(context.Context, string, string) (Destination, error)
}
type ResolverFunc func(context.Context, string, string) (Destination, error)

func (f ResolverFunc) ResolveDestination(ctx context.Context, tenant, ref string) (Destination, error) {
	return f(ctx, tenant, ref)
}

var idPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
var projectPattern = regexp.MustCompile(`^(?:[a-z][a-z0-9-]{3,61}[a-z0-9]|[0-9]{6,20})$`)
var locationPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
var envPattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,127}$`)

func (r Registration) Validate() error {
	p, e := FindProfile(r.ProfileID)
	if e != nil {
		return e
	}
	return r.validateProfile(p)
}
func (r Registration) validateProfile(p Profile) error {
	if !idPattern.MatchString(r.ID) || r.PrincipalBinding == "" || len(r.PrincipalBinding) > 1024 {
		return fmt.Errorf("valid registration id and principal_binding required")
	}
	if r.NetworkProfileRef != "" && !idPattern.MatchString(r.NetworkProfileRef) {
		return fmt.Errorf("invalid network profile reference")
	}
	if !slices.Contains(p.Modes, r.Mode) || !slices.Contains(p.Protocols, r.Protocol) {
		return fmt.Errorf("client profile %s does not support mode %s and protocol %s", p.ID, r.Mode, r.Protocol)
	}
	if r.Mode == "claude-apps-gateway" && r.Protocol != ClaudeApps {
		return fmt.Errorf("Claude apps gateway requires its native protocol")
	}
	if r.Mode != "claude-apps-gateway" && r.Protocol == ClaudeApps {
		return fmt.Errorf("native Claude apps protocol requires managed gateway sign-in")
	}
	if r.Mode == "direct" {
		if strings.HasPrefix(r.ProfileID, "antigravity") && r.Protocol != GenAI {
			return fmt.Errorf("Antigravity direct mode uses the Google GenAI client flow")
		}
		if r.DestinationRef != "" {
			return fmt.Errorf("direct mode does not select a gateway destination")
		}
		if !projectPattern.MatchString(r.ProjectID) || !locationPattern.MatchString(r.Location) {
			return fmt.Errorf("direct Google Cloud mode requires valid project_id and location")
		}
		if r.AuthMode != "adc" && !(strings.HasPrefix(r.ProfileID, "antigravity") && r.AuthMode == "workforce") {
			return fmt.Errorf("direct mode requires ADC or supported workforce sign-in")
		}
		if r.ProfileID == "claude-code" && r.Protocol != VertexAnthropic {
			return fmt.Errorf("direct Claude Code uses Vertex Anthropic protocol")
		}
	} else {
		if !idPattern.MatchString(r.DestinationRef) {
			return fmt.Errorf("gateway destination reference required")
		}
		switch r.AuthMode {
		case "gateway-key", "token-helper", "oidc":
		default:
			return fmt.Errorf("unsupported gateway authentication mode")
		}
		if r.Mode == "claude-apps-gateway" && r.AuthMode != "oidc" {
			return fmt.Errorf("Claude apps gateway requires browser OIDC sign-in")
		}
		if r.Mode == "gateway" && r.AuthMode == "oidc" {
			return fmt.Errorf("generic gateway OIDC needs an explicit supported auth adapter")
		}
		if strings.HasPrefix(r.ProfileID, "antigravity") && r.AuthMode != "gateway-key" {
			return fmt.Errorf("Antigravity gateway mode requires its gateway-issued key")
		}
		if r.AuthMode == "gateway-key" && (!envPattern.MatchString(r.CredentialEnv) || r.CredentialRef == "") {
			return fmt.Errorf("gateway key needs credential_env and an opaque credential_ref")
		}
		if r.AuthMode == "token-helper" && (r.ProfileID != "codex" || r.CredentialRef == "" || r.TokenHelper == nil) {
			return fmt.Errorf("token-helper currently requires Codex and an opaque credential reference")
		}
	}
	if r.TokenHelper != nil {
		if r.AuthMode != "token-helper" || !strings.HasPrefix(r.TokenHelper.Command, "/") || strings.ContainsAny(r.TokenHelper.Command, "\x00\r\n") || r.TokenHelper.TimeoutMS < 0 || r.TokenHelper.TimeoutMS > 30000 || r.TokenHelper.RefreshIntervalMS < 0 {
			return fmt.Errorf("token helper requires a configured absolute executable and bounded timing")
		}
		if len(r.TokenHelper.Args) > 32 {
			return fmt.Errorf("too many token helper arguments")
		}
		for _, arg := range r.TokenHelper.Args {
			if len(arg) > 2048 || strings.ContainsRune(arg, '\x00') {
				return fmt.Errorf("invalid token helper argument")
			}
		}
	}
	if r.GatewayServer != nil {
		if r.Mode != "claude-apps-gateway" {
			return fmt.Errorf("gateway server config requires native Claude apps gateway mode")
		}
		if e := r.GatewayServer.Validate(); e != nil {
			return e
		}
	}
	if r.Platform != "" && !slices.Contains([]string{"linux", "darwin", "windows"}, r.Platform) {
		return fmt.Errorf("unsupported platform")
	}
	if r.CredentialFile != "" && (r.ProfileID != "codex" || r.Mode != "direct" || !strings.HasPrefix(r.CredentialFile, "/") || strings.ContainsAny(r.CredentialFile, "\x00\r\n")) {
		return fmt.Errorf("credential_file is supported only as an absolute Codex ADC path")
	}
	if len(r.AllowedModels) > 100 {
		return fmt.Errorf("too many models")
	}
	seen := map[string]bool{}
	for _, model := range r.AllowedModels {
		if model == "" || len(model) > 256 || strings.ContainsAny(model, "\r\n\x00") || seen[model] {
			return fmt.Errorf("invalid or duplicate model identifier")
		}
		seen[model] = true
	}
	if len(r.ModelID) > 256 || strings.ContainsAny(r.ModelID, "\r\n\x00") {
		return fmt.Errorf("invalid model identifier")
	}

	return nil
}

type ServiceEndpoint struct {
	Host     string `json:"host"`
	Purpose  string `json:"purpose"`
	Required bool   `json:"required"`
}
type Artifact struct {
	Target        string `json:"target"`
	Format        string `json:"format"`
	MergeStrategy string `json:"merge_strategy"`
	Content       string `json:"content"`
}
type CredentialBinding struct {
	Reference   string `json:"reference"`
	Environment string `json:"environment"`
}

type Plan struct {
	NetworkProfileRef   string              `json:"network_profile_ref,omitempty"`
	Commands            [][]string          `json:"commands,omitempty"`
	VersionRequirements []string            `json:"version_requirements,omitempty"`
	CredentialBindings  []CredentialBinding `json:"credential_bindings,omitempty"`
	Version             string              `json:"version"`
	RegistrationID      string              `json:"registration_id"`
	ProfileID           string              `json:"profile_id"`
	SupportStatus       string              `json:"support_status"`
	Artifacts           []Artifact          `json:"artifacts"`
	Services            []ServiceEndpoint   `json:"services"`
	PreserveHeaders     []string            `json:"preserve_headers,omitempty"`
	Warnings            []string            `json:"warnings"`
	Sources             []string            `json:"sources"`
}

func BuildPlan(ctx context.Context, tenant string, r Registration, resolver DestinationResolver) (Plan, error) {
	return defaultRegistry.BuildPlan(ctx, tenant, r, resolver)
}
func buildPlan(ctx context.Context, tenant string, r Registration, resolver DestinationResolver, profile Profile, renderer Renderer) (Plan, error) {
	if tenant == "" {
		return Plan{}, fmt.Errorf("trusted tenant required")
	}
	if e := r.validateProfile(profile); e != nil {
		return Plan{}, e
	}
	p := Plan{NetworkProfileRef: r.NetworkProfileRef, Version: "v1", RegistrationID: r.ID, ProfileID: r.ProfileID, SupportStatus: "configured-unverified", Sources: profile.Sources, Warnings: []string{"Configuration plan only; upstream model availability, IAM, billing, and installed client version require verification.", "Merge reviewed fragments into existing configuration; no workstation files or cloud resources were modified."}}
	var d Destination
	if r.Mode != "direct" {
		if resolver == nil {
			return Plan{}, fmt.Errorf("trusted destination resolver required")
		}
		var e error
		d, e = resolver.ResolveDestination(ctx, tenant, r.DestinationRef)
		if e != nil {
			return Plan{}, fmt.Errorf("resolve destination: %w", e)
		}
		if d.ID != r.DestinationRef || !slices.Contains(d.Protocols, r.Protocol) {
			return Plan{}, fmt.Errorf("destination does not advertise required native protocol %s", r.Protocol)
		}
		if r.Protocol == VertexAnthropic {
			project, location := r.ProjectID, r.Location
			if project == "" {
				project = d.ProjectID
			}
			if location == "" {
				location = d.Location
			}
			if !projectPattern.MatchString(project) || !locationPattern.MatchString(location) {
				return Plan{}, fmt.Errorf("Vertex gateway requires valid configured project and location")
			}
			if (d.ProjectID != "" && project != d.ProjectID) || (d.Location != "" && location != d.Location) {
				return Plan{}, fmt.Errorf("registration cannot override the destination project or location")
			}
		}
		if !slices.Contains([]string{"", "configured", "unverified", "unavailable"}, d.Availability) {
			return Plan{}, fmt.Errorf("invalid destination availability declaration")
		}
		if d.Availability == "unavailable" {
			return Plan{}, fmt.Errorf("destination unavailable")
		}
		endpoint, e := destinationEndpoint(d.Endpoint)
		if e != nil {
			return Plan{}, e
		}
		p.Services = append(p.Services, ServiceEndpoint{endpoint.Hostname(), "gateway", true})
		if len(d.ModelIDs) > 0 {
			for _, model := range append(slices.Clone(r.AllowedModels), r.ModelID) {
				if model != "" && !slices.Contains(d.ModelIDs, model) {
					return Plan{}, fmt.Errorf("model %s not in destination declaration", model)
				}
			}
		}
		p.Warnings = append(p.Warnings, "Destination is declared but upstream availability has not been verified.")
		if d.Provider == "aiplex" {
			p.Warnings = append(p.Warnings, "Credentials for this Aiplex facade must satisfy its configured JWT or native Agent Identity verifier. Client registration does not issue opaque gateway API keys; use an accepted, refreshable bearer credential.")
		}
	}
	if e := renderer(&p, r, d); e != nil {
		return Plan{}, e
	}

	if r.GatewayServer != nil {
		if strings.TrimSuffix(r.GatewayServer.PublicURL, "/") != strings.TrimSuffix(d.Endpoint, "/") {
			return Plan{}, fmt.Errorf("gateway server public_url must match registered destination")
		}
		server, e := RenderClaudeGateway(*r.GatewayServer)
		if e != nil {
			return Plan{}, e
		}
		p.Artifacts = append(p.Artifacts, server.Artifact)
		p.Commands = append(p.Commands, slices.Clone(server.Command))
		p.CredentialBindings = append(p.CredentialBindings, server.CredentialBindings...)
		p.Services = append(p.Services, server.Services...)
		p.Warnings = append(p.Warnings, server.Warnings...)
		p.Sources = append(p.Sources, server.Sources...)
	}
	if r.Mode == "direct" {
		p.Services = append(p.Services, ServiceEndpoint{"sts.googleapis.com", "external-account federation when configured", false}, ServiceEndpoint{"iamcredentials.googleapis.com", "service-account impersonation when configured", false})
	}
	return p, nil
}
// destinationEndpoint is secureEndpoint, except that a plain-HTTP loopback
// gateway (127.0.0.1, localhost or ::1) is accepted so a local development
// stack can render client configuration. Nothing leaves the machine over it.
func destinationEndpoint(raw string) (*url.URL, error) {
	if u, e := url.Parse(raw); e == nil && u.Scheme == "http" {
		switch strings.ToLower(u.Hostname()) {
		case "127.0.0.1", "localhost", "::1":
			u2 := *u
			u2.Scheme = "https"
			if _, e := secureEndpoint(u2.String()); e != nil {
				return nil, fmt.Errorf("destination requires a URL without credentials, query or fragment")
			}
			return u, nil
		}
	}
	return secureEndpoint(raw)
}

func secureEndpoint(raw string) (*url.URL, error) {
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return nil, fmt.Errorf("destination requires HTTPS URL without credentials, query or fragment")
	}
	if strings.Contains(u.EscapedPath(), "%") || path.Clean(u.Path) != u.Path && u.Path != "" {
		return nil, fmt.Errorf("destination path is not canonical")
	}

	return u, nil
}
func addJSON(p *Plan, target string, value any) {
	b, _ := json.MarshalIndent(value, "", "  ")
	p.Artifacts = append(p.Artifacts, Artifact{target, "json", "reviewed-fragment", string(b) + "\n"})
}
func q(s string) string { b, _ := json.Marshal(s); return string(b) }
func modelList(r Registration) []map[string]string {
	models := slices.Clone(r.AllowedModels)
	if r.ModelID != "" && !slices.Contains(models, r.ModelID) {
		models = append(models, r.ModelID)
	}
	out := make([]map[string]string, 0, len(models))
	for _, m := range models {
		out = append(out, map[string]string{"modelId": m})
	}
	return out
}
func googleModelHost(location string) string {
	if location == "global" {
		return "aiplatform.googleapis.com"
	}
	if location == "us" || location == "eu" {
		return "aiplatform." + location + ".rep.googleapis.com"
	}
	return location + "-aiplatform.googleapis.com"
}
