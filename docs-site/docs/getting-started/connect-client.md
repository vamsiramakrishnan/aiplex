---
title: Connect a client
description: Create an access point and print ready-to-paste configuration for Claude, Codex or Antigravity.
---

# Connect a client

To see the output of `aiplex connect` without setting anything up, start [`aiplex dev`](./local-development.md) and run `aiplex connect local`. The named client profiles (`--client claude-code`, `codex`) apply only to an access point that offers a wire the client speaks. The `local` access point offers the four mock wires (OpenAI Chat, OpenAI Responses, Anthropic Messages and Gemini), so `claude-code`, `codex` and `antigravity` apply to it. `--client claude-desktop` is refused on any access point, with a reason: that client signs in through a Claude apps gateway server.

An [access point](../guides/access-points.md) gives a group of callers one stable URL, `https://<aiplex>/ap/{id}/…`, in front of models, MCP tools and skills. `aiplex connect` prints the URLs and configuration a client needs for it.

## 1. Create an access point

For the common case, create it from flags. `--model name=surface` exposes a model of a saved model surface under a client-facing name, `--tool` exposes a tool connection, and `--allow-groups` admits a group. With no `--allow-*` flag, every verified caller of the kinds in `--callers` (default `user,agent,client`) is admitted:

```bash
aiplex access-points create ide-platform-team --model gpt-4o-mini=openai --tool search --allow-groups eng
```

For the full specification (QoS classes, expose lists, the fields in [Access points and the single pane](../library/access-points.md#specification)), write a specification file. The references (`surface_ref`, `connection_ref`) must name a model surface and tool connection that already exist in your tenant.

```bash
aiplex access-points apply -f ide.yaml
aiplex access-points get ide-platform-team
aiplex access-points quota-plan ide-platform-team   # only if the spec has a qos block
```

## 2. Print the client configuration

```bash
aiplex connect ide-platform-team
aiplex connect ide-platform-team --client claude-code
aiplex connect ide-platform-team -o json
```

`--client` takes a client profile (for example `claude-code`, `codex`, `antigravity`) or `generic` for environment variables and curl. Snippets read the token from `$AIPLEX_TOKEN`; no token or secret is printed. `aiplex connect` needs `AIPLEX_PUBLIC_URL` set on the API to the trusted HTTPS origin; without it the API answers `409 PUBLIC_URL_REQUIRED`.

Any verified caller that the access point admits can run `aiplex connect`. Operators get the full output. Other callers get a reduced one, with only their own caller kind and auth step. A caller the access point does not admit gets `not found`.

To apply it instead of copying it, add `--write` (`claude-code`, `codex` or `gemini-cli`). The command merges the settings into the client's own config file after showing a diff, keeps a backup, never writes a token value, and `aiplex connect --undo` restores the backup. It does not install the client. See [Harness onboarding](../library/harness-onboarding.md).

```bash
aiplex connect ide-platform-team --client claude-code --write --dry-run
aiplex connect ide-platform-team --client claude-code --write
```

## 3. Check what the client can reach

```bash
aiplex who --kind client
aiplex who agent AGENT_ID
```

## ClientPlex plans

To plan configuration for a client without an access point, use ClientPlex:

```bash
aiplex clients profiles
aiplex clients register --help
aiplex clients plan --help
```

See the [ClientPlex guide](../guides/clientplex.md).

`aiplex connect <access-point> --client <client> --write` merges the configuration into the client's own file after showing a diff. It covers `claude-code`, `codex`, `gemini-cli` and `antigravity`; for Antigravity only its MCP server list is written, and its model settings stay print-only ([Harness onboarding](../library/harness-onboarding.md)). Add `--with-telemetry aiplex` to send the client's usage events to this deployment's receiver as reported records; the token is never written to the file.
