---
title: Access points
description: One stable URL in front of models, tools and skills that checks who is calling before any work happens.
---

# Access points

An access point is a tenant resource that gives a group of callers one stable URL, `https://<aiplex>/ap/{id}/…`, in front of models, MCP tools and skills.

Before a request reaches a model or a tool, the access point checks who the caller is, whether that kind of caller (agent, user or client) may use it, and whether the caller is among its subjects. An optional QoS block renders to reviewable [QuotaPlex](../guides/quotaplex.md) controls and fails closed when no control covers it.

```text
caller ──► /ap/{id}/… ──► admission ──► model / MCP tool / skill
                            │
                            └──► usage ledger record
```

An access point does not translate between model wire protocols and does not intercept traffic that bypasses it. A caller that goes straight to a provider is outside its enforcement and outside its usage records.

Read [Access points and the single pane](../library/access-points.md) for the specification, routes and admission rules, and the [guide](../guides/access-points.md) for the CLI.
