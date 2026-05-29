---
id: embedded-tier-decision
title: Tape — scale tier vs embedded tier
sidebar_label: Tape tiers
---

# Tape — scale tier vs embedded tier

Tape ships in two tiers (see
[`tape/SDK_PARITY.md`](https://github.com/vamsiramakrishnan/durable-agents/blob/main/SDK_PARITY.md)):

- **Scale tier** — the Rust `tape-server` + gRPC clients in four languages.
- **Embedded tier** — `tape-adk`, a Python package that extends ADK's
  `DatabaseSessionService` with the four extra primitives ADK doesn't
  ship (`UNKNOWN`, outbox, reconciler, compensation).

This page documents AIPlex's stance on which tier it governs.

## Decision

**AIPlex targets the scale tier. The embedded tier is dev-only.**

For production deployments, AIPlex provisions a `tape-server` per
environment (see `runtime: { engine: tape }` in the
[Tape runtime guide](./tape-runtime.md)) and treats the embedded tier
as out of scope. The Console "Runs" tab will be empty for instances
whose Tape state lives in `tape-adk`'s SQLite file rather than in
`tape-server`'s journal.

## Why

Two reasons.

### 1. Identity is a scale-tier concept

The
[AIPlex ↔ Tape identity contract](https://github.com/vamsiramakrishnan/durable-agents/blob/main/tape/docs/integrations/aiplex.md)
attaches `tenant_id`, `actor`, `subject`, `agent_id`, `scopes`, and
`labels` to every `BeginRunRequest` and persists them as indexable
columns on `tape_runs`. That schema only exists on the scale tier.

The embedded tier's SQL schema sits underneath ADK's
`DatabaseSessionService`; threading the identity columns through it
would require:

- Adding the columns to the embedded tier's tables in Python.
- Mirroring the schema across the TS / Go / Java embedded
  implementations (they all promise column-identical SQLite for
  cross-language compatibility — see G2 in `SDK_PARITY.md`).
- Updating each tier's `begin_*` paths to accept and persist
  identity.
- Updating the embedded chaos + invariant test suites.

That's a quarter of work for a tier whose value proposition is "no
separate server." A control plane (AIPlex) is exactly what the
embedded tier is supposed to avoid; bolting one on negates the
upside.

### 2. The product narrative is single-tier

AIPlex's "one stop shop for production agents" pitch is one
deployable substrate: agents are deployed (`aiplex apply`), Tape's
server is provisioned alongside them, the journal flows back into
AIPlex audit, the Console shows the timeline. Two tiers means two
operating models, two storage stories, two failure modes. The
embedded tier serves a different need (lowest-friction local dev,
demos, single-process agents).

The Console's empty state on the Runs page acknowledges this: it
shows a checklist that includes "Tape ingestion seen recently" (see
[`/api/v1/runs/_health`](../guides/tape-runtime.md#diagnostics)). An
embedded-tier deployment will never tick that box, and that's the
correct experience — the Console isn't lying about silence; it's
pointing at the gap.

## What this means in practice

- An `Instance.Runtime.Engine = "tape"` provisions a `tape-server`
  pod and an outbox sink to AIPlex (see PRs 5 + 11 item 1).
- `Instance.Runtime.Engine = "none"` is the v1 path: no durable
  runtime, no AIPlex audit projection. Use this for stateless agents
  that don't need replay.
- `tape-adk` is supported as a development substrate (`tape dev` /
  `python -m tape_adk`) but is not a deploy target AIPlex's engine
  produces manifests for. Mixed deployments are not a goal.

## Cross-references

- [`tape/SDK_PARITY.md`](https://github.com/vamsiramakrishnan/durable-agents/blob/main/SDK_PARITY.md)
  G9 — the corresponding entry on the Tape side documents this same
  decision from the SDK perspective.
- [Tape runtime guide](./tape-runtime.md) — the user-facing how-to
  for the scale tier.
- [`durable-agents/tape/docs/integrations/aiplex.md`](https://github.com/vamsiramakrishnan/durable-agents/blob/main/tape/docs/integrations/aiplex.md) —
  the wire contract.

## Revisit conditions

This decision is revisitable if:

- A concrete customer ships an embedded-tier production agent at
  scale where AIPlex governance is a real requirement.
- The Tape team chooses to retire the embedded tier (then this
  document becomes "embedded tier was a thing once").
- A future Tape primitive blurs the distinction (e.g. an embedded
  tier that *can* connect to an external `tape-server` for audit
  fan-out without sacrificing its single-process latency story).

Until one of those happens, the scale tier is the production answer
and the embedded tier is the local-dev answer.
