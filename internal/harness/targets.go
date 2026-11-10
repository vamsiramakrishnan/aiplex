package harness

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

// Writable lists the client profiles whose configuration can be written.
var Writable = []string{"claude-code", "codex", "gemini-cli"}

// TokenEnv is the environment variable every written file refers to.
const TokenEnv = "AIPLEX_TOKEN"

// DefaultHelper is the credential helper command written by --token-helper
// when no context is named.
const DefaultHelper = "aiplex auth print-token"

// Options controls what a plan writes.
type Options struct {
	Home        string // user home; required
	Cwd         string // project directory for --scope project
	Scope       string // "user" (default) or "project"
	Model       string // overrides the model chosen from the access point
	TokenHelper bool   // reference a credential helper command instead of $AIPLEX_TOKEN
	// HelperContext is the CLI context the helper must use. The helper runs
	// later, from the client, where it would otherwise resolve the default
	// context and return the wrong token.
	HelperContext string
	Telemetry   string // OTLP endpoint for the harness's own telemetry; "" = off
	// TelemetryAIPlex means Telemetry is this deployment's own OTLP receiver
	// (--with-telemetry aiplex). The receiver authenticates the sender with the
	// AIPlex token, so the export header references the token and no file ever
	// holds it: see telemetryAuth.
	TelemetryAIPlex bool
}

// Plan is the result of planning: file changes plus what must still be done
// by hand, and anything worth saying about the choices made.
type Plan struct {
	Client    string
	Changes   []Change
	Remaining []string
	Notes     []string
}

func (o Options) scope() string {
	if o.Scope == "" {
		return "user"
	}
	return o.Scope
}

// helperArgv is the credential helper as a command and arguments.
func (o Options) helperArgv() []string {
	if o.HelperContext == "" {
		return strings.Fields(DefaultHelper)
	}
	return []string{"aiplex", "--context", o.HelperContext, "auth", "print-token"}
}

var shellSafe = regexp.MustCompile(`^[A-Za-z0-9_.@:/+=,-]+$`)

// helper is the helper as one shell command line, quoted for a POSIX shell.
func (o Options) helper() string {
	argv := o.helperArgv()
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

func (o Options) validate() error {
	if o.Home == "" {
		return fmt.Errorf("cannot find your home directory")
	}
	switch o.scope() {
	case "user":
	case "project":
		if o.Cwd == "" {
			return fmt.Errorf("--scope project needs a working directory")
		}
	default:
		return fmt.Errorf("--scope must be user or project, not %q", o.Scope)
	}
	if o.Telemetry != "" {
		u, err := url.Parse(o.Telemetry)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("--with-telemetry needs an http(s) OTLP endpoint URL, for example http://localhost:4317")
		}
	}
	return nil
}

func serverName(c *aiplex.AccessPointConnect) string { return "aiplex-" + c.AccessPointID }

func firstModel(c *aiplex.AccessPointConnect, protocol string) string {
	for _, m := range c.Models {
		for _, p := range m.Protocols {
			if p == protocol {
				return m.Name
			}
		}
	}
	return ""
}

// otlpProtocol picks the OTLP transport from the endpoint: port 4317 is the
// conventional gRPC port, everything else is treated as OTLP over HTTP.
func otlpProtocol(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err == nil && u.Port() == "4317" {
		return "grpc"
	}
	return "http"
}

func tokenStep(client string) string {
	return "export " + TokenEnv + "=<your access token>   # in the shell that starts " + client + "; see `aiplex connect <access-point>` for how to get one"
}

// editJSON merges into a JSON file with fn and returns the Change.
func editJSON(path string, fn func(root *object) error) (Change, error) {
	data, existed, mode, err := readFile(path)
	if err != nil {
		return Change{}, err
	}
	root, err := parseObject(data)
	if err != nil {
		return Change{}, fmt.Errorf("%s is not valid JSON (%v); fix or move it, nothing was changed", path, err)
	}
	if err := fn(root); err != nil {
		return Change{}, fmt.Errorf("%s: %w", path, err)
	}
	return Change{Path: path, Before: data, Existed: existed, After: root.marshal(), Mode: mode}, nil
}

func mcpHTTPEntry(mcpURL string) *object {
	return obj("type", "http", "url", mcpURL, "headers", obj("Authorization", "Bearer ${"+TokenEnv+"}"))
}

// PlanClaudeCode plans the Claude Code configuration.
//
//	user:    ~/.claude/settings.json (env, apiKeyHelper) and ~/.claude.json (mcpServers)
//	project: ./.claude/settings.local.json and ./.mcp.json
func PlanClaudeCode(c *aiplex.AccessPointConnect, o Options) (*Plan, error) {
	if err := o.validate(); err != nil {
		return nil, err
	}
	if os.Getenv("CLAUDE_CONFIG_DIR") != "" {
		return nil, fmt.Errorf("CLAUDE_CONFIG_DIR is set, which moves Claude Code's files; unset it for this command or configure Claude Code by hand")
	}
	base := c.BaseURLs["anthropic"]
	if base == "" {
		return nil, fmt.Errorf("access point %s has no Anthropic Messages endpoint, which Claude Code needs; see `aiplex connect %s` for what it offers", c.AccessPointID, c.AccessPointID)
	}
	if o.Telemetry != "" && o.scope() == "project" {
		return nil, fmt.Errorf("Claude Code ignores OpenTelemetry export variables in project settings (they only apply from user or managed settings); use --scope user with --with-telemetry")
	}
	model := o.Model
	if model == "" {
		model = firstModel(c, "anthropic-messages")
	}
	p := &Plan{Client: "claude-code"}

	settingsPath := filepath.Join(o.Home, ".claude", "settings.json")
	mcpPath := filepath.Join(o.Home, ".claude.json")
	if o.scope() == "project" {
		settingsPath = filepath.Join(o.Cwd, ".claude", "settings.local.json")
		mcpPath = filepath.Join(o.Cwd, ".mcp.json")
	}
	ch, err := editJSON(settingsPath, func(root *object) error {
		env, err := root.child("env")
		if err != nil {
			return err
		}
		env.set("ANTHROPIC_BASE_URL", base)
		if model != "" {
			env.set("ANTHROPIC_MODEL", model)
		}
		if o.Telemetry != "" {
			proto := "http/protobuf"
			if otlpProtocol(o.Telemetry) == "grpc" {
				proto = "grpc"
			}
			env.set("CLAUDE_CODE_ENABLE_TELEMETRY", "1")
			env.set("OTEL_METRICS_EXPORTER", "otlp")
			env.set("OTEL_LOGS_EXPORTER", "otlp")
			env.set("OTEL_EXPORTER_OTLP_PROTOCOL", proto)
			env.set("OTEL_EXPORTER_OTLP_ENDPOINT", o.Telemetry)
			if v, ok := env.get("OTEL_LOG_USER_PROMPTS"); ok && v == "1" {
				p.Notes = append(p.Notes, "your settings already set OTEL_LOG_USER_PROMPTS=1, which exports prompt text; AIPlex left it alone, consider removing it")
			}
		}
		if o.TokenHelper {
			root.set("apiKeyHelper", o.helper())
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	p.Changes = append(p.Changes, ch)

	if c.MCPURL != "" {
		ch, err := editJSON(mcpPath, func(root *object) error {
			servers, err := root.child("mcpServers")
			if err != nil {
				return err
			}
			servers.set(serverName(c), mcpHTTPEntry(c.MCPURL))
			return nil
		})
		if err != nil {
			return nil, err
		}
		p.Changes = append(p.Changes, ch)
		if o.scope() == "project" {
			p.Notes = append(p.Notes, "Claude Code asks you to approve a project .mcp.json server the first time it starts in this directory")
		} else {
			p.Notes = append(p.Notes, "~/.claude.json is also written by Claude Code itself; run this while no Claude Code session is open so a running session does not overwrite the change")
		}
	}

	if o.Telemetry != "" && o.TelemetryAIPlex {
		// Claude Code's settings env holds literal values, so the export header
		// (which carries the token) is set in the shell, never in the file.
		p.Remaining = append(p.Remaining, "export OTEL_EXPORTER_OTLP_HEADERS=\"Authorization=Bearer $"+TokenEnv+"\"   # authenticates the telemetry export to AIPlex; settings.json cannot reference an environment variable, so the token is not written there")
		p.Notes = append(p.Notes, "AIPlex's OTLP receiver records api_request and api_error log events as source=reported usage; metrics and traces are accepted but not recorded")
	}
	if o.TokenHelper {
		p.Remaining = append(p.Remaining,
			"apiKeyHelper runs `"+o.helper()+"`; `aiplex` must be on PATH for Claude Code, and an ANTHROPIC_AUTH_TOKEN in your shell would take precedence over it",
			tokenStep("claude")+"   # still needed for the MCP server header")
	} else {
		p.Remaining = append(p.Remaining,
			"Claude Code reads the model credential from ANTHROPIC_AUTH_TOKEN, and settings files cannot reference an environment variable, so set it in the shell instead of a file: export ANTHROPIC_AUTH_TOKEN=\"$"+TokenEnv+"\"  (or rerun with --token-helper)",
			tokenStep("claude"))
	}
	return p, nil
}

// PlanGeminiCLI plans the Gemini CLI configuration:
//
//	user:    ~/.gemini/settings.json
//	project: ./.gemini/settings.json
//
// Gemini CLI has no settings key for the API base URL; that part is an
// environment variable and is reported as a remaining step.
func PlanGeminiCLI(c *aiplex.AccessPointConnect, o Options) (*Plan, error) {
	if err := o.validate(); err != nil {
		return nil, err
	}
	if o.TokenHelper {
		return nil, fmt.Errorf("Gemini CLI has no credential helper setting, so --token-helper is not available for gemini-cli")
	}
	p := &Plan{Client: "gemini-cli"}
	if base := c.BaseURLs["genai"]; base != "" {
		p.Remaining = append(p.Remaining,
			"export GOOGLE_GEMINI_BASE_URL="+base+"   # Gemini CLI has no settings-file key for the base URL",
			"export GEMINI_API_KEY=\"$"+TokenEnv+"\"   # sent as the API key; "+TokenEnv+" must hold your access token")
	} else {
		p.Notes = append(p.Notes, "this access point has no Google GenAI endpoint, so Gemini CLI cannot use its models; only the MCP server is configured")
	}
	if c.MCPURL == "" && o.Telemetry == "" {
		return p, nil
	}
	path := filepath.Join(o.Home, ".gemini", "settings.json")
	if o.scope() == "project" {
		path = filepath.Join(o.Cwd, ".gemini", "settings.json")
	}
	ch, err := editJSON(path, func(root *object) error {
		if c.MCPURL != "" {
			servers, err := root.child("mcpServers")
			if err != nil {
				return err
			}
			servers.set(serverName(c), obj("httpUrl", c.MCPURL, "headers", obj("Authorization", "Bearer ${"+TokenEnv+"}")))
		}
		if o.Telemetry != "" {
			t, err := root.child("telemetry")
			if err != nil {
				return err
			}
			t.set("enabled", true)
			t.set("otlpEndpoint", o.Telemetry)
			t.set("otlpProtocol", otlpProtocol(o.Telemetry))
			t.set("logPrompts", false)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	p.Changes = append(p.Changes, ch)
	p.Remaining = append(p.Remaining, tokenStep("gemini"))
	if o.Telemetry != "" && o.TelemetryAIPlex {
		p.Remaining = append(p.Remaining, "export GEMINI_TELEMETRY_OTLP_HEADERS=\"Authorization=Bearer $"+TokenEnv+"\"   # authenticates the telemetry export to AIPlex; not written to settings.json so the token never lands in a file")
		p.Notes = append(p.Notes, "AIPlex's OTLP receiver records gemini_cli.api_response and api_error log events as source=reported usage; whether Gemini CLI appends /v1/logs to the endpoint has not been validated")
	}
	return p, nil
}

// PlanCodex plans ~/.codex/config.toml (or $CODEX_HOME/config.toml).
func PlanCodex(c *aiplex.AccessPointConnect, o Options) (*Plan, error) {
	if err := o.validate(); err != nil {
		return nil, err
	}
	if o.scope() == "project" {
		return nil, fmt.Errorf("Codex ignores model providers and OpenTelemetry settings in a project .codex/config.toml; use --scope user")
	}
	base := c.BaseURLs["openai"]
	if base == "" {
		return nil, fmt.Errorf("access point %s has no OpenAI-compatible endpoint, which Codex needs; see `aiplex connect %s`", c.AccessPointID, c.AccessPointID)
	}
	model := o.Model
	if model == "" {
		model = firstModel(c, "openai-responses")
	}
	if model == "" {
		return nil, fmt.Errorf("access point %s offers no model on the OpenAI Responses API, which Codex requires; pass --model to choose one anyway", c.AccessPointID)
	}
	p := &Plan{Client: "codex"}

	dir := os.Getenv("CODEX_HOME")
	if dir == "" {
		dir = filepath.Join(o.Home, ".codex")
	}
	path := filepath.Join(dir, "config.toml")

	top := "model_provider = " + tomlString("aiplex") + "\nmodel = " + tomlString(model) + "\n"
	var t strings.Builder
	t.WriteString("[model_providers.aiplex]\n")
	t.WriteString("name = " + tomlString("AIPlex access point "+c.AccessPointID) + "\n")
	t.WriteString("base_url = " + tomlString(base) + "\n")
	t.WriteString("wire_api = \"responses\"\n")
	t.WriteString("requires_openai_auth = false\n")
	if o.TokenHelper {
		fields := o.helperArgv()
		args := make([]string, 0, len(fields))
		for _, a := range fields[1:] {
			args = append(args, tomlString(a))
		}
		t.WriteString("\n[model_providers.aiplex.auth]\n")
		t.WriteString("command = " + tomlString(fields[0]) + "\n")
		t.WriteString("args = [" + strings.Join(args, ", ") + "]\n")
	} else {
		t.WriteString("env_key = " + tomlString(TokenEnv) + "\n")
	}
	owned := [][]string{{"model_providers", "aiplex"}}
	if c.MCPURL != "" {
		t.WriteString("\n[mcp_servers." + tomlKey(serverName(c)) + "]\n")
		t.WriteString("url = " + tomlString(c.MCPURL) + "\n")
		t.WriteString("bearer_token_env_var = " + tomlString(TokenEnv) + "\n")
		owned = append(owned, []string{"mcp_servers", serverName(c)})
	}
	if o.Telemetry != "" {
		endpoint := o.Telemetry
		switch {
		case o.TelemetryAIPlex:
			// Codex posts to the endpoint exactly as given, so name the logs
			// route; the header refers to the token by environment variable.
			endpoint = strings.TrimRight(endpoint, "/") + "/v1/logs"
			t.WriteString("\n[otel]\nlog_user_prompt = false\n")
			t.WriteString("exporter = { otlp-http = { endpoint = " + tomlString(endpoint) + ", protocol = \"binary\", headers = { \"Authorization\" = " + tomlString("Bearer ${"+TokenEnv+"}") + " } } }\n")
			p.Notes = append(p.Notes, "AIPlex records Codex's response.completed event as source=reported usage. The export header refers to ${"+TokenEnv+"}, Codex's documented placeholder form for header values; that has not been validated against a live Codex, and the token is not written to the file")
		case otlpProtocol(endpoint) == "grpc":
			t.WriteString("\n[otel]\nlog_user_prompt = false\n")
			t.WriteString("exporter = { otlp-grpc = { endpoint = " + tomlString(endpoint) + " } }\n")
		default:
			if u, err := url.Parse(endpoint); err == nil && (u.Path == "" || u.Path == "/") {
				endpoint = strings.TrimRight(endpoint, "/") + "/v1/logs"
			}
			t.WriteString("\n[otel]\nlog_user_prompt = false\n")
			t.WriteString("exporter = { otlp-http = { endpoint = " + tomlString(endpoint) + ", protocol = \"binary\" } }\n")
		}
		owned = append(owned, []string{"otel"})
		p.Notes = append(p.Notes, "Codex exports log events through [otel]; it has no metrics exporter setting, and prompts stay redacted (log_user_prompt = false)")
	}

	data, existed, mode, err := readFile(path)
	if err != nil {
		return nil, err
	}
	after, err := MergeTOML(string(data), top, t.String(), []string{"model", "model_provider"}, owned)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	p.Changes = append(p.Changes, Change{Path: path, Before: data, Existed: existed, After: []byte(after), Mode: mode})
	p.Notes = append(p.Notes, "Codex's TOML is edited as two delimited AIPlex blocks so your comments and other keys are untouched; an existing top-level model or model_provider line is commented out, not deleted")
	if o.TokenHelper {
		p.Remaining = append(p.Remaining, "the provider runs `"+o.helper()+"` for its token; `aiplex` must be on PATH for Codex", tokenStep("codex")+"   # still needed for the MCP server")
	} else {
		p.Remaining = append(p.Remaining, tokenStep("codex"))
	}
	return p, nil
}
