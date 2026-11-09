// Package contracts contains wire types shared by independently deployed AIPlex
// modules. Identity providers and accounting implementations stay outside it.
package contracts

import (
	"fmt"
	"time"
)

const Version = "v1"

type SnapshotRef struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

// RequestContext is supplied by a trusted gateway after authentication. Decoding
// or calling Validate does not authenticate it. Modules must verify the calling
// service and bind this context to the authenticated request before using it.
type RequestContext struct {
	Version               string       `json:"version"`
	TenantID              string       `json:"tenant_id"`
	RequestID             string       `json:"request_id"`
	PrincipalID           string       `json:"principal_id"`
	ActorID               string       `json:"actor_id,omitempty"`
	MembershipSnapshotRef *SnapshotRef `json:"membership_snapshot_ref,omitempty"`
	ApplicationID         string       `json:"application_id,omitempty"`
	ResourceID            string       `json:"resource_id"`
	Purpose               string       `json:"purpose,omitempty"`
}

func (r RequestContext) Validate() error {
	if r.Version != Version {
		return fmt.Errorf("unsupported request context version %q", r.Version)
	}
	if r.TenantID == "" || r.RequestID == "" || r.PrincipalID == "" || r.ResourceID == "" {
		return fmt.Errorf("request context requires tenant, request, principal, and resource identifiers")
	}
	if ref := r.MembershipSnapshotRef; ref != nil && (ref.ID == "" || ref.Version == "") {
		return fmt.Errorf("membership snapshot reference requires ID and version")
	}
	return nil
}

// MembershipSnapshot records the authoritative membership view used for a
// decision. Membership group identifiers must be namespaced by their source.
type MembershipSnapshot struct {
	Version     string      `json:"version"`
	Ref         SnapshotRef `json:"ref"`
	TenantID    string      `json:"tenant_id"`
	PrincipalID string      `json:"principal_id"`
	Groups      []string    `json:"groups"`
	IssuedAt    time.Time   `json:"issued_at"`
	ExpiresAt   time.Time   `json:"expires_at"`
}

func (s MembershipSnapshot) ValidateFor(r RequestContext, now time.Time) error {
	if err := r.Validate(); err != nil {
		return err
	}
	if s.Version != Version || s.Ref.ID == "" || s.Ref.Version == "" {
		return fmt.Errorf("invalid membership snapshot version or reference")
	}
	if s.TenantID != r.TenantID || s.PrincipalID != r.PrincipalID {
		return fmt.Errorf("membership snapshot identity mismatch")
	}
	if r.MembershipSnapshotRef == nil || *r.MembershipSnapshotRef != s.Ref {
		return fmt.Errorf("membership snapshot reference mismatch")
	}
	if s.IssuedAt.IsZero() || s.IssuedAt.After(now) || !s.ExpiresAt.After(now) || !s.ExpiresAt.After(s.IssuedAt) {
		return fmt.Errorf("membership snapshot is not currently valid")
	}
	seen := map[string]bool{}
	for _, g := range s.Groups {
		if g == "" || seen[g] {
			return fmt.Errorf("invalid or duplicate membership group %q", g)
		}
		seen[g] = true
	}
	return nil
}
