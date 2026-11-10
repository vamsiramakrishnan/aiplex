# Harness onboarding

How to point a coding agent (Claude Code, Codex, Gemini CLI) at an AIPlex access point, and how to let that agent inspect AIPlex itself. Three pieces:

| Piece | What it is |
| --- | --- |
| `aiplex connect <access-point> --client <client> --write` | Merges the connection settings into the client's own config file, with a diff, a backup and an undo |
| `aiplex mcp serve` | A stdio MCP server with read-only tools, using your own token |
| `integrations/` and `AGENTS.md` | A Claude Code plugin, a Gemini CLI extension and harness-neutral onboarding instructions |

AIPlex does not install the client binary. It writes configuration only when you ask, and only the files listed below.

## `aiplex connect --write`

```bash
aiplex connect local --client claude-code --write --dry-run   # diff only
aiplex connect local --client claude-code --write             # diff, confirm, write
aiplex connect --undo --client claude-code                    # restore the latest backup
```

Supported clients and the files they touch (user scope; paths are under your home directory):

| Client | Files | What is merged |
| --- | --- | --- |
| `claude-code` | `~/.claude/settings.json`, `~/.claude.json` | `env.ANTHROPIC_BASE_URL`, `env.ANTHROPIC_MODEL`; the access point's MCP server under top-level `mcpServers` |
| `claude-code --scope project` | `./.claude/settings.local.json`, `./.mcp.json` | The same, for this project only |
| `codex` | `~/.codex/config.toml` (or `$CODEX_HOME/config.toml`) | `model_provider`, `model`, `[model_providers.aiplex]`, `[mcp_servers.aiplex-<id>]` |
| `gemini-cli` | `~/.gemini/settings.json` (`./.gemini/settings.json` with `--scope project`) | `mcpServers.aiplex-<id>`, `security.auth.selectedType = "gemini-api-key"`, `model.name` (the first Google GenAI model of the access point, or `--model`) |
| `antigravity` | `~/.gemini/config/mcp_config.json` (`./.agents/mcp_config.json` with `--scope project`) | MCP only: `mcpServers.aiplex` = the stdio server `aiplex mcp serve`. The access point's own MCP endpoint is not written (see below). Model and gateway settings stay print-only |

### What it does to your files

- **Merge, never replace.** Unrelated keys, their values and their order stay. A key AIPlex sets is overwritten with the access point's value. A key AIPlex does not set is never removed by a later run (use `--undo`).
- **Refuse, do not overwrite.** A file that is not valid JSON (or TOML) makes the command exit non-zero without touching it. So does a JSON value that is the wrong type, such as `env` being a string.
- **Show a diff first.** Each changed file is shown as a unified diff. On a terminal you are asked to confirm. Without a terminal the command refuses unless you pass `--yes`.
- **Existing secrets are never displayed.** Your files may already hold credentials (an `ANTHROPIC_API_KEY` in `env`, a bearer token in an MCP header, OAuth fields in `~/.claude.json`). The diff, for `--write`, `--dry-run` and `--undo`, is computed over redacted copies of both sides; the bytes that are merged, written, backed up and restored are the real ones, and the redacted form is never written. See "Redaction" below.
- **Back up first.** The original of every changed file is copied to `~/.aiplex/connect-backups/<timestamp>-<client>/` with a `manifest.json` (paths, modes, SHA-256 of before and after; no file contents besides the copies, no token). The directory is `0700` and its files `0600`. Backups hold your original config files, so treat them as you treat the originals.
- **Write atomically.** A temporary file in the same directory is renamed over the target. Existing permissions are kept; files AIPlex creates are `0600`. A symlinked config file (dotfile managers) is edited through the link.
- **Idempotent.** Running the same command twice produces no diff, writes nothing and makes no second backup.
- **Undo.** `aiplex connect --undo [--client c]` shows the diff back to the backup, asks for confirmation, then restores it, or removes a file that did not exist before. Changes you made to those files since the backup are lost; the diff shows them. Each backup can be undone once; a second `--undo` goes to the one before it.

`HOME` is respected, so a test or container can use an isolated home.

### Tokens are never written

Configuration refers to a token and never contains one. The model-side credential and the MCP header both read the `AIPLEX_TOKEN` environment variable:

- The MCP entries use `Authorization: Bearer ${AIPLEX_TOKEN}` (Claude Code, Gemini CLI) or `bearer_token_env_var = "AIPLEX_TOKEN"` (Codex).
- Codex's provider uses `env_key = "AIPLEX_TOKEN"`.
- Claude Code settings files cannot reference an environment variable, and its model credential comes from `ANTHROPIC_AUTH_TOKEN` or `apiKeyHelper`. Without `--token-helper`, the command prints the remaining step (`export ANTHROPIC_AUTH_TOKEN="$AIPLEX_TOKEN"`) instead of writing a value.
- `--token-helper` writes `apiKeyHelper: "aiplex --context NAME auth print-token"` for Claude Code and `[model_providers.aiplex.auth]` with `command = "aiplex"`, `args = ["--context", "NAME", "auth", "print-token"]` for Codex, where NAME is the context in effect for the `connect` command (`--context`, `AIPLEX_CONTEXT` or the current context). The client runs the helper later, where it would otherwise resolve the default context and return the wrong token. The name is shell-quoted in the single-string form. Because the helper reads a stored token, `--token-helper` is refused when the context in effect has none (credentials from `--token`, `AIPLEX_TOKEN` or `AIPLEX_URL` only): create and sign in to a context first with `aiplex config set-context NAME --url URL` and `aiplex login --context NAME`. Re-running with the same context is a no-op; a different context is a visible diff. `aiplex --context NAME auth print-token` prints that context's token (flag, `AIPLEX_TOKEN`, or the stored token, refreshed) and exits 3 if there is none. `aiplex` must be on the client's `PATH`; the shell quoting is for POSIX shells.

Gemini CLI has no credential helper and no settings key for the base URL, so for it the command prints, as remaining steps, `export GOOGLE_GEMINI_BASE_URL=<genai base URL>` and `export GEMINI_API_KEY="$AIPLEX_TOKEN"`.

The command ends with a "Still to do by hand" list of exactly the parts a file cannot hold.

### Redaction

What is displayed is redacted; what is written is not. Where a file parses (JSON, or TOML) the walk is structure-aware; otherwise it falls back to lines.

- A string under a key whose name contains (case-insensitive) `token`, `secret`, `password`, `passwd`, `passphrase`, `api_key`, `apikey`, `api-key`, `authorization`, `credential`, `cookie`, `session`, `private_key`, `client_secret`, `refresh`, `bearer` or `oauth`, or has `auth` as a whole segment (`auth`, `x-auth`, `userAuth`; not `author`), is shown as `[redacted]`. Everything below such a key is redacted too, except under a bare `auth` table, where only strings with sensitive names or credential shapes are (so `command` and `args` stay readable).
- A string that looks like a credential is redacted under any key: `Bearer` or `Basic` followed by a literal, `sk-`, `sk-ant-`, `ghp_`/`gho_`/`ghs_`/`ghu_`/`ghr_`, `github_pat_`, `AIza`, `xox*-`, `glpat-`, `AKIA`, `ya29.`, a JWT shape (`eyJ...`), and any 32-or-more-character string of letters, digits and `_-+/=.` that mixes letters and digits and has high entropy (not a URL or path). Such tokens inside a longer string (a URL or command line) are replaced in place. The heuristic leans toward redacting, so a long identifier may be hidden unnecessarily.
- A value that is only an environment reference stays visible: `${AIPLEX_TOKEN}`, `$VAR`, `Bearer ${VAR}`. So do `apiKeyHelper` (a command), and values of `env_key` and `*_env_var`/`*_env` keys (names of variables). Numbers and booleans are not redacted.
- JSON is pretty-printed identically on both sides for display, so a compact file does not appear as one line. In TOML, every secret value found by the parse is also removed wherever it occurs in the text (multi-line strings included), and `key = value` pairs, inline tables included, are redacted by name.
- A file that does not parse is redacted line by line with the same rules. If a line is very long, or the content is not UTF-8, only "N lines changed; content not shown" is printed.
- A change that differs only inside redacted values is reported as "a value changed; content redacted", never as up to date.

Backups are exact copies of your original files, so they contain whatever secrets those files held. They are created `0600` in a `0700` directory. The manifest holds paths, modes and SHA-256 hashes only. Error messages for an invalid file name the file and the parser's reason, not its content.

### Codex TOML

TOML libraries drop comments and reorder keys, so the Codex file is edited as text. AIPlex owns two delimited blocks (`# >>> aiplex connect ...` and `# <<< aiplex connect ...`): top-level keys at the start of the file, and the tables at the end. Everything else is left byte for byte. An existing top-level `model` or `model_provider` line is commented out with `# aiplex disabled: `, not deleted. After editing, the whole file is parsed and compared with the original: if anything outside the blocks would differ, or the result is not valid TOML (for example you already have an `[otel]` table or your own `[model_providers.aiplex]`), the command refuses and writes nothing. Codex ignores `model_providers` and `otel` in a project `.codex/config.toml`, so `--scope project` is refused for Codex.

### `--with-telemetry <otlp-endpoint>`

Also sets the client's own OpenTelemetry export, where it has one. Prompt text is never exported: nothing enabling it is written, and for Codex `log_user_prompt = false`. If your Claude Code settings already set `OTEL_LOG_USER_PROMPTS=1`, the command says so and leaves it.

| Client | Written |
| --- | --- |
| `claude-code` | `CLAUDE_CODE_ENABLE_TELEMETRY=1`, `OTEL_METRICS_EXPORTER=otlp`, `OTEL_LOGS_EXPORTER=otlp`, `OTEL_EXPORTER_OTLP_PROTOCOL`, `OTEL_EXPORTER_OTLP_ENDPOINT` in `env`. User scope only: Claude Code ignores these variables in project settings, so `--scope project` with this flag is refused |
| `codex` | `[otel]` with `log_user_prompt = false` and an `otlp-http` (default, `/v1/logs` added when the endpoint has no path) or `otlp-grpc` (port 4317) exporter. Codex's `[otel]` exports log events |
| `gemini-cli` | `telemetry.enabled`, `otlpEndpoint`, `otlpProtocol` (`grpc` or `http`), `logPrompts: false` |

The protocol is chosen from the endpoint: port `4317` is treated as gRPC, anything else as OTLP over HTTP. Whether the endpoint accepts that is not checked.

This is the client's own telemetry. AIPlex's telemetry (its spans and metrics, and the
collector a Helm install runs) is described in [observability](observability.md). Nothing
here sends the client's telemetry into the usage ledger.

## `aiplex mcp serve`

A stdio MCP server. Configure a client with the command `aiplex mcp serve`; the plugin and extension below do this for you. It uses the CLI's current context and token, so the tools see what you see.

| Tool | Returns |
| --- | --- |
| `whoami` | Principal, email, provider, groups, roles, scopes |
| `list_access_points` | The access points visible to the caller, with model, tool and skill counts |
| `get_connect_config` | What `aiplex connect` prints, for an access point and client |
| `list_models` | The models an access point offers, their protocols and the base URLs |
| `usage_summary` | Totals of the caller's own usage, optionally grouped |
| `usage_records` | A page (at most 100) of the caller's own records, with filters for status and error class |

Every tool is annotated read-only (`readOnlyHint`), and there are no mutating tools. Usage queries are filtered to the caller's own principal. Unknown counts and costs stay unknown, as in the CLI. Tool errors carry the API's message with the token removed. Diagnostics go to stderr; stdout carries only protocol messages.

Protocol: the server answers `server/discover` and requests that carry the per-request `_meta` of the 2026-07-28 revision (the current one at the time of writing), and answers the `initialize` handshake of 2025-11-25 and 2025-06-18, which today's harnesses use. A request naming any other version gets the `UnsupportedProtocolVersion` error listing the supported ones. There is no tool for explaining a denial; `usage_records` with `status` `denied` and its `error_class` is the available evidence.

## Integrations

- `integrations/claude-code/`: a Claude Code plugin with the skills `aiplex-onboard`, `aiplex-usage` and `aiplex-gcp-setup`, and an `.mcp.json` that launches `aiplex mcp serve`. Try it with `claude --plugin-dir ./integrations/claude-code`. A plugin cannot set the base URL or a credential (plugin settings carry only `agent` and `subagentStatusLine`), which is why `connect --write` exists.
- `integrations/gemini-cli/`: a Gemini CLI extension (`gemini-extension.json` and a `GEMINI.md` context file). Link it with `gemini extensions link ./integrations/gemini-cli`.
- `AGENTS.md` at the repository root: harness-neutral instructions to onboard a machine and to work in the repository.

A test walks the CLI's command tree and fails if any `aiplex` command or flag in these instruction files does not exist.

## What has and has not been validated

Config shapes were taken from the vendors' current documentation on 2026-11-10: Claude Code settings, MCP and monitoring pages (code.claude.com/docs/en/settings, /mcp, /monitoring-usage, /authentication, /plugins-reference), Codex config pages (learn.chatgpt.com/docs/config-file/config-advanced and config-reference), Gemini CLI MCP, configuration and extension references (google-gemini/gemini-cli, `docs/`), Antigravity's MCP page (antigravity.google/docs/mcp), and the MCP specification (modelcontextprotocol.io, revision 2026-07-28 and its versioning page).

Tested here: file merging, diffs, backup, undo, idempotence and token absence (unit and end-to-end tests against `aiplex dev` with an isolated home); the MCP handshake, `tools/list` and tool calls over stdio.

### Observed with real clients (Linux container, 2026-11-10, isolated `HOME`)

Each client was started with `HOME` pointing at an empty directory, with the files `connect --write` produced, against `aiplex dev` (mock models) and, where noted, a real upstream. The container's own `CLAUDE_CODE_*` and `ANTHROPIC_*` variables were removed from the client's environment, because they override settings files.

| Client and version | Observed |
| --- | --- |
| Claude Code 2.1.322 | With the written `settings.json` and `ANTHROPIC_AUTH_TOKEN`, a headless `claude -p` turn completed against the mock Anthropic model: the request reached `/ap/local/v1/messages`, the ledger holds a record with the caller `developer@localhost`, the model and token counts. The `--token-helper` variant (`apiKeyHelper`, no `ANTHROPIC_AUTH_TOKEN`) also completed. `claude mcp list` showed `aiplex-local` connected. `claude plugin validate integrations/claude-code` passed (one advisory about a missing icon); `claude --plugin-dir integrations/claude-code` loaded the plugin, connected its stdio `aiplex mcp serve` server and listed its three skills. One turn through a real upstream, z.ai's Anthropic-compatible endpoint (GLM family), also completed, including a tool call to the access point's `echo` tool. |
| Gemini CLI 0.67.0 | After the written `settings.json`, with `GOOGLE_GEMINI_BASE_URL` and `GEMINI_API_KEY` exported, `gemini -p` completed against the mock Gemini model, and against the real Gemini Developer API through `aiplex dev --provider gemini`. Requests arrived at `/ap/local/v1beta/models/...:streamGenerateContent` and the ledger holds them. `gemini mcp list` showed `aiplex-local` connected. `gemini extensions link integrations/gemini-cli` worked and connected the stdio server. |
| Codex CLI 0.167.0 | `codex exec` completed against the mock Responses model with the written `config.toml`, with `env_key` and with `--token-helper` (`[model_providers.aiplex.auth]`). `codex mcp list` showed `aiplex-local`. A real OpenAI upstream was not exercised: the OpenAI key available for this validation had no credit (HTTP 429 `insufficient_quota` on every generation endpoint). |
| Antigravity CLI `agy` 1.5.4 | Installed from the official script (antigravity.google/cli/install.sh). `agy mcp add` creates `~/.gemini/config/mcp_config.json` with `mcpServers.<name>.serverUrl` and `headers`; `agy mcp list` listed the entry written by `connect --write`. The CLI connected to a configured MCP server at start-up and sent the header `Authorization: Bearer ${AIPLEX_TOKEN}` literally: it does not expand environment variables. So AIPlex writes only the stdio server and leaves the access point's MCP endpoint, which needs a bearer token, as a manual step. Not observed: a model turn (the CLI needs a Google sign-in), and the Antigravity IDE or App loading the file (the IDE and App were not installed). The commonly reported path `~/.gemini/antigravity/mcp_config.json` is not what the CLI or the official page use. |

Things the real clients showed that the mock-only tests did not:

- **Claude Code sends `anthropic-beta` headers** (`claude-code-20250219`, `interleaved-thinking-2025-05-14`, `context-management-2025-06-27` and others, which change between versions). A surface refuses a beta that is not in its `provider.anthropic_betas`, so the first request failed with 400 until the list was added. `aiplex dev` now seeds the betas observed with 2.1.322 on its Anthropic surfaces. A surface you create yourself needs the same list, and a later Claude Code version may need more.
- **An `ANTHROPIC_API_KEY` in Claude Code's settings `env` conflicts.** Claude Code sends it as `x-api-key` next to the AIPlex token, the access point answers 401 `conflicting credentials`, and your provider key has been sent to AIPlex. `connect --write` now says so when it finds one; it does not remove it.
- **Gemini CLI needs `security.auth.selectedType`** (otherwise "Invalid auth method selected") and chooses its own models. It remaps names (a request for `gemini-3.5-flash` went to `gemini-3.8-flash`) and uses a flash-lite model for routing, so the access point has to offer every model the CLI may ask for, not only the one you configure.
- Claude Code prints "doesn't recognize the model" for a gateway model name and assumes a 200k window; this is a warning only.
- Codex warns that it has no metadata for a gateway model name, and prints an error for any other MCP server in your config that it cannot reach.

Not validated: the `--with-telemetry` settings have not been checked against a client that exports, or received by a collector. `~/.claude.json` is also written by Claude Code itself; the command warns to run it while no session is open. `CLAUDE_CONFIG_DIR` is not supported: if it is set, the Claude Code write is refused rather than guessing.
