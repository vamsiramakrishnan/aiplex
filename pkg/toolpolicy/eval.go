package toolpolicy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/common/ast"
)

// Error classes recorded in the ledger and shown as data.reason.
const (
	ClassDenied = "tool_policy_denied"
	ClassError  = "tool_policy_error"
)

// Reasons a call was refused, for the trace and the dry run. The ledger and
// the caller only ever see the class and the rule id.
const (
	ReasonDenyRule     = "deny_rule_matched"
	ReasonNoAllowRule  = "no_allow_rule_matched"
	ReasonEvalError    = "evaluation_error"
	ReasonArgsTooLarge = "arguments_too_large"
)

// Annotations are the MCP tool annotations the gateway has observed. A nil
// pointer is unknown, and an unknown annotation is absent from
// tool.annotations (so an unguarded access is an evaluation error).
type Annotations struct {
	ReadOnly, Destructive, Idempotent, OpenWorld *bool
}

// Caller is derived from the verified identity only.
type Caller struct {
	Kind      string
	Principal string
	// Groups are the verified group ids. They are visible to expressions only
	// when GroupsKnown: unknown membership is not empty membership, and
	// reading caller.groups when it is unknown is an evaluation error.
	Groups      []string
	GroupsKnown bool
	Agent       string
	Client      string
}

// Facts is everything an expression can see.
type Facts struct {
	ToolName       string
	ToolConnection string
	Annotations    Annotations
	// Args are the tool arguments. HasArgs is false when deciding for
	// tools/list, where rules that read args cannot be decided.
	Args            map[string]any
	HasArgs         bool
	Caller          Caller
	AccessPointID   string
	AccessPointTags map[string]string
	Time            time.Time
}

// ParseArgs decodes params.arguments (a JSON object, or absent) for
// evaluation. Arguments over MaxArgsBytes are an error; the caller must deny.
func ParseArgs(raw json.RawMessage) (map[string]any, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return map[string]any{}, nil
	}
	if len(raw) > MaxArgsBytes {
		return nil, ErrArgsTooLarge
	}
	var args map[string]any
	if err := json.Unmarshal(raw, &args); err != nil || args == nil {
		return nil, fmt.Errorf("tool arguments must be a JSON object")
	}
	return args, nil
}

// ErrArgsTooLarge is returned by ParseArgs and Evaluate for oversize arguments.
var ErrArgsTooLarge = errors.New("tool arguments exceed the policy evaluation limit")

type compiledRule struct {
	rule     Rule
	program  cel.Program
	usesArgs bool
	usesTime bool
}

// Set is a compiled, ordered list of rules. The zero value (and a nil *Set)
// has no rules and permits everything.
type Set struct {
	rules    []compiledRule
	hasAllow bool
}

// Len is the number of rules.
func (s *Set) Len() int {
	if s == nil {
		return 0
	}
	return len(s.rules)
}

// Rules returns copies of the rules in order.
func (s *Set) Rules() []Rule {
	if s == nil {
		return nil
	}
	out := make([]Rule, len(s.rules))
	for i, r := range s.rules {
		out[i] = r.rule
	}
	return out
}

var (
	envOnce sync.Once
	envInst *cel.Env
	envErr  error
)

var topLevelFields = map[string]map[string]bool{
	"tool":         {"name": true, "connection": true, "annotations": true},
	"caller":       {"kind": true, "principal": true, "groups": true, "groups_known": true, "agent": true, "client": true},
	"access_point": {"id": true, "labels": true},
	"request":      {"time": true},
}

var annotationFields = map[string]bool{"read_only": true, "destructive": true, "idempotent": true, "open_world": true}

func environment() (*cel.Env, error) {
	envOnce.Do(func() {
		dynMap := cel.MapType(cel.StringType, cel.DynType)
		opts := []cel.EnvOption{
			cel.Variable("tool", dynMap),
			cel.Variable("args", dynMap),
			cel.Variable("caller", dynMap),
			cel.Variable("access_point", dynMap),
			cel.Variable("request", dynMap),
			cel.CrossTypeNumericComparisons(true),
			cel.ParserExpressionSizeLimit(MaxExpressionBytes),
			cel.ParserRecursionLimit(24),
		}
		opts = append(opts, functions()...)
		envInst, envErr = cel.NewEnv(opts...)
	})
	return envInst, envErr
}

// CompileError reports the rule whose expression was rejected.
type CompileError struct {
	RuleID string
	Err    error
}

func (e *CompileError) Error() string {
	if e.RuleID == "" {
		return e.Err.Error()
	}
	return "rule " + e.RuleID + ": " + e.Err.Error()
}
func (e *CompileError) Unwrap() error { return e.Err }

// Compile validates and compiles rules. It rejects anything that does not
// type-check, reads an undeclared field, is too large, or is not boolean.
func Compile(rules []Rule) (*Set, error) {
	if len(rules) > MaxRules {
		return nil, &CompileError{Err: fmt.Errorf("rules are bounded to %d", MaxRules)}
	}
	e, err := environment()
	if err != nil {
		return nil, &CompileError{Err: fmt.Errorf("policy environment unavailable")}
	}
	set := &Set{}
	seen := map[string]bool{}
	for _, r := range rules {
		if err := r.Validate(); err != nil {
			return nil, &CompileError{Err: err}
		}
		if seen[r.ID] {
			return nil, &CompileError{RuleID: r.ID, Err: fmt.Errorf("duplicate rule id")}
		}
		seen[r.ID] = true
		parsed, issues := e.Parse(r.When)
		if issues != nil && issues.Err() != nil {
			return nil, &CompileError{RuleID: r.ID, Err: firstLine(issues.Err())}
		}
		checked, issues := e.Check(parsed)
		if issues != nil && issues.Err() != nil {
			return nil, &CompileError{RuleID: r.ID, Err: firstLine(issues.Err())}
		}
		if t := checked.OutputType(); !t.IsExactType(cel.BoolType) {
			return nil, &CompileError{RuleID: r.ID, Err: fmt.Errorf("when must be a boolean expression, not %s (compare dynamic values, for example args.flag == true)", t)}
		}
		usesArgs, usesTime, err := inspect(checked)
		if err != nil {
			return nil, &CompileError{RuleID: r.ID, Err: err}
		}
		program, err := e.Program(checked, cel.CostLimit(MaxEvalCost), cel.InterruptCheckFrequency(64))
		if err != nil {
			return nil, &CompileError{RuleID: r.ID, Err: fmt.Errorf("expression cannot be planned")}
		}
		set.rules = append(set.rules, compiledRule{rule: r, program: program, usesArgs: usesArgs, usesTime: usesTime})
		set.hasAllow = set.hasAllow || r.Effect == Allow
	}
	return set, nil
}

func firstLine(err error) error {
	line, _, _ := strings.Cut(err.Error(), "\n")
	return errors.New(line)
}

// inspect enforces the AST size cap and the declared field names, and reports
// whether the expression reads args or the request time.
func inspect(checked *cel.Ast) (usesArgs, usesTime bool, err error) {
	root := checked.NativeRep().Expr()
	nodes := 0
	ast.PreOrderVisit(root, ast.NewExprVisitor(func(e ast.Expr) {
		nodes++
		switch e.Kind() {
		case ast.IdentKind:
			switch e.AsIdent() {
			case "args":
				usesArgs = true
			case "request":
				usesTime = true
			}
		case ast.CallKind:
			call := e.AsCall()
			if call.FunctionName() == "matches" {
				pattern := call.Args()
				if call.IsMemberFunction() && len(pattern) == 1 || !call.IsMemberFunction() && len(pattern) == 2 {
					literal := pattern[len(pattern)-1]
					text, isString := "", false
					if literal.Kind() == ast.LiteralKind {
						text, isString = literal.AsLiteral().Value().(string)
					}
					if (!isString || len(text) > MaxPatternBytes) && err == nil {
						err = fmt.Errorf("matches needs a string literal pattern of at most %d bytes", MaxPatternBytes)
					}
				}
			}
		case ast.SelectKind:
			sel := e.AsSelect()
			operand := sel.Operand()
			switch operand.Kind() {
			case ast.IdentKind:
				fields, declared := topLevelFields[operand.AsIdent()]
				if declared && !fields[sel.FieldName()] && err == nil {
					err = fmt.Errorf("%s has no field %q", operand.AsIdent(), sel.FieldName())
				}
			case ast.SelectKind:
				inner := operand.AsSelect()
				if inner.Operand().Kind() == ast.IdentKind && inner.Operand().AsIdent() == "tool" && inner.FieldName() == "annotations" && !annotationFields[sel.FieldName()] && err == nil {
					err = fmt.Errorf("tool.annotations has no field %q", sel.FieldName())
				}
			}
		}
	}))
	if err == nil && nodes > MaxASTNodes {
		err = fmt.Errorf("expression is too large (%d nodes, limit %d)", nodes, MaxASTNodes)
	}
	return usesArgs, usesTime, err
}

// RuleTrace is one rule's outcome, for the dry run.
type RuleTrace struct {
	ID     string `json:"id"`
	Effect Effect `json:"effect"`
	// Result is matched, not_matched, error, skipped (the rule needs args or
	// time and was not decidable) or not_evaluated (an earlier rule decided).
	Result string `json:"result"`
	// Error is the evaluator's message. Only the dry run returns it.
	Error string `json:"error,omitempty"`
}

// Decision is the outcome of evaluating a Set.
type Decision struct {
	Allowed bool
	// Class is ClassDenied or ClassError when refused.
	Class string
	// Reason is one of the Reason constants when refused.
	Reason string
	// RuleID is the rule that decided; empty for the default deny of a set
	// with allow rules and for oversize arguments.
	RuleID string
	// Message is safe to show the caller.
	Message string
	Trace   []RuleTrace
}

// Evaluate applies the set to a call. Order: every rule is evaluated in
// order; the first matching deny refuses; an evaluation error refuses; when
// the set has any allow rule and none matched, the call is refused. A nil or
// empty set allows.
func (s *Set) Evaluate(ctx context.Context, facts Facts) Decision {
	if s == nil || len(s.rules) == 0 {
		return Decision{Allowed: true}
	}
	if !facts.HasArgs {
		return Decision{Class: ClassError, Reason: ReasonEvalError, Message: "tool policy could not be evaluated; the call was refused"}
	}
	activation, ok := facts.activation()
	if !ok {
		return Decision{Class: ClassError, Reason: ReasonArgsTooLarge, Message: "tool policy could not be evaluated; the call was refused"}
	}
	d := Decision{Trace: make([]RuleTrace, 0, len(s.rules))}
	allowMatched := false
	for i, r := range s.rules {
		matched, err := r.run(ctx, activation)
		trace := RuleTrace{ID: r.rule.ID, Effect: r.rule.Effect, Result: "not_matched"}
		switch {
		case err != nil:
			trace.Result, trace.Error = "error", err.Error()
			d.Trace = append(d.Trace, trace)
			d.markRest(s.rules[i+1:])
			d.Class, d.Reason, d.RuleID = ClassError, ReasonEvalError, r.rule.ID
			d.Message = "tool policy rule " + r.rule.ID + " could not be evaluated; the call was refused"
			return d
		case matched:
			trace.Result = "matched"
		}
		d.Trace = append(d.Trace, trace)
		if matched && r.rule.Effect == Deny {
			d.markRest(s.rules[i+1:])
			d.Class, d.Reason, d.RuleID = ClassDenied, ReasonDenyRule, r.rule.ID
			d.Message = r.rule.Message
			if d.Message == "" {
				d.Message = "tool call denied by policy rule " + r.rule.ID
			}
			return d
		}
		if matched && r.rule.Effect == Allow {
			allowMatched = true
		}
	}
	if s.hasAllow && !allowMatched {
		d.Class, d.Reason = ClassDenied, ReasonNoAllowRule
		d.Message = "tool call is not permitted by any allow rule of this tool's policy"
		return d
	}
	d.Allowed = true
	return d
}

func (d *Decision) markRest(rest []compiledRule) {
	for _, r := range rest {
		d.Trace = append(d.Trace, RuleTrace{ID: r.rule.ID, Effect: r.rule.Effect, Result: "not_evaluated"})
	}
}

// Hidden reports whether every call to the tool would be refused, decided
// without arguments or the request time. Rules that read args or request.time
// cannot hide a tool: a deny rule among them might not match, and an allow
// rule among them might. Only rules decidable from the tool, its annotations,
// the caller and the access point count.
func (s *Set) Hidden(ctx context.Context, facts Facts) bool {
	if s == nil || len(s.rules) == 0 {
		return false
	}
	facts.Args, facts.HasArgs = map[string]any{}, true
	activation, _ := facts.activation()
	allowUndecidable, allowMatched := false, false
	for _, r := range s.rules {
		if r.usesArgs || r.usesTime {
			if r.rule.Effect == Allow {
				allowUndecidable = true
			}
			continue
		}
		matched, err := r.run(ctx, activation)
		if err != nil {
			return true // every call evaluates this rule and is refused
		}
		if matched && r.rule.Effect == Deny {
			return true
		}
		if matched {
			allowMatched = true
		}
	}
	return s.hasAllow && !allowMatched && !allowUndecidable
}

func (r compiledRule) run(ctx context.Context, activation map[string]any) (bool, error) {
	out, _, err := r.program.ContextEval(ctx, activation)
	if err != nil {
		return false, err
	}
	matched, ok := out.Value().(bool)
	if !ok {
		return false, fmt.Errorf("expression did not evaluate to a boolean")
	}
	return matched, nil
}

func (f Facts) activation() (map[string]any, bool) {
	annotations := map[string]any{}
	for name, value := range map[string]*bool{"read_only": f.Annotations.ReadOnly, "destructive": f.Annotations.Destructive, "idempotent": f.Annotations.Idempotent, "open_world": f.Annotations.OpenWorld} {
		if value != nil {
			annotations[name] = *value
		}
	}
	caller := map[string]any{"kind": f.Caller.Kind, "principal": f.Caller.Principal, "groups_known": f.Caller.GroupsKnown, "agent": f.Caller.Agent, "client": f.Caller.Client}
	if f.Caller.GroupsKnown {
		groups := make([]string, len(f.Caller.Groups))
		copy(groups, f.Caller.Groups)
		caller["groups"] = groups
	}
	labels := map[string]string{}
	for k, v := range f.AccessPointTags {
		labels[k] = v
	}
	args := f.Args
	if args == nil {
		args = map[string]any{}
	}
	if encoded, err := json.Marshal(args); err != nil || len(encoded) > MaxArgsBytes {
		return nil, false
	}
	return map[string]any{
		"tool":         map[string]any{"name": f.ToolName, "connection": f.ToolConnection, "annotations": annotations},
		"args":         args,
		"caller":       caller,
		"access_point": map[string]any{"id": f.AccessPointID, "labels": labels},
		"request":      map[string]any{"time": f.Time.UTC()},
	}, true
}
