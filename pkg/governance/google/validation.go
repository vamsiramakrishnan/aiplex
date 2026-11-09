package google

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"

	"github.com/vamsiramakrishnan/aiplex/pkg/nativeapi"
)

// Google and Go JSON decoders must never disagree about duplicate selectors.
func uniqueJSON(raw []byte) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	var walk func(int) error
	walk = func(depth int) error {
		if depth > 32 {
			return errors.New("Google governance JSON nesting exceeds limit")
		}
		tok, e := d.Token()
		if e != nil {
			return errors.New("invalid Google governance JSON")
		}
		if delim, ok := tok.(json.Delim); ok {
			switch delim {
			case '{':
				seen := map[string]bool{}
				for d.More() {
					key, e := d.Token()
					if e != nil {
						return errors.New("invalid JSON key")
					}
					s, ok := key.(string)
					if !ok || seen[s] {
						return errors.New("duplicate Google governance JSON key")
					}
					seen[s] = true
					if e = walk(depth + 1); e != nil {
						return e
					}
				}
			case '[':
				for d.More() {
					if e = walk(depth + 1); e != nil {
						return e
					}
				}
			default:
				return errors.New("invalid JSON delimiter")
			}
			if _, e = d.Token(); e != nil {
				return errors.New("invalid JSON closing delimiter")
			}
		}
		return nil
	}
	if e := walk(0); e != nil {
		return e
	}
	if _, e := d.Token(); e != io.EOF {
		return errors.New("trailing Google governance JSON")
	}
	return nil
}

type iamPolicy struct {
	Version      int             `json:"version,omitempty"`
	ETag         string          `json:"etag"`
	Bindings     []iamBinding    `json:"bindings,omitempty"`
	AuditConfigs json.RawMessage `json:"auditConfigs,omitempty"`
}
type iamBinding struct {
	Role      string          `json:"role"`
	Members   []string        `json:"members"`
	Condition json.RawMessage `json:"condition,omitempty"`
}

func grants(p iamPolicy) (map[string][2]string, error) {
	out := map[string][2]string{}
	for _, b := range p.Bindings {
		if b.Role == "" || len(b.Members) == 0 {
			return nil, errors.New("invalid IAM policy binding")
		}
		condition := ""
		if len(b.Condition) > 0 && string(b.Condition) != "null" {
			var v any
			if json.Unmarshal(b.Condition, &v) != nil {
				return nil, errors.New("invalid IAM condition")
			}
			encoded, _ := json.Marshal(v)
			condition = string(encoded)
		}
		for _, m := range b.Members {
			if m == "" || strings.ContainsAny(m, "\r\n") {
				return nil, errors.New("invalid IAM member")
			}
			out[b.Role+"\x00"+m+"\x00"+condition] = [2]string{b.Role, m}
		}
	}
	return out, nil
}
func validatePolicyDiff(cfg Config, prior, desired json.RawMessage) error {
	var a, b iamPolicy
	if strict(prior, &a) != nil || strict(desired, &b) != nil {
		return errors.New("invalid IAM replacement policy")
	}
	old, e := grants(a)
	if e != nil {
		return e
	}
	next, e := grants(b)
	if e != nil {
		return e
	}
	allowed := func(pair [2]string) bool {
		return slices.Contains(cfg.AllowedRoles, pair[0]) && slices.Contains(cfg.AllowedAgentPrincipals, pair[1])
	}
	for k, pair := range old {
		if _, ok := next[k]; !ok && !allowed(pair) {
			return errors.New("IAM replacement removes or changes an unmanaged grant")
		}
	}
	for k, pair := range next {
		if _, ok := old[k]; !ok && !allowed(pair) {
			return errors.New("IAM changed grants require allowlisted agent principals and roles")
		}
	}
	canon := func(raw json.RawMessage) string {
		if len(raw) == 0 || string(raw) == "null" {
			return ""
		}
		var v any
		if json.Unmarshal(raw, &v) != nil {
			return "invalid"
		}
		data, _ := json.Marshal(v)
		return string(data)
	}
	if canon(a.AuditConfigs) != canon(b.AuditConfigs) {
		return errors.New("IAM replacement cannot change unmanaged audit configuration")
	}
	return nil
}

// ValidateOperation performs no RPC, credential lookup or other external effect.
func (d *Registry) ValidateOperation(op nativeapi.Operation) error {
	if e := nativeapi.ValidatePublicPayload(op.Payload); e != nil {
		return e
	}
	kind, action, ok := strings.Cut(op.Name, ".")
	if !ok {
		return errors.New("unknown registry operation")
	}
	if kind == "aiApplications" {
		return validatePolicy(d.cfg, op, "aiApplications")
	}
	mutable := kind == "services" || kind == "bindings"
	if !slices.Contains([]string{"services", "bindings", "agents", "mcpServers", "endpoints", "skills", "operations"}, kind) || !slices.Contains([]string{"list", "get", "create", "patch", "delete"}, action) || (!mutable && action != "list" && action != "get") || (kind == "operations" && action != "get") {
		return errors.New("unknown registry operation")
	}
	if action == "list" {
		if op.Resource != d.cfg.parent() {
			return errors.New("list requires configured registry parent")
		}
		_, e := listQuery(op.Payload)
		return e
	}
	id, e := d.cfg.resource(op.Resource, kind)
	if e != nil {
		return e
	}
	if action == "get" {
		return empty(op.Payload)
	}
	var m mutation
	if e = strict(op.Payload, &m); e != nil {
		return e
	}
	if e = requestID(m.RequestID); e != nil {
		return e
	}
	if action != "delete" {
		if _, e = d.validateResource(kind, op.Resource, m.Resource, action == "create"); e != nil {
			return e
		}
	} else if len(m.Resource) > 0 || m.UpdateMask != "" {
		return errors.New("delete accepts requestId only")
	}
	if action == "create" {
		if op.ETag != "" || m.UpdateMask != "" || !resourceID.MatchString(id) {
			return errors.New("invalid registry creation id/preconditions")
		}
		return nil
	}
	if !d.cfg.AllowNonAtomicMutations || !validFingerprint(op.ETag) {
		return errors.New("registry mutation requires explicit non-atomic opt-in and observation fingerprint")
	}
	if action == "patch" && !validMask(kind, m.UpdateMask) {
		return errors.New("explicit writable registry updateMask is required")
	}
	return nil
}
func validFingerprint(raw string) bool {
	if !strings.HasPrefix(raw, "sha256:") || len(raw) != 71 {
		return false
	}
	for _, c := range strings.TrimPrefix(raw, "sha256:") {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func (d *IAP) ValidateOperation(op nativeapi.Operation) error {
	if e := nativeapi.ValidatePublicPayload(op.Payload); e != nil {
		return e
	}
	if op.Name != "egress.getIamPolicy" && op.Name != "egress.setIamPolicy" {
		return errors.New("unknown IAP operation")
	}
	return validatePolicy(d.cfg, op, "agents", "mcpServers", "endpoints")
}
func validatePolicy(cfg Config, op nativeapi.Operation, kinds ...string) error {
	if _, e := cfg.resource(op.Resource, kinds...); e != nil {
		return e
	}
	_, action, _ := strings.Cut(op.Name, ".")
	if action == "getIamPolicy" {
		return empty(op.Payload)
	}
	if action != "setIamPolicy" {
		return errors.New("unknown IAM policy operation")
	}
	var in struct {
		Policy     json.RawMessage `json:"policy"`
		UpdateMask string          `json:"updateMask,omitempty"`
	}
	if e := strict(op.Payload, &in); e != nil {
		return e
	}
	var p struct {
		ETag     string `json:"etag"`
		Version  int    `json:"version"`
		Bindings []struct {
			Condition json.RawMessage `json:"condition"`
		} `json:"bindings"`
	}
	if json.Unmarshal(in.Policy, &p) != nil || p.ETag == "" || op.ETag == "" || p.ETag != op.ETag {
		return errors.New("IAM policy etag must match reviewed operation etag")
	}
	if in.UpdateMask != "" && in.UpdateMask != "bindings,etag" && in.UpdateMask != "bindings,etag,version" {
		return errors.New("IAM policy updateMask must preserve etag")
	}
	for _, b := range p.Bindings {
		if len(b.Condition) > 0 && string(b.Condition) != "null" && p.Version != 3 {
			return errors.New("conditional IAM bindings require policy version 3")
		}
	}
	return nil
}
func (d *IdentityManager) ValidateOperation(op nativeapi.Operation) error {
	kind, action, ok := strings.Cut(op.Name, ".")
	if !ok || kind != "authProviders" || !slices.Contains([]string{"list", "get", "create", "patch", "delete", "enable", "disable", "getIamPolicy", "setIamPolicy"}, action) {
		return errors.New("unknown auth-manager operation")
	}
	if action == "getIamPolicy" || action == "setIamPolicy" {
		return validatePolicy(d.cfg, op, "authProviders")
	}
	if action == "list" {
		if op.Resource != d.cfg.parent() {
			return errors.New("auth-provider list requires configured parent")
		}
		_, e := listQuery(op.Payload)
		return e
	}
	id, e := d.cfg.resource(op.Resource, "authProviders")
	if e != nil {
		return e
	}
	if action == "get" {
		return empty(op.Payload)
	}
	var m mutation
	if e = strict(op.Payload, &m); e != nil {
		return e
	}
	if e = requestID(m.RequestID); e != nil {
		return e
	}
	if action == "create" || action == "patch" {
		var p AuthProvider
		if e = strict(m.Resource, &p); e != nil {
			return e
		}
		if p.Name != "" && p.Name != op.Resource {
			return errors.New("auth-provider body/path mismatch")
		}
		for _, a := range p.WorkloadIDs {
			if !validPrincipal(a) || !slices.Contains(d.cfg.AllowedAgentPrincipals, a) {
				return errors.New("auth-provider workload principal is not explicitly allowed")
			}
		}
		if action == "create" && len(p.TypeParams) == 0 {
			return errors.New("auth-provider type parameters required")
		}
		if len(p.TypeParams) > 0 {
			var v map[string]json.RawMessage
			if json.Unmarshal(p.TypeParams, &v) != nil || len(v) != 1 {
				return errors.New("auth-provider requires exactly one type")
			}
			for k := range v {
				if !slices.Contains([]string{"threeLeggedOauth", "twoLeggedOauth", "apiKey", "geAuthProvider"}, k) {
					return errors.New("unsupported auth-provider type")
				}
			}
		}
	} else if len(m.Resource) > 0 || m.UpdateMask != "" {
		return errors.New("auth-provider action accepts requestId only")
	}
	if action == "create" {
		if !resourceID.MatchString(id) || op.ETag != "" || m.UpdateMask != "" {
			return errors.New("invalid auth-provider creation id/preconditions")
		}
		return nil
	}
	if !d.cfg.AllowNonAtomicMutations || !validFingerprint(op.ETag) {
		return errors.New("auth-provider mutation requires explicit non-atomic opt-in and observation fingerprint")
	}
	if action == "patch" {
		if m.UpdateMask == "" {
			return errors.New("auth-provider updateMask required")
		}
		for _, f := range strings.Split(m.UpdateMask, ",") {
			if !slices.Contains([]string{"labels", "authProviderTypeParams", "allowedScopes", "blockedScopes", "description", "workloadIds"}, f) {
				return errors.New("invalid auth-provider updateMask")
			}
		}
	}
	return nil
}
