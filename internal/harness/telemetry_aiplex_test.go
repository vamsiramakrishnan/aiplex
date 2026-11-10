package harness

import (
	"strings"
	"testing"
)

// --with-telemetry aiplex targets this deployment's own OTLP receiver. The
// receiver wants the AIPlex token as a bearer credential; no file ever holds
// it, and where a client cannot take a reference the step is printed instead.
func TestTelemetryToAIPlexNeverWritesTheTokenToAFile(t *testing.T) {
	home := isolate(t)
	endpoint := "http://127.0.0.1:18590/otlp"
	opts := Options{Home: home, Cwd: t.TempDir(), Telemetry: endpoint, TelemetryAIPlex: true}

	claude, err := PlanClaudeCode(connectFixture(), opts)
	if err != nil {
		t.Fatal(err)
	}
	apply(t, home, claude)
	settings := read(t, home+"/.claude/settings.json")
	if !strings.Contains(settings, `"OTEL_EXPORTER_OTLP_ENDPOINT": "`+endpoint+`"`) || strings.Contains(settings, "OTEL_EXPORTER_OTLP_HEADERS") || strings.Contains(settings, "Bearer") {
		t.Fatalf("Claude Code settings carry only the endpoint:\n%s", settings)
	}
	if joined := strings.Join(claude.Remaining, "\n"); !strings.Contains(joined, `export OTEL_EXPORTER_OTLP_HEADERS="Authorization=Bearer $AIPLEX_TOKEN"`) {
		t.Fatalf("the header is a printed step:\n%s", joined)
	}

	codex, err := PlanCodex(connectFixture(), opts)
	if err != nil {
		t.Fatal(err)
	}
	apply(t, home, codex)
	toml := read(t, home+"/.codex/config.toml")
	if !strings.Contains(toml, `endpoint = "`+endpoint+`/v1/logs"`) || !strings.Contains(toml, `"Authorization" = "Bearer ${AIPLEX_TOKEN}"`) || !strings.Contains(toml, "log_user_prompt = false") {
		t.Fatalf("Codex otel block:\n%s", toml)
	}

	gemini, err := PlanGeminiCLI(connectFixture(), opts)
	if err != nil {
		t.Fatal(err)
	}
	apply(t, home, gemini)
	gs := read(t, home+"/.gemini/settings.json")
	if !strings.Contains(gs, `"otlpEndpoint": "`+endpoint+`"`) || strings.Contains(gs, "otlpHeaders") || strings.Contains(gs, "Bearer ") && strings.Contains(strings.SplitN(gs, "telemetry", 2)[1], "Bearer") {
		t.Fatalf("Gemini settings:\n%s", gs)
	}
	if joined := strings.Join(gemini.Remaining, "\n"); !strings.Contains(joined, `export GEMINI_TELEMETRY_OTLP_HEADERS="Authorization=Bearer $AIPLEX_TOKEN"`) {
		t.Fatalf("the header is a printed step:\n%s", joined)
	}

	// The planted token never reaches any file.
	for _, path := range []string{"/.claude/settings.json", "/.claude.json", "/.codex/config.toml", "/.gemini/settings.json"} {
		if strings.Contains(read(t, home+path), planted) {
			t.Fatalf("token written to %s", path)
		}
	}
}
