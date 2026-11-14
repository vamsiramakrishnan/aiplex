package modelapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// Anthropic Messages on the Bedrock "Mantle" endpoint
// (bedrock-mantle.messages).

const mantleModel = "anthropic.claude-fixture-v1"

func mantleSurface(region string) func(string) Surface {
	return providerSurfaceFor(Provider{ID: ProviderBedrock, Region: region}, ProtocolAnthropicMessages, []string{mantleModel}, BedrockMantleMessagesOperation)
}

var callMantle = &fixtureCall{surface: mantleSurface("us-east-1"), path: "/anthropic/v1/messages", body: `{"model":"` + mantleModel + `","max_tokens":8,"messages":[],"stream":true}`}

func TestBedrockMantleCatalogue(t *testing.T) {
	ops, err := SelectProviderOperations(ProviderBedrock, "bedrock-mantle.messages")
	if err != nil {
		t.Fatal(err)
	}
	want := Operation{ID: "bedrock-mantle.messages", Method: "POST", Path: "/anthropic/v1/messages", Transport: SSE, Action: Invoke, SourceURL: "https://docs.aws.amazon.com/bedrock/latest/userguide/inference-messages-api.html"}
	if ops[0] != want {
		t.Fatalf("catalogue entry\n got %+v\nwant %+v", ops[0], want)
	}
	if got := Coverage(ops[0]); got != (ExecutionCoverage{RequestCount: true, TokenProfile: "anthropic-text"}) {
		t.Fatalf("coverage %+v", got)
	}
	if OperationProtocol(ProviderBedrock, "bedrock-mantle.messages") != ProtocolAnthropicMessages || UsageFormat(ProviderBedrock, "bedrock-mantle.messages") != FormatAnthropicMessages {
		t.Fatal("Mantle Messages is not the Anthropic Messages wire")
	}
	s := mantleSurface("us-east-1")("https://bedrock-mantle.us-east-1.api.aws")
	if !s.BedrockMantle() || s.BedrockSigningService() != "bedrock-mantle" {
		t.Fatal("Mantle surface signing service")
	}
	runtime := providerSurfaceFor(Provider{ID: ProviderBedrock, Region: "us-east-1"}, ProtocolBedrockConverse, []string{mantleModel}, "bedrock.converse")("https://bedrock-runtime.us-east-1.amazonaws.com")
	if runtime.BedrockMantle() || runtime.BedrockSigningService() != "bedrock" {
		t.Fatal("runtime surface signing service changed")
	}
}

// The origin is exactly bedrock-mantle.{region}.api.aws for the configured
// region; anything else is refused when the surface is validated, so a
// gateway is never built and nothing is signed or sent.
func TestBedrockMantleOriginIsExact(t *testing.T) {
	mixed := func(origin string) Surface {
		s := mantleSurface("us-east-1")(origin)
		converse, _ := SelectProviderOperations(ProviderBedrock, "bedrock.converse")
		s.Operations = append(s.Operations, converse...)
		return s
	}
	for _, test := range []struct {
		name string
		s    Surface
		ok   bool
	}{
		{"documented host", mantleSurface("us-east-1")("https://bedrock-mantle.us-east-1.api.aws"), true},
		{"other region", mantleSurface("eu-west-1")("https://bedrock-mantle.eu-west-1.api.aws"), true},
		{"region not configured", mantleSurface("us-east-1")("https://bedrock-mantle.eu-west-1.api.aws"), false},
		{"runtime host", mantleSurface("us-east-1")("https://bedrock-runtime.us-east-1.amazonaws.com"), false},
		{"suffix host", mantleSurface("us-east-1")("https://bedrock-mantle.us-east-1.api.aws.evil.example"), false},
		{"wildcard region", mantleSurface("*")("https://bedrock-mantle.*.api.aws"), false},
		{"empty region", mantleSurface("")("https://bedrock-mantle..api.aws"), false},
		{"region with a dot", mantleSurface("us-east-1.evil")("https://bedrock-mantle.us-east-1.evil.api.aws"), false},
		{"region with a slash", mantleSurface("us-east-1/x")("https://bedrock-mantle.us-east-1.api.aws"), false},
		{"region with an at sign", mantleSurface("us-east-1@evil")("https://bedrock-mantle.us-east-1.api.aws"), false},
		{"Mantle and runtime operations together", mixed("https://bedrock-mantle.us-east-1.api.aws"), false},
		{"documented host with a port", mantleSurface("us-east-1")("https://bedrock-mantle.us-east-1.api.aws:8443"), false},
		{"plain http to another host", mantleSurface("us-east-1")("http://evil.example"), false},
		{"plain http to the documented host", mantleSurface("us-east-1")("http://bedrock-mantle.us-east-1.api.aws"), false},
		{"http loopback IPv4", mantleSurface("us-east-1")("http://127.0.0.1:1234"), true},
		{"http localhost", mantleSurface("us-east-1")("http://localhost:1234"), true},
		{"http loopback IPv6", mantleSurface("us-east-1")("http://[::1]:1234"), true},
		{"another protocol", withProtocol(mantleSurface("us-east-1")("https://bedrock-mantle.us-east-1.api.aws"), ProtocolBedrockInvoke), false},
		{"anthropic-messages without the Mantle operation", withProtocol(providerSurfaceFor(Provider{ID: ProviderBedrock, Region: "us-east-1"}, ProtocolBedrockInvoke, []string{mantleModel}, "bedrock.invoke")("https://bedrock-runtime.us-east-1.amazonaws.com"), ProtocolAnthropicMessages), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := test.s.Validate()
			if (err == nil) != test.ok {
				t.Fatalf("validate: %v, want accepted=%t", err, test.ok)
			}
			if test.ok {
				return
			}
			signed := 0
			opts := options(t, "http://127.0.0.1:1", newOwnership())
			opts.TokenSource = nil
			opts.Credential = CredentialFunc(func(context.Context, *http.Request) error { signed++; return nil })
			if g, err := NewGateway(test.s, opts); err == nil || g != nil || signed != 0 {
				t.Fatalf("gateway built for a refused origin (err=%v, signed=%d)", err, signed)
			}
		})
	}
}

func withProtocol(s Surface, protocol string) Surface {
	s.Protocol = protocol
	return s
}

// A refused gateway credential is answered in the Anthropic error shape, the
// shape the Mantle wire's clients (the Anthropic SDK) read.
func TestBedrockMantleUpstreamAuthErrorShape(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"The security token included in the request is invalid: AKIDLEAKED"}`))
	}))
	defer upstream.Close()
	opts := options(t, upstream.URL, newOwnership())
	opts.TokenSource = nil
	opts.Credential = CredentialFunc(func(context.Context, *http.Request) error { return nil })
	g, err := NewGateway(mantleSurface("us-east-1")(upstream.URL), opts)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/anthropic/v1/messages", strings.NewReader(`{"model":"`+mantleModel+`","max_tokens":8,"messages":[]}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	g.ServeHTTP(w, r)
	var body struct {
		Type  string `json:"type"`
		Error struct {
			Type    string `json:"type"`
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if w.Code != http.StatusBadGateway || json.Unmarshal(w.Body.Bytes(), &body) != nil || body.Type != "error" || body.Error.Type != "api_error" || body.Error.Code != UpstreamCredentialRejectedCode || body.Error.Message == "" {
		t.Fatalf("not the Anthropic error shape: %d %s", w.Code, w.Body.String())
	}
	if w.Header().Get("X-Amzn-Errortype") != "" || strings.Contains(w.Body.String(), "AKIDLEAKED") {
		t.Fatalf("Bedrock shape or the provider body relayed: %v %s", w.Header(), w.Body.String())
	}
}

// mantleRun is one exchange through a Mantle surface with a recording
// credential and upstream.
type mantleRun struct {
	code     int
	body     string
	headers  http.Header
	uri      string
	upstream int
	signed   int
}

func runMantle(t *testing.T, credential func(*http.Request) error, headers map[string]string, body string) mantleRun {
	t.Helper()
	run := mantleRun{}
	var mu sync.Mutex
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		run.upstream++
		run.headers, run.uri = r.Header.Clone(), r.RequestURI
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","model":"` + mantleModel + `","content":[],"stop_reason":"end_turn","usage":{"input_tokens":5,"output_tokens":3}}`))
	}))
	defer upstream.Close()
	opts := options(t, upstream.URL, newOwnership())
	opts.TokenSource = nil
	opts.Credential = CredentialFunc(func(_ context.Context, r *http.Request) error {
		mu.Lock()
		run.signed++
		mu.Unlock()
		return credential(r)
	})
	g, err := NewGateway(mantleSurface("us-east-1")(upstream.URL), opts)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/anthropic/v1/messages", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	g.ServeHTTP(w, r)
	run.code, run.body = w.Code, w.Body.String()
	return run
}

func TestBedrockMantleSignsAndPinsHeaders(t *testing.T) {
	body := `{"model":"` + mantleModel + `","max_tokens":8,"messages":[]}`
	sign := func(r *http.Request) error {
		r.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20261114/us-east-1/bedrock-mantle/aws4_request")
		return nil
	}
	caller := map[string]string{"Authorization": "Bearer caller-token", "X-Api-Key": "caller-token", "X-Amz-Security-Token": "caller-session", "Anthropic-Version": "2023-06-01"}
	run := runMantle(t, sign, caller, body)
	if run.code != 200 || run.upstream != 1 || run.signed != 1 {
		t.Fatalf("exchange: %d %s (upstream %d, signed %d)", run.code, run.body, run.upstream, run.signed)
	}
	if run.uri != "/anthropic/v1/messages" || run.headers.Get("Anthropic-Version") != "2023-06-01" || !strings.Contains(run.headers.Get("Authorization"), "/bedrock-mantle/") {
		t.Fatalf("upstream request %s %v", run.uri, run.headers)
	}
	for name, values := range run.headers {
		for _, value := range values {
			if strings.Contains(value, "caller-") {
				t.Fatalf("caller credential forwarded in %s", name)
			}
		}
	}
	// The version is pinned to the documented value; betas are not enabled.
	for _, header := range []map[string]string{{"Anthropic-Version": "2023-01-01"}, {"Anthropic-Beta": "some-beta-2026-01-01"}} {
		if run := runMantle(t, sign, header, body); run.code != http.StatusBadRequest || run.upstream != 0 {
			t.Fatalf("%v: %d %s (upstream %d)", header, run.code, run.body, run.upstream)
		}
	}
	// The model is in the body and must be allowed.
	if run := runMantle(t, sign, nil, `{"model":"anthropic.other","max_tokens":8,"messages":[]}`); run.code != http.StatusForbidden || run.upstream != 0 {
		t.Fatalf("other model: %d (upstream %d)", run.code, run.upstream)
	}
}

// A request is never sent unsigned: a failed signature is a refusal.
func TestBedrockMantleRefusesWithoutSignature(t *testing.T) {
	run := runMantle(t, func(*http.Request) error { return errors.New("AWS credentials missing or expired") }, nil, `{"model":"`+mantleModel+`","max_tokens":8,"messages":[]}`)
	if run.code != http.StatusServiceUnavailable || run.upstream != 0 || strings.Contains(run.body, "AWS credentials") {
		t.Fatalf("unsigned request: %d %q (upstream %d)", run.code, run.body, run.upstream)
	}
}

// The same Messages response, through Bedrock InvokeModel (the runtime
// Messages path) and through Mantle, is metered identically, streaming and
// not, including the per-TTL cache-write counters.
func TestBedrockMantleMetersLikeRuntimeMessages(t *testing.T) {
	usage := `{"input_tokens":120,"cache_creation_input_tokens":2304,"cache_read_input_tokens":512,"cache_creation":{"ephemeral_5m_input_tokens":256,"ephemeral_1h_input_tokens":2048},"output_tokens":40}`
	message := `{"id":"msg_1","type":"message","role":"assistant","model":"` + mantleModel + `","content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn","stop_sequence":null,"usage":` + usage + `}`
	events := []string{
		`{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"` + mantleModel + `","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":120,"cache_creation_input_tokens":2304,"cache_read_input_tokens":512,"cache_creation":{"ephemeral_5m_input_tokens":256,"ephemeral_1h_input_tokens":2048},"output_tokens":1}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":` + usage + `}`,
		`{"type":"message_stop"}`,
	}
	var sse bytes.Buffer
	for _, event := range events {
		var probe struct{ Type string }
		_ = json.Unmarshal([]byte(event), &probe)
		sse.WriteString("event: " + probe.Type + "\ndata: " + event + "\n\n")
	}
	jsonl := []byte(strings.Join(events, "\n") + "\n")
	claudeBody := func(stream bool) string {
		if stream {
			return `{"model":"claude-fixture","max_tokens":8,"messages":[],"stream":true}`
		}
		return `{"model":"claude-fixture","max_tokens":8,"messages":[]}`
	}
	for _, test := range []struct {
		name                    string
		runtime, mantle, claude fixtureRoute
		runtimeData, mantleData []byte
	}{
		{
			name:        "json",
			runtime:     fixtureRoute{provider: "anthropic", file: "equivalence.json", call: callInvoke, native: natAnthropic},
			mantle:      fixtureRoute{provider: "anthropic", file: "equivalence.json", call: with(callMantle, func(c *fixtureCall) { c.body = `{"model":"` + mantleModel + `","max_tokens":8,"messages":[]}` }), native: natAnthropic},
			claude:      fixtureRoute{provider: "anthropic", file: "equivalence.json", call: with(callAnthropic, func(c *fixtureCall) { c.body = claudeBody(false) }), native: natAnthropic},
			runtimeData: []byte(message), mantleData: []byte(message),
		},
		{
			name:        "stream",
			runtime:     fixtureRoute{provider: "anthropic", file: "equivalence.jsonl", call: callInvokeS, native: natAnthropic},
			mantle:      fixtureRoute{provider: "anthropic", file: "equivalence.sse", call: callMantle, native: natAnthropic},
			claude:      fixtureRoute{provider: "anthropic", file: "equivalence.sse", call: with(callAnthropic, func(c *fixtureCall) { c.body = claudeBody(true) }), native: natAnthropic},
			runtimeData: jsonl, mantleData: sse.Bytes(),
		},
	} {
		for _, mode := range []leaseMode{modeLegacy, modeCount, modeStrict} {
			runtime := runFixtureHTTP(t, test.runtime, test.runtimeData, mode)
			mantle := runFixtureHTTP(t, test.mantle, test.mantleData, mode)
			claude := runFixtureHTTP(t, test.claude, test.mantleData, mode)
			if runtime.code != 200 || mantle.code != 200 || claude.code != 200 {
				t.Fatalf("%s mode %d: codes runtime %d %s, mantle %d %s, claude %d", test.name, mode, runtime.code, runtime.body, mantle.code, mantle.body, claude.code)
			}
			// Same wire as the Claude API: identical observations, one by one.
			if !reflect.DeepEqual(claude.observations, mantle.observations) {
				t.Fatalf("%s mode %d: observations differ from the Claude API path\n claude %+v\n mantle %+v", test.name, mode, claude.observations, mantle.observations)
			}
			// Same usage as Bedrock InvokeModel (the runtime Messages path).
			// InvokeModel streams AWS event-stream chunks, so the sequence of
			// interim observations differs; the final usage, field by field,
			// the accumulated usage and the settlement must not.
			runtimeFinal, runtimeTerminal := runtime.terminal()
			final, terminal := mantle.terminal()
			if !runtimeTerminal || !terminal || !reflect.DeepEqual(runtimeFinal, final) || !reflect.DeepEqual(runtime.accumulate(), mantle.accumulate()) {
				t.Fatalf("%s mode %d: usage differs from InvokeModel\n runtime %v (accumulated %v)\n mantle  %v (accumulated %v)", test.name, mode, runtimeFinal, runtime.accumulate(), final, mantle.accumulate())
			}
			if final["cache_write_5m_tokens"] != 256 || final["cache_write_1h_tokens"] != 2048 || final["cache_write_tokens"] != 2304 || final["cached_tokens"] != 512 || final["output_tokens"] != 40 || (mode != modeStrict && final["input_tokens"] != 120) || (mode == modeStrict && (final["uncached_input_tokens"] != 120 || final["input_tokens"] != 120+512+2304)) {
				t.Fatalf("%s mode %d: final mantle usage %v", test.name, mode, final)
			}
			if !reflect.DeepEqual(runtime.results, mantle.results) {
				t.Fatalf("%s mode %d: settlement differs\n runtime %+v\n mantle  %+v", test.name, mode, runtime.results, mantle.results)
			}
			if mode == modeStrict && !mantle.settled() {
				t.Fatalf("%s: strict anthropic-text lease did not settle: %+v", test.name, mantle.results)
			}
		}
	}
}
