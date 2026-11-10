package modelapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
)

type classRecorder struct {
	mu      sync.Mutex
	classes []string
}

func (c *classRecorder) note(_ context.Context, class string) {
	c.mu.Lock()
	c.classes = append(c.classes, class)
	c.mu.Unlock()
}

func (c *classRecorder) all() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.Join(c.classes, ",")
}

// A provider's 401 or 403 is the gateway's own credential being refused: the
// caller gets a 502 in the protocol's native error shape whose code says the
// upstream credential was rejected, the provider's body never reaches the
// caller, the lease is released as a rejected request, and the error class is
// upstream_auth. Operators see the surface id; others do not.
func TestUpstreamCredentialRejectionIsA502WithOperatorOnlyDetail(t *testing.T) {
	const upstreamBody = `{"error":{"message":"Incorrect API key provided: sk-operator-secret-value","type":"invalid_request_error","code":"invalid_api_key"}}`
	for _, tc := range []struct {
		name     string
		provider Provider
		protocol string
		models   []string
		op       string
		path     string
		body     string
		status   int
		respBody string
		check    func(t *testing.T, body map[string]any, operator bool)
	}{
		{"openai 401", Provider{ID: ProviderOpenAI}, ProtocolOpenAIChat, []string{"m"}, "openai.v1.chat-completions", "/v1/chat/completions", `{"model":"m","messages":[]}`, 401, upstreamBody,
			func(t *testing.T, body map[string]any, operator bool) {
				e, _ := body["error"].(map[string]any)
				if e["code"] != UpstreamCredentialRejectedCode || e["type"] != "server_error" {
					t.Fatalf("%v", body)
				}
				_, has := e["details"]
				if has != operator {
					t.Fatalf("details present=%v operator=%v: %v", has, operator, body)
				}
			}},
		{"anthropic 403", Provider{ID: ProviderAnthropic, AnthropicVersion: "2023-06-01"}, ProtocolAnthropicMessages, []string{"claude-x"}, "anthropic.messages", "/v1/messages", `{"model":"claude-x","max_tokens":1,"messages":[]}`, 403, `{"type":"error","error":{"type":"permission_error","message":"x-api-key sk-operator-secret-value"}}`,
			func(t *testing.T, body map[string]any, operator bool) {
				e, _ := body["error"].(map[string]any)
				if body["type"] != "error" || e["type"] != "api_error" || e["code"] != UpstreamCredentialRejectedCode {
					t.Fatalf("%v", body)
				}
				if _, has := e["details"]; has != operator {
					t.Fatalf("details: %v", body)
				}
			}},
	} {
		for _, operator := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s operator=%v", tc.name, operator), func(t *testing.T) {
				up := newProviderUpstream(t, func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.Header().Set("Retry-After", "5")
					w.WriteHeader(tc.status)
					fmt.Fprint(w, tc.respBody)
				})
				s := providerSurface(t, up.URL, tc.provider, tc.protocol, tc.models, tc.op)
				classes := &classRecorder{}
				lease := &modelLease{}
				g := providerGateway(t, s, headerCredential("Authorization", "Bearer "), lease, func(o *Options) {
					o.NoteError = classes.note
					o.OperatorDetail = func(context.Context) bool { return operator }
				})
				w := providerServe(g, "POST", tc.path, tc.body, nil)
				if w.Code != 502 {
					t.Fatalf("status %d: %s", w.Code, w.Body.String())
				}
				if strings.Contains(w.Body.String(), "sk-operator-secret-value") || strings.Contains(w.Body.String(), "invalid_api_key") || w.Header().Get("Retry-After") != "" {
					t.Fatalf("the provider's body or headers reached the caller: %s %v", w.Body.String(), w.Header())
				}
				var body map[string]any
				if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				tc.check(t, body, operator)
				if operator != strings.Contains(w.Body.String(), `"surface_id":"native"`) {
					t.Fatalf("surface id visibility wrong (operator=%v): %s", operator, w.Body.String())
				}
				if classes.all() != ErrorClassUpstreamAuth {
					t.Fatalf("classes %q", classes.all())
				}
				if len(lease.results) != 1 || !lease.results[0].Rejected {
					t.Fatalf("the lease must be released as a rejected request: %+v", lease.results)
				}
			})
		}
	}
}

func TestGeminiInvalidKeyIs502ButOtherBadRequestsAreRelayed(t *testing.T) {
	invalid := `{"error":{"code":400,"message":"API key not valid. Please pass a valid API key.","status":"INVALID_ARGUMENT","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"API_KEY_INVALID","domain":"googleapis.com"}]}}`
	other := `{"error":{"code":400,"message":"Request contains an invalid argument.","status":"INVALID_ARGUMENT"}}`
	up := newProviderUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		if strings.Contains(r.URL.Path, "bad-key-model") {
			fmt.Fprint(w, invalid)
			return
		}
		fmt.Fprint(w, other)
	})
	s := geminiDASurface("gemini.models.generateContent")(up.URL)
	s.Models = []string{"bad-key-model", "model-a"}
	classes := &classRecorder{}
	g := providerGateway(t, s, headerCredential("X-Goog-Api-Key", ""), nil, func(o *Options) { o.NoteError = classes.note })
	w := providerServe(g, "POST", "/v1beta/models/bad-key-model:generateContent", `{"contents":[]}`, nil)
	if w.Code != 502 || !strings.Contains(w.Body.String(), "UPSTREAM_CREDENTIAL_REJECTED") || strings.Contains(w.Body.String(), "API_KEY_INVALID") || classes.all() != ErrorClassUpstreamAuth {
		t.Fatalf("%d %s classes=%q", w.Code, w.Body.String(), classes.all())
	}
	// Other 400s reach the caller unchanged, whole.
	w = providerServe(g, "POST", "/v1beta/models/model-a:generateContent", `{"contents":[]}`, nil)
	if w.Code != 400 || w.Body.String() != other {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}

// OpenAI's 429 insufficient_quota is a distinct error class from a rate limit;
// the response itself is relayed unchanged.
func TestInsufficientQuotaIsDistinctFromRateLimit(t *testing.T) {
	quota := `{"error":{"message":"You exceeded your current quota, please check your plan and billing details.","type":"insufficient_quota","param":null,"code":"insufficient_quota"}}`
	rate := `{"error":{"message":"Rate limit reached for gpt-x","type":"requests","param":null,"code":"rate_limit_exceeded"}}`
	var current string
	up := newProviderUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(429)
		fmt.Fprint(w, current)
	})
	s := providerSurface(t, up.URL, Provider{ID: ProviderOpenAI}, ProtocolOpenAIChat, []string{"m"}, "openai.v1.chat-completions")
	for body, want := range map[string]string{quota: ErrorClassUpstreamQuotaExhausted, rate: ""} {
		current = body
		classes := &classRecorder{}
		g := providerGateway(t, s, headerCredential("Authorization", "Bearer "), nil, func(o *Options) { o.NoteError = classes.note })
		w := providerServe(g, "POST", "/v1/chat/completions", `{"model":"m","messages":[]}`, nil)
		if w.Code != 429 || w.Body.String() != body || classes.all() != want {
			t.Fatalf("%d %q classes=%q want %q", w.Code, w.Body.String(), classes.all(), want)
		}
	}
	// Bedrock names a quota exhaustion in the exception type header.
	bedrockUp := newProviderUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Amzn-Errortype", "ServiceQuotaExceededException:http://internal.amazon.com/coral/com.amazon.bedrock/")
		w.WriteHeader(400)
		fmt.Fprint(w, `{"message":"quota"}`)
	})
	bs := providerSurface(t, bedrockUp.URL, Provider{ID: ProviderBedrock, Region: "us-east-1"}, ProtocolBedrockConverse, []string{"anthropic.claude-3-5-sonnet-20240620-v1:0"}, "bedrock.converse")
	classes := &classRecorder{}
	g := providerGateway(t, bs, CredentialFunc(func(context.Context, *http.Request) error { return nil }), nil, func(o *Options) { o.NoteError = classes.note })
	w := providerServe(g, "POST", "/model/anthropic.claude-3-5-sonnet-20240620-v1:0/converse", `{"messages":[]}`, nil)
	if w.Code != 400 || classes.all() != ErrorClassUpstreamQuotaExhausted {
		t.Fatalf("bedrock: %d %q", w.Code, classes.all())
	}
}
