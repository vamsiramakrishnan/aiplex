package otlpingest

import (
	"strconv"

	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

// The mappings below were written from the harnesses' published documentation
// (Claude Code monitoring-usage, Codex otel configuration and source,
// Gemini CLI telemetry reference), read on 2026-11-10. See
// docs/observability.md for which were exercised against a real client.
//
// Source of truth per harness: per-request log events. Metrics (cumulative or
// delta counters) and traces are accepted but never turned into records, so a
// request cannot be counted twice by also arriving as a counter or a span.

// claudeEvent maps Claude Code events. Documented attributes of
// claude_code.api_request: model, cost_usd, cost_usd_micros, duration_ms,
// input_tokens (excludes cache tokens), output_tokens, cache_read_tokens,
// cache_creation_tokens, request_id, client_request_id, speed, effort,
// query_source; common: session.id, prompt.id, event.sequence.
func claudeEvent(name string, a attrSet) (*event, string) {
	switch name {
	case "api_request":
		ev := &event{name: name, model: a.str("model"), status: usageledger.StatusOK, session: a.str("session.id"),
			sequence: a.str("event.sequence"), promptID: a.str("prompt.id"), requestID: a.str("request_id"), clientReq: a.str("client_request_id"),
			extra: map[string]string{"query_source": a.str("query_source"), "speed": a.str("speed"), "effort": a.str("effort")}}
		ev.latencyMS = optionalNonNeg(a, "duration_ms")
		in, inOK := a.integer("input_tokens")
		out, outOK := a.integer("output_tokens")
		cr, crOK := a.integer("cache_read_tokens")
		cc, ccOK := a.integer("cache_creation_tokens")
		var bad bool
		var inP, outP, crP, ccP *int64
		for _, c := range []struct {
			dst **int64
			n   int64
			ok  bool
		}{{&inP, in, inOK}, {&outP, out, outOK}, {&crP, cr, crOK}, {&ccP, cc, ccOK}} {
			p, valid := nonNeg(c.n, c.ok)
			bad = bad || !valid
			*c.dst = p
		}
		if bad {
			return nil, "negative token count"
		}
		u := &usageledger.Usage{OutputTokens: outP, CacheReadTokens: crP, CacheWriteTokens: ccP}
		// Claude Code's input_tokens excludes cache tokens; the ledger's is the
		// total input. Without both cache counters the total is unknown, not
		// input_tokens alone.
		u.InputTokens = sumCounters(inP, crP, ccP)
		u.TotalTokens = sumCounters(u.InputTokens, outP)
		if u.Known() {
			ev.usage = u
		}
		ev.cost = claudeCost(a)
		return ev, ""
	case "api_error":
		ev := &event{name: name, model: a.str("model"), status: usageledger.StatusError, errClass: "upstream_error", session: a.str("session.id"),
			sequence: a.str("event.sequence"), promptID: a.str("prompt.id"), requestID: a.str("request_id"), clientReq: a.str("client_request_id"),
			extra: map[string]string{"query_source": a.str("query_source"), "speed": a.str("speed"), "effort": a.str("effort")}}
		ev.latencyMS = optionalNonNeg(a, "duration_ms")
		if code, ok := a.integer("status_code"); ok && code >= 100 && code <= 599 {
			ev.http = int(code)
			if code == 429 {
				ev.errClass = "rate_limited"
			}
		}
		if attempt, ok := a.integer("attempt"); ok && attempt >= 0 && attempt < 1000 {
			ev.extra["attempt"] = strconv.FormatInt(attempt, 10)
		}
		return ev, ""
	}
	return nil, ""
}

func claudeCost(a attrSet) usageledger.Cost {
	if micros, ok := a.integer("cost_usd_micros"); ok {
		if c, valid := microCost(micros, "USD"); valid {
			return c
		}
		return usageledger.UnknownCost()
	}
	if usd, ok := a.float("cost_usd"); ok {
		if c, valid := floatCost(usd, "USD"); valid {
			return c
		}
	}
	return usageledger.UnknownCost()
}

// codexEvent maps Codex events. A response's token counts are on the
// codex.sse_event with event.kind response.completed: input_token_count,
// output_token_count, cached_token_count, cache_write_token_count,
// reasoning_token_count. Codex exports no cost.
func codexEvent(name string, a attrSet) (*event, string) {
	if name != "sse_event" || a.str("event.kind") != "response.completed" {
		return nil, ""
	}
	ev := &event{name: "response.completed", model: a.str("model"), status: usageledger.StatusOK, session: a.str("conversation.id", "conversation_id"),
		extra: map[string]string{"effort": a.str("model_reasoning_effort"), "service_tier": a.str("service_tier")}}
	in, inOK := a.integer("input_token_count")
	out, outOK := a.integer("output_token_count")
	cached, cachedOK := a.integer("cached_token_count")
	write, writeOK := a.integer("cache_write_token_count")
	reason, reasonOK := a.integer("reasoning_token_count")
	var bad bool
	counter := func(n int64, ok bool) *int64 {
		p, valid := nonNeg(n, ok)
		bad = bad || !valid
		return p
	}
	u := &usageledger.Usage{InputTokens: counter(in, inOK), OutputTokens: counter(out, outOK), CacheReadTokens: counter(cached, cachedOK),
		CacheWriteTokens: counter(write, writeOK), ReasoningTokens: counter(reason, reasonOK)}
	if bad {
		return nil, "negative token count"
	}
	// OpenAI input_tokens includes cached tokens, and output_tokens includes
	// reasoning tokens.
	u.TotalTokens = sumCounters(u.InputTokens, u.OutputTokens)
	if u.Known() {
		ev.usage = u
	}
	return ev, ""
}

// geminiEvent maps Gemini CLI events. gemini_cli.api_response carries model,
// status_code, duration_ms, input_token_count, output_token_count,
// cached_content_token_count, thoughts_token_count, tool_token_count and
// total_token_count; gemini_cli.api_error carries model_name, status_code and
// duration.
func geminiEvent(name string, a attrSet) (*event, string) {
	switch name {
	case "api_response":
		ev := &event{name: name, model: a.str("model"), status: usageledger.StatusOK, session: a.str("session.id"), promptID: a.str("prompt_id"),
			extra: map[string]string{}}
		ev.latencyMS = optionalNonNeg(a, "duration_ms")
		if code, ok := a.integer("status_code"); ok && code >= 100 && code <= 599 {
			ev.http = int(code)
			if code >= 400 {
				ev.status, ev.errClass = usageledger.StatusError, "upstream_error"
			}
		}
		in, inOK := a.integer("input_token_count")
		out, outOK := a.integer("output_token_count")
		cached, cachedOK := a.integer("cached_content_token_count")
		thoughts, thoughtsOK := a.integer("thoughts_token_count")
		total, totalOK := a.integer("total_token_count")
		var bad bool
		counter := func(n int64, ok bool) *int64 {
			p, valid := nonNeg(n, ok)
			bad = bad || !valid
			return p
		}
		u := &usageledger.Usage{InputTokens: counter(in, inOK), OutputTokens: counter(out, outOK), CacheReadTokens: counter(cached, cachedOK),
			ReasoningTokens: counter(thoughts, thoughtsOK), TotalTokens: counter(total, totalOK)}
		if bad {
			return nil, "negative token count"
		}
		if u.Known() {
			ev.usage = u
		}
		return ev, ""
	case "api_error":
		ev := &event{name: name, model: a.str("model_name", "model"), status: usageledger.StatusError, errClass: "upstream_error", session: a.str("session.id"), promptID: a.str("prompt_id"),
			extra: map[string]string{}}
		ev.latencyMS = optionalNonNeg(a, "duration", "duration_ms")
		if code, ok := a.integer("status_code"); ok && code >= 100 && code <= 599 {
			ev.http = int(code)
			if code == 429 {
				ev.errClass = "rate_limited"
			}
		}
		return ev, ""
	}
	return nil, ""
}

func optionalNonNeg(a attrSet, keys ...string) *int64 {
	if n, ok := a.integer(keys...); ok && n >= 0 {
		return &n
	}
	// Some harnesses export durations as fractional milliseconds.
	if f, ok := a.float(keys...); ok && f >= 0 && f < 1e12 {
		n := int64(f + 0.5)
		return &n
	}
	return nil
}
