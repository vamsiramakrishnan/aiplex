package fragment

import (
	"strings"
	"testing"
)

func TestClaudeCodeSettingsKeyOrderAndHelper(t *testing.T) {
	got := string(ClaudeCodeSettings("https://gw.example/ap/x", "m", "/bin/aiplex auth print-token"))
	want := "{\n  \"env\": {\n    \"ANTHROPIC_BASE_URL\": \"https://gw.example/ap/x\",\n    \"ANTHROPIC_MODEL\": \"m\"\n  },\n  \"apiKeyHelper\": \"/bin/aiplex auth print-token\"\n}\n"
	if got != want {
		t.Fatalf("got %q", got)
	}
	bare := string(ClaudeCodeSettings("https://gw.example", "", ""))
	if strings.Contains(bare, "ANTHROPIC_MODEL") || strings.Contains(bare, "apiKeyHelper") {
		t.Fatal("empty model or helper must be omitted")
	}
}

func TestGeminiCLISettings(t *testing.T) {
	got := string(GeminiCLISettings("g"))
	for _, want := range []string{`"selectedType": "gemini-api-key"`, `"name": "g"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %s:\n%s", want, got)
		}
	}
	root := Obj("model", "not an object")
	if err := SetGeminiCLIModel(root, "g"); err == nil {
		t.Fatal("a non-object model key must be refused, not replaced")
	}
}

func TestShellCommandQuoting(t *testing.T) {
	if got := ShellCommand([]string{"/bin/a", "x y", "it's", "$HOME"}); got != `/bin/a 'x y' 'it'\''s' '$HOME'` {
		t.Fatalf("got %s", got)
	}
}
