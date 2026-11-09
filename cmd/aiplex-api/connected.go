package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/vamsiramakrishnan/aiplex/internal/api"
	"github.com/vamsiramakrishnan/aiplex/internal/config"
	"github.com/vamsiramakrishnan/aiplex/internal/connections"
	"github.com/vamsiramakrishnan/aiplex/internal/secrets"
	"github.com/vamsiramakrishnan/aiplex/pkg/modules"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
	"github.com/vamsiramakrishnan/aiplex/pkg/toolauth"
)

func loadConnectedConfig(path string, development bool) (connections.Config, error) {
	var cfg connections.Config
	if path == "" {
		return cfg, nil
	}
	f, e := os.Open(path)
	if e != nil {
		return cfg, e
	}
	defer f.Close()
	data, e := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if e != nil {
		return cfg, e
	}
	if len(data) > 1<<20 {
		return cfg, fmt.Errorf("connection config exceeds 1MiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if e = decoder.Decode(&cfg); e != nil {
		return cfg, e
	}
	if e = decoder.Decode(new(any)); e != io.EOF {
		return cfg, fmt.Errorf("trailing connected config")
	}
	if cfg.Network.AllowLoopback && !development {
		return cfg, fmt.Errorf("loopback upstreams require development mode")
	}
	return cfg, nil
}
func connectedHandlers(store resources.Store, sm *secrets.Manager, cfg *config.Config) (*api.ConnectedHandler, *api.ClientHandler, error) {
	if cfg.PublicURL != "" {
		public, e := url.Parse(cfg.PublicURL)
		if e != nil || public.User != nil || public.RawQuery != "" || public.Fragment != "" || public.Host == "" || (public.Path != "" && public.Path != "/") || (!cfg.DevelopmentMode && public.Scheme != "https") {
			return nil, nil, fmt.Errorf("public URL must be a trusted HTTPS origin")
		}
	}
	providerConfig, e := loadConnectedConfig(cfg.ConnectionsFile, cfg.DevelopmentMode)
	if e != nil {
		return nil, nil, e
	}
	runtime, e := connections.New(providerConfig, sm)
	if e != nil {
		return nil, nil, e
	}
	tenant := func(r *http.Request) (string, error) {
		access := api.GetWIFAccess(r.Context())
		tenants := map[string]bool{}
		if access != nil {
			for _, scope := range access.Scopes {
				if strings.HasPrefix(scope, "aiplex:tenant:") {
					id := strings.TrimPrefix(scope, "aiplex:tenant:")
					if id != "" {
						tenants[id] = true
					}
				}
			}
		}
		if len(tenants) > 1 {
			return "", fmt.Errorf("ambiguous tenant grants require explicit trusted tenancy binding")
		}
		for id := range tenants {
			return id, nil
		}
		if cfg.TenantID != "" {
			return cfg.TenantID, nil
		}
		if cfg.DevelopmentMode {
			return "development", nil
		}
		return "", fmt.Errorf("trusted tenant binding required")
	}
	var tools *toolauth.Service
	if len(providerConfig.Network.AllowedOrigins) > 0 {
		var credentials toolauth.CredentialStore = connections.SecretCredentials{Manager: sm}
		if cfg.DevelopmentMode && sm == nil {
			credentials = toolauth.NewMemoryCredentials()
		}
		tools, e = toolauth.NewService(toolauth.Config{Network: providerConfig.Network, Credentials: credentials, PreRegistered: providerConfig.PreRegistered, MetadataDocuments: providerConfig.MetadataDocuments})
		if e != nil {
			return nil, nil, e
		}
	}
	connected := api.NewConnectedHandler(store, runtime, tenant, tools, cfg.DevelopmentMode)
	connected.PublicURL = cfg.PublicURL
	connected.WithEnabledModules(cfg.EnabledModules)
	if e := connected.ConfigureDeploymentProviders(); e != nil {
		return nil, nil, e
	}
	if e := connected.ConfigureNativeGovernance(); e != nil {
		return nil, nil, e
	}
	return connected, api.NewClientHandler(store, tenant, connected), nil
}
func extendConnectedModules(available map[string]*httpModule, h *api.ConnectedHandler, clients *api.ClientHandler) {
	// Contributions remain attached to their owning Plex; invocation does not
	// acquire control-plane administrative authority.
	llm := available["llmplex"].mount
	available["llmplex"].mount = func(r chi.Router) {
		llm(r)
		r.Route("/llm/surfaces", func(r chi.Router) { resourceRoutes(r, h, "model-surface") })
		r.Get("/llm/operations", h.ModelOperations)
		r.Handle("/llm/native/{id}/*", http.HandlerFunc(h.NativeModel))
		r.Handle("/llm/gateway/{id}/*", http.HandlerFunc(h.ClientGateway))
	}
	a2a := available["a2aplex"].mount
	available["a2aplex"].mount = func(r chi.Router) {
		a2a(r)
		r.Route("/a2a/connections", func(r chi.Router) { resourceRoutes(r, h, "agent-connection"); r.Post("/{id}/invoke", h.InvokeAgent) })
	}
	available["mcplex"].descriptor.APIPrefixes = []string{"/api/v1/mcp"}
	available["mcplex"].mount = func(r chi.Router) {
		r.Route("/mcp/connections", func(r chi.Router) {
			resourceRoutes(r, h, "tool-connection")
			r.Post("/{id}/registration", h.RegisterTool)
			r.Post("/{id}/invoke", h.NativeTool)
		})
	}
	available["clientplex"] = &httpModule{descriptor: modules.Descriptor{Name: "clientplex", Version: "1.0.0", ContractVersion: "v1", Capabilities: []string{"clientplex.manage"}, Dependencies: []modules.Dependency{{Name: "identity", ContractVersion: "v1"}}, APIPrefixes: []string{"/api/v1/clients"}, Navigation: []modules.Navigation{{Path: "/clientplex", Title: "ClientPlex"}}, Commands: []modules.Command{{Name: "clients", Description: "Register and configure developer clients"}}}, mount: func(r chi.Router) {
		r.Route("/clients", func(r chi.Router) {
			r.Get("/profiles", clients.Profiles)
			r.Get("/registrations", clients.List)
			r.Get("/registrations/{id}", clients.Get)
			r.Put("/registrations/{id}", clients.Put)
			r.Delete("/registrations/{id}", clients.Delete)
			r.Post("/registrations/{id}/plan", clients.Plan)
			r.Route("/destinations", func(r chi.Router) { resourceRoutes(r, h, "client-destination") })
		})
	}}
	available["networking"] = &httpModule{descriptor: modules.Descriptor{Name: "networking", Version: "1.0.0", ContractVersion: "v1", Capabilities: []string{"networking.plan"}, Dependencies: []modules.Dependency{{Name: "identity", ContractVersion: "v1"}}, APIPrefixes: []string{"/api/v1/network"}}, mount: func(r chi.Router) {
		r.Get("/network/deployment-providers/{provider}/bindings", h.DeploymentBindings)
		r.Post("/network/deployment-providers/{provider}/bindings/{binding}/plan", h.PrepareDeployment)
		r.Route("/network/deployment-intents", func(r chi.Router) {
			resourceRoutes(r, h, "deployment-intent")
			r.Post("/{id}/plan", h.PlanDeploymentIntent)
		})
		r.Route("/network/plans", func(r chi.Router) {
			r.Get("/", h.Resources("deployment-plan"))
			r.Get("/{id}", h.Resources("deployment-plan"))
		})
		r.Route("/network/profiles", func(r chi.Router) { resourceRoutes(r, h, "network-profile"); r.Post("/{id}/plan", h.NetworkPlan) })
	}}
}
func resourceRoutes(r chi.Router, h *api.ConnectedHandler, kind string) {
	handler := h.Resources(kind)
	r.Get("/", handler)
	r.Get("/{id}", handler)
	r.Put("/{id}", handler)
	r.Delete("/{id}", handler)
}
func usesWorkloadRoutes(m *httpModule) bool {
	for _, c := range m.descriptor.Capabilities {
		if c == "workload.route" {
			return true
		}
	}
	return false
}
func serverTLS(cfg *config.Config) (*tls.Config, error) {
	if cfg.TLSCertFile == "" && cfg.TLSKeyFile == "" {
		if cfg.ClientCAFile != "" {
			return nil, fmt.Errorf("client CA requires TLS listener")
		}
		return nil, nil
	}
	if cfg.TLSCertFile == "" || cfg.TLSKeyFile == "" {
		return nil, fmt.Errorf("both TLS cert and key required")
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if cfg.ClientCAFile != "" {
		pem, e := os.ReadFile(cfg.ClientCAFile)
		if e != nil {
			return nil, e
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("client CA file contains no certificates")
		}
		tlsConfig.ClientCAs = pool
		tlsConfig.ClientAuth = tls.VerifyClientCertIfGiven
	}
	return tlsConfig, nil
}

// NativeInvocation bypasses only the administrative POST role test. The native
// handler must enforce its own per-resource action grant before credentials.
func nativeInvocation(r *http.Request) bool {
	if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/v1/mcp/connections/") && strings.HasSuffix(r.URL.Path, "/invoke") {
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/mcp/connections/"), "/invoke")
		return id != "" && !strings.ContainsAny(id, "/%\\")
	}
	if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/v1/iam/native/bindings/") && strings.HasSuffix(r.URL.Path, "/read") {
		return true
	}
	return (strings.HasPrefix(r.URL.Path, "/api/v1/llm/native/") || strings.HasPrefix(r.URL.Path, "/api/v1/llm/gateway/")) || (strings.HasPrefix(r.URL.Path, "/api/v1/a2a/connections/") && strings.HasSuffix(r.URL.Path, "/invoke"))
}
