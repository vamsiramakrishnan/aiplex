package userfederation

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"
)

// Groups is the group view of a verified token.
type Groups struct {
	// Available is true only when the pinned claim carried a well-formed list.
	// An authoritative empty list is Available with no entries.
	Available bool
	List      []string
	// Reason explains why groups are unavailable (or where they come from).
	Reason string
}

// ExtractGroups reads the provider's pinned group claim from signature-verified
// claims. A missing, null, malformed or overage-flagged claim yields
// Available=false: that is absence of a membership view, never an empty one.
func ExtractGroups(claims map[string]any, t Trust) Groups {
	if t.GroupsFrom == GroupsFromMembershipAuthority {
		return Groups{Reason: "groups are supplied by the membership authority; the token's own claims are ignored"}
	}
	// Overage indicators (Entra: hasgroups, _claim_names.groups) mean the token
	// deliberately omits the list. Presence alone is the signal, never the value.
	if marker, exists := claims["hasgroups"]; exists && marker != false {
		return Groups{Reason: "token carries a group overage indicator (hasgroups); the group list was omitted by the issuer"}
	}
	if raw, exists := claims["_claim_names"]; exists {
		names, ok := raw.(map[string]any)
		if !ok {
			return Groups{Reason: "token carries a malformed group overage indicator (_claim_names)"}
		}
		_, byName := names[t.GroupClaim]
		_, byDefault := names["groups"]
		if byName || byDefault {
			return Groups{Reason: "token carries a group overage indicator (_claim_names); the group list was omitted by the issuer"}
		}
	}
	value, found := lookupClaim(claims, t.GroupClaim)
	switch {
	case !found:
		return Groups{Reason: fmt.Sprintf("claim %q is missing from the token, so membership is unknown (not empty)", t.GroupClaim)}
	case value == nil:
		return Groups{Reason: fmt.Sprintf("claim %q is null, so membership is unknown (not empty)", t.GroupClaim)}
	}
	items, ok := value.([]any)
	if !ok || items == nil {
		return Groups{Reason: fmt.Sprintf("claim %q is not an array of strings", t.GroupClaim)}
	}
	if len(items) > 1024 {
		return Groups{Reason: fmt.Sprintf("claim %q has more than 1024 entries", t.GroupClaim)}
	}
	groups := make([]string, len(items))
	for i, item := range items {
		s, ok := item.(string)
		if !ok || !validGroup(s) {
			return Groups{Reason: fmt.Sprintf("claim %q contains an entry that is not a well-formed group identifier", t.GroupClaim)}
		}
		groups[i] = s
	}
	sort.Strings(groups)
	return Groups{Available: true, List: slices.Compact(groups)}
}

func validGroup(s string) bool {
	if s == "" || len(s) > 1024 || strings.TrimSpace(s) != s || !utf8.ValidString(s) {
		return false
	}
	for _, ch := range s {
		if ch < 0x20 || ch == 0x7f {
			return false
		}
	}
	return true
}

// lookupClaim finds a top-level claim by its exact name first, then walks a
// dotted name through nested JSON objects.
func lookupClaim(claims map[string]any, name string) (any, bool) {
	if v, ok := claims[name]; ok {
		return v, true
	}
	if !strings.Contains(name, ".") {
		return nil, false
	}
	var current any = claims
	for _, part := range strings.Split(name, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = object[part]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

// numberString renders a JSON number claim for exact comparison.
func numberString(n json.Number) string { return n.String() }
