> **Status:** Shipped. `kind=agent` and `kind=workflow` are first-class capability kinds.
> Companion to [18 — Capability Mesh](18-capability-mesh.md), [20 — MemPlex](20-memplex-memory-plane.md), [23 — Elegance vs SOTA](23-elegance-vs-state-of-art.md).

# 24 — Agent-as-Cap, Workflow-as-Cap

The architectural breakthrough this doc records: **agents themselves are capabilities, and workflows are capabilities too.** With this in place, AIPlex stops being a gateway-with-extras and becomes the capability OS for delegated AI.

This is what makes AIPlex categorically different from AWS Bedrock AgentCore and Google Vertex AI Agent Engine. Both treat Agent as a runtime concern with separate identity, audit, gateway, and memory subsystems bolted around it. AIPlex treats Agent as **just another `kind`** on the same capability primitive that already governs tools, tasks, models, skills, and memory.

---

## The two new kinds

### `kind=agent`

A `cap://agent/<name>@<version>` URI fronts an *external* agent runtime — an ADK agent on Vertex Agent Engine, a LangGraph deployment on Cloud Run, a self-hosted Letta instance, a custom HTTP service. AIPlex doesn't run the agent; it governs every call to it.

The cap URI is the contract. The runtime can change underneath (Vertex → Cloud Run → on-prem) and callers don't notice.

```yaml
kind: agent
template: adk-tutor
provider:
  spiffeId: spiffe://aiplex/.../sa/my-tutor
  endpoint: https://my-tutor.run.app
capability:
  uri: cap://agent/tutor@v1
  auth: { requiredActions: [invoke] }
```

When the cap is invoked, AIPlex (a) checks the caller's `caps` claim, (b) forwards to the provider endpoint with the act-claim chain intact, (c) emits a parent receipt, (d) auto-records child receipts when the agent's internal tool calls come back through AIPlex.

### `kind=workflow`

A `cap://workflow/<name>@<version>` URI points at a declarative spec that AIPlex itself executes. The spec is a list of steps; each step calls another cap (a tool, model, memory namespace, agent, or another workflow). Outputs from earlier steps interpolate into later steps via `{{ inputs.foo }}` and `{{ steps.<id>.output.bar }}` templates.

```yaml
spec:
  inputs:
    required: [quiz_id, student]
  steps:
    - id: fetch
      cap: cap://tool/get_quiz@v1
      action: call
      input: { id: "{{ inputs.quiz_id }}" }
    - id: grade
      cap: cap://model/gemini-2.5-flash@v1
      action: complete
      input: { prompt: "Grade: {{ steps.fetch.output.content }}" }
    - id: store
      cap: cap://memory/students/{{ inputs.student }}/grades@v1
      action: write
      input:
        key: "quiz-{{ inputs.quiz_id }}"
        data: { grade: "{{ steps.grade.output.text }}" }
  outputs:
    grade: "{{ steps.grade.output.text }}"
```

The workflow executor lives in `internal/workflow/executor.go`. It runs steps sequentially today (parallel, conditional, and loop are easy additions because the executor is ~250 LOC of straightforward state-machine code). Each step invocation goes through the gateway like any other cap call, so authz, rate limits, audit, and policy-denial recording all apply uniformly.

Spec lives on `Template.Config["spec"]` and is read by the workflow `KindHook` at deploy time. See `internal/catalog/builtin_workflows.go` for two ready-to-deploy examples (`grade-quiz`, `research-and-summarise`).

---

## Why this is the breakthrough

A hosted agent service that doesn't make agents first-class addressable resources can't actually express:

- **"Catalog all the agents I can call."** AgentCore: agents are AWS Lambda-shaped objects in your account; no canonical catalog. Agent Engine: agents are deployments in your project; you discover them via gcloud/IAM. AIPlex: `aiplex catalog list --kind agent` (and `cap://agent/*` is the universal address for any agent on any AIPlex node).

- **"Revoke this agent for this user, surgically."** AgentCore/Vertex: revocation is an IAM policy change; affects everyone. AIPlex: drop one entry from one user's cap claim; sub-second propagation, no service interruption, no other user affected.

- **"Show the full audit trail for this multi-agent flow."** AgentCore/Vertex: join across CloudTrail/Cloud Logging streams from multiple services. AIPlex: one chained receipt sequence — workflow run + every step + every nested agent's internal cap calls — because they're all caps.

- **"Move this agent from Vertex to AWS without changing caller code."** AgentCore/Vertex: not viable; the API surface differs and identity/memory have to be migrated. AIPlex: change the provider endpoint behind `cap://agent/tutor@v1`; the URI is unchanged.

- **"Compose an agent on Vertex with another agent on Bedrock under one delegation chain."** AgentCore/Vertex: parallel governance stacks + custom A2A glue. AIPlex: a workflow cap chains both with one token and one receipt sequence.

These properties don't come from any individual feature — they come from the agent being *the same kind of thing* as a tool, a memory namespace, or a model. One primitive, every property follows.

---

## Architecture in code

The total cost of agent-as-cap + workflow-as-cap on top of the Capability Mesh:

| File                                          | Purpose                                             | LOC |
|-----------------------------------------------|-----------------------------------------------------|-----|
| `internal/capability/kinds.go`                | `KindAgent` + `KindWorkflow` registered             | +14 |
| `internal/capability/uri.go`                  | `Kind.Namespace()` for new kinds                    | +4  |
| `internal/workflow/types.go`                  | Spec, Step, Run, StepResult                         | 80  |
| `internal/workflow/template.go`               | Safe `{{ path }}` interpolation, type-preserving    | 130 |
| `internal/workflow/executor.go`               | Sequential step executor + HTTP cap invoker         | 270 |
| `internal/workflow/server.go`                 | `/cap/workflow/*` HTTP handler                      | 130 |
| `internal/workflow/hook.go`                   | Implements `deploy.KindHook` for kind=workflow      | 60  |
| `internal/workflow/executor_test.go`          | 6 unit tests                                        | 200 |
| `internal/catalog/builtin_workflows.go`       | Two ready-to-deploy workflow templates              | 130 |
| `internal/catalog/builtin_agents.go`          | Two agent-runtime wrapper templates (ADK, Letta)    | 60  |
| `internal/memplex/broker.go` (additions)      | Universal `_invoke` action for every kind           | +120 |
| `internal/deploy/manifests.go` (additions)    | No K8s manifests for agent/workflow/memory kinds    | +1   |
| `internal/deploy/engine.go` (additions)       | `mergeConfig` so template defaults reach the hook   | +20 |
| `sdk/aiplex/client.go` (additions)            | `client.Workflow(uri).Run`, `client.Agent(uri)`     | +90 |
| `tests/integration_test.go` (additions)       | E2E: deploy workflow, chain 3 caps, verify receipts | +130 |

**Total: ~1,300 LOC** to make agents and workflows first-class governable resources. For comparison: equivalent capability across AgentCore's Runtime + Identity + Gateway + Observability is multiple repos and several services; in Agent Engine + Vertex IAM it's similarly distributed. The Capability Mesh did the heavy lifting.

---

## How calls flow

End-to-end, the moment a caller invokes `cap://workflow/grade-quiz@v1`:

```
Client SDK
  POST /cap/workflow/grade-quiz@v1/_invoke   (Bearer: <user-delegated token>)
       │
       ▼
Envoy AI Gateway (in production; chi router in tests)
  ┌─────────────────────────────────────────────────────────┐
  │ capability resolver  → metadata{uri, action="run"}      │
  │ ext_authz            → checks token.caps grants run     │
  │ constraint filter    → enforces max_concurrent_runs     │
  └─────────────────────────────────────────────────────────┘
       │
       ▼
workflow.Server.handleInvoke
       │
       ▼
workflow.Executor.Run(ctx, token, uri, caller, inputs)
       │
       ▼ for each step
  HTTPInvoker.Invoke(ctx, token, capURI, action, input)
       │  (forwards bearer token; adds new act-claim hop downstream)
       ▼
  POST /cap/<kind>/<step-cap>@<v>/_invoke   (Bearer: <same token>)
       │
       ▼
  ─── back to the gateway ─── (each step takes the same authz/audit path)
       │
       ▼
  step output JSON
       │
       ▼
  templated into next step's input
```

Three properties this gives you for free:

1. **Every step is governed.** Each cap invocation goes through the gateway, ext_authz, and the constraint filter. There's no "trusted internal" path that bypasses policy. A workflow step calling `cap://memory/...` is the same shape as a human directly calling that memory cap from a CLI.

2. **Audit chain is continuous.** Every step produces its own receipt with the workflow run as parent_id. The full audit reads top-down: workflow run → step → (if the step was an agent) → that agent's internal tool calls → tool results. One sequence, one query.

3. **Token threading preserves delegation.** The original user's bearer token rides every hop. Downstream caps see "Alice delegated tutor agent to call this workflow which is calling this memory write" as a single chain via the `act` claim, RFC 8693-style. Revoke the user's grant → every hop in flight 401s on the next attempt.

---

## Specifically vs. AgentCore and Agent Engine

Concrete operational scenarios where the difference shows up:

### Scenario A: switching LLM providers mid-semester

**AgentCore:** Agents are bound to Bedrock models. Switching to Gemini means re-coding agent definitions, re-testing tool integrations, re-binding identity, migrating memory stores. The audit trail before and after the switch lives in different log shapes.

**Agent Engine:** Agents are typically built with ADK + Vertex models. Same story going the other way — switching to Bedrock is a rewrite. Both vendors' "memory bank" services are non-portable.

**AIPlex:** The tutor agent's URI is `cap://agent/tutor@v1`. Behind it, the model cap is `cap://model/gemini-2.5-flash@v1`. Switching to Claude is *one cap claim change* in the workflow spec — `cap://model/claude-sonnet-4@v1`. Tutor URI, student grants, memory namespaces, audit chain: all untouched.

### Scenario B: parent reads what tutor did with my child

**AgentCore/Vertex:** Customer accounts are AWS/GCP accounts. There is no first-class concept of "Alice (a student) is the human delegating tutor agent." The AWS/GCP user *is* the school district admin. To give a parent audit access, you build a custom export-and-redact pipeline.

**AIPlex:** Alice is a real principal in the system. She delegates `cap://agent/tutor@v1` with structured constraints. Every receipt records `principals.user = alice@school.edu`. Parent gets a `cap://memory/students/alice/audit@v1` read grant; surgical, signed, live. Receipt chain across the tutor's internal tool calls is automatically scoped to Alice.

### Scenario C: cross-vendor agent composition

**AgentCore/Vertex:** Each cloud's agents talk natively to that cloud's agents. A2A protocol bridges them at the wire, but identity, audit, and cost stay siloed per cloud.

**AIPlex:** A `cap://workflow/...` chains caps from any provider. Step 1 invokes `cap://agent/agentcore-research@v1` (provider points at AgentCore endpoint). Step 2 invokes `cap://agent/vertex-summarize@v1` (provider points at Vertex). Same delegation chain, same receipt sequence, same cost attribution by user.

### Scenario D: surgical revocation

**AgentCore/Vertex:** "This tutor accidentally got access to the disciplinary records database. Revoke for all minors immediately." Both vendors: IAM policy rewrite + agent restart + cache invalidation. 30+ seconds of partial-state risk.

**AIPlex:** Run `aiplex caps revoke --user '*@school.edu' --cap cap://memory/students/*/discipline@v1`. Sub-second propagation through the constraint filter; in-flight requests 401 on next hop; no agent restart, no service interruption.

### Scenario E: family running this on a Synology NAS

**AgentCore/Vertex:** Not their market.

**AIPlex:** Same protocol, same SDK, same UX — `aiplex up` runs the whole stack as a single binary. The cap URI doesn't know or care whether the broker is on a NAS or in GKE.

---

## What's not yet shipped (and how it slots in)

- **`aiplex up` single-binary local stack.** Same workflow + agent caps run; the binary embeds OPA, an in-memory store, and a sqlite receipt sink. ~3 days.
- **Personal cap vault.** `aiplex vault export` produces a portable bundle of grants. Imports on any AIPlex node. ~2 days.
- **Live receipt stream in the Console.** SSE-streams workflow runs as they execute, drilling into nested cap calls. ~3 days.
- **Code interpreter as a built-in cap.** AgentCore has one; we don't yet. Ships as `cap://tool/code-interpreter@v1` template. ~2 days.
- **Parallel and conditional workflow steps.** Sequential is the current executor; parallel + `if`/`when` are state-machine extensions. ~2 days.

None of these change the architecture. They each compose on top of agent-as-cap + workflow-as-cap.

---

## The categorical claim

Before this doc landed: "AIPlex is a unified control plane for AI agent interactions."

After this doc lands: **AIPlex is the capability OS for delegated AI.** Agents are first-class caps. Workflows are first-class caps. Every action is a typed, addressable, governable, revocable resource bound to the human who delegated it. Vendor-portable. Locally hostable. Verifiable.

That's not a different feature; it's a different category. AgentCore and Agent Engine are vendors' managed agent stacks. AIPlex is the user-owned substrate beneath whatever runtime the agent happens to use.

---

## See also

- [18 — Capability Mesh](18-capability-mesh.md)
- [19 — CapabilityRoute](19-capability-route-crd.md)
- [20 — MemPlex](20-memplex-memory-plane.md)
- [21 — Runtime Consent & Trust Ledger](21-runtime-consent-and-trust-ledger.md)
- [23 — Elegance vs SOTA](23-elegance-vs-state-of-art.md)
- `internal/workflow/`, `internal/catalog/builtin_workflows.go`, `internal/catalog/builtin_agents.go`, `tests/integration_test.go:TestE2E_WorkflowAsCapability`
