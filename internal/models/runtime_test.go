package models

import (
	"errors"
	"testing"
)

// TestRuntimeValidate_None — the explicit absence path. Always valid,
// regardless of environment, regardless of what else is set on the
// struct (because every other field is meaningless when engine=none).
func TestRuntimeValidate_None(t *testing.T) {
	cases := []RuntimeConfig{
		NoneRuntime(),
		{}, // zero value — engine is the empty string, treated as none
		{Engine: RuntimeEngineNone, Store: RuntimeStoreConfig{Type: "garbage"}},
	}
	for i, rc := range cases {
		for _, env := range []Environment{EnvDev, EnvStaging, EnvProd} {
			if err := rc.Validate(env); err != nil {
				t.Errorf("case %d / env=%s: expected ok, got %v", i, env, err)
			}
		}
	}
}

// TestRuntimeValidate_TapeDefaults — the canonical dev config from
// TapeRuntime() is valid in dev/staging but rejected in prod (sqlite).
func TestRuntimeValidate_TapeDefaults(t *testing.T) {
	rc := TapeRuntime()
	for _, env := range []Environment{EnvDev, EnvStaging} {
		if err := rc.Validate(env); err != nil {
			t.Errorf("env=%s: expected ok, got %v", env, err)
		}
	}
	if err := rc.Validate(EnvProd); err == nil {
		t.Errorf("env=prod: expected sqlite rejection, got nil")
	} else if !errors.Is(err, ErrRuntimeInvalid) {
		t.Errorf("env=prod: expected ErrRuntimeInvalid, got %v", err)
	}
}

// TestRuntimeValidate_TapeRequiresDurable — engine=tape with durable=false
// is a configuration smell that must be caught at admission time.
func TestRuntimeValidate_TapeRequiresDurable(t *testing.T) {
	rc := TapeRuntime()
	rc.Durable = false
	err := rc.Validate(EnvDev)
	if err == nil {
		t.Fatalf("expected error for durable=false, got nil")
	}
	if !errors.Is(err, ErrRuntimeInvalid) {
		t.Fatalf("expected ErrRuntimeInvalid, got %v", err)
	}
}

// TestRuntimeValidate_ProdStoreRequiresSecret — a postgres / alloydb /
// bigtable store must reference a Secret; the deploy engine refuses to
// inline a connection string in a manifest.
func TestRuntimeValidate_ProdStoreRequiresSecret(t *testing.T) {
	for _, storeType := range []RuntimeStoreType{
		RuntimeStorePostgres, RuntimeStoreAlloyDB, RuntimeStoreBigtable,
	} {
		rc := TapeRuntime()
		rc.Store = RuntimeStoreConfig{Type: storeType}
		if err := rc.Validate(EnvProd); err == nil {
			t.Errorf("store=%s: expected secret_ref requirement, got nil", storeType)
		}

		rc.Store.SecretRef = "tape-store-url"
		if err := rc.Validate(EnvProd); err != nil {
			t.Errorf("store=%s with secret_ref: expected ok, got %v", storeType, err)
		}
	}
}

// TestRuntimeValidate_PubsubRequiresTopic — sink=pubsub without a topic
// is a deploy-time failure (the engine can't infer one).
func TestRuntimeValidate_PubsubRequiresTopic(t *testing.T) {
	rc := TapeRuntime()
	rc.Store = RuntimeStoreConfig{Type: RuntimeStorePostgres, SecretRef: "tape-store-url"}
	rc.Outbox = RuntimeOutboxConfig{Sink: RuntimeOutboxPubSub}
	if err := rc.Validate(EnvProd); err == nil {
		t.Fatalf("expected topic requirement, got nil")
	}
	rc.Outbox.Topic = "aiplex-tape-events"
	if err := rc.Validate(EnvProd); err != nil {
		t.Fatalf("expected ok with topic, got %v", err)
	}
}

// TestRuntimeValidate_UnknownValues — typos surface at admission time
// instead of silently disabling the runtime.
func TestRuntimeValidate_UnknownValues(t *testing.T) {
	t.Run("engine", func(t *testing.T) {
		rc := RuntimeConfig{Engine: "Temporal", Durable: true}
		if err := rc.Validate(EnvDev); err == nil {
			t.Errorf("expected unknown engine rejection, got nil")
		}
	})
	t.Run("store", func(t *testing.T) {
		rc := TapeRuntime()
		rc.Store.Type = "spanner"
		if err := rc.Validate(EnvDev); err == nil {
			t.Errorf("expected unknown store rejection, got nil")
		}
	})
	t.Run("sink", func(t *testing.T) {
		rc := TapeRuntime()
		rc.Outbox.Sink = "kafka"
		if err := rc.Validate(EnvDev); err == nil {
			t.Errorf("expected unknown sink rejection, got nil")
		}
	})
}
