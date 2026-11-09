package entra

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/pkg/nativeapi"
)

const tenantID = "11111111-1111-1111-1111-111111111111"
const blueprintID = "22222222-2222-2222-2222-222222222222"
const blueprintAppID = "33333333-3333-3333-3333-333333333333"
const agentID = "44444444-4444-4444-4444-444444444444"
const agentAppID = "55555555-5555-5555-5555-555555555555"
const sponsorID = "66666666-6666-6666-6666-666666666666"
const userID = "77777777-7777-7777-7777-777777777777"
const foreignID = "88888888-8888-8888-8888-888888888888"

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(code int, raw string) *http.Response {
	return &http.Response{StatusCode: code, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(raw))}
}
func payload(s Selector) json.RawMessage { raw, _ := json.Marshal(s); return raw }
func driver(t *testing.T, transport transportFunc) *Driver {
	t.Helper()
	client, e := nativeapi.NewClient("https://graph.microsoft.com", &http.Client{Transport: transport}, nativeapi.AuthFunc(func(_ context.Context, r *http.Request) error {
		r.Header.Set("Authorization", "Bearer host-token")
		return nil
	}))
	if e != nil {
		t.Fatal(e)
	}
	d, e := New(Config{TenantID: tenantID, APIVersion: "beta", AllowedBlueprints: []Blueprint{{ObjectID: blueprintID, AppID: blueprintAppID}}, AllowBlueprintCreation: true}, client)
	if e != nil {
		t.Fatal(e)
	}
	return d
}
func TestGraphNativeLifecycleAndRelationshipPaths(t *testing.T) {
	blueprintPath := "/beta/applications/" + blueprintID + "/microsoft.graph.agentIdentityBlueprint"
	agentPath := "/beta/servicePrincipals/" + agentID + "/microsoft.graph.agentIdentity"
	for _, tc := range []struct {
		name, resource, method, path string
		in                           Selector
		check                        func(*testing.T, *http.Request, map[string]any)
	}{
		{"blueprint.create", tenantID, "POST", "/beta/applications/microsoft.graph.agentIdentityBlueprint", Selector{TenantID: tenantID, DisplayName: "Blueprint", Sponsors: []Sponsor{{ObjectID: sponsorID, Type: "user"}}}, nil},
		{"blueprint.get", blueprintID, "GET", blueprintPath, Selector{TenantID: tenantID}, nil},
		{"blueprint.update", blueprintID, "PATCH", blueprintPath, Selector{TenantID: tenantID, DisplayName: "Renamed"}, nil},
		{"blueprint.delete", blueprintID, "DELETE", blueprintPath, Selector{TenantID: tenantID}, nil},
		{"blueprint.sponsors.list", blueprintID, "GET", blueprintPath + "/sponsors", Selector{TenantID: tenantID}, nil},
		{"blueprint.sponsors.remove", blueprintID, "DELETE", blueprintPath + "/sponsors/" + sponsorID + "/$ref", Selector{TenantID: tenantID, Sponsor: &Sponsor{ObjectID: sponsorID}}, nil},
		{"agent.create", blueprintID, "POST", "/beta/servicePrincipals/microsoft.graph.agentIdentity", Selector{TenantID: tenantID, DisplayName: "Agent", Sponsors: []Sponsor{{ObjectID: sponsorID, Type: "user"}}}, func(t *testing.T, r *http.Request, body map[string]any) {
			if body["agentIdentityBlueprintId"] != blueprintAppID {
				t.Fatal("creation used blueprint objectID instead of appId", body)
			}
			refs := body["sponsors@odata.bind"].([]any)
			if refs[0] != "https://graph.microsoft.com/beta/users/"+sponsorID {
				t.Fatal(refs)
			}
		}},
		{"agent.list", blueprintID, "GET", "/beta/servicePrincipals/microsoft.graph.agentIdentity", Selector{TenantID: tenantID}, func(t *testing.T, r *http.Request, _ map[string]any) {
			if r.URL.Query().Get("$filter") != "agentIdentityBlueprintId eq '"+blueprintAppID+"'" {
				t.Fatal("list was not blueprint-scoped", r.URL.RawQuery)
			}
		}},
		{"agent.get", agentID, "GET", agentPath, Selector{TenantID: tenantID, BlueprintObjectID: blueprintID}, nil},
		{"agent.disable", agentID, "PATCH", agentPath, Selector{TenantID: tenantID, BlueprintObjectID: blueprintID}, func(t *testing.T, _ *http.Request, body map[string]any) {
			if body["accountEnabled"] != false {
				t.Fatal("native account disable omitted", body)
			}
		}},
		{"agent.enable", agentID, "PATCH", agentPath, Selector{TenantID: tenantID, BlueprintObjectID: blueprintID}, nil},
		{"agent.delete", agentID, "DELETE", agentPath, Selector{TenantID: tenantID, BlueprintObjectID: blueprintID}, nil},
		{"agent.sponsors.add", agentID, "POST", agentPath + "/sponsors/$ref", Selector{TenantID: tenantID, BlueprintObjectID: blueprintID, Sponsor: &Sponsor{ObjectID: sponsorID, Type: "group"}}, func(t *testing.T, _ *http.Request, body map[string]any) {
			if body["@odata.id"] != "https://graph.microsoft.com/beta/directoryObjects/"+sponsorID {
				t.Fatal(body)
			}
		}},
		{"agent.sponsors.remove", agentID, "DELETE", agentPath + "/sponsors/" + sponsorID + "/$ref", Selector{TenantID: tenantID, BlueprintObjectID: blueprintID, Sponsor: &Sponsor{ObjectID: sponsorID}}, nil},
		{"agent.appRoles.list", agentID, "GET", "/beta/servicePrincipals/" + agentID + "/appRoleAssignments", Selector{TenantID: tenantID, BlueprintObjectID: blueprintID}, nil},
		{"agent.delegatedGrants.list", agentID, "GET", "/beta/servicePrincipals/" + agentID + "/oauth2PermissionGrants", Selector{TenantID: tenantID, BlueprintObjectID: blueprintID}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var last *http.Request
			var body map[string]any
			calls := 0
			d := driver(t, func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Header.Get("Authorization") != "Bearer host-token" {
					t.Fatal("missing host auth")
				}
				last = r
				body = map[string]any{}
				if r.Body != nil {
					_ = json.NewDecoder(r.Body).Decode(&body)
				}
				if r.Method == "GET" && r.URL.Path == blueprintPath {
					return response(200, fmt.Sprintf(`{"id":%q,"appId":%q,"passwordCredentials":[{"secretText":"must-not-leak"}]}`, blueprintID, blueprintAppID)), nil
				}
				if r.Method == "GET" && r.URL.Path == agentPath {
					return response(200, fmt.Sprintf(`{"id":%q,"appId":%q,"agentIdentityBlueprintId":%q}`, agentID, agentAppID, blueprintAppID)), nil
				}
				if tc.name == "agent.list" {
					return response(200, fmt.Sprintf(`{"value":[{"id":%q,"agentIdentityBlueprintId":%q}]}`, agentID, blueprintAppID)), nil
				}
				return response(200, `{"ok":true,"client_secret":"must-not-leak"}`), nil
			})
			result, e := d.Execute(context.Background(), nativeapi.Operation{Name: tc.name, Resource: tc.resource, Payload: payload(tc.in)})
			if e != nil {
				t.Fatal(e)
			}
			if last.Method != tc.method || last.URL.Path != tc.path {
				t.Fatalf("native request mismatch: %s %s, want %s %s", last.Method, last.URL.Path, tc.method, tc.path)
			}
			if strings.Contains(string(result.Body), "must-not-leak") {
				t.Fatal("credential property escaped governance response")
			}
			if tc.check != nil {
				tc.check(t, last, body)
			}
			if strings.HasPrefix(tc.name, "agent.") && tc.name != "agent.get" && calls < 2 {
				t.Fatal("child action did not verify authoritative blueprint parentage")
			}
		})
	}
}
func TestLocalValidationRejectsTenantBlueprintAndPayloadBeforeRPC(t *testing.T) {
	var calls atomic.Int32
	d := driver(t, func(*http.Request) (*http.Response, error) { calls.Add(1); return response(200, `{}`), nil })
	for _, op := range []nativeapi.Operation{
		{Name: "agent.disable", Resource: agentID, Payload: payload(Selector{TenantID: foreignID, BlueprintObjectID: blueprintID})},
		{Name: "agent.disable", Resource: agentID, Payload: payload(Selector{TenantID: tenantID, BlueprintObjectID: foreignID})},
		{Name: "agent.disable", Resource: "../servicePrincipals", Payload: payload(Selector{TenantID: tenantID, BlueprintObjectID: blueprintID})},
		{Name: "agent.disable", Resource: agentID, Payload: json.RawMessage(`{"tenant_id":"` + tenantID + `","blueprint_object_id":"` + blueprintID + `","client_secret":"forbidden"}`)},
		{Name: "agent.update", Resource: agentID, Payload: payload(Selector{TenantID: tenantID, BlueprintObjectID: blueprintID})},
		{Name: "agent.token", Resource: agentID, Payload: payload(Selector{TenantID: tenantID})},
	} {
		if d.ValidateOperation(op) == nil {
			t.Fatal("invalid operation accepted", op.Name)
		}
		if _, e := d.Execute(context.Background(), op); e == nil {
			t.Fatal("invalid operation executed", op.Name)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("local denial contacted Graph")
	}
}
func TestChildOfDifferentBlueprintCannotBeModified(t *testing.T) {
	var calls atomic.Int32
	d := driver(t, func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		if r.Method != "GET" {
			t.Fatal("mutated before checking blueprint")
		}
		return response(200, fmt.Sprintf(`{"id":%q,"agentIdentityBlueprintId":%q}`, agentID, foreignID)), nil
	})
	_, e := d.Execute(context.Background(), nativeapi.Operation{Name: "agent.disable", Resource: agentID, Payload: payload(Selector{TenantID: tenantID, BlueprintObjectID: blueprintID})})
	if e == nil || calls.Load() != 1 {
		t.Fatal("foreign child modified", e, calls.Load())
	}
}
func TestGraphErrorsDoNotExposeCredentialBody(t *testing.T) {
	d := driver(t, func(*http.Request) (*http.Response, error) {
		return response(401, `{"error":{"message":"Bearer highly-secret-token"}}`), nil
	})
	_, e := d.Execute(context.Background(), nativeapi.Operation{Name: "blueprint.get", Resource: blueprintID, Payload: payload(Selector{TenantID: tenantID})})
	if e == nil || strings.Contains(e.Error(), "highly-secret") {
		t.Fatal("unsafe upstream error", e)
	}
}
