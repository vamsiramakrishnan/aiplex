package auth

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/directory"
	"github.com/vamsiramakrishnan/aiplex/pkg/membership"
	"github.com/vamsiramakrishnan/aiplex/pkg/userfederation"
)

// UserDirectories resolves federated users' groups through the directory
// configured on their user identity provider (pkg/directory), behind one
// membership.Cache per provider. It implements userfederation.DirectoryResolver
// for both live authentication and dry-run verification, so both observe the
// same cached view.
//
// Precedence with a quota-level membership authority (cmd/aiplex-api
// membership deployment config, auth.DirectoryMembershipResolver): the two
// answer different questions and never merge.
//
//   - The provider directory produces the identity's group view at
//     authentication. Role-binding resolution always uses it, and quota uses it
//     through the verified-claims path (PrincipalMembership) when no quota
//     membership authority is configured.
//   - When a quota membership authority IS configured, it is the sole authority
//     for quota eligibility (it is pinned in the quota host's durable policy
//     epoch); the identity's directory groups are not consulted by quota, and
//     the quota authority's groups never flow into role bindings.
//
// Operators who want one view for both should leave the quota membership
// authority unset and rely on the provider directory.
//
// Caching is process-local. Invalidate fences one subject on this process;
// multi-replica deployments must invalidate on every replica (or rely on the
// cache TTL, at most five minutes).
type UserDirectories struct {
	Deps directory.Deps
	// CacheTTL bounds how long a resolved view is reused (default 1m, max 5m).
	CacheTTL time.Duration
	// Capacity bounds cached subjects per provider (default 1024).
	Capacity int

	mu      sync.Mutex
	entries map[string]*userDirectory
}

type userDirectory struct {
	digest    string
	authority *directory.Authority
	cache     *membership.Cache
}

// ErrNoDirectory means the provider has no directory block.
var ErrNoDirectory = errors.New("user identity provider has no directory")

func (d *UserDirectories) entry(tenant string, p userfederation.Provider) (*userDirectory, error) {
	if d == nil {
		return nil, fmt.Errorf("directory lookups are not configured on this server")
	}
	if p.Directory == nil {
		return nil, ErrNoDirectory
	}
	target := directory.Target{Tenant: tenant, Issuer: p.Issuer, PoolID: UserFederationPoolID(tenant), EntraTenant: p.RequiredClaims["tid"], Domain: p.RequiredClaims["hd"]}
	digest := membership.Digest(struct {
		Spec   directory.Spec
		Target directory.Target
	}{*p.Directory, target})
	key := tenant + "\x00" + p.ID
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.entries == nil {
		d.entries = map[string]*userDirectory{}
	}
	if e := d.entries[key]; e != nil && e.digest == digest {
		return e, nil
	}
	// A changed directory configuration starts from an empty cache: views
	// obtained under the old credential or kind are never reused.
	authority, err := directory.New(*p.Directory, target, d.Deps)
	if err != nil {
		return nil, err
	}
	ttl := d.CacheTTL
	if ttl == 0 {
		ttl = time.Minute
	}
	capacity := d.Capacity
	if capacity == 0 {
		capacity = 1024
	}
	cache, err := membership.NewCache(authority, ttl, capacity)
	if err != nil {
		return nil, err
	}
	if len(d.entries) >= 1024 {
		d.entries = map[string]*userDirectory{}
	}
	e := &userDirectory{digest: digest, authority: authority, cache: cache}
	d.entries[key] = e
	return e, nil
}

// DirectoryQuery is the membership query for a federated user of a provider:
// the canonical principal, the verified native subject, role principal.
func DirectoryQuery(tenant string, p userfederation.Provider, subject string) membership.Query {
	pool := UserFederationPoolID(tenant)
	return membership.Query{TenantID: tenant, SubjectID: CanonicalPrincipalID(p.Issuer, pool, subject), Kind: "human", Issuer: p.Issuer, PoolID: pool, NativeSubject: subject, Role: "principal"}
}

// ResolveGroups implements userfederation.DirectoryResolver. Errors carry only
// the directory package's safe reason.
func (d *UserDirectories) ResolveGroups(ctx context.Context, tenant string, p userfederation.Provider, lookup userfederation.DirectoryLookup, now time.Time) (userfederation.Groups, error) {
	e, err := d.entry(tenant, p)
	if err != nil {
		return userfederation.Groups{}, errors.New(safeDirectoryReason(err))
	}
	q := DirectoryQuery(tenant, p, lookup.Subject)
	if q.Validate() != nil {
		return userfederation.Groups{}, errors.New("the verified subject cannot be used for a directory lookup")
	}
	if lookup.Email != "" {
		ctx = directory.WithLookupKey(ctx, lookup.Email)
	}
	snapshot, err := e.cache.Resolve(ctx, q, now)
	if err != nil {
		return userfederation.Groups{}, errors.New(directory.Reason(err))
	}
	// Re-check what the cache hands back: complete, expanded, this query, this
	// authority and source only, and still fresh at receipt.
	checked := time.Now()
	if d.Deps.Clock != nil {
		checked = d.Deps.Clock()
	}
	if now.After(checked) {
		checked = now
	}
	if err := membership.Validate(snapshot, q, e.authority.Name(), []string{e.authority.Source()}, checked, 5*time.Minute); err != nil {
		return userfederation.Groups{}, errors.New(directory.Reason(err))
	}
	list := make([]string, 0, len(snapshot.Groups))
	for _, g := range snapshot.Groups {
		list = append(list, g.ID)
	}
	sort.Strings(list)
	return userfederation.Groups{Available: true, List: list, Source: userfederation.GroupSourceDirectory(p.Directory.Kind), ObservedAt: snapshot.IssuedAt, ExpiresAt: snapshot.ExpiresAt}, nil
}

// Invalidate forces the next lookup of subject (the provider's verified
// subject claim value: oid for Entra, sub otherwise) to observe the directory
// at or after notBefore. It returns the invalidated principal ID.
func (d *UserDirectories) Invalidate(tenant string, p userfederation.Provider, subject string, notBefore time.Time) (string, error) {
	e, err := d.entry(tenant, p)
	if err != nil {
		return "", err
	}
	q := DirectoryQuery(tenant, p, subject)
	if err := e.cache.Invalidate(q, notBefore); err != nil {
		return "", err
	}
	return q.SubjectID, nil
}

func safeDirectoryReason(err error) string {
	var de *directory.Error
	if errors.As(err, &de) {
		return de.Reason
	}
	// Configuration errors from directory.New contain no secrets (references only).
	return err.Error()
}
