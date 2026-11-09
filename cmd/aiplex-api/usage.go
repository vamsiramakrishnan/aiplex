package main

import (
	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"
	"github.com/vamsiramakrishnan/aiplex/internal/api"
	"github.com/vamsiramakrishnan/aiplex/internal/config"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

// configureUsageLedger selects the ledger store matching the registry store
// (Firestore when configured, memory otherwise) and attaches the recorder to
// the managed handlers and to legacy LLM usage reports.
func configureUsageLedger(store registry.Store, cfg *config.Config, connected *api.ConnectedHandler, llm *api.LLMHandler) *api.UsageHandler {
	var ledger usageledger.Store
	if firestore, ok := store.(*registry.FirestoreStore); ok {
		ledger = firestore.UsageLedger(usageledger.DefaultRetention)
	} else {
		ledger = usageledger.NewMemoryStore()
	}
	recorder := usageledger.NewRecorder(ledger, usageledger.WithErrorHandler(func(record usageledger.Record, err error) {
		// Never log usage contents beyond identifiers; records hold no payloads.
		log.Error().Err(err).Str("usage_record", record.ID).Str("tenant", record.TenantID).Str("plane", record.Plane).Msg("usage ledger write failed")
	}))
	connected.Ledger = recorder
	llm.WithUsageLedger(recorder, connected.Tenant, connected.ModelPrices)
	return api.NewUsageHandler(ledger, connected.Tenant, cfg.DevelopmentMode)
}

// mountUsageLedger serves /api/v1/usage. Prices are tenant resources of kind
// model-price and use the shared revisioned resource handler.
func mountUsageLedger(r chi.Router, usage *api.UsageHandler, connected *api.ConnectedHandler) {
	r.Route("/usage", func(r chi.Router) {
		r.Get("/records", usage.ListRecords)
		r.Get("/records/{id}", usage.GetRecord)
		r.Get("/summary", usage.Summary)
		r.Route("/prices", func(r chi.Router) { resourceRoutes(r, connected, usageledger.PriceKind) })
	})
}
