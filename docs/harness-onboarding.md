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
| `gemini-cli` | `~/.gemini/settings.json` (`./.gemini/settings.json` with `--scope project`) | `mcpServers.aiplex-<id>` |
| `antigravity` | none | Print-only. Its config file location is not documented, so AIPlex does not guess one |

### What it does to your files

- **Merge, never replace.** Unrelated keys, their values and their order stay. A key AIPlex sets is overwritten with the access point's value. A key AIPlex does not set is never removed by a later run (use `--undo`).
- **Refuse, do not overwrite.** A file that is not valid JSON (or TOML) makes the command exit non-zero without touching it. So does a JSON value that is the wrong type, such as `env` being a string.
- **Show a diff first.** Each changed file is shown as a unified diff. On a terminal you are asked to confirm. Without a terminal the command refuses unless you pass `--yes`.
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
- `--token-helper` writes `apiKeyHelper: "aiplex auth print-token"` for Claude Code and `[model_providers.aiplex.auth]` with `command = "aiplex"`, `args = ["auth", "print-token"]` for Codex. `aiplex auth print-token` prints the token the CLI would use (flag, `AIPLEX_TOKEN`, or the signed-in context, refreshed) and exits 3 if there is none. `aiplex` must be on the client's `PATH`.

Gemini CLI has no credential helper and no settings key for the base URL, so for it the command prints, as remaining steps, `export GOOGLE_GEMINI_BASE_URL=<genai base URL>` and `export GEMINI_API_KEY="$AIPLEX_TOKEN"`.

The command ends with a "Still to do by hand" list of exactly the parts a file cannot hold.

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

Config shapes were taken from the vendors' current documentation on 2026-11-10: Claude Code settings, MCP and monitoring pages (code.claude.com/docs/en/settings, /mcp, /monitoring-usage, /authentication, /plugins-reference), Codex config pages (learn.chatgpt.com/docs/config-file/config-advanced and config-reference), Gemini CLI MCP, configuration and extension references (google-gemini/gemini-cli, `docs/`), and the MCP specification (modelcontextprotocol.io, revision 2026-07-28 and its versioning page).

Tested here: file merging, diffs, backup, undo, idempotence and token absence (unit and end-to-end tests against `aiplex dev` with an isolated home); the MCP handshake, `tools/list` and tool calls over stdio.

Not validated: any of the three clients actually loading the files written, or the plugin and extension being loaded by their harness (the clients are not installed in the test environment). The `--with-telemetry` settings have not been checked against a client that exports, or received by a collector. Not exercised: that Claude Code's `apiKeyHelper` output is accepted by the AIPlex Anthropic endpoint (it is documented as a credential command; AIPlex expects a bearer token), and that Gemini CLI's `GEMINI_API_KEY` header is accepted by the AIPlex GenAI endpoint. `~/.claude.json` is also written by Claude Code itself; the command warns to run it while no session is open. `CLAUDE_CONFIG_DIR` is not supported: if it is set, the Claude Code write is refused rather than guessing.
