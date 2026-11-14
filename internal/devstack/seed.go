package devstack

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/modelapi"
	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

// Names of the resources Seed creates.
const (
	SeedAccessPoint = "local"
	SeedSurface     = "local-models"
	// SeedSurfaceB is the chat surface on the second mock provider (Mock B),
	// created with the mock provider but on no access point, for trying
	// routing: list it as a second backend of mock-gpt.
	SeedSurfaceB   = "local-models-b"
	SeedConnection = "local-tools"
	// SeedKeyedConnection is the tool connection to the keyed MCP server: its
	// upstream refuses requests without an API key, and the connection holds
	// the key only as the secret reference KeyedSecretRef. It is not on access
	// point "local"; add it with: aiplex access-points create team --tool local-tools-keyed
	SeedKeyedConnection = "local-tools-keyed"
	MockModel           = "mock-gpt"
	// MockSecretRef is the secret reference the extra mock surfaces use. Their
	// providers require a credential scheme, so the stack gives the API process
	// a throwaway value under this reference (the mock ignores it).
	MockSecretRef = "dev-mock-key"
)

// MockSurface is one built-in mock model surface: a wire the local demo
// serves besides OpenAI Chat Completions.
type MockSurface struct {
	Surface  string // surface id
	Model    string // mock model name (also the native model)
	Provider string // modelapi provider whose catalog the surface uses
	Wire     string // protocol name, e.g. anthropic-messages
}

// MockSurfaces are seeded next to the chat surface when the provider is
// "mock". Each goes through the normal surface, access-point and gateway path.
var MockSurfaces = []MockSurface{
	{"local-messages", "mock-claude", "anthropic", "anthropic-messages"},
	{"local-responses", "mock-responses", "openai", "openai-responses"},
	{"local-gemini", "mock-gemini", "gemini", "google-genai"},
}

// SeededModel is one model exposed through the access point.
type SeededModel struct {
	Name     string
	Surface  string
	Protocol string
	Price    string
}

// Seeded describes what Seed created.
type Seeded struct {
	AccessPoint string
	Surface     string
	Provider    string
	Model       string
	Protocol    string // wire the model is served on: openai-chat, anthropic-messages or google-genai
	Price       string // empty when no price was seeded
	// Models lists every model on the access point, the primary one first.
	Models     []SeededModel
	Connection string // empty when no tool connection was seeded
	// KeyedConnection is the tool connection that authenticates to its upstream
	// with an API key held as a secret reference; empty when no tools were seeded.
	KeyedConnection string
	// AgentConnection is the agent connection to the mock agent runtime.
	AgentConnection string
	Tools           []string
}

// DefaultModel is the model wired for each provider unless overridden.
func DefaultModel(provider string) string {
	switch provider {
	case "openai":
		return "gpt-4o-mini"
	case "anthropic":
		return "claude-haiku-4-5"
	case "gemini":
		return "gemini-3.1-flash-lite"
	}
	return MockModel
}

// ClaudeCodeBetas are the anthropic-beta values Claude Code 2.1.322 sent on
// /v1/messages when observed on 2026-11-10 (capture of a headless turn), plus
// the long-standing ones it has used before. The gateway refuses a beta the
// surface does not list, so a development surface lists them or the real
// client's first request fails with 400. A beta a later Claude Code adds has to
// be added to the surface's provider.anthropic_betas.
func ClaudeCodeBetas() []string {
	return []string{
		"claude-code-20250219", "interleaved-thinking-2025-05-14", "fine-grained-tool-streaming-2025-05-14",
		"token-efficient-tools-2025-02-19", "context-management-2025-06-27", "advanced-tool-use-2025-11-20",
		"effort-2025-11-24", "per-turn-control-2026-07-01", "mid-conversation-tool-changes-2026-07-01",
		"thinking-token-count-2026-05-13",
	}
}

// SurfaceSpec builds the model-surface spec for a provider. origin is the
// upstream (the mock URL for "mock").
func SurfaceSpec(id, provider, origin, model string) (map[string]any, string, error) {
	return surfaceSpec(id, provider, origin, model, false)
}

// MockSurfaceSpec builds the surface of a MockSurfaces entry: the named
// provider's native operations against the local mock origin, authenticated
// with the throwaway mock credential.
func MockSurfaceSpec(m MockSurface, origin string) (map[string]any, error) {
	spec, _, err := surfaceSpec(m.Surface, m.Provider, origin, m.Model, true)
	return spec, err
}

func surfaceSpec(id, provider, origin, model string, mock bool) (map[string]any, string, error) {
	var (
		providerBlock map[string]any
		protocol      string
		opIDs         []string
		auth          map[string]any
		providerID    string
	)
	switch provider {
	case "mock":
		providerID, protocol, opIDs = modelapi.ProviderOpenAICompatible, modelapi.ProtocolOpenAIChat, []string{"compat.chat-completions"}
		providerBlock = map[string]any{"id": providerID, "usage_semantics": modelapi.UsageSemanticsOpenAI, "stream_usage": "inject"}
		auth = map[string]any{"type": "none"}
	case "openai":
		providerID, protocol, opIDs = modelapi.ProviderOpenAI, modelapi.ProtocolOpenAIChat, []string{"openai.v1.chat-completions"}
		providerBlock = map[string]any{"id": providerID, "stream_usage": "inject"}
		if mock {
			protocol, opIDs = modelapi.ProtocolOpenAIResponses, []string{"openai.v1.responses"}
			providerBlock = map[string]any{"id": providerID}
		}
		auth = map[string]any{"type": "bearer", "secret_ref": ProviderSecretRef}
	case "anthropic":
		providerID, protocol, opIDs = modelapi.ProviderAnthropic, modelapi.ProtocolAnthropicMessages, []string{"anthropic.messages", "anthropic.messages-count-tokens"}
		providerBlock = map[string]any{"id": providerID, "anthropic_version": "2023-06-01", "anthropic_betas": ClaudeCodeBetas()}
		auth = map[string]any{"type": "api_key", "in": "header", "name": "x-api-key", "secret_ref": ProviderSecretRef}
	case "gemini":
		providerID, protocol, opIDs = modelapi.ProviderGeminiDeveloper, modelapi.ProtocolGoogleGenAI, []string{"gemini.models.generateContent", "gemini.models.streamGenerateContent", "gemini.models.countTokens"}
		providerBlock = map[string]any{"id": providerID}
		auth = map[string]any{"type": "api_key", "in": "header", "name": "x-goog-api-key", "secret_ref": ProviderSecretRef}
	default:
		return nil, "", fmt.Errorf("unknown provider %q", provider)
	}
	ops, err := modelapi.SelectProviderOperations(providerID, opIDs...)
	if err != nil {
		return nil, "", err
	}
	if mock {
		auth["secret_ref"] = MockSecretRef
	}
	return map[string]any{
		"surface": map[string]any{"id": id, "protocol": protocol, "origin": origin, "models": []string{model}, "operations": ops, "provider": providerBlock},
		"auth":    auth,
	}, protocol, nil
}

// ToolConnectionSpec builds a stateless MCP tool connection to endpoint.
func ToolConnectionSpec(id, endpoint string, tools []string, auth map[string]any, policy map[string]any) map[string]any {
	if auth == nil {
		auth = map[string]any{"type": "none"}
	}
	spec := map[string]any{
		"id": id, "resource_url": endpoint,
		"execution": map[string]any{"id": id, "endpoint": endpoint, "credential_mode": "connection-auth", "allowed_tools": tools, "protocol_version": "2025-06-18", "mode": "stateless-json"},
		"auth":      auth,
	}
	if policy != nil {
		spec["tools"] = policy
	}
	return spec
}

// MockPrice is a placeholder price for the mock model so cost estimation is
// visible locally. The rates are illustrative, not any provider's prices.
func MockPrice(model string) map[string]any {
	return map[string]any{
		"id": model, "model_id": model, "currency": "USD", "scale": 6,
		"meters": map[string]any{
			"input_tokens":  map[string]any{"numerator": 15, "denominator": 100},
			"output_tokens": map[string]any{"numerator": 60, "denominator": 100},
		},
	}
}

// API is a minimal authenticated client for the AIPlex HTTP API.
type API struct {
	URL   string
	Token string
	HTTP  *http.Client
}

// APIError is a non-2xx answer.
type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.Status, strings.TrimSpace(e.Body))
}

// Do sends a JSON request and decodes a JSON response into out (when non-nil).
func (a API) Do(ctx context.Context, method, path string, headers map[string]string, body, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.URL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if a.Token != "" {
		req.Header.Set("Authorization", "Bearer "+a.Token)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	client := a.HTTP
	if client == nil {
		// The request carries the developer token: never follow a redirect.
		client = aiplex.NewNoRedirectHTTPClient(30 * time.Second)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode/100 != 2 {
		return &APIError{Status: resp.StatusCode, Body: string(raw)}
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// Create PUTs a new resource (If-Match: 0).
func (a API) Create(ctx context.Context, path string, spec any) error {
	return a.Do(ctx, http.MethodPut, path, map[string]string{"If-Match": "0"}, spec, nil)
}

// SeedOptions selects what Seed creates.
type SeedOptions struct {
	Provider string // mock (default), openai, anthropic, gemini
	Model    string // default DefaultModel(Provider)
	NoTools  bool
}

// Seed creates, through the normal resource APIs, a model surface for the
// selected provider, a price for the mock model, a tool connection to the
// built-in MCP server and an access point exposing both.
func (s *Stack) Seed(ctx context.Context, o SeedOptions) (*Seeded, error) {
	if o.Provider == "" {
		o.Provider = s.opts.Provider
	}
	if o.Provider == "" {
		o.Provider = "mock"
	}
	if o.Model == "" {
		o.Model = DefaultModel(o.Provider)
	}
	api := API{URL: s.APIURL, Token: s.Token}
	origin := s.ModelsURL
	if o.Provider != "mock" {
		origin, _ = ProviderOrigin(o.Provider)
	}
	out := &Seeded{AccessPoint: SeedAccessPoint, Surface: SeedSurface, Provider: o.Provider, Model: o.Model}
	surface, protocol, err := SurfaceSpec(SeedSurface, o.Provider, origin, o.Model)
	if err != nil {
		return nil, err
	}
	out.Protocol = protocol
	if err = api.Create(ctx, "/api/v1/llm/surfaces/"+SeedSurface, surface); err != nil {
		return nil, fmt.Errorf("create model surface: %w", err)
	}
	apModels := []map[string]any{{"name": o.Model, "surface_ref": SeedSurface, "native_model": o.Model}}
	out.Models = []SeededModel{{Name: o.Model, Surface: SeedSurface, Protocol: protocol}}
	if o.Provider == "mock" {
		if err = api.Create(ctx, "/api/v1/usage/prices/"+o.Model, MockPrice(o.Model)); err != nil {
			return nil, fmt.Errorf("create model price: %w", err)
		}
		out.Price = o.Model
		out.Models[0].Price = o.Model
		specB, _, e := SurfaceSpec(SeedSurfaceB, o.Provider, s.ModelsBURL, o.Model)
		if e != nil {
			return nil, e
		}
		if err = api.Create(ctx, "/api/v1/llm/surfaces/"+SeedSurfaceB, specB); err != nil {
			return nil, fmt.Errorf("create model surface %s: %w", SeedSurfaceB, err)
		}
		for _, m := range MockSurfaces {
			spec, e := MockSurfaceSpec(m, origin)
			if e != nil {
				return nil, e
			}
			if err = api.Create(ctx, "/api/v1/llm/surfaces/"+m.Surface, spec); err != nil {
				return nil, fmt.Errorf("create model surface %s: %w", m.Surface, err)
			}
			if err = api.Create(ctx, "/api/v1/usage/prices/"+m.Model, MockPrice(m.Model)); err != nil {
				return nil, fmt.Errorf("create model price %s: %w", m.Model, err)
			}
			apModels = append(apModels, map[string]any{"name": m.Model, "surface_ref": m.Surface, "native_model": m.Model})
			out.Models = append(out.Models, SeededModel{Name: m.Model, Surface: m.Surface, Protocol: m.Wire, Price: m.Model})
		}
	}
	ap := map[string]any{
		"id": SeedAccessPoint, "display_name": "Local development", "caller_kinds": []string{"user", "agent", "client"},
		"subjects": map[string]any{},
		"models":   apModels,
	}
	if !o.NoTools {
		if err = api.Create(ctx, "/api/v1/mcp/connections/"+SeedConnection, ToolConnectionSpec(SeedConnection, s.MCPURL, MockTools, nil, nil)); err != nil {
			return nil, fmt.Errorf("create tool connection: %w", err)
		}
		keyedAuth := map[string]any{"type": "api_key", "in": "header", "name": KeyedMCPHeader, "secret_ref": KeyedSecretRef}
		if err = api.Create(ctx, "/api/v1/mcp/connections/"+SeedKeyedConnection, ToolConnectionSpec(SeedKeyedConnection, s.KeyedMCPURL, MockTools, keyedAuth, nil)); err != nil {
			return nil, fmt.Errorf("create tool connection %s: %w", SeedKeyedConnection, err)
		}
		out.KeyedConnection = SeedKeyedConnection
		ap["tools"] = []map[string]any{{"connection_ref": SeedConnection}}
		out.Connection, out.Tools = SeedConnection, append([]string(nil), MockTools...)
	}
	agent := map[string]any{
		"id": SeedAgentConnection, "tenant_id": DevTenant, "provider": "google-agent-runtime", "endpoint": s.AgentURL,
		"resource_id": MockAgentResource, "identity_ref": AgentIdentityRef, "governance_mode": "invocation",
	}
	if err = api.Create(ctx, "/api/v1/a2a/connections/"+SeedAgentConnection, agent); err != nil {
		return nil, fmt.Errorf("create agent connection: %w", err)
	}
	out.AgentConnection = SeedAgentConnection
	if err = api.Create(ctx, "/api/v1/access-points/"+SeedAccessPoint, ap); err != nil {
		return nil, fmt.Errorf("create access point: %w", err)
	}
	return out, nil
}
