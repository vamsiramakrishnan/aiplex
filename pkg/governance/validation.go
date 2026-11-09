package governance

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/vamsiramakrishnan/aiplex/pkg/nativeapi"
)

var identifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
var fingerprint = regexp.MustCompile(`^[0-9a-f]{64}$`)

func boundedIdentity(value string) bool {
	return value != "" && len(value) <= 4096 && !strings.ContainsAny(value, "\x00\r\n")
}
func (p PrincipalRef) Validate() error {
	if !identifier.MatchString(p.Provider) || !boundedIdentity(p.TenantID) || !boundedIdentity(p.ImmutableID) || strings.Contains(p.ImmutableID, "@") {
		return fmt.Errorf("provider, native tenant and immutable principal id required; email aliases are not principal identifiers")
	}
	switch p.Kind {
	case "agent", "user", "group", "service":
		return nil
	}
	return fmt.Errorf("principal kind must be agent, user, group or service")
}
func (r RegistryRef) Validate() error {
	if !identifier.MatchString(r.Provider) || !boundedIdentity(r.TenantID) || !boundedIdentity(r.NativeResourceName) {
		return fmt.Errorf("native registry identity required")
	}
	if r.DiscoveryURN != "" && (!strings.HasPrefix(r.DiscoveryURN, "urn:") || !boundedIdentity(r.DiscoveryURN) || r.DiscoveryURN == r.NativeResourceName) {
		return fmt.Errorf("discovery URN must remain a distinct coordinate")
	}
	if r.RuntimeRef != "" && !boundedIdentity(r.RuntimeRef) {
		return fmt.Errorf("invalid runtime reference")
	}
	return nil
}
func validateOperation(op nativeapi.Operation) error {
	if !identifier.MatchString(op.Name) || len(op.Resource) > 4096 || strings.Contains(op.Resource, "://") || strings.ContainsAny(op.Resource, "\x00\r\n?#") || strings.Contains(op.Resource, "*") {
		return fmt.Errorf("invalid native operation/resource coordinate")
	}
	if len(op.ETag) > 2048 || strings.ContainsAny(op.ETag, "\x00\r\n") {
		return fmt.Errorf("invalid native ETag")
	}
	return nativeapi.ValidatePublicPayload(op.Payload)
}
func (b AgentBinding) Validate() error {
	if !identifier.MatchString(b.ID) || !boundedIdentity(b.TenantID) || !identifier.MatchString(b.DriverRef) {
		return fmt.Errorf("binding id, storage tenant and driver reference required")
	}
	if e := b.Agent.Validate(); e != nil {
		return e
	}
	if b.Agent.Kind != "agent" {
		return fmt.Errorf("binding agent must identify an agent principal")
	}
	if b.DelegatedUser != nil {
		if e := b.DelegatedUser.Validate(); e != nil {
			return e
		}
		if b.DelegatedUser.Kind != "user" {
			return fmt.Errorf("delegated user must be a user principal")
		}
	}
	if b.Sponsor != nil {
		if e := b.Sponsor.Validate(); e != nil {
			return e
		}
	}
	if b.Registry != nil {
		if e := b.Registry.Validate(); e != nil {
			return e
		}
	}
	if len(b.Allowed) == 0 || len(b.Allowed) > 256 || len(b.Desired) > 128 {
		return fmt.Errorf("bounded explicit operation grants required")
	}
	seen := map[string]bool{}
	for _, g := range b.Allowed {
		if e := validateOperation(nativeapi.Operation{Name: g.Operation, Resource: g.Resource}); e != nil {
			return e
		}
		key := g.Operation + "\x00" + g.Resource
		if seen[key] {
			return fmt.Errorf("duplicate native operation grant")
		}
		seen[key] = true
	}
	ids := map[string]bool{}
	for _, d := range b.Desired {
		if !identifier.MatchString(d.ID) || ids[d.ID] {
			return fmt.Errorf("invalid or duplicate desired operation id")
		}
		ids[d.ID] = true
		if e := validateOperation(d.Operation); e != nil {
			return e
		}
		if !allowed(b, d.Operation) {
			return fmt.Errorf("desired operation lacks an exact binding grant")
		}
	}
	return nil
}
func allowed(b AgentBinding, op nativeapi.Operation) bool {
	for _, g := range b.Allowed {
		if g.Operation == op.Name && g.Resource == op.Resource {
			return true
		}
	}
	return false
}
func hashBytes(data []byte) string { h := sha256.Sum256(data); return hex.EncodeToString(h[:]) }
func hashObject(v any) string      { b, _ := json.Marshal(v); return hashBytes(b) }
func bindingIdentityDigest(b AgentBinding) string {
	b.Allowed = nil
	b.Desired = nil
	return hashObject(b)
}
func planDigest(p Plan) string { p.Digest = ""; p.Revision = 0; return hashObject(p) }

// RedactInventory is an explicit optional host projection. Credential fields are
// removed, rather than retained with values that could leak. Provider adapters
// must still correctly classify every credential operation as Credential=true.
func RedactInventory(_ nativeapi.Operation, r nativeapi.Result) (json.RawMessage, error) {
	if len(r.Body) == 0 {
		return nil, nil
	}
	if len(r.Body) > 1<<20 {
		return nil, fmt.Errorf("inventory response exceeds bound")
	}
	decoder := json.NewDecoder(bytes.NewReader(r.Body))
	decoder.UseNumber()
	var value any
	if e := decoder.Decode(&value); e != nil {
		return nil, fmt.Errorf("inventory is not valid JSON")
	}
	if e := decoder.Decode(new(any)); e != io.EOF {
		return nil, fmt.Errorf("trailing inventory JSON")
	}
	clean, e := redact(value, 0)
	if e != nil {
		return nil, e
	}
	data, e := json.Marshal(clean)
	if e != nil {
		return nil, e
	}
	if e = nativeapi.ValidatePublicPayload(data); e != nil {
		return nil, fmt.Errorf("inventory projection contains credential material")
	}
	return data, nil
}
func redact(v any, depth int) (any, error) {
	if depth > 32 {
		return nil, fmt.Errorf("inventory nesting exceeds bound")
	}
	switch x := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for key, value := range x {
			probe, _ := json.Marshal(map[string]any{key: nil})
			if nativeapi.ValidatePublicPayload(probe) != nil {
				continue
			}
			clean, e := redact(value, depth+1)
			if e != nil {
				return nil, e
			}
			out[key] = clean
		}
		return out, nil
	case []any:
		out := make([]any, len(x))
		for i, value := range x {
			clean, e := redact(value, depth+1)
			if e != nil {
				return nil, e
			}
			out[i] = clean
		}
		return out, nil
	case string:
		if strings.HasPrefix(x, "Bearer ") || strings.Contains(x, "-----BEGIN PRIVATE KEY-----") || strings.Contains(x, "-----BEGIN RSA PRIVATE KEY-----") {
			return "[redacted]", nil
		}
		return x, nil
	default:
		return x, nil
	}
}
