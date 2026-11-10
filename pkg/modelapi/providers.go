package modelapi

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/vamsiramakrishnan/aiplex/pkg/providerlimits"
)

// Provider IDs for non-Google native surfaces. A Surface without a Provider
// block keeps the Google Cloud surface contract unchanged.
const (
	ProviderOpenAI           = providerlimits.OpenAI
	ProviderAnthropic        = providerlimits.Anthropic
	ProviderAzureOpenAI      = providerlimits.AzureOpenAI
	ProviderBedrock          = providerlimits.Bedrock
	ProviderOpenAICompatible = providerlimits.OpenAICompatible
)

// Client protocol identifiers. The OpenAI/Anthropic ones equal the existing
// clientplex protocol names because the wire format is identical; the
// provider is a separate field. Bedrock formats are genuinely new.
const (
	ProtocolOpenAIChat        = "openai-chat"
	ProtocolOpenAIResponses   = "openai-responses"
	ProtocolAnthropicMessages = "anthropic-messages"
	ProtocolBedrockConverse   = "bedrock-converse"
	ProtocolBedrockInvoke     = "bedrock-invoke"
	// ProtocolOpenAIRealtime is the Realtime API WebSocket protocol
	// (wss://api.openai.com/v1/realtime?model=...).
	ProtocolOpenAIRealtime = "openai-realtime"
)

// EventStream is the AWS binary event-stream response transport
// (application/vnd.amazon.eventstream) used by Bedrock streaming operations.
const EventStream Transport = "aws-eventstream"

// Stream usage policies for Chat Completions, whose streaming usage is only
// reported when the request sets stream_options.include_usage.
const (
	StreamUsageRequire = "require" // reject a streaming request that did not ask for usage
	StreamUsageInject  = "inject"  // add stream_options.include_usage=true (the only body mutation)
	StreamUsageOff     = "off"     // forward unchanged; streaming usage may stay unknown
)

// CredentialScheme names how the upstream credential is presented.
type CredentialScheme string

const (
	SchemeBearer CredentialScheme = "bearer"    // Authorization: Bearer
	SchemeHeader CredentialScheme = "api-key"   // provider-named API key header
	SchemeSigV4  CredentialScheme = "aws-sigv4" // AWS Signature Version 4, service "bedrock"
	SchemeEntra  CredentialScheme = "entra"     // Microsoft Entra bearer token
	SchemeNone   CredentialScheme = "none"      // no upstream credential
)

// Provider is the operator configuration of a non-Google native surface.
// Every field is pinned by the operator; no caller value overrides it.
type Provider struct {
	ID string `json:"id"`
	// BasePath prefixes openai-compatible operation paths (default "/v1").
	BasePath string `json:"base_path,omitempty"`
	// APIVersion is the pinned Azure api-version query value for
	// deployment-scoped operations.
	APIVersion string `json:"api_version,omitempty"`
	// Region is the Bedrock Runtime signing region; it must match the origin.
	Region string `json:"region,omitempty"`
	// Organization and Project pin OpenAI-Organization / OpenAI-Project.
	Organization string `json:"organization,omitempty"`
	Project      string `json:"project,omitempty"`
	// AnthropicVersion pins the anthropic-version header.
	AnthropicVersion string `json:"anthropic_version,omitempty"`
	// AnthropicBetas is the allowlist of caller anthropic-beta values.
	AnthropicBetas []string `json:"anthropic_betas,omitempty"`
	// StreamUsage applies to Chat Completions streaming (see constants).
	StreamUsage string `json:"stream_usage,omitempty"`
	// LimitHeaderPassthrough forwards the provider's rate-limit headers to the
	// caller. Off by default: they describe the shared upstream credential.
	LimitHeaderPassthrough bool `json:"limit_header_passthrough,omitempty"`
	// UsageSemantics is the operator's declaration of the usage contract of
	// a generic OpenAI-compatible server (openai-compatible provider only).
	// "openai" asserts OpenAI Chat Completions semantics: prompt_tokens
	// includes cached_tokens (prompt_tokens_details) and cache writes,
	// completion_tokens includes reasoning, total = prompt + completion. Only
	// then may a token profile settle compat.chat-completions; without it the
	// operation is request-count.
	UsageSemantics string `json:"usage_semantics,omitempty"`
}

// UsageSemanticsOpenAI declares OpenAI usage semantics on a compatible server.
const UsageSemanticsOpenAI = "openai"

// ProviderDescriptor is the extension point for one provider: protocols,
// operation catalog, credential schemes and native usage format per operation.
type ProviderDescriptor struct {
	ID                string             `json:"id"`
	Protocols         []string           `json:"protocols"`
	CredentialSchemes []CredentialScheme `json:"credential_schemes"`
	// APIKeyHeader is the header for SchemeHeader credentials.
	APIKeyHeader string      `json:"api_key_header,omitempty"`
	Operations   []Operation `json:"operations"`
	// DefaultStreamUsage applies when Provider.StreamUsage is empty.
	DefaultStreamUsage string `json:"default_stream_usage,omitempty"`
}

// Native usage formats. Each has its own observer in this package.
const (
	FormatOpenAIChat        = "openai-chat"
	FormatOpenAIResponses   = "openai-responses"
	FormatAnthropicMessages = "anthropic-messages"
	FormatBedrockConverse   = "bedrock-converse"
	FormatBedrockInvoke     = "bedrock-invoke" // model-specific body: Messages-shaped usage is advisory
	FormatOpenAIRealtime    = "openai-realtime"
)

type providerOp struct {
	op       Operation
	format   string
	protocol string
}

func providerCatalog() map[string][]providerOp {
	openaiRef := "https://developers.openai.com/api/reference"
	azureRef := "https://learn.microsoft.com/en-us/azure/ai-foundry/openai/api-version-lifecycle"
	bedrockRef := "https://docs.aws.amazon.com/bedrock/latest/APIReference/API_Operations_Amazon_Bedrock_Runtime.html"
	chat := func(id, path, ref string) providerOp {
		return providerOp{Operation{ID: id, Method: "POST", Path: path, Transport: SSE, Action: Invoke, SourceURL: ref}, FormatOpenAIChat, ProtocolOpenAIChat}
	}
	responses := func(id, path, ref string) providerOp {
		return providerOp{Operation{ID: id, Method: "POST", Path: path, Transport: HTTP, Action: Create, ResourceKind: "response", Stateful: true, SourceURL: ref}, FormatOpenAIResponses, ProtocolOpenAIResponses}
	}
	bedrock := func(id, suffix string, transport Transport, format, protocol string) providerOp {
		return providerOp{Operation{ID: id, Method: "POST", Path: "/model/{model}/" + suffix, Transport: transport, Action: Invoke, SourceURL: bedrockRef}, format, protocol}
	}
	return map[string][]providerOp{
		ProviderOpenAI: {
			chat("openai.v1.chat-completions", "/v1/chat/completions", openaiRef+"/resources/chat/subresources/completions/methods/create"),
			responses("openai.v1.responses", "/v1/responses", openaiRef+"/resources/responses/methods/create"),
			// WebSocket sessions: request-count metered, usage advisory.
			{Operation{ID: "openai.v1.responses-websocket", Method: "GET", Path: "/v1/responses", Transport: WebSocket, Action: Invoke, ResourceKind: "response", Stateful: true, SourceURL: "https://developers.openai.com/api/docs/guides/websocket-mode"}, FormatOpenAIResponses, ProtocolOpenAIResponses},
			{Operation{ID: "openai.v1.realtime", Method: "GET", Path: "/v1/realtime", Transport: WebSocket, Action: Invoke, SourceURL: openaiRef + "/resources/realtime/server-events"}, FormatOpenAIRealtime, ProtocolOpenAIRealtime},
		},
		ProviderAnthropic: {
			{AnthropicOperations()[0], FormatAnthropicMessages, ProtocolAnthropicMessages},
		},
		ProviderAzureOpenAI: {
			chat("azure.v1.chat-completions", "/openai/v1/chat/completions", azureRef),
			responses("azure.v1.responses", "/openai/v1/responses", azureRef),
			chat("azure.deployments.chat-completions", "/openai/deployments/{model}/chat/completions", "https://learn.microsoft.com/en-us/azure/ai-foundry/openai/reference"),
		},
		ProviderBedrock: {
			bedrock("bedrock.converse", "converse", HTTP, FormatBedrockConverse, ProtocolBedrockConverse),
			bedrock("bedrock.converse-stream", "converse-stream", EventStream, FormatBedrockConverse, ProtocolBedrockConverse),
			bedrock("bedrock.invoke", "invoke", HTTP, FormatBedrockInvoke, ProtocolBedrockInvoke),
			bedrock("bedrock.invoke-stream", "invoke-with-response-stream", EventStream, FormatBedrockInvoke, ProtocolBedrockInvoke),
		},
		ProviderOpenAICompatible: {
			chat("compat.chat-completions", "{base}/chat/completions", openaiRef+"/resources/chat/subresources/completions/methods/create"),
		},
	}
}

// Providers returns every non-Google provider descriptor. Each call owns its slices.
func Providers() []ProviderDescriptor {
	out := []ProviderDescriptor{}
	for _, id := range []string{ProviderOpenAI, ProviderAnthropic, ProviderAzureOpenAI, ProviderBedrock, ProviderOpenAICompatible} {
		d, _ := ProviderByID(id)
		out = append(out, d)
	}
	return out
}

// ProviderByID returns one provider descriptor.
func ProviderByID(id string) (ProviderDescriptor, bool) {
	ops, ok := providerCatalog()[id]
	if !ok {
		return ProviderDescriptor{}, false
	}
	d := ProviderDescriptor{ID: id}
	seen := map[string]bool{}
	for _, o := range ops {
		d.Operations = append(d.Operations, o.op)
		if !seen[o.protocol] {
			seen[o.protocol] = true
			d.Protocols = append(d.Protocols, o.protocol)
		}
	}
	switch id {
	case ProviderOpenAI:
		d.CredentialSchemes = []CredentialScheme{SchemeBearer}
		d.DefaultStreamUsage = StreamUsageRequire
	case ProviderAnthropic:
		d.CredentialSchemes = []CredentialScheme{SchemeHeader}
		d.APIKeyHeader = "x-api-key"
	case ProviderAzureOpenAI:
		d.CredentialSchemes = []CredentialScheme{SchemeHeader, SchemeEntra}
		d.APIKeyHeader = "api-key"
		d.DefaultStreamUsage = StreamUsageRequire
	case ProviderBedrock:
		d.CredentialSchemes = []CredentialScheme{SchemeSigV4, SchemeBearer}
	case ProviderOpenAICompatible:
		d.CredentialSchemes = []CredentialScheme{SchemeBearer, SchemeNone}
		d.DefaultStreamUsage = StreamUsageOff
	}
	return d, true
}

// ProviderOperations returns the native operation catalog of one provider.
func ProviderOperations(id string) []Operation {
	d, _ := ProviderByID(id)
	return d.Operations
}

// SelectProviderOperations selects an explicit subset of one provider catalog.
func SelectProviderOperations(provider string, ids ...string) ([]Operation, error) {
	all := map[string]Operation{}
	for _, o := range ProviderOperations(provider) {
		all[o.ID] = o
	}
	out := []Operation{}
	seen := map[string]bool{}
	for _, id := range ids {
		o, ok := all[id]
		if !ok || seen[id] {
			return nil, fmt.Errorf("unknown or duplicate %s operation %q", provider, id)
		}
		seen[id] = true
		out = append(out, o)
	}
	return out, nil
}

func providerOperation(provider, id string) (providerOp, bool) {
	for _, o := range providerCatalog()[provider] {
		if o.op.ID == id {
			return o, true
		}
	}
	return providerOp{}, false
}

// UsageFormat returns the native usage format of a provider operation, or "".
func UsageFormat(provider, operationID string) string {
	o, _ := providerOperation(provider, operationID)
	return o.format
}

// OperationProtocol returns the client protocol of a provider operation, or "".
func OperationProtocol(provider, operationID string) string {
	o, _ := providerOperation(provider, operationID)
	return o.protocol
}

// ProviderID is the provider of a surface: the Provider block when present,
// otherwise the Google publisher (or the protocol when no publisher is set).
func (s Surface) ProviderID() string {
	if s.Provider != nil {
		return s.Provider.ID
	}
	if s.Publisher != "" {
		return s.Publisher
	}
	return s.Protocol
}

// EffectiveStreamUsage returns the Chat Completions streaming usage policy.
func (p Provider) EffectiveStreamUsage() string {
	if p.StreamUsage != "" {
		return p.StreamUsage
	}
	d, _ := ProviderByID(p.ID)
	return d.DefaultStreamUsage
}

var (
	providerModelPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:@/-]{0,255}$`)
	basePathPattern       = regexp.MustCompile(`^(/[A-Za-z0-9][A-Za-z0-9._-]*){1,4}$`)
	headerValuePattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	anthropicVersionRegex = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)
	azureVersionPattern   = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}(-preview)?$`)
	awsRegionPattern      = regexp.MustCompile(`^[a-z]{2}(-gov|-iso[a-z]?)?-[a-z]+-[0-9]$`)
)

func (s Surface) validateProvider() error {
	p := s.Provider
	d, ok := ProviderByID(p.ID)
	if !ok {
		return fmt.Errorf("unsupported model provider %q", p.ID)
	}
	if !validSegment(s.ID) {
		return fmt.Errorf("surface requires a valid id")
	}
	if p.UsageSemantics != "" && (p.ID != ProviderOpenAICompatible || p.UsageSemantics != UsageSemanticsOpenAI) {
		return fmt.Errorf("usage_semantics applies only to the openai-compatible provider and must be %q", UsageSemanticsOpenAI)
	}
	protocolOK := false
	for _, protocol := range d.Protocols {
		if s.Protocol == protocol {
			protocolOK = true
		}
	}
	if !protocolOK {
		return fmt.Errorf("protocol %q is not served by provider %s", s.Protocol, p.ID)
	}
	if s.APIVersion != "" || s.Project != "" || s.Location != "" || s.Publisher != "" || s.Endpoint != "" || len(s.DataPrefixes) > 0 || len(s.Agents) > 0 {
		return fmt.Errorf("Google surface fields do not apply to provider %s", p.ID)
	}
	origin, e := url.Parse(s.Origin)
	if e != nil || origin.Host == "" || origin.User != nil || origin.RawQuery != "" || origin.Fragment != "" || (origin.Path != "" && origin.Path != "/") || (origin.Scheme != "https" && origin.Scheme != "http") {
		return fmt.Errorf("surface origin must be a configured HTTP(S) origin; use provider.base_path for a path prefix")
	}
	host := strings.ToLower(origin.Hostname())
	switch p.ID {
	case ProviderBedrock:
		if !awsRegionPattern.MatchString(p.Region) {
			return fmt.Errorf("bedrock requires a valid signing region")
		}
		if origin.Scheme == "https" && host != "bedrock-runtime."+p.Region+".amazonaws.com" && host != "bedrock-runtime-fips."+p.Region+".amazonaws.com" && host != "bedrock-runtime."+p.Region+".amazonaws.com.cn" {
			return fmt.Errorf("bedrock origin must be the Bedrock Runtime endpoint of the signing region")
		}
	case ProviderAzureOpenAI:
		if origin.Scheme == "https" && !strings.HasSuffix(host, ".openai.azure.com") && !strings.HasSuffix(host, ".services.ai.azure.com") && !strings.HasSuffix(host, ".cognitiveservices.azure.com") {
			return fmt.Errorf("azure-openai origin must be an Azure OpenAI or Foundry resource endpoint")
		}
	}
	if p.Region != "" && p.ID != ProviderBedrock {
		return fmt.Errorf("provider.region applies only to bedrock")
	}
	if p.BasePath != "" && (p.ID != ProviderOpenAICompatible || !basePathPattern.MatchString(p.BasePath)) {
		return fmt.Errorf("provider.base_path applies only to openai-compatible and must be a plain path such as /v1")
	}
	if (p.Organization != "" || p.Project != "") && p.ID != ProviderOpenAI {
		return fmt.Errorf("provider.organization/project apply only to openai")
	}
	for _, value := range []string{p.Organization, p.Project} {
		if value != "" && !headerValuePattern.MatchString(value) {
			return fmt.Errorf("invalid pinned OpenAI organization/project")
		}
	}
	if p.ID == ProviderAnthropic {
		if !anthropicVersionRegex.MatchString(p.AnthropicVersion) {
			return fmt.Errorf("anthropic requires a pinned anthropic_version such as 2023-06-01")
		}
		seenBeta := map[string]bool{}
		for _, beta := range p.AnthropicBetas {
			if !headerValuePattern.MatchString(beta) || seenBeta[beta] {
				return fmt.Errorf("invalid or duplicate anthropic beta")
			}
			seenBeta[beta] = true
		}
	} else if p.AnthropicVersion != "" || len(p.AnthropicBetas) > 0 {
		return fmt.Errorf("anthropic settings apply only to anthropic")
	}
	switch p.StreamUsage {
	case "":
	case StreamUsageRequire, StreamUsageInject, StreamUsageOff:
		if d.DefaultStreamUsage == "" {
			return fmt.Errorf("stream_usage applies only to Chat Completions providers")
		}
	default:
		return fmt.Errorf("stream_usage must be require, inject or off")
	}
	if len(s.Models) == 0 {
		return fmt.Errorf("provider surface requires allowed models")
	}
	pathModels := false
	for _, o := range s.Operations {
		pathModels = pathModels || strings.Contains(o.Path, "{model}")
	}
	seenModel := map[string]bool{}
	for _, m := range s.Models {
		if !providerModelPattern.MatchString(m) || strings.Contains(m, "..") || strings.Contains(m, "//") || seenModel[m] || (pathModels && strings.Contains(m, "/")) {
			return fmt.Errorf("invalid or duplicate model identifier %q", m)
		}
		seenModel[m] = true
	}
	if len(s.Operations) == 0 {
		return fmt.Errorf("surface has no allowed operations")
	}
	seen := map[string]bool{}
	for _, o := range s.Operations {
		canonical, ok := providerOperation(p.ID, o.ID)
		if !ok || seen[o.ID] || o != canonical.op {
			return fmt.Errorf("operation %q is not a %s catalog operation", o.ID, p.ID)
		}
		seen[o.ID] = true
		if o.ID == "azure.deployments.chat-completions" && !azureVersionPattern.MatchString(p.APIVersion) {
			return fmt.Errorf("deployment-scoped Azure operations require a pinned provider.api_version")
		}
	}
	if p.APIVersion != "" && !seen["azure.deployments.chat-completions"] {
		return fmt.Errorf("provider.api_version applies only to deployment-scoped Azure operations")
	}
	return nil
}

func cloneProvider(p *Provider) *Provider {
	if p == nil {
		return nil
	}
	copy := *p
	copy.AnthropicBetas = append([]string(nil), p.AnthropicBetas...)
	return &copy
}

// Credential presents the server-owned upstream credential on one request.
// Implementations must only ever be bound to the surface origin.
type Credential interface {
	Authenticate(context.Context, *http.Request) error
}

// CredentialFunc adapts a function to Credential.
type CredentialFunc func(context.Context, *http.Request) error

func (f CredentialFunc) Authenticate(ctx context.Context, r *http.Request) error { return f(ctx, r) }

// BearerCredential presents a token source as Authorization: Bearer.
func BearerCredential(source TokenSource) Credential {
	return CredentialFunc(func(ctx context.Context, r *http.Request) error {
		if source == nil {
			return fmt.Errorf("token source unavailable")
		}
		token, err := source.Token(ctx)
		if err != nil || strings.TrimSpace(token) == "" || strings.ContainsAny(token, "\r\n") {
			return fmt.Errorf("upstream identity unavailable")
		}
		r.Header.Set("Authorization", "Bearer "+token)
		return nil
	})
}

// NoCredential sends no upstream credential (operator-declared local models).
func NoCredential() Credential {
	return CredentialFunc(func(context.Context, *http.Request) error { return nil })
}
