package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"
	zlog "github.com/rs/zerolog/log"
	"github.com/vamsiramakrishnan/aiplex/internal/auth"
	"github.com/vamsiramakrishnan/aiplex/internal/connections"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
	"github.com/vamsiramakrishnan/aiplex/pkg/endpointpolicy"
	"github.com/vamsiramakrishnan/aiplex/pkg/modelapi"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
	"github.com/vamsiramakrishnan/aiplex/pkg/toolcredential"
)

const (
	mantleAccessKeyID = "AKIDMANTLEOPERATOR"
	mantleSecretKey   = "mantle-operator-secret-access-key"
)

var mantleKeyRef = &toolcredential.Auth{Type: toolcredential.APIKey, In: toolcredential.InHeader, Name: "x-api-key", SecretRef: "provider-key"}

type mantleFixture struct {
	h        *ConnectedHandler
	store    resources.Store
	upstream *httptest.Server
	mu       sync.Mutex
	seen     []http.Header
	logs     *lockedLog
	resolves atomic.Int64
}

// newMantleFixture serves a Bedrock Mantle surface against a fake Mantle
// upstream, signed by the operator's aws-default identity (SigV4), or, with
// auth, carrying the surface's secret-reference API key.
func newMantleFixture(t *testing.T, accessKey, secret string, auth ...*toolcredential.Auth) *mantleFixture {
	t.Helper()
	t.Setenv("AWS_ACCESS_KEY_ID", accessKey)
	t.Setenv("AWS_SECRET_ACCESS_KEY", secret)
	t.Setenv("AWS_SESSION_TOKEN", "")
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_CONFIG_FILE", "/nonexistent")
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", "/nonexistent")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	t.Setenv("AWS_CONTAINER_CREDENTIALS_FULL_URI", "")
	t.Setenv("AWS_CONTAINER_CREDENTIALS_RELATIVE_URI", "")
	t.Setenv("AWS_WEB_IDENTITY_TOKEN_FILE", "")
	f := &mantleFixture{logs: &lockedLog{}}
	prevWriter, prevSlog := log.Writer(), slog.Default()
	log.SetOutput(f.logs)
	slog.SetDefault(slog.New(slog.NewTextHandler(f.logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { log.SetOutput(prevWriter); slog.SetDefault(prevSlog) })
	f.upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.seen = append(f.seen, r.Header.Clone())
		f.mu.Unlock()
		if r.URL.Path != "/anthropic/v1/messages" || r.URL.RawQuery != "" {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"msg_1","type":"message","role":"assistant","model":"anthropic.model-v1","content":[],"stop_reason":"end_turn","usage":{"input_tokens":7,"cache_read_input_tokens":2,"cache_creation_input_tokens":3,"cache_creation":{"ephemeral_5m_input_tokens":1,"ephemeral_1h_input_tokens":2},"output_tokens":4}}`)
	}))
	t.Cleanup(f.upstream.Close)
	runtime, err := connections.New(connections.Config{Network: endpointpolicy.Config{AllowedOrigins: []string{f.upstream.URL}, AllowLoopback: true}, Identities: []connections.Identity{{ID: "aws", TenantID: "tenant", Type: "aws-default", AllowedOrigins: []string{f.upstream.URL}}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.store = resources.NewMemoryStore()
	f.h = NewConnectedHandler(f.store, runtime, func(*http.Request) (string, error) { return "tenant", nil }, nil, true)
	connection := providerConnection(t, f.upstream.URL, modelapi.Provider{ID: modelapi.ProviderBedrock, Region: "us-east-1"}, modelapi.ProtocolAnthropicMessages, nil, modelapi.BedrockMantleMessagesOperation)
	connection.Surface.Models = []string{"anthropic.model-v1"}
	connection.IdentityRef = "aws"
	if len(auth) == 1 {
		provider, err := toolcredential.NewProvider(toolcredential.Config{HTTPClient: f.h.Runtime.HTTP, Policy: f.h.Runtime.Policy, Secrets: countingSecrets{&f.resolves}})
		if err != nil {
			t.Fatal(err)
		}
		f.h.ToolCredentials = provider
		connection.IdentityRef, connection.Auth = "", auth[0]
	}
	if err = f.h.validate(context.Background(), "tenant", "model-surface", "surface", providerSpecJSON(t, connection)); err != nil {
		t.Fatal(err)
	}
	putProviderSurface(t, f.store, connection)
	return f
}

func (f *mantleFixture) call(path string, headers ...string) *httptest.ResponseRecorder {
	identity := &models.WIFIdentity{PrincipalID: "alice"}
	r := httptest.NewRequest("POST", path, strings.NewReader(`{"model":"anthropic.model-v1","max_tokens":8,"messages":[]}`))
	r.SetPathValue("id", "surface")
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer caller-aiplex-token")
	r.Header.Set("X-Amz-Security-Token", "caller-session")
	for i := 0; i+1 < len(headers); i += 2 {
		r.Header.Set(headers[i], headers[i+1])
	}
	access := &models.ResolvedAccess{Identity: *identity, Scopes: []string{"llm:surface:surface:*", "llm:model:anthropic.model-v1"}}
	r = r.WithContext(context.WithValue(context.WithValue(context.WithValue(r.Context(), keyWIFIdentity, identity), keyWIFAccess, access), keyRequestID, "mantle"))
	w := httptest.NewRecorder()
	if strings.HasPrefix(path, "/api/v1/llm/gateway/") {
		f.h.ClientGateway(w, r)
	} else {
		f.h.NativeModel(w, r)
	}
	return w
}

func TestBedrockMantleSignsForMantleServiceWithoutLeakingKeys(t *testing.T) {
	f := newMantleFixture(t, mantleAccessKeyID, mantleSecretKey)
	ledger := usageLedgerFor(t, f.h)
	for _, path := range []string{"/api/v1/llm/native/surface/anthropic/v1/messages", "/api/v1/llm/gateway/surface/anthropic/v1/messages", "/api/v1/llm/gateway/surface/v1/messages"} {
		w := f.call(path)
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), mantleSecretKey) || strings.Contains(w.Body.String(), mantleAccessKeyID) {
			t.Fatal("key material in the response")
		}
	}
	f.mu.Lock()
	seen := f.seen
	f.mu.Unlock()
	if len(seen) != 3 {
		t.Fatalf("upstream calls %d", len(seen))
	}
	for _, headers := range seen {
		authorization := headers.Get("Authorization")
		if !strings.HasPrefix(authorization, "AWS4-HMAC-SHA256 Credential="+mantleAccessKeyID+"/") || !strings.Contains(authorization, "/us-east-1/bedrock-mantle/aws4_request") || headers.Get("X-Amz-Date") == "" || headers.Get("X-Amz-Security-Token") != "" {
			t.Fatalf("not signed for bedrock-mantle: %v", headers)
		}
		if headers.Get("Anthropic-Version") != modelapi.BedrockMantleAnthropicVersion || strings.Contains(authorization, "caller") {
			t.Fatalf("upstream headers %v", headers)
		}
	}
	records := ledger()
	if len(records) != 3 {
		t.Fatalf("ledger records %d", len(records))
	}
	raw, _ := json.Marshal(records)
	if strings.Contains(string(raw), mantleSecretKey) || strings.Contains(string(raw), mantleAccessKeyID) {
		t.Fatal("key material in the usage ledger")
	}
	r := records[0]
	if r.Provider != modelapi.ProviderBedrock || r.Usage == nil || r.Usage.OutputTokens == nil || *r.Usage.OutputTokens != 4 || r.Usage.CacheReadTokens == nil || *r.Usage.CacheReadTokens != 2 || r.Usage.CacheWrite5mTokens == nil || *r.Usage.CacheWrite5mTokens != 1 || *r.Usage.CacheWrite1hTokens != 2 {
		raw, _ := json.Marshal(r)
		t.Fatalf("ledger usage %s", raw)
	}
	if logs := f.logs.String(); strings.Contains(logs, mantleSecretKey) || strings.Contains(logs, mantleAccessKeyID) {
		t.Fatal("key material logged")
	}
}

// Without credentials the request is refused and never sent unsigned.
func TestBedrockMantleRefusesWithoutCredentials(t *testing.T) {
	f := newMantleFixture(t, "", "")
	ledger := usageLedgerFor(t, f.h)
	w := f.call("/api/v1/llm/native/surface/anthropic/v1/messages")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing credentials: %d %s", w.Code, w.Body.String())
	}
	f.mu.Lock()
	calls := len(f.seen)
	f.mu.Unlock()
	if calls != 0 {
		t.Fatal("request sent without a signature")
	}
	raw, _ := json.Marshal(ledger())
	if strings.Contains(string(raw), "caller-aiplex-token") || strings.Contains(f.logs.String(), "caller-aiplex-token") {
		t.Fatal("caller token recorded")
	}
}

func TestBedrockMantleCredentialsAndProfile(t *testing.T) {
	ops := func(ids ...string) []modelapi.Operation {
		out, err := modelapi.SelectProviderOperations(modelapi.ProviderBedrock, ids...)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	bedrock := &modelapi.Provider{ID: modelapi.ProviderBedrock, Region: "us-east-1"}
	mantle := modelapi.Surface{ID: "s", Protocol: modelapi.ProtocolAnthropicMessages, Origin: "https://bedrock-mantle.us-east-1.api.aws", Models: []string{"anthropic.m"}, Operations: ops(modelapi.BedrockMantleMessagesOperation), Provider: bedrock}
	runtime := modelapi.Surface{ID: "s", Protocol: modelapi.ProtocolBedrockConverse, Origin: "https://bedrock-runtime.us-east-1.amazonaws.com", Models: []string{"anthropic.m"}, Operations: ops("bedrock.converse"), Provider: bedrock}
	for _, test := range []struct {
		name    string
		surface modelapi.Surface
		auth    toolcredential.Auth
		ok      bool
	}{
		{"mantle API key in x-api-key", mantle, *mantleKeyRef, true},
		{"mantle bearer is not documented", mantle, *bearerRef, false},
		{"mantle API key with a prefix", mantle, toolcredential.Auth{Type: toolcredential.APIKey, In: toolcredential.InHeader, Name: "x-api-key", Prefix: "Bearer", SecretRef: "provider-key"}, false},
		{"runtime bearer", runtime, *bearerRef, true},
		{"runtime x-api-key", runtime, *mantleKeyRef, false},
	} {
		if err := providerAuthAllowed(test.surface, test.auth); (err == nil) != test.ok {
			t.Errorf("%s: %v", test.name, err)
		}
	}
	if !quotaProfileOperation(QuotaBinding{Profile: quotaProfileAnthropic, Provider: modelapi.ProviderBedrock, OperationID: modelapi.BedrockMantleMessagesOperation}) {
		t.Fatal("anthropic-text does not bind Mantle Messages")
	}
	if quotaProfileOperation(QuotaBinding{Profile: quotaProfileAnthropic, Provider: modelapi.ProviderOpenAI, OperationID: modelapi.BedrockMantleMessagesOperation}) {
		t.Fatal("anthropic-text binds Mantle Messages for another provider")
	}
	if err := accessPointWireServes(mantle, WireAnthropic, "anthropic.m"); err != nil {
		t.Fatalf("access point Messages wire: %v", err)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/v1/llm/gateway/s/anthropic/v1/messages", nil)
	if model, kind, _ := nativeKeyRoute(r); !model || kind != nativeKeyAnthropic {
		t.Fatalf("x-api-key not accepted on the facade Mantle path (model=%t kind=%v)", model, kind)
	}
	// A runtime surface does not answer the Mantle paths.
	if _, _, err := clientNativePath(runtime, httptest.NewRequest(http.MethodPost, "/", nil), "/anthropic/v1/messages"); err == nil {
		t.Fatal("Mantle path mapped onto a Bedrock Runtime surface")
	}
}

// Caller SigV4 material and credentials never reach the upstream or change
// the signature: the outbound header set is built from an allowlist
// (providerRequestHeaders copies only Content-Type, Accept and User-Agent,
// plus the pinned anthropic-version) and the credential is applied last.
func TestBedrockMantleIgnoresCallerSigningHeaders(t *testing.T) {
	f := newMantleFixture(t, mantleAccessKeyID, mantleSecretKey)
	if w := f.call("/api/v1/llm/native/surface/anthropic/v1/messages"); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	forged := []string{"X-Amz-Date", "19990101T000000Z", "X-Amz-Credential", "AKIDCALLER/19990101/us-east-1/bedrock-mantle/aws4_request", "X-Amz-Content-Sha256", "caller-content-sha", "X-Amz-Security-Token", "caller-session-token", "Authorization", "AWS4-HMAC-SHA256 Credential=AKIDCALLER/19990101/us-east-1/bedrock-mantle/aws4_request, SignedHeaders=host, Signature=caller"}
	if w := f.call("/api/v1/llm/native/surface/anthropic/v1/messages", forged...); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.seen) != 2 {
		t.Fatalf("upstream calls %d", len(f.seen))
	}
	signedHeaders := func(h http.Header) string {
		authorization := h.Get("Authorization")
		i := strings.Index(authorization, "SignedHeaders=")
		if i < 0 {
			return ""
		}
		return strings.SplitN(authorization[i:], ",", 2)[0]
	}
	plain, withForged := f.seen[0], f.seen[1]
	for name, values := range withForged {
		for _, value := range values {
			if strings.Contains(value, "caller") || strings.Contains(value, "AKIDCALLER") || strings.Contains(value, "19990101") {
				t.Fatalf("caller value reached the upstream in %s: %q", name, value)
			}
		}
	}
	if withForged.Get("X-Amz-Credential") != "" || withForged.Get("X-Amz-Security-Token") != "" || !strings.HasPrefix(withForged.Get("Authorization"), "AWS4-HMAC-SHA256 Credential="+mantleAccessKeyID+"/") {
		t.Fatalf("upstream headers %v", withForged)
	}
	if signedHeaders(plain) == "" || signedHeaders(plain) != signedHeaders(withForged) {
		t.Fatalf("caller headers changed what is signed: %q vs %q", signedHeaders(plain), signedHeaders(withForged))
	}
}

// mantleChain serves the client gateway behind the production-order
// middleware (request id, logger, native key adapter, WIF identity).
func mantleChain(t *testing.T, f *mantleFixture) (http.Handler, *bytes.Buffer) {
	t.Helper()
	store := registry.NewMemoryStore()
	if err := store.PutRoleBinding(context.Background(), &models.RoleBinding{ID: "eng", Group: "platform-eng", Role: models.RoleAdmin, Scopes: []string{"llm:surface:surface:*", "llm:model:anthropic.model-v1"}}); err != nil {
		t.Fatal(err)
	}
	validator := auth.NewWIFValidator(store, auth.WIFConfig{Verifier: nativeKeyVerifier{}, TrustedIssuers: []string{apIssuer}, WorkforcePoolID: "people", IsWorkforce: true})
	f.h.ResolveAccess = validator.SyncUserScopes
	logs := &bytes.Buffer{}
	previous := zlog.Logger
	zlog.Logger = zerolog.New(logs)
	t.Cleanup(func() { zlog.Logger = previous })
	outer := chi.NewRouter()
	outer.Use(RequestID, Logger, NativeKeyHeaders, WIFAuthMode(validator, false), AuditLog)
	outer.Route("/api/v1", func(r chi.Router) {
		r.Use(RequireIdentity(false))
		r.Handle("/llm/gateway/{id}/*", http.HandlerFunc(f.h.ClientGateway))
	})
	return outer, logs
}

// API-key mode end to end: the caller's AIPlex token in x-api-key
// authenticates the caller; the upstream receives exactly the surface's key
// in x-api-key and never the caller's token.
func TestBedrockMantleAPIKeyModeThroughTheMiddleware(t *testing.T) {
	f := newMantleFixture(t, "", "", mantleKeyRef)
	ledger := usageLedgerFor(t, f.h)
	chain, zlogs := mantleChain(t, f)
	w := nativeKeyDo(chain, "POST", "/api/v1/llm/gateway/surface/anthropic/v1/messages", `{"model":"anthropic.model-v1","max_tokens":8,"messages":[]}`, "Content-Type", "application/json", "Anthropic-Version", "2023-06-01", "X-Api-Key", nativeKeyToken)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	f.mu.Lock()
	seen := f.seen
	f.mu.Unlock()
	if len(seen) != 1 {
		t.Fatalf("upstream calls %d", len(seen))
	}
	if got := seen[0].Values("X-Api-Key"); len(got) != 1 || got[0] != providerTestSecret || seen[0].Get("Authorization") != "" {
		t.Fatalf("upstream credential: x-api-key %q authorization %q", got, seen[0].Get("Authorization"))
	}
	for name, values := range seen[0] {
		for _, value := range values {
			if strings.Contains(value, nativeKeyToken) {
				t.Fatalf("caller token reached the upstream in %s", name)
			}
		}
	}
	records := ledger()
	raw, _ := json.Marshal(records)
	if len(records) != 1 {
		t.Fatalf("ledger records %d", len(records))
	}
	for _, secret := range []string{nativeKeyToken, providerTestSecret} {
		if strings.Contains(string(raw), secret) || strings.Contains(w.Body.String(), secret) || strings.Contains(f.logs.String(), secret) || strings.Contains(zlogs.String(), secret) {
			t.Fatal("caller token or surface key in the ledger, response or logs")
		}
	}
}

// No credential: 401 from the middleware, nothing resolved or signed, and the
// upstream sees nothing.
func TestBedrockMantleUnauthenticatedCallerIsRefused(t *testing.T) {
	f := newMantleFixture(t, "", "", mantleKeyRef)
	chain, _ := mantleChain(t, f)
	w := nativeKeyDo(chain, "POST", "/api/v1/llm/gateway/surface/anthropic/v1/messages", `{"model":"anthropic.model-v1","max_tokens":8,"messages":[]}`, "Content-Type", "application/json", "Anthropic-Version", "2023-06-01")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	f.mu.Lock()
	calls := len(f.seen)
	f.mu.Unlock()
	if calls != 0 || f.resolves.Load() != 0 {
		t.Fatalf("upstream calls %d, key resolutions %d", calls, f.resolves.Load())
	}
}
