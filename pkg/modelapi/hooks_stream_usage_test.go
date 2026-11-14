package modelapi

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/pkg/hooks"
)

func readUsageFixture(t *testing.T, provider, file string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "usage-formats", provider, file))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// A stream the response hooks rewrote is metered exactly like the stream the
// provider sent: the same usage observers run over the re-emitted bytes and
// must report the same observations (counts, cumulative flags, terminal
// marks). The hooks buffer the stream; the observers run on the way in, but
// a caller (or a second gateway, or the ledger's own reader) sees the
// re-emitted stream, so its usage and terminal events must survive.
func TestHookRewrittenStreamsMeterAsTheOriginal(t *testing.T) {
	for _, c := range []struct {
		provider, file, format string
	}{
		{"openai", "responses.sse", hooks.FormatOpenAIResponses},
		{"azure-openai", "responses.sse", hooks.FormatOpenAIResponses},
		{"google", "gemini-generate.sse", hooks.FormatGemini},
		{"google", "vertex-generate.sse", hooks.FormatGemini},
		{"google", "vertex-generate-cumulative.sse", hooks.FormatGemini},
		// The siblings, for comparison.
		{"openai", "chat.sse", hooks.FormatOpenAIChat},
		{"anthropic", "messages.sse", hooks.FormatAnthropicMessages},
	} {
		t.Run(c.provider+"/"+c.file, func(t *testing.T) {
			var route fixtureRoute
			for _, r := range usageRoutes() {
				if r.provider == c.provider && r.file == c.file {
					route = r
				}
			}
			if route.call == nil {
				t.Fatalf("no conformance route for %s/%s", c.provider, c.file)
			}
			original := readUsageFixture(t, c.provider, c.file)
			content, err := hooks.ExtractResponse(c.format, "text/event-stream", original)
			if err != nil || len(content.Parts) == 0 {
				t.Fatalf("extract: %v", err)
			}
			rewritten := content.Apply(map[int]string{0: "[REDACTED]"})
			if bytes.Equal(rewritten, original) {
				t.Fatal("the stream was not rewritten")
			}
			before := runFixtureHTTP(t, route, original, modeLegacy)
			after := runFixtureHTTP(t, route, rewritten, modeLegacy)
			if before.code != 200 || after.code != 200 {
				t.Fatalf("status %d / %d", before.code, after.code)
			}
			if len(before.observations) == 0 {
				t.Fatal("the original stream produced no observations")
			}
			if !reflect.DeepEqual(before.observations, after.observations) {
				t.Fatalf("observations changed:\n%+v\n%+v", before.observations, after.observations)
			}
			if !reflect.DeepEqual(before.accumulate(), after.accumulate()) {
				t.Fatalf("usage changed: %v -> %v", before.accumulate(), after.accumulate())
			}
			a, aok := before.terminal()
			b, bok := after.terminal()
			if aok != bok || !reflect.DeepEqual(a, b) {
				t.Fatalf("terminal event changed: %v %v", a, b)
			}
			// Settlement under a token profile agrees too.
			if route.strict == strictSettles {
				strictBefore := runFixtureHTTP(t, route, original, modeStrict)
				strictAfter := runFixtureHTTP(t, route, rewritten, modeStrict)
				if strictBefore.settled() != strictAfter.settled() || !strictBefore.settled() {
					t.Fatalf("strict settlement: %v -> %v", strictBefore.settled(), strictAfter.settled())
				}
				if !reflect.DeepEqual(strictBefore.accumulate(), strictAfter.accumulate()) {
					t.Fatalf("strict usage changed: %v -> %v", strictBefore.accumulate(), strictAfter.accumulate())
				}
			}
		})
	}
}
