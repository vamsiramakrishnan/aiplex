// Package membership defines trusted directory evidence independently of quota,
// authentication providers, and transport. An authenticated subject is not itself
// proof of an empty or complete group view.
package membership

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/vamsiramakrishnan/aiplex/pkg/contracts"
)

const Version = "v1"

var ErrUnavailable = errors.New("authoritative membership unavailable")
var ErrInvalidEvidence = errors.New("membership evidence is invalid")

// Query binds a directory lookup to a verified immutable identity. Role keeps
// principal and delegated-actor caches and proofs separate even for one subject.
type Query struct {
	TenantID      string `json:"tenant_id"`
	SubjectID     string `json:"subject_id"`
	Kind          string `json:"kind"`
	Issuer        string `json:"issuer"`
	PoolID        string `json:"pool_id"`
	NativeSubject string `json:"native_subject"`
	Role          string `json:"role"`
}
type Group struct {
	Source string `json:"source"`
	ID     string `json:"id"`
}

// Snapshot is a complete expanded directory view, not a delta or list of direct
// groups. IssuedAt is the authority's observation time, never cache insertion.
type Snapshot struct {
	Version   string                `json:"version"`
	Query     Query                 `json:"query"`
	Authority string                `json:"authority"`
	Ref       contracts.SnapshotRef `json:"ref"`
	Groups    []Group               `json:"groups"`
	Complete  bool                  `json:"complete"`
	Expanded  bool                  `json:"expanded"`
	IssuedAt  time.Time             `json:"issued_at"`
	ExpiresAt time.Time             `json:"expires_at"`
}

// Authority is an operator-selected directory source, not a request-selected URL.
// ConfigurationDigest identifies its pinned trust, source and cache configuration.
type Authority interface {
	Resolve(context.Context, Query, time.Time) (Snapshot, error)
	ConfigurationDigest() string
}

func ValidAuthority(authority Authority) bool {
	if authority == nil {
		return false
	}
	v := reflect.ValueOf(authority)
	if (v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface) && v.IsNil() {
		return false
	}
	encoded := authority.ConfigurationDigest()
	raw, err := hex.DecodeString(encoded)
	return err == nil && len(raw) == 32 && hex.EncodeToString(raw) == encoded
}

func Text(v string) bool {
	if v == "" || len(v) > 1024 || strings.TrimSpace(v) != v || !utf8.ValidString(v) {
		return false
	}
	for _, c := range v {
		if c < 0x20 || c == 0x7f {
			return false
		}
	}
	return true
}
func (q Query) Validate() error {
	for _, v := range []string{q.TenantID, q.SubjectID, q.Issuer, q.PoolID, q.NativeSubject} {
		if !Text(v) {
			return ErrInvalidEvidence
		}
	}
	if (q.Role != "principal" && q.Role != "actor") || (q.Kind != "human" && q.Kind != "agent" && q.Kind != "application") || (q.Role == "actor" && q.Kind != "agent") {
		return ErrInvalidEvidence
	}
	return nil
}
func Digest(v any) string {
	raw, _ := json.Marshal(v)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func Clone(s Snapshot) Snapshot {
	if s.Groups != nil {
		s.Groups = append([]Group{}, s.Groups...)
	}
	return s
}
func Validate(s Snapshot, q Query, authority string, sources []string, now time.Time, maxAge time.Duration) error {
	if q.Validate() != nil || s.Query != q || s.Version != Version || s.Authority != authority || !Text(s.Ref.ID) || !Text(s.Ref.Version) || !s.Complete || !s.Expanded || s.Groups == nil || len(s.Groups) > 1024 || s.IssuedAt.IsZero() || s.IssuedAt.After(now) || !s.ExpiresAt.After(now) || !s.ExpiresAt.After(s.IssuedAt) || maxAge < time.Second || !s.IssuedAt.Add(maxAge).After(now) || s.ExpiresAt.Sub(s.IssuedAt) > maxAge {
		return ErrInvalidEvidence
	}
	allowed := map[string]bool{}
	for _, source := range sources {
		if !Text(source) {
			return ErrInvalidEvidence
		}
		allowed[source] = true
	}
	seen := map[Group]bool{}
	for _, g := range s.Groups {
		if !Text(g.Source) || !Text(g.ID) || !allowed[g.Source] || seen[g] {
			return ErrInvalidEvidence
		}
		seen[g] = true
	}
	return nil
}

// Expand computes a bounded transitive group closure from an authoritative graph.
// Missing graph entries are leaves. A cycle is rejected instead of returning a
// partial view that could silently omit a mandatory group constraint.
func Expand(direct []Group, parents map[Group][]Group, max int) ([]Group, error) {
	if direct == nil || max < 1 || max > 1024 {
		return nil, ErrInvalidEvidence
	}
	state := map[Group]uint8{}
	out := []Group{}
	var visit func(Group, int) error
	visit = func(g Group, depth int) error {
		if depth > 64 || !Text(g.Source) || !Text(g.ID) {
			return ErrInvalidEvidence
		}
		if state[g] == 1 {
			return fmt.Errorf("%w: cyclic directory groups", ErrInvalidEvidence)
		}
		if state[g] == 2 {
			return nil
		}
		if len(state) >= max {
			return fmt.Errorf("%w: group expansion bound", ErrInvalidEvidence)
		}
		state[g] = 1
		for _, p := range parents[g] {
			if err := visit(p, depth+1); err != nil {
				return err
			}
		}
		state[g] = 2
		out = append(out, g)
		return nil
	}
	for _, g := range direct {
		if err := visit(g, 0); err != nil {
			return nil, err
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Source == out[j].Source {
			return out[i].ID < out[j].ID
		}
		return out[i].Source < out[j].Source
	})
	return out, nil
}
