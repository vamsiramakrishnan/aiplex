//go:build e2e

package e2e

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/devstack"
	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

// routedRecords reads the access point's ledger records, newest first.
func routedRecords(t *testing.T, e *env, ap string) []usageledger.Record {
	t.Helper()
	var page usageledger.Page
	var last error
	for i := 0; i < 40; i++ {
		r := e.do("GET", "/api/v1/usage/records?access_point_id="+ap+"&limit=200", "", e.auth()...)
		if r.Status == 200 {
			if last = json.Unmarshal([]byte(r.Body), &page); last == nil {
				return page.Records
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("read records: %v", last)
	return nil
}

// recordFor waits for the record of one request (by X-Request-Id order: the
// newest record whose first attempt matches).
func waitRecords(t *testing.T, e *env, ap string, n int) []usageledger.Record {
	t.Helper()
	var recs []usageledger.Record
	eventually(t, 10*time.Second, func() error {
		recs = routedRecords(t, e, ap)
		if len(recs) < n {
			return errString("waiting for records")
		}
		return nil
	})
	return recs
}

type errString string

func (e errString) Error() string { return string(e) }

func attemptOutcomes(r usageledger.Record) string {
	parts := []string{}
	for _, a := range r.Attempts {
		parts = append(parts, a.SurfaceRef+":"+a.Outcome)
	}
	return strings.Join(parts, ",")
}

// TestReliabilityAndRouting drives retry, fallback, breakers, lowest_cost,
// stateful requests, hooks and the response cache through the real binaries
// against two independent mock providers that fail on demand.
func TestReliabilityAndRouting(t *testing.T) {
	receiver := &otlpReceiver{}
	srv, receiverURL, err := devstack.Listen(receiver)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	e := start(t, withProviderSecrets, func(o *devstack.Options) {
		o.Env = append(o.Env, "OTEL_EXPORTER_OTLP_ENDPOINT="+receiverURL, "OTEL_BSP_SCHEDULE_DELAY=100", "OTEL_METRIC_EXPORT_INTERVAL=500")
	})
	a, b := e.stack.Models, e.stack.ModelsB
	models := "[fo, fo-fault-s503, rt-fault-s503-n1, st, st-fault-dropafter, ra-fault-s429-n1-ra1, cap-fault-s429-ra9, cap, br, br-fault-s503, lc-a, lc-b, ca, hk, hk-fault-s503]"
	for id, origin := range map[string]string{"ra": e.stack.ModelsURL, "rb": e.stack.ModelsBURL} {
		e.mustCLI("llm", "surfaces", "apply", "-f", writeFile(t, id+".yaml", `surface:
  id: `+id+`
  protocol: openai-chat
  origin: `+origin+`
  models: `+models+`
  operations: [compat.chat-completions]
  provider: {id: openai-compatible, usage_semantics: openai, stream_usage: inject}
auth: {type: none}
`))
		e.mustCLI("llm", "surfaces", "apply", "-f", writeFile(t, id+"-resp.yaml", `surface:
  id: `+id+`-resp
  protocol: openai-responses
  origin: `+origin+`
  models: [resp, resp-fault-s503]
  operations: [openai.v1.responses]
  provider: {id: openai}
auth: {type: bearer, secret_ref: openai-key}
`))
	}
	e.mustCLI("usage", "prices", "set", "-f", writeFile(t, "lc-b.json", `{"id":"lc-b","model_id":"lc-b","currency":"USD","scale":6,"meters":{"input_tokens":{"numerator":1,"denominator":1},"output_tokens":{"numerator":2,"denominator":1}}}`))
	e.mustCLI("access-points", "apply", "-f", writeFile(t, "routed.yaml", `id: routed
caller_kinds: [user, agent, client]
models:
  - name: fo
    backends: [{surface_ref: ra, native_model: fo-fault-s503}, {surface_ref: rb, native_model: fo}]
  - name: rt
    backends: [{surface_ref: ra, native_model: rt-fault-s503-n1}]
    routing: {retry: {base_backoff_ms: 10, max_backoff_ms: 20}}
  - name: st
    backends: [{surface_ref: ra, native_model: st-fault-dropafter}, {surface_ref: rb, native_model: st}]
    routing: {retry: {on: [connect_error, unavailable, bad_gateway]}, fallback: {on: [connect_error, unavailable, bad_gateway]}}
  - name: ra
    backends: [{surface_ref: ra, native_model: ra-fault-s429-n1-ra1}, {surface_ref: rb, native_model: fo}]
    routing: {retry: {max_retry_after_ms: 2000}}
  - name: cap
    backends: [{surface_ref: ra, native_model: cap-fault-s429-ra9}, {surface_ref: rb, native_model: cap}]
    routing: {retry: {max_retry_after_ms: 2000}}
  - name: resp
    backends: [{surface_ref: ra-resp, native_model: resp-fault-s503}, {surface_ref: rb-resp, native_model: resp}]
  - name: br
    backends: [{surface_ref: ra, native_model: br-fault-s503}, {surface_ref: rb, native_model: br}]
    routing: {breaker: {failure_threshold: 2, open_ms: 1500}}
  - name: lc
    backends: [{surface_ref: ra, native_model: lc-a}, {surface_ref: rb, native_model: lc-b}]
    routing: {strategy: lowest_cost}
  - name: ca
    backends: [{surface_ref: ra, native_model: ca}, {surface_ref: rb, native_model: ca}]
    cache: {ttl: 5m}
  - name: hk
    backends: [{surface_ref: ra, native_model: hk-fault-s503}, {surface_ref: rb, native_model: hk}]
`))
	chat := func(model, extra string, headers ...string) httpResp {
		t.Helper()
		return e.post("/ap/routed/v1/chat/completions", `{"model":"`+model+`",`+extra+`"messages":[{"role":"user","content":"hello routing"}]}`, headers...)
	}

	t.Run("fallback A to B with only the model rewritten", func(t *testing.T) {
		r := chat("fo", "")
		if r.Status != 200 || !strings.Contains(r.Body, "[b] Hello") || r.Header.Get("X-Aiplex-Attempts") != "2" {
			t.Fatalf("%d %s attempts=%q", r.Status, r.Body, r.Header.Get("X-Aiplex-Attempts"))
		}
		ra, rb := a.Requests(), b.Requests()
		if strings.Replace(ra[len(ra)-1].Body, "fo-fault-s503", "fo", 1) != rb[len(rb)-1].Body {
			t.Fatalf("bodies differ beyond the model:\n%s\n%s", ra[len(ra)-1].Body, rb[len(rb)-1].Body)
		}
		notContains(t, r.Body, "ra", "no backend name in the answer")
	})

	t.Run("retry then success; idempotency key never reaches a provider", func(t *testing.T) {
		before := len(a.Requests())
		r := chat("rt", "", "Idempotency-Key", "caller-key-1")
		if r.Status != 200 || len(a.Requests()) != before+2 {
			t.Fatalf("%d %s upstream=%d", r.Status, r.Body, len(a.Requests())-before)
		}
		for _, up := range a.Requests()[before:] {
			if up.IdempotencyKey != "" {
				t.Fatalf("an idempotency key reached the provider: %q", up.IdempotencyKey)
			}
		}
	})

	t.Run("no retry after the first streamed byte", func(t *testing.T) {
		beforeB := len(b.Requests())
		r := chat("st", `"stream":true,"stream_options":{"include_usage":true},`)
		if r.Status != 200 || !strings.Contains(r.Body, "partial") || len(b.Requests()) != beforeB {
			t.Fatalf("%d %q b=%d", r.Status, r.Body, len(b.Requests())-beforeB)
		}
	})

	t.Run("Retry-After honoured within the cap, not waited for above it", func(t *testing.T) {
		start := time.Now()
		r := chat("ra", "")
		if r.Status != 200 || strings.Contains(r.Body, "[b]") || time.Since(start) < 900*time.Millisecond {
			t.Fatalf("hint within cap: %d after %v %s", r.Status, time.Since(start), r.Body)
		}
		start = time.Now()
		r = chat("cap", "")
		if r.Status != 200 || !strings.Contains(r.Body, "[b]") || time.Since(start) > 5*time.Second {
			t.Fatalf("hint above cap falls over at once: %d after %v %s", r.Status, time.Since(start), r.Body)
		}
	})

	t.Run("a stateful request is not failed over", func(t *testing.T) {
		beforeB := len(b.Requests())
		r := e.post("/ap/routed/v1/responses", `{"model":"resp","input":"hello"}`)
		if r.Status != 503 || len(b.Requests()) != beforeB || !strings.Contains(r.Body, "mock fault 503") {
			t.Fatalf("stored response request: %d %s b=%d", r.Status, r.Body, len(b.Requests())-beforeB)
		}
	})

	t.Run("breaker opens, then half-opens", func(t *testing.T) {
		before := len(a.Requests())
		for i := 0; i < 3; i++ {
			if r := chat("br", ""); r.Status != 200 {
				t.Fatalf("%d %s", r.Status, r.Body)
			}
		}
		if got := len(a.Requests()) - before; got != 2 {
			t.Fatalf("the open breaker should skip a after 2 failures, a saw %d", got)
		}
		out := e.mustCLI("access-points", "routing", "routed").Out
		contains(t, out, "open", "routing shows the open breaker")
		contains(t, out, "unavailable(503)", "routing shows recent outcomes")
		time.Sleep(1600 * time.Millisecond)
		chat("br", "")
		if got := len(a.Requests()) - before; got != 3 {
			t.Fatalf("half-open probe reaches a: %d", got)
		}
	})

	t.Run("lowest_cost prefers the priced backend over an unknown price", func(t *testing.T) {
		r := chat("lc", "")
		if r.Status != 200 || !strings.Contains(r.Body, "[b]") {
			t.Fatalf("%d %s", r.Status, r.Body)
		}
	})

	t.Run("request hooks run once; both backends get the hooked body", func(t *testing.T) {
		setPolicy(t, e, "pii", e2eRequestPolicy)
		defer deletePolicy(t, e, "pii")
		r := e.post("/ap/routed/v1/chat/completions", `{"model":"hk","messages":[{"role":"user","content":"write to `+e2eEmail+`"}]}`)
		if r.Status != 200 {
			t.Fatalf("%d %s", r.Status, r.Body)
		}
		ra, rb := a.Requests(), b.Requests()
		lastA, lastB := ra[len(ra)-1].Body, rb[len(rb)-1].Body
		notContains(t, lastA+lastB, e2eMarker, "hooked body")
		if strings.Replace(lastA, "hk-fault-s503", "hk", 1) != lastB {
			t.Fatalf("hooked bodies differ beyond the model:\n%s\n%s", lastA, lastB)
		}
	})

	t.Run("cache hit, miss, bypass and principal isolation", func(t *testing.T) {
		alice, err := e.stack.MintCallerToken(devstack.DevTokenRequest{User: "alice", Role: "admin"})
		if err != nil {
			t.Fatal(err)
		}
		bob, err := e.stack.MintCallerToken(devstack.DevTokenRequest{User: "bob", Role: "admin"})
		if err != nil {
			t.Fatal(err)
		}
		as := func(token string, headers ...string) httpResp {
			return e.do("POST", "/ap/routed/v1/chat/completions", `{"model":"ca","messages":[{"role":"user","content":"cache me"}]}`, append([]string{"Authorization", "Bearer " + token, "Content-Type", "application/json"}, headers...)...)
		}
		before := len(a.Requests())
		first := as(alice.Token)
		second := as(alice.Token)
		if first.Header.Get("X-Aiplex-Cache") != "miss" || second.Header.Get("X-Aiplex-Cache") != "hit" || second.Body != first.Body || len(a.Requests()) != before+1 {
			t.Fatalf("miss then hit: %q %q upstream=%d", first.Header.Get("X-Aiplex-Cache"), second.Header.Get("X-Aiplex-Cache"), len(a.Requests())-before)
		}
		if r := as(bob.Token); r.Header.Get("X-Aiplex-Cache") != "miss" {
			t.Fatalf("bob must never get alice's cached answer: %q", r.Header.Get("X-Aiplex-Cache"))
		}
		if r := as(alice.Token, "Cache-Control", "no-store"); r.Header.Get("X-Aiplex-Cache") != "bypass" {
			t.Fatalf("no-store bypasses: %q", r.Header.Get("X-Aiplex-Cache"))
		}
		out := e.mustCLI("access-points", "purge-cache", "routed").Out
		contains(t, out, "Removed 2 cached answers", "purge")
	})

	t.Run("ledger and spans", func(t *testing.T) {
		recs := waitRecords(t, e, "routed", 12)
		var fallback, hit *usageledger.Record
		for i := range recs {
			r := &recs[i]
			if attemptOutcomes(*r) == "ra:unavailable,rb:ok" && fallback == nil && r.ModelID == "fo" {
				fallback = r
			}
			if r.Cache != nil && r.Cache.Status == "hit" {
				hit = r
			}
		}
		if fallback == nil || fallback.Backend == nil || fallback.Backend.SurfaceRef != "rb" || fallback.ResourceID != "rb" || fallback.RoutingStrategy != "priority" {
			t.Fatalf("fallback record missing or wrong: %+v", fallback)
		}
		if hit == nil || hit.Cost.Basis != "cached" || hit.CachedResponseUsage == nil || *hit.Usage.InputTokens != 0 {
			t.Fatalf("cache hit record: %+v", hit)
		}
		var routeSpan receivedSpan
		eventually(t, 10*time.Second, func() error {
			s, ok := receiver.byID(fallback.SpanID)
			if !ok {
				return errString("route span not exported yet")
			}
			routeSpan = s
			return nil
		})
		if !strings.HasPrefix(routeSpan.Name, "aiplex.route") || routeSpan.Attrs["aiplex.routing.attempts"] != "2" || routeSpan.Attrs["aiplex.routing.backend"] != "rb" {
			t.Fatalf("route span: %+v", routeSpan)
		}
		children := 0
		for _, s := range receiver.all() {
			if s.ParentID == routeSpan.SpanID && s.Attrs["aiplex.routing.outcome"] != "" {
				children++
			}
		}
		if children != 2 {
			t.Fatalf("one CLIENT span per attempt under the route span, got %d", children)
		}
		wide := e.mustCLI("usage", "records", "--access-point", "routed", "-o", "wide").Out
		contains(t, wide, "unavailable,ok", "usage -o wide attempts")
	})
}
