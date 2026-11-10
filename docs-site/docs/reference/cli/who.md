---
title: aiplex who
sidebar_label: who
description: "See every caller: how it authenticates, what it can reach and why, and its usage"
---

This page is generated from the output of `aiplex --help` for the CLI built from `cmd/aiplex-cli`. Regenerate it with `npm run gen:cli` in `docs-site/`.

```text
With a kind and an id, shows one caller in detail: how it authenticates, the
models, tools and skills it can reach with the reason for each, quota coverage,
and recent usage by model and tool.

With --kind (or just a kind), lists callers of that kind with their identity,
access points, last activity and usage over --since (default 24h).

Unknown is shown as "unknown", never as zero. There is no user directory:
known users are those named by an access point or seen in the usage ledger.

Usage:
  aiplex who [agent|user|client] [id] [flags]

Examples:
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
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

