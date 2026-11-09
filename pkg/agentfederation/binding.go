package agentfederation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
)

// Resource kinds. Bindings are tenant-scoped records in the generic resource
// store; the subject index enforces one external subject per tenant.
const (
	BindingKind      = "agent-identity"
	SubjectIndexKind = "agent-identity-subject"
)

var (
	ErrDuplicateSubject = errors.New("external subject is already bound")
	ErrNotFound         = resources.ErrNotFound
	ErrConflict         = resources.ErrConflict
)

// DuplicateSubjectError names the binding that already owns the subject.
type DuplicateSubjectError struct {
	ClientID  string
	BindingID string
}

func (e *DuplicateSubjectError) Error() string {
	return fmt.Sprintf("external subject is already bound to agent %q by identity binding %q", e.ClientID, e.BindingID)
}
func (e *DuplicateSubjectError) Unwrap() error { return ErrDuplicateSubject }

var bindingID = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,62}[a-z0-9])?$`)

// ValidBindingID reports whether id is a valid binding identifier
// (lower-case DNS label, at most 64 characters).
func ValidBindingID(id string) bool { return bindingID.MatchString(id) }

// BindingRequest is the operator-supplied part of a binding (PUT body).
type BindingRequest struct {
	Preset         string            `json:"preset"`
	Inputs         map[string]string `json:"inputs"`
	RequiredClaims map[string]string `json:"required_claims,omitempty"`
	DisplayName    string            `json:"display_name,omitempty"`
	Disabled       bool              `json:"disabled,omitempty"`
}

// Binding is the persisted agent-identity resource: request plus the derived
// trust tuple. Derived fields are always recomputed server-side.
type Binding struct {
	ID          string            `json:"id"`
	ClientID    string            `json:"client_id"`
	Preset      string            `json:"preset"`
	Inputs      map[string]string `json:"inputs"`
	DisplayName string            `json:"display_name,omitempty"`
	Disabled    bool              `json:"disabled"`
	Trust
	// ExtraClaims are the operator-added required claims (subset of RequiredClaims).
	ExtraClaims map[string]string `json:"extra_claims,omitempty"`
	Revision    uint64            `json:"revision"`
}

// NewBinding derives and validates a binding from a request.
func NewBinding(clientID, id string, req BindingRequest, defaultAudience string) (Binding, error) {
	if !ValidBindingID(id) {
		return Binding{}, fmt.Errorf("binding id must be a lower-case DNS label of at most 64 characters")
	}
	if clientID == "" || len(clientID) > 128 {
		return Binding{}, fmt.Errorf("agent client id required")
	}
	if len(req.DisplayName) > 256 {
		return Binding{}, fmt.Errorf("display_name is too long")
	}
	trust, err := Derive(req.Preset, req.Inputs, req.RequiredClaims, defaultAudience)
	if err != nil {
		return Binding{}, err
	}
	inputs := map[string]string{}
	for k, v := range req.Inputs {
		if v != "" {
			inputs[k] = v
		}
	}
	var extra map[string]string
	if len(req.RequiredClaims) > 0 {
		extra = map[string]string{}
		for k, v := range req.RequiredClaims {
			extra[k] = v
		}
	}
	return Binding{ID: id, ClientID: clientID, Preset: req.Preset, Inputs: inputs, DisplayName: req.DisplayName, Disabled: req.Disabled, Trust: trust, ExtraClaims: extra}, nil
}

func recordID(clientID, id string) string { return clientID + "/" + id }

// SubjectKey identifies one external subject within a tenant.
func SubjectKey(issuer, claim, subject string) string {
	sum := sha256.Sum256([]byte(issuer + "\x00" + claim + "\x00" + subject))
	return hex.EncodeToString(sum[:])
}

func (b Binding) subjectKey() string { return SubjectKey(b.Issuer, b.SubjectClaim, b.Subject) }

type subjectIndex struct {
	ClientID     string `json:"client_id"`
	BindingID    string `json:"binding_id"`
	Issuer       string `json:"issuer"`
	SubjectClaim string `json:"subject_claim"`
	Subject      string `json:"subject"`
	ReservedAt   int64  `json:"reserved_at"`
}

// staleReservation bounds how long a reservation whose binding write has not
// (yet) landed blocks other bindings. It distinguishes an in-flight create from
// an entry orphaned by a failed write.
const staleReservation = time.Minute

// Store persists bindings tenant-scoped in a resources.Store.
type Store struct{ resources resources.Store }

func NewStore(store resources.Store) *Store { return &Store{resources: store} }

func decodeBinding(rec resources.Record) (Binding, error) {
	var b Binding
	if err := json.Unmarshal(rec.Spec, &b); err != nil {
		return Binding{}, fmt.Errorf("invalid stored identity binding")
	}
	b.Revision = rec.Revision
	return b, nil
}

// Get returns one binding of one agent.
func (s *Store) Get(ctx context.Context, tenant, clientID, id string) (Binding, error) {
	rec, err := s.resources.Get(ctx, tenant, BindingKind, recordID(clientID, id))
	if err != nil {
		return Binding{}, err
	}
	b, err := decodeBinding(rec)
	if err != nil {
		return Binding{}, err
	}
	if b.ClientID != clientID || b.ID != id {
		return Binding{}, ErrNotFound
	}
	return b, nil
}

// List returns the tenant's bindings, optionally only for one agent.
func (s *Store) List(ctx context.Context, tenant, clientID string) ([]Binding, error) {
	records, err := s.resources.List(ctx, tenant, BindingKind)
	if err != nil {
		return nil, err
	}
	out := []Binding{}
	for _, rec := range records {
		b, err := decodeBinding(rec)
		if err != nil || recordID(b.ClientID, b.ID) != rec.ID {
			continue
		}
		if clientID == "" || b.ClientID == clientID {
			out = append(out, b)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return recordID(out[i].ClientID, out[i].ID) < recordID(out[j].ClientID, out[j].ID)
	})
	return out, nil
}

// ByIssuer returns the tenant's bindings that pin issuer.
func (s *Store) ByIssuer(ctx context.Context, tenant, issuer string) ([]Binding, error) {
	all, err := s.List(ctx, tenant, "")
	if err != nil {
		return nil, err
	}
	out := []Binding{}
	for _, b := range all {
		if b.Issuer == issuer {
			out = append(out, b)
		}
	}
	return out, nil
}

// FindBySubject resolves the binding that owns (issuer, claim, subject) in a
// tenant. Stale index entries (binding deleted or changed) resolve to not found.
func (s *Store) FindBySubject(ctx context.Context, tenant, issuer, claim, subject string) (Binding, error) {
	idx, err := s.readIndex(ctx, tenant, SubjectKey(issuer, claim, subject))
	if err != nil {
		return Binding{}, err
	}
	b, err := s.Get(ctx, tenant, idx.ClientID, idx.BindingID)
	if err != nil {
		return Binding{}, err
	}
	if b.Issuer != issuer || b.SubjectClaim != claim || b.Subject != subject {
		return Binding{}, ErrNotFound
	}
	return b, nil
}

func (s *Store) readIndex(ctx context.Context, tenant, key string) (subjectIndex, error) {
	rec, err := s.resources.Get(ctx, tenant, SubjectIndexKind, key)
	if err != nil {
		return subjectIndex{}, err
	}
	var idx subjectIndex
	if json.Unmarshal(rec.Spec, &idx) != nil {
		return subjectIndex{}, fmt.Errorf("invalid identity subject index")
	}
	return idx, nil
}

// claimSubject reserves the binding's subject key. It succeeds when the key
// is free, already owned by this binding, or held by a stale entry.
func (s *Store) claimSubject(ctx context.Context, tenant string, b Binding) (created bool, err error) {
	key := b.subjectKey()
	payload, _ := json.Marshal(subjectIndex{ClientID: b.ClientID, BindingID: b.ID, Issuer: b.Issuer, SubjectClaim: b.SubjectClaim, Subject: b.Subject, ReservedAt: time.Now().Unix()})
	for attempt := 0; attempt < 3; attempt++ {
		rec, err := s.resources.Get(ctx, tenant, SubjectIndexKind, key)
		if errors.Is(err, resources.ErrNotFound) {
			_, err = s.resources.Put(ctx, resources.Record{TenantID: tenant, Kind: SubjectIndexKind, ID: key, Spec: payload}, 0)
			if errors.Is(err, resources.ErrConflict) {
				continue
			}
			return err == nil, err
		}
		if err != nil {
			return false, err
		}
		var idx subjectIndex
		if json.Unmarshal(rec.Spec, &idx) != nil {
			return false, fmt.Errorf("invalid identity subject index")
		}
		if idx.ClientID == b.ClientID && idx.BindingID == b.ID {
			return false, nil
		}
		owner, err := s.Get(ctx, tenant, idx.ClientID, idx.BindingID)
		if err == nil && owner.subjectKey() == key {
			return false, &DuplicateSubjectError{ClientID: idx.ClientID, BindingID: idx.BindingID}
		}
		if err != nil && !errors.Is(err, resources.ErrNotFound) {
			return false, err
		}
		if err != nil && time.Since(time.Unix(idx.ReservedAt, 0)) < staleReservation {
			// The owning binding may still be being written by a concurrent request.
			return false, &DuplicateSubjectError{ClientID: idx.ClientID, BindingID: idx.BindingID}
		}
		// Stale entry: take it over with compare-and-swap.
		if _, err = s.resources.Put(ctx, resources.Record{TenantID: tenant, Kind: SubjectIndexKind, ID: key, Spec: payload}, rec.Revision); errors.Is(err, resources.ErrConflict) {
			continue
		} else if err != nil {
			return false, err
		}
		return true, nil
	}
	return false, fmt.Errorf("identity subject index is contended; retry")
}

// releaseSubject removes key when it still points at (clientID, id).
func (s *Store) releaseSubject(ctx context.Context, tenant, key, clientID, id string) {
	rec, err := s.resources.Get(ctx, tenant, SubjectIndexKind, key)
	if err != nil {
		return
	}
	var idx subjectIndex
	if json.Unmarshal(rec.Spec, &idx) != nil || idx.ClientID != clientID || idx.BindingID != id {
		return
	}
	_ = s.resources.Delete(ctx, tenant, SubjectIndexKind, key, rec.Revision)
}

// Put creates (expected=0) or replaces (expected=current revision) a binding,
// rejecting an external subject already bound by another binding in the tenant.
func (s *Store) Put(ctx context.Context, tenant string, b Binding, expected uint64) (Binding, error) {
	var previous *Binding
	if expected != 0 {
		current, err := s.Get(ctx, tenant, b.ClientID, b.ID)
		if err != nil {
			if errors.Is(err, resources.ErrNotFound) {
				return Binding{}, resources.ErrConflict
			}
			return Binding{}, err
		}
		if current.Revision != expected {
			return Binding{}, resources.ErrConflict
		}
		previous = &current
	}
	created, err := s.claimSubject(ctx, tenant, b)
	if err != nil {
		return Binding{}, err
	}
	stored := b
	stored.Revision = 0
	payload, err := json.Marshal(stored)
	if err != nil {
		return Binding{}, err
	}
	rec, err := s.resources.Put(ctx, resources.Record{TenantID: tenant, Kind: BindingKind, ID: recordID(b.ClientID, b.ID), Spec: payload}, expected)
	if err != nil {
		if created {
			s.releaseSubject(ctx, tenant, b.subjectKey(), b.ClientID, b.ID)
		}
		return Binding{}, err
	}
	if previous != nil && previous.subjectKey() != b.subjectKey() {
		s.releaseSubject(ctx, tenant, previous.subjectKey(), b.ClientID, b.ID)
	}
	return decodeBinding(rec)
}

// Delete removes a binding at an expected revision and releases its subject.
func (s *Store) Delete(ctx context.Context, tenant, clientID, id string, expected uint64) error {
	current, err := s.Get(ctx, tenant, clientID, id)
	if err != nil {
		return err
	}
	if err := s.resources.Delete(ctx, tenant, BindingKind, recordID(clientID, id), expected); err != nil {
		return err
	}
	s.releaseSubject(ctx, tenant, current.subjectKey(), clientID, id)
	return nil
}

// Tenant returns a BindingFinder scoped to one tenant.
func (s *Store) Tenant(tenant string) BindingFinder { return tenantFinder{s, tenant} }

type tenantFinder struct {
	store  *Store
	tenant string
}

func (f tenantFinder) FindBySubject(ctx context.Context, issuer, claim, subject string) (Binding, error) {
	return f.store.FindBySubject(ctx, f.tenant, issuer, claim, subject)
}
func (f tenantFinder) ByIssuer(ctx context.Context, issuer string) ([]Binding, error) {
	return f.store.ByIssuer(ctx, f.tenant, issuer)
}
