package modelapi

// Usage of Bedrock InvokeModel bodies other than Anthropic Messages.
//
// InvokeModel carries each model family's own body. A family is selected only
// by an exact rule on the model id in the request path (never by the body),
// and its usage is read only from the fields AWS documents for it. Read from
// AWS's public model-parameter reference on 2026-11-14:
//
//   - Amazon Nova (understanding models, version 1: Micro, Lite, Pro), ids
//     amazon.nova-micro-v1…, amazon.nova-lite-v1…, amazon.nova-pro-v1… (model
//     cards model-card-amazon-nova-{micro,lite,pro}.html). Response
//     (https://docs.aws.amazon.com/nova/latest/userguide/complete-request-schema-response.html,
//     "non-streaming Converse and InvokeModel"): "stopReason": string,
//     "usage": {"inputTokens": int, "outputTokens": int, "totalTokens": int}.
//     The streaming chunk shape is not documented there, so Nova streams are
//     not observed. Nova Premier and Nova 2 are not covered (no documented
//     InvokeModel response read for them).
//   - Amazon Titan Text (Express, Lite, Premier), ids amazon.titan-text-express-v1,
//     amazon.titan-text-lite-v1, amazon.titan-text-premier-v1:0
//     (https://docs.aws.amazon.com/bedrock/latest/userguide/model-parameters-titan-text.html):
//     InvokeModel response "inputTextTokenCount": int and "results", "an array
//     of one item" with "tokenCount": int and "completionReason". Stream chunks
//     carry inputTextTokenCount and totalOutputTextTokenCount, but the page does
//     not say which chunk is final or whether the count is cumulative, so Titan
//     streams are not observed.
//   - Meta Llama 3 Instruct (3, 3.1, 3.2, 3.3), ids meta.llama3-…
//     (https://docs.aws.amazon.com/bedrock/latest/userguide/model-parameters-meta.html):
//     "The Llama 3 Instruct models return" "prompt_token_count": int,
//     "generation_token_count": int, "stop_reason": string. The stream chunk is
//     not documented, so Llama streams are not observed. Llama 4 is listed on
//     that page but the response section names Llama 3 only: not covered.
//
// Skipped families (Nova Premier, Nova 2, Llama 4, Llama 2, Cohere, Mistral,
// AI21, DeepSeek, Writer and the others): no InvokeModel response shape for
// them was read from AWS's documentation, so their usage is unknown.
//
// Each family's report is read only when its documented end marker
// (stopReason, completionReason, stop_reason) is a non-empty string: the
// documented complete response always carries it, and a body without it does
// not match the documented shape, so its usage is unknown.
//
// The model id rule (bedrockInvokeRouteFor). AWS's InvokeModel API reference
// (https://docs.aws.amazon.com/bedrock/latest/APIReference/API_runtime_InvokeModel.html,
// read 2026-11-14) gives the modelId forms: a base model id
// [a-z0-9-]{1,63}.[a-z0-9-]{1,63}... (the vendor, a dot, the model), its
// foundation-model ARN, an inference-profile ARN, and ARNs that name no model
// (provisioned-model, custom-model, custom-model-deployment, imported-model,
// application-inference-profile). A cross-Region inference profile id is a
// base model id behind a geography prefix, removed before the rule applies;
// the accepted prefixes and where AWS shows each (read 2026-11-14):
//
//   - us, eu, apac, in: geographic-cross-region-inference.html ("geographic
//     prefixes such as us, eu, apac, in");
//   - ca: model-card-amazon-nova-lite.html (ca.amazon.nova-lite-v1:0);
//   - jp: model-card-amazon-nova-2-lite.html (jp.amazon.nova-2-lite-v1:0);
//   - global: global-cross-region-inference.html (inference-profile/global.<MODEL NAME>)
//     and model-card-amazon-nova-2-lite.html (global.amazon.nova-2-lite-v1:0).
//
// An id that names a vendor other than anthropic is read only by the family
// parser that claims it, otherwise its usage is unknown: it never reaches the
// Anthropic Messages parser. An id that names no vendor (an ARN without an
// embedded model id, or a name without a dot) keeps the earlier behaviour: a
// Messages-shaped body is read as Messages. An ARN that embeds a model id
// (foundation-model/…, inference-profile/…) follows the rule for that id.
//
// None of these is a token profile: the usage is advisory ledger telemetry
// for request-count leases and unmetered surfaces, and is never fed to a
// token-metered lease (a strict profile keeps refusing these bodies).
// InvokeModelWithResponseStream (AWS's API reference) documents no
// invocation-metrics trailer, so none is read.

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
)

type bedrockInvokeFamily struct {
	name string
	// prefixes are the documented model id prefixes; a prefix matches when
	// the id equals it or continues with ':' (a version suffix), or, for an
	// open prefix ending in '-', any continuation.
	prefixes []string
	parse    func(root map[string]json.RawMessage) (counters map[string]int64, terminal bool, ok bool)
}

var bedrockInvokeFamilies = []bedrockInvokeFamily{
	{name: "amazon-nova", prefixes: []string{"amazon.nova-micro-v1", "amazon.nova-lite-v1", "amazon.nova-pro-v1"}, parse: parseNovaInvoke},
	{name: "amazon-titan-text", prefixes: []string{"amazon.titan-text-express-v1", "amazon.titan-text-lite-v1", "amazon.titan-text-premier-v1"}, parse: parseTitanTextInvoke},
	{name: "meta-llama3", prefixes: []string{"meta.llama3-"}, parse: parseLlamaInvoke},
}

// bedrockGeographyPrefixes are the cross-Region inference profile prefixes
// AWS shows (see the file comment for the page of each).
var bedrockGeographyPrefixes = []string{"us.", "eu.", "apac.", "in.", "ca.", "jp.", "global."}

// invokeRoute is how an InvokeModel body may be read.
type invokeRoute struct {
	// family claims the id: only its parser reads the body.
	family *bedrockInvokeFamily
	// anthropic: the id names Anthropic or no vendor, so a Messages-shaped
	// body is read as Messages (the earlier behaviour).
	anthropic bool
}

// bedrockInvokeRouteFor classifies an InvokeModel model id.
func bedrockInvokeRouteFor(model string) invokeRoute {
	if strings.HasPrefix(model, "arn:") {
		parts := strings.SplitN(model, ":", 6)
		if len(parts) != 6 {
			return invokeRoute{anthropic: true}
		}
		resource := parts[5]
		embedded := ""
		for _, kind := range []string{"foundation-model/", "inference-profile/"} {
			if strings.HasPrefix(resource, kind) {
				embedded = strings.TrimPrefix(resource, kind)
			}
		}
		if embedded == "" || !strings.Contains(embedded, ".") {
			// The ARN names no model: no vendor.
			return invokeRoute{anthropic: true}
		}
		model = embedded
	}
	if !strings.Contains(model, ".") {
		// Not the vendor.model form: no vendor named.
		return invokeRoute{anthropic: true}
	}
	for _, geo := range bedrockGeographyPrefixes {
		if strings.HasPrefix(model, geo) && strings.Contains(strings.TrimPrefix(model, geo), ".") {
			model = strings.TrimPrefix(model, geo)
			break
		}
	}
	if family := bedrockInvokeFamilyFor(model); family != nil {
		return invokeRoute{family: family}
	}
	vendor := model[:strings.Index(model, ".")]
	return invokeRoute{anthropic: vendor == "anthropic"}
}

// bedrockInvokeFamilyFor selects the family of a base model id (no ARN, no
// geography prefix), or nil.
func bedrockInvokeFamilyFor(model string) *bedrockInvokeFamily {
	if model == "" {
		return nil
	}
	for i := range bedrockInvokeFamilies {
		for _, prefix := range bedrockInvokeFamilies[i].prefixes {
			if strings.HasSuffix(prefix, "-") {
				if strings.HasPrefix(model, prefix) && len(model) > len(prefix) {
					return &bedrockInvokeFamilies[i]
				}
				continue
			}
			if model == prefix || strings.HasPrefix(model, prefix+":") {
				return &bedrockInvokeFamilies[i]
			}
		}
	}
	return nil
}

type invokeModelKey struct{}

// withInvokeModel records the InvokeModel path model for the observers.
func withInvokeModel(ctx context.Context, model string) context.Context {
	return context.WithValue(ctx, invokeModelKey{}, model)
}

// invokeRouteOf classifies the request's InvokeModel model. Without a
// recorded model (not an InvokeModel request) the earlier behaviour applies.
func invokeRouteOf(ctx context.Context) invokeRoute {
	model, ok := ctx.Value(invokeModelKey{}).(string)
	if !ok {
		return invokeRoute{anthropic: true}
	}
	return bedrockInvokeRouteFor(model)
}

// documentedCount reads one documented token count: a JSON integer from 0 to
// 2^53. Anything else (absent, null, negative, fractional, a string) is
// unknown.
func documentedCount(raw json.RawMessage) (int64, bool) {
	text := strings.TrimSpace(string(raw))
	if text == "" || text == "null" || strings.ContainsAny(text, ".eE\"") {
		return 0, false
	}
	n, err := strconv.ParseInt(text, 10, 64)
	if err != nil || n < 0 || n > 1<<53 {
		return 0, false
	}
	return n, true
}

// onlyKeys reports whether an object has no members outside the documented set.
func onlyKeys(object map[string]json.RawMessage, keys ...string) bool {
	for key := range object {
		known := false
		for _, k := range keys {
			known = known || key == k
		}
		if !known {
			return false
		}
	}
	return true
}

func terminalString(raw json.RawMessage) bool {
	var s string
	return raw != nil && json.Unmarshal(raw, &s) == nil && s != ""
}

// parseNovaInvoke: {"output":{…},"stopReason":…,"usage":{"inputTokens","outputTokens","totalTokens"}}.
func parseNovaInvoke(root map[string]json.RawMessage) (map[string]int64, bool, bool) {
	var usage map[string]json.RawMessage
	if root["output"] == nil || !terminalString(root["stopReason"]) || json.Unmarshal(root["usage"], &usage) != nil || usage == nil || !onlyKeys(usage, "inputTokens", "outputTokens", "totalTokens") {
		return nil, false, false
	}
	counters := readCounts(usage, map[string]string{"inputTokens": "input_tokens", "outputTokens": "output_tokens", "totalTokens": "total_tokens"})
	input, hasInput := counters["input_tokens"]
	output, hasOutput := counters["output_tokens"]
	if total, hasTotal := counters["total_tokens"]; hasInput && hasOutput && hasTotal && total != input+output {
		// The response contradicts itself: none of the three is trusted.
		return nil, false, true
	}
	return counters, true, true
}

// parseTitanTextInvoke: {"inputTextTokenCount","results":[{"tokenCount","outputText","completionReason"}]}.
func parseTitanTextInvoke(root map[string]json.RawMessage) (map[string]int64, bool, bool) {
	var results []map[string]json.RawMessage
	if root["inputTextTokenCount"] == nil || json.Unmarshal(root["results"], &results) != nil || len(results) != 1 || results[0] == nil || !terminalString(results[0]["completionReason"]) {
		return nil, false, false
	}
	input := readCounts(root, map[string]string{"inputTextTokenCount": "input_tokens"})
	output := readCounts(results[0], map[string]string{"tokenCount": "output_tokens"})
	if input == nil || output == nil {
		return nil, false, true
	}
	for key, value := range output {
		input[key] = value
	}
	return input, true, true
}

// parseLlamaInvoke: {"generation","prompt_token_count","generation_token_count","stop_reason"}.
func parseLlamaInvoke(root map[string]json.RawMessage) (map[string]int64, bool, bool) {
	if root["generation"] == nil || !terminalString(root["stop_reason"]) || (root["prompt_token_count"] == nil && root["generation_token_count"] == nil) {
		return nil, false, false
	}
	return readCounts(root, map[string]string{"prompt_token_count": "input_tokens", "generation_token_count": "output_tokens"}), true, true
}

// readCounts maps the documented counts that are present and valid; any
// present but invalid count makes the whole report unknown (nil map entries
// are never invented as zero).
func readCounts(object map[string]json.RawMessage, fields map[string]string) map[string]int64 {
	counters := map[string]int64{}
	for native, meter := range fields {
		raw, present := object[native]
		if !present {
			continue
		}
		n, ok := documentedCount(raw)
		if !ok {
			return nil
		}
		counters[meter] = n
	}
	return counters
}

// inspectInvokeFamily observes a non-streaming InvokeModel body of a
// documented family. The observation is advisory (never a token-profile
// settlement); an undocumented shape or an invalid count is unknown.
func (g *Gateway) inspectInvokeFamily(family *bedrockInvokeFamily, op Operation, data []byte) jsonObservation {
	var result jsonObservation
	var root map[string]json.RawMessage
	if json.Unmarshal(data, &root) != nil || root == nil {
		return result
	}
	counters, terminal, ok := family.parse(root)
	if !ok || len(counters) == 0 {
		return result
	}
	result.nativeTerminal = terminal
	result.advisory = true
	result.usage = &UsageObservation{Source: "upstream", SurfaceID: g.surface.ID, OperationID: op.ID, Terminal: terminal, Counters: counters, Cumulative: true}
	return result
}
