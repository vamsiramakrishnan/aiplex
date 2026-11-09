package deployment

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/contracts"
	"github.com/vamsiramakrishnan/aiplex/pkg/enforcement"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
)

type fakeRunner struct {
	mu             sync.Mutex
	calls          []Command
	interrupted    bool
	exit           int
	runtimeChanged bool
}

func (f *fakeRunner) Run(ctx context.Context, c Command) (ProcessResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, c)
	if slices.Contains(c.Args, "--version") {
		d := "sha256:" + strings.Repeat("a", 64)
		if f.runtimeChanged {
			d = "sha256:" + strings.Repeat("b", 64)
		}
		b, _ := json.Marshal(map[string]string{"name": "anvil", "digest": d})
		return ProcessResult{Stdout: b, Started: true}, nil
	}
	if slices.Contains(c.Args, "deploy") {
		return ProcessResult{Stdout: []byte("Plan prepared; no cloud resources changed"), Started: true}, nil
	}
	inputIndex := slices.Index(c.Args, "--input")
	if inputIndex < 0 {
		return ProcessResult{}, errors.New("missing input file")
	}
	if filepath.Dir(c.Args[inputIndex+1]) == c.Directory {
		return ProcessResult{}, errors.New("private input changed approved bundle")
	}
	content, e := os.ReadFile(c.Args[inputIndex+1])
	if e != nil {
		return ProcessResult{}, e
	}
	if !json.Valid(content) {
		return ProcessResult{}, errors.New("invalid input")
	}
	info, _ := os.Stat(c.Args[inputIndex+1])
	if info.Mode().Perm() != 0600 {
		return ProcessResult{}, errors.New("input permissions")
	}
	if f.interrupted {
		return ProcessResult{Started: true, Interrupted: true, ExitCode: -1}, context.DeadlineExceeded
	}
	if f.exit != 0 {
		return ProcessResult{Started: true, ExitCode: f.exit, Stderr: []byte(`{"error":{"code":"policy_denied"}}`)}, errors.New("refused")
	}
	if slices.Contains(c.Args, "--dry-run") {
		return ProcessResult{Started: true, Stdout: []byte(`{"operation":"infra.networks.create","method":"POST","url":"https://api.example/networks?api_key=never-persist-this","headers":{"authorization":"Bearer never-persist-this"},"body":{"password":"never-persist-this"},"idempotencyKeyPresent":true,"retryPlan":{"enabled":false,"maxAttempts":0},"confirmationRequired":true}`)}, nil
	}
	return ProcessResult{Started: true, Stdout: []byte(`{"id":"network-created"}`)}, nil
}
func (f *fakeRunner) effects() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if slices.Contains(c.Args, "run") && !slices.Contains(c.Args, "--dry-run") {
			n++
		}
	}
	return n
}

type approvals struct {
	calls int
	err   error
}

func (a *approvals) Verify(_ context.Context, p Approval) error {
	a.calls++
	if a.err != nil {
		return a.err
	}
	if p.Reference != "reviewed-receipt" || !hexDigest.MatchString(p.ActionDigest) {
		return errors.New("approval receipt rejected")
	}
	return nil
}
func newFixture(t *testing.T, execute bool) (*Anvil, *fakeRunner, *approvals, Request, Config) {
	t.Helper()
	dir := t.TempDir()
	bundle := filepath.Join(dir, "bundle")
	_ = os.Mkdir(bundle, 0700)
	_ = os.WriteFile(filepath.Join(bundle, "air.json"), []byte(`{"schemaVersion":"1","operations":[]}`), 0600)
	_ = os.WriteFile(filepath.Join(bundle, "deploy.json"), []byte(`{"kind":"network"}`), 0600)
	bundleDigest, e := BundleDigest(bundle)
	if e != nil {
		t.Fatal(e)
	}
	exe := filepath.Join(dir, "anvil-test")
	_ = os.WriteFile(exe, []byte("fixture runtime"), 0700)
	cfg := Config{ID: "anvil", Executable: exe, ExecutableSHA256: digest([]byte("fixture runtime")), ExpectedBuildDigest: "sha256:" + strings.Repeat("a", 64), Bundles: []Bundle{{ID: "infra", TenantID: "tenant-a", Directory: bundle, Digest: bundleDigest}}, Bindings: []Binding{{ID: "network-create", TenantID: "tenant-a", ResourceID: "network-profile", BundleID: "infra", Command: []string{"networks", "create"}, OperationID: "infra.networks.create", AuthProfile: "prod", Scopes: []string{"infra.write"}, Effect: "write"}}, AllowExecute: execute}
	request := Request{Context: contracts.RequestContext{Version: "v1", TenantID: "tenant-a", RequestID: "r", PrincipalID: "canonical-principal", ActorID: "agent", ResourceID: "network-profile"}, BindingID: "network-create", Inputs: json.RawMessage(`{"name":"$(never-shell)","cidr":"10.0.0.0/24"}`), IdempotencyKey: "request-1"}
	runner := &fakeRunner{}
	approved := &approvals{}
	pipeline, e := enforcement.New(nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	p, e := NewAnvilWithRunner(cfg, AuthorizeFunc(func(context.Context, Action) error { return nil }), PipelineAdmission{pipeline}, approved, ResourceJournal{resources.NewMemoryStore()}, runner)
	if e != nil {
		t.Fatal(e)
	}
	return p, runner, approved, request, cfg
}
func TestPreparePinsBundleRuntimeAndExactNativeCLI(t *testing.T) {
	p, runner, _, r, _ := newFixture(t, false)
	plan, e := p.Prepare(context.Background(), r)
	if e != nil {
		t.Fatal(e)
	}
	if plan.State != Planned || plan.PlanOnly {
		t.Fatalf("wrong plan state %+v", plan)
	}
	serialized, _ := json.Marshal(plan)
	if strings.Contains(string(serialized), "never-persist-this") || strings.Contains(string(serialized), "Bearer ") {
		t.Fatal("public plan leaked preview credentials")
	}
	if plan.InputDigest != digest(r.Inputs) || plan.ActionDigest == "" || plan.PreviewDigest == "" {
		t.Fatal("missing provenance")
	}
	last := runner.calls[len(runner.calls)-1]
	for _, flag := range []string{"--dry-run", "--json", "--input", "--confirm", "--idempotency-key", "--no-retries"} {
		if !slices.Contains(last.Args, flag) {
			t.Fatal(flag)
		}
	}
	if slices.Contains(last.Args, string(r.Inputs)) {
		t.Fatal("inputs leaked into argv")
	}
	if _, e = os.Stat(last.Directory); !os.IsNotExist(e) {
		t.Fatal("private snapshot retained")
	}
	env := strings.Join(last.Env, "\n")
	if !strings.Contains(env, `"id":"canonical-principal"`) || !strings.Contains(env, `"scopes":["infra.write"]`) {
		t.Fatal("verified principal not propagated")
	}
	if _, e = p.Execute(context.Background(), r, plan, "reviewed-receipt"); !errors.Is(e, ErrDisabled) {
		t.Fatal(e)
	}
}

func TestExecutionNeedsExactApprovalAndJournalPreventsReplay(t *testing.T) {
	p, runner, approval, r, _ := newFixture(t, true)
	plan, e := p.Prepare(context.Background(), r)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.Execute(context.Background(), r, plan, "caller-says-yes"); e == nil {
		t.Fatal("unverified approval accepted")
	}
	if runner.effects() != 0 {
		t.Fatal("effect before approval")
	}
	receipt, e := p.Execute(context.Background(), r, plan, "reviewed-receipt")
	if e != nil {
		t.Fatal(e)
	}
	if receipt.State != Succeeded || receipt.Readiness != "unverified" {
		t.Fatal(receipt)
	}
	again, e := p.Execute(context.Background(), r, plan, "reviewed-receipt")
	if e != nil || again != receipt || runner.effects() != 1 {
		t.Fatal(e, again, runner.effects())
	}
	if approval.calls < 2 {
		t.Fatal("approval bypass")
	}
	changed := r
	changed.Inputs = json.RawMessage(`{"name":"other"}`)
	if _, e = p.Execute(context.Background(), changed, plan, "reviewed-receipt"); e == nil {
		t.Fatal("changed input accepted")
	}
}
func TestUnknownOutcomeIsJournaledAndNeverReexecuted(t *testing.T) {
	p, runner, _, r, _ := newFixture(t, true)
	plan, e := p.Prepare(context.Background(), r)
	if e != nil {
		t.Fatal(e)
	}
	runner.interrupted = true
	receipt, e := p.Execute(context.Background(), r, plan, "reviewed-receipt")
	if e == nil || receipt.State != Unknown {
		t.Fatal(e, receipt)
	}
	runner.interrupted = false
	replay, e := p.Execute(context.Background(), r, plan, "reviewed-receipt")
	if e != nil || replay.State != Unknown || runner.effects() != 1 {
		t.Fatal(e, replay, runner.effects())
	}
}
func TestBundleRuntimeTenantAndPreviewChangesFailClosed(t *testing.T) {
	for _, mode := range []string{"tenant", "bundle", "runtime", "preview", "expired", "actor"} {
		t.Run(mode, func(t *testing.T) {
			p, runner, _, r, cfg := newFixture(t, true)
			plan, e := p.Prepare(context.Background(), r)
			if e != nil {
				t.Fatal(e)
			}
			switch mode {
			case "tenant":
				r.Context.TenantID = "other"
			case "bundle":
				_ = os.WriteFile(filepath.Join(cfg.Bundles[0].Directory, "deploy.json"), []byte(`{"changed":true}`), 0600)
			case "runtime":
				runner.runtimeChanged = true
			case "preview":
				plan.Preview = json.RawMessage(`{"safe":true}`)
			case "expired":
				p.now = func() time.Time { return plan.ExpiresAt.Add(time.Second) }
			case "actor":
				r.Context.ActorID = "other-agent"
			}
			if _, e = p.Execute(context.Background(), r, plan, "reviewed-receipt"); e == nil {
				t.Fatal("tamper accepted")
			}
			if runner.effects() != 0 {
				t.Fatal("tampered effect")
			}
		})
	}
}
func TestPlanOnlyAnvilDeployNeverClaimsApplied(t *testing.T) {
	p, runner, _, r, cfg := newFixture(t, true)
	b := cfg.Bindings[0]
	b.ID = "connector-cloud-run"
	b.Command = nil
	b.OperationID = ""
	b.PlanTarget = "cloud-run"
	cfg.Bindings = []Binding{b}
	pipeline, _ := enforcement.New(nil, nil)
	p, e := NewAnvilWithRunner(cfg, AuthorizeFunc(func(context.Context, Action) error { return nil }), PipelineAdmission{pipeline}, &approvals{}, ResourceJournal{resources.NewMemoryStore()}, runner)
	if e != nil {
		t.Fatal(e)
	}
	r.BindingID = b.ID
	plan, e := p.Prepare(context.Background(), r)
	if e != nil || !plan.PlanOnly || plan.State != Planned {
		t.Fatal(e, plan)
	}
	if _, e = p.Execute(context.Background(), r, plan, "reviewed-receipt"); e == nil {
		t.Fatal("plan-only deploy treated as apply")
	}
}
func TestBundleHashExcludesOnlyKnownEvidenceAndRejectsSymlinks(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "air.yaml"), []byte("version: 1\n"), 0600)
	first, e := BundleDigest(dir)
	if e != nil {
		t.Fatal(e)
	}
	_ = os.WriteFile(filepath.Join(dir, "certification.json"), []byte(`{"passed":true}`), 0600)
	second, _ := BundleDigest(dir)
	if second != first {
		t.Fatal("evidence changed bundle hash")
	}
	_ = os.WriteFile(filepath.Join(dir, "unknown.report.json"), []byte(`{}`), 0600)
	third, _ := BundleDigest(dir)
	if third == first {
		t.Fatal("unknown report excluded")
	}
	_ = os.Symlink("/etc/passwd", filepath.Join(dir, "escape"))
	if _, e = BundleDigest(dir); e == nil {
		t.Fatal("symlink accepted")
	}
}
func TestJournalCASAndCrossActionConflict(t *testing.T) {
	j := ResourceJournal{resources.NewMemoryStore()}
	r := Receipt{ProviderID: "p", TenantID: "t", PrincipalID: "person", ResourceID: "resource", IdempotencyKey: "same", ActionDigest: strings.Repeat("a", 64), State: Pending, Readiness: "unverified"}
	_, fresh, e := j.Begin(context.Background(), r)
	if e != nil || !fresh {
		t.Fatal(e)
	}
	r.ActionDigest = strings.Repeat("b", 64)
	if _, _, e = j.Begin(context.Background(), r); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
}

func TestApprovalCannotExtendPlanExpiryOrChangeIntentProvenance(t *testing.T) {
	p, runner, _, request, _ := newFixture(t, true)
	request.Provenance = json.RawMessage(`{"intent_revision":1,"network_revision":1}`)
	plan, e := p.Prepare(context.Background(), request)
	if e != nil {
		t.Fatal(e)
	}
	tampered := plan
	tampered.CreatedAt = plan.CreatedAt.Add(time.Minute)
	tampered.ExpiresAt = plan.ExpiresAt.Add(time.Minute)
	if _, e = p.Execute(context.Background(), request, tampered, "reviewed-receipt"); e == nil {
		t.Fatal("approval expiry extended without new digest")
	}
	request.Provenance = json.RawMessage(`{"intent_revision":2,"network_revision":1}`)
	if _, e = p.Execute(context.Background(), request, plan, "reviewed-receipt"); e == nil {
		t.Fatal("changed intent provenance executed")
	}
	if runner.effects() != 0 {
		t.Fatal("refused plan reached execution")
	}
}
