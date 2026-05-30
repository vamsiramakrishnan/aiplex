package api

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	pb "github.com/vamsiramakrishnan/durable-agents/tape/sdk/go/tapepb"

	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
)

// RetentionReactor drives the compaction + purge lifecycle for
// Tape-backed runs (PR 13). Loops on a configurable tick (default
// 15 minutes — payload-zero updates and event purges are heavy in
// aggregate and don't need to fire often). On each tick:
//
//   1. compactSettledRuns: walks AIPlex-projected runs that have
//      a terminal status and an ended_at older than (now -
//      compact_after_days). For each, calls Tape's CompactRun via
//      the TapeAdmin compaction surface and updates the local
//      ExecutionRun projection (Compacted=true, CompactedAt=now).
//
//   2. purgeExpiredEvents: walks runs whose ended_at is older than
//      (now - delete_after_days). For each, hands off events to
//      backend TTL and keeps the ExecutionRun summary by default
//      — the projection is the immutable audit anchor. Set
//      DeleteProjection=true to drop the summary too.
//
// Retention windows come from each Instance's RuntimeConfig.Retention.
// We honour per-instance policies so a tenant with stricter compliance
// can shorten its hot window without affecting other tenants.
type RetentionReactor struct {
	store     registry.Store
	compactor TapeCompactor
	interval  time.Duration

	mu      sync.Mutex
	closing chan struct{}
	wg      sync.WaitGroup
}

// TapeCompactor abstracts Tape's compact_run RPC so tests can
// substitute a fake. The real implementation is GRPCTapeAdmin
// (extended below).
type TapeCompactor interface {
	CompactRun(ctx context.Context, runID string) (TapeCompactResult, error)
}

// TapeCompactResult mirrors Tape's CompactRunResponse.
type TapeCompactResult struct {
	DecisionsZeroed  int64
	EffectsZeroed    int64
	BytesSaved       int64
	AlreadyCompacted bool
}

// NewRetentionReactor builds the reactor. `interval` controls the
// tick cadence; pass 0 for the default 15 minutes.
func NewRetentionReactor(store registry.Store, compactor TapeCompactor, interval time.Duration) *RetentionReactor {
	if interval <= 0 {
		interval = 15 * time.Minute
	}
	return &RetentionReactor{
		store:     store,
		compactor: compactor,
		interval:  interval,
		closing:   make(chan struct{}),
	}
}

// Start launches the reactor in the background. Stop the reactor by
// calling Close — graceful shutdown drains the in-flight tick.
func (r *RetentionReactor) Start(ctx context.Context) {
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		ticker := time.NewTicker(r.interval)
		defer ticker.Stop()
		// Tick once on startup so a fresh process picks up the
		// backlog from before it started.
		r.tick(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-r.closing:
				return
			case <-ticker.C:
				r.tick(ctx)
			}
		}
	}()
}

// Close signals the loop to exit and waits for the in-flight tick.
func (r *RetentionReactor) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	select {
	case <-r.closing:
		// Already closed.
	default:
		close(r.closing)
	}
	r.wg.Wait()
}

// tick runs one compaction + purge pass.
func (r *RetentionReactor) tick(ctx context.Context) {
	r.compactSettledRuns(ctx)
	r.purgeExpiredEvents(ctx)
}

// compactSettledRuns finds runs eligible per their Instance's
// retention.compact_after_days and calls Tape's CompactRun + flips the
// projection's Compacted flag.
//
// The instance → retention policy lookup is per-run because different
// instances may have different policies; we group runs by instance to
// avoid a per-row policy lookup.
func (r *RetentionReactor) compactSettledRuns(ctx context.Context) {
	logger := log.Ctx(ctx).With().Str("reactor", "retention.compact").Logger()
	now := time.Now()

	runs, err := r.store.ListExecutionRuns(ctx, "", "", 1000)
	if err != nil {
		logger.Warn().Err(err).Msg("list runs failed")
		return
	}

	// Cache instance → retention policy lookups across the batch.
	policyCache := map[string]models.RuntimeRetention{}
	policyFor := func(instanceID string) models.RuntimeRetention {
		if p, ok := policyCache[instanceID]; ok {
			return p
		}
		inst, err := r.store.GetInstance(ctx, instanceID)
		if err != nil {
			// Unknown instance → fall back to the canonical defaults
			// rather than leaving runs unattended forever.
			p := models.NormaliseRetention(models.RuntimeRetention{})
			policyCache[instanceID] = p
			return p
		}
		p := models.NormaliseRetention(inst.Runtime.Retention)
		policyCache[instanceID] = p
		return p
	}

	for _, run := range runs {
		if run.Compacted {
			continue
		}
		if !isTerminal(run.Status) {
			continue
		}
		if run.EndedAt == nil {
			continue
		}
		policy := policyFor(run.AIPlexInstanceID)
		cutoff := now.AddDate(0, 0, -policy.CompactAfterDays)
		if !run.EndedAt.Before(cutoff) {
			continue
		}
		result, err := r.compactor.CompactRun(ctx, run.RunID)
		if err != nil {
			logger.Warn().Str("run_id", run.RunID).Err(err).Msg("Tape compact_run failed")
			continue
		}
		// Stamp the projection. Both fresh compactions and
		// already-compacted runs land here (the projection might
		// have missed the run.compacted event but Tape has the
		// authoritative state).
		stamp := now
		updated := run
		updated.Compacted = true
		updated.CompactedAt = &stamp
		if err := r.store.UpsertExecutionRun(ctx, &updated); err != nil {
			logger.Warn().Str("run_id", run.RunID).Err(err).Msg("projection upsert failed")
			continue
		}
		logger.Info().
			Str("run_id", run.RunID).
			Int64("decisions_zeroed", result.DecisionsZeroed).
			Int64("effects_zeroed", result.EffectsZeroed).
			Int64("bytes_saved", result.BytesSaved).
			Bool("already_compacted", result.AlreadyCompacted).
			Msg("compacted")
	}
}

// purgeExpiredEvents drops execution_events rows for runs older than
// delete_after_days. The ExecutionRun summary is kept by default
// (DeleteProjection=false), so the audit anchor survives. When
// DeleteProjection=true the projection row is also deleted; we honour
// this for ops teams who genuinely want hard erasure (right-to-be-
// forgotten, etc.) and accept that they're trading auditability for
// the strict policy.
func (r *RetentionReactor) purgeExpiredEvents(ctx context.Context) {
	logger := log.Ctx(ctx).With().Str("reactor", "retention.purge").Logger()
	now := time.Now()

	runs, err := r.store.ListExecutionRuns(ctx, "", "", 1000)
	if err != nil {
		logger.Warn().Err(err).Msg("list runs failed")
		return
	}

	policyCache := map[string]models.RuntimeRetention{}
	policyFor := func(instanceID string) models.RuntimeRetention {
		if p, ok := policyCache[instanceID]; ok {
			return p
		}
		inst, err := r.store.GetInstance(ctx, instanceID)
		if err != nil {
			p := models.NormaliseRetention(models.RuntimeRetention{})
			policyCache[instanceID] = p
			return p
		}
		p := models.NormaliseRetention(inst.Runtime.Retention)
		policyCache[instanceID] = p
		return p
	}

	for _, run := range runs {
		if !isTerminal(run.Status) || run.EndedAt == nil {
			continue
		}
		policy := policyFor(run.AIPlexInstanceID)
		cutoff := now.AddDate(0, 0, -policy.DeleteAfterDays)
		if !run.EndedAt.Before(cutoff) {
			continue
		}
		// Hard purge — paginated drop. The store interface today
		// doesn't expose a bulk purge; we list + idempotency is
		// the contract. Future store implementations can add a
		// dedicated bulk-delete method.
		if events, err := r.store.ListExecutionEvents(ctx, run.RunID, 0, 100000); err == nil && len(events) > 0 {
			// MemoryStore + Firestore don't expose a per-row event
			// delete (events are append-only). The "purge" here is a
			// projection-level acknowledgement: we mark the run's
			// RetainedUntil so the Console renders accordingly, and
			// let the per-backend retention TTL (Firestore TTL
			// policies, or a scheduled DELETE on the SQL backend)
			// reclaim the rows. This avoids correctness pitfalls
			// from racing event ingestion vs. delete.
			retained := now
			updated := run
			updated.RetainedUntil = &retained
			if err := r.store.UpsertExecutionRun(ctx, &updated); err != nil {
				logger.Warn().Str("run_id", run.RunID).Err(err).Msg("projection upsert failed")
				continue
			}
			logger.Info().
				Str("run_id", run.RunID).
				Int("events_remaining", len(events)).
				Msg("retention window expired; events handed off to backend TTL")
		}
		if policy.DeleteProjection {
			if err := r.store.DeleteExecutionRun(ctx, run.RunID); err != nil {
				logger.Warn().Str("run_id", run.RunID).Err(err).Msg("delete projection failed")
			}
		}
	}
}

func isTerminal(s models.ExecutionRunStatus) bool {
	switch s {
	case models.ExecutionRunTerminal,
		models.ExecutionRunFailed,
		models.ExecutionRunCancelled,
		models.ExecutionRunStuck:
		return true
	}
	return false
}

// CompactRun on the *GRPCTapeAdmin shim — adapts the Tape Go SDK
// response into TapeCompactResult so the reactor doesn't import
// proto types directly.
//
// The lazy-dial pattern from the existing operator actions applies
// here too.
func (g *GRPCTapeAdmin) CompactRun(ctx context.Context, runID string) (TapeCompactResult, error) {
	c, err := g.ensure()
	if err != nil {
		return TapeCompactResult{}, fmt.Errorf("dial tape-server: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	resp, err := c.PB().CompactRun(ctx, &pb.CompactRunRequest{RunId: runID})
	if err != nil {
		return TapeCompactResult{}, err
	}
	return TapeCompactResult{
		DecisionsZeroed:  resp.DecisionsZeroed,
		EffectsZeroed:    resp.EffectsZeroed,
		BytesSaved:       resp.BytesSaved,
		AlreadyCompacted: resp.AlreadyCompacted,
	}, nil
}
