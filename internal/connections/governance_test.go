package connections

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/pkg/endpointpolicy"
	"github.com/vamsiramakrishnan/aiplex/pkg/nativeapi"
)

const graphNativeTestTenant = "11111111-1111-1111-1111-111111111111"
const graphNativeTestBlueprint = "22222222-2222-2222-2222-222222222222"
const graphNativeTestApp = "33333333-3333-3333-3333-333333333333"
const graphNativeTestOptions = `{"tenant_id":"` + graphNativeTestTenant + `","api_version":"beta","allowed_blueprints":[{"object_id":"` + graphNativeTestBlueprint + `","app_id":"` + graphNativeTestApp + `"}]}`

func nativeConnectionRuntime(t *testing.T, origin string, additionalOrigins ...string) *Runtime {
	t.Helper()
	origins := append([]string{origin}, additionalOrigins...)
	runtime, err := New(Config{
		Network:    endpointpolicy.Config{AllowedOrigins: origins, AllowLoopback: true},
		Identities: []Identity{{ID: "operator-native", TenantID: "app-tenant", Type: "bearer-env", TokenEnv: "AIPLEX_NATIVE_CONNECTION_TEST_TOKEN", AllowedOrigins: []string{origin}}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return runtime
}

func graphNativeProvider(origin string) GovernanceProvider {
	return GovernanceProvider{ID: "entra-native", TenantID: "app-tenant", Type: "entra-agent-id", Endpoint: origin, IdentityRef: "operator-native", Options: json.RawMessage(graphNativeTestOptions)}
}

func graphNativeBlueprintOperation() nativeapi.Operation {
	return nativeapi.Operation{Name: "blueprint.get", Resource: graphNativeTestBlueprint, Payload: json.RawMessage(`{"tenant_id":"` + graphNativeTestTenant + `"}`)}
}

func TestGovernanceGraphNativeAuthorityBeforeOperations(t *testing.T) {
	// The credential deliberately is opaque. Only Graph may interpret it.
	t.Setenv("AIPLEX_NATIVE_CONNECTION_TEST_TOKEN", "opaque-native-token-v1")
	var authorityCalls, nativeCalls atomic.Int32
	var authorityTenant atomic.Value
	authorityTenant.Store("99999999-9999-9999-9999-999999999999")
	var mu sync.Mutex
	var received []string
	var pinnedOrigin string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		received = append(received, r.Header.Get("Authorization"))
		mu.Unlock()
		if r.Method != http.MethodGet || "http://"+r.Host != pinnedOrigin {
			t.Errorf("native Graph request escaped reviewed endpoint: %s %s", r.Method, r.Host)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1.0/organization":
			authorityCalls.Add(1)
			if r.URL.Query().Get("$select") != "id" || len(r.URL.Query()) != 1 {
				t.Errorf("authority attestation did not use exact organization selector: %q", r.URL.RawQuery)
			}
			_, _ = fmt.Fprintf(w, `{"value":[{"id":%q}]}`, authorityTenant.Load().(string))
		case "/beta/applications/" + graphNativeTestBlueprint + "/microsoft.graph.agentIdentityBlueprint":
			nativeCalls.Add(1)
			_, _ = fmt.Fprintf(w, `{"id":%q,"appId":%q}`, graphNativeTestBlueprint, graphNativeTestApp)
		default:
			t.Errorf("unreviewed native Graph path %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer upstream.Close()
	pinnedOrigin = upstream.URL
	runtime := nativeConnectionRuntime(t, upstream.URL)
	driver, fingerprint, err := runtime.GovernanceDriver(graphNativeProvider(upstream.URL))
	if err != nil || len(fingerprint) != 64 {
		t.Fatalf("Graph host driver construction: %v fingerprint=%q", err, fingerprint)
	}
	if authorityCalls.Load() != 0 || nativeCalls.Load() != 0 {
		t.Fatal("driver construction performed native side effects")
	}
	wrongTenant := graphNativeBlueprintOperation()
	wrongTenant.Payload = json.RawMessage(`{"tenant_id":"99999999-9999-9999-9999-999999999999"}`)
	wrongBlueprint := graphNativeBlueprintOperation()
	wrongBlueprint.Resource = "44444444-4444-4444-4444-444444444444"
	for _, invalid := range []nativeapi.Operation{wrongTenant, wrongBlueprint} {
		if _, err = driver.Execute(context.Background(), invalid); err == nil || authorityCalls.Load() != 0 || nativeCalls.Load() != 0 {
			t.Fatalf("caller crossed native authority before pure validation: err=%v authority=%d native=%d", err, authorityCalls.Load(), nativeCalls.Load())
		}
	}
	if _, err = driver.Execute(context.Background(), graphNativeBlueprintOperation()); err == nil || strings.Contains(err.Error(), "opaque-native-token") || authorityCalls.Load() != 1 || nativeCalls.Load() != 0 {
		t.Fatalf("wrong native tenant reached operation or disclosed credential: err=%v authority=%d native=%d", err, authorityCalls.Load(), nativeCalls.Load())
	}
	authorityTenant.Store(graphNativeTestTenant)
	for i := 0; i < 2; i++ {
		result, executeErr := driver.Execute(context.Background(), graphNativeBlueprintOperation())
		if executeErr != nil || result.StatusCode != http.StatusOK || authorityCalls.Load() != 2 || nativeCalls.Load() != int32(i+1) {
			t.Fatalf("opaque Graph credential, retry after failed attestation, or attestation cache failed: err=%v authority=%d native=%d", executeErr, authorityCalls.Load(), nativeCalls.Load())
		}
	}
	// Token rotation must recheck the authority before any native CRUD/read.
	t.Setenv("AIPLEX_NATIVE_CONNECTION_TEST_TOKEN", "opaque-native-token-v2")
	authorityTenant.Store("99999999-9999-9999-9999-999999999999")
	if _, err = driver.Execute(context.Background(), graphNativeBlueprintOperation()); err == nil || authorityCalls.Load() != 3 || nativeCalls.Load() != 2 {
		t.Fatalf("rotated credential inherited previous authority: err=%v authority=%d native=%d", err, authorityCalls.Load(), nativeCalls.Load())
	}
	mu.Lock()
	defer mu.Unlock()
	want := []string{"Bearer opaque-native-token-v1", "Bearer opaque-native-token-v1", "Bearer opaque-native-token-v1", "Bearer opaque-native-token-v1", "Bearer opaque-native-token-v2"}
	if len(received) != len(want) {
		t.Fatalf("unexpected credential destination request count: %d", len(received))
	}
	for i := range want {
		if received[i] != want[i] {
			t.Fatalf("authority attestation and operation used different credentials at request %d", i)
		}
	}
}

func TestGovernanceGraphAuthorityDoesNotFollowRedirectOrAmbiguousOrganization(t *testing.T) {
	var escapedCalls atomic.Int32
	escaped := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		escapedCalls.Add(1)
		_, _ = io.WriteString(w, `{"value":[{"id":"`+graphNativeTestTenant+`"}]}`)
	}))
	defer escaped.Close()
	for _, response := range []string{"redirect", `{"value":[]}`, `{"value":[{"id":"` + graphNativeTestTenant + `"},{"id":"` + graphNativeTestTenant + `"}]}`, `{"value":[{}]}`} {
		t.Run(response, func(t *testing.T) {
			t.Setenv("AIPLEX_NATIVE_CONNECTION_TEST_TOKEN", "opaque-guard-test-token")
			var nativeCalls, authorityCalls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1.0/organization" {
					nativeCalls.Add(1)
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				authorityCalls.Add(1)
				if response == "redirect" {
					http.Redirect(w, r, escaped.URL+"/v1.0/organization", http.StatusTemporaryRedirect)
					return
				}
				_, _ = io.WriteString(w, response)
			}))
			defer upstream.Close()
			runtime := nativeConnectionRuntime(t, upstream.URL, escaped.URL)
			driver, _, err := runtime.GovernanceDriver(graphNativeProvider(upstream.URL))
			if err != nil {
				t.Fatal(err)
			}
			if _, err = driver.Execute(context.Background(), graphNativeBlueprintOperation()); err == nil || nativeCalls.Load() != 0 || authorityCalls.Load() != 1 || escapedCalls.Load() != 0 {
				t.Fatalf("unattested native authority accepted: err=%v authority=%d native=%d redirected=%d", err, authorityCalls.Load(), nativeCalls.Load(), escapedCalls.Load())
			}
		})
	}
}

func TestGovernanceHostRejectsUnboundCredentialsAndNativeOptionsBeforeHTTP(t *testing.T) {
	t.Setenv("AIPLEX_NATIVE_CONNECTION_TEST_TOKEN", "operator-test-token")
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = io.WriteString(w, `{}`)
	}))
	defer upstream.Close()
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = io.WriteString(w, `{}`)
	}))
	defer other.Close()
	runtime := nativeConnectionRuntime(t, upstream.URL, other.URL)
	valid := GovernanceProvider{ID: "google-native", TenantID: "app-tenant", Type: "google-agent-registry", Endpoint: upstream.URL, IdentityRef: "operator-native", Options: json.RawMessage(`{"project":"project-a","location":"global"}`)}
	for _, change := range []struct {
		name  string
		apply func(*GovernanceProvider)
	}{
		{"cross tenant", func(p *GovernanceProvider) { p.TenantID = "other-app-tenant" }},
		{"wrong identity", func(p *GovernanceProvider) { p.IdentityRef = "missing" }},
		{"globally allowed but not credential destination", func(p *GovernanceProvider) { p.Endpoint = other.URL }},
		{"unknown options", func(p *GovernanceProvider) {
			p.Options = json.RawMessage(`{"project":"project-a","location":"global","endpoint":"https://caller.example"}`)
		}},
		{"invalid native authority", func(p *GovernanceProvider) {
			p.Options = json.RawMessage(`{"project":"../foreign","location":"global"}`)
		}},
		{"unsupported provider", func(p *GovernanceProvider) { p.Type = "caller-custom-proxy" }},
		{"AgentCore bearer instead of SigV4", func(p *GovernanceProvider) { p.Type = "aws-agentcore-identity"; p.Region = "us-east-1" }},
	} {
		t.Run(change.name, func(t *testing.T) {
			provider := valid
			change.apply(&provider)
			if _, _, err := runtime.GovernanceDriver(provider); err == nil || calls.Load() != 0 {
				t.Fatalf("unreviewed native provider accepted or contacted: err=%v calls=%d", err, calls.Load())
			}
		})
	}
	client, fingerprint, err := runtime.GovernanceClient(valid)
	if err != nil || len(fingerprint) != 64 {
		t.Fatalf("valid pinned client: %v fingerprint=%q", err, fingerprint)
	}
	t.Setenv("AIPLEX_NATIVE_CONNECTION_TEST_TOKEN", "")
	if _, err = client.Do(context.Background(), http.MethodGet, "v1/projects/project-a/locations/global/services", nil, nil); err == nil || calls.Load() != 0 {
		t.Fatalf("missing host credential reached HTTP: err=%v calls=%d", err, calls.Load())
	}
}
