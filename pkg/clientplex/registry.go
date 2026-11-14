package clientplex

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"sync"
)

// Renderer is trusted host-owned code. Registration APIs cannot load code or
// supply executable renderer URLs; external clients are added by composition.
type Renderer func(*Plan, Registration, Destination) error
type profileEntry struct {
	profile  Profile
	renderer Renderer
}
type Registry struct {
	mu      sync.RWMutex
	entries map[string]profileEntry
}

func NewRegistry() *Registry { return &Registry{entries: map[string]profileEntry{}} }
func cloneProfile(p Profile) Profile {
	p.Modes = slices.Clone(p.Modes)
	p.Protocols = slices.Clone(p.Protocols)
	p.Sources = slices.Clone(p.Sources)
	return p
}
func (r *Registry) Register(p Profile, renderer Renderer) error {
	if !idPattern.MatchString(p.ID) || p.Name == "" || p.Surface == "" || len(p.Modes) == 0 || len(p.Protocols) == 0 || renderer == nil {
		return fmt.Errorf("valid client profile and trusted renderer required")
	}
	for _, values := range [][]string{p.Modes, p.Protocols} {
		seen := map[string]bool{}
		for _, v := range values {
			if v == "" || seen[v] {
				return fmt.Errorf("invalid or duplicate profile capability")
			}
			seen[v] = true
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.entries == nil {
		r.entries = map[string]profileEntry{}
	}
	if _, ok := r.entries[p.ID]; ok {
		return fmt.Errorf("duplicate client profile %q", p.ID)
	}
	r.entries[p.ID] = profileEntry{cloneProfile(p), renderer}
	return nil
}
func (r *Registry) lookup(id string) (Profile, Renderer, error) {
	if r == nil {
		return Profile{}, nil, fmt.Errorf("client profile registry unavailable")
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, ok := r.entries[id]
	if !ok {
		return Profile{}, nil, fmt.Errorf("unsupported client profile %q", id)
	}
	return cloneProfile(entry.profile), entry.renderer, nil
}
func (r *Registry) Profiles() []Profile {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Profile, 0, len(r.entries))
	for _, entry := range r.entries {
		out = append(out, cloneProfile(entry.profile))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
func (r *Registry) BuildPlan(ctx context.Context, tenant string, registration Registration, resolver DestinationResolver) (Plan, error) {
	profile, renderer, e := r.lookup(registration.ProfileID)
	if e != nil {
		return Plan{}, e
	}
	return buildPlan(ctx, tenant, registration, resolver, profile, renderer)
}
func NewBuiltinRegistry() *Registry {
	r := NewRegistry()
	wrap := func(f func(*Plan, Registration, Destination)) Renderer {
		return func(p *Plan, s Registration, d Destination) error { f(p, s, d); return nil }
	}
	renderers := map[string]Renderer{"antigravity": wrap(renderAntigravity), "antigravity-acp": wrap(renderAntigravity), "codex": wrap(renderCodex), "claude-code": wrap(renderClaudeCode), "claude-desktop": wrap(renderClaudeDesktop), "gemini-cli": wrap(renderGeminiCLI)}
	for _, p := range builtin {
		if e := r.Register(p, renderers[p.ID]); e != nil {
			panic(e)
		}
	}
	return r
}

var defaultRegistry = NewBuiltinRegistry()
