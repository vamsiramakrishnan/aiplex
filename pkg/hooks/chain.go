package hooks

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"time"
	"unicode/utf8"

	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

// Decisions a hook can return.
const (
	Allow     = usageledger.HookAllow
	Deny      = usageledger.HookDeny
	Transform = usageledger.HookTransform
)

// Stable error codes of a refused exchange.
const (
	// CodeDenied is a hook decision: HTTP 403 in the caller's protocol.
	CodeDenied = "HOOK_DENIED"
	// CodeUnavailable is a configured hook that could not decide (timeout,
	// unreachable sidecar, invalid answer): HTTP 503, fail closed.
	CodeUnavailable = "HOOK_UNAVAILABLE"
	// CodeUnavailableConfig is a hook policy that could not be loaded.
	CodeUnavailableConfig = "HOOK_POLICY_UNAVAILABLE"
)

// Default public messages. They never contain request or response content.
const (
	MessageDenied        = "the request was refused by a content policy"
	MessageResponse      = "the response was withheld by a content policy"
	MessageUnavailable   = "a content policy check is unavailable; the request was refused"
	MessageUninspectable = "this request cannot be inspected by the configured content policy and was refused"
	MessageStreaming     = "streaming is not available under the configured content policy; send the request without streaming"
	MessageNonText       = "non-text content is not allowed by the configured content policy"
	MessageSession       = "WebSocket sessions are not available under the configured content policy"
)

// Meta is the normalized, non-content description of an exchange that hooks
// (and sidecars) receive.
type Meta struct {
	Plane         string `json:"plane"`
	Operation     string `json:"operation,omitempty"`
	Provider      string `json:"provider,omitempty"`
	Model         string `json:"model,omitempty"`
	Tool          string `json:"tool,omitempty"`
	CallerKind    string `json:"caller_kind,omitempty"`
	PrincipalID   string `json:"principal_id,omitempty"`
	AccessPointID string `json:"access_point_id,omitempty"`
}

// Input is what one hook run sees.
type Input struct {
	Meta
	Phase       string
	Parts       []Part
	Uninspected []Uninspected
}

// Verdict is one hook's decision.
type Verdict struct {
	Action string
	// Rule is the rule that denied (recorded, never shown to the caller).
	Rule string
	// Replacements are new texts by part index (Transform).
	Replacements map[int]string
	// Counts are matches per rule id.
	Counts map[string]int
}

// Runtime is what hooks need from the host.
type Runtime struct {
	// Client sends sidecar requests. It must enforce the outbound endpoint
	// policy; redirects are refused by the hook regardless.
	Client *http.Client
	// Secret resolves a secret reference to its value for sidecar auth.
	Secret func(ctx context.Context, ref string) (string, error)
}

// Outcome is the result of running a phase.
type Outcome struct {
	// Action is Allow, Deny or Transform. A fail-closed hook error is Deny
	// with Code CodeUnavailable.
	Action  string
	Status  int
	Code    string
	Message string
	// Body is the native body to send on (Transform), else nil.
	Body    []byte
	Results []usageledger.HookResult
	// Uninspected counts parts present but not inspected.
	Uninspected int
}

// Refused reports a deny outcome.
func (o Outcome) Refused() bool { return o.Action == Deny }

func deny(status int, code, message string) Outcome {
	return Outcome{Action: Deny, Status: status, Code: code, Message: message}
}

// Run runs the hooks of resolved that apply to phase over content. A nil
// content with extractErr set means the body could not be inspected.
func Run(ctx context.Context, rt Runtime, resolved Resolved, meta Meta, phase string, content *Content, extractErr error) Outcome {
	var hooks []ResolvedHook
	for _, h := range resolved.Hooks {
		if h.runsIn(phase) {
			hooks = append(hooks, h)
		}
	}
	if len(hooks) == 0 {
		return Outcome{Action: Allow}
	}
	if content == nil {
		out := Outcome{Action: Allow}
		for _, h := range hooks {
			result := usageledger.HookResult{ID: h.Name(), Type: h.Type, Phase: phase, Decision: usageledger.HookSkipped, Rules: []usageledger.HookRuleCount{{ID: "uninspectable", Count: 1}}}
			if h.OnUninspectable != "allow" {
				result.Decision = Deny
				out.Results = append(out.Results, result)
				refused := deny(http.StatusForbidden, CodeDenied, MessageUninspectable)
				refused.Results = out.Results
				return refused
			}
			out.Results = append(out.Results, result)
		}
		return out
	}
	out := Outcome{Action: Allow, Uninspected: len(content.Uninspected)}
	if len(content.Uninspected) > 0 && resolved.NonText == "deny" {
		refused := deny(http.StatusForbidden, CodeDenied, MessageNonText)
		refused.Uninspected = out.Uninspected
		refused.Results = []usageledger.HookResult{{ID: "policy/non_text", Type: "policy", Phase: phase, Decision: Deny, Rules: []usageledger.HookRuleCount{{ID: "non_text", Count: len(content.Uninspected)}}}}
		return refused
	}
	texts := content.Texts()
	for _, h := range hooks {
		started := time.Now()
		input := Input{Meta: meta, Phase: phase, Uninspected: content.Uninspected}
		for i, p := range content.Parts {
			p.Text = texts[i]
			input.Parts = append(input.Parts, p)
		}
		verdict, err := runHook(ctx, rt, h, input)
		result := usageledger.HookResult{ID: h.Name(), Type: h.Type, Phase: phase, DurationMS: time.Since(started).Milliseconds(), Rules: ruleCounts(verdict.Counts)}
		if err != nil {
			result.Decision = usageledger.HookError
			if h.HTTP != nil && h.HTTP.FailOpen {
				result.FailOpenUsed = true
				out.Results = append(out.Results, result)
				continue
			}
			out.Results = append(out.Results, result)
			refused := deny(http.StatusServiceUnavailable, CodeUnavailable, MessageUnavailable)
			refused.Results, refused.Uninspected = out.Results, out.Uninspected
			return refused
		}
		result.Decision = verdict.Action
		out.Results = append(out.Results, result)
		switch verdict.Action {
		case Deny:
			message := h.Message
			if message == "" {
				message = MessageDenied
				if phase == PhaseResponse {
					message = MessageResponse
				}
			}
			if verdict.Rule != "" {
				// The denying rule is listed first.
				rules := []usageledger.HookRuleCount{{ID: verdict.Rule, Count: 1}}
				for _, r := range result.Rules {
					if r.ID == verdict.Rule {
						rules[0].Count = r.Count
						continue
					}
					rules = append(rules, r)
				}
				out.Results[len(out.Results)-1].Rules = rules
			}
			refused := deny(http.StatusForbidden, CodeDenied, message)
			refused.Results, refused.Uninspected = out.Results, out.Uninspected
			return refused
		case Transform:
			for i, text := range verdict.Replacements {
				if i >= 0 && i < len(texts) {
					texts[i] = text
				}
			}
		}
	}
	changed := map[int]string{}
	for i, text := range texts {
		if text != content.Parts[i].Text {
			changed[i] = text
		}
	}
	if len(changed) > 0 {
		out.Action = Transform
		out.Body = content.Apply(changed)
	}
	return out
}

func ruleCounts(counts map[string]int) []usageledger.HookRuleCount {
	if len(counts) == 0 {
		return nil
	}
	out := make([]usageledger.HookRuleCount, 0, len(counts))
	for id, n := range counts {
		out = append(out, usageledger.HookRuleCount{ID: id, Count: n})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

var errTimeout = errors.New("hook timed out")

// runHook runs one hook within its timeout. A hook that does not answer in
// time is an error (fail closed unless the hook is fail_open).
func runHook(ctx context.Context, rt Runtime, h ResolvedHook, in Input) (Verdict, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(h.timeoutMS())*time.Millisecond)
	defer cancel()
	type answer struct {
		v   Verdict
		err error
	}
	done := make(chan answer, 1)
	go func() {
		v, err := dispatch(ctx, rt, h.Hook, in)
		done <- answer{v, err}
	}()
	select {
	case a := <-done:
		if a.err == nil {
			a.err = checkVerdict(a.v, len(in.Parts))
		}
		return a.v, a.err
	case <-ctx.Done():
		return Verdict{}, errTimeout
	}
}

func checkVerdict(v Verdict, parts int) error {
	switch v.Action {
	case Allow, Deny:
		return nil
	case Transform:
		for i := range v.Replacements {
			if i < 0 || i >= parts {
				return errors.New("transform names an unknown part")
			}
		}
		return nil
	}
	return errors.New("hook returned no decision")
}

func dispatch(ctx context.Context, rt Runtime, h Hook, in Input) (Verdict, error) {
	switch h.Type {
	case TypeRedact:
		return runRedact(h, in)
	case TypeSizeLimit:
		return h.SizeLimit.run(in), nil
	case TypeHTTP:
		return runHTTP(ctx, rt, h, in)
	}
	return Verdict{}, errors.New("unknown hook type")
}

func runRedact(h Hook, in Input) (Verdict, error) {
	rules, err := compileRedact(*h.Redact)
	if err != nil {
		return Verdict{}, err
	}
	total := 0
	for _, p := range in.Parts {
		total += len(p.Text)
	}
	if total > MaxInspectBytes {
		return Verdict{Action: Deny, Rule: "inspect_limit"}, nil
	}
	v := Verdict{Action: Allow, Counts: map[string]int{}, Replacements: map[int]string{}}
	for i, p := range in.Parts {
		masked, denied := redactText(rules, p.Text, v.Counts)
		if denied != "" && v.Rule == "" {
			v.Rule = denied
		}
		if masked != p.Text {
			v.Replacements[i] = masked
		}
	}
	switch {
	case v.Rule != "":
		v.Action, v.Replacements = Deny, nil
	case len(v.Replacements) > 0:
		v.Action = Transform
	}
	return v, nil
}

// SizeLimitConfig bounds the inspected input.
type SizeLimitConfig struct {
	// MaxChars bounds the total characters (Unicode code points) of all
	// text parts.
	MaxChars int `json:"max_chars,omitempty"`
	// MaxParts bounds the number of text parts plus uninspected parts.
	MaxParts int `json:"max_parts,omitempty"`
}

func (c SizeLimitConfig) validate() error {
	if c.MaxChars < 0 || c.MaxParts < 0 || (c.MaxChars == 0 && c.MaxParts == 0) {
		return errors.New("size_limit needs a positive max_chars or max_parts")
	}
	return nil
}

func (c *SizeLimitConfig) run(in Input) Verdict {
	if c.MaxParts > 0 && len(in.Parts)+len(in.Uninspected) > c.MaxParts {
		return Verdict{Action: Deny, Rule: "max_parts"}
	}
	if c.MaxChars > 0 {
		total := 0
		for _, p := range in.Parts {
			total += utf8.RuneCountInString(p.Text)
		}
		if total > c.MaxChars {
			return Verdict{Action: Deny, Rule: "max_chars"}
		}
	}
	return Verdict{Action: Allow}
}
