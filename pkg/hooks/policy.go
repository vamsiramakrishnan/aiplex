package hooks

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
)

// Kind is the tenant resource kind of a hook policy.
const Kind = "hook-policy"

// Planes.
const (
	PlaneModels = "llmplex"
	PlaneTools  = "mcplex"
)

// Phases.
const (
	PhaseRequest  = "request"
	PhaseResponse = "response"
)

// Scope kinds, broadest first.
const (
	ScopeTenant         = "tenant"
	ScopeAccessPoint    = "access_point"
	ScopeModelSurface   = "model_surface"
	ScopeToolConnection = "tool_connection"
)

// Streaming settings for a streamed response when a response hook applies.
const (
	StreamingDeny        = "deny"
	StreamingBuffer      = "buffer"
	StreamingPassthrough = "passthrough_uninspected"
)

// Hook types.
const (
	TypeRedact    = "redact"
	TypeSizeLimit = "size_limit"
	TypeHTTP      = "http"
)

// Limits on a policy.
const (
	MaxHooksPerPolicy = 16
	MaxRulesPerHook   = 64
	MaxPatternLength  = 1024
	MaxTimeoutMS      = 30000
	DefaultTimeoutMS  = 2000
	// MaxInspectBytes bounds the text one hook run sees (sum of part bytes).
	MaxInspectBytes = 4 << 20
)

// Policy binds an ordered hook list to a scope. A request collects every
// policy whose scope matches it; broader scopes run first and a narrower
// scope can only add hooks (see Resolve).
type Policy struct {
	ID          string   `json:"id"`
	Description string   `json:"description,omitempty"`
	Scope       Scope    `json:"scope"`
	Planes      []string `json:"planes,omitempty"`
	// Streaming applies to a streamed model response when a response-phase
	// hook applies: deny (default), buffer or passthrough_uninspected.
	Streaming string `json:"streaming,omitempty"`
	// NonText decides requests and responses with parts AIPlex cannot
	// inspect (images, audio, files): allow (default, counted in the
	// ledger as uninspected_parts) or deny.
	NonText string `json:"non_text,omitempty"`
	Hooks   []Hook `json:"hooks"`
}

// Scope selects where a policy applies. ID is empty for the tenant scope.
type Scope struct {
	Kind string `json:"kind"`
	ID   string `json:"id,omitempty"`
}

// Hook is one configured hook.
type Hook struct {
	ID     string   `json:"id"`
	Type   string   `json:"type"`
	Phases []string `json:"phases"`
	// TimeoutMS bounds one run of the hook (default 2000, at most 30000).
	TimeoutMS int `json:"timeout_ms,omitempty"`
	// OnUninspectable decides a body the hook cannot inspect (a wire format
	// with no extractor, or a body that does not parse): deny (default) or
	// allow.
	OnUninspectable string `json:"on_uninspectable,omitempty"`
	// Message is the safe public message a caller sees when this hook
	// denies. It must not contain anything secret.
	Message   string           `json:"message,omitempty"`
	Redact    *RedactConfig    `json:"redact,omitempty"`
	SizeLimit *SizeLimitConfig `json:"size_limit,omitempty"`
	HTTP      *HTTPConfig      `json:"http,omitempty"`
}

var identifier = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,62}$`)

// DecodePolicy decodes and validates a stored or submitted spec. Unknown
// fields at any level are refused.
func DecodePolicy(id string, raw []byte) (Policy, error) {
	var p Policy
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return Policy{}, fmt.Errorf("invalid hook policy: %v", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return Policy{}, fmt.Errorf("invalid hook policy: trailing data")
	}
	if id != "" && p.ID != id {
		return Policy{}, fmt.Errorf("id must equal the resource id %q", id)
	}
	return p, p.Validate()
}

// Validate checks a policy. Every regex is compiled here, so a pattern that
// does not compile is refused at save time.
func (p Policy) Validate() error {
	if !identifier.MatchString(p.ID) {
		return fmt.Errorf("id must be 1-63 lowercase letters, digits, '.', '_' or '-'")
	}
	if len(p.Description) > 512 {
		return fmt.Errorf("description is longer than 512 characters")
	}
	switch p.Scope.Kind {
	case ScopeTenant:
		if p.Scope.ID != "" {
			return fmt.Errorf("scope.id must be empty for the tenant scope")
		}
	case ScopeAccessPoint, ScopeModelSurface, ScopeToolConnection:
		if p.Scope.ID == "" || len(p.Scope.ID) > 128 || strings.ContainsAny(p.Scope.ID, " /\r\n\t") {
			return fmt.Errorf("scope.id is required for scope %s", p.Scope.Kind)
		}
	default:
		return fmt.Errorf("scope.kind must be tenant, access_point, model_surface or tool_connection")
	}
	seenPlane := map[string]bool{}
	for _, plane := range p.Planes {
		if (plane != PlaneModels && plane != PlaneTools) || seenPlane[plane] {
			return fmt.Errorf("planes must be distinct values of llmplex and mcplex")
		}
		seenPlane[plane] = true
	}
	if p.Scope.Kind == ScopeModelSurface && seenPlane[PlaneTools] || p.Scope.Kind == ScopeToolConnection && seenPlane[PlaneModels] {
		return fmt.Errorf("planes do not match scope %s", p.Scope.Kind)
	}
	switch p.Streaming {
	case "", StreamingDeny, StreamingBuffer, StreamingPassthrough:
	default:
		return fmt.Errorf("streaming must be deny, buffer or passthrough_uninspected")
	}
	switch p.NonText {
	case "", "allow", "deny":
	default:
		return fmt.Errorf("non_text must be allow or deny")
	}
	if len(p.Hooks) == 0 || len(p.Hooks) > MaxHooksPerPolicy {
		return fmt.Errorf("hooks must list 1 to %d hooks", MaxHooksPerPolicy)
	}
	seen := map[string]bool{}
	for i, h := range p.Hooks {
		if err := h.validate(); err != nil {
			return fmt.Errorf("hooks[%d]: %w", i, err)
		}
		if seen[h.ID] {
			return fmt.Errorf("hooks[%d]: duplicate hook id %q", i, h.ID)
		}
		seen[h.ID] = true
	}
	return nil
}

func (h Hook) validate() error {
	if !identifier.MatchString(h.ID) {
		return fmt.Errorf("id must be 1-63 lowercase letters, digits, '.', '_' or '-'")
	}
	if len(h.Phases) == 0 {
		return fmt.Errorf("phases is required (request, response or both)")
	}
	seen := map[string]bool{}
	for _, phase := range h.Phases {
		if (phase != PhaseRequest && phase != PhaseResponse) || seen[phase] {
			return fmt.Errorf("phases must be distinct values of request and response")
		}
		seen[phase] = true
	}
	if h.TimeoutMS < 0 || h.TimeoutMS > MaxTimeoutMS {
		return fmt.Errorf("timeout_ms must be between 1 and %d", MaxTimeoutMS)
	}
	switch h.OnUninspectable {
	case "", "deny", "allow":
	default:
		return fmt.Errorf("on_uninspectable must be deny or allow")
	}
	if len(h.Message) > 256 || !printable(h.Message) {
		return fmt.Errorf("message must be printable text of at most 256 characters")
	}
	configs := 0
	for _, set := range []bool{h.Redact != nil, h.SizeLimit != nil, h.HTTP != nil} {
		if set {
			configs++
		}
	}
	if configs > 1 {
		return fmt.Errorf("exactly one of redact, size_limit or http may be set")
	}
	switch h.Type {
	case TypeRedact:
		if h.Redact == nil {
			return fmt.Errorf("type redact requires a redact block")
		}
		_, err := compileRedact(*h.Redact)
		return err
	case TypeSizeLimit:
		if h.SizeLimit == nil {
			return fmt.Errorf("type size_limit requires a size_limit block")
		}
		return h.SizeLimit.validate()
	case TypeHTTP:
		if h.HTTP == nil {
			return fmt.Errorf("type http requires an http block")
		}
		return h.HTTP.validate()
	}
	return fmt.Errorf("unknown hook type %q (redact, size_limit or http)", h.Type)
}

func printable(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func (h Hook) timeoutMS() int {
	if h.TimeoutMS == 0 {
		return DefaultTimeoutMS
	}
	return h.TimeoutMS
}

func (h Hook) runsIn(phase string) bool {
	for _, p := range h.Phases {
		if p == phase {
			return true
		}
	}
	return false
}

// Target is what one exchange is: the scopes it falls under.
type Target struct {
	Plane         string
	AccessPointID string
	ModelSurface  string
	// ModelSurfaces adds further model surfaces: a routed access-point model
	// resolves the policies of every one of its backends together, so the
	// request hooks run once with the union (scopes add, never remove).
	ModelSurfaces  []string
	ToolConnection string
}

// Resolved is the effective configuration for one exchange.
type Resolved struct {
	Hooks []ResolvedHook
	// Streaming and NonText are the most restrictive settings of every
	// matching policy, so a narrower scope cannot loosen a broader one.
	Streaming string
	NonText   string
}

// ResolvedHook is a hook with the policy it came from.
type ResolvedHook struct {
	PolicyID string
	Hook
}

// Name is the hook's ledger identifier: policy/hook.
func (h ResolvedHook) Name() string { return h.PolicyID + "/" + h.ID }

func scopeRank(kind string) int {
	switch kind {
	case ScopeTenant:
		return 0
	case ScopeAccessPoint:
		return 1
	}
	return 2
}

// Resolve selects the policies that apply to target. Tenant policies run
// first, then access point, then model surface or tool connection; within a
// scope, policies run in id order. A narrower scope adds hooks; it cannot
// remove or weaken a broader one.
func Resolve(policies []Policy, target Target) Resolved {
	matched := []Policy{}
	for _, p := range policies {
		if !p.applies(target) {
			continue
		}
		matched = append(matched, p)
	}
	sort.SliceStable(matched, func(i, j int) bool {
		ri, rj := scopeRank(matched[i].Scope.Kind), scopeRank(matched[j].Scope.Kind)
		if ri != rj {
			return ri < rj
		}
		return matched[i].ID < matched[j].ID
	})
	out := Resolved{Streaming: StreamingPassthrough, NonText: "allow"}
	if len(matched) == 0 {
		return out
	}
	strict := map[string]int{StreamingPassthrough: 0, StreamingBuffer: 1, StreamingDeny: 2}
	for _, p := range matched {
		streaming := p.Streaming
		if streaming == "" {
			streaming = StreamingDeny
		}
		if strict[streaming] > strict[out.Streaming] {
			out.Streaming = streaming
		}
		if p.NonText == "deny" {
			out.NonText = "deny"
		}
		for _, h := range p.Hooks {
			out.Hooks = append(out.Hooks, ResolvedHook{PolicyID: p.ID, Hook: h})
		}
	}
	return out
}

// Resolved returns the policy's own hooks and settings, ignoring its scope
// and planes (a dry run).
func (p Policy) Resolved() Resolved {
	out := Resolved{Streaming: p.Streaming, NonText: p.NonText}
	if out.Streaming == "" {
		out.Streaming = StreamingDeny
	}
	if out.NonText == "" {
		out.NonText = "allow"
	}
	for _, h := range p.Hooks {
		out.Hooks = append(out.Hooks, ResolvedHook{PolicyID: p.ID, Hook: h})
	}
	return out
}

func (p Policy) applies(t Target) bool {
	if len(p.Planes) > 0 {
		found := false
		for _, plane := range p.Planes {
			found = found || plane == t.Plane
		}
		if !found {
			return false
		}
	}
	switch p.Scope.Kind {
	case ScopeTenant:
		return true
	case ScopeAccessPoint:
		return t.AccessPointID != "" && p.Scope.ID == t.AccessPointID
	case ScopeModelSurface:
		if t.Plane != PlaneModels {
			return false
		}
		for _, s := range t.ModelSurfaces {
			if p.Scope.ID == s {
				return true
			}
		}
		return p.Scope.ID == t.ModelSurface
	case ScopeToolConnection:
		return t.Plane == PlaneTools && p.Scope.ID == t.ToolConnection
	}
	return false
}

// HasPhase reports whether any hook runs in phase.
func (r Resolved) HasPhase(phase string) bool {
	for _, h := range r.Hooks {
		if h.runsIn(phase) {
			return true
		}
	}
	return false
}
