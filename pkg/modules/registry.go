// Package modules defines AIPlex's provider-independent module contract.
// It has no dependency on the API server, persistence, or a particular Plex.
package modules

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

const ContractVersion = "v1"

// Dependency names a module rather than implicitly choosing a capability provider.
// Optional dependencies may be absent; when present they must be compatible.
type Dependency struct {
	Name            string `json:"name"`
	ContractVersion string `json:"contract_version"`
	MinVersion      string `json:"min_version,omitempty"`
	Optional        bool   `json:"optional,omitempty"`
}

// Navigation and Command describe host-owned UI/CLI contributions. They are
// discovery metadata; the host still registers trusted implementations explicitly.
type Navigation struct {
	Path  string `json:"path"`
	Title string `json:"title"`
}
type Command struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type Descriptor struct {
	Name            string       `json:"name"`
	Version         string       `json:"version"`
	ContractVersion string       `json:"contract_version"`
	Capabilities    []string     `json:"capabilities,omitempty"`
	Dependencies    []Dependency `json:"dependencies,omitempty"`
	APIPrefixes     []string     `json:"api_prefixes,omitempty"`
	Navigation      []Navigation `json:"navigation,omitempty"`
	Commands        []Command    `json:"commands,omitempty"`
	// ConfigSchema advertises module-owned configuration. The module validates
	// its values; the registry only checks that this is a JSON schema object.
	ConfigSchema json.RawMessage `json:"config_schema,omitempty"`
}

// Module is the in-process lifecycle contract. An out-of-process adapter can
// implement it using the remote module's versioned discovery and health APIs.
// Start and Stop must honor context cancellation and Stop must tolerate partial
// startup. Every enabled module is required; omit disabled optional modules.
type Module interface {
	Descriptor() Descriptor
	Start(context.Context) error
	Ready(context.Context) error
	Stop(context.Context) error
}

type entry struct {
	module     Module
	descriptor Descriptor
}
type Registry struct {
	mu      sync.RWMutex
	entries map[string]entry
	frozen  bool
}

func NewRegistry() *Registry { return &Registry{entries: map[string]entry{}} }

var namePattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`)
var capabilityPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[.:-][a-z0-9]+)*$`)

func (d Descriptor) Validate() error {
	if !namePattern.MatchString(d.Name) {
		return fmt.Errorf("invalid module name %q", d.Name)
	}
	if _, err := parseVersion(d.Version); err != nil {
		return fmt.Errorf("module %s: %w", d.Name, err)
	}
	if d.ContractVersion != ContractVersion {
		return fmt.Errorf("module %s: unsupported contract %q", d.Name, d.ContractVersion)
	}
	if len(d.ConfigSchema) > 0 {
		var schema map[string]any
		if err := json.Unmarshal(d.ConfigSchema, &schema); err != nil || schema == nil {
			return fmt.Errorf("module %s: configuration schema must be a JSON object", d.Name)
		}
	}
	seen := map[string]bool{}
	for _, c := range d.Capabilities {
		if !capabilityPattern.MatchString(c) || seen[c] {
			return fmt.Errorf("module %s: invalid or duplicate capability %q", d.Name, c)
		}
		seen[c] = true
	}
	seen = map[string]bool{}
	for _, dep := range d.Dependencies {
		if !namePattern.MatchString(dep.Name) || dep.Name == d.Name || seen[dep.Name] {
			return fmt.Errorf("module %s: invalid or duplicate dependency %q", d.Name, dep.Name)
		}
		seen[dep.Name] = true
		if dep.ContractVersion != ContractVersion {
			return fmt.Errorf("module %s: unsupported dependency contract %q", d.Name, dep.ContractVersion)
		}
		if dep.MinVersion != "" {
			if _, err := parseVersion(dep.MinVersion); err != nil {
				return fmt.Errorf("module %s dependency %s: %w", d.Name, dep.Name, err)
			}
		}
	}
	seen = map[string]bool{}
	for _, p := range d.APIPrefixes {
		if !validPath(p) || p == "/" || seen[p] {
			return fmt.Errorf("module %s: invalid or duplicate API prefix %q", d.Name, p)
		}
		seen[p] = true
	}
	seen = map[string]bool{}
	for _, n := range d.Navigation {
		if !validPath(n.Path) || strings.TrimSpace(n.Title) == "" || seen[n.Path] {
			return fmt.Errorf("module %s: invalid or duplicate navigation %q", d.Name, n.Path)
		}
		seen[n.Path] = true
	}
	seen = map[string]bool{}
	for _, c := range d.Commands {
		if !namePattern.MatchString(c.Name) || seen[c.Name] {
			return fmt.Errorf("module %s: invalid or duplicate command %q", d.Name, c.Name)
		}
		seen[c.Name] = true
	}
	return nil
}

func validPath(p string) bool {
	return strings.HasPrefix(p, "/") && path.Clean(p) == p && (p == "/" || !strings.HasSuffix(p, "/")) && !strings.ContainsAny(p, "?#% \\ \t\r\n")
}

// Only release versions are accepted so compatibility never silently treats a
// prerelease as the corresponding stable release.
func parseVersion(s string) ([3]uint64, error) {
	var v [3]uint64
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return v, fmt.Errorf("invalid release version %q (want major.minor.patch)", s)
	}
	for i, p := range parts {
		if p == "" || (len(p) > 1 && p[0] == '0') {
			return v, fmt.Errorf("invalid release version %q", s)
		}
		for _, c := range p {
			if c < '0' || c > '9' {
				return v, fmt.Errorf("invalid release version %q", s)
			}
		}
		n, err := strconv.ParseUint(p, 10, 64)
		if err != nil {
			return v, fmt.Errorf("invalid release version %q", s)
		}
		v[i] = n
	}
	return v, nil
}
func atLeast(actual, minimum string) bool {
	a, _ := parseVersion(actual)
	b, _ := parseVersion(minimum)
	// A minimum version never implies compatibility with a new major version.
	if a[0] != b[0] {
		return false
	}
	for i := 1; i < 3; i++ {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return true
}
func clone(d Descriptor) Descriptor {
	d.Capabilities = append([]string(nil), d.Capabilities...)
	d.Dependencies = append([]Dependency(nil), d.Dependencies...)
	d.APIPrefixes = append([]string(nil), d.APIPrefixes...)
	d.Navigation = append([]Navigation(nil), d.Navigation...)
	d.Commands = append([]Command(nil), d.Commands...)
	d.ConfigSchema = append(json.RawMessage(nil), d.ConfigSchema...)
	return d
}

func (r *Registry) Register(m Module) error {
	if m == nil || (reflect.ValueOf(m).Kind() == reflect.Ptr && reflect.ValueOf(m).IsNil()) {
		return fmt.Errorf("nil module")
	}
	d := clone(m.Descriptor())
	if err := d.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.frozen {
		return fmt.Errorf("module registry is frozen")
	}
	if r.entries == nil {
		r.entries = map[string]entry{}
	}
	if _, exists := r.entries[d.Name]; exists {
		return fmt.Errorf("duplicate module %s", d.Name)
	}
	for name, e := range r.entries {
		for _, p := range d.APIPrefixes {
			for _, q := range e.descriptor.APIPrefixes {
				if p == q || strings.HasPrefix(p, q+"/") || strings.HasPrefix(q, p+"/") {
					return fmt.Errorf("module %s API prefix %s conflicts with %s", d.Name, p, name)
				}
			}
		}
		for _, n := range d.Navigation {
			for _, x := range e.descriptor.Navigation {
				if n.Path == x.Path {
					return fmt.Errorf("module %s navigation %s conflicts with %s", d.Name, n.Path, name)
				}
			}
		}
		for _, c := range d.Commands {
			for _, x := range e.descriptor.Commands {
				if c.Name == x.Name {
					return fmt.Errorf("module %s command %s conflicts with %s", d.Name, c.Name, name)
				}
			}
		}
	}
	r.entries[d.Name] = entry{m, d}
	return nil
}

func (r *Registry) Descriptors() []Descriptor {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := sortedNames(r.entries)
	out := make([]Descriptor, 0, len(names))
	for _, n := range names {
		out = append(out, clone(r.entries[n].descriptor))
	}
	return out
}

func sortedNames(entries map[string]entry) []string {
	names := make([]string, 0, len(entries))
	for n := range entries {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Plan validates the complete dependency graph without starting any module.
func (r *Registry) Plan() ([]Descriptor, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	plan, err := plan(r.entries)
	if err != nil {
		return nil, err
	}
	out := make([]Descriptor, 0, len(plan))
	for _, e := range plan {
		out = append(out, clone(e.descriptor))
	}
	return out, nil
}

func plan(entries map[string]entry) ([]entry, error) {
	state := map[string]int{}
	out := make([]entry, 0, len(entries))
	var visit func(string) error
	visit = func(name string) error {
		if state[name] == 1 {
			return fmt.Errorf("module dependency cycle at %s", name)
		}
		if state[name] == 2 {
			return nil
		}
		state[name] = 1
		e := entries[name]
		deps := append([]Dependency(nil), e.descriptor.Dependencies...)
		sort.Slice(deps, func(i, j int) bool { return deps[i].Name < deps[j].Name })
		for _, d := range deps {
			dep, ok := entries[d.Name]
			if !ok {
				if d.Optional {
					continue
				}
				return fmt.Errorf("module %s requires missing module %s", name, d.Name)
			}
			if d.ContractVersion != dep.descriptor.ContractVersion || (d.MinVersion != "" && !atLeast(dep.descriptor.Version, d.MinVersion)) {
				return fmt.Errorf("module %s requires incompatible module %s", name, d.Name)
			}
			if err := visit(d.Name); err != nil {
				return err
			}
		}
		state[name] = 2
		out = append(out, e)
		return nil
	}
	for _, n := range sortedNames(entries) {
		if err := visit(n); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (r *Registry) freeze() ([]entry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entries, err := plan(r.entries)
	if err != nil {
		return nil, err
	}
	r.frozen = true
	return entries, nil
}
