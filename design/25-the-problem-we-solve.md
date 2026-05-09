# 25 — The Problem We Solve

> **Status:** North star. The single criterion every feature debate reduces to.

## The problem in one sentence

**Delegating to AI is uncontrollable, unverifiable, unportable.** AIPlex makes every agent action a Capability — a typed, addressable, human-bound, governable, revocable, verifiable resource — so delegation finally has a primitive.

## The five voices of the same problem

| Who | What they say today |
|---|---|
| **End user** (Alice the student, or her parent) | *"I don't know what this AI is doing in my name. I can't stop it precisely. When something goes wrong I can't prove what it touched."* |
| **Developer** building agentic products | *"Every customer wants their agents bound to their users with their governance. I'm spending weeks per platform reinventing IAM, audit, and orchestration glue. None of it ports."* |
| **Enterprise admin** | *"Our employees are spinning up shadow agents that touch production data. I have no audit, no budget control, no way to revoke one user's grant without breaking everyone."* |
| **Compliance officer** | *"When the regulator asks 'what did the agent see and do for which patient,' we can't answer with confidence. Our logs aren't even cryptographically integral."* |
| **Founder of an AI product** | *"I'm being forced into the identity/governance/audit business when I just want to ship the product. And whatever I build will rot when the model vendor changes."* |

Five people, one underlying gap: **AI lacks the delegation primitive every prior computing era built.**

## The primitive gap, named

Every successful computing era is defined by a delegation primitive that turned a hard problem into a solved one:

| Era | Hard problem | Primitive that solved it |
|---|---|---|
| Operating systems | "Run multiple programs without one corrupting another" | The **process** — typed, isolated, identifiable, schedulable |
| Networks / Web | "Address resources across machines" | The **URL** — typed, addressable, dereferenceable, cacheable |
| Cloud | "Authorize machines and humans against shared resources" | The **IAM principal + role** — identity-scoped, policy-bound, auditable |
| AI agents (today) | "Govern what agents do on someone's behalf" | **Nothing.** We're nailing OAuth and MCP servers together with vendor glue. |

We treat agents like humans with API keys. That misfit is the source of every symptom in the table above. Every "AgentCore Identity service" or "Vertex agent ACL" is a bandage on the missing primitive — they treat the symptom (governance) rather than building the abstraction that makes the symptom impossible.

The Capability is that primitive.

```
cap://<kind>/<name>[/<sub-path>]@<version>
```

A Capability is a typed, addressable, governable unit of agent action. The URI is the unit of:
- catalog browsing
- deployment
- routing
- policy
- audit
- SDK invocation
- revocation
- cost attribution
- end-user delegation

**One identifier; every cross-cutting concern keys on it.**

Once that primitive exists, the symptoms dissolve:
- **Governance** isn't a service to wire up — it's reading the cap claim in the JWT.
- **Audit** isn't a log pipeline — it's the chain of receipts a cap call leaves behind.
- **Revocation** isn't a re-deploy — it's removing one cap entry from one user's claim.
- **Portability** isn't a migration project — same cap URI works against any AIPlex node.
- **Composition** isn't custom orchestration — it's a workflow cap calling other caps.
- **Local-vs-cloud** isn't two products — it's the same protocol with different deployment shapes.
- **Per-user cost attribution** isn't custom plumbing — every receipt records the human principal.

## The test

Every feature debate, every priority call, every architectural choice reduces to one question:

> **Does this make the delegation primitive more real to the people who need it?**

Pass / fail by stakeholder lens:

- Does it let the **end user** see, constrain, or revoke what an agent does in their name?
- Does it save the **developer** weeks of glue per platform?
- Does it give the **admin** surgical control with kernel-grade isolation?
- Does it give the **compliance officer** a verifiable trail they can hand to a regulator?
- Does it free the **founder** from the IAM/audit/governance business?

If the answer to all five is no, the feature is decoration — even if it's elegant.

## The test, applied

What passes:

| Feature | Why it passes |
|---|---|
| **`aiplex up` (single-binary local stack)** | Puts the primitive in 30 seconds onto a laptop / NAS. End user gets data sovereignty. Developer gets a frictionless dev loop. |
| **Per-cap sandboxing (process / container / microVM)** | Makes the primitive *trustworthy*. Admin gets blast-radius guarantees that don't depend on policy adherence. |
| **Live receipt streams in the Console** | Makes the primitive *visible*. Compliance officer sees the chain in real time. |
| **Personal cap vault (`vault export/import`)** | Makes the primitive *yours*. Founder doesn't lock customers in. |
| **Workflow caps + agent caps** | Makes the primitive *recursive*. One IAM/audit/cost story across multi-agent flows. |
| **Signed receipt chain (sigstore-anchored)** | Makes the primitive *verifiable*. Regulator gets cryptographic evidence. |
| **JIT step-up consent** | Makes the primitive *humane*. End user approves with full context, not at registration time. |
| **Code interpreter as a built-in cap** | Makes the primitive *useful*. Most-asked-for tool, governed like everything else. |

What does not pass (decorations to deprioritise even when they look attractive):

| Tempting feature | Why it fails the test |
|---|---|
| Better dashboards that don't change what the primitive can do | No stakeholder gains anything they couldn't already do |
| One more LLM provider integration | They're caps already; one more is data, not architecture |
| A new kind that doesn't represent a new way humans delegate | Adds surface without adding primitive coverage |
| Vendor-specific UX (e.g. an "AWS-native" mode) | Re-introduces lock-in; the whole point of the primitive is to escape that |
| Marketing-grade benchmarks vs competitors | Comparison is downstream; the primitive either exists or doesn't |

## Why this framing wins

The competitor map has dozens of companies attacking pieces of the symptom space:

- **AgentCore / Vertex Agent Engine** — vendor-managed agent runtimes (treating governance as a service to sell)
- **Auth0 GenAI / WorkOS AuthKit** — agent identity productized (treating delegation as an OAuth extension)
- **Solo.io agentgateway / Tetrate** — service mesh for agents (treating governance as data-plane policy)
- **Smithery / Glama / Docker MCP** — catalog + execution (treating discovery as the primary surface)
- **LiteLLM / Portkey / Kong AI** — LLM gateways (treating model routing as the bottleneck)
- **Letta / LangGraph / CrewAI** — agent runtimes (treating composition as a framework problem)

Each of these is a real business. None of them is building **the primitive itself** — they're building products that would all be 10× simpler if the primitive existed.

That's the wedge. AIPlex doesn't compete with any of them feature-for-feature; it builds the substrate that makes their problems easier to solve. ADK agents become caps. Letta memory becomes a cap backend. Smithery catalogs become cap sources. Auth0 vault becomes a cap-vault transport. We're not in their market — we're under it.

## The categorical claim

Before this doc: "AIPlex is a unified control plane for AI agent interactions." (a feature)

After this doc: **AIPlex is the delegation primitive AI was missing.** (a category)

Categories beat features. The first time someone runs `aiplex up`, sees a live receipt stream from their agent's actions, and exports a portable cap vault to a friend's machine — that person will not go back to a world without the primitive. That's the bet.

## Reading order for new contributors

1. This doc — *why we exist*
2. [18 — Capability Mesh](18-capability-mesh.md) — *what the primitive is*
3. [24 — Agent-as-Cap, Workflow-as-Cap](24-agent-and-workflow-as-cap.md) — *the architectural breakthrough*
4. [22 — Roadmap](22-roadmap-100x.md) — *what we're building, in order*
5. [23 — Elegance vs SOTA](23-elegance-vs-state-of-art.md) — *what we're better than, and why*

Anything proposed should pass the test in this doc before it gets a phase number in 22.
