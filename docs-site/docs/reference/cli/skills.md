---
title: aiplex skills
sidebar_label: skills
description: "Inspect skill servers and skill invocations"
---

This page is generated from the output of `aiplex --help` for the CLI built from `cmd/aiplex-cli`. Regenerate it with `npm run gen:cli` in `docs-site/`.

```text
Inspect skill servers and skill invocations

Usage:
  aiplex skills [flags]
  aiplex skills [command]

Examples:
  aiplex skills servers
  aiplex skills invocations --agent tutor-agent

Available Commands:
  servers           List running SkillsPlex servers
  record-invocation Record a skill invocation audit event
  invocations       List recent skill invocations

Flags:
  -h, --help   help for skills

Global Flags:
      --context string   Use this CLI context for one command (env: AIPLEX_CONTEXT)
      --debug            Show debug output, including raw API errors
      --no-color         Disable colour (also: NO_COLOR=1)
  -o, --output string    Output format: table, wide (every column, untruncated), json or yaml (default "table")
      --token string     Bearer token (env: AIPLEX_TOKEN)
      --url string       AIPlex API URL (env: AIPLEX_URL)

Use "aiplex skills [command] --help" for more information about a command.

Exit codes:
  0  success
  1  error (including server errors)
  2  invalid input: bad flags or arguments, or a specification the server rejected
  3  not signed in, token rejected, or not permitted
  4  not found
  5  conflict: the resource changed since it was read, or already exists
  6  the API could not be reached
```

### `aiplex skills servers`

```text
List running SkillsPlex servers

Usage:
  aiplex skills servers [flags]

Examples:
  aiplex skills servers

Flags:
  -h, --help   help for servers

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

### `aiplex skills record-invocation`

```text
Record a skill invocation audit event

Usage:
  aiplex skills record-invocation [flags]

Examples:
  aiplex skills record-invocation --agent tutor-agent --instance skills-abc --skill summarize

Flags:
      --agent string      Agent ID (required)
  -h, --help              help for record-invocation
      --instance string   Skill server instance ID (required)
  -s, --skill string      Skill name (required)
      --trace string      W3C trace id (optional)
  -u, --user string       User ID

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

### `aiplex skills invocations`

```text
List recent skill invocations. --limit caps how many are returned (1 to 1000, default 100).

Usage:
  aiplex skills invocations [flags]

Examples:
  aiplex skills invocations
  aiplex skills invocations --agent tutor-agent --limit 20
  aiplex skills invocations -o json

Flags:
      --agent string   Filter by agent ID
  -h, --help           help for invocations
      --limit int      Most invocations to return (1 to 1000) (default 100)
  -s, --skill string   Filter by skill name

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

