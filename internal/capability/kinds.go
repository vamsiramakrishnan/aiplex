package capability

// KindSpec describes the contract for a capability kind:
// which actions are allowed, default action, and standard constraint keys.
type KindSpec struct {
	Kind           Kind
	Actions        []string // e.g. ["call"] for tool, ["read","write","search","list","delete","subscribe"] for memory
	DefaultAction  string   // chosen when an invocation does not specify one
	ConstraintKeys []string // documented constraint keys for this kind
	Discovery      string   // RPC method or path used for discovery (informational)
}

// kinds is the registry. Adding a kind = appending here + handling in the
// capability resolver and (where applicable) the deploy engine.
var kinds = map[Kind]KindSpec{
	KindTool: {
		Kind:           KindTool,
		Actions:        []string{"call"},
		DefaultAction:  "call",
		ConstraintKeys: []string{"rate_per_min", "max_input_bytes"},
		Discovery:      "tools/list",
	},
	KindTask: {
		Kind:           KindTask,
		Actions:        []string{"invoke", "cancel"},
		DefaultAction:  "invoke",
		ConstraintKeys: []string{"max_concurrent", "priority_ceiling"},
		Discovery:      "tasks/list",
	},
	KindModel: {
		Kind:           KindModel,
		Actions:        []string{"complete", "embed"},
		DefaultAction:  "complete",
		ConstraintKeys: []string{"max_tokens_per_call", "monthly_token_budget", "temperature_max"},
		Discovery:      "models/list",
	},
	KindSkill: {
		Kind:           KindSkill,
		Actions:        []string{"invoke"},
		DefaultAction:  "invoke",
		ConstraintKeys: []string{"rate_per_min"},
		Discovery:      "skills/list",
	},
	KindMemory: {
		Kind:           KindMemory,
		Actions:        []string{"read", "write", "search", "list", "delete", "subscribe"},
		DefaultAction:  "read",
		ConstraintKeys: []string{"key_prefix", "max_value_bytes", "ttl_seconds_max", "tenant", "read_only"},
		Discovery:      "memory/describe",
	},
	KindAgent: {
		Kind:           KindAgent,
		Actions:        []string{"invoke", "cancel", "stream"},
		DefaultAction:  "invoke",
		ConstraintKeys: []string{"max_concurrent", "max_steps", "monthly_token_budget"},
		Discovery:      "agent/describe",
	},
	KindWorkflow: {
		Kind:           KindWorkflow,
		Actions:        []string{"run", "cancel", "describe"},
		DefaultAction:  "run",
		ConstraintKeys: []string{"max_concurrent_runs", "max_steps_per_run"},
		Discovery:      "workflow/describe",
	},
	KindMeta: {
		Kind:           KindMeta,
		Actions:        []string{"create", "read", "update", "delete", "list"},
		DefaultAction:  "read",
		ConstraintKeys: nil,
		Discovery:      "",
	},
}

// Spec returns the KindSpec for k. Ok=false if the kind is unknown.
func Spec(k Kind) (KindSpec, bool) {
	s, ok := kinds[k]
	return s, ok
}

// MustSpec returns the spec for k or panics. Use for known-good kinds.
func MustSpec(k Kind) KindSpec {
	s, ok := kinds[k]
	if !ok {
		panic("unknown capability kind: " + string(k))
	}
	return s
}

// IsAllowedAction reports whether action is valid for kind k.
func IsAllowedAction(k Kind, action string) bool {
	s, ok := kinds[k]
	if !ok {
		return false
	}
	for _, a := range s.Actions {
		if a == action {
			return true
		}
	}
	return false
}
