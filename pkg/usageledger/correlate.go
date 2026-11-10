package usageledger

import (
	"regexp"
	"strings"
)

// A reported record (harness telemetry) and a managed record (the access
// point exchange) can describe the same model request. Summaries must not add
// both. Records are matched only on an explicit key both sides carry, always
// within one tenant and one verified principal:
//
//   - trace: the managed record's caller_span_id (the span id in the
//     traceparent the harness sent) equals the reported event's own span id,
//     and the trace ids are equal;
//   - client_request_id: the harness's per-request id (Claude Code's
//     client_request_id, sent upstream as the X-Client-Request-Id header) on
//     both sides;
//   - request_id: both sides carry the same X-Request-Id the caller chose. The
//     server-generated form ("req-<number>") is unique only within one process
//     and is never used as a key.
//
// With no shared key the records are kept apart and are not guessed together
// by model, time or token counts.

// serverRequestID matches request ids the API generates itself.
var serverRequestID = regexp.MustCompile(`^req-[0-9]+$`)

// CorrelationKeys returns the keys under which a record can be matched. Keys
// embed the principal, so the same key from another principal never matches.
func CorrelationKeys(r Record) []string {
	if r.PrincipalID == "" {
		return nil
	}
	var keys []string
	add := func(kind string, parts ...string) {
		for _, p := range parts {
			if p == "" {
				return
			}
		}
		keys = append(keys, kind+"\x00"+r.PrincipalID+"\x00"+strings.Join(parts, "\x00"))
	}
	switch r.Source {
	case SourceManaged:
		add("trace", r.TraceID, r.CallerSpanID)
	case SourceReported:
		add("trace", r.TraceID, r.SpanID)
	}
	add("client", r.ClientRequestID)
	if r.RequestID != "" && !serverRequestID.MatchString(r.RequestID) {
		add("request", r.RequestID)
	}
	return keys
}

// ManagedKeys is the set of correlation keys of managed records.
type ManagedKeys map[string]struct{}

// AddManaged records a managed record's keys.
func (m ManagedKeys) AddManaged(r Record) {
	if r.Source != SourceManaged {
		return
	}
	for _, k := range CorrelationKeys(r) {
		m[k] = struct{}{}
	}
}

// Correlates reports whether a reported record shares a key with a managed one.
func (m ManagedKeys) Correlates(r Record) bool {
	if r.Source != SourceReported {
		return false
	}
	for _, k := range CorrelationKeys(r) {
		if _, ok := m[k]; ok {
			return true
		}
	}
	return false
}
