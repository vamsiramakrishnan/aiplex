package otlpingest

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"math/big"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	collogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"

	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

// Harness names, as stored in the record's harness field.
const (
	HarnessClaudeCode = "claude-code"
	HarnessCodex      = "codex"
	HarnessGeminiCLI  = "gemini-cli"
	// HarnessUnknown labels counters for events no harness claims.
	HarnessUnknown = "unknown"
)

// Sender is the verified caller. The caller of MapLogs fills it from the
// verified token only.
type Sender struct {
	TenantID         string
	PrincipalID      string
	PrincipalDisplay string
	ActorID          string
	ActorDisplay     string
	ClientID         string
	// CallerKind is agent, user or client, derived from the token.
	CallerKind string
}

// LogsResult is the outcome of mapping one logs export.
type LogsResult struct {
	// Records are the reported records, without cost estimates for events that
	// carried no cost. Their IDs are deterministic (see RecordID), so storing
	// a retried export again conflicts instead of duplicating.
	Records []usageledger.Record
	// Rejected counts records that were recognized but unusable, with up to a
	// few content-free reasons in Errors.
	Rejected int64
	Errors   []string
	// Unmapped counts log records not turned into a record: events with no
	// usage (prompts, tool results, ...) and events no harness claims. They are
	// counted by harness in UnmappedByHarness and are the candidates for
	// forwarding.
	Unmapped          int64
	UnmappedByHarness map[string]int64
	// Forward holds only the unmapped records, with their resource and scope
	// (nil when there are none).
	Forward *collogs.ExportLogsServiceRequest
}

// Limits of the retention window an event timestamp may fall in.
const (
	maxFuture = 10 * time.Minute
	maxErrors = 5
)

// MapLogs maps the log records of an export. now and retention bound event
// timestamps (a record from the future or older than the ledger keeps is
// rejected).
func MapLogs(req *collogs.ExportLogsServiceRequest, sender Sender, now time.Time, retention time.Duration) LogsResult {
	res := LogsResult{UnmappedByHarness: map[string]int64{}}
	if retention <= 0 {
		retention = usageledger.DefaultRetention
	}
	for _, rl := range req.GetResourceLogs() {
		resource := attrSet{resource: toMap(rl.GetResource().GetAttributes())}
		var forwardScopes []*logspb.ScopeLogs
		for _, sl := range rl.GetScopeLogs() {
			var forwardRecords []*logspb.LogRecord
			for _, lr := range sl.GetLogRecords() {
				attrs := attrSet{resource: resource.resource, record: toMap(lr.GetAttributes())}
				harness, event := classify(lr, attrs)
				var (
					rec    *usageledger.Record
					reason string
				)
				if harness != "" {
					rec, reason = mapEvent(harness, event, lr, attrs, sender, now, retention)
				}
				switch {
				case rec != nil:
					res.Records = append(res.Records, *rec)
				case reason != "":
					res.Rejected++
					if len(res.Errors) < maxErrors {
						res.Errors = append(res.Errors, reason)
					}
				default:
					res.Unmapped++
					label := harness
					if label == "" {
						label = HarnessUnknown
					}
					res.UnmappedByHarness[label]++
					forwardRecords = append(forwardRecords, lr)
				}
			}
			if len(forwardRecords) > 0 {
				forwardScopes = append(forwardScopes, &logspb.ScopeLogs{Scope: sl.GetScope(), SchemaUrl: sl.GetSchemaUrl(), LogRecords: forwardRecords})
			}
		}
		if len(forwardScopes) > 0 {
			if res.Forward == nil {
				res.Forward = &collogs.ExportLogsServiceRequest{}
			}
			rlOut := &logspb.ResourceLogs{Resource: rl.GetResource(), SchemaUrl: rl.GetSchemaUrl(), ScopeLogs: forwardScopes}
			res.Forward.ResourceLogs = append(res.Forward.ResourceLogs, rlOut)
		}
	}
	return res
}

// ---- attribute access ----

type attrSet struct {
	record, resource map[string]*commonpb.AnyValue
}

func toMap(kvs []*commonpb.KeyValue) map[string]*commonpb.AnyValue {
	if len(kvs) == 0 {
		return nil
	}
	m := make(map[string]*commonpb.AnyValue, len(kvs))
	for _, kv := range kvs {
		if kv != nil && kv.Key != "" {
			m[kv.Key] = kv.Value
		}
	}
	return m
}

// get returns the record-level value, falling back to the resource's.
func (a attrSet) get(keys ...string) *commonpb.AnyValue {
	for _, k := range keys {
		if v, ok := a.record[k]; ok && v != nil {
			return v
		}
	}
	for _, k := range keys {
		if v, ok := a.resource[k]; ok && v != nil {
			return v
		}
	}
	return nil
}

func (a attrSet) str(keys ...string) string {
	if v := a.get(keys...); v != nil {
		if s, ok := stringOf(v); ok {
			return s
		}
	}
	return ""
}

func (a attrSet) integer(keys ...string) (int64, bool) {
	if v := a.get(keys...); v != nil {
		return intOf(v)
	}
	return 0, false
}

func (a attrSet) float(keys ...string) (float64, bool) {
	if v := a.get(keys...); v != nil {
		return floatOf(v)
	}
	return 0, false
}

func stringOf(v *commonpb.AnyValue) (string, bool) {
	switch x := v.GetValue().(type) {
	case *commonpb.AnyValue_StringValue:
		return x.StringValue, true
	case *commonpb.AnyValue_IntValue:
		return strconv.FormatInt(x.IntValue, 10), true
	case *commonpb.AnyValue_DoubleValue:
		return strconv.FormatFloat(x.DoubleValue, 'f', -1, 64), true
	case *commonpb.AnyValue_BoolValue:
		return strconv.FormatBool(x.BoolValue), true
	}
	return "", false
}

// intOf reads a whole number from an int, an integral double or a numeric
// string (Claude Code exports some numbers as strings).
func intOf(v *commonpb.AnyValue) (int64, bool) {
	switch x := v.GetValue().(type) {
	case *commonpb.AnyValue_IntValue:
		return x.IntValue, true
	case *commonpb.AnyValue_DoubleValue:
		if x.DoubleValue == math.Trunc(x.DoubleValue) && math.Abs(x.DoubleValue) < 1<<53 {
			return int64(x.DoubleValue), true
		}
	case *commonpb.AnyValue_StringValue:
		n, err := strconv.ParseInt(strings.TrimSpace(x.StringValue), 10, 64)
		if err == nil {
			return n, true
		}
	}
	return 0, false
}

func floatOf(v *commonpb.AnyValue) (float64, bool) {
	switch x := v.GetValue().(type) {
	case *commonpb.AnyValue_DoubleValue:
		return x.DoubleValue, !math.IsNaN(x.DoubleValue) && !math.IsInf(x.DoubleValue, 0)
	case *commonpb.AnyValue_IntValue:
		return float64(x.IntValue), true
	case *commonpb.AnyValue_StringValue:
		f, err := strconv.ParseFloat(strings.TrimSpace(x.StringValue), 64)
		return f, err == nil && !math.IsNaN(f) && !math.IsInf(f, 0)
	}
	return 0, false
}

// ---- classification ----

// classify names the harness and the event (without the harness prefix).
func classify(lr *logspb.LogRecord, attrs attrSet) (harness, event string) {
	name := lr.GetEventName()
	if name == "" {
		if v, ok := attrs.record["event.name"]; ok {
			name, _ = stringOf(v)
		}
	}
	if name == "" {
		if s, ok := lr.GetBody().GetValue().(*commonpb.AnyValue_StringValue); ok && len(s.StringValue) <= 128 && !strings.ContainsAny(s.StringValue, " \n") {
			name = s.StringValue
		}
	}
	for _, h := range []struct{ prefix, harness string }{{"claude_code.", HarnessClaudeCode}, {"codex.", HarnessCodex}, {"gemini_cli.", HarnessGeminiCLI}} {
		if strings.HasPrefix(name, h.prefix) {
			return h.harness, strings.TrimPrefix(name, h.prefix)
		}
	}
	if name != "" && attrs.str("service.name") == HarnessClaudeCode {
		return HarnessClaudeCode, name
	}
	return "", name
}

// ---- event mapping ----

// event is what a harness event tells us, before it becomes a record.
type event struct {
	name      string
	model     string
	status    string
	errClass  string
	http      int
	usage     *usageledger.Usage
	cost      usageledger.Cost
	latencyMS *int64
	session   string
	sequence  string
	promptID  string
	requestID string
	clientReq string
	extra     map[string]string
}

// mapEvent returns a record for a usage-bearing event, (nil,"") for an
// event that is recognized but carries no usage or is unknown (unmapped), and
// (nil,reason) for a usage-bearing event that cannot be used.
func mapEvent(harness, name string, lr *logspb.LogRecord, attrs attrSet, sender Sender, now time.Time, retention time.Duration) (*usageledger.Record, string) {
	var ev *event
	var reason string
	switch harness {
	case HarnessClaudeCode:
		ev, reason = claudeEvent(name, attrs)
	case HarnessCodex:
		ev, reason = codexEvent(name, attrs)
	case HarnessGeminiCLI:
		ev, reason = geminiEvent(name, attrs)
	}
	if reason != "" {
		return nil, harness + " " + name + ": " + reason
	}
	if ev == nil {
		return nil, ""
	}
	at, ok := eventTime(lr, attrs)
	if !ok {
		return nil, harness + " " + name + ": no usable timestamp"
	}
	if at.After(now.Add(maxFuture)) || at.Before(now.Add(-retention)) {
		return nil, harness + " " + name + ": timestamp outside the ledger's window"
	}
	rec := usageledger.Record{
		TenantID: sender.TenantID, PrincipalID: sender.PrincipalID, PrincipalDisplay: sender.PrincipalDisplay,
		ActorID: sender.ActorID, ActorDisplay: sender.ActorDisplay, ClientID: sender.ClientID, CallerKind: sender.CallerKind,
		Plane: usageledger.PlaneLLM, Operation: harness + "." + name, ModelID: ev.model,
		Source: usageledger.SourceReported, Requests: 1,
		Status: ev.status, ErrorClass: ev.errClass, HTTPStatus: ev.http,
		Usage: ev.usage, Cost: ev.cost,
		Harness: harness, HarnessVersion: boundedText(attrs.str("service.version", "app.version"), 64),
		SessionID: boundedText(ev.session, usageledger.MaxTextLength), ClientRequestID: validRequestID(ev.clientReq),
		FinishedAt: at.UTC(), StartedAt: at.UTC(),
	}
	if rec.Cost.Basis == "" {
		rec.Cost = usageledger.UnknownCost()
	}
	if ev.latencyMS != nil && *ev.latencyMS >= 0 && *ev.latencyMS <= int64(7*24*time.Hour/time.Millisecond) {
		rec.LatencyMS = *ev.latencyMS
		rec.StartedAt = at.UTC().Add(-time.Duration(*ev.latencyMS) * time.Millisecond)
	}
	rec.TraceID, rec.SpanID = recordIDs(lr)
	rec.ReportedAttributes = reportedAttributes(ev, attrs)
	rec.ID = RecordID(sender.TenantID, sender.PrincipalID, harness, ev, at)
	if err := rec.Validate(); err != nil {
		return nil, harness + " " + name + ": record is not valid"
	}
	return &rec, ""
}

// RecordID is the deterministic identifier of a reported event: the hash of
// (tenant, sender principal, harness, session, event timestamp, event name,
// sequence, prompt id, request ids). The same event sent twice (an OTLP retry)
// therefore maps to the same record and the ledger rejects the second append.
func RecordID(tenant, principal, harness string, ev *event, at time.Time) string {
	h := sha256.New()
	for _, part := range []string{tenant, principal, harness, ev.session, strconv.FormatInt(at.UnixNano(), 10), ev.name, ev.sequence, ev.promptID, ev.requestID, ev.clientReq, ev.model} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return "ul_" + hex.EncodeToString(h.Sum(nil)[:16])
}

func eventTime(lr *logspb.LogRecord, attrs attrSet) (time.Time, bool) {
	switch {
	case lr.GetTimeUnixNano() > 0:
		return time.Unix(0, int64(lr.GetTimeUnixNano())), lr.GetTimeUnixNano() < math.MaxInt64
	case lr.GetObservedTimeUnixNano() > 0:
		return time.Unix(0, int64(lr.GetObservedTimeUnixNano())), lr.GetObservedTimeUnixNano() < math.MaxInt64
	}
	if s := attrs.str("event.timestamp"); s != "" {
		if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func recordIDs(lr *logspb.LogRecord) (trace, span string) {
	if t := lr.GetTraceId(); len(t) == 16 && !allZero(t) {
		trace = hex.EncodeToString(t)
		if s := lr.GetSpanId(); len(s) == 8 && !allZero(s) {
			span = hex.EncodeToString(s)
		}
	}
	return trace, span
}

func allZero(b []byte) bool {
	for _, c := range b {
		if c != 0 {
			return false
		}
	}
	return true
}

// reportedAttributes is the documented, bounded subset of sender labels kept
// on a record. Identity-like attributes (user.email, user.id, user.account_*,
// organization.id, prompts, tool inputs, error text, repository details) are
// never copied.
func reportedAttributes(ev *event, attrs attrSet) map[string]string {
	out := map[string]string{}
	put := func(key, value string) {
		if value = boundedText(value, usageledger.MaxLabelValueLength); value != "" && usageledger.ValidReportedAttributeKey(key) && len(out) < usageledger.MaxReportedAttributes {
			out[key] = value
		}
	}
	put("event", ev.name)
	put("prompt_id", ev.promptID)
	put("request_id", ev.requestID)
	for key, value := range ev.extra {
		put(key, value)
	}
	for _, m := range []struct{ from, to string }{{"app.entrypoint", "entrypoint"}, {"terminal.type", "terminal_type"}} {
		put(m.to, attrs.str(m.from))
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// boundedText returns s when it is short printable UTF-8, else "".
func boundedText(s string, max int) string {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > max || !utf8.ValidString(s) {
		return ""
	}
	for _, c := range s {
		if c < 0x20 || c == 0x7f {
			return ""
		}
	}
	return s
}

func validRequestID(s string) string {
	s = strings.TrimSpace(s)
	if len(s) == 0 || len(s) > 128 {
		return ""
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.' || c == ':') {
			return ""
		}
	}
	return s
}

// ---- cost ----

// floatCost builds a reported-basis cost from a decimal USD amount, exactly
// from its shortest decimal representation (at most 12 places; more are
// rounded up so a cost is never understated by truncation).
func floatCost(usd float64, currency string) (usageledger.Cost, bool) {
	if usd < 0 || math.IsNaN(usd) || math.IsInf(usd, 0) || usd > 1e9 {
		return usageledger.UnknownCost(), false
	}
	text := strconv.FormatFloat(usd, 'f', -1, 64)
	whole, frac := text, ""
	if i := strings.IndexByte(text, '.'); i >= 0 {
		whole, frac = text[:i], text[i+1:]
	}
	roundUp := false
	if len(frac) > 12 {
		roundUp = strings.Trim(frac[12:], "0") != ""
		frac = frac[:12]
	}
	digits := strings.TrimLeft(whole+frac, "0")
	if digits == "" {
		digits = "0"
	}
	amount, ok := new(big.Int).SetString(digits, 10)
	if !ok {
		return usageledger.UnknownCost(), false
	}
	if roundUp {
		amount.Add(amount, big.NewInt(1))
	}
	return usageledger.Cost{Basis: usageledger.BasisReported, Currency: currency, Amount: amount.String(), Scale: uint8(len(frac))}, true
}

func microCost(micros int64, currency string) (usageledger.Cost, bool) {
	if micros < 0 {
		return usageledger.UnknownCost(), false
	}
	return usageledger.Cost{Basis: usageledger.BasisReported, Currency: currency, Amount: strconv.FormatInt(micros, 10), Scale: 6}, true
}

// ---- token arithmetic ----

func nonNeg(n int64, ok bool) (*int64, bool) {
	if !ok {
		return nil, true
	}
	if n < 0 {
		return nil, false
	}
	return usageledger.Int64(n), true
}

func sumCounters(parts ...*int64) *int64 {
	var total int64
	for _, p := range parts {
		if p == nil || total > math.MaxInt64-*p {
			return nil
		}
		total += *p
	}
	return usageledger.Int64(total)
}
