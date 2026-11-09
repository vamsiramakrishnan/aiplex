package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/internal/models"
)

func TestVersionedConfigurationCASAndTombstonesAcrossBackends(t *testing.T) {
	factories := map[string]func(*testing.T) VersionedConfigurationStore{"memory": func(*testing.T) VersionedConfigurationStore { return NewMemoryStore() }, "firestore-wire": func(t *testing.T) VersionedConfigurationStore { s, _ := quotaWireFixture(t); return s }}
	for backend, makeStore := range factories {
		t.Run(backend, func(t *testing.T) {
			store := makeStore(t)
			ctx := context.Background()
			for _, test := range []struct {
				kind, id string
				value    any
			}{{ConfigAgent, "agent", &models.Agent{ClientID: "agent", ClientSecret: "never-store"}}, {ConfigRoute, "route", &models.LLMRouteConfig{ModelID: "route"}}, {ConfigProvider, "provider", &models.ProviderConfig{Provider: "provider"}}, {ConfigRoleBinding, "binding", &models.RoleBinding{ID: "binding"}}, {ConfigUserScopes, "principal", &configurationScopes{Scopes: []string{"llm:model:a"}}}} {
				t.Run(test.kind, func(t *testing.T) {
					raw, _ := json.Marshal(test.value)
					head, err := store.ConfigurationGeneration(ctx)
					if err != nil {
						t.Fatal(err)
					}
					first, err := store.PutConfiguration(ctx, ConfigurationRecord{Kind: test.kind, ID: test.id, Spec: raw}, 0, head)
					if err != nil || first.Revision != 1 {
						t.Fatal(first, err)
					}
					if test.kind == ConfigAgent && string(first.Spec) == string(raw) {
						t.Fatal("secret was not stripped")
					}
					if _, err = store.PutConfiguration(ctx, first, 0, -1); !errors.Is(err, ErrConfigurationConflict) {
						t.Fatal("create replaced existing record", err)
					}
					current, err := store.GetConfiguration(ctx, test.kind, test.id)
					if err != nil || current.Revision != 1 {
						t.Fatal(current, err)
					}
					second, err := store.PutConfiguration(ctx, current, current.Revision, -1)
					if err != nil || second.Revision != 2 {
						t.Fatal(second, err)
					}
					if err = store.DeleteConfiguration(ctx, test.kind, test.id, first.Revision, -1); !errors.Is(err, ErrConfigurationConflict) {
						t.Fatal("stale delete allowed", err)
					}
					if err = store.DeleteConfiguration(ctx, test.kind, test.id, second.Revision, -1); err != nil {
						t.Fatal(err)
					}
					if _, err = store.GetConfiguration(ctx, test.kind, test.id); !errors.Is(err, ErrNotFound) {
						t.Fatal(err)
					}
					recreated, err := store.PutConfiguration(ctx, first, 0, -1)
					if err != nil || recreated.Revision <= second.Revision {
						t.Fatal("ABA revision reused", recreated, err)
					}
					if _, err = store.PutConfiguration(ctx, current, second.Revision, -1); !errors.Is(err, ErrConfigurationConflict) {
						t.Fatal("deleted incarnation updated recreated record", err)
					}
				})
			}
		})
	}
}
func TestConfigurationDependencyGenerationRejectsValidationRaces(t *testing.T) {
	for _, backend := range []string{"memory", "firestore-wire"} {
		t.Run(backend, func(t *testing.T) {
			var store VersionedConfigurationStore = NewMemoryStore()
			if backend == "firestore-wire" {
				s, _ := quotaWireFixture(t)
				store = s
			}
			ctx := context.Background()
			before, err := store.ConfigurationGeneration(ctx)
			if err != nil {
				t.Fatal(err)
			}
			provider, _ := json.Marshal(models.ProviderConfig{Provider: "provider"})
			if _, err = store.PutConfiguration(ctx, ConfigurationRecord{Kind: ConfigProvider, ID: "provider", Spec: provider}, 0, before); err != nil {
				t.Fatal(err)
			}
			route, _ := json.Marshal(models.LLMRouteConfig{ModelID: "route"})
			if _, err = store.PutConfiguration(ctx, ConfigurationRecord{Kind: ConfigRoute, ID: "route", Spec: route}, 0, before); !errors.Is(err, ErrConfigurationConflict) {
				t.Fatal("dependency changed after validation", err)
			}
		})
	}
}
func TestConfigurationConcurrentCASHasOneWinner(t *testing.T) {
	for _, backend := range []string{"memory", "firestore-wire"} {
		t.Run(backend, func(t *testing.T) {
			var store VersionedConfigurationStore = NewMemoryStore()
			if backend == "firestore-wire" {
				s, _ := quotaWireFixture(t)
				store = s
			}
			ctx := context.Background()
			raw, _ := json.Marshal(models.RoleBinding{ID: "binding"})
			if _, err := store.PutConfiguration(ctx, ConfigurationRecord{Kind: ConfigRoleBinding, ID: "binding", Spec: raw}, 0, 0); err != nil {
				t.Fatal(err)
			}
			var winners atomic.Int32
			var wg sync.WaitGroup
			for i := 0; i < 8; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					_, err := store.PutConfiguration(ctx, ConfigurationRecord{Kind: ConfigRoleBinding, ID: "binding", Spec: raw}, 1, -1)
					if err == nil {
						winners.Add(1)
					} else if !errors.Is(err, ErrConfigurationConflict) {
						t.Error(err)
					}
				}()
			}
			wg.Wait()
			if winners.Load() != 1 {
				t.Fatal("multiple writers replaced one revision", winners.Load())
			}
		})
	}
}

func TestAtomicManualGrantUnionDoesNotLoseConcurrentDeployments(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := store.ModifyUserScopes(ctx, "principal", func(current []string) ([]string, error) {
				return append(current, fmt.Sprintf("mcp:tools:tool-%d", i)), nil
			}); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	scopes, err := store.GetUserScopes(ctx, "principal")
	if err != nil || len(scopes) != 32 {
		t.Fatal("concurrent grants lost", len(scopes), err)
	}
	record, err := store.GetConfiguration(ctx, ConfigUserScopes, "principal")
	if err != nil || record.Revision != 32 {
		t.Fatal(record, err)
	}
}
func TestFirestoreConfigurationRetriesAndPreservesLegacyDocuments(t *testing.T) {
	store, wire := quotaWireFixture(t)
	ctx := context.Background()
	wire.mu.Lock()
	wire.abortCommits = 1
	wire.mu.Unlock()
	value := &models.RoleBinding{ID: "binding", Group: "engineering", Role: models.RoleViewer}
	if err := store.PutRoleBinding(ctx, value); err != nil {
		t.Fatal(err)
	}
	if value.Revision != 1 {
		t.Fatal(value)
	}
	wire.mu.Lock()
	defer wire.mu.Unlock()
	if wire.documents["projects/quota-test/databases/(default)/documents/role_bindings/binding"] == nil {
		t.Fatal("legacy native document location changed")
	}
}
