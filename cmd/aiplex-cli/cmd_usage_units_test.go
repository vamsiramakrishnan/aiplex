package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const unitRecords = `{"records":[
{"id":"ul_voice","tenant_id":"t","started_at":"2026-11-09T10:00:00Z","finished_at":"2026-11-09T10:01:30Z","latency_ms":90000,"plane":"llmplex","model_id":"rt-model","status":"ok","source":"managed","requests":1,"session":true,"batch":true,
 "usage":{"input_tokens":10,"output_tokens":null,"cache_read_tokens":null,"cache_write_tokens":0,"reasoning_tokens":null,"total_tokens":null,"input_audio_tokens":0,"input_text_tokens":7,"output_image_tokens":4,"voice_session_milliseconds":90000,"audio_input_milliseconds":0},
 "cost":{"basis":"unknown"}},
{"id":"ul_image","tenant_id":"t","started_at":"2026-11-09T09:00:00Z","finished_at":"2026-11-09T09:00:01Z","latency_ms":900,"plane":"llmplex","model_id":"img-model","status":"ok","source":"managed","requests":1,"image_size":"1024x1024","image_quality":"high",
 "usage":{"input_tokens":null,"output_tokens":null,"cache_read_tokens":null,"cache_write_tokens":null,"reasoning_tokens":null,"total_tokens":null,"generated_images":2},
 "cost":{"basis":"unknown"}},
{"id":"ul_shed","tenant_id":"t","started_at":"2026-11-09T08:00:00Z","finished_at":"2026-11-09T08:00:00Z","latency_ms":1,"plane":"llmplex","model_id":"m","status":"denied","error_class":"capacity_shed","http_status":429,"source":"managed","requests":1,"usage":null,"cost":{"basis":"unknown"},
 "provider_limits":{"provider":"anthropic","observed_at":"2026-11-09T07:59:59Z","classification":"overloaded","retry_after_ms":1500,"requests":{"remaining":0}}}
]}`

func unitsServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/usage/records":
			w.Write([]byte(unitRecords))
		case "/api/v1/usage/records/ul_voice", "/api/v1/usage/records/ul_shed":
			var page struct {
				Records []json.RawMessage `json:"records"`
			}
			json.Unmarshal([]byte(unitRecords), &page)
			if strings.HasSuffix(r.URL.Path, "ul_voice") {
				w.Write(page.Records[0])
			} else {
				w.Write(page.Records[2])
			}
		case "/api/v1/usage/summary":
			w.Write([]byte(`{"group_by":["model_id"],"groups":[
{"key":{"model_id":"rt-model"},"records":2,"requests":2,"errors":0,"denied":0,"unknown_status":0,"tokens":{"input_tokens":10,"cache_write_tokens":0},"tokens_unknown":{},"unknown_usage_records":0,"cost":[],"unknown_cost_records":2,"modality_tokens":{"input_audio_tokens":0,"input_text_tokens":7},"units":{"voice_session_milliseconds":150000,"generated_images":0},"unit_records":{"voice_session_milliseconds":2,"generated_images":1},"batch_records":1},
{"key":{"model_id":"plain"},"records":1,"requests":1,"errors":0,"denied":0,"unknown_status":0,"tokens":{},"tokens_unknown":{},"unknown_usage_records":1,"cost":[],"unknown_cost_records":1}],"records_scanned":3,"truncated":false}`))
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestUsageRecordsTableShowsUnitsAndModalitiesWithoutMixingWithTokens(t *testing.T) {
	server := unitsServer(t)
	defer server.Close()
	out, err := runUsage(t, server, "wide", "records")
	if err != nil {
		t.Fatal(err)
	}
	line := func(prefix string) string {
		for _, l := range strings.Split(out, "\n") {
			if strings.HasPrefix(l, prefix) {
				return l
			}
		}
		t.Fatalf("no %s row in\n%s", prefix, out)
		return ""
	}
	voice, image, shed := line("ul_voice"), line("ul_image"), line("ul_shed")
	if !strings.Contains(voice, "batch") || !strings.Contains(voice, "in text=7 audio=0 | out image=4") {
		t.Errorf("modality or batch flag: %q", voice)
	}
	// 90000 ms is a duration, the reported zero is zero, and neither is a token count.
	if !strings.Contains(voice, "voice-session 1m30s, audio-in 0s") && !strings.Contains(voice, "audio-in 0s, voice-session 1m30s") {
		t.Errorf("duration units: %q", voice)
	}
	if !strings.Contains(image, "2 images (1024x1024 high)") {
		t.Errorf("image count with size/quality: %q", image)
	}
	// The image record reported no tokens and no modality: unknown, not 0 and not listed.
	if strings.Contains(image, " 0 ") || strings.Count(image, "unknown") != 4 {
		t.Errorf("unreported counters must be unknown (in, out, cache-w, cost): %q", image)
	}
	if !strings.Contains(shed, "denied (capacity_shed)") {
		t.Errorf("capacity shed reason: %q", shed)
	}
	// A reported zero cache write is zero; the voice row's output is unknown.
	if !strings.Contains(voice, " 0 ") && !strings.Contains(voice, "  0  ") {
		t.Errorf("reported zero cache-write must show 0: %q", voice)
	}
}

func TestUsageGetShowsUnitsModalitiesCacheWriteAndLimits(t *testing.T) {
	server := unitsServer(t)
	defer server.Close()
	out, err := runUsage(t, server, "table", "get", "ul_voice")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"batch:", "true", "cache_write_tokens:", "input_text_tokens (subset):", "7",
		"input_video_tokens (subset):", "voice_session_milliseconds (unit):", "90000 ms (1m30s)",
		"video_output_milliseconds (unit):", "generated_images (unit):",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	row := func(name string) string {
		for _, l := range strings.Split(out, "\n") {
			if strings.HasPrefix(l, name) {
				return l
			}
		}
		return ""
	}
	if !strings.HasSuffix(strings.TrimSpace(row("input_video_tokens (subset):")), "unknown") || !strings.HasSuffix(strings.TrimSpace(row("video_output_milliseconds (unit):")), "unknown") {
		t.Errorf("unreported splits and units must be unknown:\n%s", out)
	}
	if !strings.HasSuffix(strings.TrimSpace(row("input_audio_tokens (subset):")), "0") || !strings.HasSuffix(strings.TrimSpace(row("cache_write_tokens:")), "0") {
		t.Errorf("reported zeros must stay zero:\n%s", out)
	}
	if !strings.Contains(out, "provider_limits:") || !strings.Contains(out, "none observed") {
		t.Errorf("record without limits must say none observed:\n%s", out)
	}
	shed, err := runUsage(t, server, "table", "get", "ul_shed")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"capacity_shed", "overloaded", "retry_after:", "1.5s", "remaining 0 of unknown", "tokens:", "not reported", "anthropic observed 2026-11-09T07:59:59Z"} {
		if !strings.Contains(shed, want) {
			t.Errorf("missing %q in\n%s", want, shed)
		}
	}
}

func TestUsageSummaryShowsUnitsAndModalitiesSeparatelyFromTokens(t *testing.T) {
	server := unitsServer(t)
	defer server.Close()
	out, err := runUsage(t, server, "wide", "summary", "--by", "model")
	if err != nil {
		t.Fatal(err)
	}
	var rt, plain string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "rt-model") {
			rt = l
		}
		if strings.HasPrefix(l, "plain") {
			plain = l
		}
	}
	for _, want := range []string{"voice-session 2m30s (2 rec)", "0 images (1 rec)", "in text=7 audio=0"} {
		if !strings.Contains(rt, want) {
			t.Errorf("missing %q in %q", want, rt)
		}
	}
	if strings.Contains(rt, "150000") {
		t.Errorf("milliseconds shown as a bare number: %q", rt)
	}
	// IN totals 10, the cache write total was reported as zero.
	if f := strings.Fields(rt); f[4] != "10" {
		t.Errorf("IN column: %q", rt)
	}
	if strings.Contains(plain, "images") || strings.Contains(plain, "voice") || strings.Count(plain, "unknown") < 3 {
		t.Errorf("group with no units must list none and show tokens unknown: %q", plain)
	}
	js, err := runUsage(t, server, "json", "summary")
	if err != nil || !strings.Contains(js, `"unit_records"`) || !strings.Contains(js, `"batch_records": 1`) {
		t.Fatal(js, err)
	}
}

func TestUsagePricesSetAcceptsUnitModalityMetersAndSelectors(t *testing.T) {
	var sent string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "PUT":
			b, _ := io.ReadAll(r.Body)
			sent = string(b)
			w.Write([]byte(`{"id":"img","revision":1,"spec":` + sent + `}`))
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/img"):
			w.WriteHeader(404)
			w.Write([]byte(`{"code":"NOT_FOUND","message":"resource not found"}`))
		case r.Method == "GET":
			w.Write([]byte(`[{"id":"img","revision":1,"spec":{"id":"img","model_id":"img-model","currency":"USD","scale":6,"batch":false,"image_size":"1024x1024","image_quality":"high","meters":{"generated_images":{"numerator":40000,"denominator":1},"input_audio_tokens":{"numerator":3,"denominator":1000},"voice_session_milliseconds":{"numerator":1,"denominator":1000}}}}]`))
		}
	}))
	defer server.Close()
	file := filepath.Join(t.TempDir(), "price.json")
	spec := `{"id":"img","model_id":"img-model","currency":"USD","scale":6,"batch":false,"image_size":"1024x1024","image_quality":"high","meters":{"generated_images":{"numerator":40000,"denominator":1},"input_audio_tokens":{"numerator":3,"denominator":1000},"voice_session_milliseconds":{"numerator":1,"denominator":1000},"video_output_milliseconds":{"numerator":1,"denominator":100}}}`
	if err := os.WriteFile(file, []byte(spec), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := runUsage(t, server, "json", "prices", "set", "-f", file); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"generated_images"`, `"input_audio_tokens"`, `"voice_session_milliseconds"`, `"video_output_milliseconds"`, `"batch":false`, `"image_size":"1024x1024"`, `"image_quality":"high"`} {
		if !strings.Contains(sent, want) {
			t.Errorf("%s not sent: %s", want, sent)
		}
	}
	// A typo'd selector is still refused by the strict file decoder.
	bad := filepath.Join(t.TempDir(), "bad.json")
	os.WriteFile(bad, []byte(strings.Replace(spec, "image_size", "image_sise", 1)), 0o600)
	if _, err := runUsage(t, server, "json", "prices", "set", "-f", bad); err == nil {
		t.Fatal("unknown price field accepted")
	}
	// An unknown meter is refused before any request.
	sent = ""
	badMeter := filepath.Join(t.TempDir(), "meter.json")
	os.WriteFile(badMeter, []byte(strings.Replace(spec, "generated_images", "generated_pictures", 1)), 0o600)
	if _, err := runUsage(t, server, "json", "prices", "set", "-f", badMeter); err == nil || sent != "" {
		t.Fatal("unknown meter reached the server", err)
	}
	out, err := runUsage(t, server, "wide", "prices", "list")
	if err != nil || !strings.Contains(out, "SELECTORS") || !strings.Contains(out, "batch=false image_size=1024x1024 image_quality=high") || !strings.Contains(out, "generated_images=40000/1") {
		t.Fatal(out, err)
	}
}
