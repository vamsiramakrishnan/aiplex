package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/vamsiramakrishnan/aiplex/internal/connections"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/pkg/deployment"
	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
)

type deploymentAPIRunner struct {
	writes  atomic.Int32
	unknown bool
}

func (f *deploymentAPIRunner) Run(_ context.Context, c deployment.Command) (deployment.ProcessResult, error) {
	if slices.Contains(c.Args, "--version") {
		return deployment.ProcessResult{Started: true, Stdout: []byte(`{"name":"anvil","digest":"sha256:` + strings.Repeat("a", 64) + `"}`)}, nil
	}
	if slices.Contains(c.Args, "--dry-run") {
		return deployment.ProcessResult{Started: true, Stdout: []byte(`{"operation":"infra.networks.create","method":"POST","url":"https://infra.example/networks","headers":{},"body":{},"idempotencyKeyPresent":true,"retryPlan":{"enabled":false,"maxAttempts":0},"confirmationRequired":true}`)}, nil
	}
	f.writes.Add(1)
	if f.unknown {
		return deployment.ProcessResult{Started: true, Interrupted: true}, context.DeadlineExceeded
	}
	return deployment.ProcessResult{Started: true, Stdout: []byte(`{"name":"network-created"}`)}, nil
}

type deploymentAPIFixture struct {
	h               *ConnectedHandler
	router          chi.Router
	runner          *deploymentAPIRunner
	owner, reviewer *models.WIFIdentity
	scopes          []string
	revoked         bool
	quota           *QuotaHost
	store           *resources.MemoryStore
}

func deploymentAPITestFixture(t *testing.T, unknown bool, probe string) *deploymentAPIFixture {
	t.Helper()
	now := time.Now().UTC()
	store := resources.NewMemoryStore()
	h := cohesionHandler(t, store, probe)
	f := &deploymentAPIFixture{h: h, store: store, runner: &deploymentAPIRunner{unknown: unknown}, owner: quotaHostTestIdentity(t, now, nil), reviewer: quotaHostTestIdentity(t, now, func(c map[string]any) { c["sub"] = "independent-reviewer" })}
	f.scopes = []string{"aiplex:deployment:plan:network", "aiplex:deployment:approve:network", "aiplex:deployment:execute:network", "aiplex:deployment:observe:network"}
	h.ResolveAccess = func(_ context.Context, i *models.WIFIdentity) (*models.ResolvedAccess, error) {
		scopes := f.scopes
		if f.revoked && i.PrincipalID == f.reviewer.PrincipalID {
			scopes = nil
		}
		return &models.ResolvedAccess{Identity: *i, Scopes: scopes}, nil
	}
	dir := t.TempDir()
	bundle := filepath.Join(dir, "bundle")
	if err := os.Mkdir(bundle, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundle, "air.json"), []byte(`{"schemaVersion":"1","operations":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	digest, err := deployment.BundleDigest(bundle)
	if err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(dir, "anvil")
	raw := []byte("fixture runtime")
	hash := sha256.Sum256(raw)
	if err = os.WriteFile(executable, raw, 0700); err != nil {
		t.Fatal(err)
	}
	cfg := deployment.Config{ID: "anvil", Executable: executable, ExecutableSHA256: hex.EncodeToString(hash[:]), ExpectedBuildDigest: "sha256:" + strings.Repeat("a", 64), Bundles: []deployment.Bundle{{ID: "infra", TenantID: "tenant", Directory: bundle, Digest: digest}}, Bindings: []deployment.Binding{{ID: "create", TenantID: "tenant", ResourceID: "network", BundleID: "infra", Command: []string{"networks", "create"}, OperationID: "infra.networks.create", Scopes: []string{"infra.write"}, Effect: "write"}}, AllowExecute: true}
	h.Runtime.Config.DeploymentProviders = []connections.DeploymentProvider{{Config: cfg, Readiness: []connections.DeploymentReadiness{{TenantID: "tenant", BindingID: "create", IdentityRef: "host", URL: probe + "/readiness", TargetRef: "native-network", ResourceField: "name", ReadyField: "state", ReadyValue: "READY", TTL: time.Minute}}}}
	qcfg := quotaHostTestConfig(now)
	qcfg.Bindings = nil
	for _, phase := range []string{"plan", "execute", "observe"} {
		qcfg.Bindings = append(qcfg.Bindings, QuotaBinding{ID: phase, TenantID: "tenant", Plane: "mcplex", ResourceID: "network", OperationID: "tools/call:anvil." + phase + ":infra.networks.create", Profile: "request-count", Provider: "anvil", Meters: map[string]string{"requests": "requests"}})
	}
	f.quota, err = NewQuotaHost(context.Background(), qcfg, quotaplex.NewMemoryStore(), time.Now, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	h.Quota = f.quota
	provider, err := deployment.NewAnvilWithRunner(cfg, deployment.AuthorizeFunc(func(ctx context.Context, a deployment.Action) error {
		return h.currentDeploymentGrant(ctx, a.Request.Context, "aiplex:deployment:"+a.Phase+":"+a.Request.Context.ResourceID)
	}), deploymentHostAdmission{h: h}, deploymentApprovals{h: h}, deployment.ResourceJournal{Store: store}, f.runner)
	if err != nil {
		t.Fatal(err)
	}
	h.Deployments = map[string]*deployment.Anvil{"anvil": provider}
	cohesionRecord(t, store, "deployment-intent", "network", deployment.Intent{ID: "network", ProviderRef: "anvil", BindingID: "create", Inputs: json.RawMessage(`{"name":"native-network"}`)})
	router := chi.NewRouter()
	router.Post("/providers/{provider}/bindings/{binding}/plan", h.PrepareDeployment)
	h.MountDeploymentExecution(router)
	f.router = router
	return f
}
func (f *deploymentAPIFixture) call(method, path, body, key string, i *models.WIFIdentity) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	ctx := context.WithValue(r.Context(), keyWIFIdentity, i)
	ctx = context.WithValue(ctx, keyWIFAccess, &models.ResolvedAccess{Identity: *i, Scopes: f.scopes})
	ctx = context.WithValue(ctx, keyRequestID, "request")
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, r.WithContext(ctx))
	return w
}
func (f *deploymentAPIFixture) plan(t *testing.T) savedDeploymentPlan {
	t.Helper()
	w := f.call("POST", "/providers/anvil/bindings/create/plan", "{}", "plan-one", f.owner)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var record resources.Record
	var plan savedDeploymentPlan
	if json.Unmarshal(w.Body.Bytes(), &record) != nil || json.Unmarshal(record.Spec, &plan) != nil {
		t.Fatal(w.Body.String())
	}
	return plan
}
func (f *deploymentAPIFixture) approve(t *testing.T, p savedDeploymentPlan) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"digest": p.Plan.ActionDigest, "reason": "reviewed native operation"})
	w := f.call("POST", "/network/plans/"+p.Plan.ActionDigest+"/approve", string(body), "approval-one", f.reviewer)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var record resources.Record
	if json.Unmarshal(w.Body.Bytes(), &record) != nil {
		t.Fatal(w.Body.String())
	}
	return record.ID
}
func TestDeploymentAPIReviewedApplyIsOneShotAndReadinessIsIndependent(t *testing.T) {
	var reads atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads.Add(1)
		if r.Header.Get("Authorization") != "Bearer readiness-token" {
			t.Error("wrong observation credential")
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"name":"native-network","state":"READY"}`)
	}))
	defer upstream.Close()
	t.Setenv("COHESION_HOST_CREDENTIAL", "readiness-token")
	f := deploymentAPITestFixture(t, false, upstream.URL)
	plan := f.plan(t)
	approval := f.approve(t, plan)
	body, _ := json.Marshal(map[string]string{"digest": plan.Plan.ActionDigest, "approval_id": approval})
	w := f.call("POST", "/network/plans/"+plan.Plan.ActionDigest+"/execute", string(body), "apply", f.reviewer)
	if w.Code != 403 || f.runner.writes.Load() != 0 {
		t.Fatal("different executor invoked", w.Code)
	}
	for i := 0; i < 2; i++ {
		w = f.call("POST", "/network/plans/"+plan.Plan.ActionDigest+"/execute", string(body), "apply", f.owner)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if f.runner.writes.Load() != 1 {
		t.Fatal("operation redispatched", f.runner.writes.Load())
	}
	var receipt deployment.Receipt
	_ = json.Unmarshal(w.Body.Bytes(), &receipt)
	if receipt.State != deployment.Succeeded || receipt.Readiness != "unverified" {
		t.Fatal(receipt)
	}
	w = f.call("POST", "/network/plans/"+plan.Plan.ActionDigest+"/observe", "{}", "", f.owner)
	if w.Code != 400 || reads.Load() != 0 {
		t.Fatal("missing key consumed observation")
	}
	w = f.call("POST", "/network/plans/"+plan.Plan.ActionDigest+"/observe", "{}", "observe-one", f.owner)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var observation DeploymentObservation
	_ = json.Unmarshal(w.Body.Bytes(), &observation)
	if !observation.Ready || observation.TargetRef != "native-network" || !observation.ExpiresAt.After(observation.ObservedAt) {
		t.Fatal(observation)
	}

	w = f.call("POST", "/network/plans/"+plan.Plan.ActionDigest+"/observe", "{}", "observe-one", f.owner)
	if w.Code != 200 || reads.Load() != 1 {
		t.Fatal("committed observation was read again upstream", w.Code, reads.Load())
	}
	w = f.call("GET", "/network/plans/"+plan.Plan.ActionDigest+"/observation", "", "observe-one", f.owner)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = f.call("GET", "/network/plans/"+plan.Plan.ActionDigest+"/observation", "", "observe-one", f.reviewer)
	if w.Code != 404 {
		t.Fatal("observation crossed owner boundary", w.Code)
	}
	rows := quotaHostTestAttempts(t, f.quota.store)
	if len(rows) != 3 {
		t.Fatal("plan/apply/probe not independently accounted", len(rows))
	}
}
func TestDeploymentAPIRevokedApprovalAndChangedIntentPreventWrites(t *testing.T) {
	for _, mutation := range []string{"revoke", "intent"} {
		t.Run(mutation, func(t *testing.T) {
			f := deploymentAPITestFixture(t, false, "http://127.0.0.1:19999")
			plan := f.plan(t)
			approval := f.approve(t, plan)
			if mutation == "revoke" {
				f.revoked = true
			} else {
				record, err := f.store.Get(context.Background(), "tenant", "deployment-intent", "network")
				if err != nil {
					t.Fatal(err)
				}
				if _, err = f.store.Put(context.Background(), record, record.Revision); err != nil {
					t.Fatal(err)
				}
			}
			body, _ := json.Marshal(map[string]string{"digest": plan.Plan.ActionDigest, "approval_id": approval})
			w := f.call("POST", "/network/plans/"+plan.Plan.ActionDigest+"/execute", string(body), "apply", f.owner)
			if w.Code < 400 || f.runner.writes.Load() != 0 {
				t.Fatal("revoked or stale review invoked", w.Code, f.runner.writes.Load())
			}
		})
	}
}
func TestDeploymentAPIUnknownInvocationIsNotRetriedOrDeclaredReady(t *testing.T) {
	f := deploymentAPITestFixture(t, true, "http://127.0.0.1:19999")
	plan := f.plan(t)
	approval := f.approve(t, plan)
	body, _ := json.Marshal(map[string]string{"digest": plan.Plan.ActionDigest, "approval_id": approval})
	w := f.call("POST", "/network/plans/"+plan.Plan.ActionDigest+"/execute", string(body), "apply", f.owner)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = f.call("POST", "/network/plans/"+plan.Plan.ActionDigest+"/execute", string(body), "apply", f.owner)
	if w.Code != 200 || f.runner.writes.Load() != 1 {
		t.Fatal("unknown operation retried", w.Code)
	}
	var receipt deployment.Receipt
	_ = json.Unmarshal(w.Body.Bytes(), &receipt)
	if receipt.State != deployment.Unknown || receipt.Readiness != "unverified" {
		t.Fatal(receipt)
	}
}
