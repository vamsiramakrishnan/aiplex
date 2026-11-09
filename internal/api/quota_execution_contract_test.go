package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/pkg/contracts"
	"github.com/vamsiramakrishnan/aiplex/pkg/execution"
	"github.com/vamsiramakrishnan/aiplex/pkg/modelapi"
	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
)

func TestManagedQuotaSSETailCannotReplaceHigherCumulativeUsage(t *testing.T) {
	for _, terminal := range []bool{false, true} {
		name := "usage-only tail"
		if terminal {
			name = "second terminal frame"
		}
		t.Run(name, func(t *testing.T) {
			lower := strings.ReplaceAll(quotaHostModelTerminal, `"candidatesTokenCount":2`, `"candidatesTokenCount":1`)
			lower = strings.ReplaceAll(lower, `"totalTokenCount":6`, `"totalTokenCount":5`)
			if !terminal {
				lower = `{"responseId":"native-response","modelVersion":"model-v1","usageMetadata":{"promptTokenCount":3,"cachedContentTokenCount":1,"candidatesTokenCount":1,"thoughtsTokenCount":1,"totalTokenCount":5}}`
			}
			f := quotaHostModelNew(t, true, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, quotaHostModelSSEFirst+"data: "+quotaHostModelTerminal+"\n\n"+"data: "+lower+"\n\n")
			}, nil)
			w := httptest.NewRecorder()
			f.handler.NativeModel(w, f.request("streamGenerateContent", "tail-regression", quotaHostModelBody, false))
			info := quotaHostModelAttempt(t, f, w.Header().Get(execution.AttemptHeader))
			if info.Decision.State != "unknown" || w.Body.String() != quotaHostModelSSEFirst || f.calls.Load() != 1 {
				t.Fatalf("contradictory tail escaped or settled: state=%s calls=%d body=%s", info.Decision.State, f.calls.Load(), w.Body.String())
			}
			for _, budget := range info.Plan.Budgets {
				balance, err := f.host.Engines()["tenant"].BudgetBalance(context.Background(), "tenant", budget.Key)
				if err != nil || balance.Held <= 0 || balance.Consumed != 0 {
					t.Fatalf("contradictory usage released exposure: %+v %v", balance, err)
				}
			}
		})
	}
}

func TestManagedQuotaSSERepeatedCumulativeFinalDoesNotDoubleCharge(t *testing.T) {
	stream := quotaHostModelSSEFirst + "data: " + quotaHostModelTerminal + "\n\n" + "data: " + quotaHostModelTerminal + "\n\n"
	f := quotaHostModelNew(t, true, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, stream)
	}, nil)
	w := httptest.NewRecorder()
	f.handler.NativeModel(w, f.request("streamGenerateContent", "repeated-final", quotaHostModelBody, false))
	if w.Code != 200 || w.Body.String() != stream || f.calls.Load() != 1 {
		t.Fatalf("idempotent cumulative facts changed native framing: %d %s", w.Code, w.Body.String())
	}
	quotaHostModelAssertSettled(t, f, w.Header().Get(execution.AttemptHeader))
}

// Two hosts use different Engine instances over the same transactional store.
// Both native planes must consume the same tenant/principal/account controls.
func quotaCrossPlaneFixture(t *testing.T, guard string, upstream http.HandlerFunc, configure ...func(*QuotaHostConfig)) (*quotaHostModelFixture, *ConnectedHandler, func(*ConnectedHandler, http.ResponseWriter, string, string)) {
	t.Helper()
	var config QuotaHostConfig
	f := quotaHostModelNew(t, false, upstream, func(cfg *QuotaHostConfig) {
		cfg.Tenants[0].Policy.Meters["calls"] = quotaplex.Meter{ID: "calls", Unit: "calls"}
		cfg.Bindings = append(cfg.Bindings, QuotaBinding{ID: "tool-search", TenantID: "tenant", Plane: "mcplex", ResourceID: "tool", OperationID: "tools/call:search", Profile: "request-count", Provider: "mcp", Meters: map[string]string{"requests": "requests", "tool_calls": "calls"}})
		switch guard {
		case "budget":
			cfg.Tenants[0].Policy.Budgets[0].Limit = 1
		case "rate":
			cfg.Tenants[0].Policy.Rates[0].Rate = 1
			cfg.Tenants[0].Policy.Rates[0].Burst = 1
		}
		for _, update := range configure {
			update(cfg)
		}
		config = *cfg
	})
	record, err := f.resources.Get(context.Background(), "tenant", "model-surface", "surface")
	if err != nil {
		t.Fatal(err)
	}
	var model ModelConnection
	if err := json.Unmarshal(record.Spec, &model); err != nil {
		t.Fatal(err)
	}
	tool := managedToolConnection(model.Surface.Origin + "/mcp")
	tool.ID, tool.Execution.ID = "tool", "tool"
	cohesionRecord(t, f.resources, "tool-connection", "tool", tool)
	access := f.access()
	access.Scopes = append(access.Scopes, "mcp:tools:search")
	f.handler.ResolveAccess = func(context.Context, *models.WIFIdentity) (*models.ResolvedAccess, error) { return access, nil }
	replica := NewConnectedHandler(f.resources, f.handler.Runtime, f.handler.Tenant, nil, true)
	replica.ResolveAccess = f.handler.ResolveAccess
	replica.Quota, err = NewQuotaHost(context.Background(), config, f.store, func() time.Time { return f.now }, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	invoke := func(h *ConnectedHandler, w http.ResponseWriter, plane, key string) {
		r := f.request("generateContent", key, quotaHostModelBody, false)
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		r = r.WithContext(ctx)
		if plane == "mcplex" {
			r.URL.Path = "/api/v1/mcp/connections/tool/invoke"
			r.SetPathValue("id", "tool")
			r.Body = io.NopCloser(strings.NewReader(managedToolBody))
		}
		r = r.WithContext(context.WithValue(r.Context(), keyWIFAccess, access))
		if plane == "mcplex" {
			h.NativeTool(w, r)
		} else {
			h.NativeModel(w, r)
		}
	}
	return f, replica, invoke
}

func TestManagedQuotaIndependentActorCapSpansModelAndTool(t *testing.T) {
	f, replica, invoke := quotaCrossPlaneFixture(t, "actor", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"native":true}`)
	}, func(cfg *QuotaHostConfig) {
		policy := &cfg.Tenants[0].Policy
		policy.Budgets = append(policy.Budgets, quotaplex.BudgetRule{ID: "actor-cap", Scope: quotaplex.Scope{Kind: "actor"}, MeterID: "requests", Limit: 1, Period: policy.Budgets[0].Period})
	})
	f.identity = quotaHostTestIdentity(t, f.now, func(claims map[string]any) {
		claims["act"] = map[string]any{"sub": "delegated-agent", "groups": []any{"finance"}}
	})
	// Native actor claims identify the actor; an independent trusted directory
	// supplies its membership. Neither the human nor act.groups supplies it.
	actor := quotaplex.Subject{ID: f.identity.ActorID, Kind: "agent", Membership: quotaplex.Membership{Ref: contracts.SnapshotRef{ID: "independent-agent-directory", Version: "7"}, TenantID: "tenant", SubjectID: f.identity.ActorID, Groups: []quotaplex.GroupRef{{Source: "agent-directory", ID: "automation"}}, IssuedAt: f.now.Add(-time.Minute), ExpiresAt: f.now.Add(time.Minute)}}
	f.host.actor, replica.Quota.actor = quotaHostTestActor{actor}, quotaHostTestActor{actor}
	access, err := f.handler.ResolveAccess(context.Background(), f.identity)
	if err != nil {
		t.Fatal(err)
	}
	access.Identity = *f.identity
	first := httptest.NewRecorder()
	invoke(f.handler, first, "llmplex", "delegated-model")
	if first.Code != 200 {
		t.Fatal(first.Code, first.Body.String())
	}
	info := quotaHostModelAttempt(t, f, first.Header().Get(execution.AttemptHeader))
	if info.Demand.Actor == nil || info.Demand.Actor.ID != actor.ID || info.Demand.Actor.Membership.Ref != actor.Membership.Ref || len(info.Demand.Actor.Membership.Groups) != 1 || info.Demand.Actor.Membership.Groups[0] != actor.Membership.Groups[0] || info.Demand.Principal.Membership.Groups[0].ID != "engineering" || info.Plan.PayerID != "team" {
		t.Fatal("independent identities or actor membership were lost at actual dispatch", info.Demand)
	}
	next := httptest.NewRecorder()
	invoke(replica, next, "mcplex", "delegated-tool")
	if next.Code != 429 || f.calls.Load() != 1 {
		t.Fatal("tool dispatch bypassed the actor's model consumption", next.Code, next.Body.String(), f.calls.Load())
	}
}

func TestManagedQuotaMissingAuthoritativeMembershipCannotReachEitherPlane(t *testing.T) {
	f, replica, invoke := quotaCrossPlaneFixture(t, "identity", func(http.ResponseWriter, *http.Request) {
		t.Error("request without authoritative memberships reached provider")
	})
	f.identity.VerifiedEvidence.GroupsPresent = false
	t.Setenv("QUOTA_HOST_MODEL_TOKEN", "")
	for _, plane := range []string{"llmplex", "mcplex"} {
		w := httptest.NewRecorder()
		invoke(replica, w, plane, "unverified-"+plane)
		if w.Code != 503 || f.calls.Load() != 0 || len(quotaHostTestAttempts(t, f.store)) != 0 {
			t.Fatal("unverified membership reached credentials or reserved capacity", plane, w.Code, w.Body.String())
		}
	}
}

func TestManagedQuotaSharedModelToolLimitsAcrossReplicas(t *testing.T) {
	for _, guard := range []string{"budget", "rate", "concurrency"} {
		for _, firstPlane := range []string{"llmplex", "mcplex"} {
			t.Run(guard+"/"+firstPlane+" first", func(t *testing.T) {
				entered, release := make(chan struct{}), make(chan struct{})
				var held atomic.Bool
				defer close(release)
				f, replica, invoke := quotaCrossPlaneFixture(t, guard, func(w http.ResponseWriter, r *http.Request) {
					if guard == "concurrency" && held.CompareAndSwap(false, true) {
						close(entered)
						select {
						case <-release:
						case <-r.Context().Done():
							return
						}
					}
					w.Header().Set("Content-Type", "application/json")
					if r.URL.Path == "/mcp" {
						io.WriteString(w, managedToolResponse)
					} else {
						io.WriteString(w, `{"native":true}`)
					}
				})
				first := httptest.NewRecorder()
				done := make(chan struct{})
				go func() { defer close(done); invoke(f.handler, first, firstPlane, "first") }()
				if guard == "concurrency" {
					select {
					case <-entered:
					case <-time.After(3 * time.Second):
						t.Fatal("first plane did not reach provider")
					}
				} else {
					<-done
					if first.Code != 200 {
						t.Fatal("first plane failed", first.Code, first.Body.String())
					}
				}
				secondPlane := "llmplex"
				if firstPlane == "llmplex" {
					secondPlane = "mcplex"
				}
				denied := httptest.NewRecorder()
				invoke(replica, denied, secondPlane, "other-plane")
				if denied.Code != 429 || f.calls.Load() != 1 {
					t.Fatalf("shared %s bypassed by %s: status=%d calls=%d body=%s", guard, secondPlane, denied.Code, f.calls.Load(), denied.Body.String())
				}
				if guard == "concurrency" {
					release <- struct{}{}
					select {
					case <-done:
					case <-time.After(3 * time.Second):
						t.Fatal("first plane failed to finish")
					}
					if first.Code != 200 {
						t.Fatal("first plane failed", first.Code, first.Body.String())
					}
				}
				info := quotaHostModelAttempt(t, f, first.Header().Get(execution.AttemptHeader))
				if info.Decision.State != "settled" || info.Plan.PayerID != "team" || info.Actual["requests"] != 1 {
					t.Fatal("shared payer or terminal usage changed", info)
				}
			})
		}
	}
}

func TestManagedQuotaRetriesAndFallbackKeepSeparateCharges(t *testing.T) {
	var upstreamCalls atomic.Int32
	f := quotaHostModelNew(t, false, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if upstreamCalls.Add(1) <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		io.WriteString(w, `{"native":true}`)
	}, func(cfg *QuotaHostConfig) {
		fallback := cfg.Bindings[0]
		fallback.ID, fallback.ResourceID = "model-fallback", "fallback"
		cfg.Bindings = append(cfg.Bindings, fallback)
	})
	record, err := f.resources.Get(context.Background(), "tenant", "model-surface", "surface")
	if err != nil {
		t.Fatal(err)
	}
	var fallback ModelConnection
	if err = json.Unmarshal(record.Spec, &fallback); err != nil {
		t.Fatal(err)
	}
	fallback.Surface.ID = "fallback"
	cohesionRecord(t, f.resources, "model-surface", "fallback", fallback)
	access := f.access()
	access.Scopes = append(access.Scopes, "llm:surface:fallback:*")
	f.handler.ResolveAccess = func(context.Context, *models.WIFIdentity) (*models.ResolvedAccess, error) { return access, nil }
	invoke := func(key string, facade, useFallback bool, body string) *httptest.ResponseRecorder {
		r := f.request("generateContent", key, body, facade)
		if useFallback {
			r.URL.Path = strings.Replace(r.URL.Path, "/surface/", "/fallback/", 1)
			r.SetPathValue("id", "fallback")
		}
		r.Header.Set("X-Aiplex-Operation-Id", "one-logical-workflow")
		r = r.WithContext(context.WithValue(r.Context(), keyWIFAccess, access))
		w := httptest.NewRecorder()
		f.serve(w, r, facade)
		return w
	}
	first := invoke("attempt-one", false, false, quotaHostModelBody)
	if first.Code != 503 || f.calls.Load() != 1 {
		t.Fatal(first.Code, first.Body.String())
	}
	// Changing the facade or payload cannot turn a transport retry into a
	// fresh billable invocation. A new retry and fallback require new keys.
	for _, body := range []string{quotaHostModelBody, strings.ReplaceAll(quotaHostModelBody, "hello", "changed")} {
		if w := invoke("attempt-one", true, false, body); w.Code != 409 || f.calls.Load() != 1 {
			t.Fatal("retry bypassed canonical native attempt", w.Code, w.Body.String())
		}
	}
	retry := invoke("attempt-two", true, false, quotaHostModelBody)
	fallbackResult := invoke("fallback-attempt", false, true, quotaHostModelBody)
	if retry.Code != 503 || fallbackResult.Code != 200 || f.calls.Load() != 3 {
		t.Fatal("new billable attempts were not dispatched independently", retry.Code, fallbackResult.Code, f.calls.Load())
	}
	ids := map[string]bool{}
	logical := ""
	for _, w := range []*httptest.ResponseRecorder{first, retry, fallbackResult} {
		info := quotaHostModelAttempt(t, f, w.Header().Get(execution.AttemptHeader))
		if ids[info.Demand.AttemptID] || info.Decision.State != "settled" || info.Actual["requests"] != 1 {
			t.Fatal("billable retries merged or HTTP errors refunded", info)
		}
		ids[info.Demand.AttemptID] = true
		if logical != "" && logical != info.Demand.LogicalOperationID {
			t.Fatal("fallback lost causal linkage", info.Demand)
		}
		logical = info.Demand.LogicalOperationID
		for _, budget := range info.Plan.Budgets {
			balance, err := f.host.Engines()["tenant"].BudgetBalance(context.Background(), "tenant", budget.Key)
			if err != nil || balance.Consumed != 3 || balance.Held != 0 {
				t.Fatal("separate provider calls were not each charged once", balance, err)
			}
		}
	}
}

func TestManagedQuotaAsyncSubmissionCountsExchangeWithoutInventingJobUsage(t *testing.T) {
	const native = `{"name":"projects/project/locations/us-central1/publishers/google/models/model-v1/operations/job-1","done":false}`
	f := quotaHostModelNew(t, false, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, native)
	}, func(cfg *QuotaHostConfig) {
		cfg.Bindings = cfg.Bindings[:1]
		cfg.Bindings[0].OperationID = "models.predictLongRunning"
	})
	record, err := f.resources.Get(context.Background(), "tenant", "model-surface", "surface")
	if err != nil {
		t.Fatal(err)
	}
	var connection ModelConnection
	if err = json.Unmarshal(record.Spec, &connection); err != nil {
		t.Fatal(err)
	}
	connection.Surface.Operations, err = modelapi.SelectOperations("models.predictLongRunning")
	if err != nil {
		t.Fatal(err)
	}
	record.Spec, err = json.Marshal(connection)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.resources.Put(context.Background(), resources.Record{TenantID: "tenant", Kind: "model-surface", ID: "surface", Spec: record.Spec}, record.Revision); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	f.handler.NativeModel(w, f.request("predictLongRunning", "submit-job", `{"instances":[{"prompt":"native job"}]}`, false))
	if w.Code != 200 || w.Body.String() != native || f.calls.Load() != 1 {
		t.Fatal("native asynchronous submission changed", w.Code, w.Body.String(), f.calls.Load())
	}
	info := quotaHostModelAttempt(t, f, w.Header().Get(execution.AttemptHeader))
	if info.Decision.State != "settled" || info.Actual["requests"] != 1 || len(info.Actual) != 1 || !strings.HasSuffix(info.ProviderResource, "/operations/job-1") {
		t.Fatal("HTTP completion was confused with billable job completion", info)
	}
}

func TestManagedQuotaUnknownStreamAndFallbackRetainSeparateExposure(t *testing.T) {
	f := quotaHostModelNew(t, true, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ":streamGenerateContent") {
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, quotaHostModelSSEFirst)
		} else {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, quotaHostModelTerminal)
		}
	}, func(cfg *QuotaHostConfig) { cfg.Tenants[0].Policy.Concurrency[0].Limit = 2 })
	invoke := func(operation, key string) *httptest.ResponseRecorder {
		r := f.request(operation, key, quotaHostModelBody, false)
		r.Header.Set("X-Aiplex-Operation-Id", "unknown-then-fallback")
		w := httptest.NewRecorder()
		f.handler.NativeModel(w, r)
		return w
	}
	first := invoke("streamGenerateContent", "unknown-stream")
	unknown := quotaHostModelAttempt(t, f, first.Header().Get(execution.AttemptHeader))
	if unknown.Decision.State != "unknown" {
		t.Fatal("incomplete stream did not retain exposure", unknown)
	}
	if replay := invoke("streamGenerateContent", "unknown-stream"); replay.Code != 409 || f.calls.Load() != 1 {
		t.Fatal("unknown attempt redispatched", replay.Code, f.calls.Load())
	}
	fallback := invoke("generateContent", "known-fallback")
	known := quotaHostModelAttempt(t, f, fallback.Header().Get(execution.AttemptHeader))
	if fallback.Code != 200 || known.Decision.State != "settled" || known.Demand.LogicalOperationID != unknown.Demand.LogicalOperationID || known.Demand.AttemptID == unknown.Demand.AttemptID || f.calls.Load() != 2 {
		t.Fatal("fallback merged with uncertain provider work", known, fallback.Code, f.calls.Load())
	}
	for _, budget := range unknown.Plan.Budgets {
		balance, err := f.host.Engines()["tenant"].BudgetBalance(context.Background(), "tenant", budget.Key)
		if err != nil || balance.Held != budget.Demand || balance.Consumed != known.Actual[budget.MeterID] {
			t.Fatal("successful fallback erased the original uncertainty", balance, err)
		}
	}
}
