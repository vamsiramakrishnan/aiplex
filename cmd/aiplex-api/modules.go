package main

import (
	"context"
	"fmt"
	"github.com/go-chi/chi/v5"
	"github.com/vamsiramakrishnan/aiplex/internal/api"
	"github.com/vamsiramakrishnan/aiplex/pkg/modules"
	"net/http"
)

// httpModule contributes API routes and metadata through one registration. It
// owns no process resources; independently deployed modules use modules.Remote.
type httpModule struct {
	descriptor modules.Descriptor
	mount      func(chi.Router)
}

func (m *httpModule) Descriptor() modules.Descriptor { return m.descriptor }
func (m *httpModule) Start(context.Context) error    { return nil }
func (m *httpModule) Stop(context.Context) error     { return nil }
func (m *httpModule) Ready(context.Context) error    { return nil }

func builtinModules(llmH *api.LLMHandler, a2aH *api.A2AHandler, skillsH *api.SkillsHandler) map[string]*httpModule {
	makeModule := func(name, title, path, command string, mount func(chi.Router)) *httpModule {
		d := modules.Descriptor{Name: name, Version: "1.0.0", ContractVersion: modules.ContractVersion, Capabilities: []string{name + ".manage", "workload.route"}, Dependencies: []modules.Dependency{{Name: "identity", ContractVersion: modules.ContractVersion}}, Navigation: []modules.Navigation{{Path: "/" + name, Title: title}}}
		if path != "" {
			d.APIPrefixes = []string{"/api/v1" + path}
		}
		if command != "" {
			d.Commands = []modules.Command{{Name: command, Description: "Manage " + title}}
		}
		return &httpModule{d, mount}
	}
	return map[string]*httpModule{
		"mcplex": makeModule("mcplex", "MCPlex", "", "", func(chi.Router) {}),
		"llmplex": makeModule("llmplex", "LLMPlex", "/llm", "llm", func(r chi.Router) {
			r.Route("/llm", func(r chi.Router) {
				r.Get("/routes", llmH.ListRouteConfigs)
				r.Get("/routes/{modelId}", llmH.GetRouteConfig)
				r.Put("/routes/{modelId}", llmH.PutRouteConfig)
				r.Delete("/routes/{modelId}", llmH.DeleteRouteConfig)
				r.Get("/providers", llmH.ListProviders)
				r.Put("/providers/{provider}", llmH.PutProvider)
				r.Post("/usage", llmH.RecordUsage)
				r.Get("/usage", llmH.ListUsageRecords)
				r.Get("/usage/summary", llmH.GetUsageSummary)
			})
		}),
		"a2aplex": makeModule("a2aplex", "A2APlex", "/a2a", "a2a", func(r chi.Router) {
			r.Route("/a2a", func(r chi.Router) {
				r.Get("/agents", a2aH.ListAgentCards)
				r.Post("/delegations", a2aH.RecordDelegation)
				r.Get("/delegations", a2aH.ListDelegations)
				r.Get("/delegations/{id}", a2aH.GetDelegation)
				r.Patch("/delegations/{id}", a2aH.UpdateDelegation)
				r.Get("/delegations/{id}/chain", a2aH.GetDelegationChain)
			})
		}),
		"skillsplex": makeModule("skillsplex", "SkillsPlex", "/skills", "skills", func(r chi.Router) {
			r.Route("/skills", func(r chi.Router) {
				r.Get("/servers", skillsH.ListSkillServers)
				r.Post("/invocations", skillsH.RecordInvocation)
				r.Get("/invocations", skillsH.ListInvocations)
			})
		}),
	}
}

func selectModules(names []string, available map[string]*httpModule) ([]*httpModule, error) {
	selected := make([]*httpModule, 0, len(names))
	seen := map[string]bool{}
	for _, name := range names {
		m, ok := available[name]
		if !ok {
			return nil, fmt.Errorf("unknown enabled module %q", name)
		}
		if seen[name] {
			return nil, fmt.Errorf("duplicate enabled module %q", name)
		}
		seen[name] = true
		selected = append(selected, m)
	}
	return selected, nil
}

func moduleDiscovery(registry *modules.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { api.JSON(w, http.StatusOK, registry.Descriptors()) }
}

func moduleEnabled(ms []*httpModule, name string) bool {
	for _, m := range ms {
		if m.descriptor.Name == name {
			return true
		}
	}
	return false
}
