package google

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/pkg/nativeapi"
)

func testClient(t *testing.T, h http.HandlerFunc) (*nativeapi.Client, *atomic.Int32) {
	t.Helper()
	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	calls := new(atomic.Int32)
	c, e := nativeapi.NewClient(s.URL, s.Client(), nativeapi.AuthFunc(func(context.Context, *http.Request) error { calls.Add(1); return nil }))
	if e != nil {
		t.Fatal(e)
	}
	return c, calls
}
func raw(s string) json.RawMessage { return json.RawMessage(s) }
func TestRegistryNativeProtocol(t *testing.T) {
	parent := "projects/project-1/locations/global"
	var calls []string
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.RequestURI())
		switch r.Method {
		case "POST":
			var service Service
			if e := json.NewDecoder(r.Body).Decode(&service); e != nil {
				t.Error(e)
			}
			if service.Name != parent+"/services/test-agent" || service.AgentSpec == nil {
				t.Errorf("wrong service %+v", service)
			}
			io.WriteString(w, `{"name":"projects/project-1/locations/global/operations/created"}`)
		case "PATCH":
			if r.URL.Query().Get("updateMask") != "displayName" {
				t.Error("missing update mask")
			}
			io.WriteString(w, `{"name":"projects/project-1/locations/global/operations/patched"}`)
		case "DELETE":
			if b, _ := io.ReadAll(r.Body); len(b) != 0 {
				t.Error("delete body must be empty")
			}
			io.WriteString(w, `{"name":"projects/project-1/locations/global/operations/deleted"}`)
		default:
			io.WriteString(w, `{"name":"projects/project-1/locations/global/services/test-agent","displayName":"before"}`)
		}
	})
	d, e := New(Config{Project: "project-1", Location: "global", AllowNonAtomicMutations: true}, c)
	if e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	create := nativeapi.Operation{Name: "services.create", Resource: parent + "/services/test-agent", Payload: raw(`{"resource":{"agentSpec":{"type":"NO_SPEC"}}}`)}
	if _, e = d.Execute(ctx, create); e != nil {
		t.Fatal(e)
	}
	if calls[0] != "POST /v1/"+parent+"/services?serviceId=test-agent" {
		t.Fatal(calls)
	}
	r, e := d.Execute(ctx, nativeapi.Operation{Name: "services.get", Resource: create.Resource})
	if e != nil || !strings.HasPrefix(r.ETag, "sha256:") {
		t.Fatalf("observation %+v %v", r, e)
	}
	if _, e = d.Execute(ctx, nativeapi.Operation{Name: "services.patch", Resource: create.Resource, ETag: r.ETag, Payload: raw(`{"resource":{"displayName":"after"},"updateMask":"displayName"}`)}); e != nil {
		t.Fatal(e)
	}
	if _, e = d.Execute(ctx, nativeapi.Operation{Name: "services.delete", Resource: create.Resource, ETag: r.ETag, Payload: raw(`{}`)}); e != nil {
		t.Fatal(e)
	}
	if len(calls) != 6 {
		t.Fatalf("wrong native calls %v", calls)
	}
}
func TestRegistryRejectsBeforeCredentials(t *testing.T) {
	c, calls := testClient(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected native call") })
	d, _ := New(Config{Project: "project-1", Location: "global"}, c)
	for _, op := range []nativeapi.Operation{
		{Name: "services.get", Resource: "projects/foreign/locations/global/services/secret"},
		{Name: "services.get", Resource: "projects/project-1/locations/global/services/%2e%2e"},
		{Name: "services.get", Resource: "projects/project-1/locations/global/services/x:delete"},
		{Name: "services.get", Resource: "projects/project-1/locations/global/services/foo:getIamPolicy"},
		{Name: "operations.get", Resource: "projects/project-1/locations/global/operations/foo:cancel"},
		{Name: "services.create", Resource: "projects/project-1/locations/global/services/test-agent", Payload: raw(`{"resource":{"name":"projects/foreign/locations/global/services/test-agent","agentSpec":{"type":"NO_SPEC"}}}`)},
		{Name: "services.patch", Resource: "projects/project-1/locations/global/services/test-agent", Payload: raw(`{"resource":{"displayName":"after"},"updateMask":"displayName"}`)},
		{Name: "services.create", Resource: "projects/project-1/locations/global/services/test-agent", Payload: raw(`{"resource":{"agentSpec":{"type":"NO_SPEC"}},"resource":{"agentSpec":{"type":"NO_SPEC"}}}`)},
		{Name: "services.create", Resource: "projects/project-1/locations/global/services/test-agent", Payload: raw(`{"resource":{"agentSpec":{"type":"A2A_AGENT_CARD","content":{"client_secret":"never-persist"}}}}`)},
		{Name: "bindings.create", Resource: "projects/project-1/locations/global/bindings/test-binding", Payload: raw(`{"resource":{"target":{"identifier":"urn:mcp:foreign:ns:tool"}}}`)},
	} {
		if e := d.ValidateOperation(op); e == nil {
			t.Errorf("validated forbidden operation %+v", op)
		}
		if _, e := d.Execute(context.Background(), op); e == nil {
			t.Errorf("executed forbidden operation %+v", op)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid operation acquired credentials")
	}
}
func TestRegistryStaleFingerprintDoesNotWrite(t *testing.T) {
	var writes atomic.Int32
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			writes.Add(1)
		}
		io.WriteString(w, `{"name":"projects/project-1/locations/global/services/test-agent","displayName":"changed"}`)
	})
	d, _ := New(Config{Project: "project-1", Location: "global", AllowNonAtomicMutations: true}, c)
	old, _ := fingerprint(raw(`{"name":"projects/project-1/locations/global/services/test-agent","displayName":"old"}`))
	_, e := d.Execute(context.Background(), nativeapi.Operation{Name: "services.delete", Resource: "projects/project-1/locations/global/services/test-agent", ETag: old, Payload: raw(`{}`)})
	if e == nil || writes.Load() != 0 {
		t.Fatal("stale delete reached native write")
	}
}
func TestDiscoveryAndBindingProtocol(t *testing.T) {
	parent := "projects/project-1/locations/global"
	source, target := "urn:agent:publisher:ns:source", "urn:mcp:publisher:ns:target"
	var paths []string
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		if strings.HasSuffix(r.URL.Path, "bindings") {
			if r.URL.Query().Get("bindingId") != "test-binding" {
				t.Error("missing binding id")
			}
			var b Binding
			json.NewDecoder(r.Body).Decode(&b)
			if b.Source.Identifier != source || b.Target.Identifier != target || b.AuthProviderBinding.AuthProvider != parent+"/authProviders/test-provider" {
				t.Error("native binding payload mismatch")
			}
		}
		io.WriteString(w, `{}`)
	})
	d, _ := New(Config{Project: "project-1", Location: "global", AllowedIdentifiers: []string{source, target}}, c)
	for _, kind := range []string{"agents", "mcpServers", "endpoints", "skills"} {
		if _, e := d.Execute(context.Background(), nativeapi.Operation{Name: kind + ".list", Resource: parent, Payload: raw(`{"pageSize":20,"pageToken":"next"}`)}); e != nil {
			t.Fatal(e)
		}
	}
	_, e := d.Execute(context.Background(), nativeapi.Operation{Name: "bindings.create", Resource: parent + "/bindings/test-binding", Payload: raw(`{"resource":{"source":{"identifier":"` + source + `"},"target":{"identifier":"` + target + `"},"authProviderBinding":{"authProvider":"` + parent + `/authProviders/test-provider"}}}`)})
	if e != nil {
		t.Fatal(e)
	}
	if paths[3] != "GET /v1alpha/"+parent+"/skills" {
		t.Fatal(paths)
	}
}

func TestIAPPolicyNativeETagAndScopedDiff(t *testing.T) {
	const agent = "principal://agents.global.org-123.system.id.goog/resources/aiplatform/projects/456/locations/us-central1/reasoningEngines/one"
	const base = `{"etag":"cloud-cas","version":3,"bindings":[{"role":"roles/viewer","members":["user:owner@example.com"]}]}`
	var writes atomic.Int32
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/projects/456/locations/global/iap_web/agentRegistry/mcpServers/tool:getIamPolicy" && r.URL.Path != "/v1/projects/456/locations/global/iap_web/agentRegistry/mcpServers/tool:setIamPolicy" {
			t.Error(r.URL.Path)
		}
		if r.Method != "POST" {
			t.Error("IAP policy RPC requires POST")
		}
		if strings.HasSuffix(r.URL.Path, ":setIamPolicy") {
			writes.Add(1)
			var v struct {
				Policy json.RawMessage `json:"policy"`
			}
			json.NewDecoder(r.Body).Decode(&v)
			if !strings.Contains(string(v.Policy), `"etag":"cloud-cas"`) {
				t.Error("provider CAS missing")
			}
			io.WriteString(w, `{"etag":"after"}`)
		} else {
			io.WriteString(w, base)
		}
	})
	d, e := NewIAP(Config{Project: "456", Location: "global", AllowedAgentPrincipals: []string{agent}, AllowedRoles: []string{"roles/iap.egressor"}}, c)
	if e != nil {
		t.Fatal(e)
	}
	for _, member := range []string{"allUsers", "principal://agents.global.org-999.system.id.goog/resources/foreign"} {
		op := nativeapi.Operation{Name: "egress.setIamPolicy", Resource: "projects/456/locations/global/mcpServers/tool", ETag: "cloud-cas", Payload: raw(`{"policy":{"etag":"cloud-cas","version":3,"bindings":[{"role":"roles/viewer","members":["user:owner@example.com"]},{"role":"roles/iap.egressor","members":["` + member + `"]}]}}`)}
		if _, e = d.Execute(context.Background(), op); e == nil {
			t.Fatal("foreign/wildcard IAM principal accepted")
		}
	}
	if writes.Load() != 0 {
		t.Fatal("invalid IAM grants reached write")
	}
	op := nativeapi.Operation{Name: "egress.setIamPolicy", Resource: "projects/456/locations/global/mcpServers/tool", ETag: "cloud-cas", Payload: raw(`{"policy":{"etag":"cloud-cas","version":3,"bindings":[{"role":"roles/viewer","members":["user:owner@example.com"]},{"role":"roles/iap.egressor","members":["` + agent + `"]}]}}`)}
	r, e := d.Execute(context.Background(), op)
	if e != nil || r.ETag != "after" || writes.Load() != 1 {
		t.Fatalf("policy write %+v %v", r, e)
	}
	op.Payload = raw(`{"policy":{"etag":"cloud-cas","version":3,"bindings":[{"role":"roles/iap.egressor","members":["` + agent + `"]}]}}`)
	if _, e = d.Execute(context.Background(), op); e == nil || writes.Load() != 1 {
		t.Fatal("unmanaged owner grant removed")
	}
	op.ETag = "wrong"
	if e = d.ValidateOperation(op); e == nil {
		t.Fatal("ETag mismatch accepted")
	}
}
func TestCredentialBrokerSeparateUserAndAgent(t *testing.T) {
	const agent = "principal://agents.global.org-123.system.id.goog/resources/aiplatform/projects/456/locations/us-central1/reasoningEngines/one"
	provider := "projects/456/locations/global/authProviders/test-provider"
	c, calls := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/"+provider+"/credentials:retrieve" {
			t.Error(r.URL.Path)
		}
		var request map[string]any
		json.NewDecoder(r.Body).Decode(&request)
		if request["userId"] != "trusted-user" {
			t.Error("user delegation collapsed into agent identity")
		}
		io.WriteString(w, `{"uriConsentRequired":{"authorizationUri":"https://consent.example/authorize","consentNonce":"nonce","uid":"flow"}}`)
	})
	authorize := DelegationAuthorizeFunc(func(_ context.Context, d Delegation) error {
		if d.UserID != "trusted-user" {
			return errors.New("user not authorized")
		}
		return nil
	})
	broker, e := NewCredentialsClient(Config{Project: "456", Location: "global", AllowedAgentPrincipals: []string{agent}}, c, authorize)
	if e != nil {
		t.Fatal(e)
	}
	in := RetrieveRequest{Delegation: Delegation{AgentPrincipal: agent, UserID: "forged-user", AuthProvider: provider}}
	if _, e = broker.Retrieve(context.Background(), in); e == nil || calls.Load() != 0 {
		t.Fatal("delegation denial did not precede credentials")
	}
	in.Delegation.UserID = "trusted-user"
	r, e := broker.Retrieve(context.Background(), in)
	if e != nil || r.ConsentRequired == nil || r.ConsentRequired.AuthorizationURI != "https://consent.example/authorize" {
		t.Fatalf("broker response %+v %v", r, e)
	}
	manager, _ := NewIdentityManager(Config{Project: "456", Location: "global"}, c)
	for _, op := range manager.Operations() {
		if !op.Credential {
			t.Fatal("auth-manager exposed as public data")
		}
	}
}

func TestAuthManagerNativeProtocolAndActions(t *testing.T) {
	const agent = "principal://agents.global.org-123.system.id.goog/resources/aiplatform/projects/456/locations/us-central1/reasoningEngines/one"
	const provider = "projects/456/locations/global/authProviders/test-provider"
	const observed = `{"name":"projects/456/locations/global/authProviders/test-provider","state":"DISABLED"}`
	const requestID = "531d05b9-c62c-4b72-9f4c-a6c77fa62d17"
	var paths []string
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == "GET":
			io.WriteString(w, observed)
		case strings.HasSuffix(r.URL.Path, ":enable"):
			if r.URL.RawQuery != "" {
				t.Error("enable requestId belongs in JSON body")
			}
			var payload map[string]string
			if e := json.NewDecoder(r.Body).Decode(&payload); e != nil || payload["requestId"] != requestID {
				t.Errorf("enable request payload %v %v", payload, e)
			}
			io.WriteString(w, `{"state":"ENABLED"}`)
		default:
			if r.URL.Query().Get("authProviderId") != "test-provider" {
				t.Error("authProviderId missing")
			}
			var v AuthProvider
			if e := json.NewDecoder(r.Body).Decode(&v); e != nil || len(v.WorkloadIDs) != 1 || v.WorkloadIDs[0] != agent {
				t.Errorf("auth-provider payload %+v %v", v, e)
			}
			io.WriteString(w, observed)
		}
	})
	d, e := NewIdentityManager(Config{Project: "456", Location: "global", AllowedAgentPrincipals: []string{agent}, AllowNonAtomicMutations: true}, c)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = d.Execute(context.Background(), nativeapi.Operation{Name: "authProviders.create", Resource: provider, Payload: raw(`{"resource":{"authProviderTypeParams":{"apiKey":{"apiKey":"private-test-fixture"}},"workloadIds":["` + agent + `"]}}`)}); e != nil {
		t.Fatal(e)
	}
	read, e := d.Execute(context.Background(), nativeapi.Operation{Name: "authProviders.get", Resource: provider})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = d.Execute(context.Background(), nativeapi.Operation{Name: "authProviders.enable", Resource: provider, ETag: read.ETag, Payload: raw(`{"requestId":"` + requestID + `"}`)}); e != nil {
		t.Fatal(e)
	}
	if len(paths) != 4 || paths[0] != "POST /v1/projects/456/locations/global/authProviders" || paths[3] != "POST /v1/"+provider+":enable" {
		t.Fatal(paths)
	}
}
