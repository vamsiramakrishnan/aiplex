# AIPlex plugin for Claude Code

A Claude Code plugin with three skills and one MCP server.

| Part | What it does |
| --- | --- |
| `skills/aiplex-onboard` | Install check, start `aiplex dev` or point at a remote AIPlex, run `aiplex connect --write`, verify with one request, show the usage |
| `skills/aiplex-usage` | Answers "what did I spend" and "why was I denied" from your own usage ledger |
| `skills/aiplex-gcp-setup` | Walks `aiplex init` and `aiplex platform` for a Google Cloud deployment |
| `.mcp.json` | Starts `aiplex mcp serve`, a stdio MCP server with read-only tools: `whoami`, `list_access_points`, `get_connect_config`, `list_models`, `usage_summary`, `usage_records` |

The `aiplex` binary must be on `PATH`. The plugin does not install it.

## Install

From a clone of this repository:

```bash
claude --plugin-dir ./integrations/claude-code
claude plugin validate ./integrations/claude-code
```

## A plugin cannot point Claude Code at AIPlex by itself

Plugin settings only carry `agent` and `subagentStatusLine`, not `env`, so a plugin cannot set `ANTHROPIC_BASE_URL` or a credential. That is why `aiplex connect <access-point> --client claude-code --write` exists: it merges those settings into `~/.claude/settings.json` (diff first, backup, `--undo`). Install the plugin for the skills and tools; run `connect --write` for the connection. The `aiplex-onboard` skill does both.

The MCP server uses the CLI's current context and token (`AIPLEX_TOKEN`, `--token`, or `aiplex login`). It is read-only: nothing it exposes changes AIPlex. Tool errors carry the API's message and never the token.

Verified against the Claude Code plugin reference (code.claude.com/docs/en/plugins-reference). `claude plugin validate` has not been run against this directory in this repository's tests; the repository tests check that the manifests parse and that every `aiplex` command in the skills exists.
