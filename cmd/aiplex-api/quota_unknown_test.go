package main

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/vamsiramakrishnan/aiplex/internal/api"
	"github.com/vamsiramakrishnan/aiplex/internal/config"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
)

// pagedUnknownRecovery serves attempt pages from a script; each call returns
// the next scripted page.
type pagedUnknownRecovery struct {
	pages []unknownPage
	calls int
}
type unknownPage struct {
	report quotaplex.RecoveryReport
	err    error
}

func (p *pagedUnknownRecovery) RunOnce(context.Context, quotaplex.RecoveryPage) (quotaplex.RecoveryReport, error) {
	return quotaplex.RecoveryReport{Done: true}, nil
}
func (p *pagedUnknownRecovery) RecoverAttempts(context.Context, quotaplex.RecoveryPage) (quotaplex.RecoveryReport, error) {
	page := p.pages[p.calls%len(p.pages)]
	p.calls++
	return page.report, page.err
}
func (p *pagedUnknownRecovery) ReconcileLoans(context.Context, quotaplex.RecoveryPage) (quotaplex.RecoveryReport, error) {
	return quotaplex.RecoveryReport{Done: true}, nil
}

func unknownSummary(asOf time.Time, attempts int, oldest *time.Time, exposure ...quotaplex.HeldExposure) *quotaplex.UnknownExposure {
	u := &quotaplex.UnknownExposure{AsOf: asOf, Attempts: attempts, OldestDispatchAt: oldest, Exposure: []quotaplex.HeldExposure{}}
	u.Merge(quotaplex.UnknownExposure{Exposure: exposure})
	return u
}

func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	previous, level := log.Logger, zerolog.GlobalLevel()
	log.Logger = zerolog.New(&buf)
	zerolog.SetGlobalLevel(zerolog.DebugLevel)
	t.Cleanup(func() { log.Logger = previous; zerolog.SetGlobalLevel(level) })
	return &buf
}

func TestQuotaWorkerPublishesUnknownExposureOnlyAfterACompleteScan(t *testing.T) {
	logs := captureLog(t)
	now := time.Date(2026, 11, 9, 14, 0, 0, 0, time.UTC)
	old := now.Add(-3 * time.Hour)
	young := now.Add(-10 * time.Minute)
	tokens := quotaplex.HeldExposure{MeterID: "tokens", Unit: "tokens", KnownPart: 10, Attempts: 1}
	usdUnknown := quotaplex.HeldExposure{MeterID: "usd", Unit: "money", Currency: "USD", Scale: 6, UncomputedAttempts: 1, Attempts: 1}
	fake := &pagedUnknownRecovery{pages: []unknownPage{
		{report: quotaplex.RecoveryReport{Scanned: 32, NextAfterID: "p1", Unknown: unknownSummary(now, 1, &old, tokens)}},
		{report: quotaplex.RecoveryReport{Scanned: 3, Done: true, Unknown: unknownSummary(now, 1, &young, tokens, usdUnknown)}},
	}}
	m := &quotaRuntime{warnAfter: time.Hour, workers: []quotaWorker{{tenant: "tenant", recovery: fake}}}
	m.pass(context.Background())
	if _, ok := m.unknownMetrics(); ok {
		t.Fatal("a partial scan must not be published")
	}
	if logs.Len() != 0 {
		t.Fatalf("no warning before the scan completes: %s", logs)
	}
	m.pass(context.Background())
	snapshot, ok := m.unknownMetrics()
	if !ok || snapshot.Attempts != 2 || !snapshot.OldestAgeKnown || snapshot.OldestAge != 3*time.Hour {
		t.Fatalf("%+v %v", snapshot, ok)
	}
	var sawTokens, sawUSD bool
	for _, e := range snapshot.Exposure {
		switch e.Meter {
		case "tokens":
			sawTokens = e.KnownPart == 20 && e.Complete
		case "usd":
			sawUSD = !e.Complete && e.Uncomputed == 1 && e.Unit == "money" && e.Currency == "USD"
		}
	}
	if !sawTokens || !sawUSD {
		t.Fatalf("exposure %+v", snapshot.Exposure)
	}
	warnings := strings.Count(logs.String(), "older than the warning threshold")
	if warnings != 1 || !strings.Contains(logs.String(), `"oldest_age":"3h0m0s"`) || !strings.Contains(logs.String(), "usd: unknown") || !strings.Contains(logs.String(), `"exposure_complete":false`) {
		t.Fatalf("one warning per completed scan, with unknown kept unknown: %s", logs)
	}

	// A young scan logs nothing; a failed page keeps the previous figures.
	logs.Reset()
	fake.pages = []unknownPage{{report: quotaplex.RecoveryReport{Done: true, Unknown: unknownSummary(now, 1, &young, tokens)}}}
	m.pass(context.Background())
	if logs.Len() != 0 {
		t.Fatalf("a young unknown attempt is not a warning: %s", logs)
	}
	if s, _ := m.unknownMetrics(); s.Attempts != 1 {
		t.Fatal(s)
	}
	fake.pages = []unknownPage{{report: quotaplex.RecoveryReport{Done: true}, err: errors.New("store unavailable")}}
	m.pass(context.Background())
	if s, ok := m.unknownMetrics(); !ok || s.Attempts != 1 {
		t.Fatal("a failed scan replaced the last complete figures", s, ok)
	}
	if !strings.Contains(logs.String(), "scan page failed") {
		t.Fatalf("an incomplete scan is reported: %s", logs)
	}

	// An unknown attempt without a dispatch time cannot be shown young.
	logs.Reset()
	undated := unknownSummary(now, 1, nil, tokens)
	undated.UndatedAttempts = 1
	fake.pages = []unknownPage{{report: quotaplex.RecoveryReport{Done: true, Unknown: undated}}}
	m.pass(context.Background())
	if s, _ := m.unknownMetrics(); s.OldestAgeKnown || !strings.Contains(logs.String(), `"oldest_age":"unknown"`) {
		t.Fatalf("%+v %s", s, logs)
	}
}

// A store that keeps failing after one good scan: the figures stay, the
// staleness signal grows, and every pass warns.
func TestQuotaUnknownFiguresShowTheirAgeWhileScansKeepFailing(t *testing.T) {
	logs := captureLog(t)
	now := time.Date(2026, 11, 9, 14, 0, 0, 0, time.UTC)
	clock := now
	old := now.Add(-2 * time.Hour)
	fake := &pagedUnknownRecovery{pages: []unknownPage{{report: quotaplex.RecoveryReport{Done: true, Unknown: unknownSummary(now, 1, &old)}}}}
	m := &quotaRuntime{warnAfter: 24 * time.Hour, clock: func() time.Time { return clock }, workers: []quotaWorker{{tenant: "tenant", recovery: fake}}}
	m.pass(context.Background())
	if s, ok := m.unknownMetrics(); !ok || s.Attempts != 1 || s.ScanAge != 0 {
		t.Fatal(s, ok)
	}
	logs.Reset()
	fake.pages = []unknownPage{
		{report: quotaplex.RecoveryReport{Scanned: 1, NextAfterID: "a"}, err: errors.New("store unavailable")},
		{report: quotaplex.RecoveryReport{}, err: errors.New("store unavailable")},
	}
	for pass := 1; pass <= 3; pass++ {
		clock = now.Add(time.Duration(pass) * time.Minute)
		m.pass(context.Background())
		s, ok := m.unknownMetrics()
		if !ok || s.Attempts != 1 || s.ScanAge != time.Duration(pass)*time.Minute {
			t.Fatalf("pass %d: %+v %v", pass, s, ok)
		}
	}
	if n := strings.Count(logs.String(), "scan page failed"); n != 3 {
		t.Fatalf("one warning per failed pass, got %d: %s", n, logs)
	}
	if !strings.Contains(logs.String(), `"last_complete_scan":"3m0s"`) || strings.Contains(logs.String(), "store unavailable") {
		t.Fatalf("the warning names the age of the figures and no error content: %s", logs)
	}
}

func TestQuotaMetricsWaitForEveryTenant(t *testing.T) {
	now := time.Now().UTC()
	m := &quotaRuntime{warnAfter: time.Hour, workers: []quotaWorker{
		{tenant: "a", unknown: unknownSummary(now, 0, nil)},
		{tenant: "b"},
	}}
	if _, ok := m.unknownMetrics(); ok {
		t.Fatal("a tenant without a complete scan must not be counted as zero")
	}
	m.workers[1].unknown = unknownSummary(now, 0, nil)
	if s, ok := m.unknownMetrics(); !ok || s.Attempts != 0 || s.OldestAgeKnown {
		t.Fatal(s, ok)
	}
}

func TestQuotaUnknownWarnAfterIsBounded(t *testing.T) {
	ctx := context.Background()
	for _, value := range []string{"30s", "721h", "soon"} {
		spec := quotaStartupSpec(time.Now())
		spec.UnknownWarnAfter = value
		cfg := config.Config{DevelopmentMode: true, QuotaFile: quotaStartupFile(t, spec), EnabledModules: []string{"mcplex", "quotaplex"}}
		if _, err := configureQuota(ctx, &cfg, registry.NewMemoryStore(), api.NewConnectedHandler(nil, nil, nil, nil, true)); err == nil || !strings.Contains(err.Error(), "unknown_warn_after") {
			t.Fatalf("%s accepted: %v", value, err)
		}
	}
	spec := quotaStartupSpec(time.Now())
	cfg := config.Config{DevelopmentMode: true, QuotaFile: quotaStartupFile(t, spec), EnabledModules: []string{"mcplex", "quotaplex"}}
	runtime, err := configureQuota(ctx, &cfg, registry.NewMemoryStore(), api.NewConnectedHandler(nil, nil, nil, nil, true))
	if err != nil || runtime.warnAfter != defaultUnknownWarnAfter {
		t.Fatal(runtime, err)
	}
	spec.UnknownWarnAfter = "15m"
	cfg.QuotaFile = filepath.Clean(quotaStartupFile(t, spec))
	runtime, err = configureQuota(ctx, &cfg, registry.NewMemoryStore(), api.NewConnectedHandler(nil, nil, nil, nil, true))
	if err != nil || runtime.warnAfter != 15*time.Minute {
		t.Fatal(err)
	}
	// The real recovery worker produces a summary the runtime publishes.
	runtime.pass(ctx)
	if s, ok := runtime.unknownMetrics(); !ok || s.Attempts != 0 {
		t.Fatal(s, ok)
	}
}
