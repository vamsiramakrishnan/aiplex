---
title: Connect a harness
description: Point a coding harness at an AIPlex access point, review its settings and check a real call.
---

# Connect a harness

Start [the local stack](./local-development.md). It creates the `local` access point with four model protocols and a mock MCP server. Preview the configuration for your installed harness.

```bash
aiplex connect local --client claude-code --write --dry-run
```

Choose the profile that matches your harness. `--client` selects a configuration format. Access comes from the identity you present and the grants on each model and tool.

| Harness | Profile | What `--write` configures |
| --- | --- | --- |
| Claude Code | `claude-code` | Anthropic Messages settings and MCP servers |
| Codex | `codex` | Responses provider and MCP servers in `config.toml` |
| Gemini CLI | `gemini-cli` | Gemini settings and MCP servers |
| Antigravity | `antigravity` | Local stdio MCP server entry. The CLI prints the remaining model and remote MCP setup steps. |

## 1. Create an access point

Skip this step when using `local`. For a team, expose a saved model surface that speaks the harness’s protocol and a tool connection from your tenant. This example gives the `eng` group access to the local stack’s mock Claude model and tools.

```bash
aiplex access-points create ide-platform-team \
  --model mock-claude=local-messages --tool local-tools --allow-groups eng
```

Set `AIPLEX_PUBLIC_URL` on a remote API to the trusted HTTPS origin clients can reach. The local stack sets its own origin. For a specification with QoS or individual tool allowlists, use `aiplex access-points apply -f ide.yaml`. [Access point specification](../library/access-points.md#specification)

## 2. Print the client configuration

```bash
aiplex connect local                         # URLs, environment variables and curl
aiplex connect local --client claude-code     # settings to apply manually
aiplex connect local --client claude-code --write
```

Use your team’s access point ID in place of `local`. The write shows a diff, asks for confirmation and backs up the original files. Complete any printed authentication steps. Config files hold environment references or a token helper. To restore your files, run `aiplex connect --undo --client claude-code`. [File locations, token helpers and backups](../library/harness-onboarding.md)

## 3. Check what the client can reach

Start the harness, send a prompt to the configured model, then run `aiplex usage records --since 1h` to find the managed call. Check the caller, model and access point. Operators can inspect effective access for a registered client with `aiplex who client REGISTRATION_ID`. A harness using your workforce token can appear under your user identity.

For Claude Code, Codex or Gemini CLI, add `--with-telemetry aiplex` when writing settings to configure the client’s own telemetry export. Complete the authentication steps it prints. The ledger marks those events as `reported`. [Harness telemetry](../library/harness-onboarding.md#telemetry-into-the-usage-ledger)

## ClientPlex plans

Use a [ClientPlex registration](../guides/clientplex.md) to review a client’s destination, protocol and configuration. Claude Desktop uses a native Claude apps gateway sign-in. Antigravity ACP supports printed configuration. To use an access point as the model backend for several harnesses running under Branchyard, choose the [Branchyard target](../library/harness-onboarding.md#branchyard).
