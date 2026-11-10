package modelapi

// Usage-format conformance harness.
//
// Every fixture under testdata/usage-formats/{openai,anthropic,google} is fed
// through the real gateway path that serves its surface/transport (an
// httptest upstream returns the fixture bytes; the gateway's own observers
// produce the observations). Fixtures whose surface has no gateway path are
// listed explicitly as unsupported, with a reason, so coverage stays visible.
//
// The three expected.json files were written independently and do NOT share
// one convention. The harness therefore converts BOTH the expected values and
// the observed counters into one canonical form before comparing:
//
//	input_tokens        all prompt-side tokens: fresh + cache read + cache write
//	                    (tool-use prompt tokens excluded)
//	output_tokens       all billed generated tokens, INCLUDING reasoning/thoughts
//	cache_read_tokens   cache reads (subset of input_tokens)
//	cache_write_tokens  cache writes (subset of input_tokens)
//	reasoning_tokens    informational subset of output_tokens
//	total_tokens        provider-reported total when the format reports one,
//	                    otherwise input_tokens + output_tokens
//
// Expected -> canonical, per fixture convention (see expectConvention):
//
//	openai        verbatim (prompt/input includes cache read+write; output
//	              includes reasoning; total = input + output)
//	anthropic     input = input + cache_read + cache_write (anthropic/ README:
//	              input_tokens is EXCLUSIVE of cache); total as given (derived)
//	gemini        output = output + reasoning (google/ README: thoughts are
//	              NOT inside candidates); total as given (provider total)
//	google-claude verbatim input (google/ README already sums 87+2048+300);
//	              total null -> derived
//	vertex-openai output = completion + reasoning (completion EXCLUDES
//	              reasoning on the Vertex OpenAI-compatible endpoint)
//	no-consumption countTokens-style estimates are not usage: all null
//
// Observed -> canonical uses the documented vocabulary of the AIPlex counters
// for the operation's native semantics (see nativeSemantics).

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/vamsiramakrishnan/aiplex/pkg/execution"
	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

type conformanceStatus string

const (
	statusExact       conformanceStatus = "exact"
	statusAdvisory    conformanceStatus = "advisory"
	statusUnsupported conformanceStatus = "unsupported"
)

type expectConvention int

const (
	convOpenAI expectConvention = iota
	convAnthropic
	convGemini
	convGoogleClaude
	convVertexOpenAI
	convNoConsumption
)

type nativeSemantics int

const (
	natOpenAI    nativeSemantics = iota // input inclusive of cache; output inclusive of reasoning
	natAnthropic                        // input exclusive of cache (raw) or uncached+cached (normalized)
	natGemini                           // input inclusive of cached; thoughts additive to output
)

// strictOutcome is what a token-metered (strict) lease must see.
type strictOutcome int

const (
	strictNotApplicable strictOutcome = iota // advisory/unsupported row: no token profile
	strictSettles                            // complete terminal usage settles, values must match
	strictRefuses                            // complete response but the profile fails closed
	strictUnknown                            // incomplete: nothing may settle
)

type fixtureCall struct {
	surface  func(origin string) Surface
	path     string
	body     string
	upstream int    // upstream HTTP status (default 200)
	media    string // upstream content type override
	// ws marks a WebSocket fixture replayed through a relay.
	ws string
	// contentType overrides the request Content-Type (multipart uploads).
	contentType string
	// method overrides POST; bind pre-binds resources to the test caller so
	// that retrieval-style operations pass their ownership check.
	method string
	bind   []Resource
}

type fixtureRoute struct {
	provider, file     string
	surface, transport string
	status             conformanceStatus
	reason             string
	// formatOnly: the surface itself is not served, but the fixture's body
	// format is shared with a served operation and is checked through it.
	formatOnly   bool
	call         *fixtureCall
	conv         expectConvention
	native       nativeSemantics
	final        bool
	strict       strictOutcome
	strictReason string
	// allowUnknown: canonical fields the observer may leave unknown although
	// the fixture states a value (an UNCONFIRMED composition), with the reason.
	allowUnknown map[string]string
	// check replaces the HTTP replay for fixtures that are not one exchange
	// (batch results files aggregated across fetches).
	check func(t *testing.T, data []byte, want canonical)
}

type expectedUsage struct {
	Input      *int64 `json:"input_tokens"`
	Output     *int64 `json:"output_tokens"`
	CacheRead  *int64 `json:"cache_read_tokens"`
	CacheWrite *int64 `json:"cache_write_tokens"`
	Reasoning  *int64 `json:"reasoning_tokens"`
	Total      *int64 `json:"total_tokens"`
	Notes      string `json:"notes"`
}

var canonicalFields = []string{"input_tokens", "output_tokens", "cache_read_tokens", "cache_write_tokens", "reasoning_tokens", "total_tokens"}

type canonical map[string]*int64

func i64(v int64) *int64 { return &v }

func addOpt(a *int64, bs ...*int64) *int64 {
	if a == nil {
		return nil
	}
	sum := *a
	for _, b := range bs {
		if b != nil {
			sum += *b
		}
	}
	return &sum
}

func deriveTotal(c canonical) {
	if c["total_tokens"] == nil && c["input_tokens"] != nil && c["output_tokens"] != nil {
		c["total_tokens"] = i64(*c["input_tokens"] + *c["output_tokens"])
	}
}

func expectedCanonical(conv expectConvention, e expectedUsage) canonical {
	c := canonical{"input_tokens": e.Input, "output_tokens": e.Output, "cache_read_tokens": e.CacheRead, "cache_write_tokens": e.CacheWrite, "reasoning_tokens": e.Reasoning, "total_tokens": e.Total}
	switch conv {
	case convAnthropic:
		c["input_tokens"] = addOpt(e.Input, e.CacheRead, e.CacheWrite)
	case convGemini, convVertexOpenAI:
		c["output_tokens"] = addOpt(e.Output, e.Reasoning)
	case convNoConsumption:
		return canonical{}
	}
	if e.Output == nil {
		// Input-only formats (embeddings) state no total of their own here.
		return c
	}
	deriveTotal(c)
	return c
}

func observedCanonical(native nativeSemantics, counters map[string]int64) canonical {
	get := func(key string) *int64 {
		if v, ok := counters[key]; ok {
			return i64(v)
		}
		return nil
	}
	c := canonical{"input_tokens": get("input_tokens"), "output_tokens": get("output_tokens"), "cache_read_tokens": get("cached_tokens"), "cache_write_tokens": get("cache_write_tokens"), "reasoning_tokens": get("thought_tokens"), "total_tokens": get("total_tokens")}
	switch native {
	case natAnthropic:
		if _, normalized := counters["uncached_input_tokens"]; normalized {
			// Normalized vocabulary: input_tokens is already total input.
			c["input_tokens"] = get("input_tokens")
		} else {
			c["input_tokens"] = addOpt(get("input_tokens"), get("cached_tokens"), get("cache_write_tokens"))
		}
	case natGemini:
		c["output_tokens"] = addOpt(get("output_tokens"), get("thought_tokens"))
	}
	if len(counters) > 0 && c["output_tokens"] != nil {
		deriveTotal(c)
	}
	return c
}

func describe(c canonical) string {
	parts := []string{}
	for _, f := range canonicalFields {
		if c[f] == nil {
			parts = append(parts, f+"=null")
		} else {
			parts = append(parts, fmt.Sprintf("%s=%d", f, *c[f]))
		}
	}
	return strings.Join(parts, " ")
}

func compareCanonical(want, got canonical, allow map[string]string) []string {
	var diffs []string
	for _, f := range canonicalFields {
		w, g := want[f], got[f]
		switch {
		case w == nil && g == nil:
		case w != nil && g != nil && *w == *g:
		case w != nil && g == nil && allow[f] != "":
		default:
			ws, gs := "null", "null"
			if w != nil {
				ws = fmt.Sprint(*w)
			}
			if g != nil {
				gs = fmt.Sprint(*g)
			}
			diffs = append(diffs, fmt.Sprintf("%s want %s got %s", f, ws, gs))
		}
	}
	return diffs
}

// ---- surfaces and calls ----

const (
	vertexBase  = "/v1/projects/test-project/locations/global"
	vertexModel = vertexBase + "/publishers/%s/models/model-a:%s"
)

func googleSurfaceFor(publisher string, ids ...string) func(string) Surface {
	return func(origin string) Surface {
		ops, err := SelectOperations(ids...)
		if err != nil {
			panic(err)
		}
		return Surface{ID: "native", Protocol: "google-native", Origin: origin, APIVersion: "v1", Project: "test-project", Location: "global", Publisher: publisher, Endpoint: "openapi", Models: []string{"model-a", "model-b"}, Operations: ops}
	}
}

func providerSurfaceFor(p Provider, protocol string, models []string, ids ...string) func(string) Surface {
	return func(origin string) Surface {
		ops, err := SelectProviderOperations(p.ID, ids...)
		if err != nil {
			panic(err)
		}
		copy := p
		return Surface{ID: "native", Protocol: protocol, Origin: origin, Models: models, Operations: ops, Provider: &copy}
	}
}

var (
	callVertexGenerate = &fixtureCall{surface: googleSurfaceFor("google", "models.generateContent"), path: fmt.Sprintf(vertexModel, "google", "generateContent"), body: controlledBody}
	callVertexStream   = &fixtureCall{surface: googleSurfaceFor("google", "models.streamGenerateContent"), path: fmt.Sprintf(vertexModel, "google", "streamGenerateContent"), body: controlledBody}
	callVertexCount    = &fixtureCall{surface: googleSurfaceFor("google", "models.countTokens"), path: fmt.Sprintf(vertexModel, "google", "countTokens"), body: controlledBody}
	callVertexEmbed    = &fixtureCall{surface: googleSurfaceFor("google", "models.embedContent"), path: fmt.Sprintf(vertexModel, "google", "embedContent"), body: `{"content":{"parts":[{"text":"x"}]}}`}
	callVertexPredict  = &fixtureCall{surface: googleSurfaceFor("google", "models.predict"), path: fmt.Sprintf(vertexModel, "google", "predict"), body: `{"instances":[{"content":"x"}]}`}
	callInteraction    = &fixtureCall{surface: googleSurfaceFor("google", "interaction.create"), path: vertexBase + "/interactions", body: `{"model":"model-a","input":"hi","store":false}`}
	callVertexChat     = &fixtureCall{surface: googleSurfaceFor("google", "openai.chat-completions"), path: vertexBase + "/endpoints/openapi/chat/completions", body: `{"model":"google/model-a","messages":[{"role":"user","content":"hi"}]}`}
	callVertexClaude   = &fixtureCall{surface: googleSurfaceFor("anthropic", "models.rawPredict"), path: fmt.Sprintf(vertexModel, "anthropic", "rawPredict"), body: `{"anthropic_version":"vertex-2023-10-16","max_tokens":8,"messages":[]}`}
	callVertexClaudeS  = &fixtureCall{surface: googleSurfaceFor("anthropic", "models.streamRawPredict"), path: fmt.Sprintf(vertexModel, "anthropic", "streamRawPredict"), body: `{"anthropic_version":"vertex-2023-10-16","max_tokens":8,"messages":[],"stream":true}`}
	callOpenAIChat     = &fixtureCall{surface: providerSurfaceFor(Provider{ID: ProviderOpenAI}, ProtocolOpenAIChat, []string{"fixture-model-a"}, "openai.v1.chat-completions"), path: "/v1/chat/completions", body: `{"model":"fixture-model-a","messages":[{"role":"user","content":"hi"}]}`}
	callOpenAIChatS    = &fixtureCall{surface: providerSurfaceFor(Provider{ID: ProviderOpenAI}, ProtocolOpenAIChat, []string{"fixture-model-a"}, "openai.v1.chat-completions"), path: "/v1/chat/completions", body: `{"model":"fixture-model-a","messages":[{"role":"user","content":"hi"}],"stream":true,"stream_options":{"include_usage":true}}`}
	callResponses      = &fixtureCall{surface: providerSurfaceFor(Provider{ID: ProviderOpenAI}, ProtocolOpenAIResponses, []string{"fixture-model-a"}, "openai.v1.responses"), path: "/v1/responses", body: `{"model":"fixture-model-a","input":"hi"}`}
	callAnthropic      = &fixtureCall{surface: providerSurfaceFor(Provider{ID: ProviderAnthropic, AnthropicVersion: "2023-06-01"}, ProtocolAnthropicMessages, []string{"claude-fixture"}, "anthropic.messages"), path: "/v1/messages", body: `{"model":"claude-fixture","max_tokens":8,"messages":[]}`}
	bedrockModel       = "anthropic.claude-fixture-v1:0"
	callConverse       = &fixtureCall{surface: providerSurfaceFor(Provider{ID: ProviderBedrock, Region: "us-east-1"}, ProtocolBedrockConverse, []string{bedrockModel}, "bedrock.converse"), path: "/model/" + bedrockModel + "/converse", body: `{"messages":[]}`}
	callConverseS      = &fixtureCall{surface: providerSurfaceFor(Provider{ID: ProviderBedrock, Region: "us-east-1"}, ProtocolBedrockConverse, []string{bedrockModel}, "bedrock.converse-stream"), path: "/model/" + bedrockModel + "/converse-stream", body: `{"messages":[]}`, media: "application/vnd.amazon.eventstream"}
	callInvoke         = &fixtureCall{surface: providerSurfaceFor(Provider{ID: ProviderBedrock, Region: "us-east-1"}, ProtocolBedrockInvoke, []string{bedrockModel}, "bedrock.invoke"), path: "/model/" + bedrockModel + "/invoke", body: `{"anthropic_version":"bedrock-2023-05-31","max_tokens":8,"messages":[]}`}
	callInvokeS        = &fixtureCall{surface: providerSurfaceFor(Provider{ID: ProviderBedrock, Region: "us-east-1"}, ProtocolBedrockInvoke, []string{bedrockModel}, "bedrock.invoke-stream"), path: "/model/" + bedrockModel + "/invoke-with-response-stream", body: `{"anthropic_version":"bedrock-2023-05-31","max_tokens":8,"messages":[]}`, media: "application/vnd.amazon.eventstream"}
)

var (
	callAzureChat  = &fixtureCall{surface: providerSurfaceFor(Provider{ID: ProviderAzureOpenAI}, ProtocolOpenAIChat, []string{"fixture-model-a"}, "azure.v1.chat-completions"), path: "/openai/v1/chat/completions", body: `{"model":"fixture-model-a","messages":[{"role":"user","content":"hi"}]}`}
	callAzureChatS = with(callAzureChat, func(c *fixtureCall) {
		c.body = `{"model":"fixture-model-a","messages":[{"role":"user","content":"hi"}],"stream":true,"stream_options":{"include_usage":true}}`
	})
	callAzureDeploy = &fixtureCall{surface: providerSurfaceFor(Provider{ID: ProviderAzureOpenAI, APIVersion: "2024-10-21"}, ProtocolOpenAIChat, []string{"fixture-model-a"}, "azure.deployments.chat-completions"), path: "/openai/deployments/fixture-model-a/chat/completions", body: `{"messages":[{"role":"user","content":"hi"}]}`}
	callAzureResp   = &fixtureCall{surface: providerSurfaceFor(Provider{ID: ProviderAzureOpenAI}, ProtocolOpenAIResponses, []string{"fixture-model-a"}, "azure.v1.responses"), path: "/openai/v1/responses", body: `{"model":"fixture-model-a","input":"hi"}`}
	// The generic compatible surface declares OpenAI usage semantics; the
	// quota host refuses a token profile without that declaration.
	callCompat  = &fixtureCall{surface: providerSurfaceFor(Provider{ID: ProviderOpenAICompatible, UsageSemantics: UsageSemanticsOpenAI}, ProtocolOpenAIChat, []string{"fixture-model-a"}, "compat.chat-completions"), path: "/v1/chat/completions", body: `{"model":"fixture-model-a","messages":[{"role":"user","content":"hi"}]}`}
	callCompatS = with(callCompat, func(c *fixtureCall) {
		c.body = `{"model":"fixture-model-a","messages":[{"role":"user","content":"hi"}],"stream":true,"stream_options":{"include_usage":true}}`
	})
	callLive        = &fixtureCall{surface: googleSurfaceFor("google", "live.connect"), path: "/ws/google.cloud.aiplatform.v1.LlmBidiService/BidiGenerateContent", body: `{"setup":{"model":"model-a"}}`, ws: "live"}
	callRealtime    = &fixtureCall{surface: providerSurfaceFor(Provider{ID: ProviderOpenAI}, ProtocolOpenAIRealtime, []string{"fixture-model-c"}, "openai.v1.realtime"), path: "/v1/realtime?model=fixture-model-c", ws: "realtime"}
	callResponsesWS = &fixtureCall{surface: providerSurfaceFor(Provider{ID: ProviderOpenAI}, ProtocolOpenAIResponses, []string{"fixture-model-b"}, "openai.v1.responses-websocket"), path: "/v1/responses", ws: "responses"}
)

func with(c *fixtureCall, mutate func(*fixtureCall)) *fixtureCall {
	copy := *c
	mutate(&copy)
	return &copy
}

// ---- the route table: one entry per fixture ----

func unsupported(provider, file, surface, transport, reason string) fixtureRoute {
	return fixtureRoute{provider: provider, file: file, surface: surface, transport: transport, status: statusUnsupported, reason: reason}
}

func usageRoutes() []fixtureRoute {
	exact := func(provider, file, surface, transport string, call *fixtureCall, conv expectConvention, native nativeSemantics, final bool, strict strictOutcome, why string) fixtureRoute {
		return fixtureRoute{provider: provider, file: file, surface: surface, transport: transport, status: statusExact, call: call, conv: conv, native: native, final: final, strict: strict, strictReason: why}
	}
	advisory := func(provider, file, surface, transport string, call *fixtureCall, conv expectConvention, native nativeSemantics, final bool, reason string) fixtureRoute {
		return fixtureRoute{provider: provider, file: file, surface: surface, transport: transport, status: statusAdvisory, reason: reason, call: call, conv: conv, native: native, final: final}
	}
	formatOnly := func(r fixtureRoute, status conformanceStatus, reason string) fixtureRoute {
		r.status, r.reason, r.formatOnly, r.strict = status, reason, true, strictNotApplicable
		return r
	}
	const noDA = "no Gemini Developer API (generativelanguage.googleapis.com) surface; body format is shared with the Vertex operation and checked through it"
	const cwRefuse = "positive cache writes are outside the output-inclusive token profile (fail closed)"
	const toolRefuse = "server-tool quantities are outside the output-inclusive token profile (fail closed)"
	const noCached = "openai-text requires a cached-input breakdown that this endpoint does not report (fail closed)"
	const requestCount = "operation coverage is request-count; token usage is recorded as advisory ledger telemetry"
	const sessionReason = "WebSocket session: one request-count admission and one settlement per session; usage events are advisory, final only with clean close and complete per-response/snapshot evidence, otherwise labelled lower_bound/ambiguous"
	routes := []fixtureRoute{
		// ---------------- OpenAI ----------------
		exact("openai", "chat.json", "chat-completions", "json", callOpenAIChat, convOpenAI, natOpenAI, true, strictSettles, ""),
		exact("openai", "chat.sse", "chat-completions", "sse", callOpenAIChatS, convOpenAI, natOpenAI, true, strictSettles, ""),
		exact("openai", "chat.interrupted.sse", "chat-completions", "sse", callOpenAIChatS, convOpenAI, natOpenAI, false, strictUnknown, ""),
		exact("openai", "chat.nousage.sse", "chat-completions", "sse", callOpenAIChatS, convOpenAI, natOpenAI, false, strictUnknown, ""),
		exact("openai", "responses.json", "responses", "json", callResponses, convOpenAI, natOpenAI, true, strictSettles, ""),
		exact("openai", "responses-web-search.json", "responses", "json", callResponses, convOpenAI, natOpenAI, true, strictRefuses, toolRefuse),
		exact("openai", "responses.sse", "responses", "sse", callResponses, convOpenAI, natOpenAI, true, strictSettles, ""),
		exact("openai", "responses.failed.sse", "responses", "sse", callResponses, convOpenAI, natOpenAI, false, strictUnknown, ""),
		exact("openai", "responses.incomplete.sse", "responses", "sse", callResponses, convOpenAI, natOpenAI, false, strictUnknown, ""),
		exact("openai", "responses.error.sse", "responses", "sse", callResponses, convOpenAI, natOpenAI, false, strictUnknown, ""),
		formatOnly(exact("openai", "responses.background.queued.json", "responses-retrieve", "json", callResponses, convOpenAI, natOpenAI, false, strictUnknown, ""), statusUnsupported, "GET /v1/responses/{id} (background poll) is not an OpenAI provider operation; response body checked through the Responses observer"),
		formatOnly(exact("openai", "responses.background.cancelled.json", "responses-cancel", "json", callResponses, convOpenAI, natOpenAI, false, strictUnknown, ""), statusUnsupported, "POST /v1/responses/{id}/cancel is not an OpenAI provider operation; response body checked through the Responses observer"),
		unsupported("openai", "embeddings.json", "embeddings", "json", "no /v1/embeddings operation on the OpenAI provider surface"),
		unsupported("openai", "moderation.json", "moderations", "json", "no /v1/moderations operation (no usage documented)"),

		// ---------------- Azure OpenAI (docs/reference/usage-formats/azure-openai.md) ----------------
		exact("azure-openai", "chat.json", "v1-chat-completions", "json", callAzureChat, convOpenAI, natOpenAI, true, strictSettles, ""),
		exact("azure-openai", "chat.sse", "v1-chat-completions", "sse", callAzureChatS, convOpenAI, natOpenAI, true, strictSettles, ""),
		exact("azure-openai", "chat.interrupted.sse", "v1-chat-completions", "sse", callAzureChatS, convOpenAI, natOpenAI, false, strictUnknown, ""),
		exact("azure-openai", "deployments-chat.json", "deployments-chat-completions", "json", callAzureDeploy, convOpenAI, natOpenAI, true, strictRefuses, "prompt_tokens_details is optional in the Azure CompletionUsage schema and absent here; openai-text requires the cached-input breakdown (fail closed)"),
		exact("azure-openai", "responses.json", "v1-responses", "json", callAzureResp, convOpenAI, natOpenAI, true, strictSettles, ""),
		exact("azure-openai", "responses.sse", "v1-responses", "sse", callAzureResp, convOpenAI, natOpenAI, true, strictSettles, ""),

		// ---------------- Generic OpenAI-compatible (usage_semantics: "openai") ----------------
		exact("openai-compatible", "chat.json", "chat-completions", "json", callCompat, convOpenAI, natOpenAI, true, strictRefuses, "server reports no prompt_tokens_details; openai-text requires the cached-input breakdown (fail closed)"),
		exact("openai-compatible", "chat.final-usage.sse", "chat-completions", "sse", callCompatS, convOpenAI, natOpenAI, true, strictSettles, ""),
		exact("openai-compatible", "chat.no-done.sse", "chat-completions", "sse", callCompatS, convOpenAI, natOpenAI, false, strictUnknown, ""),
		exact("openai-compatible", "chat.nousage.sse", "chat-completions", "sse", callCompatS, convOpenAI, natOpenAI, false, strictUnknown, ""),

		// ---------------- Anthropic ----------------
		exact("anthropic", "messages.json", "messages", "json", callAnthropic, convAnthropic, natAnthropic, true, strictRefuses, cwRefuse),
		exact("anthropic", "messages.sse", "messages", "sse", with(callAnthropic, func(c *fixtureCall) { c.body = `{"model":"claude-fixture","max_tokens":8,"messages":[],"stream":true}` }), convAnthropic, natAnthropic, true, strictSettles, ""),
		exact("anthropic", "messages-server-tool.sse", "messages", "sse", with(callAnthropic, func(c *fixtureCall) { c.body = `{"model":"claude-fixture","max_tokens":8,"messages":[],"stream":true}` }), convAnthropic, natAnthropic, true, strictRefuses, toolRefuse),
		exact("anthropic", "messages-usage-absent.sse", "messages", "sse", with(callAnthropic, func(c *fixtureCall) { c.body = `{"model":"claude-fixture","max_tokens":8,"messages":[],"stream":true}` }), convAnthropic, natAnthropic, false, strictUnknown, ""),
		exact("anthropic", "messages-interrupted.sse", "messages", "sse", with(callAnthropic, func(c *fixtureCall) { c.body = `{"model":"claude-fixture","max_tokens":8,"messages":[],"stream":true}` }), convAnthropic, natAnthropic, false, strictUnknown, ""),
		exact("anthropic", "messages-error-midstream.sse", "messages", "sse", with(callAnthropic, func(c *fixtureCall) { c.body = `{"model":"claude-fixture","max_tokens":8,"messages":[],"stream":true}` }), convAnthropic, natAnthropic, false, strictUnknown, ""),
		exact("anthropic", "messages-iterations.json", "messages", "json", callAnthropic, convAnthropic, natAnthropic, true, strictSettles, ""),
		exact("anthropic", "messages-code-execution.json", "messages", "json", callAnthropic, convAnthropic, natAnthropic, true, strictRefuses, toolRefuse),
		unsupported("anthropic", "count-tokens.json", "count-tokens", "json", "no /v1/messages/count_tokens operation (an estimate, never consumption)"),
		unsupported("anthropic", "managed-agents-session.json", "managed-agents", "json", "no Managed Agents operation: the session object usage is a documented cumulative snapshot (platform.claude.com/docs/en/managed-agents/session-observability, re-read 2026-11-10), but the session.usage event envelope is still not shown and a session has no single terminal exchange through which a snapshot could settle once without double counting repeated polls"),
		unsupported("anthropic", "ratelimit.json", "rate-limit-metadata", "json", "documentation blob of rate-limit headers/429 bodies, not a response; header parsing is covered by pkg/providerlimits"),
		exact("anthropic", "vertex-raw.json", "vertex-rawPredict", "json", callVertexClaude, convAnthropic, natAnthropic, true, strictSettles, ""),
		exact("anthropic", "vertex-raw.sse", "vertex-streamRawPredict", "sse", callVertexClaudeS, convAnthropic, natAnthropic, true, strictSettles, ""),
		exact("anthropic", "vertex-raw-interrupted.sse", "vertex-streamRawPredict", "sse", callVertexClaudeS, convAnthropic, natAnthropic, false, strictUnknown, ""),
		exact("anthropic", "bedrock-invoke.json", "bedrock-invoke", "json", callInvoke, convAnthropic, natAnthropic, true, strictSettles, ""),
		exact("anthropic", "bedrock-invoke-stream.jsonl", "bedrock-invoke-stream", "aws-eventstream", callInvokeS, convAnthropic, natAnthropic, true, strictSettles, ""),
		exact("anthropic", "bedrock-invoke-stream-interrupted.jsonl", "bedrock-invoke-stream", "aws-eventstream", callInvokeS, convAnthropic, natAnthropic, false, strictUnknown, ""),
		exact("anthropic", "bedrock-converse.json", "bedrock-converse", "json", callConverse, convAnthropic, natAnthropic, true, strictSettles, ""),
		exact("anthropic", "bedrock-converse-stream.jsonl", "bedrock-converse-stream", "aws-eventstream", callConverseS, convAnthropic, natAnthropic, true, strictSettles, ""),
		exact("anthropic", "bedrock-converse-stream-interrupted.jsonl", "bedrock-converse-stream", "aws-eventstream", callConverseS, convAnthropic, natAnthropic, false, strictUnknown, ""),
		formatOnly(exact("anthropic", "bedrock-mantle.sse", "bedrock-mantle-messages", "sse", with(callAnthropic, func(c *fixtureCall) { c.body = `{"model":"claude-fixture","max_tokens":8,"messages":[],"stream":true}` }), convAnthropic, natAnthropic, true, strictNotApplicable, ""), statusUnsupported, "no bedrock-mantle surface (SigV4 service bedrock-mantle, origin bedrock-mantle.{region}.api.aws); Messages SSE format checked through the Anthropic observer"),

		// ---------------- Google ----------------
		formatOnly(exact("google", "gemini-generate.json", "gemini-generateContent", "json", callVertexGenerate, convGemini, natGemini, true, strictNotApplicable, ""), statusUnsupported, noDA),
		formatOnly(exact("google", "gemini-generate-tooluse.json", "gemini-generateContent", "json", callVertexGenerate, convGemini, natGemini, true, strictNotApplicable, ""), statusUnsupported, noDA),
		formatOnly(exact("google", "gemini-generate.sse", "gemini-streamGenerateContent", "sse", callVertexStream, convGemini, natGemini, true, strictNotApplicable, ""), statusUnsupported, noDA),
		formatOnly(exact("google", "gemini-generate.stream.json", "gemini-streamGenerateContent", "json-array", with(callVertexStream, func(c *fixtureCall) { c.media = "application/json" }), convGemini, natGemini, true, strictNotApplicable, ""), statusUnsupported, noDA),
		formatOnly(exact("google", "gemini-generate-interrupted.sse", "gemini-streamGenerateContent", "sse", callVertexStream, convGemini, natGemini, false, strictNotApplicable, ""), statusUnsupported, noDA),
		formatOnly(exact("google", "gemini-generate-error-429.json", "gemini-generateContent", "json", with(callVertexGenerate, func(c *fixtureCall) { c.upstream = 429 }), convGemini, natGemini, false, strictNotApplicable, ""), statusUnsupported, noDA),
		formatOnly(exact("google", "gemini-count-tokens.json", "gemini-countTokens", "json", callVertexCount, convNoConsumption, natGemini, false, strictNotApplicable, ""), statusUnsupported, noDA+"; countTokens is an estimate and must never be recorded as usage"),
		formatOnly(exact("google", "gemini-embed.json", "gemini-embedContent", "json", callVertexEmbed, convGemini, natGemini, true, strictNotApplicable, ""), statusUnsupported, noDA),
		unsupported("google", "gemini-cachedcontent-create.json", "gemini-cachedContents", "json", "no Gemini Developer API surface and not replayed: DA cache names (cachedContents/{id}) are outside the Vertex resource scope, and how cache creation is billed is UNCONFIRMED (cache.create is request-count; usageMetadata.totalTokenCount is not mapped to cache writes)"),
		formatOnly(exact("google", "gemini-interactions.json", "gemini-interactions", "json", callInteraction, convGemini, natGemini, true, strictNotApplicable, ""), statusUnsupported, noDA),
		formatOnly(exact("google", "gemini-interactions.sse", "gemini-interactions", "sse", callInteraction, convGemini, natGemini, true, strictNotApplicable, ""), statusUnsupported, noDA),
		formatOnly(exact("google", "gemini-interactions-interrupted.sse", "gemini-interactions", "sse", callInteraction, convGemini, natGemini, false, strictNotApplicable, ""), statusUnsupported, noDA),
		formatOnly(advisory("google", "gemini-live.ws.jsonl", "gemini-live", "websocket", callLive, convGemini, natGemini, true, ""), statusUnsupported, noDA+" (Live messages replayed through the Vertex Live relay)"),
		formatOnly(advisory("google", "gemini-live-interrupted.ws.jsonl", "gemini-live", "websocket", callLive, convGemini, natGemini, false, ""), statusUnsupported, noDA+" (Live messages replayed through the Vertex Live relay)"),
		exact("google", "vertex-generate.json", "vertex-generateContent", "json", callVertexGenerate, convGemini, natGemini, true, strictSettles, ""),
		exact("google", "vertex-generate-flex.json", "vertex-generateContent", "json", callVertexGenerate, convGemini, natGemini, true, strictSettles, ""),
		exact("google", "vertex-generate-image.json", "vertex-generateContent", "json", callVertexGenerate, convGemini, natGemini, true, strictSettles, ""),
		exact("google", "vertex-generate-error-429.json", "vertex-generateContent", "json", with(callVertexGenerate, func(c *fixtureCall) { c.upstream = 429 }), convGemini, natGemini, false, strictUnknown, ""),
		exact("google", "vertex-generate.sse", "vertex-streamGenerateContent", "sse", callVertexStream, convGemini, natGemini, true, strictSettles, ""),
		exact("google", "vertex-generate-cumulative.sse", "vertex-streamGenerateContent", "sse", callVertexStream, convGemini, natGemini, true, strictSettles, ""),
		exact("google", "vertex-generate-interrupted.sse", "vertex-streamGenerateContent", "sse", callVertexStream, convGemini, natGemini, false, strictUnknown, ""),
		// JSON-array framing (no alt=sse) is buffered under a lease, observed
		// exactly and settled before delivery; the quota host admits it.
		exact("google", "vertex-generate.stream.json", "vertex-streamGenerateContent", "json-array", with(callVertexStream, func(c *fixtureCall) { c.media = "application/json" }), convGemini, natGemini, true, strictSettles, ""),
		advisory("google", "vertex-count-tokens.json", "vertex-countTokens", "json", callVertexCount, convNoConsumption, natGemini, false, "countTokens is an input-size estimate, never consumption; only the request is counted"),
		exact("google", "vertex-embed-predict.json", "vertex-predict", "json", callVertexPredict, convGemini, natGemini, true, strictSettles, ""),
		exact("google", "vertex-openai-chat.json", "vertex-openai-chat", "json", callVertexChat, convVertexOpenAI, natOpenAI, true, strictSettles, ""),
		exact("google", "vertex-openai-chat.sse", "vertex-openai-chat", "sse", with(callVertexChat, func(c *fixtureCall) {
			c.body = `{"model":"google/model-a","messages":[{"role":"user","content":"hi"}],"stream":true}`
		}), convVertexOpenAI, natOpenAI, true, strictSettles, ""),
		exact("google", "vertex-openai-chat-interrupted.sse", "vertex-openai-chat", "sse", with(callVertexChat, func(c *fixtureCall) {
			c.body = `{"model":"google/model-a","messages":[{"role":"user","content":"hi"}],"stream":true}`
		}), convVertexOpenAI, natOpenAI, false, strictUnknown, ""),
		exact("google", "vertex-maas-chat.json", "vertex-openai-chat", "json", callVertexChat, convOpenAI, natOpenAI, true, strictSettles, ""),
		exact("google", "vertex-maas-chat.sse", "vertex-openai-chat", "sse", with(callVertexChat, func(c *fixtureCall) {
			c.body = `{"model":"google/model-a","messages":[{"role":"user","content":"hi"}],"stream":true}`
		}), convOpenAI, natOpenAI, true, strictSettles, ""),
		exact("google", "vertex-claude-rawpredict.json", "vertex-rawPredict", "json", callVertexClaude, convGoogleClaude, natAnthropic, true, strictSettles, ""),
		exact("google", "vertex-claude-streamrawpredict.sse", "vertex-streamRawPredict", "sse", callVertexClaudeS, convGoogleClaude, natAnthropic, true, strictSettles, ""),
		exact("google", "vertex-claude-streamrawpredict-interrupted.sse", "vertex-streamRawPredict", "sse", callVertexClaudeS, convGoogleClaude, natAnthropic, false, strictUnknown, ""),
		exact("google", "vertex-interactions.json", "vertex-interactions", "json", callInteraction, convGemini, natGemini, true, strictSettles, ""),
		exact("google", "vertex-interactions.sse", "vertex-interactions", "sse", callInteraction, convGemini, natGemini, true, strictSettles, ""),
		exact("google", "vertex-interactions-interrupted.sse", "vertex-interactions", "sse", callInteraction, convGemini, natGemini, false, strictUnknown, ""),
		exact("google", "vertex-live.ws.jsonl", "vertex-live", "websocket", callLive, convGemini, natGemini, true, strictSettles, ""),
		exact("google", "vertex-live-interrupted.ws.jsonl", "vertex-live", "websocket", callLive, convGemini, natGemini, false, strictUnknown, ""),
		exact("openai", "realtime.ws.jsonl", "realtime", "websocket", callRealtime, convOpenAI, natOpenAI, true, strictSettles, ""),
		exact("openai", "realtime.cancelled.ws.jsonl", "realtime", "websocket", callRealtime, convOpenAI, natOpenAI, false, strictUnknown, ""),
		exact("openai", "realtime.interrupted.ws.jsonl", "realtime", "websocket", callRealtime, convOpenAI, natOpenAI, false, strictUnknown, ""),
		exact("openai", "responses.ws.jsonl", "responses", "websocket", callResponsesWS, convOpenAI, natOpenAI, true, strictSettles, ""),
		exact("openai", "responses.interrupted.ws.jsonl", "responses", "websocket", callResponsesWS, convOpenAI, natOpenAI, false, strictUnknown, ""),
	}
	// Utility operations (ops_utility_routes_test.go) replace the matching
	// unsupported/format-checked entries above; media and batch routes
	// (usage_conformance_extended_test.go) are appended.
	return append(overrideRoutes(routes, utilityRoutes()), extendedUsageRoutes()...)
}

// usageFixtureProviders are the fixture directories under testdata/usage-formats.
var usageFixtureProviders = []string{"openai", "anthropic", "google", "azure-openai", "openai-compatible"}

func withAllow(r fixtureRoute, allow map[string]string) fixtureRoute {
	r.allowUnknown = allow
	return r
}

// ---- running a fixture through the gateway ----

type leaseMode int

const (
	modeLegacy leaseMode = iota // no quota: the legacy Observe hook feeds the ledger
	modeCount                   // quota with a request-count profile (advisory usage)
	modeStrict                  // quota with a token profile
)

type fixtureRun struct {
	code         int
	body         []byte
	observations []execution.Observation
	results      []execution.Result
	aborts       int
}

func (r fixtureRun) settled() bool {
	for _, result := range r.results {
		if result.Terminal && result.Err == nil {
			return true
		}
	}
	return false
}

// accumulate merges observations exactly as the usage ledger does.
func (r fixtureRun) accumulate() map[string]int64 {
	var acc usageledger.Accumulator
	for _, o := range r.observations {
		if o.Source == "upstream" {
			acc.Observe(o.Counters, o.Cumulative)
		}
	}
	return acc.Snapshot()
}

func (r fixtureRun) terminal() (map[string]int64, bool) {
	for i := len(r.observations) - 1; i >= 0; i-- {
		if r.observations[i].Terminal {
			return r.observations[i].Counters, true
		}
	}
	return nil, false
}

func eventStreamFromJSONL(t *testing.T, data []byte, invoke bool) []byte {
	t.Helper()
	var out []byte
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var member map[string]json.RawMessage
		if err := json.Unmarshal(line, &member); err != nil {
			t.Fatalf("fixture line: %v", err)
		}
		exception := ""
		for key := range member {
			if len(member) == 1 && strings.HasSuffix(key, "Exception") {
				exception = key
			}
		}
		switch {
		case exception != "":
			out = append(out, encodeEventMessage([][2]string{{":exception-type", exception}, {":content-type", "application/json"}, {":message-type", "exception"}}, member[exception])...)
		case invoke:
			payload, _ := json.Marshal(map[string]string{"bytes": base64.StdEncoding.EncodeToString(line)})
			out = append(out, encodeEventMessage([][2]string{{":event-type", "chunk"}, {":content-type", "application/json"}, {":message-type", "event"}}, payload)...)
		default:
			if len(member) != 1 {
				t.Fatalf("converse fixture line must have one union member: %s", line)
			}
			for key, value := range member {
				out = append(out, encodeEventMessage([][2]string{{":event-type", key}, {":content-type", "application/json"}, {":message-type", "event"}}, value)...)
			}
		}
	}
	return out
}

type collectingLease struct {
	modelLease
	profile string
}

func (l *collectingLease) MeteringProfile() string { return l.profile }

func runFixtureHTTP(t *testing.T, route fixtureRoute, data []byte, mode leaseMode) fixtureRun {
	t.Helper()
	call := route.call
	payload := data
	media := call.media
	switch {
	case media == "application/vnd.amazon.eventstream":
		payload = eventStreamFromJSONL(t, data, strings.Contains(call.path, "invoke"))
	case media == "" && strings.HasSuffix(route.file, ".sse"):
		media = "text/event-stream"
	case media == "":
		media = "application/json"
	}
	status := call.upstream
	if status == 0 {
		status = http.StatusOK
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", media)
		w.WriteHeader(status)
		_, _ = w.Write(payload)
	}))
	defer upstream.Close()
	s := call.surface(upstream.URL)
	ownership := newOwnership()
	for _, resource := range call.bind {
		if err := ownership.Bind(context.Background(), Owner{"tenant", "alice", "native"}, resource); err != nil {
			t.Fatal(err)
		}
	}
	opts := options(t, upstream.URL, ownership)
	if s.Provider != nil {
		opts.LiveDialer = nil
		opts.TokenSource = nil
		opts.Credential = NoCredential()
	}
	var mu sync.Mutex
	run := fixtureRun{}
	var lease *collectingLease
	switch mode {
	case modeLegacy:
		opts.Observe = func(_ context.Context, _ Owner, value UsageObservation) {
			mu.Lock()
			defer mu.Unlock()
			run.observations = append(run.observations, execution.Observation{Source: value.Source, Counters: value.Counters, Cumulative: value.Cumulative, Terminal: value.Terminal, ModelVersion: value.ModelVersion})
		}
	case modeCount, modeStrict:
		lease = &collectingLease{}
		if mode == modeCount {
			lease.profile = "request-count"
		}
		opts.Execution = ExecutionPreparerFunc(func(context.Context, ExecutionRequest) (execution.Lease, error) { return lease, nil })
	}
	g, err := NewGateway(s, opts)
	if err != nil {
		t.Fatal(err)
	}
	method := call.method
	if method == "" {
		method = "POST"
	}
	r := httptest.NewRequest(method, call.path, strings.NewReader(call.body))
	r.Header.Set("Content-Type", "application/json")
	if call.contentType != "" {
		r.Header.Set("Content-Type", call.contentType)
	}
	w := httptest.NewRecorder()
	g.ServeHTTP(w, r)
	run.code, run.body = w.Code, w.Body.Bytes()
	if lease != nil {
		lease.mu.Lock()
		run.observations = append(run.observations, lease.observations...)
		run.results = append(run.results, lease.results...)
		lease.mu.Unlock()
	}
	return run
}

func loadExpected(t *testing.T, provider string) map[string]expectedUsage {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "usage-formats", provider, "expected.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = validateJSON(data); err != nil {
		t.Fatalf("%s expected.json: %v", provider, err)
	}
	out := map[string]expectedUsage{}
	if err = json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestUsageFormatConformance is the conformance harness. Every fixture file
// must have exactly one route; a fixture without a route fails the test.
func TestUsageFormatConformance(t *testing.T) {
	routes := usageRoutes()
	byFile := map[string]fixtureRoute{}
	for _, route := range routes {
		key := route.provider + "/" + route.file
		if _, dup := byFile[key]; dup {
			t.Fatalf("duplicate route for %s", key)
		}
		byFile[key] = route
	}
	for _, provider := range usageFixtureProviders {
		expected := loadExpected(t, provider)
		entries, err := os.ReadDir(filepath.Join("testdata", "usage-formats", provider))
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if entry.Name() == "expected.json" {
				continue
			}
			key := provider + "/" + entry.Name()
			if _, ok := byFile[key]; !ok {
				t.Errorf("fixture %s has no conformance route (add it, marking it unsupported if no observer serves it)", key)
			}
			if _, ok := expected[entry.Name()]; !ok {
				t.Errorf("fixture %s has no expected.json entry", key)
			}
		}
		for name := range expected {
			if _, err := os.Stat(filepath.Join("testdata", "usage-formats", provider, name)); err != nil {
				t.Errorf("expected.json entry %s/%s has no fixture", provider, name)
			}
		}
	}
	expectations := map[string]map[string]expectedUsage{}
	for _, provider := range usageFixtureProviders {
		expectations[provider] = loadExpected(t, provider)
	}
	for _, route := range routes {
		route := route
		t.Run(route.provider+"/"+route.file, func(t *testing.T) {
			if route.status == statusUnsupported && route.call == nil {
				if route.reason == "" {
					t.Fatal("unsupported fixture needs a reason")
				}
				return
			}
			data, err := os.ReadFile(filepath.Join("testdata", "usage-formats", route.provider, route.file))
			if err != nil {
				t.Fatal(err)
			}
			want := expectedCanonical(route.conv, expectations[route.provider][route.file])
			if route.check != nil {
				route.check(t, data, want)
				return
			}
			if route.call == nil {
				t.Fatal("served fixture needs a call")
			}
			checkFixture(t, route, data, want)
		})
	}
}

func checkFixture(t *testing.T, route fixtureRoute, data []byte, want canonical) {
	t.Helper()
	if route.call.ws != "" {
		checkWebSocketFixture(t, route, data, want)
		return
	}
	// Ledger views: without quota (legacy observer) and with a request-count
	// lease. Both must report the fixture's usage, unknown never as zero.
	for _, mode := range []leaseMode{modeLegacy, modeCount} {
		run := runFixtureHTTP(t, route, data, mode)
		got := observedCanonical(route.native, run.accumulate())
		if diffs := compareCanonical(want, got, route.allowUnknown); len(diffs) > 0 {
			t.Errorf("mode %d ledger usage mismatch: %s\n want %s\n got  %s", mode, strings.Join(diffs, "; "), describe(want), describe(got))
		}
		if mode == modeCount {
			_, terminal := run.terminal()
			if route.final && !terminal && len(want) > 0 && want["output_tokens"] != nil {
				t.Errorf("complete response produced no terminal observation: %+v", run.results)
			}
			if !route.final && run.settled() && hasTokens(want) {
				t.Errorf("incomplete response settled: %+v", run.results)
			}
		}
	}
	if route.strict == strictNotApplicable {
		return
	}
	run := runFixtureHTTP(t, route, data, modeStrict)
	switch route.strict {
	case strictSettles:
		counters, ok := run.terminal()
		if !run.settled() || !ok {
			t.Fatalf("strict profile did not settle a complete response: %+v obs=%+v", run.results, run.observations)
		}
		got := observedCanonical(route.native, counters)
		if route.native == natGemini {
			// Strict Gemini settlement rule: an omitted cachedContentTokenCount
			// settles as 0 (proto3 zero omission; conservative, all input is
			// charged as uncached), and thoughts are derived exactly as
			// total - prompt - candidates. Both may therefore be 0 where the
			// fixture says "not reported".
			for _, f := range []string{"cache_read_tokens", "reasoning_tokens"} {
				if want[f] == nil && got[f] != nil && *got[f] == 0 {
					got[f] = nil
				}
			}
		}
		if route.native == natOpenAI && want["cache_write_tokens"] == nil && got["cache_write_tokens"] != nil && *got["cache_write_tokens"] == 0 {
			// Strict OpenAI-format settlement: an absent cache_write_tokens
			// (documented default 0; models without separately billed writes
			// keep written tokens in ordinary input) settles as 0.
			got["cache_write_tokens"] = nil
		}
		if diffs := compareCanonical(want, got, route.allowUnknown); len(diffs) > 0 {
			t.Errorf("strict settlement mismatch: %s\n want %s\n got  %s", strings.Join(diffs, "; "), describe(want), describe(got))
		}
	case strictRefuses, strictUnknown:
		if run.settled() {
			counters, _ := run.terminal()
			t.Errorf("strict profile settled (%s): %v", route.strictReason, counters)
		}
		if _, ok := run.terminal(); ok {
			t.Errorf("strict profile recorded terminal usage it could not settle")
		}
	}
}

func hasTokens(c canonical) bool {
	for _, v := range c {
		if v != nil {
			return true
		}
	}
	return false
}

// runFixtureWebSocket replays a .ws.jsonl message log from a fake upstream
// through the gateway's WebSocket relay. Interrupted fixtures drop the TCP
// connection without a close frame; complete ones close normally.
func runFixtureWebSocket(t *testing.T, route fixtureRoute, data []byte, mode leaseMode) (fixtureRun, int32) {
	t.Helper()
	var dials atomic.Int32
	interrupted := strings.Contains(route.file, "interrupted")
	upgrader := websocket.Upgrader{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dials.Add(1)
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		if route.call.ws == "live" {
			if _, _, err = conn.ReadMessage(); err != nil {
				return
			}
		}
		scanner := bufio.NewScanner(bytes.NewReader(data))
		scanner.Buffer(make([]byte, 1<<20), 1<<20)
		for scanner.Scan() {
			if line := bytes.TrimSpace(scanner.Bytes()); len(line) > 0 {
				if conn.WriteMessage(websocket.TextMessage, line) != nil {
					return
				}
			}
		}
		if interrupted {
			_ = conn.UnderlyingConn().Close()
			return
		}
		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer upstream.Close()
	s := route.call.surface(upstream.URL)
	opts := options(t, upstream.URL, newOwnership())
	if s.Provider != nil {
		opts.TokenSource = nil
		opts.Credential = NoCredential()
	}
	var mu sync.Mutex
	run := fixtureRun{}
	var lease *collectingLease
	switch mode {
	case modeLegacy:
		opts.Observe = func(_ context.Context, _ Owner, value UsageObservation) {
			mu.Lock()
			defer mu.Unlock()
			run.observations = append(run.observations, execution.Observation{Source: value.Source, Counters: value.Counters, Cumulative: value.Cumulative, Terminal: value.Terminal, Bound: value.Bound})
		}
	default:
		lease = &collectingLease{}
		if mode == modeCount {
			lease.profile = "request-count"
		}
		opts.Execution = ExecutionPreparerFunc(func(context.Context, ExecutionRequest) (execution.Lease, error) { return lease, nil })
	}
	g, err := NewGateway(s, opts)
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan struct{})
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(finished)
		g.ServeHTTP(w, r)
	}))
	defer gateway.Close()
	conn, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(gateway.URL, "http")+route.call.path, nil)
	if err != nil {
		if response != nil {
			run.code = response.StatusCode
		}
		<-finished
	} else {
		run.code = http.StatusSwitchingProtocols
		if route.call.body != "" {
			_ = conn.WriteMessage(websocket.TextMessage, []byte(route.call.body))
		}
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				break
			}
		}
		conn.Close()
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Fatal("relay did not finish")
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if lease != nil {
		lease.mu.Lock()
		run.observations = append(run.observations, lease.observations...)
		run.results = append(run.results, lease.results...)
		run.aborts = lease.aborts
		lease.mu.Unlock()
	}
	return run, dials.Load()
}

func checkWebSocketFixture(t *testing.T, route fixtureRoute, data []byte, want canonical) {
	t.Helper()
	for _, mode := range []leaseMode{modeLegacy, modeCount} {
		run, dials := runFixtureWebSocket(t, route, data, mode)
		if dials != 1 || run.code != http.StatusSwitchingProtocols {
			t.Fatalf("mode %d: session not relayed: code=%d dials=%d", mode, run.code, dials)
		}
		got := observedCanonical(route.native, run.accumulate())
		if diffs := compareCanonical(want, got, route.allowUnknown); len(diffs) > 0 {
			t.Errorf("mode %d session usage mismatch: %s\n want %s\n got  %s", mode, strings.Join(diffs, "; "), describe(want), describe(got))
		}
		_, terminal := run.terminal()
		bound := ""
		for _, o := range run.observations {
			if o.Bound != "" {
				bound = o.Bound
			}
		}
		if route.final && (!terminal || bound != "") {
			t.Errorf("mode %d: complete session not final (bound %q): %+v", mode, bound, run.observations)
		}
		if !route.final && (terminal || bound == "") {
			t.Errorf("mode %d: incomplete session not labelled (terminal=%t bound=%q)", mode, terminal, bound)
		}
		if mode == modeCount {
			// One session = one request: always settled exactly once.
			if len(run.results) != 1 || !run.results[0].ResponseComplete || run.results[0].Terminal != route.final {
				t.Errorf("session settlement: %+v", run.results)
			}
		}
	}
	// On a session-metered surface a token-metered lease is refused before
	// credentials or the dial.
	run, dials := runFixtureWebSocket(t, route, data, modeStrict)
	if dials != 0 || run.aborts != 1 || len(run.results) != 0 {
		t.Errorf("token-metered session admitted: dials=%d aborts=%d results=%+v", dials, run.aborts, run.results)
	}
	// On a token-metered surface every response / allowance window is
	// admitted, settled exactly or retained (session_metering_test.go).
	if route.strict != strictNotApplicable {
		checkTokenSessionFixture(t, route, data, want)
	}
}

// ---- coverage matrix ----

type coverageRow struct {
	provider, surface, transport string
	status                       conformanceStatus
	fixtures                     []string
	reasons                      []string
}

func coverageMatrix(routes []fixtureRoute) []coverageRow {
	rank := map[conformanceStatus]int{statusUnsupported: 0, statusAdvisory: 1, statusExact: 2}
	rows := map[string]*coverageRow{}
	for _, r := range routes {
		key := r.provider + "|" + r.surface + "|" + r.transport
		row := rows[key]
		if row == nil {
			row = &coverageRow{provider: r.provider, surface: r.surface, transport: r.transport, status: r.status}
			rows[key] = row
		}
		if rank[r.status] < rank[row.status] {
			row.status = r.status
		}
		row.fixtures = append(row.fixtures, r.file)
		reason := r.reason
		if r.status == statusExact && r.strict == strictRefuses {
			reason = "observation exact; strict settlement fails closed for this fixture: " + r.strictReason
		}
		if reason != "" {
			seen := false
			for _, existing := range row.reasons {
				seen = seen || existing == reason
			}
			if !seen {
				row.reasons = append(row.reasons, reason)
			}
		}
	}
	out := []coverageRow{}
	for _, row := range rows {
		sort.Strings(row.fixtures)
		out = append(out, *row)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].provider != out[j].provider {
			return out[i].provider < out[j].provider
		}
		if out[i].surface != out[j].surface {
			return out[i].surface < out[j].surface
		}
		return out[i].transport < out[j].transport
	})
	return out
}

func renderCoverage(routes []fixtureRoute) string {
	var b strings.Builder
	b.WriteString("# Usage-format coverage matrix\n\n")
	b.WriteString("<!-- GENERATED by TestUsageCoverageMatrix; do not edit by hand. -->\n\n")
	b.WriteString("Regenerate from the repository root with:\n\n```sh\nAIPLEX_WRITE_USAGE_COVERAGE=1 go test ./pkg/modelapi -run TestUsageCoverageMatrix\n```\n\n")
	b.WriteString("Each row is provider x surface x transport. `exact`: the gateway observer reproduces the fixture's usage and the operation may carry a token profile (strict settlement still fails closed where noted). `advisory`: usage is observed as ledger telemetry only; the operation is request-count metered. `unsupported`: no gateway observer path serves this surface/transport (fixtures marked format-checked are still replayed through the observer of an equivalent served operation).\n\n")
	b.WriteString(coverageRules)
	counts := map[string]map[conformanceStatus]int{}
	rows := coverageMatrix(routes)
	for _, row := range rows {
		if counts[row.provider] == nil {
			counts[row.provider] = map[conformanceStatus]int{}
		}
		counts[row.provider][row.status]++
	}
	b.WriteString("## Summary (rows)\n\n| Provider | exact | advisory | unsupported |\n| --- | --- | --- | --- |\n")
	for _, p := range []string{"anthropic", "azure-openai", "google", "openai", "openai-compatible"} {
		fmt.Fprintf(&b, "| %s | %d | %d | %d |\n", p, counts[p][statusExact], counts[p][statusAdvisory], counts[p][statusUnsupported])
	}
	b.WriteString("\n## Matrix\n\n| Provider | Surface | Transport | Status | Fixtures | Notes |\n| --- | --- | --- | --- | --- | --- |\n")
	formatChecked := map[string]bool{}
	for _, r := range routes {
		if r.formatOnly {
			formatChecked[r.provider+"/"+r.file] = true
		}
	}
	for _, row := range rows {
		names := []string{}
		for _, f := range row.fixtures {
			if formatChecked[row.provider+"/"+f] {
				f += " (format-checked)"
			}
			names = append(names, "`"+f+"`")
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s |\n", row.provider, row.surface, row.transport, row.status, strings.Join(names, "<br>"), strings.ReplaceAll(strings.Join(row.reasons, "<br>"), "|", "\\|"))
	}
	return b.String()
}

const coverageRules = `## How fixtures are compared

The harness (` + "`pkg/modelapi/usage_conformance_test.go`" + `) replays every fixture through the
gateway path that serves it, three ways: without quota (legacy observer), with a
request-count lease, and with a token-profile lease. The first two must reproduce
the fixture's usage exactly as the usage ledger accumulates it; the token-profile
lease must either settle the same values or fail closed, and must never settle an
incomplete exchange. WebSocket fixtures are replayed by a fake upstream socket;
interrupted fixtures drop the TCP connection without a close frame.

The three ` + "`expected.json`" + ` files use different conventions, so expected and observed
values are both converted to one canonical form: ` + "`input_tokens`" + ` = all prompt-side
tokens (fresh + cache read + cache write, tool-use prompt excluded);
` + "`output_tokens`" + ` = all billed generated tokens including reasoning/thoughts;
` + "`reasoning_tokens`" + ` = informational subset of output; ` + "`total_tokens`" + ` = the provider's
total when reported, else input + output. Per-directory rules:

| Fixture convention | Expected -> canonical |
| --- | --- |
| ` + "`openai/`" + ` | verbatim (input includes cache read and write; output includes reasoning) |
| ` + "`anthropic/`" + ` (incl. Bedrock, Vertex rawPredict) | input = input + cache_read + cache_write (input is exclusive of cache) |
| ` + "`google/`" + ` Gemini, Interactions, Live | output = output + reasoning (thoughts are not inside candidates) |
| ` + "`google/vertex-claude-*`" + ` | input verbatim (already summed); null total derived |
| ` + "`google/vertex-openai-chat*`" + ` | output = completion + reasoning (completion excludes reasoning) |
| ` + "`google/*count-tokens*`" + ` | not consumption: no usage may be observed |

Strict Gemini settlement treats an omitted ` + "`cachedContentTokenCount`" + ` as 0: the field is a
plain proto3 ` + "`int32`" + ` without presence in both the Vertex and Developer API protos, and
the proto3 JSON mapping omits default values. Thoughts are derived as
total - prompt - candidates. Cache writes are a separate meter (scalar or per-TTL);
OpenAI-format strict settlement treats an absent ` + "`cache_write_tokens`" + ` as 0 (documented
default; models without separately billed writes keep them in ordinary input).
Strict lease rows also cover Azure OpenAI (` + "`azure-openai/`" + `) and generic compatible
servers that declare ` + "`usage_semantics: \"openai\"`" + ` (` + "`openai-compatible/`" + `), both in the
OpenAI convention. Non-token units the ledger holds (audio/voice/video milliseconds,
generated images, per-modality token splits) are compared by the media and batch
checks; batch results files are aggregated with the gateway's own batch parser
and must settle only when complete.

WebSocket sessions run two ways. On a session-metered surface (default) one
request-count admission and one settlement per session; usage without final
evidence is labelled ` + "`lower_bound`" + `, ` + "`ambiguous`" + ` (resumed Live session) or ` + "`partial`" + `
(units the ledger has no field for, such as Realtime input-transcription meters
billed to another model). On a token-metered surface
(` + "`websocket.metering: \"token\"`" + `) the harness replays the fixture with one lease per
admitted Realtime/Responses response or Live allowance window: complete sessions
settle every lease and the settled leases add up to the fixture's usage; an
interrupted session leaves its open lease unsettled.

`

// TestUsageCoverageMatrix keeps docs/reference/usage-formats/coverage.md in
// sync with the harness so a coverage regression is a visible diff.
func TestUsageCoverageMatrix(t *testing.T) {
	path := filepath.Join("..", "..", "docs", "reference", "usage-formats", "coverage.md")
	generated := renderCoverage(usageRoutes())
	if os.Getenv("AIPLEX_WRITE_USAGE_COVERAGE") == "1" {
		if err := os.WriteFile(path, []byte(generated), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	current, err := os.ReadFile(path)
	if err != nil || string(current) != generated {
		t.Fatalf("%s is stale; regenerate with AIPLEX_WRITE_USAGE_COVERAGE=1 go test ./pkg/modelapi -run TestUsageCoverageMatrix", path)
	}
}

var _ = io.EOF
