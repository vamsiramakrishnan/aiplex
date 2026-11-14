package telemetry

import (
	"strings"
	"testing"
	"time"
)

func metricLines(text, prefix string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, prefix) {
			out = append(out, line)
		}
	}
	return out
}

// The unknown-attempt gauges report nothing before a complete scan, never a
// zero; an exposure that could not be computed is labelled incomplete and
// its uncomputed attempts are counted; no tenant or attempt identifier is a
// label.
func TestQuotaUnknownGaugesKeepUnknownsVisible(t *testing.T) {
	tel := setup(t, nil)
	ready := false
	snapshot := QuotaUnknown{
		Attempts: 3, OldestAge: 90 * time.Minute, OldestAgeKnown: true,
		Exposure: []QuotaExposure{
			{Meter: "tokens", Unit: "tokens", KnownPart: 30, KnownAttempts: 2, Complete: true},
			{Meter: "usd", Unit: "money", Currency: "USD", Scale: 6, KnownPart: 150, KnownAttempts: 1, Uncomputed: 1},
			{Unit: "weird-unit", Uncomputed: 1},
		},
	}
	Metrics().ObserveQuotaUnknown(func() (QuotaUnknown, bool) { return snapshot, ready })
	if text := prometheusText(t, tel); strings.Contains(text, "aiplex_quota_unknown") {
		t.Fatalf("no scan yet must report nothing, not zero:\n%s", text)
	}
	ready = true
	text := prometheusText(t, tel)
	attempts := metricLines(text, "aiplex_quota_unknown_attempts")
	if len(attempts) != 1 || !strings.HasSuffix(attempts[0], " 3") {
		t.Fatalf("attempts %v\n%s", attempts, text)
	}
	age := metricLines(text, "aiplex_quota_unknown_oldest_age_seconds")
	if len(age) != 1 || !strings.HasSuffix(age[0], " 5400") {
		t.Fatalf("age %v\n%s", age, text)
	}
	exposure := strings.Join(metricLines(text, "aiplex_quota_unknown_exposure{"), "\n")
	for _, want := range []string{`aiplex_quota_meter="tokens"`, `aiplex_quota_exposure_complete="true"`, `aiplex_quota_currency="USD"`, `aiplex_quota_scale="6"`, `aiplex_quota_exposure_complete="false"`} {
		if !strings.Contains(exposure, want) {
			t.Errorf("exposure lacks %s:\n%s", want, exposure)
		}
	}
	// No known amount at all: no exposure series (not a 0), only the count
	// of uncomputed attempts.
	if strings.Contains(exposure, `aiplex_quota_meter="unknown"`) {
		t.Fatalf("an exposure with no known part was observed:\n%s", exposure)
	}
	uncomputed := strings.Join(metricLines(text, "aiplex_quota_unknown_exposure_uncomputed"), "\n")
	for _, want := range []string{`aiplex_quota_meter="usd"`, `aiplex_quota_meter="unknown"`, `aiplex_quota_unit="other"`, "} 1"} {
		if !strings.Contains(uncomputed, want) {
			t.Fatalf("uncomputed lacks %s: %s", want, uncomputed)
		}
	}
	if strings.Contains(text, `tenant="`) || strings.Contains(text, `tenant_id="`) {
		t.Fatal("a tenant label leaked into the metrics")
	}

	// An undated attempt makes the age unknown: it is not reported.
	snapshot = QuotaUnknown{Attempts: 1, Undated: 1}
	text = prometheusText(t, tel)
	if len(metricLines(text, "aiplex_quota_unknown_oldest_age")) != 0 || len(metricLines(text, "aiplex_quota_unknown_undated")) != 1 {
		t.Fatalf("undated:\n%s", text)
	}
	// The age of the figures is exported.
	snapshot = QuotaUnknown{Attempts: 1, ScanAge: 90 * time.Second}
	if lines := metricLines(prometheusText(t, tel), "aiplex_quota_unknown_scan_age_seconds"); len(lines) != 1 || !strings.HasSuffix(lines[0], " 90") {
		t.Fatalf("scan age %v", lines)
	}
	// Two entries that differ only by non-standard units share one label
	// set: one observation carries their sum, and incompleteness wins.
	snapshot = QuotaUnknown{Attempts: 2, Exposure: []QuotaExposure{
		{Meter: "media", Unit: "pages", KnownPart: 4, KnownAttempts: 1, Complete: true},
		{Meter: "media", Unit: "images", KnownPart: 6, KnownAttempts: 1, Uncomputed: 1},
	}}
	text = prometheusText(t, tel)
	media := metricLines(text, `aiplex_quota_unknown_exposure{`)
	if len(media) != 1 || !strings.Contains(media[0], `aiplex_quota_unit="other"`) || !strings.Contains(media[0], `aiplex_quota_exposure_complete="false"`) || !strings.HasSuffix(media[0], " 10") {
		t.Fatalf("collapsed labels must be summed into one observation: %v", media)
	}
	if lines := metricLines(text, "aiplex_quota_unknown_exposure_uncomputed"); len(lines) != 1 || !strings.HasSuffix(lines[0], " 1") {
		t.Fatalf("uncomputed %v", lines)
	}
	// A later source replaces the earlier one without a second registration.
	Metrics().ObserveQuotaUnknown(func() (QuotaUnknown, bool) { return QuotaUnknown{Attempts: 7}, true })
	if attempts := metricLines(prometheusText(t, tel), "aiplex_quota_unknown_attempts"); len(attempts) != 1 || !strings.HasSuffix(attempts[0], " 7") {
		t.Fatal(attempts)
	}
}
