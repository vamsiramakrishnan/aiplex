package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/internal/auth"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
)

// Data-level authorization: what an allowed caller may see, tenant
// isolation of identifiers, and the self-service views.

func (s *authzServer) put(t *testing.T, tenant, kind, id, spec string) {
	t.Helper()
	if _, err := s.resources.Put(context.Background(), resources.Record{TenantID: tenant, Kind: kind, ID: id, Spec: json.RawMessage(spec)}, 0); err != nil {
		t.Fatal(err)
	}
}

func (s *authzServer) do(t *testing.T, method, path, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	s.handler.ServeHTTP(rec, authzRequest(method, path, token, body))
	return rec
}

func principalOf(caller authzCaller) string {
	return auth.CanonicalPrincipalID(authzIssuer, "people", "subject-"+string(caller))
}

func TestAccessPointReadsAreFilteredForOrdinaryCallers(t *testing.T) {
	s := newAuthzServer(t)
	// "open" admits every user; "admins-only" names one principal; "agents"
	// admits only agents.
	s.put(t, authzHomeTenant, "access-point", "open", `{"id":"open","display_name":"Open","caller_kinds":["user"],"models":[{"name":"m","surface_ref":"s","native_model":"n"}]}`)
	s.put(t, authzHomeTenant, "access-point", "admins-only", `{"id":"admins-only","caller_kinds":["user"],"subjects":{"principals":["`+principalOf(callerAdmin)+`"]}}`)
	s.put(t, authzHomeTenant, "access-point", "agents", `{"id":"agents","caller_kinds":["agent"]}`)
	s.put(t, authzForeign, "access-point", "foreign", `{"id":"foreign","caller_kinds":["user"]}`)

	// Operators read full records, including subject lists.
	rec := s.do(t, "GET", "/api/v1/access-points", string(callerDeployer), "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), principalOf(callerAdmin)) || strings.Contains(rec.Body.String(), `"foreign"`) {
		t.Fatalf("operator list: %d %s", rec.Code, rec.Body)
	}

	for _, caller := range []authzCaller{callerNoRole, callerViewer} {
		rec = s.do(t, "GET", "/api/v1/access-points", string(caller), "")
		var views []map[string]any
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &views) != nil {
			t.Fatalf("%s list: %d %s", caller, rec.Code, rec.Body)
		}
		if len(views) != 1 || views[0]["id"] != "open" || views[0]["display_name"] != "Open" || views[0]["endpoints"] == nil {
			t.Fatalf("%s must see only the access point that admits it: %s", caller, rec.Body)
		}
		for _, leaked := range []string{"subjects", "principals", "spec", "caller_kinds", principalOf(callerAdmin), "admins-only", "agents"} {
			if strings.Contains(rec.Body.String(), leaked) {
				t.Fatalf("%s list leaks %q: %s", caller, leaked, rec.Body)
			}
		}
		rec = s.do(t, "GET", "/api/v1/access-points/open", string(caller), "")
		if rec.Code != 200 || strings.Contains(rec.Body.String(), "caller_kinds") || !strings.Contains(rec.Body.String(), "/ap/open/") {
			t.Fatalf("%s get admitted: %d %s", caller, rec.Code, rec.Body)
		}
		missing := s.do(t, "GET", "/api/v1/access-points/does-not-exist", string(caller), "")
		for _, id := range []string{"admins-only", "agents", "foreign"} {
			rec = s.do(t, "GET", "/api/v1/access-points/"+id, string(caller), "")
			if rec.Code != missing.Code || rec.Code != 404 || authzErrorCode(rec.Body.Bytes()) != authzErrorCode(missing.Body.Bytes()) {
				t.Fatalf("%s get of non-admitting %s must look missing: %d %s vs %d %s", caller, id, rec.Code, rec.Body, missing.Code, missing.Body)
			}
		}
	}

	rec = s.do(t, "GET", "/api/v1/access-points", string(callerAgent), "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"agents"`) || strings.Contains(rec.Body.String(), `"open"`) {
		t.Fatalf("agent list: %d %s", rec.Code, rec.Body)
	}
}

func TestCrossTenantIdentifiersBehaveLikeMissingOnes(t *testing.T) {
	s := newAuthzServer(t)
	for kind, path := range map[string]string{
		"access-point":       "/api/v1/access-points/",
		"model-surface":      "/api/v1/llm/surfaces/",
		"tool-connection":    "/api/v1/mcp/connections/",
		"agent-connection":   "/api/v1/a2a/connections/",
		"client-destination": "/api/v1/clients/destinations/",
		"network-profile":    "/api/v1/network/profiles/",
		"model-price":        "/api/v1/usage/prices/",
	} {
		s.put(t, authzHomeTenant, kind, "home-only", `{"id":"home-only"}`)
		missing := s.do(t, "GET", path+"never-existed", string(callerForeign), "")
		found := s.do(t, "GET", path+"home-only", string(callerForeign), "")
		if found.Code != 404 || found.Code != missing.Code || authzErrorCode(found.Body.Bytes()) != authzErrorCode(missing.Body.Bytes()) {
			t.Errorf("%s: cross-tenant id answered %d %s, missing id %d %s", kind, found.Code, found.Body, missing.Code, missing.Body)
		}
		if home := s.do(t, "GET", path+"home-only", string(callerAdmin), ""); home.Code != 200 {
			t.Errorf("%s: home admin cannot read its own record: %d %s", kind, home.Code, home.Body)
		}
		list := s.do(t, "GET", strings.TrimSuffix(path, "/"), string(callerForeign), "")
		if list.Code != 200 || strings.Contains(list.Body.String(), "home-only") {
			t.Errorf("%s: foreign list leaks home records: %d %s", kind, list.Code, list.Body)
		}
		// Writes address the caller's own tenant; a delete of the home id
		// must not remove the home record.
		req := authzRequest("DELETE", path+"home-only", string(callerForeign), "")
		req.Header.Set("If-Match", `"1"`)
		rec := httptest.NewRecorder()
		s.handler.ServeHTTP(rec, req)
		if rec.Code != 404 {
			t.Errorf("%s: cross-tenant delete answered %d %s", kind, rec.Code, rec.Body)
		}
		if _, err := s.resources.Get(context.Background(), authzHomeTenant, kind, "home-only"); err != nil {
			t.Errorf("%s: cross-tenant delete removed the home record", kind)
		}
	}
}

// failingResources fails reads of one access point id, as a store outage.
type failingResources struct {
	resources.Store
	failID string
}

func (f failingResources) Get(ctx context.Context, tenant, kind, id string) (resources.Record, error) {
	if kind == "access-point" && id == f.failID {
		return resources.Record{}, errors.New("backend unavailable")
	}
	return f.Store.Get(ctx, tenant, kind, id)
}

// A store outage is 503 ACCESS_POINT_UNAVAILABLE on every route; only a saved
// access point that no longer decodes is 409 ACCESS_POINT_INVALID, and only on
// administrative routes.
func TestAccessPointLoadFailuresMapConsistently(t *testing.T) {
	s := newAuthzServerWithResources(t, failingResources{Store: resources.NewMemoryStore(), failID: "outage"})
	s.put(t, authzHomeTenant, "access-point", "outage", `{"id":"outage","caller_kinds":["user"]}`)
	s.put(t, authzHomeTenant, "access-point", "corrupt", `{"id":"corrupt","caller_kinds":["user"],"unknown_field":true}`)
	for _, test := range []struct {
		method, path, token string
		outage, corrupt     int
	}{
		{"GET", "/api/v1/overview/access-points/%s", string(callerAdmin), 503, 409},
		{"GET", "/api/v1/overview/access-points/%s/connect", string(callerAdmin), 503, 409},
		{"POST", "/api/v1/access-points/%s/quota-plan", string(callerAdmin), 503, 409},
		{"POST", "/ap/%s/v1/chat/completions", string(callerNoRole), 503, 503},
	} {
		body := ""
		if test.method == "POST" {
			body = `{"model":"m","messages":[]}`
		}
		rec := s.do(t, test.method, fmt.Sprintf(test.path, "outage"), test.token, body)
		if rec.Code != test.outage || !strings.Contains(strings.ToUpper(rec.Body.String()), "ACCESS_POINT_UNAVAILABLE") {
			t.Errorf("%s outage: %d %s", test.path, rec.Code, rec.Body)
		}
		rec = s.do(t, test.method, fmt.Sprintf(test.path, "corrupt"), test.token, body)
		if rec.Code != test.corrupt {
			t.Errorf("%s corrupt: %d %s", test.path, rec.Code, rec.Body)
		}
	}
}
