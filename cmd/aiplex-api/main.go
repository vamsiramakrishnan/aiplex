package main

import (
	"context"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"
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

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Config
	cfg, err := config.Load(ctx)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load config")
	}

	// Logging
	level, _ := zerolog.ParseLevel(cfg.LogLevel)
	zerolog.SetGlobalLevel(level)
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339})

	// Store — use Firestore when configured, otherwise in-memory
	var store registry.Store
	var resourceStore resources.Store = resources.NewMemoryStore()
	projectID := os.Getenv("GCP_PROJECT_ID")
	if projectID == "" {
		projectID = os.Getenv("FIRESTORE_PROJECT_ID")
	}

	if projectID != "" {
		databaseID := os.Getenv("FIRESTORE_DATABASE_ID") // optional, defaults to (default)
		fs, err := registry.NewFirestoreStore(projectID, databaseID)
		if err != nil {
			log.Fatal().Err(err).Msg("firestore init failed")
		}
		defer fs.Close()
		store = fs
		resourceStore = fs.ModuleResources()
		log.Info().Str("project", projectID).Str("database", databaseID).Msg("using Firestore store")
	} else {
		store = registry.NewMemoryStore()
		log.Warn().Msg("using in-memory store — data will not persist across restarts")
	}

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
		catalog.NewOfficialMCPSource(""), // Official MCP registry
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

	wifValidator, err := identityValidator(store, cfg)
	if err != nil {
		log.Fatal().Err(err).Msg("identity configuration failed")
	}

	if err := bootstrapRoleBindings(ctx, store, cfg.RoleBindingsFile); err != nil {
		log.Fatal().Err(err).Msg("operator role binding configuration failed")
	}

	// Secret Manager (optional — nil in local dev)
	sm, err := secrets.NewManager(context.Background(), projectID)
	if err != nil {
		log.Warn().Err(err).Msg("secret manager init failed — secret validation disabled")
	}
	if sm != nil {
		defer sm.Close()
	}

	// Handlers
	catalogH := api.NewCatalogHandler(aggregator, store)
	instanceH := api.NewInstanceHandler(store, engine)
	agentH := api.NewAgentHandler(store, hydraClient)
	authH := api.NewAuthHandler(hydraClient, store)
	llmH := api.NewLLMHandler(store, k8sClient, cfg.GatewayName, sm)
	a2aH := api.NewA2AHandler(store)
	skillsH := api.NewSkillsHandler(store)
	dashH := api.NewDashboardHandler(store)
	iamH := api.NewIAMHandler(store, wifValidator)
	moduleRegistry := modules.NewRegistry()
	if err := moduleRegistry.Register(&httpModule{descriptor: modules.Descriptor{Name: "identity", Version: "1.0.0", ContractVersion: modules.ContractVersion, Capabilities: []string{"identity.resolve"}}}); err != nil {
		log.Fatal().Err(err).Msg("register identity")
	}
	connectedH, clientH, err := connectedHandlers(resourceStore, sm, cfg)
	if err != nil {
		log.Fatal().Err(err).Msg("connected module configuration failed")
	}
	availableModules := builtinModules(llmH, a2aH, skillsH)
	extendConnectedModules(availableModules, connectedH, clientH)
	enabledModules, err := selectModules(cfg.EnabledModules, availableModules)
	if err != nil {
		log.Fatal().Err(err).Msg("invalid module configuration")
	}
	enabledPlanes := make([]models.Plane, 0, len(enabledModules))
	for _, m := range enabledModules {
		if err := moduleRegistry.Register(m); err != nil {
			log.Fatal().Err(err).Msg("register module")
		}
		if usesWorkloadRoutes(m) {
			enabledPlanes = append(enabledPlanes, models.Plane(m.descriptor.Name))
		}
	}
	routes, err := deploy.NewRouteRegistryForPlanes(enabledPlanes...)
	if err != nil {
		log.Fatal().Err(err).Msg("configure module routes")
	}
	engine.WithRouteRegistry(routes)
	catalogH.WithEnabledPlanes(enabledPlanes...)
	if err := registerExternalModules(moduleRegistry, cfg.ModuleManifest, cfg.DevelopmentMode); err != nil {
		log.Fatal().Err(err).Msg("external module configuration failed")
	}
	runtime := modules.NewRuntime(moduleRegistry)
	startCtx, startCancel := context.WithTimeout(ctx, 15*time.Second)
	err = runtime.Start(startCtx)
	startCancel()
	if err != nil {
		log.Fatal().Err(err).Msg("module startup failed")
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := runtime.Stop(stopCtx); err != nil {
			log.Error().Err(err).Msg("module shutdown failed")
		}
	}()
	sseH := api.NewSSEHandler(store)
	runsH := api.NewRunsHandler(store)
	// PR 11 item 3: real Tape admin client when TAPE_URL is set.
	// Falls back to NoopTapeAdmin (the default) in dev paths where
	// no tape-server is reachable, so the API still starts cleanly.
	if tapeAdmin, ok := api.NewGRPCTapeAdmin(); ok {
		runsH = runsH.WithTapeAdmin(tapeAdmin)
		defer tapeAdmin.Close()
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
			defer retention.Close()
			log.Info().Dur("interval", interval).Msg("retention reactor started")
		}
	} else {
		log.Warn().Msg("TAPE_URL not set — operator actions are no-ops (NoopTapeAdmin)")
	}

	// Router
	r := chi.NewRouter()
	r.Use(api.Recover)
	r.Use(api.RequestID)
	r.Use(api.Logger)
	corsOrigins := cfg.AllowedOrigins
	if len(corsOrigins) == 0 {
		log.Warn().Msg("CONSOLE_ORIGINS unset — allowing all origins (dev only)")
		corsOrigins = []string{"*"}
	} else {
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
	r.Use(api.WIFAuthMode(wifValidator, cfg.DevelopmentMode)) // Extract WIF identity + sync Dimension B
	r.Use(api.AuditLog)                                       // Log all mutations
	r.Use(api.RateLimit(200))                                 // 200 req/min per user (in-process)
	r.Use(api.Compress)                                       // Response compression hints

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

	// API v1
	r.Route("/api/v1", func(r chi.Router) {
		r.Use(api.RequireIdentity(cfg.DevelopmentMode))
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if nativeInvocation(r) {
					next.ServeHTTP(w, r)
					return
				}
				api.ControlPlaneAccess(cfg.DevelopmentMode)(next).ServeHTTP(w, r)
			})
		})
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

		for _, module := range enabledModules {
			module.mount(r)
		}
		r.Get("/modules", moduleDiscovery(moduleRegistry))

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
		r.Route("/runs", func(r chi.Router) {
			r.Use(api.RequireTenant)
			r.Get("/", runsH.List)
			r.Get("/_health", runsH.Health)
			r.Get("/{run_id}", runsH.Get)
			r.Get("/{run_id}/events", runsH.Events)
			r.Get("/{run_id}/effects", runsH.Effects)
			r.Get("/{run_id}/obligations", runsH.Obligations)
			r.Get("/{run_id}/budgets", runsH.Budgets)
			r.Get("/{run_id}/operator-audit", runsH.ListOperatorAudit)
			// Operator actions (PR 10 + PR 11 item 7 idempotency wrap).
			// Each requires an aiplex:runs:{action} scope at the gateway
			// authz layer; the Idempotency middleware dedupes
			// double-clicks for 5 minutes.
			r.Group(func(r chi.Router) {
				r.Use(api.Idempotency(runsActionFromPath))
				r.Post("/{run_id}/redrive", runsH.Redrive)
				r.Post("/{run_id}/reconcile", runsH.Reconcile)
				r.Post("/{run_id}/cancel", runsH.Cancel)
				r.Post("/{run_id}/signal", runsH.Signal)
				r.Post("/{run_id}/compensate", runsH.Compensate)
				r.Post("/{run_id}/compact", runsH.Compact)
			})
		})

		// IAM — role bindings, WIF identity resolution
		r.Route("/iam", func(r chi.Router) {
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
	r.With(api.RequireIdentity(cfg.DevelopmentMode)).Get("/events/stream", sseH.Stream)

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
			log.Fatal().Err(e).Msg("CIMD metadata configuration invalid")
		}
		r.Handle("/mcp/client-metadata/*", metadata)
	}

	// Start server
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	tlsConfig, err := serverTLS(cfg)
	if err != nil {
		log.Fatal().Err(err).Msg("TLS listener configuration failed")
	}
	srv := &http.Server{TLSConfig: tlsConfig, Addr: addr, Handler: r, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 90 * time.Second}

	go func() {
		log.Info().Str("addr", addr).Msg("aiplex-api starting")
		serve := srv.ListenAndServe
		if cfg.TLSCertFile != "" {
			serve = func() error { return srv.ListenAndServeTLS(cfg.TLSCertFile, cfg.TLSKeyFile) }
		}
		if err := serve(); err != nil && err != http.ErrServerClosed {
			log.Fatal().Err(err).Msg("server failed")
		}
	}()

	<-ctx.Done()
	log.Info().Msg("shutting down")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	srv.Shutdown(shutdownCtx)
}

// runsActionFromPath extracts the operator action name from a /runs/{id}/{action}
// path for idempotency caching. Returns "" if the path doesn't match.
func runsActionFromPath(r *http.Request) string {
	path := r.URL.Path
	// Last segment of /api/v1/runs/{id}/{action}
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		return path[i+1:]
	}
	return ""
}

func parseFloatEnv(name string, def float64) float64 {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	n, err := strconv.ParseFloat(v, 64)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

func parseIntEnv(name string, def int) int {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

func parseDurationEnv(name string, def time.Duration) time.Duration {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return def
	}
	return d
}
