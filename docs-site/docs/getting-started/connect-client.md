---
title: Connect a client
description: Create an access point and print ready-to-paste configuration for Claude, Codex or Antigravity.
---

# Connect a client

An [access point](../guides/access-points.md) gives a group of callers one stable URL, `https://<aiplex>/ap/{id}/…`, in front of models, MCP tools and skills. `aiplex connect` prints the URLs and configuration a client needs for it.

## 1. Create an access point

Write a specification file. The fields are defined in [Access points and the single pane](../library/access-points.md#specification); the references (`surface_ref`, `connection_ref`) must name a model surface and tool connection that already exist in your tenant.

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
