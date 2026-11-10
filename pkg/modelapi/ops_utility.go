package modelapi

// Text and utility operations of the provider surfaces: the Gemini Developer
// API, OpenAI embeddings / moderations / Responses lifecycle, Anthropic token
// counting. Every operation here is JSON or SSE; none is media or batch.
//
// Metering contract (see Coverage):
//
//   - All of these operations are request-count metered. None installs a token
//     profile: the token profiles (token_profiles.go, quota_host_profiles.go)
//     require the complete uncached/cached/output counter set, which embeddings
//     (input only) and the Gemini Developer API (zero-omitting proto3 JSON, no
//     profile admission for its provider id) do not satisfy without redesign.
//   - Usage the provider reports on a usage-bearing exchange (generate, stream,
//     interactions create, embeddings) is read by the existing observers and
//     recorded as ledger telemetry.
//   - Token-count endpoints, moderations, retrieval, cancel, delete and list
//     responses are never read for usage: they are estimates, free, or a
//     second view of an exchange that was already metered. Reading usage from a
//     retrieved background response would settle the same tokens twice, so
//     these operations are structurally incapable of recording any.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/vamsiramakrishnan/aiplex/pkg/contracts"
)

// Gemini Developer API provider and the client protocols added with it.
const (
	ProviderGeminiDeveloper = "gemini-developer"
	// ProtocolGoogleGenAI equals clientplex.GenAI: Google GenAI SDKs speak it to
	// both Vertex and the Developer API.
	ProtocolGoogleGenAI       = "google-genai"
	ProtocolOpenAIEmbeddings  = "openai-embeddings"
	ProtocolOpenAIModerations = "openai-moderations"

	FormatGeminiDeveloper      = "gemini-developer"
	FormatOpenAIEmbeddings     = "openai-embeddings"
	FormatOpenAIModerations    = "openai-moderations"
	FormatAnthropicCountTokens = "anthropic-count-tokens"

	// DefaultModerationModel is the model the Moderations API applies when the
	// request omits one. The gateway authorizes it explicitly without
	// rewriting the body.
	DefaultModerationModel = "omni-moderation-latest"
)

type utilityKind int

const (
	// utilStandard operations flow through the existing observers.
	utilStandard utilityKind = iota
	// utilEmbedding operations report input tokens only.
	utilEmbedding
	// utilNoUsage responses are never read for usage.
	utilNoUsage
)

type utilitySpec struct {
	kind       utilityKind
	cumulative bool
	// stateless operations bind and reference no resource.
	stateless bool
	// query is the allowlist of caller query parameters forwarded upstream,
	// each with a value validator. Everything else (notably key=) is dropped.
	query map[string]func(string) bool
	// noBody operations (GET/DELETE/cancel) accept an empty body only.
	noBody bool
	// asynchronous marks resource operations (request-count of the call, not of
	// the job or retained resource).
	asynchronous bool
}

type utilityOp struct {
	providerOp
	provider string
	spec     utilitySpec
}

var utilityIndex = func() map[string]utilityOp {
	out := map[string]utilityOp{}
	for _, list := range [][]utilityOp{geminiDeveloperOps(), openAIUtilityOps(), anthropicUtilityOps()} {
		for _, o := range list {
			out[o.op.ID] = o
		}
	}
	return out
}()

// utilityProviderCatalog returns the new operations per provider, in a stable order.
func utilityProviderCatalog() map[string][]providerOp {
	out := map[string][]providerOp{}
	for _, list := range [][]utilityOp{geminiDeveloperOps(), openAIUtilityOps(), anthropicUtilityOps()} {
		for _, o := range list {
			out[o.provider] = append(out[o.provider], o.providerOp)
		}
	}
	return out
}

func utilityNoUsage(op Operation) bool { return utilityIndex[op.ID].spec.kind == utilNoUsage }
func utilityCumulative(op Operation) bool {
	return utilityIndex[op.ID].spec.cumulative
}

// utilityCoverage is the Coverage of a utility operation: request-count only.
func utilityCoverage(op Operation) ExecutionCoverage {
	o, ok := utilityIndex[op.ID]
	if !ok {
		return ExecutionCoverage{}
	}
	return ExecutionCoverage{RequestCount: true, Asynchronous: o.spec.asynchronous}
}

// inspectStateless handles the utility operations that bind no resource.
func inspectStateless(surfaceID string, op Operation, data []byte, includeUsage bool) (jsonObservation, bool, error) {
	o, ok := utilityIndex[op.ID]
	if !ok || !o.spec.stateless {
		return jsonObservation{}, false, nil
	}
	if o.spec.kind != utilEmbedding || !includeUsage {
		return jsonObservation{}, true, nil
	}
	var root map[string]json.RawMessage
	if json.Unmarshal(data, &root) != nil {
		return jsonObservation{}, true, nil
	}
	// OpenAI: usage{prompt_tokens,total_tokens}. Gemini: usageMetadata
	// (EmbeddingUsageMetadata){promptTokenCount}. Input tokens only; no output
	// or total counter is invented where the provider reports none.
	var raw json.RawMessage
	fields := map[string]string{}
	if raw = root["usage"]; raw != nil {
		fields["prompt_tokens"], fields["total_tokens"] = "input_tokens", "total_tokens"
	} else if raw = root["usageMetadata"]; raw != nil {
		fields["promptTokenCount"] = "input_tokens"
	}
	if raw == nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return jsonObservation{}, true, nil
	}
	var usage map[string]json.RawMessage
	if json.Unmarshal(raw, &usage) != nil {
		return jsonObservation{}, true, fmt.Errorf("invalid provider usage object")
	}
	counters := map[string]int64{}
	for native, meter := range fields {
		value := usage[native]
		if value == nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			continue
		}
		var n int64
		if json.Unmarshal(value, &n) != nil || n < 0 || n > 1<<53 {
			return jsonObservation{}, true, fmt.Errorf("invalid provider usage counter")
		}
		counters[meter] = n
	}
	if len(counters) == 0 {
		return jsonObservation{}, true, nil
	}
	return jsonObservation{nativeTerminal: true, usage: &UsageObservation{Source: "upstream", SurfaceID: surfaceID, OperationID: op.ID, Terminal: true, Counters: counters, Cumulative: true}}, true, nil
}

// providerResourceID normalizes a provider resource name to its bare ID for
// the ownership store. Developer API names are collection-prefixed
// (cachedContents/{id}); OpenAI IDs are already bare.
func (g *Gateway) providerResourceID(kind, name string) (string, error) {
	prefix := map[string]string{"cache": "cachedContents/", "interaction": "interactions/"}[kind]
	if prefix == "" {
		return "", fmt.Errorf("upstream resource outside configured scope")
	}
	id := strings.TrimPrefix(name, prefix)
	if !validSegment(id) {
		return "", fmt.Errorf("invalid upstream resource ID")
	}
	return id, nil
}

// preflightUtility enforces, before any body is read or credential acquired:
// shared-listing authorization and resource ownership for operations that
// address one resource by ID (response retrieve/cancel/delete, cached content
// get/update/delete, interaction get/cancel/delete).
func (g *Gateway) preflightUtility(ctx context.Context, trusted contracts.RequestContext, owner Owner, op Operation, params map[string]string, md *Metadata) error {
	if _, ok := utilityIndex[op.ID]; !ok {
		return nil
	}
	if op.Action == List && (g.options.AllowShared == nil || !g.options.AllowShared(ctx, trusted, g.Surface(), op)) {
		return forbidden("shared listing requires explicit authorization")
	}
	id := params["id"]
	if op.Stateful && id != "" {
		if !validSegment(id) {
			return badRequest("invalid resource identifier")
		}
		md.Resource = Resource{op.ResourceKind, id}
		if g.options.Ownership == nil || g.options.Ownership.Check(ctx, owner, md.Resource) != nil {
			return forbidden("resource ownership denied")
		}
	}
	return nil
}

var (
	digitsQuery = regexp.MustCompile(`^[0-9]{1,9}$`)
	tokenQuery  = regexp.MustCompile(`^[A-Za-z0-9_=+/.:-]{1,1000}$`)
	maskQuery   = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.]*(,[A-Za-z][A-Za-z0-9_.]*){0,15}$`)
	boolQuery   = regexp.MustCompile(`^(true|false)$`)
	enumQuery   = regexp.MustCompile(`^[A-Za-z0-9_.]{1,64}$`)
)

func queryEquals(want string) func(string) bool { return func(v string) bool { return v == want } }
func queryMatches(p *regexp.Regexp) func(string) bool {
	return func(v string) bool { return p.MatchString(v) }
}

// utilityQuery returns the caller query parameters allowed to cross for this
// operation, re-encoded. Caller credentials (key=, access_token=) and every
// undeclared parameter are dropped.
func utilityQuery(op Operation, in url.Values) string {
	o, ok := utilityIndex[op.ID]
	if !ok || len(o.spec.query) == 0 {
		return ""
	}
	out := url.Values{}
	for name, valid := range o.spec.query {
		for _, v := range in[name] {
			if valid(v) {
				out.Add(name, v)
			}
		}
	}
	return out.Encode()
}

// utilityRequestHeaders applies per-provider forwarded protocol headers.
func utilityRequestHeaders(provider string, in, out map[string][]string) error {
	if provider != ProviderGeminiDeveloper {
		return nil
	}
	// Interactions API revision pin (Api-Revision: YYYY-MM-DD), documented in
	// the Interactions quickstart. A malformed value is refused, not forwarded.
	for key, values := range in {
		if !strings.EqualFold(key, "Api-Revision") {
			continue
		}
		if len(values) != 1 || !regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`).MatchString(values[0]) {
			return badRequest("invalid Api-Revision")
		}
		out["Api-Revision"] = []string{values[0]}
	}
	return nil
}

// utilityBody validates the request body of a utility operation. handled is
// false when the generic provider validation applies (embeddings, count_tokens).
func (g *Gateway) utilityBody(ctx context.Context, owner Owner, op Operation, md *Metadata, body []byte) ([]byte, bool, error) {
	o, ok := utilityIndex[op.ID]
	if !ok {
		return nil, false, nil
	}
	if o.spec.noBody {
		trimmed := bytes.TrimSpace(body)
		if len(trimmed) != 0 && !bytes.Equal(trimmed, []byte("{}")) {
			return nil, true, badRequest("this operation takes no request body")
		}
		return body, true, nil
	}
	switch {
	case o.provider == ProviderGeminiDeveloper:
		out, err := g.validateGeminiDeveloperBody(ctx, owner, op, md, body)
		return out, true, err
	case op.ID == "openai.v1.moderations":
		out, err := g.validateModerationBody(md, body)
		return out, true, err
	}
	return nil, false, nil
}

// utilityObject decodes a request body into a JSON object, refusing
// ambiguous spellings of the given security fields.
func utilityObject(body []byte, reserved ...string) (map[string]json.RawMessage, error) {
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, badRequest("a native JSON request body is required")
	}
	if validateJSON(body) != nil {
		return nil, badRequest("invalid native JSON")
	}
	var doc map[string]json.RawMessage
	if json.Unmarshal(body, &doc) != nil || doc == nil {
		return nil, badRequest("a native JSON object is required")
	}
	for key := range doc {
		for _, name := range reserved {
			if strings.EqualFold(key, name) && key != name {
				return nil, forbidden("ambiguous security field spelling")
			}
		}
	}
	return doc, nil
}

func (g *Gateway) validateModerationBody(md *Metadata, body []byte) ([]byte, error) {
	doc, err := utilityObject(body, "model")
	if err != nil {
		return nil, err
	}
	model := DefaultModerationModel
	if raw := doc["model"]; raw != nil && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		if json.Unmarshal(raw, &model) != nil {
			return nil, badRequest("model must be a string")
		}
	}
	if !g.allowedModel(model) {
		return nil, forbidden("body model is not allowed")
	}
	md.Model = model
	return body, nil
}

// OperationFor matches a method and native path against the surface's
// installed operations, returning the operation and its {id}/{model} parameters.
func (s Surface) OperationFor(method, path string) (Operation, map[string]string, bool) {
	g := &Gateway{surface: s}
	for _, o := range s.Operations {
		c, err := compileOperation(s, o)
		if err != nil {
			return Operation{}, nil, false
		}
		g.operations = append(g.operations, c)
	}
	return g.match(method, path)
}
