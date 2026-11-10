package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"

	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

// applyAPI is the part of the SDK that `aiplex apply` uses, so the convergence
// logic can be tested without a server.
type applyAPI interface {
	ListInstances(ctx context.Context, opts *aiplex.ListInstancesOpts) ([]aiplex.Instance, error)
	Deploy(ctx context.Context, req *aiplex.DeployRequest) (*aiplex.Instance, error)
	GetAgent(ctx context.Context, clientID string) (*aiplex.Agent, error)
	RegisterAgent(ctx context.Context, req *aiplex.RegisterAgentRequest) (*aiplex.Agent, error)
	GetLLMRoute(ctx context.Context, modelID string) (*aiplex.LLMRouteConfig, error)
	PutLLMRoute(ctx context.Context, modelID string, rc *aiplex.LLMRouteConfig) (*aiplex.LLMRouteConfig, error)
}

type applyAction string

const (
	actCreated   applyAction = "created"
	actUpdated   applyAction = "updated"
	actUnchanged applyAction = "unchanged"
	actSkipped   applyAction = "skipped"
	actFailed    applyAction = "failed"
)

type applyResult struct {
	Kind, Name string
	Action     applyAction
	Detail     string
}

// applyManifest converges the server on the manifest: each object is created
// when absent, updated when it differs and can be updated, and left alone when
// it already matches. It never stops at the first failure; failures are
// reported per object. With dryRun nothing is written and the actions say what
// would happen.
//
// What can be updated in place is limited by the API. Routes have a PUT with an
// If-Match revision, so a differing route is fetched and replaced at the
// revision just read. Agents and instances have no update route: they are
// compared, and a difference is reported as a failure that names the field,
// never as a silent overwrite.
func applyManifest(ctx context.Context, api applyAPI, m *AiplexManifest, dryRun bool, skipInstancesReason string) []applyResult {
	var results []applyResult
	add := func(kind, name string, a applyAction, detail string) {
		results = append(results, applyResult{kind, name, a, detail})
	}

	if len(m.Instances) > 0 {
		if skipInstancesReason != "" {
			for _, inst := range m.Instances {
				add("instance", inst.Name, actSkipped, skipInstancesReason)
			}
		} else {
			existing, err := api.ListInstances(ctx, nil)
			for _, inst := range m.Instances {
				if err != nil {
					add("instance", inst.Name, actFailed, "list instances: "+err.Error())
					continue
				}
				a, detail := convergeInstance(ctx, api, existing, inst, dryRun)
				add("instance", inst.Name, a, detail)
			}
		}
	}
	for _, agent := range m.Agents {
		a, detail := convergeAgent(ctx, api, agent, dryRun)
		add("agent", agent.ClientID, a, detail)
	}
	for _, route := range m.Routes {
		a, detail := convergeRoute(ctx, api, route, dryRun)
		add("route", route.ModelID, a, detail)
	}
	return results
}

func convergeInstance(ctx context.Context, api applyAPI, existing []aiplex.Instance, want ManifestInstance, dryRun bool) (applyAction, string) {
	for _, have := range existing {
		if have.Plane != want.Plane || have.DisplayName != want.Name ||
			have.Status == "terminated" || have.Status == "failed" {
			continue
		}
		// Instances are matched by plane and display name. There is no update
		// route, so a different template or configuration is reported, not
		// replaced.
		if have.TemplateID != want.Template {
			return actFailed, fmt.Sprintf("field template: instance %q already runs template %q (manifest: %q); instances cannot be changed in place, undeploy it first (aiplex undeploy %s)", want.Name, have.TemplateID, want.Template, have.ID)
		}
		if !subsetEqual(jsonValue(want.Config), jsonValue(have.Config)) {
			return actFailed, fmt.Sprintf("field config: instance %q was deployed with different configuration; instances cannot be changed in place, undeploy it first (aiplex undeploy %s)", want.Name, have.ID)
		}
		return actUnchanged, have.ID
	}
	if dryRun {
		return actCreated, "would deploy"
	}
	inst, err := api.Deploy(ctx, &aiplex.DeployRequest{Plane: want.Plane, TemplateID: want.Template, DisplayName: want.Name, Config: want.Config})
	if err != nil {
		return actFailed, err.Error()
	}
	return actCreated, inst.ID
}

func convergeAgent(ctx context.Context, api applyAPI, want ManifestAgent, dryRun bool) (applyAction, string) {
	have, err := api.GetAgent(ctx, want.ClientID)
	switch {
	case err == nil:
		if diff := agentDiff(want, have); len(diff) > 0 {
			return actFailed, fmt.Sprintf("field %s differs from the registered agent; agents cannot be updated in place (their OAuth client is issued once), delete it first (aiplex agents delete %s) and apply again", strings.Join(diff, ", "), want.ClientID)
		}
		return actUnchanged, ""
	case !spIsNotFound(err):
		return actFailed, err.Error()
	}
	if dryRun {
		return actCreated, "would register"
	}
	_, err = api.RegisterAgent(ctx, &aiplex.RegisterAgentRequest{
		ClientID:      want.ClientID,
		DisplayName:   want.DisplayName,
		Description:   want.Description,
		AuthMethod:    want.AuthMethod,
		GrantTypes:    want.GrantTypes,
		AllowedScopes: want.AllowedScopes,
		WIFPrincipal:  want.WIFPrincipal,
		RedirectURIs:  want.RedirectURIs,
	})
	if err != nil {
		return actFailed, err.Error()
	}
	return actCreated, ""
}

// agentDiff names the manifest fields that differ from the registered agent.
// grant_types is compared only when the manifest names it (the server defaults
// it from auth_method).
func agentDiff(want ManifestAgent, have *aiplex.Agent) []string {
	var diff []string
	if want.DisplayName != have.DisplayName {
		diff = append(diff, "display_name")
	}
	if want.Description != have.Description {
		diff = append(diff, "description")
	}
	if want.AuthMethod != have.AuthMethod {
		diff = append(diff, "auth_method")
	}
	if len(want.GrantTypes) > 0 && !reflect.DeepEqual(sortedUnique(want.GrantTypes), sortedUnique(have.GrantTypes)) {
		diff = append(diff, "grant_types")
	}
	if !reflect.DeepEqual(sortedUnique(want.AllowedScopes), sortedUnique(have.AllowedScopes)) {
		diff = append(diff, "allowed_scopes")
	}
	if want.WIFPrincipal != have.WIFPrincipal {
		diff = append(diff, "wif_principal")
	}
	if !reflect.DeepEqual(sortedUnique(want.RedirectURIs), sortedUnique(have.RedirectURIs)) {
		diff = append(diff, "redirect_uris")
	}
	return diff
}

func convergeRoute(ctx context.Context, api applyAPI, want ManifestRoute, dryRun bool) (applyAction, string) {
	have, err := api.GetLLMRoute(ctx, want.ModelID)
	creating := false
	switch {
	case err == nil:
	case spIsNotFound(err):
		creating = true
	default:
		return actFailed, err.Error()
	}

	if !creating {
		// A revision in the manifest is a precondition the author chose: apply
		// only over that revision.
		if want.Revision > 0 && want.Revision != have.Revision {
			return actFailed, fmt.Sprintf("field revision: manifest expects revision %d but the saved route is at %d; refresh the manifest or remove the revision line", want.Revision, have.Revision)
		}
		if routeMatches(want, have) {
			return actUnchanged, fmt.Sprintf("revision %d", have.Revision)
		}
	}
	if dryRun {
		if creating {
			return actCreated, "would create"
		}
		return actUpdated, "would update"
	}

	body := &aiplex.LLMRouteConfig{
		ModelID:   want.ModelID,
		Backends:  want.Backends,
		Fallbacks: want.Fallbacks,
		Budget:    want.Budget,
	}
	if !creating {
		// Replace at the revision just read, keeping settings the manifest
		// cannot express (cache TTL, rate limit). Server-owned metadata is
		// recomputed by the server and not sent back.
		body.Revision = have.Revision
		body.CacheTTL = have.CacheTTL
		body.RateLimit = have.RateLimit
	}
	saved, err := api.PutLLMRoute(ctx, want.ModelID, body)
	if err != nil {
		return actFailed, err.Error()
	}
	if creating {
		return actCreated, fmt.Sprintf("revision %d", saved.Revision)
	}
	return actUpdated, fmt.Sprintf("revision %d -> %d", have.Revision, saved.Revision)
}

// routeMatches compares only what the manifest manages. A saved route may carry
// extra server-added fields on its backends; those do not count as differences.
func routeMatches(want ManifestRoute, have *aiplex.LLMRouteConfig) bool {
	desired := map[string]any{
		"backends":  jsonValue(want.Backends),
		"fallbacks": jsonValue(want.Fallbacks),
		"budget":    jsonValue(want.Budget),
	}
	actual := map[string]any{
		"backends":  jsonValue(have.Backends),
		"fallbacks": jsonValue(have.Fallbacks),
		"budget":    jsonValue(have.Budget),
	}
	return subsetEqual(desired, actual)
}

// jsonValue round-trips v through JSON so structs, maps and numbers compare
// uniformly.
func jsonValue(v any) any {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var out any
	if json.Unmarshal(raw, &out) != nil {
		return nil
	}
	return out
}

func isZeroJSON(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return x == ""
	case float64:
		return x == 0
	case bool:
		return !x
	case []any:
		return len(x) == 0
	case map[string]any:
		return len(x) == 0
	}
	return false
}

// subsetEqual reports whether every value the desired side sets is present and
// equal on the actual side. Keys only the actual side has are ignored (server
// defaults and metadata); a desired zero value matches an absent key. Arrays
// must have the same length and match element by element.
func subsetEqual(desired, actual any) bool {
	switch d := desired.(type) {
	case map[string]any:
		a, _ := actual.(map[string]any)
		for k, dv := range d {
			av, ok := a[k]
			if !ok {
				if !isZeroJSON(dv) {
					return false
				}
				continue
			}
			if !subsetEqual(dv, av) {
				return false
			}
		}
		return true
	case []any:
		a, ok := actual.([]any)
		if !ok {
			return len(d) == 0
		}
		if len(a) != len(d) {
			return false
		}
		for i := range d {
			if !subsetEqual(d[i], a[i]) {
				return false
			}
		}
		return true
	case nil:
		return isZeroJSON(actual)
	default:
		return reflect.DeepEqual(desired, actual) || (isZeroJSON(desired) && isZeroJSON(actual))
	}
}

func sortedUnique(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// printApplyResults writes one line per object and a summary, and returns the
// number of failures.
func printApplyResults(w io.Writer, results []applyResult, dryRun bool) int {
	counts := map[applyAction]int{}
	for _, r := range results {
		counts[r.Action]++
		label := string(r.Action)
		if dryRun && r.Action == actCreated {
			label = "would create"
		} else if dryRun && r.Action == actUpdated {
			label = "would update"
		}
		detail := r.Detail
		if dryRun && (strings.HasPrefix(detail, "would ")) {
			detail = ""
		}
		line := fmt.Sprintf("  %-13s %s %s", label, r.Kind, r.Name)
		if detail != "" {
			line += "  (" + detail + ")"
		}
		fmt.Fprintln(w, line)
	}
	fmt.Fprintln(w)
	prefix := "Summary"
	if dryRun {
		prefix = "Plan (dry run, nothing written)"
	}
	fmt.Fprintf(w, "%s: %d created, %d updated, %d unchanged, %d skipped, %d failed\n",
		prefix, counts[actCreated], counts[actUpdated], counts[actUnchanged], counts[actSkipped], counts[actFailed])
	return counts[actFailed]
}
