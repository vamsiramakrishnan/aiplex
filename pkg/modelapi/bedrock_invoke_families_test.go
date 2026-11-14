package modelapi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// InvokeModel bodies of documented non-Anthropic families.

const (
	novaModel  = "amazon.nova-lite-v1:0"
	titanModel = "amazon.titan-text-express-v1"
	llamaModel = "meta.llama3-3-70b-instruct-v1:0"
)

var familyRequests = map[string]string{
	novaModel:  `{"schemaVersion":"messages-v1","messages":[{"role":"user","content":[{"text":"hi"}]}],"inferenceConfig":{"maxTokens":64}}`,
	titanModel: `{"inputText":"hi","textGenerationConfig":{"maxTokenCount":64}}`,
	llamaModel: `{"prompt":"hi","max_gen_len":64}`,
}

func invokeCall(model, request string, stream bool) *fixtureCall {
	op, suffix, media := "bedrock.invoke", "invoke", ""
	if stream {
		op, suffix, media = "bedrock.invoke-stream", "invoke-with-response-stream", "application/vnd.amazon.eventstream"
	}
	return &fixtureCall{surface: providerSurfaceFor(Provider{ID: ProviderBedrock, Region: "us-east-1"}, ProtocolBedrockInvoke, []string{model}, op), path: "/model/" + model + "/" + suffix, body: request, media: media}
}

// bedrockFamilyRoutes are the coverage rows of the documented families.
func bedrockFamilyRoutes() []fixtureRoute {
	const advisoryNote = "advisory: usage read from the fields AWS documents for this family (fixture built from AWS's documented response shape with synthetic values, not recorded live); request-count only, no token profile, so a strict lease refuses it"
	family := func(file, surface, model string) fixtureRoute {
		return fixtureRoute{provider: "anthropic", file: file, surface: surface, transport: "json", status: statusAdvisory, reason: advisoryNote, call: invokeCall(model, familyRequests[model], false), conv: convOpenAI, native: natOpenAI, final: true, strict: strictRefuses, strictReason: "no token profile for this InvokeModel family"}
	}
	return []fixtureRoute{
		family("bedrock-invoke-nova.json", "bedrock-invoke-nova", novaModel),
		family("bedrock-invoke-titan-text.json", "bedrock-invoke-titan-text", titanModel),
		family("bedrock-invoke-llama.json", "bedrock-invoke-llama", llamaModel),
		{provider: "anthropic", file: "bedrock-invoke-stream-titan-text.jsonl", surface: "bedrock-invoke-stream-titan-text", transport: "aws-eventstream", status: statusUnsupported, reason: "not observed: AWS documents the Titan Text stream chunk fields but not which chunk is final or whether totalOutputTextTokenCount is cumulative (fixture built from the documented chunk shape with synthetic values, not recorded live); usage unknown, a strict lease refuses it", call: invokeCall(titanModel, familyRequests[titanModel], true), conv: convOpenAI, native: natOpenAI, final: false, strict: strictUnknown},
	}
}

func TestBedrockInvokeFamilyRule(t *testing.T) {
	for model, want := range map[string]string{
		"amazon.nova-lite-v1:0":                  "amazon-nova",
		"amazon.nova-micro-v1:0":                 "amazon-nova",
		"amazon.nova-pro-v1:0":                   "amazon-nova",
		"us.amazon.nova-pro-v1:0":                "amazon-nova",
		"apac.amazon.nova-pro-v1:0":              "amazon-nova",
		"global.amazon.nova-lite-v1:0":           "amazon-nova",
		"amazon.titan-text-express-v1":           "amazon-titan-text",
		"amazon.titan-text-lite-v1":              "amazon-titan-text",
		"amazon.titan-text-premier-v1:0":         "amazon-titan-text",
		"meta.llama3-8b-instruct-v1:0":           "meta-llama3",
		"meta.llama3-3-70b-instruct-v1:0":        "meta-llama3",
		"us.meta.llama3-1-8b-instruct-v1:0":      "meta-llama3",
		"amazon.nova-2-lite-v1:0":                "",
		"amazon.nova-premier-v1:0":               "",
		"amazon.nova-canvas-v1:0":                "",
		"amazon.nova-lite-v10":                   "",
		"amazon.titan-embed-text-v2:0":           "",
		"amazon.titan-text-express-v1x":          "",
		"meta.llama4-maverick-17b-instruct-v1:0": "",
		"meta.llama2-13b-chat-v1":                "",
		"meta.llama3-":                           "",
		"cohere.command-r-v1:0":                  "",
		"anthropic.claude-fixture-v1:0":          "",
		"xx.amazon.nova-lite-v1:0":               "",
		"us.us.amazon.nova-lite-v1:0":            "",
		"AMAZON.NOVA-LITE-V1:0":                  "",
		"arn:aws:bedrock:us-east-1:123456789012:provisioned-model/abcdefghijkl":                      "",
		"arn:aws:bedrock:us-east-1::foundation-model/amazon.nova-lite-v1:0":                          "amazon-nova",
		"arn:aws:bedrock:us-east-1:123456789012:inference-profile/us.meta.llama3-1-8b-instruct-v1:0": "meta-llama3",
		"in.amazon.nova-lite-v1:0":  "amazon-nova",
		"ca.amazon.nova-lite-v1:0":  "amazon-nova",
		"jp.amazon.nova-micro-v1:0": "amazon-nova",
		"au.amazon.nova-lite-v1:0":  "",
		"arn:aws:bedrock:us-east-1:123456789012:application-inference-profile/abcdefghijk": "",
		"": "",
	} {
		got := ""
		if f := bedrockInvokeRouteFor(model).family; f != nil {
			got = f.name
		}
		if got != want {
			t.Errorf("%q: family %q, want %q", model, got, want)
		}
	}
}

func readAnthropicFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "usage-formats", "anthropic", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// Every family's body under every other family's model id, an Anthropic id
// and an unknown id is unknown usage: no parser falls back to another, and a
// documented family's id never reaches the Anthropic parser even with an
// Anthropic Messages body.
func TestBedrockInvokeNoCrossFamilyParsing(t *testing.T) {
	bodies := map[string][]byte{
		novaModel:  readAnthropicFixture(t, "bedrock-invoke-nova.json"),
		titanModel: readAnthropicFixture(t, "bedrock-invoke-titan-text.json"),
		llamaModel: readAnthropicFixture(t, "bedrock-invoke-llama.json"),
	}
	messages := readAnthropicFixture(t, "bedrock-invoke.json")
	models := []string{novaModel, titanModel, llamaModel, "cohere.command-r-v1:0", "mistral.mistral-large-2407-v1:0"}
	for _, model := range models {
		for owner, body := range bodies {
			if owner == model {
				continue
			}
			route := fixtureRoute{provider: "anthropic", file: "cross.json", call: invokeCall(model, familyRequests[owner], false)}
			for _, mode := range []leaseMode{modeLegacy, modeCount, modeStrict} {
				run := runFixtureHTTP(t, route, body, mode)
				if run.code != 200 || len(run.accumulate()) != 0 || run.settled() {
					t.Fatalf("%s body under %s (mode %d): code %d usage %v settled %t", owner, model, mode, run.code, run.accumulate(), run.settled())
				}
			}
		}
	}
	for _, model := range []string{novaModel, titanModel, llamaModel} {
		route := fixtureRoute{provider: "anthropic", file: "messages.json", call: invokeCall(model, `{"anthropic_version":"bedrock-2023-05-31","max_tokens":8,"messages":[]}`, false)}
		for _, mode := range []leaseMode{modeLegacy, modeCount, modeStrict} {
			if run := runFixtureHTTP(t, route, messages, mode); len(run.accumulate()) != 0 || run.settled() {
				t.Fatalf("Messages body under %s reached the Anthropic parser (mode %d): %v", model, mode, run.accumulate())
			}
		}
	}
}

// Missing, null, negative, fractional or non-numeric counts are unknown,
// never 0; a valid report is read exactly.
func TestBedrockInvokeFamilyCounts(t *testing.T) {
	type check struct {
		model, body string
		want        map[string]int64 // nil: unknown
	}
	checks := []check{
		{novaModel, `{"output":{},"stopReason":"end_turn","usage":{"inputTokens":3,"outputTokens":2,"totalTokens":5}}`, map[string]int64{"input_tokens": 3, "output_tokens": 2, "total_tokens": 5}},
		{novaModel, `{"output":{},"stopReason":"end_turn","usage":{"inputTokens":3,"outputTokens":2}}`, map[string]int64{"input_tokens": 3, "output_tokens": 2}},
		{novaModel, `{"output":{},"stopReason":"end_turn","usage":{"inputTokens":null,"outputTokens":2,"totalTokens":5}}`, nil},
		{novaModel, `{"output":{},"stopReason":"end_turn","usage":{"inputTokens":-1,"outputTokens":2,"totalTokens":5}}`, nil},
		{novaModel, `{"output":{},"stopReason":"end_turn","usage":{"inputTokens":3.5,"outputTokens":2,"totalTokens":5}}`, nil},
		{novaModel, `{"output":{},"stopReason":"end_turn","usage":{"inputTokens":"3","outputTokens":2,"totalTokens":5}}`, nil},
		{novaModel, `{"output":{},"stopReason":"end_turn","usage":{"inputTokens":3,"outputTokens":2,"totalTokens":5,"cacheReadInputTokenCount":9}}`, nil},
		{novaModel, `{"stopReason":"end_turn","usage":{"inputTokens":3,"outputTokens":2,"totalTokens":5}}`, nil},
		{novaModel, `{"output":{},"stopReason":"end_turn"}`, nil},
		{titanModel, `{"inputTextTokenCount":4,"results":[{"tokenCount":6,"outputText":"x","completionReason":"FINISHED"}]}`, map[string]int64{"input_tokens": 4, "output_tokens": 6}},
		{titanModel, `{"inputTextTokenCount":4,"results":[{"tokenCount":6.0,"outputText":"x","completionReason":"FINISHED"}]}`, nil},
		{titanModel, `{"inputTextTokenCount":4,"results":[{"tokenCount":6},{"tokenCount":1}]}`, nil},
		{titanModel, `{"inputTextTokenCount":4,"results":[]}`, nil},
		{titanModel, `{"inputTextTokenCount":-4,"results":[{"tokenCount":6}]}`, nil},
		{titanModel, `{"results":[{"tokenCount":6}]}`, nil},
		{llamaModel, `{"generation":"x","prompt_token_count":7,"generation_token_count":8,"stop_reason":"stop"}`, map[string]int64{"input_tokens": 7, "output_tokens": 8}},
		{llamaModel, `{"generation":"x","prompt_token_count":7,"stop_reason":"stop"}`, map[string]int64{"input_tokens": 7}},
		{llamaModel, `{"generation":"x","prompt_token_count":"7","generation_token_count":8}`, nil},
		{llamaModel, `{"generation":"x","prompt_token_count":7,"generation_token_count":1e3}`, nil},
		{llamaModel, `{"generation":"x","prompt_token_count":null,"generation_token_count":null}`, nil},
		{llamaModel, `{"prompt_token_count":7,"generation_token_count":8}`, nil},
	}
	for _, c := range checks {
		route := fixtureRoute{provider: "anthropic", file: "counts.json", call: invokeCall(c.model, familyRequests[c.model], false)}
		run := runFixtureHTTP(t, route, []byte(c.body), modeLegacy)
		got := run.accumulate()
		if c.want == nil {
			if len(got) != 0 {
				t.Errorf("%s %s: usage %v, want unknown", c.model, c.body, got)
			}
			continue
		}
		if len(got) != len(c.want) {
			t.Errorf("%s %s: usage %v, want %v", c.model, c.body, got, c.want)
			continue
		}
		for key, n := range c.want {
			if got[key] != n {
				t.Errorf("%s %s: usage %v, want %v", c.model, c.body, got, c.want)
			}
		}
	}
}

// A strict (token-metered) lease still refuses what it cannot read: a family
// that was not added (Cohere) is unchanged, and the added families' advisory
// usage never reaches a strict lease. A request-count lease records it.
func TestBedrockInvokeStrictLeaseUnchanged(t *testing.T) {
	cohere := fixtureRoute{provider: "anthropic", file: "cohere.json", call: invokeCall("cohere.command-r-v1:0", `{"message":"hi"}`, false)}
	body := []byte(`{"text":"x","finish_reason":"COMPLETE","meta":{"billed_units":{"input_tokens":5,"output_tokens":3}}}`)
	for _, mode := range []leaseMode{modeLegacy, modeCount, modeStrict} {
		if run := runFixtureHTTP(t, cohere, body, mode); len(run.observations) != 0 || run.settled() {
			t.Fatalf("Cohere body observed or settled (mode %d): %+v %+v", mode, run.observations, run.results)
		}
	}
	nova := fixtureRoute{provider: "anthropic", file: "nova.json", call: invokeCall(novaModel, familyRequests[novaModel], false)}
	data := readAnthropicFixture(t, "bedrock-invoke-nova.json")
	if run := runFixtureHTTP(t, nova, data, modeStrict); len(run.observations) != 0 || run.settled() {
		t.Fatalf("advisory family usage reached a strict lease: %+v %+v", run.observations, run.results)
	}
	run := runFixtureHTTP(t, nova, data, modeCount)
	if final, ok := run.terminal(); !ok || final["input_tokens"] != 431 || !run.settled() {
		t.Fatalf("request-count lease did not record the advisory usage: %+v %+v", run.observations, run.results)
	}
}

// Family streams are not observed: no usage, a request-count lease settles
// the request, a strict lease is not settled.
func TestBedrockInvokeFamilyStreamsUnobserved(t *testing.T) {
	data := readAnthropicFixture(t, "bedrock-invoke-stream-titan-text.jsonl")
	for _, model := range []string{titanModel, novaModel, llamaModel} {
		route := fixtureRoute{provider: "anthropic", file: "stream.jsonl", call: invokeCall(model, familyRequests[model], true)}
		for _, mode := range []leaseMode{modeLegacy, modeCount, modeStrict} {
			run := runFixtureHTTP(t, route, data, mode)
			if run.code != 200 || len(run.accumulate()) != 0 {
				t.Fatalf("%s stream (mode %d): code %d usage %v", model, mode, run.code, run.accumulate())
			}
			if mode == modeStrict && run.settled() {
				t.Fatalf("%s stream settled a strict lease", model)
			}
		}
		// An Anthropic Messages stream under a family id is not parsed either.
		messages := readAnthropicFixture(t, "bedrock-invoke-stream.jsonl")
		if run := runFixtureHTTP(t, route, messages, modeLegacy); len(run.accumulate()) != 0 {
			t.Fatalf("Messages stream under %s reached the Anthropic parser: %v", model, run.accumulate())
		}
	}
}

// An id that names a vendor other than Anthropic never reaches the Anthropic
// parser, JSON or stream; an Anthropic id or an id that names no vendor is
// read as before.
func TestBedrockInvokeVendorRule(t *testing.T) {
	messages := readAnthropicFixture(t, "bedrock-invoke.json")
	streamData := readAnthropicFixture(t, "bedrock-invoke-stream.jsonl")
	request := `{"anthropic_version":"bedrock-2023-05-31","max_tokens":8,"messages":[]}`
	for _, test := range []struct {
		model string
		read  bool
	}{
		{"cohere.command-r-v1:0", false},
		{"mistral.mistral-large-2402-v1:0", false},
		{"amazon.nova-premier-v1:0", false},
		{"xx.amazon.nova-pro-v1:0", false},
		{"meta.llama4-x", false},
		{"arn:aws:bedrock:us-east-1:123456789012:inference-profile/us.cohere.command-r-v1:0", false},
		{"anthropic.claude-fixture-v1:0", true},
		{"us.anthropic.claude-fixture-v1:0", true},
		{"arn:aws:bedrock:us-east-1:123456789012:provisioned-model/abcdefghijkl", true},
		{"arn:aws:bedrock:us-east-1:123456789012:inference-profile/us.anthropic.claude-fixture-v1:0", true},
		{"model-v1", true},
	} {
		if got := bedrockInvokeRouteFor(test.model); got.anthropic != test.read || got.family != nil {
			t.Errorf("%s: route %+v, want anthropic=%t", test.model, got, test.read)
		}
		if strings.HasPrefix(test.model, "arn:") {
			// A surface refuses a path model containing '/', so an ARN
			// cannot be relayed today; the rule is checked above.
			continue
		}
		for _, stream := range []bool{false, true} {
			data := messages
			if stream {
				data = streamData
			}
			route := fixtureRoute{provider: "anthropic", file: "vendor.json", call: invokeCall(test.model, request, stream)}
			run := runFixtureHTTP(t, route, data, modeLegacy)
			if run.code != 200 {
				t.Fatalf("%s (stream %t): %d %s", test.model, stream, run.code, run.body)
			}
			if got := len(run.accumulate()) > 0; got != test.read {
				t.Errorf("%s (stream %t): usage %v, want read=%t", test.model, stream, run.accumulate(), test.read)
			}
		}
	}
}

// Nova's three counts must agree: a total that is not input plus output is a
// self-contradicting report and none of the three is recorded.
func TestBedrockInvokeNovaTotalConsistency(t *testing.T) {
	route := fixtureRoute{provider: "anthropic", file: "nova.json", call: invokeCall(novaModel, familyRequests[novaModel], false)}
	consistent := runFixtureHTTP(t, route, []byte(`{"output":{},"stopReason":"end_turn","usage":{"inputTokens":3,"outputTokens":2,"totalTokens":5}}`), modeLegacy).accumulate()
	if consistent["input_tokens"] != 3 || consistent["output_tokens"] != 2 || consistent["total_tokens"] != 5 {
		t.Fatalf("consistent report: %v", consistent)
	}
	if got := runFixtureHTTP(t, route, []byte(`{"output":{},"stopReason":"end_turn","usage":{"inputTokens":3,"outputTokens":2,"totalTokens":99}}`), modeLegacy).accumulate(); len(got) != 0 {
		t.Fatalf("contradicting report recorded: %v", got)
	}
}

// Titan: input present and the result's tokenCount missing records input
// only; output stays absent (unknown), never 0.
func TestBedrockInvokeTitanMissingOutput(t *testing.T) {
	route := fixtureRoute{provider: "anthropic", file: "titan.json", call: invokeCall(titanModel, familyRequests[titanModel], false)}
	got := runFixtureHTTP(t, route, []byte(`{"inputTextTokenCount":4,"results":[{"outputText":"x","completionReason":"FINISHED"}]}`), modeLegacy).accumulate()
	if _, present := got["output_tokens"]; present || got["input_tokens"] != 4 || len(got) != 1 {
		t.Fatalf("usage %v, want input only", got)
	}
}

// The documented end marker is required: without it the report is unknown.
func TestBedrockInvokeEndMarkerRequired(t *testing.T) {
	for model, body := range map[string]string{
		novaModel:  `{"output":{},"usage":{"inputTokens":3,"outputTokens":2,"totalTokens":5}}`,
		titanModel: `{"inputTextTokenCount":4,"results":[{"tokenCount":6,"outputText":"x"}]}`,
		llamaModel: `{"generation":"x","prompt_token_count":7,"generation_token_count":8,"stop_reason":""}`,
	} {
		route := fixtureRoute{provider: "anthropic", file: "marker.json", call: invokeCall(model, familyRequests[model], false)}
		for _, mode := range []leaseMode{modeLegacy, modeCount} {
			if got := runFixtureHTTP(t, route, []byte(body), mode).accumulate(); len(got) != 0 {
				t.Fatalf("%s without end marker (mode %d): %v", model, mode, got)
			}
		}
	}
}
