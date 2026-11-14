package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const quotaUnknownPage = `{"kind":"attempts","records":[],"done":true,"as_of":"2026-11-09T14:00:00Z","older_than":"1h0m0s","unknown":{"as_of":"2026-11-09T14:00:00Z","attempts":2,"oldest_dispatch_at":"2026-11-09T12:00:00Z","exposure":[` +
	`{"meter_id":"tokens","unit":"tokens","quantity":30,"known_part":30,"uncomputed_attempts":0,"attempts":2},` +
	`{"meter_id":"usd","unit":"money","currency":"USD","scale":6,"quantity":null,"known_part":150,"uncomputed_attempts":1,"attempts":2}]}}`

func TestQuotaUnknownShowsCountAgeAndExposureWithUnknownKeptUnknown(t *testing.T) {
	var calls []spCall
	spServer(t, &calls, map[string]string{"GET /api/v1/quota/inventory": quotaUnknownPage})
	out, err := runSP(t, "", quotaCmd(), "unknown", "--older-than", "1h")
	if err != nil {
		t.Fatal(err, out)
	}
	for _, want := range []string{
		"Unknown attempts dispatched at least 1h0m0s ago: 2",
		"Oldest:   2h0m0s (dispatched 2026-11-09T12:00:00Z)",
		"tokens: 30 tokens",
		"usd: unknown (known part 150 USD minor units (scale 6); 1 attempt(s) not computable)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if len(calls) != 1 || !strings.Contains(calls[0].query, "older_than=1h0m0s") || !strings.Contains(calls[0].query, "kind=attempts") || !strings.Contains(calls[0].query, "limit=64") {
		t.Fatalf("%+v", calls)
	}

	output = "json"
	out, err = runSP(t, "", quotaCmd(), "unknown", "--older-than", "1h")
	output = "table"
	var report map[string]any
	if err != nil || json.Unmarshal([]byte(out), &report) != nil {
		t.Fatal(err, out)
	}
	exposure := report["unknown"].(map[string]any)["exposure"].([]any)
	usd := exposure[1].(map[string]any)
	if report["complete"] != false || report["oldest_age_seconds"] != float64(7200) || usd["quantity"] != nil || usd["meter_id"] != "usd" {
		t.Fatalf("json must keep the unknown amount null and say incomplete:\n%s", out)
	}
}

func TestQuotaUnknownWithNothingSaysNone(t *testing.T) {
	var calls []spCall
	spServer(t, &calls, map[string]string{"GET /api/v1/quota/inventory": `{"kind":"attempts","records":[],"done":true,"unknown":{"as_of":"2026-11-09T14:00:00Z","attempts":0,"exposure":[]}}`})
	out, err := runSP(t, "", quotaCmd(), "unknown")
	if err != nil || !strings.Contains(out, "dispatched at least 0s ago: 0") || !strings.Contains(out, "Oldest:   none") || strings.Contains(out, "Exposure") {
		t.Fatal(err, out)
	}
}

func TestQuotaOlderThanRefusalsUseUsageExitCodes(t *testing.T) {
	var calls []spCall
	spServer(t, &calls, map[string]string{"GET /api/v1/quota/inventory": quotaUnknownPage})
	for _, args := range [][]string{
		{"unknown", "--older-than", "soon"},
		{"unknown", "--older-than", "-1h"},
		{"inventory", "budgets", "--older-than", "1h"},
		{"inventory", "attempts", "--older-than", "-5m"},
	} {
		cmd := quotaCmd()
		_, err := runSP(t, "", cmd, args...)
		if err == nil {
			t.Fatalf("%v accepted", args)
		}
		if code := reportError(io.Discard, cmd, args, err); code != exitUsage {
			t.Fatalf("%v: exit %d, want %d (%v)", args, code, exitUsage, err)
		}
	}
	if len(calls) != 0 {
		t.Fatalf("refused flags reached the server: %+v", calls)
	}
	// The inventory page passes the filter through.
	if _, err := runSP(t, "", quotaCmd(), "inventory", "attempts", "--older-than", "30m", "--limit", "5"); err != nil || len(calls) != 1 || !strings.Contains(calls[0].query, "older_than=30m0s") {
		t.Fatal(err, calls)
	}

	// A server refusal of the filter (400) is a usage error too.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":"QUOTA_INVALID_REQUEST","message":"quota operation could not be completed"}`))
	}))
	defer server.Close()
	apiURL = server.URL
	cmd := quotaCmd()
	_, err := runSP(t, "", cmd, "unknown", "--older-than", "1h")
	if err == nil || reportError(io.Discard, cmd, []string{"unknown"}, err) != exitUsage {
		t.Fatal(err)
	}
}

// runQuotaStructured runs `quota <args>` with -o json and -o yaml and decodes
// both into generic values.
func runQuotaStructured(t *testing.T, args ...string) (any, any) {
	t.Helper()
	decoded := map[string]any{}
	for _, format := range []string{"json", "yaml"} {
		output = format
		out, err := runSP(t, "", quotaCmd(), args...)
		output = "table"
		if err != nil {
			t.Fatal(format, err, out)
		}
		var v any
		if format == "json" {
			err = json.Unmarshal([]byte(out), &v)
		} else {
			err = yaml.Unmarshal([]byte(out), &v)
		}
		if err != nil {
			t.Fatalf("%s: %v\n%s", format, err, out)
		}
		// Normalize number types (YAML decodes ints, JSON float64).
		raw, _ := json.Marshal(v)
		v = nil
		_ = json.Unmarshal(raw, &v)
		decoded[format] = v
	}
	return decoded["json"], decoded["yaml"]
}

func TestQuotaUnknownYAMLAndJSONCarryTheSameKeysAndValues(t *testing.T) {
	var calls []spCall
	spServer(t, &calls, map[string]string{"GET /api/v1/quota/inventory": quotaUnknownPage})
	for _, args := range [][]string{{"unknown", "--older-than", "1h"}, {"inventory", "attempts", "--older-than", "1h"}} {
		j, y := runQuotaStructured(t, args...)
		if !reflect.DeepEqual(j, y) {
			t.Fatalf("%v: yaml differs from json:\njson %v\nyaml %v", args, j, y)
		}
		raw, _ := json.Marshal(y)
		for _, key := range []string{`"known_part"`, `"uncomputed_attempts"`, `"meter_id"`} {
			if !strings.Contains(string(raw), key) {
				t.Errorf("%v: yaml lacks %s: %s", args, key, raw)
			}
		}
		if args[0] == "unknown" && !strings.Contains(string(raw), `"oldest_age_seconds":7200`) {
			t.Errorf("yaml lacks oldest_age_seconds: %s", raw)
		}
	}
}

func TestQuotaUnknownTruncatedScanIsLabelledInEveryFormat(t *testing.T) {
	// A page that never finishes and never advances: the scan stops and
	// says its figures are lower bounds.
	var calls []spCall
	spServer(t, &calls, map[string]string{"GET /api/v1/quota/inventory": strings.Replace(quotaUnknownPage, `"done":true`, `"done":false,"next_after_id":"same"`, 1)})
	out, err := runSP(t, "", quotaCmd(), "unknown")
	if err != nil || !strings.Contains(out, "Truncated:") || !strings.Contains(out, "at least") {
		t.Fatal(err, out)
	}
	j, y := runQuotaStructured(t, "unknown")
	for name, v := range map[string]any{"json": j, "yaml": y} {
		m := v.(map[string]any)
		if m["truncated"] != true || m["complete"] != false {
			t.Fatalf("%s: %v", name, m)
		}
	}
}
