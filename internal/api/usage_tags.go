package api

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/vamsiramakrishnan/aiplex/pkg/telemetry"
	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

// Tags label usage records for comparison (an experiment arm, a prompt
// version). Two sources:
//
//   - the access point's labels, which are trusted configuration and always
//     apply;
//   - the X-AIPlex-Tags request header, "key=value,key=value", honored only for
//     keys the operator listed in AIPLEX_TAG_KEYS_ALLOWED (default: none). A
//     caller can set its own labels but cannot override a configured one.
//
// The number of distinct header-supplied values per key and tenant is capped
// (AIPLEX_TAG_MAX_VALUES, default 50); beyond it a value is stored as "(other)"
// and counted in aiplex.usage.tags_rejected{reason="overflow"}. The set of
// seen values is kept in process, so each replica applies the cap on its own
// view and a restart starts it afresh.
const (
	// TagsHeader carries caller-supplied tags.
	TagsHeader = "X-AIPlex-Tags"
	// EnvTagKeysAllowed lists the keys a caller may set, comma separated.
	EnvTagKeysAllowed = "AIPLEX_TAG_KEYS_ALLOWED"
	// EnvTagMaxValues caps distinct header-supplied values per key and tenant.
	EnvTagMaxValues = "AIPLEX_TAG_MAX_VALUES"

	defaultTagMaxValues = 50
	maxTagsHeaderBytes  = 512
)

// TagPolicy resolves and bounds record tags. The zero value allows no
// caller-supplied tags.
type TagPolicy struct {
	allowed   map[string]bool
	maxValues int

	mu   sync.Mutex
	seen map[string]map[string]map[string]struct{} // tenant -> key -> values
}

// NewTagPolicy builds a policy from the operator's allowlist of header keys.
func NewTagPolicy(allowedKeys []string, maxValues int) (*TagPolicy, error) {
	p := &TagPolicy{allowed: map[string]bool{}, maxValues: maxValues, seen: map[string]map[string]map[string]struct{}{}}
	if p.maxValues <= 0 {
		p.maxValues = defaultTagMaxValues
	}
	for _, k := range allowedKeys {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		if !usageledger.ValidTagKey(k) {
			return nil, fmt.Errorf("%s: %q is not a valid tag key ([a-z][a-z0-9_.-]{0,31})", EnvTagKeysAllowed, k)
		}
		p.allowed[k] = true
	}
	if len(p.allowed) > usageledger.MaxTags {
		return nil, fmt.Errorf("%s: at most %d keys", EnvTagKeysAllowed, usageledger.MaxTags)
	}
	return p, nil
}

// TagPolicyFromEnv reads AIPLEX_TAG_KEYS_ALLOWED and AIPLEX_TAG_MAX_VALUES.
func TagPolicyFromEnv(getenv func(string) string) (*TagPolicy, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	max := 0
	if raw := strings.TrimSpace(getenv(EnvTagMaxValues)); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 10_000 {
			return nil, fmt.Errorf("%s must be an integer between 1 and 10000", EnvTagMaxValues)
		}
		max = n
	}
	return NewTagPolicy(strings.Split(getenv(EnvTagKeysAllowed), ","), max)
}

// AllowedKeys returns the keys a caller may set (for the Console).
func (p *TagPolicy) AllowedKeys() []string {
	if p == nil {
		return []string{}
	}
	keys := make([]string, 0, len(p.allowed))
	for k := range p.allowed {
		keys = append(keys, k)
	}
	return sortedStrings(keys)
}

// Resolve returns the tags for a request: configured labels first, then the
// header's allowed pairs that do not collide with a configured key. It never
// fails a request: unusable header parts are dropped and counted.
func (p *TagPolicy) Resolve(ctx context.Context, tenant string, configured map[string]string, header string) map[string]string {
	var out map[string]string
	if len(configured) > 0 {
		out = make(map[string]string, len(configured))
		for k, v := range configured {
			out[k] = v
		}
	}
	header = strings.TrimSpace(header)
	if header == "" {
		return out
	}
	reject := func(reason string) { telemetry.Metrics().RecordTagRejected(ctx, reason, 1) }
	if p == nil || len(p.allowed) == 0 {
		reject(telemetry.TagNotAllowed)
		return out
	}
	if len(header) > maxTagsHeaderBytes {
		reject(telemetry.TagInvalid)
		return out
	}
	for _, pair := range strings.Split(header, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(pair), "=")
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch {
		case !ok || !usageledger.ValidTagKey(k) || !usageledger.ValidTagValue(v) || v == usageledger.TagOverflowValue:
			reject(telemetry.TagInvalid)
			continue
		case !p.allowed[k]:
			reject(telemetry.TagNotAllowed)
			continue
		}
		if _, configuredKey := configured[k]; configuredKey {
			reject(telemetry.TagNotAllowed)
			continue
		}
		if out == nil {
			out = map[string]string{}
		}
		if len(out) >= usageledger.MaxTags {
			reject(telemetry.TagInvalid)
			break
		}
		if !p.admit(tenant, k, v) {
			v = usageledger.TagOverflowValue
			reject(telemetry.TagOverflow)
		}
		out[k] = v
	}
	return out
}

// admit records a header-supplied value and reports whether the key is still
// within its distinct-value cap for the tenant.
func (p *TagPolicy) admit(tenant, key, value string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	keys := p.seen[tenant]
	if keys == nil {
		if len(p.seen) >= 10_000 {
			return false
		}
		keys = map[string]map[string]struct{}{}
		p.seen[tenant] = keys
	}
	values := keys[key]
	if values == nil {
		values = map[string]struct{}{}
		keys[key] = values
	}
	if _, ok := values[value]; ok {
		return true
	}
	if len(values) >= p.maxValues {
		return false
	}
	values[value] = struct{}{}
	return true
}

// requestTags resolves the tags of an access point request.
func (p *TagPolicy) requestTags(r *http.Request, tenant string, configured map[string]string) map[string]string {
	return p.Resolve(r.Context(), tenant, configured, r.Header.Get(TagsHeader))
}

func sortedStrings(in []string) []string {
	out := append([]string{}, in...)
	sort.Strings(out)
	return out
}
