package fragment

import (
	"regexp"
	"strings"
)

// Environment variable names that Gemini CLI reads. Its settings file has no
// key for the base URL or the API key, so these are the only way to set them.
const (
	GeminiBaseURLEnv = "GOOGLE_GEMINI_BASE_URL"
	GeminiKeyEnv     = "GEMINI_API_KEY"
)

// SetClaudeCodeEnv writes the model-routing keys Claude Code reads from the
// "env" object of settings.json. An empty model leaves ANTHROPIC_MODEL unset.
func SetClaudeCodeEnv(env *Object, baseURL, model string) {
	env.Set("ANTHROPIC_BASE_URL", baseURL)
	if model != "" {
		env.Set("ANTHROPIC_MODEL", model)
	}
}

// SetClaudeCodeHelper writes Claude Code's credential helper setting: one
// shell command line in the top-level apiKeyHelper key.
func SetClaudeCodeHelper(root *Object, command string) {
	root.Set("apiKeyHelper", command)
}

// ClaudeCodeSettings is the Claude Code settings.json fragment for a gateway
// that speaks Anthropic Messages: the env keys and, when helper is not empty,
// the apiKeyHelper command. It is what `connect --write` writes into a file
// that did not exist (before MCP and telemetry additions).
func ClaudeCodeSettings(baseURL, model, helper string) []byte {
	root := NewObject()
	env, _ := root.Child("env")
	SetClaudeCodeEnv(env, baseURL, model)
	if helper != "" {
		SetClaudeCodeHelper(root, helper)
	}
	return root.Marshal()
}

// SetGeminiCLIModel writes the settings Gemini CLI needs to use an API key
// against a gateway: the API-key auth method and, when model is not empty, the
// model name. It fails when either key already holds something that is not an
// object.
//
// Observed with Gemini CLI 0.67 (2026-11-10): with only GEMINI_API_KEY in the
// environment a headless run stops with "Invalid auth method selected" unless
// security.auth.selectedType names the API-key method, and the CLI asks for its
// own default model unless model.name names one the gateway serves.
func SetGeminiCLIModel(root *Object, model string) error {
	sec, err := root.Child("security")
	if err != nil {
		return err
	}
	auth, err := sec.Child("auth")
	if err != nil {
		return err
	}
	auth.Set("selectedType", "gemini-api-key")
	if model != "" {
		m, err := root.Child("model")
		if err != nil {
			return err
		}
		m.Set("name", model)
	}
	return nil
}

// GeminiCLISettings is the Gemini CLI settings.json fragment for a gateway
// serving Google GenAI, without MCP or telemetry additions.
func GeminiCLISettings(model string) []byte {
	root := NewObject()
	_ = SetGeminiCLIModel(root, model) // a new object has no conflicting keys
	return root.Marshal()
}

var shellSafe = regexp.MustCompile(`^[A-Za-z0-9_.@:/+=,-]+$`)

// ShellCommand joins argv into one command line quoted for a POSIX shell.
func ShellCommand(argv []string) string {
	out := make([]string, len(argv))
	for i, a := range argv {
		if shellSafe.MatchString(a) {
			out[i] = a
		} else {
			out[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		}
	}
	return strings.Join(out, " ")
}

// ExportLine is a shell line that sets name to value.
func ExportLine(name, value string) string { return "export " + name + "=" + value }

// CopyLine is the shell line that copies the variable from into name, for a
// client that reads name while the launcher holds the credential in from. It
// names the variable, never a value.
func CopyLine(name, from string) string { return ExportLine(name, `"$`+from+`"`) }

// ClaudeCodeTokenEnv is the variable Claude Code reads for a bearer credential.
const ClaudeCodeTokenEnv = "ANTHROPIC_AUTH_TOKEN"
