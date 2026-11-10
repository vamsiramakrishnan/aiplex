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
)

// Names of the resources Seed creates.
const (
	SeedAccessPoint = "local"
	SeedSurface     = "local-models"
	SeedConnection  = "local-tools"
	MockModel       = "mock-gpt"
)

// Seeded describes what Seed created.
type Seeded struct {
	AccessPoint string
	Surface     string
	Provider    string
	Model       string
	Protocol    string // wire the model is served on: openai-chat, anthropic-messages or google-genai
	Price       string // empty when no price was seeded
	Connection  string // empty when no tool connection was seeded
	Tools       []string
}

// DefaultModel is the model wired for each provider unless overridden.
func DefaultModel(provider string) string {
	switch provider {
	case "openai":
		return "gpt-4o-mini"
	case "anthropic":
		return "claude-haiku-4-5"
	case "gemini":
		return "gemini-2.5-flash"
	}
	return MockModel
}

// SurfaceSpec builds the model-surface spec for a provider. origin is the
// upstream (the mock URL for "mock").
func SurfaceSpec(id, provider, origin, model string) (map[string]any, string, error) {
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
		auth = map[string]any{"type": "bearer", "secret_ref": ProviderSecretRef}
	case "anthropic":
		providerID, protocol, opIDs = modelapi.ProviderAnthropic, modelapi.ProtocolAnthropicMessages, []string{"anthropic.messages", "anthropic.messages-count-tokens"}
		providerBlock = map[string]any{"id": providerID, "anthropic_version": "2023-06-01"}
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
			"requests":      map[string]any{"numerator": 0, "denominator": 1},
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
		client = &http.Client{Timeout: 30 * time.Second}
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
	if o.Provider == "mock" {
		if err = api.Create(ctx, "/api/v1/usage/prices/"+o.Model, MockPrice(o.Model)); err != nil {
			return nil, fmt.Errorf("create model price: %w", err)
		}
		out.Price = o.Model
	}
	ap := map[string]any{
		"id": SeedAccessPoint, "display_name": "Local development", "caller_kinds": []string{"user", "agent", "client"},
		"subjects": map[string]any{},
		"models":   []map[string]any{{"name": o.Model, "surface_ref": SeedSurface, "native_model": o.Model}},
	}
	if !o.NoTools {
		if err = api.Create(ctx, "/api/v1/mcp/connections/"+SeedConnection, ToolConnectionSpec(SeedConnection, s.MCPURL, MockTools, nil, nil)); err != nil {
			return nil, fmt.Errorf("create tool connection: %w", err)
		}
		ap["tools"] = []map[string]any{{"connection_ref": SeedConnection}}
		out.Connection, out.Tools = SeedConnection, append([]string(nil), MockTools...)
	}
	if err = api.Create(ctx, "/api/v1/access-points/"+SeedAccessPoint, ap); err != nil {
		return nil, fmt.Errorf("create access point: %w", err)
	}
	return out, nil
}
