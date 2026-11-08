package deployment

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type Anvil struct {
	cfg       Config
	authorize Authorizer
	admit     Admission
	approvals ApprovalVerifier
	journal   Journal
	runner    Runner
	now       func() time.Time
	bundles   map[string]Bundle
	bindings  map[string]Binding
}

var hexDigest = regexp.MustCompile(`^[0-9a-f]{64}$`)
var handle = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

func NewAnvil(cfg Config, authorize Authorizer, admit Admission, approvals ApprovalVerifier, journal Journal) (*Anvil, error) {
	return NewAnvilWithRunner(cfg, authorize, admit, approvals, journal, ExecRunner{})
}

// NewAnvilWithRunner permits host-owned execution infrastructure and hermetic
// tests. A runner is privileged configuration, never a request-selected adapter.
func NewAnvilWithRunner(cfg Config, authorize Authorizer, admit Admission, approvals ApprovalVerifier, journal Journal, runner Runner) (*Anvil, error) {
	if !handle.MatchString(cfg.ID) || authorize == nil || admit == nil || runner == nil {
		return nil, fmt.Errorf("provider id, authorization, admission and runner required")
	}
	if !filepath.IsAbs(cfg.Executable) || !hexDigest.MatchString(cfg.ExecutableSHA256) || !strings.HasPrefix(cfg.ExpectedBuildDigest, "sha256:") || !hexDigest.MatchString(strings.TrimPrefix(cfg.ExpectedBuildDigest, "sha256:")) {
		return nil, fmt.Errorf("pinned executable and Anvil build identity required")
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.Timeout < time.Millisecond || cfg.Timeout > 5*time.Minute {
		return nil, fmt.Errorf("invalid execution timeout")
	}
	if cfg.MaxOutputBytes == 0 {
		cfg.MaxOutputBytes = 1 << 20
	}
	if cfg.MaxOutputBytes < 1 || cfg.MaxOutputBytes > 8<<20 {
		return nil, fmt.Errorf("invalid output bound")
	}
	if cfg.AllowExecute && (approvals == nil || journal == nil) {
		return nil, fmt.Errorf("execution requires verified approvals and a durable journal")
	}
	cfg.PrefixSHA256 = maps.Clone(cfg.PrefixSHA256)
	cfg.PrefixArgs = append([]string(nil), cfg.PrefixArgs...)
	cfg.Environment = append([]string(nil), cfg.Environment...)
	for _, arg := range cfg.PrefixArgs {
		if !filepath.IsAbs(arg) || !hexDigest.MatchString(cfg.PrefixSHA256[arg]) {
			return nil, fmt.Errorf("prefix arguments must be pinned absolute script files")
		}
	}
	for _, env := range cfg.Environment {
		key, _, ok := strings.Cut(env, "=")
		if !ok || !regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`).MatchString(key) || strings.ContainsAny(env, "\x00\r\n") {
			return nil, fmt.Errorf("invalid configured subprocess environment")
		}
		switch key {
		case "HOME", "TMPDIR", "NODE_OPTIONS", "NODE_PATH", "LD_PRELOAD", "LD_LIBRARY_PATH", "BASH_ENV", "ENV", "ANVIL_MCP_TARGET", "ANVIL_PRINCIPALS", "ANVIL_PRINCIPAL", "ANVIL_IDENTITY_PROFILE":
			return nil, fmt.Errorf("unsafe subprocess environment override %s", key)
		}
	}
	a := &Anvil{cfg: cfg, authorize: authorize, admit: admit, approvals: approvals, journal: journal, runner: runner, now: time.Now, bundles: map[string]Bundle{}, bindings: map[string]Binding{}}
	for _, bundle := range cfg.Bundles {
		key := bundle.TenantID + "\x00" + bundle.ID
		if bundle.TenantID == "" || !handle.MatchString(bundle.ID) || !filepath.IsAbs(bundle.Directory) || !hexDigest.MatchString(bundle.Digest) {
			return nil, fmt.Errorf("invalid approved bundle")
		}
		if _, ok := a.bundles[key]; ok {
			return nil, fmt.Errorf("duplicate bundle")
		}
		a.bundles[key] = bundle
	}
	for _, binding := range cfg.Bindings {
		key := binding.TenantID + "\x00" + binding.ID
		if binding.TenantID == "" || !handle.MatchString(binding.ID) || binding.ResourceID == "" || (binding.Effect != "read" && binding.Effect != "write") {
			return nil, fmt.Errorf("invalid operation binding")
		}
		if _, ok := a.bindings[key]; ok {
			return nil, fmt.Errorf("duplicate operation binding")
		}
		if _, ok := a.bundles[binding.TenantID+"\x00"+binding.BundleID]; !ok {
			return nil, fmt.Errorf("binding bundle absent from tenant")
		}
		if binding.PlanTarget != "" {
			if binding.PlanTarget != "cloud-run" && binding.PlanTarget != "kubernetes" {
				return nil, fmt.Errorf("unsupported Anvil deployment-plan target")
			}
			if len(binding.Command) > 0 || binding.OperationID != "" {
				return nil, fmt.Errorf("plan-only binding cannot select an operation")
			}
		} else {
			if len(binding.Command) < 1 || binding.OperationID == "" {
				return nil, fmt.Errorf("reviewed operation and command handle required")
			}
			for _, part := range binding.Command {
				if !handle.MatchString(part) {
					return nil, fmt.Errorf("invalid generated operation command")
				}
			}
		}
		if binding.AuthProfile != "" && !handle.MatchString(binding.AuthProfile) {
			return nil, fmt.Errorf("invalid auth profile")
		}
		for _, scope := range binding.Scopes {
			if scope == "" || strings.Contains(scope, "*") || strings.ContainsAny(scope, "\r\n\x00") {
				return nil, fmt.Errorf("Anvil scopes must be exact reviewed grants")
			}
		}
		binding.Scopes = append([]string(nil), binding.Scopes...)
		binding.Command = append([]string(nil), binding.Command...)
		a.bindings[key] = binding
	}
	if len(a.bindings) == 0 {
		return nil, fmt.Errorf("at least one approved binding required")
	}
	return a, nil
}
func (a *Anvil) lookup(request Request) (Binding, Bundle, error) {
	if e := request.Context.Validate(); e != nil {
		return Binding{}, Bundle{}, e
	}
	binding, ok := a.bindings[request.Context.TenantID+"\x00"+request.BindingID]
	if !ok || binding.ResourceID != request.Context.ResourceID {
		return Binding{}, Bundle{}, fmt.Errorf("deployment tenant/resource binding denied")
	}
	if request.IdempotencyKey == "" || len(request.IdempotencyKey) > 256 || strings.ContainsAny(request.IdempotencyKey, "\r\n\x00") {
		return Binding{}, Bundle{}, fmt.Errorf("stable idempotency key required")
	}
	if len(request.Provenance) > 64<<10 || (len(request.Provenance) > 0 && !json.Valid(request.Provenance)) {
		return Binding{}, Bundle{}, fmt.Errorf("deployment provenance must be bounded valid JSON")
	}
	var object map[string]json.RawMessage
	if len(request.Inputs) > 1<<20 || json.Unmarshal(request.Inputs, &object) != nil || object == nil {
		return Binding{}, Bundle{}, fmt.Errorf("deployment inputs must be a bounded JSON object")
	}
	return binding, a.bundles[request.Context.TenantID+"\x00"+binding.BundleID], nil
}
func (a *Anvil) checkExecutable() error {
	verify := func(path, want string) error {
		info, e := os.Lstat(path)
		if e != nil {
			return e
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("runtime executable/script must be a regular pinned file")
		}
		if info.Size() > 512<<20 {
			return fmt.Errorf("runtime file exceeds bound")
		}
		data, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		if digest(data) != want {
			return fmt.Errorf("pinned runtime file changed")
		}
		return nil
	}
	if e := verify(a.cfg.Executable, a.cfg.ExecutableSHA256); e != nil {
		return e
	}
	for _, file := range a.cfg.PrefixArgs {
		if e := verify(file, a.cfg.PrefixSHA256[file]); e != nil {
			return e
		}
	}
	return nil
}
func (a *Anvil) command(ctx context.Context, dir string, args []string, extraEnv ...string) (ProcessResult, error) {
	if e := a.checkExecutable(); e != nil {
		return ProcessResult{}, e
	}
	bounded, cancel := context.WithTimeout(ctx, a.cfg.Timeout)
	defer cancel()
	env := []string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=" + dir, "TMPDIR=" + dir}
	env = append(env, a.cfg.Environment...)
	env = append(env, extraEnv...)
	return a.runner.Run(bounded, Command{a.cfg.Executable, append(append([]string(nil), a.cfg.PrefixArgs...), args...), env, dir, a.cfg.MaxOutputBytes})
}
func (a *Anvil) build(ctx context.Context, dir string) error {
	r, e := a.command(ctx, dir, []string{"--version", "--json"})
	if e != nil || r.ExitCode != 0 {
		return fmt.Errorf("Anvil build identity unavailable")
	}
	var identity struct{ Name, Digest string }
	if json.Unmarshal(r.Stdout, &identity) != nil || identity.Name != "anvil" || identity.Digest != a.cfg.ExpectedBuildDigest {
		return fmt.Errorf("Anvil build identity differs from approved runtime")
	}
	return nil
}
func (a *Anvil) args(binding Binding, root, input string, request Request, dry bool) []string {
	if binding.PlanTarget != "" {
		if binding.PlanTarget == "cloud-run" {
			profile := binding.AuthProfile
			if profile == "" {
				profile = "prod"
			}
			return []string{"deploy", "cloud-run", root, "--env", profile}
		}
		return []string{"deploy", "kubernetes", root}
	}
	args := []string{"run", root}
	args = append(args, binding.Command...)
	args = append(args, "--input", input, "--json", "--mcp", "direct", "--no-retries", "--confirm", "--idempotency-key", upstreamIdempotency(request))
	if binding.AuthProfile != "" {
		args = append(args, "--auth-profile", binding.AuthProfile)
	}
	if dry {
		args = append(args, "--dry-run")
	}
	return args
}
func (a *Anvil) Prepare(ctx context.Context, request Request) (plan Plan, err error) {
	binding, bundle, e := a.lookup(request)
	if e != nil {
		return Plan{}, e
	}
	action := Action{request, "plan", a.cfg.ID, binding.OperationID}
	if e = a.authorize.Authorize(ctx, action); e != nil {
		return Plan{}, e
	}
	receipt, e := a.admit.Admit(ctx, action)
	if e != nil || receipt == nil {
		return Plan{}, fmt.Errorf("deployment admission denied")
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err != nil {
			_ = receipt.Abort(cleanup)
		} else if e := receipt.Complete(cleanup, true); e != nil {
			plan = Plan{}
			err = fmt.Errorf("admission completion failed: %w", e)
		}
	}()
	root, canonical, full, e := snapshot(bundle)
	if e != nil {
		return Plan{}, e
	}
	defer os.RemoveAll(root)
	if e = a.build(ctx, root); e != nil {
		return Plan{}, e
	}
	inputDir, e := os.MkdirTemp("", "aiplex-anvil-input-")
	if e != nil {
		return Plan{}, e
	}
	defer os.RemoveAll(inputDir)
	input := filepath.Join(inputDir, "input.json")
	if e = os.WriteFile(input, request.Inputs, 0600); e != nil {
		return Plan{}, e
	}
	result, e := a.command(ctx, root, a.args(binding, root, input, request, true), bindingEnvironment(request, binding)...)
	if e != nil || result.ExitCode != 0 {
		return Plan{}, processError(result, e)
	}
	preview := json.RawMessage(nil)
	if binding.PlanTarget != "" {
		preview, _ = json.Marshal(map[string]any{"target": binding.PlanTarget, "instructions_digest": digest(result.Stdout), "instructions_bytes": len(result.Stdout), "scope": "plan-only; Anvil has not applied cloud resources"})
	} else {
		var native struct {
			Operation             string                     `json:"operation"`
			Method                string                     `json:"method"`
			URL                   string                     `json:"url"`
			Headers               map[string]json.RawMessage `json:"headers"`
			Body                  json.RawMessage            `json:"body"`
			IdempotencyKeyPresent bool                       `json:"idempotencyKeyPresent"`
			ConfirmationRequired  bool                       `json:"confirmationRequired"`
		}
		if json.Unmarshal(result.Stdout, &native) != nil || native.Operation != binding.OperationID || native.Method == "" || native.URL == "" {
			return Plan{}, fmt.Errorf("Anvil returned no matching native dry-run plan")
		}
		target, e := url.Parse(native.URL)
		if e != nil || target.Host == "" || target.User != nil || (target.Scheme != "https" && target.Scheme != "http") {
			return Plan{}, fmt.Errorf("Anvil returned invalid upstream URL")
		}
		headerNames := make([]string, 0, len(native.Headers))
		for name := range native.Headers {
			headerNames = append(headerNames, name)
		}
		sort.Strings(headerNames)
		// A public deployment plan never persists upstream header values, URL query
		// values or business payloads. The action remains bound to exact input bytes.
		preview, _ = json.Marshal(map[string]any{"operation": native.Operation, "method": native.Method, "target_origin": target.Scheme + "://" + target.Host, "header_names": headerNames, "header_values": "redacted", "payload_omitted": true, "payload_bytes": len(native.Body), "idempotency_key_present": native.IdempotencyKeyPresent, "confirmation_required": native.ConfirmationRequired})
	}

	now := a.now().UTC()
	plan = Plan{Version: "v1", ProviderID: a.cfg.ID, BindingID: binding.ID, TenantID: request.Context.TenantID, PrincipalID: request.Context.PrincipalID, ActorID: request.Context.ActorID, ResourceID: request.Context.ResourceID, OperationID: binding.OperationID, BundleDigest: canonical, SnapshotDigest: full, ToolBuildDigest: a.cfg.ExpectedBuildDigest, ExecutableDigest: a.cfg.ExecutableSHA256, ProvenanceDigest: digest(request.Provenance), InputDigest: digest(request.Inputs), IdempotencyKey: request.IdempotencyKey, Effect: binding.Effect, PlanOnly: binding.PlanTarget != "", State: Planned, CreatedAt: now, ExpiresAt: now.Add(15 * time.Minute), Preview: preview}
	plan.ConfigurationDigest = a.configurationDigest()
	plan.BindingDigest = bindingDigest(binding)
	plan.PreviewDigest = digest(plan.Preview)
	plan.ActionDigest = planDigest(plan)
	return plan, nil
}
func planDigest(p Plan) string {
	p.ActionDigest = ""
	p.Preview = nil
	b, _ := json.Marshal(p)
	return digest(b)
}
func (a *Anvil) Execute(ctx context.Context, request Request, plan Plan, approvalRef string) (Receipt, error) {
	if !a.cfg.AllowExecute {
		return Receipt{}, ErrDisabled
	}
	binding, bundle, e := a.lookup(request)
	if e != nil {
		return Receipt{}, e
	}
	if binding.PlanTarget != "" || plan.PlanOnly {
		return Receipt{}, fmt.Errorf("Anvil deployment-plan output is not an executable action")
	}
	now := a.now()
	if plan.Version != "v1" || plan.State != Planned || plan.ActionDigest != planDigest(plan) || plan.PreviewDigest != digest(plan.Preview) || plan.ConfigurationDigest != a.configurationDigest() || !plan.ExpiresAt.After(now) || plan.CreatedAt.After(now) || plan.ExpiresAt.Sub(plan.CreatedAt) > 15*time.Minute {
		return Receipt{}, fmt.Errorf("invalid or expired deployment plan")
	}
	if plan.ProviderID != a.cfg.ID || plan.BindingID != binding.ID || plan.TenantID != request.Context.TenantID || plan.PrincipalID != request.Context.PrincipalID || plan.ActorID != request.Context.ActorID || plan.BindingDigest != bindingDigest(binding) || plan.ResourceID != request.Context.ResourceID || plan.OperationID != binding.OperationID || plan.InputDigest != digest(request.Inputs) || plan.ProvenanceDigest != digest(request.Provenance) || plan.IdempotencyKey != request.IdempotencyKey || plan.ToolBuildDigest != a.cfg.ExpectedBuildDigest || plan.ExecutableDigest != a.cfg.ExecutableSHA256 || plan.Effect != binding.Effect {
		return Receipt{}, fmt.Errorf("approved action changed")
	}
	if approvalRef == "" {
		return Receipt{}, fmt.Errorf("host-verified approval receipt required")
	}
	if e = a.authorize.Authorize(ctx, Action{request, "execute", a.cfg.ID, binding.OperationID}); e != nil {
		return Receipt{}, e
	}
	if e = a.approvals.Verify(ctx, Approval{approvalRef, plan.TenantID, plan.PrincipalID, plan.ResourceID, plan.ActionDigest}); e != nil {
		return Receipt{}, e
	}
	root, canonical, full, e := snapshot(bundle)
	if e != nil {
		return Receipt{}, e
	}
	defer os.RemoveAll(root)
	if canonical != plan.BundleDigest || full != plan.SnapshotDigest {
		return Receipt{}, fmt.Errorf("approved snapshot changed")
	}
	if e = a.build(ctx, root); e != nil {
		return Receipt{}, e
	}
	admission, e := a.admit.Admit(ctx, Action{request, "execute", a.cfg.ID, binding.OperationID})
	if e != nil || admission == nil {
		return Receipt{}, fmt.Errorf("execution admission denied")
	}
	consumed := false
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if !consumed {
			_ = admission.Abort(cleanup)
		}
	}()
	inputDir, e := os.MkdirTemp("", "aiplex-anvil-input-")
	if e != nil {
		return Receipt{}, e
	}
	defer os.RemoveAll(inputDir)
	input := filepath.Join(inputDir, "input.json")
	if e = os.WriteFile(input, request.Inputs, 0600); e != nil {
		return Receipt{}, e
	}
	pending := Receipt{ProviderID: a.cfg.ID, TenantID: plan.TenantID, PrincipalID: plan.PrincipalID, ResourceID: plan.ResourceID, IdempotencyKey: request.IdempotencyKey, ActionDigest: plan.ActionDigest, State: Pending, Readiness: "unverified"}
	prior, fresh, e := a.journal.Begin(ctx, pending)
	if e != nil {
		return Receipt{}, e
	}
	if !fresh {
		if prior.ActionDigest != pending.ActionDigest || prior.TenantID != pending.TenantID || prior.ResourceID != pending.ResourceID || prior.PrincipalID != pending.PrincipalID {
			return Receipt{}, ErrConflict
		}
		return prior, nil
	}
	result, runErr := a.command(ctx, root, a.args(binding, root, input, request, false), bindingEnvironment(request, binding)...)
	pending.FinishedAt = a.now().UTC()
	pending.OutputDigest = digest(result.Stdout)
	pending.State = Unknown
	if !result.Started {
		pending.State = Refused
	}
	if runErr == nil && result.ExitCode == 0 && !result.Interrupted {
		var data any
		if json.Unmarshal(result.Stdout, &data) == nil {
			pending.State = Succeeded
		}
	}
	if pending.State != Succeeded {
		pending.ErrorCode = processCode(result)
		if pending.ErrorCode == "" {
			pending.ErrorCode = "execution_outcome_unknown"
		}
	}
	// Once a process has started, effects may have happened even if its response
	// is lost. Never release accounting or automatically retry an unknown outcome.
	consumed = result.Started
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	finishErr := a.journal.Finish(cleanup, pending)
	if consumed {
		if e := admission.Complete(cleanup, pending.State == Succeeded); e != nil {
			return pending, fmt.Errorf("admission settlement unknown: %w", e)
		}
	}
	if finishErr != nil {
		return pending, fmt.Errorf("deployment journal completion unknown: %w", finishErr)
	}
	if pending.State != Succeeded {
		return pending, processError(result, runErr)
	}
	return pending, nil
}
func processCode(result ProcessResult) string {
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(result.Stderr, &envelope) == nil && safeErrorCode(envelope.Error.Code) {
		return envelope.Error.Code
	}
	return ""
}
func processError(result ProcessResult, cause error) error {
	code := processCode(result)
	if code != "" {
		return fmt.Errorf("Anvil refused operation (%s, exit %d)", code, result.ExitCode)
	}
	if result.Interrupted {
		return fmt.Errorf("Anvil process interrupted; execution outcome may be unknown")
	}
	if cause != nil {
		return fmt.Errorf("Anvil command failed (exit %d)", result.ExitCode)
	}
	return fmt.Errorf("Anvil command failed (exit %d)", result.ExitCode)
}

// ConfigurationDigest binds the exact host-owned environment and runtime
// launcher to approval without disclosing credential values.
func (a *Anvil) configurationDigest() string {
	data, _ := json.Marshal(struct {
		ID, Executable, ExecutableDigest, BuildDigest string
		Prefix                                        []string
		PrefixDigests                                 map[string]string
		Environment                                   []string
	}{a.cfg.ID, a.cfg.Executable, a.cfg.ExecutableSHA256, a.cfg.ExpectedBuildDigest, a.cfg.PrefixArgs, a.cfg.PrefixSHA256, a.cfg.Environment})
	return digest(data)
}

// The child receives an exact principal directory generated from verified host
// context and binding-specific scopes; anonymous/every-scope defaults are avoided.
func bindingEnvironment(r Request, b Binding) []string {
	keyBytes, _ := json.Marshal([]string{r.Context.TenantID, r.Context.PrincipalID, r.Context.ActorID})
	key := digest(keyBytes)
	directory, _ := json.Marshal(map[string]any{key: map[string]any{"id": r.Context.PrincipalID, "scopes": b.Scopes}})
	return []string{"ANVIL_PRINCIPAL=" + key, "ANVIL_PRINCIPALS=" + string(directory)}
}
func upstreamIdempotency(r Request) string {
	data, _ := json.Marshal([]string{r.Context.TenantID, r.Context.PrincipalID, r.Context.ResourceID, r.BindingID, r.IdempotencyKey})
	return "aiplex-" + digest(data)
}

func bindingDigest(binding Binding) string { data, _ := json.Marshal(binding); return digest(data) }

func (a *Anvil) Bindings(tenant string) []BindingInfo {
	out := []BindingInfo{}
	for _, b := range a.bindings {
		if b.TenantID == tenant {
			out = append(out, BindingInfo{b.ID, b.ResourceID, b.OperationID, b.Effect, b.PlanTarget != ""})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
func (a *Anvil) Binding(tenant, id string) (BindingInfo, error) {
	b, ok := a.bindings[tenant+"\x00"+id]
	if !ok {
		return BindingInfo{}, fmt.Errorf("deployment binding not found")
	}
	return BindingInfo{b.ID, b.ResourceID, b.OperationID, b.Effect, b.PlanTarget != ""}, nil
}

func safeErrorCode(code string) bool {
	switch code {
	case "validation_error", "schema_mismatch", "confirmation_required", "idempotency_required", "auth_required", "permission_denied", "policy_denied", "unsafe_retry_blocked", "idempotency_ledger_unavailable", "unsupported_operation", "not_found", "conflict", "rate_limited", "upstream_timeout", "upstream_unavailable", "unknown_upstream_error":
		return true
	}
	return false
}
