package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
	"github.com/vamsiramakrishnan/aiplex/pkg/toolauth"
	"github.com/vamsiramakrishnan/aiplex/pkg/toolexec"
)

func (h *ConnectedHandler) toolAuthorizationContext(w http.ResponseWriter, r *http.Request) (ToolConnection, resources.Record, toolauth.SessionKey, bool) {
	tenant, ok := h.tenant(w, r)
	if !ok {
		return ToolConnection{}, resources.Record{}, toolauth.SessionKey{}, false
	}
	if h.ToolSessions == nil || h.Tools == nil {
		Error(w, r, 503, "TOOL_AUTHORIZATION_UNAVAILABLE", "configure durable tool authorization and secret storage")
		return ToolConnection{}, resources.Record{}, toolauth.SessionKey{}, false
	}
	rec, e := h.Store.Get(r.Context(), tenant, "tool-connection", idParam(r))
	if e != nil {
		resourceError(w, r, e)
		return ToolConnection{}, resources.Record{}, toolauth.SessionKey{}, false
	}
	var c ToolConnection
	if json.Unmarshal(rec.Spec, &c) != nil || c.Execution == nil || c.Execution.CredentialMode != toolexec.DelegatedOAuth || c.Execution.Validate() != nil || c.Execution.ID != c.ID || c.Execution.Endpoint != c.ResourceURL {
		Error(w, r, 409, "TOOL_AUTHORIZATION_UNAVAILABLE", "saved connection does not support delegated OAuth")
		return ToolConnection{}, resources.Record{}, toolauth.SessionKey{}, false
	}
	key, e := h.toolSessionKey(r.Context(), tenant, rec, c)
	if e != nil {
		Error(w, r, 401, "TOOL_IDENTITY_REQUIRED", "verified tool owner required")
		return ToolConnection{}, resources.Record{}, toolauth.SessionKey{}, false
	}
	allowed := h.authorizeCurrent(r.Context(), r, "mcp:authorize:"+c.ID) == nil
	for _, tool := range c.Execution.AllowedTools {
		if h.authorizeCurrent(r.Context(), r, "mcp:tools:"+tool) == nil {
			allowed = true
			break
		}
	}
	if !allowed {
		Error(w, r, 403, "TOOL_AUTHORIZATION_DENIED", "tool authorization grant required")
		return ToolConnection{}, resources.Record{}, toolauth.SessionKey{}, false
	}
	return c, rec, key, true
}
func decodeToolAuthorization(r *http.Request, target any) error {
	raw, err := io.ReadAll(io.LimitReader(r.Body, (16<<10)+1))
	if err != nil || len(raw) > 16<<10 {
		return fmt.Errorf("bounded authorization body required")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if e := decoder.Decode(target); e != nil {
		return fmt.Errorf("invalid authorization body")
	}
	if e := decoder.Decode(new(any)); e != io.EOF {
		return fmt.Errorf("single authorization body required")
	}
	return nil
}
func (h *ConnectedHandler) StartToolAuthorization(w http.ResponseWriter, r *http.Request) {
	c, rec, key, ok := h.toolAuthorizationContext(w, r)
	if !ok {
		return
	}
	var input struct {
		RedirectURI string `json:"redirect_uri"`
	}
	if decodeToolAuthorization(r, &input) != nil {
		Error(w, r, 400, "INVALID_REQUEST", "exact registered redirect_uri required")
		return
	}
	reg, e := h.Tools.Prepare(r.Context(), toolauth.Connection{TenantID: key.Owner.TenantID, ID: c.ID, ResourceURL: c.ResourceURL, Issuer: c.Issuer}, c.Client)
	if e != nil {
		Error(w, r, 502, "TOOL_REGISTRATION_FAILED", "tool registration is unavailable")
		return
	}
	current, e := h.Store.Get(r.Context(), key.Owner.TenantID, "tool-connection", c.ID)
	if e != nil || current.Revision != rec.Revision {
		Error(w, r, 409, "TOOL_CONFIGURATION_CHANGED", "tool connection changed during authorization")
		return
	}
	challenge, e := h.ToolSessions.Begin(r.Context(), toolauth.AuthorizationStart{Key: key, Registration: reg, RedirectURI: input.RedirectURI, Scopes: c.Client.Scopes})
	if e != nil {
		toolAuthorizationError(w, r, e)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	JSON(w, 200, challenge)
}
func (h *ConnectedHandler) CompleteToolAuthorization(w http.ResponseWriter, r *http.Request) {
	_, _, key, ok := h.toolAuthorizationContext(w, r)
	if !ok {
		return
	}
	var input toolauth.AuthorizationResponse
	if decodeToolAuthorization(r, &input) != nil {
		Error(w, r, 400, "INVALID_REQUEST", "authorization response fields required")
		return
	}
	info, e := h.ToolSessions.Complete(r.Context(), key, input)
	if e != nil {
		toolAuthorizationError(w, r, e)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	JSON(w, 200, info)
}
func (h *ConnectedHandler) GetToolAuthorization(w http.ResponseWriter, r *http.Request) {
	_, _, key, ok := h.toolAuthorizationContext(w, r)
	if !ok {
		return
	}
	info, e := h.ToolSessions.Status(r.Context(), key)
	if e != nil {
		Error(w, r, 404, "TOOL_AUTHORIZATION_REQUIRED", "no active authorization for this verified owner and connection")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	JSON(w, 200, info)
}
func (h *ConnectedHandler) RevokeToolAuthorization(w http.ResponseWriter, r *http.Request) {
	_, _, key, ok := h.toolAuthorizationContext(w, r)
	if !ok {
		return
	}
	if e := h.ToolSessions.Revoke(r.Context(), key); e != nil {
		toolAuthorizationError(w, r, e)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func toolAuthorizationError(w http.ResponseWriter, r *http.Request, e error) {
	// Provider bodies, authorization codes and tokens never enter public errors.
	code := "TOOL_AUTHORIZATION_FAILED"
	if typed, ok := e.(*toolauth.RuntimeError); ok {
		code = typed.Code
	}
	Error(w, r, 409, code, "tool authorization could not be completed; inspect authorization status or start a new grant")
}
