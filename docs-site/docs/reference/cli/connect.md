---
title: aiplex connect
sidebar_label: connect
description: "Print ready-to-paste client configuration for an access point"
---

This page is generated from the output of `aiplex --help` for the CLI built from `cmd/aiplex-cli`. Regenerate it with `npm run gen:cli` in `docs-site/`.

```text
Shows the base URLs, MCP and skills URLs, how to obtain a token, and snippets
for a client. --client takes a client profile (for example claude-code, codex,
antigravity) or "generic" (the default) for environment variables and curl.

Snippets read the token from $AIPLEX_TOKEN; no token or secret is printed.

With --write, the configuration is merged into the client's own config file
instead of only being printed. Supported for claude-code (settings.json and the
MCP server list), codex (config.toml), gemini-cli (settings.json) and antigravity
(its MCP server list only: ~/.gemini/config/mcp_config.json). Each file
is shown as a diff and needs confirmation (--yes without a terminal). A copy of
the original goes to ~/.aiplex/connect-backups first, existing keys are never
replaced wholesale, and a file that is not valid JSON or TOML is refused rather
than overwritten. A token value is never written: files refer to $AIPLEX_TOKEN,
or, with --token-helper, to "aiplex --context NAME auth print-token" for the context in
effect (which must have a stored token, since the client runs the helper later). Parts a file cannot hold
(an environment variable for the shell) are printed as remaining steps.
antigravity's model and gateway settings, and antigravity-acp, stay print-only.

--undo restores the most recent backup for --client (or the most recent one).
--dry-run shows the diff and writes nothing. aiplex does not install the client.

Usage:
  aiplex connect <access-point> [flags]

Examples:
  aiplex connect ide
  aiplex connect ide --client claude-code
  aiplex connect local --client claude-code --write
  aiplex connect local --client codex --write --dry-run
  aiplex connect local --client gemini-cli --write --with-telemetry http://localhost:4318
  aiplex connect --undo --client claude-code
  aiplex connect ide -o json

Flags:
      --client string           Client profile ID, gemini-cli, or generic (default "generic")
      --dry-run                 With --write or --undo: show the diff and write nothing
  -h, --help                    help for connect
      --model string            With --write: model to select, instead of the first one the access point offers on the client's API
      --scope string            With --write: user (home directory files) or project (this directory's files; claude-code and gemini-cli) (default "user")
      --token-helper            With --write: let the client fetch its token from "aiplex --context NAME auth print-token" for the context in effect (needs a stored token; claude-code, codex)
      --undo                    Restore the most recent backup made by --write (for --client, or the latest of any)
      --with-telemetry string   With --write: also point the client's own OpenTelemetry export at this OTLP endpoint, or 'aiplex' for this deployment's own receiver, which records the client's usage events as reported records (prompts stay redacted; the token is never written)
      --write                   Merge the configuration into the client's own config file (shows a diff, backs up first)
  -y, --yes                     Do not ask for confirmation (required without a terminal)

Global Flags:
      --context string   Use this CLI context for one command (env: AIPLEX_CONTEXT)
      --debug            Show debug output, including raw API errors
      --no-color         Disable colour (also: NO_COLOR=1)
  -o, --output string    Output format: table, wide (every column, untruncated), json or yaml (default "table")
      --token string     Bearer token (env: AIPLEX_TOKEN)
      --url string       AIPlex API URL (env: AIPLEX_URL)

Exit codes:
  0  success
  1  error (including server errors)
  2  invalid input: bad flags or arguments, or a specification the server rejected
  3  not signed in, token rejected, or not permitted
  4  not found
  5  conflict: the resource changed since it was read, or already exists
  6  the API could not be reached
```

