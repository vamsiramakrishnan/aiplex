package userfederation

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

// Resource kinds. Providers are tenant-scoped records in the generic resource
// store; the source index enforces one provider per issuer+audience per tenant.
const (
	ProviderKind    = "user-identity-provider"
	SourceIndexKind = "user-identity-provider-source"
)

var (
	ErrDuplicateSource = errors.New("issuer and audience are already configured")
	ErrNotFound        = resources.ErrNotFound
	ErrConflict        = resources.ErrConflict
)

// DuplicateSourceError names the provider that already owns the issuer+audience.
type DuplicateSourceError struct{ ProviderID string }

func (e *DuplicateSourceError) Error() string {
	return fmt.Sprintf("issuer and audience are already trusted by user identity provider %q", e.ProviderID)
}
func (e *DuplicateSourceError) Unwrap() error { return ErrDuplicateSource }

var providerID = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,62}[a-z0-9])?$`)

// ValidProviderID reports whether id is a lower-case DNS label of at most 64
// characters. "verify" is reserved for the dry-run route.
func ValidProviderID(id string) bool { return providerID.MatchString(id) && id != "verify" }

// ProviderRequest is the operator-supplied part of a provider (PUT body).
type ProviderRequest struct {
	Preset         string            `json:"preset"`
	Inputs         map[string]string `json:"inputs"`
	RequiredClaims map[string]string `json:"required_claims,omitempty"`
	DisplayName    string            `json:"display_name,omitempty"`
	Disabled       bool              `json:"disabled,omitempty"`
}

// Provider is the persisted user-identity-provider resource: request plus the
// derived trust tuple, which is always recomputed server-side.
type Provider struct {
	ID          string            `json:"id"`
	Preset      string            `json:"preset"`
	Inputs      map[string]string `json:"inputs"`
	DisplayName string            `json:"display_name,omitempty"`
	Disabled    bool              `json:"disabled"`
	Trust
	// ExtraClaims are the operator-added required claims (subset of RequiredClaims).
	ExtraClaims map[string]string `json:"extra_claims,omitempty"`
	Revision    uint64            `json:"revision"`
}

// NewProvider derives and validates a provider from a request.
func NewProvider(id string, req ProviderRequest) (Provider, error) {
	if !ValidProviderID(id) {
		return Provider{}, fmt.Errorf("provider id must be a lower-case DNS label of at most 64 characters (and not \"verify\")")
	}
	if len(req.DisplayName) > 256 {
		return Provider{}, fmt.Errorf("display_name is too long")
	}
	trust, err := Derive(req.Preset, req.Inputs, req.RequiredClaims)
	if err != nil {
		return Provider{}, err
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
	return Provider{ID: id, Preset: req.Preset, Inputs: inputs, DisplayName: req.DisplayName, Disabled: req.Disabled, Trust: trust, ExtraClaims: extra}, nil
}

// SourceKey identifies one issuer+audience within a tenant.
func SourceKey(issuer, audience string) string {
	sum := sha256.Sum256([]byte(issuer + "\x00" + audience))
	return hex.EncodeToString(sum[:])
}

func (p Provider) sourceKey() string { return SourceKey(p.Issuer, p.Audience) }

type sourceIndex struct {
	ProviderID string `json:"provider_id"`
	Issuer     string `json:"issuer"`
	Audience   string `json:"audience"`
	ReservedAt int64  `json:"reserved_at"`
}

// staleReservation bounds how long a reservation whose provider write has not
// landed blocks others; it separates an in-flight create from an orphan.
const staleReservation = time.Minute

// Store persists providers tenant-scoped in a resources.Store.
type Store struct{ resources resources.Store }

func NewStore(store resources.Store) *Store { return &Store{resources: store} }

func decodeProvider(rec resources.Record) (Provider, error) {
	var p Provider
	if err := json.Unmarshal(rec.Spec, &p); err != nil {
		return Provider{}, fmt.Errorf("invalid stored user identity provider")
	}
	p.Revision = rec.Revision
	return p, nil
}

// Get returns one provider.
func (s *Store) Get(ctx context.Context, tenant, id string) (Provider, error) {
	rec, err := s.resources.Get(ctx, tenant, ProviderKind, id)
	if err != nil {
		return Provider{}, err
	}
	p, err := decodeProvider(rec)
	if err != nil {
		return Provider{}, err
	}
	if p.ID != id {
		return Provider{}, ErrNotFound
	}
	return p, nil
}

// List returns the tenant's providers ordered by ID.
func (s *Store) List(ctx context.Context, tenant string) ([]Provider, error) {
	records, err := s.resources.List(ctx, tenant, ProviderKind)
	if err != nil {
		return nil, err
	}
	out := []Provider{}
	for _, rec := range records {
		if p, err := decodeProvider(rec); err == nil && p.ID == rec.ID {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// ByIssuer returns the tenant's providers that pin issuer.
func (s *Store) ByIssuer(ctx context.Context, tenant, issuer string) ([]Provider, error) {
	all, err := s.List(ctx, tenant)
	if err != nil {
		return nil, err
	}
	out := []Provider{}
	for _, p := range all {
		if p.Issuer == issuer {
			out = append(out, p)
		}
	}
	return out, nil
}

func (s *Store) claimSource(ctx context.Context, tenant string, p Provider) (created bool, err error) {
	key := p.sourceKey()
	payload, _ := json.Marshal(sourceIndex{ProviderID: p.ID, Issuer: p.Issuer, Audience: p.Audience, ReservedAt: time.Now().Unix()})
	for attempt := 0; attempt < 3; attempt++ {
		rec, err := s.resources.Get(ctx, tenant, SourceIndexKind, key)
		if errors.Is(err, resources.ErrNotFound) {
			_, err = s.resources.Put(ctx, resources.Record{TenantID: tenant, Kind: SourceIndexKind, ID: key, Spec: payload}, 0)
			if errors.Is(err, resources.ErrConflict) {
				continue
			}
			return err == nil, err
		}
		if err != nil {
			return false, err
		}
		var idx sourceIndex
		if json.Unmarshal(rec.Spec, &idx) != nil {
			return false, fmt.Errorf("invalid user identity source index")
		}
		if idx.ProviderID == p.ID {
			return false, nil
		}
		owner, err := s.Get(ctx, tenant, idx.ProviderID)
		if err == nil && owner.sourceKey() == key {
			return false, &DuplicateSourceError{ProviderID: idx.ProviderID}
		}
		if err != nil && !errors.Is(err, resources.ErrNotFound) {
			return false, err
		}
		if err != nil && time.Since(time.Unix(idx.ReservedAt, 0)) < staleReservation {
			return false, &DuplicateSourceError{ProviderID: idx.ProviderID}
		}
		if _, err = s.resources.Put(ctx, resources.Record{TenantID: tenant, Kind: SourceIndexKind, ID: key, Spec: payload}, rec.Revision); errors.Is(err, resources.ErrConflict) {
			continue
		} else if err != nil {
			return false, err
		}
		return true, nil
	}
	return false, fmt.Errorf("user identity source index is contended; retry")
}

func (s *Store) releaseSource(ctx context.Context, tenant, key, id string) {
	rec, err := s.resources.Get(ctx, tenant, SourceIndexKind, key)
	if err != nil {
		return
	}
	var idx sourceIndex
	if json.Unmarshal(rec.Spec, &idx) != nil || idx.ProviderID != id {
		return
	}
	_ = s.resources.Delete(ctx, tenant, SourceIndexKind, key, rec.Revision)
}

// Put creates (expected=0) or replaces (expected=current revision) a provider,
// rejecting an issuer+audience already trusted by another provider in the tenant.
func (s *Store) Put(ctx context.Context, tenant string, p Provider, expected uint64) (Provider, error) {
	var previous *Provider
	if expected != 0 {
		current, err := s.Get(ctx, tenant, p.ID)
		if err != nil {
			if errors.Is(err, resources.ErrNotFound) {
				return Provider{}, resources.ErrConflict
			}
			return Provider{}, err
		}
		if current.Revision != expected {
			return Provider{}, resources.ErrConflict
		}
		previous = &current
	}
	created, err := s.claimSource(ctx, tenant, p)
	if err != nil {
		return Provider{}, err
	}
	stored := p
	stored.Revision = 0
	payload, err := json.Marshal(stored)
	if err != nil {
		return Provider{}, err
	}
	rec, err := s.resources.Put(ctx, resources.Record{TenantID: tenant, Kind: ProviderKind, ID: p.ID, Spec: payload}, expected)
	if err != nil {
		if created {
			s.releaseSource(ctx, tenant, p.sourceKey(), p.ID)
		}
		return Provider{}, err
	}
	if previous != nil && previous.sourceKey() != p.sourceKey() {
		s.releaseSource(ctx, tenant, previous.sourceKey(), p.ID)
	}
	return decodeProvider(rec)
}

// Delete removes a provider at an expected revision and releases its source.
func (s *Store) Delete(ctx context.Context, tenant, id string, expected uint64) error {
	current, err := s.Get(ctx, tenant, id)
	if err != nil {
		return err
	}
	if err := s.resources.Delete(ctx, tenant, ProviderKind, id, expected); err != nil {
		return err
	}
	s.releaseSource(ctx, tenant, current.sourceKey(), id)
	return nil
}

// Tenant returns a ProviderFinder scoped to one tenant.
func (s *Store) Tenant(tenant string) ProviderFinder { return tenantFinder{s, tenant} }

type tenantFinder struct {
	store  *Store
	tenant string
}

func (f tenantFinder) ByIssuer(ctx context.Context, issuer string) ([]Provider, error) {
	return f.store.ByIssuer(ctx, f.tenant, issuer)
}
