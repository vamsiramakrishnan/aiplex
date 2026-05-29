package models

// Runtime configuration for an Instance — how the agent inside it executes.
// For durable agents this carries the engine (currently "tape"), the
// backing store, which reactors to run, and where the outbox stream goes.
// For non-durable instances (the v1 path) the Runtime field exists but is
// set to `RuntimeConfig{Engine: RuntimeEngineNone}` — explicit absence
// over nil, so consumers never branch on a nil pointer.
//
// See the Tape integration survey at docs/integration/aiplex-tape-survey.md
// for the architectural context, and tape/docs/integrations/aiplex.md in
// the durable-agents repo for the wire contract.

// RuntimeEngine names the execution substrate for an instance.
type RuntimeEngine string

const (
	// RuntimeEngineNone is the v1 path — no durable runtime, the agent
	// runs as a plain pod with whatever state model it brings itself.
	RuntimeEngineNone RuntimeEngine = "none"
	// RuntimeEngineTape backs the instance with the Tape durable-execution
	// substrate. AIPlex provisions a tape-server and reactors alongside
	// the agent pod and injects the AIPLEX_* + TAPE_URL env vars.
	RuntimeEngineTape RuntimeEngine = "tape"
)

// RuntimeStoreType names the persistence backend for the runtime engine's
// journal. SQLite is dev-only; production deployments use Postgres /
// AlloyDB / Bigtable.
type RuntimeStoreType string

const (
	RuntimeStoreSQLite   RuntimeStoreType = "sqlite"
	RuntimeStorePostgres RuntimeStoreType = "postgres"
	RuntimeStoreAlloyDB  RuntimeStoreType = "alloydb"
	RuntimeStoreBigtable RuntimeStoreType = "bigtable"
)

// RuntimeOutboxSink names how the runtime engine's outbox stream is fanned
// out for downstream consumers (including AIPlex's own audit ingestion).
type RuntimeOutboxSink string

const (
	RuntimeOutboxLog     RuntimeOutboxSink = "log"
	RuntimeOutboxWebhook RuntimeOutboxSink = "webhook"
	RuntimeOutboxPubSub  RuntimeOutboxSink = "pubsub"
)

// RuntimeStoreConfig describes the journal/projection store.
type RuntimeStoreConfig struct {
	Type RuntimeStoreType `json:"type"`
	// SecretRef names the K8s Secret carrying the store's connection URL
	// (for postgres/alloydb/bigtable). Required for production stores —
	// the deploy engine never inlines a connection string in the
	// generated manifest. Unused for sqlite.
	SecretRef string `json:"secret_ref,omitempty"`
}

// RuntimeReactorsConfig toggles each of Tape's reactor loops. All four are
// independent and can be scaled per-environment.
type RuntimeReactorsConfig struct {
	Recovery     bool `json:"recovery"`
	Reconciler   bool `json:"reconciler"`
	Timers       bool `json:"timers"`
	Outbox       bool `json:"outbox"`
	Compensation bool `json:"compensation"`
}

// RuntimeOutboxConfig configures the fan-out of the runtime's outbox stream.
type RuntimeOutboxConfig struct {
	Sink  RuntimeOutboxSink `json:"sink"`
	Topic string            `json:"topic,omitempty"` // required for sink=pubsub
}

// RuntimeConfig describes how an Instance executes — currently a thin
// envelope over the engine selector + its backing store + which reactors
// to run + where the outbox goes. RuntimeConfig{Engine: RuntimeEngineNone}
// is the explicit non-durable path and is always valid.
type RuntimeConfig struct {
	Engine     RuntimeEngine          `json:"engine"`
	Durable    bool                   `json:"durable"`
	Replayable bool                   `json:"replayable"`
	Store      RuntimeStoreConfig     `json:"store"`
	Reactors   RuntimeReactorsConfig  `json:"reactors"`
	Outbox     RuntimeOutboxConfig    `json:"outbox"`
}

// NoneRuntime is the canonical "no durable runtime" config — use this for
// Instances that don't need a runtime engine. It is the zero-value-friendly
// equivalent of "explicit absence."
func NoneRuntime() RuntimeConfig {
	return RuntimeConfig{Engine: RuntimeEngineNone}
}

// TapeRuntime returns a runtime config for the Tape engine with sensible
// defaults — all four reactors on, outbox set to a `log` sink (so a dev
// deployment works without external infrastructure). Callers override the
// store and outbox as needed.
func TapeRuntime() RuntimeConfig {
	return RuntimeConfig{
		Engine:     RuntimeEngineTape,
		Durable:    true,
		Replayable: true,
		Store:      RuntimeStoreConfig{Type: RuntimeStoreSQLite},
		Reactors: RuntimeReactorsConfig{
			Recovery: true, Reconciler: true, Timers: true,
			Outbox: true, Compensation: true,
		},
		Outbox: RuntimeOutboxConfig{Sink: RuntimeOutboxLog},
	}
}
