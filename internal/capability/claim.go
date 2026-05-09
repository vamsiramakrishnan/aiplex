package capability

import (
	"strings"
	"time"
)

// Cap is one entry in a JWT's caps claim. It grants the bearer permission
// to perform a subset of actions on a single capability URI.
//
//	{
//	  "uri": "cap://tool/search_curriculum@v1",
//	  "actions": ["call"],
//	  "constraints": {"rate_per_min": 30}
//	}
type Cap struct {
	URI         string         `json:"uri"`
	Actions     []string       `json:"actions,omitempty"`
	Constraints map[string]any `json:"constraints,omitempty"`
	NotBefore   int64          `json:"nbf,omitempty"` // epoch seconds
	NotAfter    int64          `json:"exp,omitempty"` // epoch seconds (per-cap, overrides token exp)
}

// Allows reports whether c grants the given action on the given URI.
func (c Cap) Allows(uri, action string) bool {
	if c.URI != uri {
		return false
	}
	if !c.timeOK(time.Now().Unix()) {
		return false
	}
	if len(c.Actions) == 0 {
		// Empty actions means "all actions of the kind". Check against the
		// kind's action set if we can parse the URI.
		if u, err := ParseURI(uri); err == nil {
			return IsAllowedAction(u.Kind, action)
		}
		return true
	}
	for _, a := range c.Actions {
		if a == action {
			return true
		}
	}
	return false
}

func (c Cap) timeOK(now int64) bool {
	if c.NotBefore > 0 && now < c.NotBefore {
		return false
	}
	if c.NotAfter > 0 && now > c.NotAfter {
		return false
	}
	return true
}

// CapSet is a slice of Cap entries with helpers.
type CapSet []Cap

// Allows reports whether any cap in the set grants (uri, action).
func (s CapSet) Allows(uri, action string) bool {
	for _, c := range s {
		if c.Allows(uri, action) {
			return true
		}
	}
	return false
}

// Find returns the first cap matching uri (regardless of action), or nil.
func (s CapSet) Find(uri string) *Cap {
	for i := range s {
		if s[i].URI == uri {
			return &s[i]
		}
	}
	return nil
}

// URIs returns just the cap URIs in the set, in order.
func (s CapSet) URIs() []string {
	out := make([]string, 0, len(s))
	for _, c := range s {
		out = append(out, c.URI)
	}
	return out
}

// Intersect returns the elements of s that also appear (by URI) in other.
// Actions are intersected per-URI; constraints from `s` win.
func (s CapSet) Intersect(other CapSet) CapSet {
	idx := make(map[string]Cap, len(other))
	for _, c := range other {
		idx[c.URI] = c
	}
	var out CapSet
	for _, c := range s {
		o, ok := idx[c.URI]
		if !ok {
			continue
		}
		merged := c
		merged.Actions = intersectStrings(c.Actions, o.Actions)
		out = append(out, merged)
	}
	return out
}

// Union appends caps from other that are not already present in s by URI.
// When duplicate URIs appear, the existing entry is kept.
func (s CapSet) Union(other CapSet) CapSet {
	seen := make(map[string]struct{}, len(s))
	for _, c := range s {
		seen[c.URI] = struct{}{}
	}
	out := append(CapSet(nil), s...)
	for _, c := range other {
		if _, ok := seen[c.URI]; ok {
			continue
		}
		out = append(out, c)
		seen[c.URI] = struct{}{}
	}
	return out
}

// String returns a space-separated list of URIs. Useful for logs.
func (s CapSet) String() string {
	return strings.Join(s.URIs(), " ")
}

func intersectStrings(a, b []string) []string {
	if len(a) == 0 {
		return append([]string(nil), b...)
	}
	if len(b) == 0 {
		return append([]string(nil), a...)
	}
	set := make(map[string]struct{}, len(b))
	for _, x := range b {
		set[x] = struct{}{}
	}
	var out []string
	for _, x := range a {
		if _, ok := set[x]; ok {
			out = append(out, x)
		}
	}
	return out
}
