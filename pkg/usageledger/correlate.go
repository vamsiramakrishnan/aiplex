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
//   - client_request_id: the harness's per-request id (Claude Code's
//     client_request_id, sent upstream as X-Client-Request-Id) on both sides;
//   - trace and span: the managed record's caller_span_id (the span id in the
//     traceparent the harness sent) equals the reported event's own span id,
//     and the trace ids are equal;
//   - request_id: both sides carry the same X-Request-Id the caller chose. The
//     server-generated form ("req-<token>-<number>") is never used as a
//     key, and the API never takes an id of that form from a caller;
//   - trace only, as a fallback: the reported event is in a trace the caller
//     propagated (the managed record continued it) but its span is not the
//     request's. Claude Code's log events are an example: they carry the
//     interaction's trace id but not the LLM request span's id. A trace can hold
//     several requests, so each managed record absorbs at most one reported
//     record this way.
//
// With no shared key the records are kept apart and are not guessed together
// by model, time or token counts.

// serverRequestID matches request ids the API generates itself: req-<token>-<n>
// with a random 16-hex token per process, and the older req-<n>, which records
// written before the token was added still carry.
var serverRequestID = regexp.MustCompile(`^req-(?:[0-9a-f]{16}-)?[0-9]+$`)

// IsServerRequestID reports whether id has the form the API generates. Such an
// id is never a correlation key, and the API never accepts one from a caller.
func IsServerRequestID(id string) bool { return serverRequestID.MatchString(id) }

func correlationKey(kind, principal string, parts ...string) string {
	for _, p := range parts {
		if p == "" {
			return ""
		}
	}
	return kind + "\x00" + principal + "\x00" + strings.Join(parts, "\x00")
}

// exactKeys are keys that identify one request.
func exactKeys(r Record) []string {
	if r.PrincipalID == "" {
		return nil
	}
	var keys []string
	add := func(k string) {
		if k != "" {
			keys = append(keys, k)
		}
	}
	add(correlationKey("client", r.PrincipalID, r.ClientRequestID))
	switch r.Source {
	case SourceManaged:
		add(correlationKey("trace", r.PrincipalID, r.TraceID, r.CallerSpanID))
	case SourceReported:
		add(correlationKey("trace", r.PrincipalID, r.TraceID, r.SpanID))
	}
	if r.RequestID != "" && !serverRequestID.MatchString(r.RequestID) {
		add(correlationKey("request", r.PrincipalID, r.RequestID))
	}
	return keys
}

// traceKey is the weaker key shared by every request in one propagated trace.
func traceKey(r Record) string {
	if r.PrincipalID == "" {
		return ""
	}
	switch r.Source {
	case SourceManaged:
		// Only a trace the caller propagated says anything about the caller.
		if r.CallerSpanID == "" {
			return ""
		}
		return correlationKey("tracelevel", r.PrincipalID, r.TraceID)
	case SourceReported:
		return correlationKey("tracelevel", r.PrincipalID, r.TraceID)
	}
	return ""
}

// CorrelationKeys returns every key under which a record can be matched.
func CorrelationKeys(r Record) []string {
	keys := exactKeys(r)
	if k := traceKey(r); k != "" {
		keys = append(keys, k)
	}
	return keys
}

type managedEntry struct{ used bool }

// ManagedKeys indexes managed records by correlation key. The nil value
// correlates nothing.
type ManagedKeys struct {
	exact map[string]*managedEntry
	trace map[string][]*managedEntry
}

// NewManagedKeys returns an empty index.
func NewManagedKeys() *ManagedKeys {
	return &ManagedKeys{exact: map[string]*managedEntry{}, trace: map[string][]*managedEntry{}}
}

// AddManaged records a managed record's keys.
func (m *ManagedKeys) AddManaged(r Record) {
	if m == nil || r.Source != SourceManaged {
		return
	}
	entry := &managedEntry{}
	for _, k := range exactKeys(r) {
		if _, taken := m.exact[k]; !taken {
			m.exact[k] = entry
		}
	}
	if k := traceKey(r); k != "" {
		m.trace[k] = append(m.trace[k], entry)
	}
}

// Correlates reports whether a reported record describes a request that has a
// managed record. An exact key matches the record it names; the trace
// fallback uses up one not yet matched managed record of the trace.
func (m *ManagedKeys) Correlates(r Record) bool {
	if m == nil || r.Source != SourceReported {
		return false
	}
	for _, k := range exactKeys(r) {
		if entry, ok := m.exact[k]; ok {
			entry.used = true
			return true
		}
	}
	if k := traceKey(r); k != "" {
		for _, entry := range m.trace[k] {
			if !entry.used {
				entry.used = true
				return true
			}
		}
	}
	return false
}
