package aiplex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// IAM role bindings map identity-provider groups to AIPlex roles. They decide
// who is an administrator, so every call here is admin-only on the server and
// the client refuses what the server would refuse. See docs/authorization.md.

// RoleInfo is one AIPlex role and the scopes it grants by default.
type RoleInfo struct {
	Role          string   `json:"role"`
	DefaultScopes []string `json:"default_scopes"`
}

// RoleBinding is a stored group-to-role mapping. Revision is the optimistic
// concurrency token for update and delete.
type RoleBinding struct {
	ID          string    `json:"id"`
	Revision    int64     `json:"revision"`
	Group       string    `json:"group"`
	Role        string    `json:"role"`
	Provider    string    `json:"provider,omitempty"`
	PoolID      string    `json:"pool_id,omitempty"`
	Scopes      []string  `json:"scopes"`
	Description string    `json:"description,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	CreatedBy   string    `json:"created_by"`
}

// RoleBindingInput is the writable part of a role binding. The server sets
// the creation time and creator and ignores any the caller sends, so they are
// not fields here.
//
// Scopes nil means "not specified": on create the server uses the role's
// default scopes; on update it keeps the stored scopes (or the new role's
// defaults when the role changes). A non-nil empty slice means no scopes.
// Provider and PoolID, when set, restrict the binding to one issuer or pool.
// Update replaces Group, Role, Provider, PoolID and Description with the
// values given, so an empty string clears one.
type RoleBindingInput struct {
	// ID is optional on create (the server derives one from the binding's
	// identity) and must be empty or equal to the path ID on update.
	ID          string
	Group       string
	Role        string
	Provider    string
	PoolID      string
	Scopes      []string
	Description string
}

// MarshalJSON writes every field except Scopes when it is nil, so "not
// specified" and "empty" stay different on the wire.
func (in RoleBindingInput) MarshalJSON() ([]byte, error) {
	type wire struct {
		ID          string    `json:"id,omitempty"`
		Group       string    `json:"group"`
		Role        string    `json:"role"`
		Provider    string    `json:"provider"`
		PoolID      string    `json:"pool_id"`
		Description string    `json:"description"`
		Scopes      *[]string `json:"scopes,omitempty"`
	}
	w := wire{ID: in.ID, Group: in.Group, Role: in.Role, Provider: in.Provider, PoolID: in.PoolID, Description: in.Description}
	if in.Scopes != nil {
		w.Scopes = &in.Scopes
	}
	return json.Marshal(w)
}

// PrincipalValidation is the answer of ValidatePrincipal. An invalid
// principal is a successful call with Valid false and the reason in Error.
type PrincipalValidation struct {
	Valid     bool   `json:"valid"`
	Principal string `json:"principal,omitempty"`
	Error     string `json:"error,omitempty"`
}

func roleBindingPath(id string) (string, error) {
	if err := validateResourceID(id); err != nil {
		return "", err
	}
	if err := validateRoleBindingID(id); err != nil {
		return "", err
	}
	return "/api/v1/iam/role-bindings/" + url.PathEscape(id), nil
}

func validateRoleBindingID(id string) error {
	if strings.ContainsAny(id, "/\\\x00\r\n") || len(id) > 256 {
		return fmt.Errorf("aiplex: invalid role binding ID")
	}
	return nil
}

// Validate mirrors the server's checks (internal/api/iam.go) so a request the
// server would refuse never leaves the client, and refuses an ID the client
// could not then read or delete. Scope syntax is left to the server.
func (in RoleBindingInput) Validate() error {
	if strings.TrimSpace(in.Group) == "" || len(in.Group) > 512 || strings.ContainsAny(in.Group, "\x00\r\n") {
		return fmt.Errorf("aiplex: bounded group identity is required")
	}
	switch in.Role {
	case "admin", "deployer", "viewer", "agent":
	default:
		return fmt.Errorf("aiplex: invalid role; expected admin, deployer, viewer or agent")
	}
	if len(in.Provider) > 2048 || len(in.PoolID) > 512 || strings.ContainsAny(in.Provider+in.PoolID, "\x00\r\n") {
		return fmt.Errorf("aiplex: invalid identity source restriction")
	}
	if in.ID != "" {
		if err := validateResourceID(in.ID); err != nil {
			return err
		}
		return validateRoleBindingID(in.ID)
	}
	return nil
}

// normalize makes Scopes an empty list rather than null, so a binding with no
// scopes is shown, and written back by 'get -o yaml | apply -f -', as [].
func (b *RoleBinding) normalize() {
	if b.Scopes == nil {
		b.Scopes = []string{}
	}
}

// ListRoles returns the AIPlex roles and their default scopes.
func (c *Client) ListRoles(ctx context.Context) ([]RoleInfo, error) {
	out := []RoleInfo{}
	if err := c.do(ctx, http.MethodGet, "/api/v1/iam/roles", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ListRoleBindings returns every role binding (admin only), or those for one
// group when group is not empty.
func (c *Client) ListRoleBindings(ctx context.Context, group string) ([]RoleBinding, error) {
	path := "/api/v1/iam/role-bindings"
	if group != "" {
		path += "?group=" + url.QueryEscape(group)
	}
	out := []RoleBinding{}
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []RoleBinding{}
	}
	for i := range out {
		out[i].normalize()
	}
	return out, nil
}

// GetRoleBinding returns one role binding including its current Revision.
func (c *Client) GetRoleBinding(ctx context.Context, id string) (*RoleBinding, error) {
	path, err := roleBindingPath(id)
	if err != nil {
		return nil, err
	}
	var out RoleBinding
	if err = c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	out.normalize()
	return &out, nil
}

// CreateRoleBinding creates a role binding. An existing binding with the same
// ID is a conflict, never an overwrite.
func (c *Client) CreateRoleBinding(ctx context.Context, in RoleBindingInput) (*RoleBinding, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}
	var out RoleBinding
	if err := c.doWithHeaders(ctx, http.MethodPost, "/api/v1/iam/role-bindings", in, &out, map[string]string{"If-Match": "0"}); err != nil {
		return nil, err
	}
	out.normalize()
	return &out, nil
}

// UpdateRoleBinding replaces the binding id at expectedRevision, which must
// equal the stored revision (read it with GetRoleBinding). A stale revision is
// refused with a 412 REVISION_CONFLICT.
func (c *Client) UpdateRoleBinding(ctx context.Context, id string, in RoleBindingInput, expectedRevision int64) (*RoleBinding, error) {
	path, err := roleBindingPath(id)
	if err != nil {
		return nil, err
	}
	if in.ID != "" && in.ID != id {
		return nil, fmt.Errorf("aiplex: role binding ID %q does not match %q; the ID cannot change", in.ID, id)
	}
	if err = in.Validate(); err != nil {
		return nil, err
	}
	if expectedRevision <= 0 {
		return nil, fmt.Errorf("aiplex: positive expected revision required")
	}
	var out RoleBinding
	if err = c.doWithHeaders(ctx, http.MethodPut, path, in, &out, map[string]string{"If-Match": strconv.FormatInt(expectedRevision, 10)}); err != nil {
		return nil, err
	}
	out.normalize()
	return &out, nil
}

// DeleteRoleBinding removes a role binding; the current revision is required.
// People who held a role only through it lose that role on their next request.
func (c *Client) DeleteRoleBinding(ctx context.Context, id string, expectedRevision int64) error {
	path, err := roleBindingPath(id)
	if err != nil {
		return err
	}
	if expectedRevision <= 0 {
		return fmt.Errorf("aiplex: positive expected revision required")
	}
	return c.doWithHeaders(ctx, http.MethodDelete, path, nil, nil, map[string]string{"If-Match": strconv.FormatInt(expectedRevision, 10)})
}

// ValidatePrincipal checks a Google Cloud workload or workforce identity
// principal string without creating anything (admin only).
func (c *Client) ValidatePrincipal(ctx context.Context, principal string) (*PrincipalValidation, error) {
	if principal == "" {
		return nil, fmt.Errorf("aiplex: principal is required")
	}
	var out PrincipalValidation
	if err := c.do(ctx, http.MethodPost, "/api/v1/iam/validate-principal", map[string]string{"principal": principal}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
