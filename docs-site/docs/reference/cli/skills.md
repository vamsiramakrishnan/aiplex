---
title: aiplex skills
sidebar_label: skills
description: "SkillsPlex — manage skill servers and invocations"
---

This page is generated from the output of `aiplex --help` for the CLI built from `cmd/aiplex-cli`. Regenerate it with `npm run gen:cli` in `docs-site/`.

```text
SkillsPlex — manage skill servers and invocations

Usage:
  aiplex skills [command]

Available Commands:
  invocations       List recent skill invocations
  record-invocation Record a skill invocation audit event
  servers           List running SkillsPlex servers

Flags:
  -h, --help   help for skills

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)

Use "aiplex skills [command] --help" for more information about a command.
```

### `aiplex skills invocations`

```text
List recent skill invocations

Usage:
  aiplex skills invocations [flags]

Flags:
      --agent string   Filter by agent ID
  -h, --help           help for invocations
  -s, --skill string   Filter by skill name

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

### `aiplex skills record-invocation`

```text
Record a skill invocation audit event

Usage:
  aiplex skills record-invocation [flags]

Flags:
      --agent string      Agent ID (required)
  -h, --help              help for record-invocation
      --instance string   Skill server instance ID (required)
  -s, --skill string      Skill name (required)
      --trace string      W3C trace id (optional)
  -u, --user string       User ID

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

### `aiplex skills servers`

```text
List running SkillsPlex servers

Usage:
  aiplex skills servers [flags]

Flags:
  -h, --help   help for servers

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

