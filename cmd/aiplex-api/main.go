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

	// Store (in-memory for dev; swap for Firestore in production)
	store := registry.NewMemoryStore()

	// Seed built-in LLM provider templates
	providers := catalog.NewBuiltInProviders()
	templates, _ := providers.Fetch(ctx)
	for i := range templates {
		store.PutTemplate(ctx, &templates[i])
	}

	// Catalog aggregator
	sources := []catalog.Source{
		catalog.NewLocalSource(store, models.PlaneMCPlex),
		catalog.NewLocalSource(store, models.PlaneA2APlex),
		catalog.NewLocalSource(store, models.PlaneLLMPlex),
		providers,
	}
	aggregator := catalog.NewAggregator(sources)

	// Deploy engine
	engine := deploy.NewEngine(store, cfg.TrustDomain)

	// Auth (Ory Hydra)
	hydraClient := auth.NewHydraClient(cfg.HydraAdminURL)

	// Handlers
	catalogH := api.NewCatalogHandler(aggregator, store)
	instanceH := api.NewInstanceHandler(store, engine)
	agentH := api.NewAgentHandler(store)
	authH := api.NewAuthHandler(hydraClient, store)
	llmH := api.NewLLMHandler(store)
	a2aH := api.NewA2AHandler(store)
	dashH := api.NewDashboardHandler(store)

	// Router
	r := chi.NewRouter()
	r.Use(api.Recover)
	r.Use(api.RequestID)
	r.Use(api.Logger)
	r.Use(api.CORS("*")) // TODO: restrict to Console origin in production
	r.Use(api.MaxBody(1 << 20)) // 1MB max request body

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

		// Dashboard — unified observability
		r.Route("/dashboard", func(r chi.Router) {
			r.Get("/stats", dashH.GetStats)
			r.Get("/denials", dashH.ListPolicyDenials)
			r.Post("/denials", dashH.RecordPolicyDenial)
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

	// MCP sub-registry (v0.1 spec)
	r.Get("/v0.1/servers", catalogH.List)

	// A2A Agent Card discovery (per-instance)
	r.Get("/a2a/{instanceId}/.well-known/agent.json", a2aH.GetAgentCard)

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
