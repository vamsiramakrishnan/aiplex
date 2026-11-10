package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"

	"github.com/vamsiramakrishnan/aiplex/internal/api"
	"github.com/vamsiramakrishnan/aiplex/internal/auth"
	"github.com/vamsiramakrishnan/aiplex/internal/catalog"
	"github.com/vamsiramakrishnan/aiplex/internal/config"
	"github.com/vamsiramakrishnan/aiplex/internal/deploy"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
	"github.com/vamsiramakrishnan/aiplex/internal/secrets"
	"github.com/vamsiramakrishnan/aiplex/pkg/modules"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
)

// serverOptions are test seams. Production leaves every field zero and gets
// exactly the wiring main has always used.
type serverOptions struct {
	// store replaces the registry store chosen from configuration.
	store registry.Store
	// identity replaces the identity validator built from configuration.
	identity *auth.WIFValidator
	// officialRegistryURL replaces the public MCP registry origin.
	officialRegistryURL string
	// metrics serves GET /metrics (main passes the telemetry exporter's
	// handler; nil serves an empty exposition).
	metrics http.Handler
}

// server is the fully wired HTTP API. close releases resources acquired while
// building it, in reverse order.
type server struct {
	handler http.Handler
	store   registry.Store
	closers []func()
}

func (s *server) close() {
	for i := len(s.closers) - 1; i >= 0; i-- {
		s.closers[i]()
	}
	s.closers = nil
}

// newServer builds the router exactly as the process serves it: the same
// middleware order, module selection and route mounts. Tests construct it to
// exercise authorization end to end.
func newServer(ctx context.Context, cfg *config.Config, opts serverOptions) (srv *server, err error) {
	srv = &server{}
	defer func() {
		if err != nil {
			srv.close()
			srv = nil
		}
	}()

	// Store — use Firestore when configured, otherwise in-memory
	var store registry.Store
	var resourceStore resources.Store = resources.NewMemoryStore()
	projectID, databaseID := firestoreConfiguration(cfg)

	switch {
	case opts.store != nil:
		store = opts.store
		if withResources, ok := opts.store.(interface{ ModuleResources() resources.Store }); ok {
			resourceStore = withResources.ModuleResources()
		}
	case projectID != "":
		fs, err := registry.NewFirestoreStore(projectID, databaseID)
		if err != nil {
			return nil, fmt.Errorf("firestore init failed: %w", err)
		}
		srv.closers = append(srv.closers, func() { fs.Close() })
		store = fs
		resourceStore = fs.ModuleResources()
		log.Info().Str("project", projectID).Str("database", databaseID).Msg("using Firestore store")
	default:
		store = registry.NewMemoryStore()
		log.Warn().Msg("using in-memory store — data will not persist across restarts")
	}
	srv.store = store

	// Seed built-in LLM provider + skill templates
	providers := catalog.NewBuiltInProviders()
	llmTemplates, _ := providers.Fetch(ctx)
	for i := range llmTemplates {
		store.PutTemplate(ctx, &llmTemplates[i])
	}
	builtinSkills := catalog.NewBuiltInSkills()
	skillTemplates, _ := builtinSkills.Fetch(ctx)
	for i := range skillTemplates {
		store.PutTemplate(ctx, &skillTemplates[i])
	}

	// Catalog aggregator
	sources := []catalog.Source{
		catalog.NewOfficialMCPSource(opts.officialRegistryURL), // Official MCP registry
		catalog.NewLocalSource(store, models.PlaneMCPlex),
		catalog.NewLocalSource(store, models.PlaneA2APlex),
		catalog.NewLocalSource(store, models.PlaneLLMPlex),
		catalog.NewLocalSource(store, models.PlaneSkillsPlex),
		providers,
		builtinSkills,
	}
	aggregator := catalog.NewAggregator(sources)

	// Deploy engine — use live K8s client in production, no-op in dev
	var k8sClient deploy.K8sClient
	if os.Getenv("KUBERNETES_SERVICE_HOST") != "" {
		k8sClient = deploy.NewLiveK8sClientConfigured()
		log.Info().Msg("using live K8s client (in-cluster)")
	} else {
		k8sClient = deploy.NewNoOpK8sClient()
		log.Info().Msg("using no-op K8s client (dev mode)")
	}
	engine := deploy.NewEngineWithK8s(store, k8sClient, cfg.TrustDomain, cfg.GatewayName)

	// Auth (Ory Hydra)
	hydraClient := auth.NewHydraClient(cfg.HydraAdminURL)

	wifValidator := opts.identity
	if wifValidator == nil {
		if wifValidator, err = identityValidator(store, cfg); err != nil {
			return nil, fmt.Errorf("identity configuration failed: %w", err)
		}
	}

	if err := bootstrapRoleBindings(ctx, store, cfg.RoleBindingsFile); err != nil {
		return nil, fmt.Errorf("operator role binding configuration failed: %w", err)
	}

	// Secret Manager (optional — nil in local dev)
	sm, err := secrets.NewManager(context.Background(), projectID)
	if err != nil {
		log.Warn().Err(err).Msg("secret manager init failed — secret validation disabled")
	}
	if sm != nil {
		srv.closers = append(srv.closers, func() { sm.Close() })
	}

	// Handlers
	catalogH := api.NewCatalogHandler(aggregator, store)
	instanceH := api.NewInstanceHandler(store, engine)
	// Development mode without an explicitly configured Hydra keeps agent
	// registrations in the local registry only (no OAuth client is issued);
	// otherwise every registration fails against the default in-cluster URL.
	agentHydra := hydraClient
	if cfg.DevelopmentMode && os.Getenv("HYDRA_ADMIN_URL") == "" {
		agentHydra = nil
		log.Info().Msg("development mode without HYDRA_ADMIN_URL: agent registrations are local only")
	}
	agentH := api.NewAgentHandler(store, agentHydra)
	authH := api.NewAuthHandler(hydraClient, store)
	llmH := api.NewLLMHandler(store, k8sClient, cfg.GatewayName, sm)
	a2aH := api.NewA2AHandler(store)
	skillsH := api.NewSkillsHandler(store)
	dashH := api.NewDashboardHandler(store)
	iamH := api.NewIAMHandler(store, wifValidator)
	moduleRegistry := modules.NewRegistry()
	if err := moduleRegistry.Register(&httpModule{descriptor: modules.Descriptor{Name: "identity", Version: "1.0.0", ContractVersion: modules.ContractVersion, Capabilities: []string{"identity.resolve", "identity.governance"}, APIPrefixes: []string{"/api/v1/iam/native"}, Commands: []modules.Command{{Name: "native", Description: "Govern native agent identities and registries"}}}}); err != nil {
		return nil, fmt.Errorf("register identity: %w", err)
	}
	connectedH, clientH, err := connectedHandlers(resourceStore, sm, cfg)
	if err != nil {
		return nil, fmt.Errorf("connected module configuration failed: %w", err)
	}
	connectedH.ResolveAccess = wifValidator.SyncUserScopes
	connectedH.AccessPoint = accessPointOptions(cfg, store)
	federationH, err := agentFederation(cfg, store, resourceStore, connectedH.Runtime.Config.Network, connectedH.Tenant, wifValidator)
	if err != nil {
		return nil, fmt.Errorf("agent identity federation configuration failed: %w", err)
	}
	userDirectoriesR, secretName, err := userDirectories(connectedH.Runtime, sm, cfg.DevelopmentMode)
	if err != nil {
		return nil, fmt.Errorf("user identity directory configuration failed: %w", err)
	}
	userFederationH, err := userFederation(cfg, resourceStore, connectedH.Runtime.Config.Network, connectedH.Tenant, wifValidator, userDirectoriesR)
	if err != nil {
		return nil, fmt.Errorf("user identity federation configuration failed: %w", err)
	}
	userFederationH.SecretName = secretName
	// Write-time and dry-run overlap detection between the two federation kinds.
	federationH.Providers = userFederationH.Providers
	userFederationH.Bindings = federationH.Bindings
	usageH := configureUsageLedger(store, cfg, connectedH, llmH)
	dashH.Ledger, dashH.Tenant = usageH.Ledger, connectedH.Tenant
	quotaRuntime, err := configureQuota(ctx, cfg, store, connectedH)
	if err != nil {
		return nil, fmt.Errorf("quota module configuration failed: %w", err)
	}
	availableModules := builtinModules(llmH, a2aH, skillsH)
	extendConnectedModules(availableModules, connectedH, clientH)
	if quotaRuntime != nil {
		availableModules["quotaplex"] = &httpModule{descriptor: quotaRuntime.Descriptor(), mount: func(r chi.Router) { quotaRuntime.mount(r, connectedH) }, lifecycle: quotaRuntime}
	}
	enabledModules, err := selectModules(cfg.EnabledModules, availableModules)
	if err != nil {
		return nil, fmt.Errorf("invalid module configuration: %w", err)
	}
	enabledPlanes := make([]models.Plane, 0, len(enabledModules))
	for _, m := range enabledModules {
		if err := moduleRegistry.Register(m); err != nil {
			return nil, fmt.Errorf("register module: %w", err)
		}
		if usesWorkloadRoutes(m) {
			enabledPlanes = append(enabledPlanes, models.Plane(m.descriptor.Name))
		}
	}
	routes, err := deploy.NewRouteRegistryForPlanes(enabledPlanes...)
	if err != nil {
		return nil, fmt.Errorf("configure module routes: %w", err)
	}
	engine.WithRouteRegistry(routes)
	catalogH.WithEnabledPlanes(enabledPlanes...)
	if err := registerExternalModules(moduleRegistry, cfg.ModuleManifest, cfg.DevelopmentMode); err != nil {
		return nil, fmt.Errorf("external module configuration failed: %w", err)
	}
	runtime := modules.NewRuntime(moduleRegistry)
	startCtx, startCancel := context.WithTimeout(ctx, 15*time.Second)
	err = runtime.Start(startCtx)
	startCancel()
	if err != nil {
		return nil, fmt.Errorf("module startup failed: %w", err)
	}
	srv.closers = append(srv.closers, func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := runtime.Stop(stopCtx); err != nil {
			log.Error().Err(err).Msg("module shutdown failed")
		}
	})
	sseH := api.NewSSEHandler(store)
	runsH := api.NewRunsHandler(store)
	// PR 11 item 3: real Tape admin client when TAPE_URL is set.
	// Falls back to NoopTapeAdmin (the default) in dev paths where
	// no tape-server is reachable, so the API still starts cleanly.
	if tapeAdmin, ok := api.NewGRPCTapeAdmin(); ok {
		runsH = runsH.WithTapeAdmin(tapeAdmin)
		srv.closers = append(srv.closers, func() { tapeAdmin.Close() })
		log.Info().Str("tape_url", os.Getenv("TAPE_URL")).
			Msg("Tape admin client wired (operator actions live)")
		// PR 13: retention reactor. Ticks every TAPE_RETENTION_INTERVAL
		// (default 15m). Compacts settled runs older than each
		// instance's runtime.retention.compact_after_days; surfaces
		// purge-eligible runs to the projection. Off by default —
		// AIPLEX_RETENTION_ENABLED=1 turns it on (so existing dev
		// stacks don't start compacting unexpectedly).
		if os.Getenv("AIPLEX_RETENTION_ENABLED") == "1" {
			interval := parseDurationEnv("TAPE_RETENTION_INTERVAL", 15*time.Minute)
			retention := api.NewRetentionReactor(store, tapeAdmin, interval)
			retention.Start(ctx)
			srv.closers = append(srv.closers, func() { retention.Close() })
			log.Info().Dur("interval", interval).Msg("retention reactor started")
		}
	} else {
		log.Warn().Msg("TAPE_URL not set — operator actions are no-ops (NoopTapeAdmin)")
	}

	// Router
	r := chi.NewRouter()
	r.Use(api.Recover)
	r.Use(api.RequestID)
	r.Use(api.Tracing) // SERVER span; continues a valid inbound traceparent
	r.Use(api.Logger)
	corsOrigins := cfg.AllowedOrigins
	switch {
	case len(corsOrigins) == 0 && cfg.DevelopmentMode:
		log.Warn().Msg("CONSOLE_ORIGINS unset — allowing all origins (development mode)")
		corsOrigins = []string{"*"}
	case len(corsOrigins) == 0:
		log.Warn().Msg("CONSOLE_ORIGINS unset — cross-origin browser access is disabled")
	default:
		log.Info().Strs("origins", corsOrigins).Msg("CORS restricted to configured origins")
	}
	r.Use(api.CORS(corsOrigins...))
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			limit := int64(1 << 20)
			if nativeInvocation(r) {
				limit = 8 << 20
			}
			api.MaxBody(limit)(next).ServeHTTP(w, r)
		})
	}) // 1MB max request body
	r.Use(api.NativeKeyHeaders)                               // provider-native key headers -> bearer, model routes only
	r.Use(api.WIFAuthMode(wifValidator, cfg.DevelopmentMode)) // Extract WIF identity + sync Dimension B
	// Development mode: refuse unauthenticated requests that came through a
	// proxy (the loopback-only API has been exposed).
	r.Use(api.RefuseProxiedAnonymousInDevelopment(cfg.DevelopmentMode))
	api.SetTrustedProxyHops(cfg.TrustedProxyHops)
	r.Use(api.AuditLog) // Log all mutations
	// 200 req/min per verified caller, else per peer IP (in-process). A
	// development API serves one local developer whose SDK loops, demos and
	// tests all come from one identity and one IP, so its default is higher.
	rateLimit := 200
	if cfg.DevelopmentMode {
		rateLimit = 1200
	}
	r.Use(api.RateLimit(rateLimit))
	r.Use(api.Compress) // Response compression hints
	// Route authorization by mounted pattern (route_policy.go). It needs the
	// verified identity, so it runs after WIFAuthMode.
	r.Use(api.RoutePolicy(r, routePolicy(cfg)))

	// Health (readyz checks store connectivity)
	healthH := api.NewHealthHandler(store)
	r.Get("/healthz", healthH.Liveness)
	r.Get("/readyz", func(w http.ResponseWriter, r *http.Request) {
		probe, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if err := runtime.Ready(probe); err != nil {
			api.JSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable", "error": "module unavailable"})
			return
		}
		healthH.Readiness(w, r.WithContext(probe))
	})
	// Prometheus text exposition for an in-cluster scraper: a shared-secret
	// internal route (route_policy.go) that the gateway does not publish.
	metrics := opts.metrics
	if metrics == nil {
		metrics = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		})
	}
	r.With(api.MetricsAuth(cfg.DevelopmentMode)).Get("/metrics", metrics.ServeHTTP)

	// API v1
	r.Route("/api/v1", func(r chi.Router) {
		// Role and tenant-scope checks for every route are in RoutePolicy.
		r.Use(api.RequireIdentity(cfg.DevelopmentMode))
		// Catalog
		r.Get("/catalog", catalogH.List)
		r.Get("/catalog/{id}", catalogH.Get)

		// Instances
		r.Get("/instances", instanceH.List)
		r.Post("/instances", instanceH.Deploy)
		r.Get("/instances/{id}", instanceH.Get)
		r.Delete("/instances/{id}", instanceH.Undeploy)
		r.Get("/instances/{id}/history", instanceH.History)

		// Agents
		r.Get("/agents", agentH.List)
		r.Post("/agents", agentH.Register)
		r.Get("/agents/{clientId}", agentH.Get)
		r.Delete("/agents/{clientId}", agentH.Delete)
		r.Get("/agents/{clientId}/permissions", agentH.GetPermissions)
		federationH.Mount(r)     // cross-cloud agent identity presets, bindings, plans, verify
		userFederationH.Mount(r) // workforce user identity providers, plans, verify

		for _, module := range enabledModules {
			module.mount(r)
		}
		r.Get("/modules", moduleDiscovery(moduleRegistry))
		mountUsageLedger(r, usageH, connectedH)
		mountHookPolicies(r, connectedH) // /api/v1/hooks/policies
		connectedH.MountAccessPoints(r) // /api/v1/access-points
		// Single-pane overview: /api/v1/overview
		api.NewOverviewHandler(connectedH, usageH.Ledger, store, federationH.Bindings).Mount(r)

		// Dashboard — unified observability
		r.Route("/dashboard", func(r chi.Router) {
			r.Get("/stats", dashH.GetStats)
			r.Get("/denials", dashH.ListPolicyDenials)
			r.Post("/denials", dashH.RecordPolicyDenial)
		})

		// Runs — AIPlex ↔ Tape run timeline (AIPlex integration PR 7).
		// Read-only projection of Tape's journal, indexed by tenant /
		// agent / actor / subject for the Console. Requires
		// aiplex:runs:read at the gateway authz layer; operator
		// actions (redrive / reconcile / cancel / signal / compensate)
		// arrive in PR 10 under aiplex:runs:* scopes.
		// Multi-tenant enforcement (PR 11 item 6): require a tenant
		// claim or aiplex:runs:read:cross_tenant before any /runs route.
		// Opt-in via AIPLEX_REQUIRE_TENANT=1 so existing dev paths
		// don't break overnight.
		// Reads need aiplex:runs:read and each action aiplex:runs:{action},
		// enforced here as well as at the gateway; a run outside the caller's
		// tenant claim answers as missing (RunVisible).
		r.Route("/runs", func(r chi.Router) {
			r.Use(api.RequireTenant)
			read := api.RequireRunsScope("read")
			r.With(read).Get("/", runsH.List)
			r.With(read).Get("/_health", runsH.Health)
			r.Group(func(r chi.Router) {
				r.Use(read, runsH.RunVisible)
				r.Get("/{run_id}", runsH.Get)
				r.Get("/{run_id}/events", runsH.Events)
				r.Get("/{run_id}/effects", runsH.Effects)
				r.Get("/{run_id}/obligations", runsH.Obligations)
				r.Get("/{run_id}/budgets", runsH.Budgets)
				r.Get("/{run_id}/operator-audit", runsH.ListOperatorAudit)
			})
			// Operator actions (PR 10 + PR 11 item 7 idempotency wrap). The
			// Idempotency middleware dedupes double-clicks for 5 minutes; it
			// runs after the grant and visibility checks so a cached reply is
			// never served to a caller who may not act on the run.
			idempotent := api.Idempotency(runsActionFromPath)
			for action, handler := range map[string]http.HandlerFunc{"redrive": runsH.Redrive, "reconcile": runsH.Reconcile, "cancel": runsH.Cancel, "signal": runsH.Signal, "compensate": runsH.Compensate, "compact": runsH.Compact} {
				r.With(api.RequireRunsScope(action), runsH.RunVisible, idempotent).Post("/{run_id}/"+action, handler)
			}
		})

		// IAM — role bindings, WIF identity resolution
		r.Route("/iam", func(r chi.Router) {
			connectedH.MountNativeGovernance(r)
			r.Get("/roles", iamH.ListRoles)
			r.Get("/whoami", iamH.ResolveIdentity)
			r.Post("/validate-principal", iamH.ValidateWIFPrincipal)
			r.Get("/role-bindings", iamH.ListRoleBindings)
			r.Post("/role-bindings", iamH.CreateRoleBinding)
			r.Get("/role-bindings/{id}", iamH.GetRoleBinding)
			r.Put("/role-bindings/{id}", iamH.UpdateRoleBinding)
			r.Delete("/role-bindings/{id}", iamH.DeleteRoleBinding)
		})
	})

	// Auth webhooks (Hydra consent + token hook)
	r.Route("/auth", func(r chi.Router) {
		r.With(api.RequireIdentity(cfg.DevelopmentMode)).Get("/consent", authH.ConsentGet)
		r.With(api.RequireIdentity(cfg.DevelopmentMode)).Post("/consent", authH.ConsentAccept)
		r.With(api.BearerToken("AUTH_HOOK_TOKEN")).Post("/token-hook", authH.TokenHook)
		r.Get("/login", authH.LoginRedirect)
		r.With(api.RequireIdentity(cfg.DevelopmentMode), api.RequireRole(models.RoleAdmin)).Get("/users/{userId}/scopes", authH.GetUserScopes)
		r.With(api.RequireIdentity(cfg.DevelopmentMode), api.RequireRole(models.RoleAdmin)).Put("/users/{userId}/scopes", authH.SetUserScopes)
	})

	// Server-Sent Events for live dashboard
	// Per-run mode (?run_id=) streams a run's journal: the same grant and
	// tenant visibility as GET /api/v1/runs/{run_id}/events.
	runStream := api.RequireRunsScope("read")(runsH.RunVisible(http.HandlerFunc(sseH.Stream)))
	r.With(api.RequireIdentity(cfg.DevelopmentMode)).Get("/events/stream", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Has("run_id") {
			runStream.ServeHTTP(w, r)
			return
		}
		sseH.Stream(w, r)
	})

	// AIPlex ↔ Tape audit ingestion. Tape's outbox relay POSTs
	// here; AIPlex dedupes on (run_id, seq), quarantines unknown
	// agents, and updates the ExecutionRun projection. See
	// internal/api/runs.go and docs/guides/tape-runtime.md.
	//
	// PR 11 layers: bearer token (TAPE_INGEST_TOKEN) + per-IP rate
	// limit (TAPE_INGEST_RPS, default 1000). Defence-in-depth so a
	// misbehaving outbox can't flood projection storage and an
	// unauthorized writer can't poison the audit trail.
	r.Group(func(r chi.Router) {
		r.Use(api.BearerToken("TAPE_INGEST_TOKEN"))
		r.Use(api.IPRateLimit(parseFloatEnv("TAPE_INGEST_RPS", 1000), parseIntEnv("TAPE_INGEST_BURST", 1000)))
		r.Post("/internal/tape/events", runsH.Ingest)
		r.Post("/internal/projections/rebuild/{run_id}", runsH.RebuildProjection)
	})

	// MCP sub-registry (v0.1 spec)
	if moduleEnabled(enabledModules, "mcplex") {
		r.With(api.RequireIdentity(cfg.DevelopmentMode)).Get("/v0.1/servers", catalogH.List)
	}

	// Access points: /ap/{id}/… model, MCP, catalog and skill endpoints.
	connectedH.MountAccessPointEndpoints(r)

	// A2A Agent Card discovery (per-instance)
	if moduleEnabled(enabledModules, "a2aplex") {
		r.With(api.RequireIdentity(cfg.DevelopmentMode)).Get("/a2a/{instanceId}/.well-known/agent.json", a2aH.GetAgentCard)
	}

	// SkillsPlex skills manifest (per-instance)
	if moduleEnabled(enabledModules, "skillsplex") {
		r.With(api.RequireIdentity(cfg.DevelopmentMode)).Get("/skills/{instanceId}/.well-known/skills.json", skillsH.GetSkillsManifest)
	}

	if moduleEnabled(enabledModules, "mcplex") && connectedH.Tools != nil && cfg.PublicURL != "" {
		metadata, e := connectedH.Tools.MetadataHandler(cfg.PublicURL)
		if e != nil {
			return nil, fmt.Errorf("CIMD metadata configuration invalid: %w", e)
		}
		r.Handle("/mcp/client-metadata/*", metadata)
	}

	srv.handler = r
	return srv, nil
}
