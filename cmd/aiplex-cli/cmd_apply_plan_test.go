package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

// fakeApplyAPI is an in-memory server with the real API's update rules: agents
// and instances are create-only, routes are replaced at the revision read.
type fakeApplyAPI struct {
	instances []aiplex.Instance
	agents    map[string]*aiplex.Agent
	routes    map[string]*aiplex.LLMRouteConfig
	writes    int
	lastPut   *aiplex.LLMRouteConfig
}

func newFakeApplyAPI() *fakeApplyAPI {
	return &fakeApplyAPI{agents: map[string]*aiplex.Agent{}, routes: map[string]*aiplex.LLMRouteConfig{}}
}

func notFound() error { return &aiplex.Error{Code: "NOT_FOUND", StatusCode: http.StatusNotFound} }

func (f *fakeApplyAPI) ListInstances(context.Context, *aiplex.ListInstancesOpts) ([]aiplex.Instance, error) {
	return f.instances, nil
}
func (f *fakeApplyAPI) Deploy(_ context.Context, r *aiplex.DeployRequest) (*aiplex.Instance, error) {
	f.writes++
	inst := aiplex.Instance{ID: fmt.Sprintf("inst-%d", len(f.instances)+1), Plane: r.Plane, TemplateID: r.TemplateID, DisplayName: r.DisplayName, Config: r.Config, Status: "running"}
	f.instances = append(f.instances, inst)
	return &inst, nil
}
func (f *fakeApplyAPI) GetAgent(_ context.Context, id string) (*aiplex.Agent, error) {
	if a, ok := f.agents[id]; ok {
		return a, nil
	}
	return nil, notFound()
}
func (f *fakeApplyAPI) RegisterAgent(_ context.Context, r *aiplex.RegisterAgentRequest) (*aiplex.Agent, error) {
	f.writes++
	if _, ok := f.agents[r.ClientID]; ok {
		return nil, &aiplex.Error{StatusCode: http.StatusConflict}
	}
	grants := r.GrantTypes
	if len(grants) == 0 {
		grants = []string{r.AuthMethod}
	}
	a := &aiplex.Agent{Revision: 1, ClientID: r.ClientID, DisplayName: r.DisplayName, Description: r.Description, AuthMethod: r.AuthMethod, GrantTypes: grants, AllowedScopes: r.AllowedScopes, Status: "active", RegisteredBy: "me"}
	f.agents[r.ClientID] = a
	return a, nil
}
func (f *fakeApplyAPI) GetLLMRoute(_ context.Context, id string) (*aiplex.LLMRouteConfig, error) {
	if r, ok := f.routes[id]; ok {
		return r, nil
	}
	return nil, notFound()
}
func (f *fakeApplyAPI) PutLLMRoute(_ context.Context, id string, rc *aiplex.LLMRouteConfig) (*aiplex.LLMRouteConfig, error) {
	cur := f.routes[id]
	want := int64(0)
	if cur != nil {
		want = cur.Revision
	}
	if rc.Revision != want {
		return nil, &aiplex.Error{Code: "REVISION_CONFLICT", StatusCode: http.StatusPreconditionFailed}
	}
	f.writes++
	f.lastPut = rc
	saved := *rc
	saved.Backends = append([]aiplex.LLMBackend(nil), rc.Backends...)
	saved.Revision = want + 1
	saved.ID, saved.Owner = "route-"+id, "me" // server-owned fields
	f.routes[id] = &saved
	return &saved, nil
}

func sampleManifest() *AiplexManifest {
	return &AiplexManifest{
		Version:   "v1",
		Instances: []ManifestInstance{{Name: "kb", Plane: "mcplex", Template: "kb-search", Config: map[string]any{"corpus": "default"}}},
		Agents: []ManifestAgent{{ClientID: "tutor", DisplayName: "Tutor", AuthMethod: "client_credentials",
			AllowedScopes: []string{"mcp:tools:b", "mcp:tools:a"}}},
		Routes: []ManifestRoute{{ModelID: "m", Backends: []aiplex.LLMBackend{{Provider: "openai", ModelID: "gpt", Weight: 100, Enabled: true}},
			Budget: &aiplex.UsageBudget{MaxDailyCostUSD: 10}}},
	}
}

func counts(results []applyResult) map[applyAction]int {
	c := map[applyAction]int{}
	for _, r := range results {
		c[r.Action]++
	}
	return c
}

func TestApplyConvergesAndSecondRunIsUnchanged(t *testing.T) {
	api, m := newFakeApplyAPI(), sampleManifest()
	first := applyManifest(context.Background(), api, m, false, "")
	if c := counts(first); c[actCreated] != 3 || len(first) != 3 {
		t.Fatalf("first run: %+v", first)
	}
	writes := api.writes
	second := applyManifest(context.Background(), api, m, false, "")
	if c := counts(second); c[actUnchanged] != 3 {
		t.Fatalf("second run should be unchanged: %+v", second)
	}
	if api.writes != writes {
		t.Fatal("unchanged apply wrote")
	}
}

func TestApplyUpdatesRouteAtTheRevisionJustRead(t *testing.T) {
	api, m := newFakeApplyAPI(), sampleManifest()
	applyManifest(context.Background(), api, m, false, "")
	api.routes["m"].CacheTTL = 30 // set outside the manifest
	m.Routes[0].Backends[0].Weight = 50
	res := applyManifest(context.Background(), api, m, false, "")
	var route applyResult
	for _, r := range res {
		if r.Kind == "route" {
			route = r
		}
	}
	if route.Action != actUpdated || !strings.Contains(route.Detail, "1 -> 2") {
		t.Fatalf("route: %+v", route)
	}
	if api.lastPut.Revision != 1 || api.lastPut.CacheTTL != 30 {
		t.Fatalf("update should carry the read revision and unmanaged settings: %+v", api.lastPut)
	}
	if api.lastPut.Owner != "" || api.lastPut.ID != "" {
		t.Fatal("server-owned metadata was sent back")
	}
	if again := applyManifest(context.Background(), api, m, false, ""); counts(again)[actUnchanged] != 3 {
		t.Fatalf("not idempotent after update: %+v", again)
	}
}

func TestApplyManifestRevisionIsAPrecondition(t *testing.T) {
	api, m := newFakeApplyAPI(), sampleManifest()
	applyManifest(context.Background(), api, m, false, "")
	m.Routes[0].Backends[0].Weight = 1
	m.Routes[0].Revision = 7
	for _, r := range applyManifest(context.Background(), api, m, false, "") {
		if r.Kind == "route" && (r.Action != actFailed || !strings.Contains(r.Detail, "field revision")) {
			t.Fatalf("stale revision applied: %+v", r)
		}
	}
}

func TestApplyAgentAndInstanceDifferencesNameTheField(t *testing.T) {
	api, m := newFakeApplyAPI(), sampleManifest()
	applyManifest(context.Background(), api, m, false, "")
	m.Agents[0].AllowedScopes = []string{"mcp:tools:a"}
	m.Instances[0].Template = "other"
	res := applyManifest(context.Background(), api, m, false, "")
	for _, r := range res {
		switch r.Kind {
		case "agent":
			if r.Action != actFailed || !strings.Contains(r.Detail, "allowed_scopes") {
				t.Fatalf("agent: %+v", r)
			}
		case "instance":
			if r.Action != actFailed || !strings.Contains(r.Detail, "template") {
				t.Fatalf("instance: %+v", r)
			}
		}
	}
	// Scope order and duplicates are not differences.
	m.Agents[0].AllowedScopes = []string{"mcp:tools:a", "mcp:tools:b", "mcp:tools:a"}
	m.Instances[0].Template = "kb-search"
	if c := counts(applyManifest(context.Background(), api, m, false, "")); c[actFailed] != 0 {
		t.Fatalf("equivalent scopes reported as a difference: %+v", c)
	}
}

func TestApplyDryRunWritesNothingAndSkipsInstances(t *testing.T) {
	api, m := newFakeApplyAPI(), sampleManifest()
	res := applyManifest(context.Background(), api, m, true, "")
	if api.writes != 0 || counts(res)[actCreated] != 3 {
		t.Fatalf("dry run: writes=%d %+v", api.writes, res)
	}
	var out bytes.Buffer
	printApplyResults(&out, res, true)
	if !strings.Contains(out.String(), "would create") || !strings.Contains(out.String(), "Plan (dry run, nothing written)") {
		t.Fatalf("output: %s", out.String())
	}
	res = applyManifest(context.Background(), api, m, false, "no cluster")
	if c := counts(res); c[actSkipped] != 1 || c[actCreated] != 2 {
		t.Fatalf("skip: %+v", res)
	}
}

func TestSubsetEqualIgnoresServerAddedFields(t *testing.T) {
	desired := jsonValue(map[string]any{"a": 1, "b": []any{map[string]any{"x": "y", "z": ""}}, "c": false})
	actual := jsonValue(map[string]any{"a": 1, "b": []any{map[string]any{"x": "y", "added": 1}}, "extra": true})
	if !subsetEqual(desired, actual) {
		t.Fatal("server-added fields and zero values must not count")
	}
	if subsetEqual(jsonValue(map[string]any{"a": 2}), actual) || subsetEqual(jsonValue(map[string]any{"b": []any{}}), actual) {
		t.Fatal("real differences missed")
	}
}
