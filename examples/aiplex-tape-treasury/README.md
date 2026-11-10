# AIPlex + Tape — Treasury Agent (end-to-end)

## What this example does

A treasury agent runs on Tape's durable runtime, AIPlex governs its
scope grants and surfaces its journal in the Console (see
[`docs-site/docs/guides/runs.md`](../../docs-site/docs/guides/runs.md)).

This directory contains the **AIPlex side** — the deployment manifest
that wires the agent into AIPlex's deploy engine + auth + audit. The
**agent code itself** lives in the `durable-agents` repo at
[`tape/examples/standalone/aiplex-integration/`](https://github.com/vamsiramakrishnan/durable-agents/tree/main/tape/examples/standalone/aiplex-integration).
Together they cover the flow: AIPlex deploys, Tape executes, AIPlex
observes.

**Requirements.** Everything except the in-process smoke test needs the
separate `durable-agents` (Tape) repository checked out next to this one
(`make dev-tape` expects `../durable-agents` and stops with an error if it is
missing). Without it, only `make e2e-aiplex-tape` runs; it simulates Tape in
process.

## Flow

```
┌──────────────────┐
│ aiplex apply -f  │  → AIPlex deploy engine reads runtime: {engine: tape}
│   treasury.yaml  │     → generates tape-server + reactors in aiplex-system
└────────┬─────────┘     → injects AIPLEX_* + TAPE_URL on the agent pod
         │
         ▼
┌──────────────────┐
│  agent pod boots │  durable_app(identity=RunIdentity.from_env())
│  (Python ADK)    │  → every BeginRun carries tenant / actor / scopes
└────────┬─────────┘
         │
         ▼
┌──────────────────┐
│ tool calls fly   │  @tape.effect(scope="mcp:tools:bank_wire", ...)
│                  │  → SDK pre-checks scope ∈ run.scopes
│  bank_wire(...)  │  → server re-checks at BeginEffect
│                  │  → journal records: begin
└────────┬─────────┘
         │ ───── pod crashes mid-call ─────
         ▼
┌──────────────────┐
│ Tape reconciler  │  asks the bank: did this business_key land?
│                  │  → yes → write effect.confirmed (no duplicate!)
└────────┬─────────┘
         │
         ▼
┌──────────────────┐
│ outbox relay     │  POST /internal/tape/events to AIPlex
│                  │  → idempotent on (run_id, seq)
└────────┬─────────┘
         │
         ▼
┌──────────────────┐
│ AIPlex Console   │  /runs shows the timeline:
│                  │    ▶ run started · ◆ decision · ⟶ effect.begin
│                  │    · ? UNKNOWN · ✓ effect.confirmed · ✓ completed
└──────────────────┘
```

## Running it

### 1. The smoke test (no external infra)

In-process E2E test that exercises every layer of the pipeline. Runs
in under a second. Asserts the headline claim: a crash mid-wire does
not produce a duplicate wire.

```bash
make e2e-aiplex-tape
```

Source: [`tests/aiplex_tape_e2e_test.go`](../../tests/aiplex_tape_e2e_test.go).

### 2. Local development (real tape-server, real AIPlex API)

Spin up AIPlex against an in-memory store + the agent pod against a
local tape-server, then drive the agent and watch the Console:

```bash
# Terminal 1 — local AIPlex API + console
make dev

# Terminal 2 — Tape server + the agent (in the sibling repo)
cd ../durable-agents
cargo build --release --manifest-path tape/server/Cargo.toml
./tape/server/target/release/tape-server &
cd tape/examples/standalone/aiplex-integration
export AIPLEX_TENANT_ID=acme
export AIPLEX_AGENT_ID=treasury-agent
export AIPLEX_ACTOR=spiffe://acme/ns/a2aplex/sa/treasury-agent
export AIPLEX_SUBJECT=$(whoami)@example.com
export AIPLEX_SCOPES="mcp:tools:read_balance mcp:tools:bank_wire"
python run.py

# Then open the Console http://localhost:5173/runs
```

### 3. Production deploy

With the deploy engine wired against a cluster, `treasury.yaml` generates
the tape-server Deployment, Service and reactors Deployment in `aiplex-system`
(image `ghcr.io/vamsiramakrishnan/tape-server:latest`), the agent's
ServiceAccount, Deployment, Service and NetworkPolicy, and the `AIPLEX_*` and
`TAPE_URL` environment variables on the agent pod. Applying them to a live
cluster has not been exercised in this repository:

```bash
aiplex apply -f examples/aiplex-tape-treasury/treasury.yaml
```

## What it shows

| Claim | Evidence |
| --- | --- |
| Manifest generation includes the Tape runtime resources | `tests/aiplex_tape_e2e_test.go::TestE2E_AIPlexTape_TreasuryStory` — asserts `Instance.Runtime.Engine == tape` on the deployed instance + the engine emits tape-server + reactors |
| Identity (tenant/actor/subject/agent_id/scopes) is threaded onto every run | Same test seeds events with the deployed instance's ID; the run projection surfaces them via `GET /api/v1/runs/{id}` |
| A crash mid-effect doesn't duplicate the side effect | `EffectsCount == 3` (begin + unknown + confirmed) — NOT 4 (no second begin), `UnknownEffects == 1` (transitioned, not duplicated) |
| Scope denials are auditable | `TestE2E_AIPlexTape_PolicyDenialFlow` — `PolicyViolations == 1`, `EffectsCount == 0` (no fake effect from a denied attempt), and the run shows up in the timeline |
| Console-side reads round-trip the full timeline | `/api/v1/runs/{id}/events` returns the 6-event sequence in seq order; the Runs page consumes this every 3 seconds |
