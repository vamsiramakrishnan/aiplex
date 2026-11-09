package entra

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/pkg/nativeapi"
)

func broker(t *testing.T, transport transportFunc) *TokenBroker {
	t.Helper()
	b, e := NewTokenBroker(BrokerConfig{TenantID: tenantID, Blueprint: Blueprint{ObjectID: blueprintID, AppID: blueprintAppID}, AllowedAgents: []AgentBinding{{ObjectID: agentID, AppID: agentAppID}}, Services: []string{"Graph"}, SidecarURL: "https://sidecar.example", AgentIdentityOverridesEnabled: true}, &http.Client{Transport: transport}, nativeapi.AuthFunc(func(_ context.Context, r *http.Request) error {
		r.Header.Set("X-Host-Authorization", "host-only")
		return nil
	}))
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func tokenRequest() TokenRequest {
	return TokenRequest{TenantID: tenantID, BlueprintObjectID: blueprintID, AgentObjectID: agentID, AgentAppID: agentAppID, Service: "Graph"}
}
func TestApprovedSidecarAutonomousUsesAgentApplicationID(t *testing.T) {
	calls := 0
	b := broker(t, func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != "GET" || r.URL.Path != "/AuthorizationHeaderUnauthenticated/Graph" || r.URL.Query().Get("AgentIdentity") != agentAppID || len(r.URL.Query()) != 1 {
			t.Fatalf("wrong sidecar autonomous request: %s %s", r.Method, r.URL)
		}
		if r.Header.Get("X-Host-Authorization") != "host-only" || r.Header.Get("Authorization") != "" {
			t.Fatal("host auth missing or user context inserted")
		}
		return response(200, `{"authorizationHeader":"Bearer downstream-secret"}`), nil
	})
	credential, e := b.AcquireAutonomous(context.Background(), tokenRequest())
	if e != nil || credential.BearerToken() != "downstream-secret" || calls != 1 {
		t.Fatal("sidecar autonomous acquisition failed", e, calls)
	}
	encoded, e := json.Marshal(credential)
	if e != nil || strings.Contains(string(encoded), "downstream-secret") || strings.Contains(fmt.Sprintf("%v %#v", credential, credential), "downstream-secret") {
		t.Fatal("credential exposed through serialization/formatting")
	}
}
func TestApprovedSidecarOBOValidatesTenantUserAndBlueprintBeforeExchange(t *testing.T) {
	paths := []string{}
	b := broker(t, func(r *http.Request) (*http.Response, error) {
		paths = append(paths, r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer incoming-user-token" {
			t.Fatal("OBO did not forward assertion exclusively to sidecar")
		}
		switch r.URL.Path {
		case "/Validate":
			return response(200, fmt.Sprintf(`{"protocol":"Bearer","token":"incoming-user-token","claims":{"tid":%q,"oid":%q,"aud":%q}}`, tenantID, userID, blueprintAppID)), nil
		case "/AuthorizationHeader/Graph":
			if r.URL.Query().Get("AgentIdentity") != agentAppID || len(r.URL.Query()) != 1 {
				t.Fatal("OBO used wrong child or arbitrary overrides")
			}
			return response(200, `{"authorizationHeader":"Bearer delegated-resource-token"}`), nil
		default:
			t.Fatal("unexpected sidecar endpoint", r.URL.Path)
			return nil, nil
		}
	})
	request := tokenRequest()
	request.UserObjectID = userID
	credential, e := b.AcquireOnBehalfOf(context.Background(), request, "incoming-user-token")
	if e != nil || credential.Header() != "Bearer delegated-resource-token" || strings.Join(paths, ",") != "/Validate,/AuthorizationHeader/Graph" {
		t.Fatal("OBO sequence failed", e, paths)
	}
}
func TestOBORejectsMismatchedValidatedIdentityAndDoesNotExchange(t *testing.T) {
	for _, field := range []string{"tid", "oid", "aud"} {
		t.Run(field, func(t *testing.T) {
			calls := 0
			b := broker(t, func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.Path != "/Validate" {
					t.Fatal("invalid user reached token acquisition")
				}
				claims := map[string]string{"tid": tenantID, "oid": userID, "aud": blueprintAppID}
				claims[field] = foreignID
				body, _ := json.Marshal(map[string]any{"protocol": "Bearer", "claims": claims})
				return response(200, string(body)), nil
			})
			request := tokenRequest()
			request.UserObjectID = userID
			if _, e := b.AcquireOnBehalfOf(context.Background(), request, "incoming-user-token"); e == nil || calls != 1 {
				t.Fatal("invalid identity accepted", e, calls)
			}
		})
	}
}
func TestBrokerRejectsCrossTenantBlueprintAgentAndServiceLocally(t *testing.T) {
	calls := 0
	b := broker(t, func(*http.Request) (*http.Response, error) {
		calls++
		return response(500, `{"detail":"incoming-user-token secret"}`), nil
	})
	for _, change := range []func(*TokenRequest){func(r *TokenRequest) { r.TenantID = foreignID }, func(r *TokenRequest) { r.BlueprintObjectID = foreignID }, func(r *TokenRequest) { r.AgentObjectID = foreignID }, func(r *TokenRequest) { r.AgentAppID = foreignID }, func(r *TokenRequest) { r.Service = "Other" }, func(r *TokenRequest) { r.UserObjectID = userID }} {
		r := tokenRequest()
		change(&r)
		if _, e := b.AcquireAutonomous(context.Background(), r); e == nil {
			t.Fatal("unapproved broker binding accepted")
		}
	}
	if calls != 0 {
		t.Fatal("local denial contacted sidecar")
	}
	r := tokenRequest()
	r.UserObjectID = userID
	if _, e := b.AcquireOnBehalfOf(context.Background(), r, "incoming-user-token"); e == nil || strings.Contains(e.Error(), "incoming-user-token") {
		t.Fatal("sidecar error leaked assertion", e)
	}
}
func TestBrokerDoesNotAdvertiseUnconfiguredAgentOverrides(t *testing.T) {
	_, e := NewTokenBroker(BrokerConfig{TenantID: tenantID, Blueprint: Blueprint{ObjectID: blueprintID, AppID: blueprintAppID}, AllowedAgents: []AgentBinding{{ObjectID: agentID, AppID: agentAppID}}, Services: []string{"Graph"}, SidecarURL: "https://sidecar.example"}, nil, nativeapi.AuthFunc(func(context.Context, *http.Request) error { return nil }))
	if e == nil {
		t.Fatal("agent token acquisition enabled without sidecar override configuration")
	}
}

func TestOBOCannotOverwriteSidecarHostAuthorization(t *testing.T) {
	for _, name := range []string{"Authorization", "authorization", "AUTHORIZATION"} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			b := broker(t, func(*http.Request) (*http.Response, error) {
				calls++
				return response(200, `{}`), nil
			})
			b.host = nativeapi.AuthFunc(func(_ context.Context, r *http.Request) error {
				r.Header[name] = []string{"Bearer host-only-credential"}
				return nil
			})
			request := tokenRequest()
			request.UserObjectID = userID
			_, err := b.AcquireOnBehalfOf(context.Background(), request, "incoming-user-token")
			if err == nil || calls != 0 || strings.Contains(err.Error(), "host-only-credential") || strings.Contains(err.Error(), "incoming-user-token") {
				t.Fatalf("host authentication was silently overwritten: %v calls=%d", err, calls)
			}
		})
	}
}
