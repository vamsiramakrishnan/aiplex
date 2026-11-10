package api

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/internal/connections"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/pkg/endpointpolicy"
	"github.com/vamsiramakrishnan/aiplex/pkg/modelapi"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
	"github.com/vamsiramakrishnan/aiplex/pkg/toolcredential"
	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

const providerTestSecret = "sk-tenant-secret-from-reference"

type providerTestSecrets map[string]string

func (s providerTestSecrets) Resolve(_ context.Context, tenant, ref string) (string, error) {
	if v, ok := s[tenant+"/"+ref]; ok {
		return v, nil
	}
	return "", fmt.Errorf("secret unavailable")
}
func (s providerTestSecrets) Exists(_ context.Context, tenant, ref string) (bool, error) {
	_, ok := s[tenant+"/"+ref]
	return ok, nil
}

func installProviderCredentials(t *testing.T, h *ConnectedHandler) {
	t.Helper()
	provider, err := toolcredential.NewProvider(toolcredential.Config{HTTPClient: h.Runtime.HTTP, Policy: h.Runtime.Policy, Secrets: providerTestSecrets{"tenant/provider-key": providerTestSecret}})
	if err != nil {
		t.Fatal(err)
	}
	h.ToolCredentials = provider
}

func putProviderSurface(t *testing.T, store resources.Store, connection ModelConnection) {
	t.Helper()
	spec, err := json.Marshal(connection)
	if err != nil {
		t.Fatal(err)
	}
	prior, err := store.Get(context.Background(), "tenant", "model-surface", connection.Surface.ID)
	revision := uint64(0)
	if err == nil {
		revision = prior.Revision
	}
	if _, err = store.Put(context.Background(), resources.Record{TenantID: "tenant", Kind: "model-surface", ID: connection.Surface.ID, Spec: spec}, revision); err != nil {
		t.Fatal(err)
	}
}

func providerConnection(t *testing.T, origin string, p modelapi.Provider, protocol string, auth *toolcredential.Auth, ids ...string) ModelConnection {
	t.Helper()
	ops, err := modelapi.SelectProviderOperations(p.ID, ids...)
	if err != nil {
		t.Fatal(err)
	}
	return ModelConnection{Surface: modelapi.Surface{ID: "surface", Protocol: protocol, Origin: origin, Models: []string{"model-v1"}, Operations: ops, Provider: &p}, Auth: auth}
}

var (
	bearerRef    = &toolcredential.Auth{Type: toolcredential.Bearer, SecretRef: "provider-key"}
	anthropicRef = &toolcredential.Auth{Type: toolcredential.APIKey, In: toolcredential.InHeader, Name: "x-api-key", SecretRef: "provider-key"}
	azureRef     = &toolcredential.Auth{Type: toolcredential.APIKey, In: toolcredential.InHeader, Name: "api-key", SecretRef: "provider-key"}
)

func TestManagedProviderSurfacesSettleStrictTextProfiles(t *testing.T) {
	type providerCase struct {
		name, provider, protocol, operation, credentialHeader, path, body, response string
		auth                                                                        *toolcredential.Auth
		settings                                                                    modelapi.Provider
		facade, sse                                                                 bool
	}
	chatJSON := `{"id":"chatcmpl-1","model":"model-v1","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":7,"completion_tokens":3,"total_tokens":10,"prompt_tokens_details":{"cached_tokens":2},"completion_tokens_details":{"reasoning_tokens":1}}}`
	chatSSE := "data: {\"id\":\"chatcmpl-1\",\"model\":\"model-v1\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: {\"id\":\"chatcmpl-1\",\"model\":\"model-v1\",\"choices\":[],\"usage\":{\"prompt_tokens\":7,\"prompt_tokens_details\":{\"cached_tokens\":2},\"completion_tokens\":3,\"completion_tokens_details\":{\"reasoning_tokens\":1},\"total_tokens\":10}}\n\ndata: [DONE]\n\n"
	responsesJSON := `{"id":"resp_1","model":"model-v1","status":"completed","usage":{"input_tokens":7,"input_tokens_details":{"cached_tokens":2},"output_tokens":3,"output_tokens_details":{"reasoning_tokens":1},"total_tokens":10}}`
	messagesJSON := `{"id":"msg_1","type":"message","model":"model-v1","stop_reason":"end_turn","usage":{"input_tokens":5,"cache_read_input_tokens":2,"cache_creation_input_tokens":0,"output_tokens":3}}`
	messagesSSE := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"model\":\"model-v1\",\"usage\":{\"input_tokens\":5,\"cache_read_input_tokens\":2,\"cache_creation_input_tokens\":0,\"output_tokens\":1}}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":3}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	chatBody := `{"model":"model-v1","messages":[{"role":"user","content":"hello"}],"max_completion_tokens":3}`
	anthropicSettings := modelapi.Provider{ID: modelapi.ProviderAnthropic, AnthropicVersion: "2023-06-01"}
	tests := []providerCase{
		{"openai-chat-json", modelapi.ProviderOpenAI, "openai-chat", "openai.v1.chat-completions", "Authorization", "/v1/chat/completions", chatBody, chatJSON, bearerRef, modelapi.Provider{ID: modelapi.ProviderOpenAI}, false, false},
		{"openai-chat-sse-facade", modelapi.ProviderOpenAI, "openai-chat", "openai.v1.chat-completions", "Authorization", "/v1/chat/completions", `{"model":"model-v1","messages":[{"role":"user","content":"hello"}],"max_completion_tokens":3,"stream":true,"stream_options":{"include_usage":true}}`, chatSSE, bearerRef, modelapi.Provider{ID: modelapi.ProviderOpenAI}, true, true},
		{"openai-responses-facade", modelapi.ProviderOpenAI, "openai-responses", "openai.v1.responses", "Authorization", "/v1/responses", `{"model":"model-v1","input":"hello","max_output_tokens":3,"store":false}`, responsesJSON, bearerRef, modelapi.Provider{ID: modelapi.ProviderOpenAI}, true, false},
		{"anthropic-json", modelapi.ProviderAnthropic, "anthropic-messages", "anthropic.messages", "X-Api-Key", "/v1/messages", `{"model":"model-v1","messages":[{"role":"user","content":"hello"}],"max_tokens":3}`, messagesJSON, anthropicRef, anthropicSettings, false, false},
		{"anthropic-sse-facade", modelapi.ProviderAnthropic, "anthropic-messages", "anthropic.messages", "X-Api-Key", "/v1/messages", `{"model":"model-v1","messages":[{"role":"user","content":"hello"}],"max_tokens":3,"stream":true}`, messagesSSE, anthropicRef, anthropicSettings, true, true},
		{"azure-deployment", modelapi.ProviderAzureOpenAI, "openai-chat", "azure.deployments.chat-completions", "Api-Key", "/openai/deployments/model-v1/chat/completions", `{"messages":[{"role":"user","content":"hello"}],"max_completion_tokens":3}`, chatJSON, azureRef, modelapi.Provider{ID: modelapi.ProviderAzureOpenAI, APIVersion: "2024-10-21"}, true, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var mu sync.Mutex
			var seen []http.Header
			f := quotaHostModelNew(t, true, func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				seen = append(seen, r.Header.Clone())
				mu.Unlock()
				if test.sse {
					w.Header().Set("Content-Type", "text/event-stream")
				} else {
					w.Header().Set("Content-Type", "application/json")
				}
				w.Header().Set("X-Ratelimit-Remaining-Tokens", "999")
				fmt.Fprint(w, test.response)
			}, func(cfg *QuotaHostConfig) {
				cfg.Bindings = cfg.Bindings[:1]
				binding := &cfg.Bindings[0]
				binding.OperationID, binding.Provider, binding.BillingAccount, binding.Region = test.operation, test.provider, "", ""
				binding.Profile = quotaProfileOpenAI
				if test.provider == modelapi.ProviderAnthropic {
					binding.Profile = quotaProfileAnthropic
				}
				delete(binding.Meters, "thought_tokens")
				delete(cfg.Tenants[0].Policy.Meters, "thought_tokens")
				book := &cfg.Tenants[0].Policy.PriceBooks[0]
				prices := book.Prices[:0]
				for _, price := range book.Prices {
					if price.MeterID != "thought_tokens" {
						prices = append(prices, price)
					}
				}
				book.Prices = prices
				book.Match.Provider, book.Match.BillingAccount, book.Match.Region = test.provider, "", ""
			})
			installProviderCredentials(t, f.handler)
			record, _ := f.resources.Get(context.Background(), "tenant", "model-surface", "surface")
			var google ModelConnection
			_ = json.Unmarshal(record.Spec, &google)
			connection := providerConnection(t, google.Surface.Origin, test.settings, test.protocol, test.auth, test.operation)
			if err := f.handler.validate(context.Background(), "tenant", "model-surface", "surface", providerSpecJSON(t, connection)); err != nil {
				t.Fatal("provider surface rejected:", err)
			}
			putProviderSurface(t, f.resources, connection)
			ledger := usageLedgerFor(t, f.handler)
			r := f.request("generateContent", "provider-attempt", test.body, false)
			r.URL.Path, r.URL.RawQuery = "/api/v1/llm/native/surface"+test.path, ""
			r.Header.Set("X-Api-Key", "caller-key-never-forwarded")
			r.Header.Set("Anthropic-Version", "2023-06-01")
			serve := f.handler.NativeModel
			if test.facade {
				r.URL.Path = "/api/v1/llm/gateway/surface" + test.path
				serve = f.handler.ClientGateway
			}
			w := httptest.NewRecorder()
			serve(w, r)
			if w.Code != 200 || w.Body.String() != test.response || f.calls.Load() != 1 {
				t.Fatalf("managed provider call failed %d %s calls=%d", w.Code, w.Body.String(), f.calls.Load())
			}
			if w.Header().Get("X-Ratelimit-Remaining-Tokens") != "" {
				t.Fatal("shared-key limit header leaked to caller")
			}
			upstream := seen[0]
			wantCredential := providerTestSecret
			if test.credentialHeader == "Authorization" {
				wantCredential = "Bearer " + providerTestSecret
			}
			if upstream.Get(test.credentialHeader) != wantCredential {
				t.Fatalf("tenant secret-reference credential not injected: %v", upstream)
			}
			for _, name := range []string{"Authorization", "X-Api-Key", "X-Goog-User-Project"} {
				if name != test.credentialHeader && upstream.Get(name) != "" {
					t.Fatalf("caller header %s crossed upstream", name)
				}
			}
			info := quotaHostModelAttempt(t, f, w.Header().Get("X-Aiplex-Attempt-ID"))
			if info.Decision.State != "settled" || info.Actual["usd"] != 21 || info.Actual["uncached_input_tokens"] != 5 || info.Actual["cached_tokens"] != 2 || info.Actual["output_tokens"] != 3 || info.Demand.Target.Provider != test.provider {
				t.Fatal("provider usage did not settle exactly", info.Decision.State, info.Actual, info.Demand.Target)
			}
			records := ledger()
			if len(records) != 1 {
				t.Fatalf("want one ledger record, got %d", len(records))
			}
			rec := records[0]
			if rec.Provider != test.provider || rec.Operation != test.operation || rec.Status != usageledger.StatusOK || rec.Usage == nil || *rec.Usage.InputTokens != 7 || *rec.Usage.CacheReadTokens != 2 || *rec.Usage.OutputTokens != 3 || rec.Cost.Basis != usageledger.BasisSettled {
				t.Fatalf("ledger record wrong: %+v usage=%+v", rec, rec.Usage)
			}
			wantLimits := test.provider != modelapi.ProviderAnthropic
			if (rec.ProviderLimits != nil) != wantLimits || (wantLimits && *rec.ProviderLimits.Tokens.Remaining != 999) {
				t.Fatalf("provider_limits on ledger record: %+v", rec.ProviderLimits)
			}
			raw, _ := json.Marshal(rec)
			if strings.Contains(string(raw), providerTestSecret) || strings.Contains(w.Body.String(), providerTestSecret) {
				t.Fatal("credential reached ledger or caller")
			}
			retry := httptest.NewRecorder()
			retryRequest := r.Clone(r.Context())
			retryRequest.Body = io.NopCloser(strings.NewReader(test.body))
			serve(retry, retryRequest)
			if retry.Code != 409 || f.calls.Load() != 1 {
				t.Fatal("same attempt redispatched", retry.Code, f.calls.Load())
			}
		})
	}
}

func providerSpecJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// bedrockTestFrame encodes one AWS event-stream message with string headers.
func bedrockTestFrame(event, payload string) []byte {
	var headers []byte
	for _, kv := range [][2]string{{":event-type", event}, {":content-type", "application/json"}, {":message-type", "event"}} {
		headers = append(headers, byte(len(kv[0])))
		headers = append(headers, kv[0]...)
		headers = append(headers, 7)
		headers = binary.BigEndian.AppendUint16(headers, uint16(len(kv[1])))
		headers = append(headers, kv[1]...)
	}
	total := 12 + len(headers) + len(payload) + 4
	out := binary.BigEndian.AppendUint32(nil, uint32(total))
	out = binary.BigEndian.AppendUint32(out, uint32(len(headers)))
	out = binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(out))
	out = append(append(out, headers...), payload...)
	return binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(out))
}

func usageLedgerFor(t *testing.T, h *ConnectedHandler) func() []usageledger.Record {
	t.Helper()
	store := usageledger.NewMemoryStore()
	h.Ledger = usageledger.NewRecorder(store, usageledger.WithErrorHandler(func(_ usageledger.Record, err error) { t.Errorf("ledger write failed: %v", err) }))
	return func() []usageledger.Record {
		h.Ledger.Flush()
		page, err := store.List(context.Background(), "tenant", usageledger.Query{Limit: usageledger.MaxLimit})
		if err != nil {
			t.Fatal(err)
		}
		return page.Records
	}
}

func TestManagedBedrockEventStreamCountsRequestAndRecordsAdvisoryUsage(t *testing.T) {
	frame := func(event, payload string) []byte {
		return bedrockTestFrame(event, payload)
	}
	var stream []byte
	for _, f := range [][2]string{{"messageStart", `{"role":"assistant"}`}, {"contentBlockDelta", `{"contentBlockIndex":0,"delta":{"text":"hi"}}`}, {"messageStop", `{"stopReason":"end_turn"}`}, {"metadata", `{"usage":{"inputTokens":13,"outputTokens":52,"totalTokens":65},"metrics":{"latencyMs":10}}`}} {
		stream = append(stream, frame(f[0], f[1])...)
	}
	var seenPath, seenAuth string
	f := quotaHostModelNew(t, false, func(w http.ResponseWriter, r *http.Request) {
		seenPath, seenAuth = r.URL.EscapedPath(), r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
		w.Header().Set("X-Amzn-Requestid", "aws-request")
		_, _ = w.Write(stream)
	}, func(cfg *QuotaHostConfig) {
		cfg.Bindings = cfg.Bindings[:1]
		binding := &cfg.Bindings[0]
		binding.OperationID, binding.Provider, binding.BillingAccount, binding.Region, binding.Model = "bedrock.converse-stream", modelapi.ProviderBedrock, "", "us-east-1", "model.v1:0"
	})
	installProviderCredentials(t, f.handler)
	record, _ := f.resources.Get(context.Background(), "tenant", "model-surface", "surface")
	var google ModelConnection
	_ = json.Unmarshal(record.Spec, &google)
	connection := providerConnection(t, google.Surface.Origin, modelapi.Provider{ID: modelapi.ProviderBedrock, Region: "us-east-1"}, "bedrock-converse", bearerRef, "bedrock.converse-stream")
	connection.Surface.Models = []string{"model.v1:0"}
	if err := f.handler.validate(context.Background(), "tenant", "model-surface", "surface", providerSpecJSON(t, connection)); err != nil {
		t.Fatal(err)
	}
	putProviderSurface(t, f.handler.Store, connection)
	ledger := usageLedgerFor(t, f.handler)
	access := &models.ResolvedAccess{Identity: *f.identity, Scopes: []string{"llm:surface:surface:*", "llm:model:model.v1:0"}}
	f.handler.ResolveAccess = func(context.Context, *models.WIFIdentity) (*models.ResolvedAccess, error) { return access, nil }
	r := f.request("generateContent", "bedrock-attempt", `{"messages":[{"role":"user","content":[{"text":"hi"}]}]}`, false)
	r = r.WithContext(context.WithValue(r.Context(), keyWIFAccess, access))
	// An unmodified AWS SDK escapes ':' in the model label.
	r.URL.Path, r.URL.RawPath, r.URL.RawQuery = "/api/v1/llm/gateway/surface/model/model.v1:0/converse-stream", "/api/v1/llm/gateway/surface/model/model.v1%3A0/converse-stream", ""
	w := httptest.NewRecorder()
	f.handler.ClientGateway(w, r)
	if w.Code != 200 || w.Body.String() != string(stream) || w.Header().Get("X-Amzn-Requestid") != "aws-request" {
		t.Fatalf("event stream not delivered: %d %q", w.Code, w.Body.String())
	}
	if seenPath != "/model/model.v1:0/converse-stream" || seenAuth != "Bearer "+providerTestSecret {
		t.Fatalf("wrong native dispatch %s %s", seenPath, seenAuth)
	}
	info := quotaHostModelAttempt(t, f, w.Header().Get("X-Aiplex-Attempt-ID"))
	if info.Decision.State != "settled" || info.Actual["requests"] != 1 || info.Demand.Target.Region != "us-east-1" {
		t.Fatal("request-count settlement missing", info.Decision.State, info.Actual)
	}
	records := ledger()
	if len(records) != 1 || !records[0].Streaming || records[0].Usage == nil || *records[0].Usage.InputTokens != 13 || *records[0].Usage.OutputTokens != 52 || records[0].Provider != modelapi.ProviderBedrock {
		t.Fatalf("ledger record: %+v", records)
	}
}

func TestBedrockIdentityProfileSignsWithSigV4ForBedrockService(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIDEXAMPLEOPERATOR")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "operator-secret-access-key")
	t.Setenv("AWS_SESSION_TOKEN", "")
	t.Setenv("AWS_CONFIG_FILE", "/nonexistent")
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", "/nonexistent")
	var seen http.Header
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"output":{},"stopReason":"end_turn","usage":{"inputTokens":1,"outputTokens":1,"totalTokens":2}}`)
	}))
	defer upstream.Close()
	runtime, err := connections.New(connections.Config{Network: endpointpolicy.Config{AllowedOrigins: []string{upstream.URL}, AllowLoopback: true}, Identities: []connections.Identity{{ID: "aws", TenantID: "tenant", Type: "aws-default", AllowedOrigins: []string{upstream.URL}}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	store := resources.NewMemoryStore()
	h := NewConnectedHandler(store, runtime, func(*http.Request) (string, error) { return "tenant", nil }, nil, true)
	connection := providerConnection(t, upstream.URL, modelapi.Provider{ID: modelapi.ProviderBedrock, Region: "us-east-1"}, "bedrock-converse", nil, "bedrock.converse")
	connection.IdentityRef = "aws"
	if err = h.validate(context.Background(), "tenant", "model-surface", "surface", providerSpecJSON(t, connection)); err != nil {
		t.Fatal(err)
	}
	putProviderSurface(t, store, connection)
	identity := &models.WIFIdentity{PrincipalID: "alice"}
	r := httptest.NewRequest("POST", "/api/v1/llm/native/surface/model/model-v1/converse", strings.NewReader(`{"messages":[]}`))
	r.SetPathValue("id", "surface")
	r.Header.Set("Authorization", "Bearer caller-aiplex-token")
	r.Header.Set("X-Amz-Security-Token", "caller-session")
	access := &models.ResolvedAccess{Identity: *identity, Scopes: []string{"llm:surface:surface:*", "llm:model:model-v1"}}
	r = r.WithContext(context.WithValue(context.WithValue(context.WithValue(r.Context(), keyWIFIdentity, identity), keyWIFAccess, access), keyRequestID, "sigv4"))
	w := httptest.NewRecorder()
	h.NativeModel(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	authorization := seen.Get("Authorization")
	if !strings.HasPrefix(authorization, "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLEOPERATOR/") || !strings.Contains(authorization, "/us-east-1/bedrock/aws4_request") || seen.Get("X-Amz-Date") == "" || seen.Get("X-Amz-Security-Token") != "" {
		t.Fatalf("not signed with the operator SigV4 identity: %v", seen)
	}
}

func TestProviderCapacityEndpointIsAdminAndTenantScoped(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "2")
		w.Header().Set("Anthropic-Ratelimit-Requests-Remaining", "0")
		w.WriteHeader(429)
		fmt.Fprint(w, `{"type":"error","error":{"type":"rate_limit_error"}}`)
	}))
	defer upstream.Close()
	runtime, err := connections.New(connections.Config{Network: endpointpolicy.Config{AllowedOrigins: []string{upstream.URL}, AllowLoopback: true}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	store := resources.NewMemoryStore()
	tenant := "tenant"
	h := NewConnectedHandler(store, runtime, func(*http.Request) (string, error) { return tenant, nil }, nil, true)
	installProviderCredentials(t, h)
	connection := providerConnection(t, upstream.URL, modelapi.Provider{ID: modelapi.ProviderAnthropic, AnthropicVersion: "2023-06-01"}, "anthropic-messages", anthropicRef, "anthropic.messages")
	if err = h.validate(context.Background(), "tenant", "model-surface", "surface", providerSpecJSON(t, connection)); err != nil {
		t.Fatal(err)
	}
	putProviderSurface(t, store, connection)
	ledger := usageLedgerFor(t, h)
	identity := &models.WIFIdentity{PrincipalID: "alice"}
	call := func(handler http.HandlerFunc, method, path, body string, roles []models.IAMRole) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.SetPathValue("id", "surface")
		access := &models.ResolvedAccess{Identity: *identity, Scopes: []string{"llm:surface:surface:*", "llm:model:model-v1"}, Roles: roles}
		r = r.WithContext(context.WithValue(context.WithValue(r.Context(), keyWIFIdentity, identity), keyWIFAccess, access))
		r = r.WithContext(context.WithValue(r.Context(), keyRequestID, "capacity-request"))
		w := httptest.NewRecorder()
		handler(w, r)
		return w
	}
	w := call(h.NativeModel, "POST", "/api/v1/llm/native/surface/v1/messages", `{"model":"model-v1","max_tokens":1,"messages":[]}`, nil)
	if w.Code != 429 || w.Header().Get("Retry-After") != "2" || w.Header().Get("Anthropic-Ratelimit-Requests-Remaining") != "" {
		t.Fatal("native 429 not delivered with only the backoff hint", w.Code, w.Header())
	}
	records := ledger()
	if len(records) != 1 || records[0].ProviderLimits == nil || records[0].ProviderLimits.Classification != "rate_limited" || *records[0].ProviderLimits.RetryAfterMS != 2000 || records[0].ErrorClass != "rate_limited" {
		t.Fatalf("ledger limits: %+v", records)
	}
	if denied := call(h.SurfaceCapacity, "GET", "/api/v1/llm/surfaces/surface/capacity", "", nil); denied.Code != 403 {
		t.Fatal("capacity readable without admin role", denied.Code)
	}
	got := call(h.SurfaceCapacity, "GET", "/api/v1/llm/surfaces/surface/capacity", "", []models.IAMRole{models.RoleAdmin})
	var body struct {
		Observed bool `json:"observed"`
		Limits   struct {
			Provider       string `json:"provider"`
			Classification string `json:"classification"`
			Requests       struct {
				Remaining *int64 `json:"remaining"`
			} `json:"requests"`
		} `json:"limits"`
	}
	if got.Code != 200 || json.Unmarshal(got.Body.Bytes(), &body) != nil || !body.Observed || body.Limits.Provider != "anthropic" || body.Limits.Classification != "rate_limited" || body.Limits.Requests.Remaining == nil || *body.Limits.Requests.Remaining != 0 {
		t.Fatalf("capacity: %d %s", got.Code, got.Body.String())
	}
	if signal, ok := h.CapacitySignal().LastObserved("tenant", "surface"); !ok || signal.Classification != "rate_limited" {
		t.Fatal("capacity signal contract missing observation")
	}
	tenant = "other-tenant"
	if other := call(h.SurfaceCapacity, "GET", "/api/v1/llm/surfaces/surface/capacity", "", []models.IAMRole{models.RoleAdmin}); other.Code != 404 {
		t.Fatal("another tenant read the surface capacity", other.Code, other.Body.String())
	}
}

func TestProviderModelSurfaceValidation(t *testing.T) {
	runtime, err := connections.New(connections.Config{Network: endpointpolicy.Config{AllowedOrigins: []string{"https://api.openai.com", "https://res.openai.azure.com", "https://bedrock-runtime.us-east-1.amazonaws.com", "https://api.anthropic.com"}}, Identities: []connections.Identity{
		{ID: "entra", TenantID: "tenant", Type: "azure-default", AllowedOrigins: []string{"https://res.openai.azure.com"}},
		{ID: "aws", TenantID: "tenant", Type: "aws-default", AllowedOrigins: []string{"https://bedrock-runtime.us-east-1.amazonaws.com"}},
		{ID: "google", TenantID: "tenant", Type: "gcp-adc", AllowedOrigins: []string{"https://api.openai.com"}},
		{ID: "narrow", TenantID: "tenant", Type: "azure-default", AllowedOrigins: []string{"https://other.openai.azure.com"}},
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	h := NewConnectedHandler(resources.NewMemoryStore(), runtime, nil, nil, false)
	installProviderCredentials(t, h)
	base := func(origin string, p modelapi.Provider, protocol string, auth *toolcredential.Auth, identity string, ids ...string) ModelConnection {
		c := providerConnection(t, origin, p, protocol, auth, ids...)
		c.IdentityRef = identity
		return c
	}
	openai := modelapi.Provider{ID: modelapi.ProviderOpenAI}
	anthropic := modelapi.Provider{ID: modelapi.ProviderAnthropic, AnthropicVersion: "2023-06-01"}
	azure := modelapi.Provider{ID: modelapi.ProviderAzureOpenAI}
	bedrock := modelapi.Provider{ID: modelapi.ProviderBedrock, Region: "us-east-1"}
	missing := &toolcredential.Auth{Type: toolcredential.Bearer, SecretRef: "missing-key"}
	tests := []struct {
		name  string
		spec  ModelConnection
		valid bool
	}{
		{"openai secret ref", base("https://api.openai.com", openai, "openai-chat", bearerRef, "", "openai.v1.chat-completions"), true},
		{"anthropic x-api-key", base("https://api.anthropic.com", anthropic, "anthropic-messages", anthropicRef, "", "anthropic.messages"), true},
		{"azure api-key", base("https://res.openai.azure.com", azure, "openai-chat", azureRef, "", "azure.v1.chat-completions"), true},
		{"azure entra", base("https://res.openai.azure.com", azure, "openai-responses", nil, "entra", "azure.v1.responses"), true},
		{"bedrock sigv4", base("https://bedrock-runtime.us-east-1.amazonaws.com", bedrock, "bedrock-converse", nil, "aws", "bedrock.converse"), true},
		{"bedrock api key", base("https://bedrock-runtime.us-east-1.amazonaws.com", bedrock, "bedrock-converse", bearerRef, "", "bedrock.converse"), true},
		{"missing secret", base("https://api.openai.com", openai, "openai-chat", missing, "", "openai.v1.chat-completions"), false},
		{"anthropic bearer", base("https://api.anthropic.com", anthropic, "anthropic-messages", bearerRef, "", "anthropic.messages"), false},
		{"azure wrong header", base("https://res.openai.azure.com", azure, "openai-chat", anthropicRef, "", "azure.v1.chat-completions"), false},
		{"both credentials", base("https://api.openai.com", openai, "openai-chat", bearerRef, "entra", "openai.v1.chat-completions"), false},
		{"no credential", base("https://api.openai.com", openai, "openai-chat", nil, "", "openai.v1.chat-completions"), false},
		{"google identity to openai", base("https://api.openai.com", openai, "openai-chat", nil, "google", "openai.v1.chat-completions"), false},
		{"entra origin not approved", base("https://res.openai.azure.com", azure, "openai-chat", nil, "narrow", "azure.v1.chat-completions"), false},
		{"origin outside policy", base("https://api.mistral.ai", modelapi.Provider{ID: modelapi.ProviderOpenAICompatible}, "openai-chat", bearerRef, "", "compat.chat-completions"), false},
	}
	for _, test := range tests {
		err := h.validate(context.Background(), "tenant", "model-surface", "surface", providerSpecJSON(t, test.spec))
		if (err == nil) != test.valid {
			t.Fatalf("%s: valid=%t err=%v", test.name, test.valid, err)
		}
	}
	billing := base("https://api.openai.com", openai, "openai-chat", bearerRef, "", "openai.v1.chat-completions")
	billing.BillingProject = "p"
	if h.validate(context.Background(), "tenant", "model-surface", "surface", providerSpecJSON(t, billing)) == nil {
		t.Fatal("billing project accepted on provider surface")
	}
	ops, _ := modelapi.SelectOperations("models.generateContent")
	google := ModelConnection{Surface: modelapi.Surface{ID: "surface", Protocol: "google-genai", Origin: "https://api.openai.com", APIVersion: "v1", Project: "p", Location: "us-central1", Publisher: "google", Models: []string{"m"}, Operations: ops}, IdentityRef: "google", Auth: bearerRef}
	if h.validate(context.Background(), "tenant", "model-surface", "surface", providerSpecJSON(t, google)) == nil {
		t.Fatal("auth accepted on a Google surface")
	}
}

func TestProviderClientFacadePaths(t *testing.T) {
	surface := func(p modelapi.Provider, protocol string, ids ...string) modelapi.Surface {
		ops, err := modelapi.SelectProviderOperations(p.ID, ids...)
		if err != nil {
			t.Fatal(err)
		}
		return modelapi.Surface{ID: "s", Protocol: protocol, Origin: "https://api.example.com", Models: []string{"m"}, Operations: ops, Provider: &p}
	}
	tests := []struct {
		s          modelapi.Surface
		in, want   string
		shouldFail bool
	}{
		{surface(modelapi.Provider{ID: modelapi.ProviderOpenAI}, "openai-chat", "openai.v1.chat-completions"), "/chat/completions", "/v1/chat/completions", false},
		{surface(modelapi.Provider{ID: modelapi.ProviderOpenAI}, "openai-chat", "openai.v1.chat-completions"), "/v1/responses", "/v1/responses", false},
		{surface(modelapi.Provider{ID: modelapi.ProviderAnthropic, AnthropicVersion: "2023-06-01"}, "anthropic-messages", "anthropic.messages"), "/v1/messages", "/v1/messages", false},
		{surface(modelapi.Provider{ID: modelapi.ProviderAzureOpenAI}, "openai-chat", "azure.v1.chat-completions"), "/openai/v1/chat/completions", "/openai/v1/chat/completions", false},
		{surface(modelapi.Provider{ID: modelapi.ProviderAzureOpenAI, APIVersion: "2024-10-21"}, "openai-chat", "azure.deployments.chat-completions"), "/openai/deployments/m/chat/completions", "/openai/deployments/m/chat/completions", false},
		{surface(modelapi.Provider{ID: modelapi.ProviderBedrock, Region: "us-east-1"}, "bedrock-converse", "bedrock.converse"), "/model/m.v1:0/converse", "/model/m.v1:0/converse", false},
		{surface(modelapi.Provider{ID: modelapi.ProviderOpenAICompatible, BasePath: "/openai/v1"}, "openai-chat", "compat.chat-completions"), "/chat/completions", "/openai/v1/chat/completions", false},
		{surface(modelapi.Provider{ID: modelapi.ProviderOpenAICompatible}, "openai-chat", "compat.chat-completions"), "/v1/chat/completions", "/v1/chat/completions", false},
		{surface(modelapi.Provider{ID: modelapi.ProviderOpenAI}, "openai-chat", "openai.v1.chat-completions"), "/v1/embeddings", "", true},
		{surface(modelapi.Provider{ID: modelapi.ProviderBedrock, Region: "us-east-1"}, "bedrock-converse", "bedrock.converse"), "/model/a/b/converse", "", true},
	}
	for _, test := range tests {
		r := httptest.NewRequest("POST", "/x", strings.NewReader(`{}`))
		got, body, err := clientNativePath(test.s, r, test.in)
		if (err != nil) != test.shouldFail || got != test.want || body != nil {
			t.Fatalf("%s %s -> %q %v", test.s.Provider.ID, test.in, got, err)
		}
	}
	ready := surface(modelapi.Provider{ID: modelapi.ProviderOpenAI}, "openai-responses", "openai.v1.chat-completions")
	if clientSurfaceReady(ready) {
		t.Fatal("responses protocol ready without a responses operation")
	}
	ready.Protocol = "openai-chat"
	if !clientSurfaceReady(ready) {
		t.Fatal("chat surface not ready")
	}
}
