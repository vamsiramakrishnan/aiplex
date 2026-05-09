package memplex

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// applyPII walks the value's JSON map and applies PII rules in order. Returns
// the (possibly mutated) data, or ErrPIIRejected if any rule's action is
// "reject" and the field is present.
func applyPII(data map[string]any, policy *PIIPolicy) (map[string]any, error) {
	if policy == nil || !policy.Enabled || len(policy.Rules) == 0 {
		return data, nil
	}
	out := cloneMap(data)
	for _, r := range policy.Rules {
		val, ok := lookupPath(out, r.Field)
		if !ok {
			continue
		}
		switch r.Action {
		case "reject":
			if val != nil && val != "" {
				return nil, ErrPIIRejected
			}
		case "hash":
			if s, ok := val.(string); ok && s != "" {
				sum := sha256.Sum256([]byte(s))
				_ = setPath(out, r.Field, "sha256:"+hex.EncodeToString(sum[:]))
			}
		case "redact":
			_ = setPath(out, r.Field, "[REDACTED]")
		}
	}
	return out, nil
}

// lookupPath walks dotted paths like "user.ssn" through a JSON map.
func lookupPath(m map[string]any, path string) (any, bool) {
	if path == "" {
		return nil, false
	}
	parts := strings.Split(path, ".")
	var cur any = m
	for _, p := range parts {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = obj[p]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

// setPath assigns at a dotted path, creating intermediate maps as needed.
func setPath(m map[string]any, path string, val any) bool {
	if path == "" {
		return false
	}
	parts := strings.Split(path, ".")
	cur := m
	for i, p := range parts {
		if i == len(parts)-1 {
			cur[p] = val
			return true
		}
		next, ok := cur[p].(map[string]any)
		if !ok {
			next = make(map[string]any)
			cur[p] = next
		}
		cur = next
	}
	return false
}

// cloneMap returns a shallow copy that is safe to mutate.
func cloneMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		if sub, ok := v.(map[string]any); ok {
			out[k] = cloneMap(sub)
		} else {
			out[k] = v
		}
	}
	return out
}
