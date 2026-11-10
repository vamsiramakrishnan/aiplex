package usageledger

import "strings"

// Server tools are provider-side tools the provider runs and bills per request
// (Anthropic web search, OpenAI built-in tool calls, Gemini grounding queries).
// A record holds the counts the provider reported, by tool kind. Kinds are
// lowercase identifiers; a kind the ledger has not seen before is kept under
// its own sanitized name, so an unrecognized tool never disappears.
const (
	// MaxServerToolKinds bounds the kinds one record carries.
	MaxServerToolKinds = 16
	maxServerToolKind  = 40
	// MeterServerToolPrefix prefixes the per-call price meter of a kind:
	// "server_tool.web_search".
	MeterServerToolPrefix = "server_tool."
)

// ValidServerToolKind accepts [a-z][a-z0-9_]{0,39}.
func ValidServerToolKind(kind string) bool {
	if kind == "" || len(kind) > maxServerToolKind || kind[0] < 'a' || kind[0] > 'z' {
		return false
	}
	for _, c := range kind {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '_' {
			return false
		}
	}
	return true
}

// ServerToolMeter returns the price meter name for a kind.
func ServerToolMeter(kind string) string { return MeterServerToolPrefix + kind }

func serverToolKindOfMeter(meter string) (string, bool) {
	kind, ok := strings.CutPrefix(meter, MeterServerToolPrefix)
	return kind, ok && ValidServerToolKind(kind)
}

func validateServerTools(tools map[string]int64) error {
	if len(tools) > MaxServerToolKinds {
		return invalid("server_tools holds more than %d kinds", MaxServerToolKinds)
	}
	for kind, n := range tools {
		if !ValidServerToolKind(kind) || n < 0 {
			return invalid("server_tools needs lowercase kinds and nonnegative counts")
		}
	}
	return nil
}

// ServerToolsFromCounters extracts server-tool request counts from normalized
// execution counters named "server_tool_<kind>_requests". Invalid kinds are
// dropped and the result is bounded; it returns nil when none were reported.
func ServerToolsFromCounters(counters map[string]int64) map[string]int64 {
	var out map[string]int64
	for name, n := range counters {
		kind, ok := strings.CutPrefix(name, "server_tool_")
		if !ok {
			continue
		}
		kind, ok = strings.CutSuffix(kind, "_requests")
		if !ok || !ValidServerToolKind(kind) || n < 0 {
			continue
		}
		if out == nil {
			out = map[string]int64{}
		}
		if len(out) >= MaxServerToolKinds {
			break
		}
		out[kind] = n
	}
	return out
}
