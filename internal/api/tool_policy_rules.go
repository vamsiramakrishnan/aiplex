package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/vamsiramakrishnan/aiplex/pkg/contracts"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
	"github.com/vamsiramakrishnan/aiplex/pkg/telemetry"
	"github.com/vamsiramakrishnan/aiplex/pkg/toolexec"
	"github.com/vamsiramakrishnan/aiplex/pkg/toolpolicy"
	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

// Policy rules on tool calls. The connection's tools.rules and the access
// point's tools[].rules are two independent rule sets; a call must pass both,
// so an access point can only add restrictions. Both run after the name
// lists, the caller's mcp:tools scope and read_only, and before quota, hooks
// and the upstream credential. See pkg/toolpolicy for the evaluation order.

// rulesFor compiles the connection's rules and the access point's rules for
// this connection. A compilation failure of saved rules (they were validated
// on save, so this means the stored record is damaged) is returned so the
// caller can fail closed.
func (h *ConnectedHandler) rulesFor(r *http.Request, connection ToolConnection) (conn, ap *toolpolicy.Set, apID string, err error) {
	if connection.Tools != nil {
		if conn, err = toolpolicy.CompileCached(connection.Tools.Rules); err != nil {
			return nil, nil, "", err
		}
	}
	if call := accessPointCallFrom(r.Context()); call != nil {
		apID = call.ID
		if ap, err = toolpolicy.CompileCached(call.toolRules[connection.ID]); err != nil {
			return nil, nil, "", err
		}
	}
	return conn, ap, apID, nil
}

// toolPolicyCaller derives the caller facts from the verified identity only.
func (h *ConnectedHandler) toolPolicyCaller(ctx context.Context, tenant string) (toolpolicy.Caller, error) {
	identity := GetWIFIdentity(ctx)
	if identity == nil || identity.PrincipalID == "" {
		return toolpolicy.Caller{}, errors.New("verified identity required")
	}
	caller := toolpolicy.Caller{Principal: identity.PrincipalID}
	if call := accessPointCallFrom(ctx); call != nil {
		caller.Kind, caller.Client = call.CallerKind, call.ClientID
	} else {
		resolved, err := h.resolveAccessPointCaller(ctx, tenant, identity)
		if err != nil {
			return toolpolicy.Caller{}, err
		}
		caller.Kind, caller.Client = resolved.Kind, resolved.ClientID
	}
	switch {
	case identity.ActorID != "":
		caller.Agent = identity.ActorID
	case !identity.IsWorkforce:
		caller.Agent = identity.PrincipalID
	}
	// Same rule as access point subjects and role bindings: a group view the
	// verifier recorded as missing or invalid is unknown, not empty.
	if e := identity.VerifiedEvidence; e == nil || (e.GroupsPresent && e.GroupsValid) {
		caller.GroupsKnown = true
		caller.Groups = append([]string(nil), identity.Groups...)
	}
	return caller, nil
}

func policyAnnotations(listed toolexec.ListedTool) toolpolicy.Annotations {
	return toolpolicy.Annotations{ReadOnly: listed.ReadOnlyHint, Destructive: listed.DestructiveHint, Idempotent: listed.IdempotentHint, OpenWorld: listed.OpenWorldHint}
}

func policyDenial(d toolpolicy.Decision) *toolexec.Denial {
	return &toolexec.Denial{Code: toolexec.RPCForbidden, Reason: d.Class, Message: d.Message, Rule: d.RuleID}
}

// installToolRules wires rule evaluation into the call path and tools/list.
// Rules are evaluated inside Authorize, after the checks governToolExecution
// installed. A call whose rules cannot be loaded, whose caller cannot be
// established or whose arguments cannot be evaluated is refused.
func (h *ConnectedHandler) installToolRules(r *http.Request, tenant string, record resources.Record, connection ToolConnection, opts *toolexec.Options, usage *usageCapture) {
	connRules, apRules, apID, rulesErr := h.rulesFor(r, connection)
	if rulesErr == nil && connRules.Len()+apRules.Len() == 0 {
		return
	}
	unavailable := func() error {
		usage.policyDecision(toolpolicy.ClassError, "")
		telemetry.Metrics().RecordToolPolicy(r.Context(), "error")
		return &toolexec.Denial{Code: toolexec.RPCForbidden, Reason: toolpolicy.ClassError, Message: "tool policy could not be evaluated; the call was refused"}
	}
	inner := opts.Authorize
	if rulesErr != nil {
		// Saved rules that no longer compile: refuse every call and every
		// listing rather than run without them.
		opts.Authorize = func(context.Context, contracts.RequestContext, toolexec.Connection, toolexec.Request) error {
			return unavailable()
		}
		if opts.List != nil {
			list := *opts.List
			list.Visible = func(context.Context, contracts.RequestContext, toolexec.ListedTool) bool { return false }
			opts.List = &list
		}
		return
	}
	opts.Authorize = func(ctx context.Context, trusted contracts.RequestContext, c toolexec.Connection, request toolexec.Request) error {
		if err := inner(ctx, trusted, c, request); err != nil {
			return err
		}
		caller, err := h.toolPolicyCaller(r.Context(), tenant)
		if err != nil {
			return unavailable()
		}
		facts := toolpolicy.Facts{ToolName: request.Name, ToolConnection: connection.ID, HasArgs: true, Caller: caller, AccessPointID: apID, Time: time.Now().UTC()}
		args, err := toolpolicy.ParseArgs(request.Arguments)
		if err != nil {
			return unavailable()
		}
		facts.Args = args
		facts.Annotations = h.policyAnnotationsFor(ctx, tenant, record, request.Name)
		for _, set := range []*toolpolicy.Set{connRules, apRules} {
			d := set.Evaluate(ctx, facts)
			telemetry.Metrics().RecordToolPolicy(ctx, decisionLabel(d))
			if !d.Allowed {
				usage.policyDecision(d.Class, d.RuleID)
				return policyDenial(d)
			}
		}
		return nil
	}
	if opts.List == nil {
		return
	}
	list := *opts.List
	visible := list.Visible
	var caller toolpolicy.Caller
	var callerErr error
	authorize := list.Authorize
	list.Authorize = func(ctx context.Context, trusted contracts.RequestContext, c toolexec.Connection) error {
		if err := authorize(ctx, trusted, c); err != nil {
			return err
		}
		caller, callerErr = h.toolPolicyCaller(r.Context(), tenant)
		return nil
	}
	list.Visible = func(ctx context.Context, trusted contracts.RequestContext, tool toolexec.ListedTool) bool {
		if !visible(ctx, trusted, tool) {
			return false
		}
		if callerErr != nil {
			return false
		}
		facts := toolpolicy.Facts{ToolName: tool.Name, ToolConnection: connection.ID, Annotations: policyAnnotations(tool), Caller: caller, AccessPointID: apID, Time: time.Now().UTC()}
		return !connRules.Hidden(ctx, facts) && !apRules.Hidden(ctx, facts)
	}
	opts.List = &list
}

func decisionLabel(d toolpolicy.Decision) string {
	switch {
	case d.Allowed:
		return "allow"
	case d.Class == toolpolicy.ClassError:
		return "error"
	}
	return "deny"
}

// policyAnnotationsFor returns the gateway-observed annotations of a tool.
// Missing, stale or other-revision observations are unknown (absent in the
// expression), exactly as for read_only.
func (h *ConnectedHandler) policyAnnotationsFor(ctx context.Context, tenant string, record resources.Record, name string) toolpolicy.Annotations {
	saved, err := h.Store.Get(ctx, tenant, toolAnnotationsKind, record.ID)
	if err != nil {
		return toolpolicy.Annotations{}
	}
	var annotations toolAnnotations
	if json.Unmarshal(saved.Spec, &annotations) != nil || annotations.ConnectionID != record.ID || annotations.Revision != record.Revision || !annotations.fresh(name, toolAnnotationClock()) {
		return toolpolicy.Annotations{}
	}
	pick := func(m map[string]bool) *bool {
		if v, ok := m[name]; ok {
			return &v
		}
		return nil
	}
	return toolpolicy.Annotations{ReadOnly: pick(annotations.ReadOnly), Destructive: pick(annotations.Destructive), Idempotent: pick(annotations.Idempotent), OpenWorld: pick(annotations.OpenWorld)}
}

// toolPolicySpanAttributes marks an exchange a policy rule refused: the class
// and the deciding rule identifier, never arguments.
func toolPolicySpanAttributes(record usageledger.Record) []attribute.KeyValue {
	if record.ErrorClass != toolpolicy.ClassDenied && record.ErrorClass != toolpolicy.ClassError {
		return nil
	}
	attrs := []attribute.KeyValue{attribute.String(telemetry.AttrToolPolicyDecision, record.ErrorClass)}
	if record.ToolPolicyRule != "" {
		attrs = append(attrs, attribute.String(telemetry.AttrToolPolicyRule, record.ToolPolicyRule))
	}
	return attrs
}

// policyDecision records a refusal by policy: the error class and the deciding
// rule identifier, never arguments or caller facts.
func (c *usageCapture) policyDecision(class, rule string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.errorCode = class
	c.record.ToolPolicyRule = rule
	c.mu.Unlock()
}
