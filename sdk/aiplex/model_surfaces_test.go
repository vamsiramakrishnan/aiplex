package aiplex_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

const surfaceSpecJSON = `{"surface":{"id":"oa","protocol":"openai-chat","origin":"https://api.openai.com","models":["gpt-x"],
"operations":[{"id":"openai.v1.chat-completions","method":"POST","path":"/v1/chat/completions","transport":"sse","action":"invoke","source_url":"https://x","future_op_field":1}],
"provider":{"id":"openai","stream_usage":"inject","organization":"org-1","future_provider_field":{"a":[1,2]}},
"websocket":{"metering":"token","max_session_seconds":600,"future_ws":"x"},
"future_surface_field":true},
"auth":{"type":"bearer","secret_ref":"openai-key","future_auth":"y"},
"capacity_policy":{"cooldown_enabled":true,"headroom_fraction":0.2,"min_weight":2,"future_cap":null},
"future_spec_field":"z"}`

func TestModelSurfaceSpecRoundTripKeepsUnknownFields(t *testing.T) {
	var spec aiplex.ModelSurfaceSpec
	if err := json.Unmarshal([]byte(surfaceSpecJSON), &spec); err != nil {
		t.Fatal(err)
	}
	p, ws, cp := spec.Surface.Provider, spec.Surface.WebSocket, spec.CapacityPolicy
	if p == nil || p.ID != "openai" || p.StreamUsage != "inject" || p.Organization != "org-1" ||
		ws == nil || ws.Metering != aiplex.WebSocketMeteringToken || ws.MaxSessionSeconds != 600 ||
		cp == nil || !cp.CooldownEnabled || cp.HeadroomFraction != 0.2 || cp.MinWeight != 2 ||
		spec.Auth == nil || spec.Auth.SecretRef != "openai-key" || len(spec.Surface.Operations) != 1 {
		t.Fatalf("typed fields not decoded: %+v", spec)
	}
	for name, extra := range map[string]map[string]json.RawMessage{"spec": spec.Extra, "surface": spec.Surface.Extra, "provider": p.Extra, "websocket": ws.Extra, "auth": spec.Auth.Extra, "capacity": cp.Extra} {
		if len(extra) != 1 {
			t.Errorf("%s extra = %v", name, extra)
		}
	}
	out, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	var want, got any
	json.Unmarshal([]byte(surfaceSpecJSON), &want)
	json.Unmarshal(out, &got)
	// Unknown operation fields are the server's canonical operation; they are
	// not preserved by the alias, everything else must match exactly.
	wantJSON, _ := json.Marshal(want)
	if !strings.Contains(string(out), `"future_provider_field":{"a":[1,2]}`) || !strings.Contains(string(out), `"future_cap":null`) || !strings.Contains(string(out), `"future_spec_field":"z"`) || !strings.Contains(string(out), `"future_ws":"x"`) || !strings.Contains(string(out), `"future_auth":"y"`) || !strings.Contains(string(out), `"future_surface_field":true`) {
		t.Fatalf("unknown fields lost: %s (input %s)", out, wantJSON)
	}
	// Known fields never get overridden by Extra.
	spec.Surface.Provider.Extra["id"] = json.RawMessage(`"evil"`)
	out, _ = json.Marshal(spec)
	if strings.Contains(string(out), "evil") {
		t.Fatal("Extra overrode a known field")
	}
}

func TestModelSurfaceSpecOmitsUnsetBlocks(t *testing.T) {
	out, _ := json.Marshal(aiplex.ModelSurfaceSpec{Surface: aiplex.ModelSurface{ID: "s", Protocol: "p", Origin: "https://o", Provider: &aiplex.ModelProvider{ID: "anthropic", AnthropicVersion: "2023-06-01"}}, IdentityRef: "x"})
	for _, absent := range []string{"websocket", "capacity_policy", "auth", "billing_project", "api_version"} {
		if strings.Contains(string(out), absent) {
			t.Errorf("%s present in %s", absent, out)
		}
	}
}

func TestModelSurfaceClientCalls(t *testing.T) {
	var puts int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/v1/llm/operations":
			if r.URL.Query().Get("provider") != "bedrock" {
				http.Error(w, "no", 400)
				return
			}
			w.Write([]byte(`{"id":"bedrock","protocols":["bedrock-converse"],"credential_schemes":["aws-sigv4","bearer"],"operations":[{"id":"bedrock.converse","method":"POST","path":"/model/{model}/converse","transport":"http","action":"invoke","source_url":"https://d"}]}`))
		case r.Method == "GET" && r.URL.Path == "/api/v1/llm/surfaces/oa":
			w.Write([]byte(`{"tenant_id":"t","kind":"model-surface","id":"oa","revision":4,"spec":` + surfaceSpecJSON + `}`))
		case r.Method == "PUT" && r.URL.Path == "/api/v1/llm/surfaces/oa":
			puts++
			if r.Header.Get("If-Match") == "3" {
				w.WriteHeader(409)
				w.Write([]byte(`{"error":{"code":"REVISION_CONFLICT","message":"changed"}}`))
				return
			}
			body, _ := readAll(r)
			if !strings.Contains(body, "future_spec_field") {
				t.Error("unknown field not sent back", body)
			}
			w.Write([]byte(`{"tenant_id":"t","kind":"model-surface","id":"oa","revision":5,"spec":` + body + `}`))
		case r.URL.Path == "/api/v1/llm/surfaces/oa/capacity":
			w.Write([]byte(`{"surface_id":"oa","observed":true,"scope":"replica-local last observation; advisory","limits":{"provider":"openai","observed_at":"2026-01-01T00:00:00Z","classification":"rate_limited","retry_after_ms":2000,"requests":{"limit":100,"remaining":0},"tokens":{"limit":1000}},"capacity_policy":{"enabled":true,"policy":{"cooldown_enabled":true,"max_cooldown_seconds":60,"default_weight":1,"signal_max_age_seconds":60},"cooldown":{"phase":"cooling","retry_after_ms":2000}}}`))
		case r.URL.Path == "/api/v1/llm/surfaces/idle/capacity":
			w.Write([]byte(`{"surface_id":"idle","observed":false,"scope":"s","capacity_policy":{"enabled":false}}`))
		case r.URL.Path == "/api/v1/llm/batches":
			if r.URL.Query().Get("state") != "open" || r.URL.Query().Get("limit") != "2" || r.URL.Query().Get("cursor") != "c0" || r.URL.Query().Get("surface_id") != "oa" {
				t.Error("batch query", r.URL.RawQuery)
			}
			w.Write([]byte(`{"batches":[{"id":"i1","surface_id":"oa","provider":"openai","batch_id":"b1","create_operation":"op","state":"open","ended":false,"expected":null},{"id":"i2","surface_id":"oa","provider":"openai","batch_id":"b2","create_operation":"op","state":"open","ended":true,"expected":0}],"next_cursor":"i2"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	c := aiplex.NewClient(server.URL)
	ctx := context.Background()

	d, err := c.ListModelOperations(ctx, "bedrock")
	if err != nil || d.ID != "bedrock" || len(d.Operations) != 1 || d.Protocols[0] != "bedrock-converse" {
		t.Fatal(d, err)
	}
	if _, err = c.ListModelOperations(ctx, ""); err == nil {
		t.Fatal("empty provider accepted")
	}
	rec, err := c.GetModelSurface(ctx, "oa")
	if err != nil || rec.Revision != 4 || rec.Spec.Surface.Provider.ID != "openai" {
		t.Fatal(rec, err)
	}
	saved, err := c.PutModelSurface(ctx, "oa", rec.Spec, rec.Revision)
	if err != nil || saved.Revision != 5 {
		t.Fatal(saved, err)
	}
	if _, err = c.PutModelSurface(ctx, "oa", rec.Spec, 3); !aiplex.IsRevisionConflict(err) {
		t.Fatal("stale revision not a conflict", err)
	}

	cap, err := c.GetSurfaceCapacity(ctx, "oa")
	if err != nil || !cap.Observed || cap.Limits == nil || *cap.Limits.RetryAfterMS != 2000 || *cap.Limits.Requests.Remaining != 0 || cap.Limits.Tokens.Remaining != nil || *cap.Limits.Tokens.Limit != 1000 ||
		cap.CapacityPolicy.Cooldown.Phase != aiplex.CooldownCooling || !cap.CapacityPolicy.Policy.CooldownEnabled {
		t.Fatalf("%+v %v", cap, err)
	}
	if cap.Limits.Tokens.Remaining != nil {
		t.Fatal("unreported remaining decoded as a value")
	}
	idle, err := c.GetSurfaceCapacity(ctx, "idle")
	if err != nil || idle.Observed || idle.Limits != nil || idle.CapacityPolicy.Enabled || idle.CapacityPolicy.Cooldown != nil {
		t.Fatalf("%+v %v", idle, err)
	}

	page, err := c.ListModelBatches(ctx, aiplex.ModelBatchQuery{State: "open", SurfaceID: "oa", Limit: 2, Cursor: "c0"})
	if err != nil || len(page.Batches) != 2 || page.NextCursor != "i2" {
		t.Fatal(page, err)
	}
	if page.Batches[0].Expected != nil || page.Batches[1].Expected == nil || *page.Batches[1].Expected != 0 {
		t.Fatal("null expected count must stay distinct from a reported zero")
	}
	if _, err = c.ListModelBatches(ctx, aiplex.ModelBatchQuery{State: "weird"}); err == nil {
		t.Fatal("bad state accepted")
	}
}

func readAll(r *http.Request) (string, error) {
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := r.Body.Read(buf)
		sb.Write(buf[:n])
		if err != nil {
			return sb.String(), nil
		}
	}
}

func TestLedgerRecordDecodesUnitsAndModalitiesWithNullVersusZero(t *testing.T) {
	const record = `{"id":"ul_1","tenant_id":"t","started_at":"2026-01-01T00:00:00Z","finished_at":"2026-01-01T00:00:01Z","latency_ms":1,"plane":"llmplex","status":"error","error_class":"capacity_shed","source":"managed","requests":1,"batch":true,"image_size":"1024x1024","image_quality":"high",
"usage":{"input_tokens":10,"output_tokens":null,"cache_read_tokens":null,"cache_write_tokens":0,"reasoning_tokens":null,"total_tokens":null,
"input_audio_tokens":0,"input_text_tokens":7,"output_image_tokens":4,"audio_input_milliseconds":0,"voice_session_milliseconds":90000,"generated_images":2},
"cost":{"basis":"unknown"},
"provider_limits":{"provider":"anthropic","observed_at":"2026-01-01T00:00:00Z","classification":"overloaded","requests":{"remaining":0}}}`
	var r aiplex.UsageLedgerRecord
	if err := json.Unmarshal([]byte(record), &r); err != nil {
		t.Fatal(err)
	}
	u := r.Usage
	if !r.Batch || r.ImageSize != "1024x1024" || r.ImageQuality != "high" || r.ErrorClass != "capacity_shed" || r.ProviderLimits == nil || r.ProviderLimits.Classification != "overloaded" {
		t.Fatalf("record fields: %+v", r)
	}
	if u.CacheWriteTokens == nil || *u.CacheWriteTokens != 0 || u.CacheReadTokens != nil {
		t.Fatal("cache write zero vs read null lost")
	}
	if got := u.UnitCounter(usageledger.CounterInputAudioTokens); got == nil || *got != 0 {
		t.Fatal("reported zero audio tokens decoded as unknown")
	}
	if u.UnitCounter(usageledger.CounterInputVideoTokens) != nil || u.UnitCounter(usageledger.CounterVideoOutputMilliseconds) != nil || u.UnitCounter(usageledger.CounterOutputTextTokens) != nil {
		t.Fatal("absent unit decoded as a value")
	}
	if got := u.UnitCounter(usageledger.CounterAudioInputMilliseconds); got == nil || *got != 0 {
		t.Fatal("reported zero audio milliseconds lost")
	}
	if got := u.UnitCounter(usageledger.CounterVoiceSessionMilliseconds); got == nil || *got != 90000 {
		t.Fatal("voice session milliseconds lost")
	}
	if got := u.UnitCounter(usageledger.CounterGeneratedImages); got == nil || *got != 2 {
		t.Fatal("generated images lost")
	}
	// Re-encoding keeps the unknown/zero distinction.
	out, _ := json.Marshal(r)
	if !strings.Contains(string(out), `"audio_input_milliseconds":0`) || strings.Contains(string(out), `"input_video_tokens"`) || strings.Contains(string(out), `"video_output_milliseconds"`) {
		t.Fatalf("re-encode: %s", out)
	}
}

func TestLedgerSummaryAndPriceDecodeNewFields(t *testing.T) {
	var s aiplex.UsageLedgerSummary
	err := json.Unmarshal([]byte(`{"group_by":[],"groups":[{"key":{},"records":3,"requests":3,"errors":0,"denied":0,"unknown_status":0,"tokens":{},"tokens_unknown":{},"unknown_usage_records":0,"cost":[],"unknown_cost_records":3,
"modality_tokens":{"input_audio_tokens":0,"input_text_tokens":50},"units":{"voice_session_milliseconds":120000,"generated_images":0},"unit_records":{"voice_session_milliseconds":2,"generated_images":1},"batch_records":1}],"records_scanned":3,"truncated":false}`), &s)
	if err != nil {
		t.Fatal(err)
	}
	g := s.Groups[0]
	if v, ok := g.ModalityTokens["input_audio_tokens"]; !ok || v != 0 {
		t.Fatal("reported zero modality total lost")
	}
	if _, ok := g.ModalityTokens["output_audio_tokens"]; ok {
		t.Fatal("unreported modality appeared")
	}
	if g.Units["voice_session_milliseconds"] != 120000 || g.UnitRecords["voice_session_milliseconds"] != 2 || g.BatchRecords != 1 {
		t.Fatalf("%+v", g)
	}
	if _, ok := g.Units["video_output_milliseconds"]; ok {
		t.Fatal("unreported unit appeared")
	}

	var p aiplex.ModelPrice
	if err = json.Unmarshal([]byte(`{"id":"p","model_id":"m","currency":"USD","scale":6,"meters":{"input_audio_tokens":{"numerator":3,"denominator":1000},"generated_images":{"numerator":40000,"denominator":1},"voice_session_milliseconds":{"numerator":1,"denominator":1000}},"batch":false,"image_size":"1024x1024","image_quality":"high"}`), &p); err != nil {
		t.Fatal(err)
	}
	if err = p.Validate(); err != nil {
		t.Fatal(err)
	}
	if p.Batch == nil || *p.Batch || p.ImageSize != "1024x1024" || p.ImageQuality != "high" {
		t.Fatalf("selectors: %+v", p)
	}
	var unset aiplex.ModelPrice
	json.Unmarshal([]byte(`{"id":"p","model_id":"m","currency":"USD","scale":0,"meters":{"requests":{"numerator":1,"denominator":1}}}`), &unset)
	if unset.Batch != nil {
		t.Fatal("absent batch selector must stay nil, distinct from false")
	}
}
