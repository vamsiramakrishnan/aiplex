---
name: aiplex-onboard
description: Point this machine's coding agent (Claude Code, Codex or Gemini CLI) at an AIPlex access point. Use when the user wants to start using AIPlex, connect a harness to it, or check that a connection works.
---

# Onboard a coding agent to AIPlex

Work through these steps in order. Run each command yourself and read its output before moving on. Stop and ask the user before anything that edits a file outside the project; `aiplex connect --write` shows a diff and asks first, so let the user see it.

## 1. Check the CLI

```bash
aiplex version
```

If `aiplex` is not found, tell the user to install it from the repository README (`make build`, or a release binary). This skill does not install software.

## 2. Find or start an AIPlex

Ask which they want:

- **Local, no cloud account.** Start the local stack in the background and load its environment:

```bash
aiplex dev --detach
aiplex dev status
eval "$(aiplex dev env)"
```

  The local access point is `local`. Its models are mock models; it needs no key.

- **A remote AIPlex.** Ask for its URL and the access point ID. Then:

```bash
aiplex config set-context prod --url https://aiplex.example.com
aiplex login --context prod
aiplex access-points list
```

  `aiplex login` opens a browser for the user to approve. Do not ask the user to paste a token into the chat.

Confirm the connection works and who the caller is:

```bash
aiplex whoami
```

## 3. Write the harness configuration

`aiplex connect <access-point> --client <client> --write` merges the settings into the client's own config file. It shows a unified diff, asks for confirmation, keeps a backup under `~/.aiplex/connect-backups`, and never writes a token value. Preview first:

```bash
aiplex connect local --client claude-code --write --dry-run
```

Clients: `claude-code`, `codex`, `gemini-cli` and `antigravity`. For Antigravity, `--write` writes only its MCP server list; its model settings are print-only (`aiplex connect local --client antigravity`).

If the diff looks right, ask the user, then run it with `--yes` only after they agree:

```bash
aiplex connect local --client claude-code --write --yes
```

Options worth knowing: `--scope project` (claude-code, gemini-cli: write the project's files instead of the home directory), `--model <id>` to pick a model, `--token-helper` so the client runs `aiplex --context NAME auth print-token` for the context in effect instead of reading an environment variable (it needs a stored token from `aiplex login`; it is refused when credentials come only from the environment), and `--with-telemetry <otlp-endpoint>` to turn on the client's own OpenTelemetry export (prompt logging is never enabled). `--with-telemetry aiplex` sends the usage events to this deployment's own receiver, which records them as reported usage.

The command ends with "Still to do by hand". Those are shell environment variables a config file cannot hold, usually the access token. Tell the user exactly what it printed; for Claude Code that is `export ANTHROPIC_AUTH_TOKEN="$AIPLEX_TOKEN"` in the shell that starts `claude`. A plugin cannot set the base URL or the token itself, which is why this step edits the settings file.

To undo: `aiplex connect --undo --client claude-code`.

## 4. Verify with one request

Use the access point directly, so a failure is about AIPlex and not about the harness:

```bash
curl -s "$AIPLEX_URL/ap/local/v1/chat/completions" -H "Authorization: Bearer $AIPLEX_TOKEN" -H "Content-Type: application/json" -d '{"model":"mock-gpt","messages":[{"role":"user","content":"Hello"}]}'
```

For a remote access point, read the base URL from `aiplex connect <access-point>` and use a model it lists. Then ask the user to restart the harness so it reads the new settings.

## 5. Show the usage

```bash
aiplex usage records --since 1h
```

The request from step 4 should be there, with who made it and what it cost. A missing cost means unknown (no price is set for that model), not free. For deeper questions use the `aiplex-usage` skill.
