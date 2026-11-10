package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// manifestPlanes are the planes a manifest instance may name.
var manifestPlanes = []string{"mcplex", "a2aplex", "llmplex", "skillsplex"}

// loadManifest reads a manifest file. YAML is converted to JSON first so that
// one set of field names (the JSON tags, which the SDK types also use) applies
// to both formats; a field the manifest type does not know is an error rather
// than being ignored.
func loadManifest(path string) (*AiplexManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".json", ".yaml", ".yml":
	default:
		return nil, fmt.Errorf("unsupported file extension %q (expected .yaml, .yml or .json)", ext)
	}
	raw := data
	if ext != ".json" {
		var doc any
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return nil, fmt.Errorf("invalid YAML: %w", err)
		}
		if raw, err = json.Marshal(yamlToJSON(doc)); err != nil {
			return nil, fmt.Errorf("invalid YAML: %w", err)
		}
	}
	var manifest AiplexManifest
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&manifest); err != nil {
		return nil, fmt.Errorf("invalid manifest: %w", err)
	}
	if manifest.Version == "" {
		manifest.Version = "v1"
	}
	return &manifest, nil
}

// yamlToJSON turns yaml.v3's generic values into ones encoding/json accepts.
func yamlToJSON(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			x[k] = yamlToJSON(e)
		}
		return x
	case map[any]any:
		out := map[string]any{}
		for k, e := range x {
			out[fmt.Sprint(k)] = yamlToJSON(e)
		}
		return out
	case []any:
		for i, e := range x {
			x[i] = yamlToJSON(e)
		}
		return x
	}
	return v
}

// validateManifest is every check `aiplex apply` makes before it writes
// anything; `aiplex validate` runs exactly this and nothing else.
func validateManifest(m *AiplexManifest) []string {
	var errs []string
	if m.Version != "v1" {
		errs = append(errs, fmt.Sprintf("version %q is not supported (use v1, or omit it)", m.Version))
	}
	if len(m.Instances)+len(m.Agents)+len(m.Routes) == 0 {
		errs = append(errs, "manifest must contain at least one of: instances, agents, routes")
	}
	for i, inst := range m.Instances {
		at := fmt.Sprintf("instances[%d]", i)
		if inst.Name == "" {
			errs = append(errs, at+": missing required field 'name'")
		}
		if inst.Template == "" {
			errs = append(errs, at+": missing required field 'template'")
		}
		valid := false
		for _, p := range manifestPlanes {
			valid = valid || inst.Plane == p
		}
		if !valid {
			errs = append(errs, fmt.Sprintf("%s: invalid plane %q (expected %s)", at, inst.Plane, strings.Join(manifestPlanes, ", ")))
		}
	}
	for i, a := range m.Agents {
		at := fmt.Sprintf("agents[%d]", i)
		if a.ClientID == "" {
			errs = append(errs, at+": missing required field 'client_id'")
		}
		if a.AuthMethod == "authorization_code" && len(a.RedirectURIs) == 0 {
			errs = append(errs, at+": auth_method authorization_code requires at least one redirect_uris entry")
		}
		if a.AuthMethod != "authorization_code" && len(a.RedirectURIs) > 0 {
			errs = append(errs, at+": redirect_uris apply only to auth_method authorization_code")
		}
	}
	seen := map[string]bool{}
	for i, r := range m.Routes {
		at := fmt.Sprintf("routes[%d]", i)
		if r.ModelID == "" {
			errs = append(errs, at+": missing required field 'model_id'")
		}
		if len(r.Backends) == 0 {
			errs = append(errs, at+": needs at least one backend")
		}
		for _, f := range r.Fallbacks {
			// A fallback must be a route that is saved first, so it has to come
			// earlier in this file (or already exist on the server).
			if f == r.ModelID {
				errs = append(errs, fmt.Sprintf("%s: route %q lists itself as a fallback", at, r.ModelID))
			}
		}
		if r.ModelID != "" {
			if seen[r.ModelID] {
				errs = append(errs, fmt.Sprintf("%s: route %q is listed twice", at, r.ModelID))
			}
			seen[r.ModelID] = true
		}
	}
	return errs
}
