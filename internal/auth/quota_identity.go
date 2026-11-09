package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/pkg/contracts"
)

// MembershipSource namespaces native group IDs by the configured verified
// issuer and pool. Its encoding has no separator ambiguity with a raw group ID.
func MembershipSource(provider, pool string) string {
	raw, _ := json.Marshal([]string{provider, pool})
	digest := sha256.Sum256(raw)
	return "source-" + hex.EncodeToString(digest[:])
}

// PrincipalMembership binds the verified credential's exact membership view to
// one tenant. It never invents a lifetime from request arrival or an empty group
// view from a missing/overage claim. A delegated actor requires independent
// membership evidence; this function supplies only the principal's snapshot.
func PrincipalMembership(identity *models.WIFIdentity, tenant string, now time.Time) (contracts.MembershipSnapshot, error) {
	if identity == nil || identity.VerifiedEvidence == nil || tenant == "" || now.IsZero() {
		return contracts.MembershipSnapshot{}, fmt.Errorf("verified principal membership evidence is required")
	}
	evidence := identity.VerifiedEvidence
	if evidence.Issuer == "" || evidence.PoolID == "" || evidence.Subject == "" || evidence.Issuer != identity.Provider || evidence.PoolID != identity.PoolID || evidence.Subject != identity.Subject || identity.PrincipalID != CanonicalPrincipalID(evidence.Issuer, evidence.PoolID, evidence.Subject) {
		return contracts.MembershipSnapshot{}, fmt.Errorf("membership evidence does not bind the verified identity")
	}
	expiresAt := evidence.ExpiresAt
	if !evidence.CredentialExpiresAt.IsZero() && evidence.CredentialExpiresAt.Before(expiresAt) {
		expiresAt = evidence.CredentialExpiresAt
	}
	if !evidence.LifetimeValid || evidence.IssuedAt.IsZero() || evidence.IssuedAt.After(now) || !expiresAt.After(now) || !expiresAt.After(evidence.IssuedAt) {
		return contracts.MembershipSnapshot{}, fmt.Errorf("verified membership lifetime is missing, stale or not yet valid")
	}
	if !evidence.GroupsPresent || !evidence.GroupsValid {
		return contracts.MembershipSnapshot{}, fmt.Errorf("an authoritative verified group view is required")
	}
	// Consumers cannot silently substitute a mutable public group list for the
	// copy captured at verification. Empty and nil views are equivalent sets.
	groups, valid := membershipGroups(identity.Groups)
	if !valid || !slices.Equal(groups, evidence.Groups) {
		return contracts.MembershipSnapshot{}, fmt.Errorf("identity groups changed after verification")
	}
	source := MembershipSource(evidence.Issuer, evidence.PoolID)
	qualified := make([]string, len(groups))
	for i, group := range groups {
		qualified[i] = source + ":" + group
	}
	stamp := struct {
		Tenant, Principal, Source string
		Groups                    []string
		IssuedAt, ExpiresAt       time.Time
	}{tenant, identity.PrincipalID, source, groups, evidence.IssuedAt.UTC(), expiresAt.UTC()}
	raw, _ := json.Marshal(stamp)
	digest := sha256.Sum256(raw)
	return contracts.MembershipSnapshot{Version: contracts.Version, Ref: contracts.SnapshotRef{ID: "claims-" + identity.PrincipalID, Version: hex.EncodeToString(digest[:])}, TenantID: tenant, PrincipalID: identity.PrincipalID, Groups: qualified, IssuedAt: evidence.IssuedAt.UTC(), ExpiresAt: expiresAt.UTC()}, nil
}

func constrainRequestEvidence(r *http.Request, evidence *models.IdentityEvidence) {
	if evidence == nil || r.TLS == nil || len(r.TLS.PeerCertificates) == 0 || len(r.TLS.VerifiedChains) == 0 || len(r.TLS.VerifiedChains[0]) == 0 {
		return
	}
	leaf := r.TLS.PeerCertificates[0]
	if leaf == nil || r.TLS.VerifiedChains[0][0] == nil || !leaf.Equal(r.TLS.VerifiedChains[0][0]) {
		return
	}
	if leaf.NotAfter.IsZero() {
		evidence.LifetimeValid = false
		return
	}
	evidence.CredentialExpiresAt = leaf.NotAfter.UTC()
}

// Called only after the configured TokenVerifier has accepted the credential.
// Evidence validation is intentionally stricter than existing role extraction.
func verifiedIdentityEvidence(claims map[string]any, identity *models.WIFIdentity) *models.IdentityEvidence {
	evidence := &models.IdentityEvidence{Issuer: identity.Provider, PoolID: identity.PoolID, Subject: identity.Subject, ActorSubject: identity.ActorSubject}
	iat, iatOK := evidenceNumericDate(claims["iat"])
	exp, expOK := evidenceNumericDate(claims["exp"])
	if iatOK {
		evidence.IssuedAt = time.Unix(iat, 0).UTC()
	}
	if expOK {
		evidence.ExpiresAt = time.Unix(exp, 0).UTC()
	}
	evidence.LifetimeValid = iatOK && expOK && exp > iat
	direct, directPresent := claims["groups"]
	google, googlePresent := googleMembershipClaim(claims)
	evidence.GroupsPresent = directPresent || googlePresent
	var groups []string
	valid := false
	switch {
	case directPresent && googlePresent:
		directGroups, directValid := claimMembershipGroups(direct)
		googleGroups, googleValid := claimMembershipGroups(google)
		groups, valid = directGroups, directValid && googleValid && slices.Equal(directGroups, googleGroups)
		evidence.GroupsSource = "groups+google.sign_in_attributes.groups"
	case directPresent:
		groups, valid = claimMembershipGroups(direct)
		evidence.GroupsSource = "groups"
	case googlePresent:
		groups, valid = claimMembershipGroups(google)
		evidence.GroupsSource = "google.sign_in_attributes.groups"
	}
	if referencesExternalGroups(claims) {
		valid = false
	}
	evidence.GroupsValid = valid
	evidence.Groups = slices.Clone(groups)
	return evidence
}

func googleMembershipClaim(claims map[string]any) (any, bool) {
	google, _ := claims["google"].(map[string]any)
	attributes, _ := google["sign_in_attributes"].(map[string]any)
	groups, ok := attributes["groups"]
	return groups, ok
}

func referencesExternalGroups(claims map[string]any) bool {
	if marker, exists := claims["hasgroups"]; exists && marker != false {
		return true
	}
	if raw, exists := claims["_claim_names"]; exists {
		names, ok := raw.(map[string]any)
		if !ok {
			return true
		}
		if _, exists = names["groups"]; exists {
			return true
		}
	}
	return false
}

func claimMembershipGroups(value any) ([]string, bool) {
	// Signed null is absence of a usable view, never an authoritative empty
	// array. Reject typed nil slices from custom verifier adapters as well.
	switch value := value.(type) {
	case nil:
		return nil, false
	case []string:
		if value == nil {
			return nil, false
		}
	case []any:
		if value == nil {
			return nil, false
		}
	}
	return membershipGroups(value)
}

func membershipGroups(value any) ([]string, bool) {
	var groups []string
	switch values := value.(type) {
	case []string:
		groups = slices.Clone(values)
	case []any:
		groups = make([]string, len(values))
		for i, value := range values {
			var ok bool
			groups[i], ok = value.(string)
			if !ok {
				return nil, false
			}
		}
	case nil:
		// A nil Go slice is a legitimate empty internal snapshot; a signed JSON
		// null is rejected by verifiedIdentityEvidence before calling this path.
		return nil, true
	default:
		return nil, false
	}
	if len(groups) > 1024 {
		return nil, false
	}
	for _, group := range groups {
		if group == "" || len(group) > 1024 || strings.TrimSpace(group) != group || !utf8.ValidString(group) {
			return nil, false
		}
		for _, ch := range group {
			if ch < 0x20 || ch == 0x7f {
				return nil, false
			}
		}
	}
	sort.Strings(groups)
	return slices.Compact(groups), true
}

func evidenceNumericDate(value any) (int64, bool) {
	var seconds int64
	switch value := value.(type) {
	case json.Number:
		var err error
		seconds, err = value.Int64()
		if err != nil {
			return 0, false
		}
	case int64:
		seconds = value
	case int:
		seconds = int64(value)
	case float64:
		if math.IsNaN(value) || math.IsInf(value, 0) || value != math.Trunc(value) || value < 1 || value > 253402300799 {
			return 0, false
		}
		seconds = int64(value)
	default:
		return 0, false
	}
	return seconds, seconds > 0 && seconds <= 253402300799
}
