package models

import (
	"errors"
	"fmt"
)

// Environment identifies the deployment tier for validation rules that
// only apply in production (e.g. SQLite is dev-only).
type Environment string

const (
	EnvDev     Environment = "dev"
	EnvStaging Environment = "staging"
	EnvProd    Environment = "prod"
)

// ErrRuntimeInvalid is returned by Validate when the runtime config is
// inconsistent. Callers should join with errors.Is for typed checks.
var ErrRuntimeInvalid = errors.New("runtime: invalid")

// Validate checks the runtime config against the rules documented in
// docs/integration/aiplex-tape-survey.md §"Validation":
//
//   * engine=tape implies durable=true (no transient Tape runs).
//   * Non-sqlite stores must reference a Secret rather than inlining a
//     connection string in the manifest.
//   * sink=pubsub requires a topic — the deploy engine can't infer it.
//   * In production, sqlite is rejected (it's an in-pod file and doesn't
//     survive pod restarts the way the Tape contract assumes).
//
// engine=none short-circuits — there's nothing else to validate.
func (r RuntimeConfig) Validate(env Environment) error {
	if r.Engine == RuntimeEngineNone || r.Engine == "" {
		return nil
	}
	if r.Engine != RuntimeEngineTape {
		return fmt.Errorf("%w: unknown engine %q (supported: none, tape)",
			ErrRuntimeInvalid, r.Engine)
	}
	if !r.Durable {
		return fmt.Errorf("%w: engine=%s requires durable=true "+
			"(the whole point of a durable runtime)",
			ErrRuntimeInvalid, r.Engine)
	}
	switch r.Store.Type {
	case "":
		return fmt.Errorf("%w: engine=%s requires store.type "+
			"(one of sqlite, postgres, alloydb, bigtable)",
			ErrRuntimeInvalid, r.Engine)
	case RuntimeStoreSQLite:
		if env == EnvProd {
			return fmt.Errorf("%w: store.type=sqlite is dev-only; "+
				"use postgres, alloydb, or bigtable in production",
				ErrRuntimeInvalid)
		}
	case RuntimeStorePostgres, RuntimeStoreAlloyDB, RuntimeStoreBigtable:
		if r.Store.SecretRef == "" {
			return fmt.Errorf("%w: store.type=%s requires store.secret_ref "+
				"(connection URLs must come from a K8s Secret, never inlined)",
				ErrRuntimeInvalid, r.Store.Type)
		}
	default:
		return fmt.Errorf("%w: unknown store.type %q (supported: sqlite, postgres, alloydb, bigtable)",
			ErrRuntimeInvalid, r.Store.Type)
	}
	switch r.Outbox.Sink {
	case "", RuntimeOutboxLog, RuntimeOutboxWebhook, RuntimeOutboxPubSub:
		// ok
	default:
		return fmt.Errorf("%w: unknown outbox.sink %q (supported: log, webhook, pubsub)",
			ErrRuntimeInvalid, r.Outbox.Sink)
	}
	if r.Outbox.Sink == RuntimeOutboxPubSub && r.Outbox.Topic == "" {
		return fmt.Errorf("%w: outbox.sink=pubsub requires outbox.topic",
			ErrRuntimeInvalid)
	}
	return nil
}
