package main

import (
	"encoding/json"
	"testing"
)

// An invocation without an operation must not reach the grant check, where
// it would be evaluated as "a2a:agent:{id}:" and match wildcard grants.
func TestAgentInvocationRequiresAnOperation(t *testing.T) {
	s := newAuthzServer(t)
	s.put(t, authzHomeTenant, "agent-connection", "agent-1", `{"id":"agent-1","tenant_id":"`+authzHomeTenant+`"}`)
	for _, body := range []string{`{}`, `{"operation":""}`, `{"operation":"   "}`} {
		rec := s.do(t, "POST", "/api/v1/a2a/connections/agent-1/invoke", string(callerAdmin), body)
		if rec.Code != 400 {
			t.Errorf("body %s: got %d %s", body, rec.Code, rec.Body)
		}
	}
}

// Server-owned fields in a role-binding update are not taken from the body.
func TestRoleBindingUpdateIgnoresClientRevision(t *testing.T) {
	s := newAuthzServer(t)
	rec := s.do(t, "POST", "/api/v1/iam/role-bindings", string(callerAdmin), `{"id":"rb","group":"g","role":"viewer"}`)
	if rec.Code != 201 && rec.Code != 200 {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	req := authzRequest("PUT", "/api/v1/iam/role-bindings/rb", string(callerAdmin), `{"description":"d","revision":999,"created_by":"someone-else"}`)
	req.Header.Set("If-Match", rec.Header().Get("ETag"))
	rec = s.do2(req)
	if rec.Code != 200 {
		t.Fatalf("update: %d %s", rec.Code, rec.Body)
	}
	got := s.do(t, "GET", "/api/v1/iam/role-bindings/rb", string(callerAdmin), "")
	var binding struct {
		Revision  int64  `json:"revision"`
		CreatedBy string `json:"created_by"`
	}
	if json.Unmarshal(got.Body.Bytes(), &binding) != nil || binding.Revision == 999 || binding.CreatedBy == "someone-else" {
		t.Fatalf("server-owned fields taken from the body: %s", got.Body)
	}
}
