// Package capability defines the unifying primitive of AIPlex.
//
// A Capability is a typed, addressable, governable unit of agent action.
// Every plane (tool, task, model, skill, memory, meta) is a kind of capability.
// See design/18-capability-mesh.md.
package capability

import (
	"fmt"
	"regexp"
	"strings"
)

// Kind is the discriminator for a capability.
type Kind string

const (
	KindTool     Kind = "tool"     // MCP tool
	KindTask     Kind = "task"     // A2A task
	KindModel    Kind = "model"    // LLM model
	KindSkill    Kind = "skill"    // Skill bundle
	KindMemory   Kind = "memory"   // Memory namespace
	KindAgent    Kind = "agent"    // Hosted agent runtime (ADK, LangGraph, custom)
	KindWorkflow Kind = "workflow" // Declarative cap chain executed by AIPlex
	KindMeta     Kind = "meta"     // AIPlex itself (deploy, register, govern)
)

// AllKinds returns every registered kind.
func AllKinds() []Kind {
	return []Kind{KindTool, KindTask, KindModel, KindSkill, KindMemory, KindAgent, KindWorkflow, KindMeta}
}

// Valid reports whether k is a known kind.
func (k Kind) Valid() bool {
	switch k {
	case KindTool, KindTask, KindModel, KindSkill, KindMemory, KindAgent, KindWorkflow, KindMeta:
		return true
	}
	return false
}

// Namespace returns the K8s namespace that hosts capability providers of this kind.
// Memory capabilities run in memplex, meta in aiplex-system, etc. Empty kinds default
// to aiplex-system.
func (k Kind) Namespace() string {
	switch k {
	case KindTool:
		return "mcplex"
	case KindTask:
		return "a2aplex"
	case KindSkill:
		return "skillsplex"
	case KindMemory:
		return "memplex"
	case KindAgent:
		return "agentplex"
	case KindWorkflow:
		return "aiplex-system" // workflows execute inside AIPlex itself
	case KindModel, KindMeta:
		return "aiplex-system"
	}
	return "aiplex-system"
}

// URI is a parsed capability URI:  cap://<kind>/<name>[/<sub-path>]@<version>
type URI struct {
	Kind    Kind
	Name    string // may contain "/" for hierarchical names (e.g. "students/alice/profile")
	Version string // "v1", "v1.2", "latest"
	Raw     string // canonical form
}

var (
	// Validates the version segment after '@'.
	versionPattern = regexp.MustCompile(`^(?:v\d+(?:\.\d+){0,2}|latest)$`)
	// Validates a name segment (slashes are allowed inside Name; each segment).
	// Dots are allowed for model identifiers like "gemini-2.5-flash".
	// Curly-brace template variables (e.g. `{tenant}`, `{user}`) are allowed
	// for parameterised namespaces — the constraint filter substitutes them
	// against JWT claims at request time. See design/20.
	segmentPattern = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9_\-\.]*|\{[A-Za-z][A-Za-z0-9_]*\})$`)
)

// ParseURI parses a capability URI. It returns an error on malformed input.
func ParseURI(s string) (URI, error) {
	const prefix = "cap://"
	if !strings.HasPrefix(s, prefix) {
		return URI{}, fmt.Errorf("capability URI must start with %q: %s", prefix, s)
	}
	rest := strings.TrimPrefix(s, prefix)

	at := strings.LastIndex(rest, "@")
	if at < 0 {
		return URI{}, fmt.Errorf("capability URI must include @version: %s", s)
	}
	left, version := rest[:at], rest[at+1:]
	if !versionPattern.MatchString(version) {
		return URI{}, fmt.Errorf("invalid version %q in URI %s", version, s)
	}

	slash := strings.IndexByte(left, '/')
	if slash < 0 {
		return URI{}, fmt.Errorf("capability URI must include kind/name: %s", s)
	}
	kindStr, name := left[:slash], left[slash+1:]
	kind := Kind(kindStr)
	if !kind.Valid() {
		return URI{}, fmt.Errorf("unknown kind %q in URI %s", kindStr, s)
	}
	if name == "" {
		return URI{}, fmt.Errorf("empty name in URI %s", s)
	}
	for _, seg := range strings.Split(name, "/") {
		if !segmentPattern.MatchString(seg) {
			return URI{}, fmt.Errorf("invalid name segment %q in URI %s", seg, s)
		}
	}

	return URI{
		Kind:    kind,
		Name:    name,
		Version: version,
		Raw:     s,
	}, nil
}

// MustParseURI parses or panics. Use only with constants.
func MustParseURI(s string) URI {
	u, err := ParseURI(s)
	if err != nil {
		panic(err)
	}
	return u
}

// String returns the canonical form, identical to the input that was parsed.
func (u URI) String() string {
	if u.Raw != "" {
		return u.Raw
	}
	return fmt.Sprintf("cap://%s/%s@%s", u.Kind, u.Name, u.Version)
}

// New builds a URI from its parts (handy for catalog sources).
func New(kind Kind, name, version string) URI {
	if version == "" {
		version = "v1"
	}
	return URI{
		Kind:    kind,
		Name:    name,
		Version: version,
		Raw:     fmt.Sprintf("cap://%s/%s@%s", kind, name, version),
	}
}

// IsZero reports whether u is the zero URI.
func (u URI) IsZero() bool { return u.Raw == "" && u.Name == "" && u.Kind == "" }

// PathSegment returns a URL-safe path segment for the capability — used by
// CapabilityRoute path templates and audit log streams. Slashes in Name are
// preserved; '@' is replaced with '_'.
func (u URI) PathSegment() string {
	return fmt.Sprintf("%s/%s@%s", u.Kind, u.Name, u.Version)
}
