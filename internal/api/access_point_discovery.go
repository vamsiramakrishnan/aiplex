package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
)

// MountAccessPointEndpoints mounts the client-facing access-point URLs at the
// server root: /ap/{id}/… and the RFC 9728 well-known metadata.
func (h *ConnectedHandler) MountAccessPointEndpoints(r chi.Router) {
	r.Route("/ap/{apid}", func(r chi.Router) {
		r.Post("/v1/chat/completions", h.AccessPointModel(WireOpenAIChat))
		r.Post("/v1/responses", h.AccessPointModel(WireOpenAIResponses))
		r.Post("/v1/messages", h.AccessPointModel(WireAnthropic))
		r.Post("/v1beta/models/{target}", h.AccessPointModel(WireGenAI))
		r.Post("/model/{target}/converse", h.AccessPointModel(WireBedrockConverse))
		r.Post("/model/{target}/converse-stream", h.AccessPointModel(WireBedrockConverse))
		r.Post("/model/{target}/invoke", h.AccessPointModel(WireBedrockInvoke))
		r.Post("/model/{target}/invoke-with-response-stream", h.AccessPointModel(WireBedrockInvoke))
		r.Get("/v1/realtime", h.AccessPointModel(WireOpenAIRealtime))
		r.Get(geminiLivePath, h.AccessPointModel(WireGeminiLive))
		h.mountAccessPointMedia(r)
		r.Get("/v1/models", h.AccessPointListModels)
		r.Get("/v1beta/models", h.AccessPointListGenAIModels)
		h.mountAccessPointUtility(r)
		h.mountAccessPointAzure(r)
		r.HandleFunc("/mcp", h.AccessPointMCP)
		r.HandleFunc("/mcp/{connection}", h.AccessPointMCP)
		r.Get("/v0.1/servers", h.AccessPointServers)
		r.Get("/skills", h.AccessPointSkills)
		r.Get("/.well-known/oauth-protected-resource", h.AccessPointProtectedResource)
		r.Get("/.well-known/oauth-protected-resource/mcp", h.AccessPointProtectedResource)
		r.Get("/.well-known/oauth-protected-resource/mcp/{connection}", h.AccessPointProtectedResource)
	})
	r.Get("/.well-known/oauth-protected-resource/ap/{apid}/mcp", h.AccessPointProtectedResource)
	r.Get("/.well-known/oauth-protected-resource/ap/{apid}/mcp/{connection}", h.AccessPointProtectedResource)
}

// AccessPointProtectedResource serves RFC 9728 metadata for the MCP resources
// of an access point. It is public and does not reveal whether the access
// point exists; the authorization server is operator configuration.
func (h *ConnectedHandler) AccessPointProtectedResource(w http.ResponseWriter, r *http.Request) {
	apID, connection := chi.URLParam(r, "apid"), chi.URLParam(r, "connection")
	if !accessPointIDPattern.MatchString(apID) || (connection != "" && !accessPointConnectionPattern.MatchString(connection)) {
		http.NotFound(w, r)
		return
	}
	if h.PublicURL == "" || len(h.AccessPoint.AuthorizationServers) == 0 {
		Error(w, r, http.StatusNotFound, "NOT_CONFIGURED", "protected resource metadata requires a public URL and an authorization server")
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=300")
	JSON(w, http.StatusOK, map[string]any{
		"resource":                 h.publicBase() + accessPointResourcePath(apID, connection),
		"authorization_servers":    h.AccessPoint.AuthorizationServers,
		"bearer_methods_supported": []string{"header"},
		"resource_name":            "AIPlex access point " + apID,
	})
}

// accessPointServer extends a catalog template with the official MCP
// registry "remotes" member so catalog clients can connect.
type accessPointServer struct {
	models.Template
	Remotes []accessPointRemote `json:"remotes"`
}

type accessPointRemote struct {
	Type string `json:"type"`
	URL  string `json:"url"`
}

// AccessPointServers lists the access point's MCP endpoints in the catalog
// page shape served by /v0.1/servers.
func (h *ConnectedHandler) AccessPointServers(w http.ResponseWriter, r *http.Request) {
	_, ap, _, ok := h.accessPointGate(w, r, func(w http.ResponseWriter, r *http.Request, status int, code, message string) {
		Error(w, r, status, code, message)
	})
	if !ok {
		return
	}
	servers := []accessPointServer{}
	if h.moduleEnabled("mcplex") && len(ap.Tools) > 0 {
		base := h.publicBase()
		name := ap.DisplayName
		if name == "" {
			name = ap.ID
		}
		servers = append(servers, accessPointServer{Template: models.Template{ID: "ap-" + ap.ID, Source: "access-point", Plane: models.PlaneMCPlex, Name: name, Description: "Every tool exposed by access point " + ap.ID + "; tool names are {connection}__{tool}", Category: "access-point", Version: "1.0.0", Verified: true}, Remotes: []accessPointRemote{{Type: "streamable-http", URL: base + accessPointResourcePath(ap.ID, "")}}})
		for _, tool := range ap.Tools {
			servers = append(servers, accessPointServer{Template: models.Template{ID: "ap-" + ap.ID + "-" + tool.ConnectionRef, Source: "access-point", Plane: models.PlaneMCPlex, Name: tool.ConnectionRef, Description: "Tool connection " + tool.ConnectionRef + " through access point " + ap.ID, Category: "access-point", Version: "1.0.0", Verified: true}, Remotes: []accessPointRemote{{Type: "streamable-http", URL: base + accessPointResourcePath(ap.ID, tool.ConnectionRef)}}})
		}
	}
	entries := h.registryEntries(servers)
	JSON(w, http.StatusOK, map[string]any{
		"servers": entries, "metadata": map[string]any{"count": len(entries)},
		"templates": servers, "total": len(servers), "page": 0, "page_size": len(servers),
	})
}

// AccessPointSkills returns the manifests of the skill servers an access
// point exposes, reduced to the skills the caller may invoke.
func (h *ConnectedHandler) AccessPointSkills(w http.ResponseWriter, r *http.Request) {
	tenant, ap, _, ok := h.accessPointGate(w, r, func(w http.ResponseWriter, r *http.Request, status int, code, message string) {
		Error(w, r, status, code, message)
	})
	if !ok {
		return
	}
	if !h.moduleEnabled("skillsplex") {
		Error(w, r, http.StatusNotFound, "MODULE_DISABLED", "skill endpoints are disabled")
		return
	}
	access := GetWIFAccess(r.Context())
	var scopes []string
	if access != nil {
		scopes = access.Scopes
	}
	manifests := []map[string]any{}
	unavailable := []string{}
	for _, entry := range ap.Skills {
		instance, err := h.accessPointSkillInstance(r.Context(), tenant, entry.InstanceRef)
		if err != nil || instance.Status != models.StatusRunning {
			unavailable = append(unavailable, entry.InstanceRef)
			continue
		}
		template, err := h.AccessPoint.Skills.GetTemplate(r.Context(), instance.TemplateID)
		if err != nil || template == nil || template.Plane != models.PlaneSkillsPlex {
			unavailable = append(unavailable, entry.InstanceRef)
			continue
		}
		visible := []models.SkillInfo{}
		for _, skill := range deployedSkills(instance, template) {
			if scopeAllowed(scopes, "skill:invoke:"+skill.Name) {
				visible = append(visible, skill)
			}
		}
		manifests = append(manifests, skillsManifest(instance, template, visible))
	}
	JSON(w, http.StatusOK, map[string]any{"access_point_id": ap.ID, "servers": manifests, "unavailable": unavailable})
}
