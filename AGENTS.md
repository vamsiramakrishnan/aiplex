# AGENTS.md

Instructions for a coding agent, whatever harness it runs in. Two jobs: onboard a machine to AIPlex, and work in this repository.

## Onboard this machine to AIPlex

AIPlex is a gateway for models, tools and agents. A coding agent reaches it through an access point (`/ap/<id>/...`). Do these steps in order and read each command's output before the next.

1. Check the CLI is installed:

   ```bash
   aiplex version
   ```

   If it is missing, build it from this repository with `make build` (the binary is `bin/aiplex`), or ask the user. Do not install software without asking.

2. Pick an AIPlex. For a local one with mock models and no cloud account:

   ```bash
   aiplex dev --detach
   eval "$(aiplex dev env)"
   ```

   The access point is `local`. For a remote one, create a context and sign in (the user approves in a browser; never ask for a token in chat):

   ```bash
   aiplex config set-context prod --url https://aiplex.example.com
   aiplex login --context prod
   ```

3. Write the harness's own configuration. Supported clients are `claude-code`, `codex` and `gemini-cli`. Preview, then write:

   ```bash
   aiplex connect local --client claude-code --write --dry-run
   aiplex connect local --client claude-code --write
   ```

   It shows a diff, asks for confirmation (`--yes` when there is no terminal, only after the user agreed), backs up the original under `~/.aiplex/connect-backups`, merges rather than replaces, and refuses a file that is not valid JSON or TOML. It never writes a token value. Whatever it lists under "Still to do by hand" is shell environment the user must set, usually the access token. Undo with `aiplex connect --undo --client claude-code`. For Antigravity, `--write` writes only its MCP server list; its model settings are print-only. `--with-telemetry aiplex` points the client's OpenTelemetry export at this deployment's receiver.

4. Verify with one request through the access point, then look at the ledger:

   ```bash
   aiplex whoami
   aiplex usage records --since 1h
   ```

5. Optional: give the agent read-only tools for inspecting AIPlex itself. `aiplex mcp serve` is a stdio MCP server (`whoami`, `list_access_points`, `get_connect_config`, `list_models`, `usage_summary`, `usage_records`). The Claude Code plugin in `integrations/claude-code` and the Gemini CLI extension in `integrations/gemini-cli` wire it up.

A missing token count, price or cost is unknown, not zero. Report it that way.

## Work in this repository

Read `CLAUDE.md` first; its conventions are binding and are not repeated here (fail closed, secrets are references, tenant comes from the verified identity, every new route needs a policy entry, docs change with behaviour).

```bash
make build
go build ./... && go vet ./...
go test ./... -count=1
```

The code is the source of truth. `docs/` holds the verified guides.
