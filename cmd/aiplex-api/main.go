package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
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

	// WIF Validator
	wifValidator := auth.NewWIFValidator(store, auth.WIFConfig{
		WorkforcePoolID: cfg.WorkforcePoolID,
		WorkloadPoolID:  cfg.WIFPool,
		TrustedIssuers: []string{
			"https://accounts.google.com",
			"https://sts.windows.net/",
		},
	})

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
	sseH := api.NewSSEHandler(store)
	runsH := api.NewRunsHandler(store)

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
	r.Use(api.MaxBody(1 << 20))          // 1MB max request body
	r.Use(api.WIFAuth(wifValidator))     // Extract WIF identity + sync Dimension B
	r.Use(api.AuditLog)                  // Log all mutations
	r.Use(api.RateLimit(200))            // 200 req/min per user (in-process)
	r.Use(api.Compress)                  // Response compression hints

	// Health (readyz checks store connectivity)
	healthH := api.NewHealthHandler(store)
	r.Get("/healthz", healthH.Liveness)
	r.Get("/readyz", healthH.Readiness)

	// API v1
	r.Route("/api/v1", func(r chi.Router) {
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

		// LLMPlex — routing, providers, usage
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

		// A2APlex — delegations, agent cards
		r.Route("/a2a", func(r chi.Router) {
			r.Get("/agents", a2aH.ListAgentCards)
			r.Post("/delegations", a2aH.RecordDelegation)
			r.Get("/delegations", a2aH.ListDelegations)
			r.Get("/delegations/{id}", a2aH.GetDelegation)
			r.Patch("/delegations/{id}", a2aH.UpdateDelegation)
			r.Get("/delegations/{id}/chain", a2aH.GetDelegationChain)
		})

		// SkillsPlex — skill servers, manifests, invocation audit
		r.Route("/skills", func(r chi.Router) {
			r.Get("/servers", skillsH.ListSkillServers)
			r.Post("/invocations", skillsH.RecordInvocation)
			r.Get("/invocations", skillsH.ListInvocations)
		})

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
		r.Route("/runs", func(r chi.Router) {
			r.Get("/", runsH.List)
			r.Get("/{run_id}", runsH.Get)
			r.Get("/{run_id}/events", runsH.Events)
			r.Get("/{run_id}/effects", runsH.Effects)
			r.Get("/{run_id}/obligations", runsH.Obligations)
			r.Get("/{run_id}/budgets", runsH.Budgets)
			// Operator actions (PR 10). Each requires an aiplex:runs:*
			// scope at the authz layer. The handler returns 202 +
			// appends a synthetic ExecutionEvent so the action shows
			// up on the run timeline next to Tape's own journal rows.
			r.Post("/{run_id}/redrive", runsH.Redrive)
			r.Post("/{run_id}/reconcile", runsH.Reconcile)
			r.Post("/{run_id}/cancel", runsH.Cancel)
			r.Post("/{run_id}/signal", runsH.Signal)
			r.Post("/{run_id}/compensate", runsH.Compensate)
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
		r.Get("/consent", authH.ConsentGet)
		r.Post("/consent", authH.ConsentAccept)
		r.Post("/token-hook", authH.TokenHook)
		r.Get("/login", authH.LoginRedirect)
		r.Get("/users/{userId}/scopes", authH.GetUserScopes)
		r.Put("/users/{userId}/scopes", authH.SetUserScopes)
	})

	// Server-Sent Events for live dashboard
	r.Get("/events/stream", sseH.Stream)

	// AIPlex ↔ Tape audit ingestion. Tape's outbox relay POSTs
	// here; AIPlex dedupes on (run_id, seq), quarantines unknown
	// agents, and updates the ExecutionRun projection. See
	// internal/api/runs.go and docs/guides/tape-runtime.md.
	r.Post("/internal/tape/events", runsH.Ingest)

	// MCP sub-registry (v0.1 spec)
	r.Get("/v0.1/servers", catalogH.List)

	// A2A Agent Card discovery (per-instance)
	r.Get("/a2a/{instanceId}/.well-known/agent.json", a2aH.GetAgentCard)

	// SkillsPlex skills manifest (per-instance)
	r.Get("/skills/{instanceId}/.well-known/skills.json", skillsH.GetSkillsManifest)

	// Start server
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	srv := &http.Server{Addr: addr, Handler: r}

	go func() {
		log.Info().Str("addr", addr).Msg("aiplex-api starting")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal().Err(err).Msg("server failed")
		}
	}()

	<-ctx.Done()
	log.Info().Msg("shutting down")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	srv.Shutdown(shutdownCtx)
}
