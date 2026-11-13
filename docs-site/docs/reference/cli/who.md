---
title: aiplex who
sidebar_label: who
description: "See every caller: how it signs in, what it can reach and its usage"
---

This page is generated from the output of `aiplex --help` for the CLI built from `cmd/aiplex-cli`. Regenerate it with `npm run gen:cli` in `docs-site/`.

```text
With a kind and an id, shows one caller in detail: how it authenticates, the
models, tools and skills it can reach with the reason for each, quota coverage,
and recent usage by model and tool.

With no arguments, lists every caller, one table per kind. With --kind (or
just a kind), lists callers of that kind with their identity, access points,
last activity and usage over --since (default 24h).

Unknown is shown as "unknown", never as zero. There is no user directory:
known users are those named by an access point or seen in the usage ledger.

Usage:
  aiplex who [agent|user|client] [id] [flags]

Examples:
  aiplex who
  aiplex who --kind agent
  aiplex who agent tutor-agent
  aiplex who user principal-ab12… --since 7d
  aiplex who --kind client -o json

Flags:
      --cursor string   Continue a listing from a previous next-cursor
  -h, --help            help for who
      --kind string     List callers of this kind: agent, user or client
      --limit int       Page size when listing (default 50, max 200)
      --since string    Usage window: a duration like 24h or 7d, or an RFC3339 time (default 24h)

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

