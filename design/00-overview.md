# AIPlex Design Documents

This folder contains detailed design documents for each major subsystem of AIPlex. While `CLAUDE.md` provides the big picture, these documents go deep into implementation details, edge cases, failure modes, and technical decisions.

## Document Index

| # | Document | Subsystem | Key Questions Answered |
|---|----------|-----------|----------------------|
| 01 | [Architecture & Request Flow](01-architecture-request-flow.md) | System-wide | How does a request travel from agent to backend? What happens at each hop? |
| 02 | [Auth: Ory Hydra + Kratos](02-auth-keycloak.md) | Access control | How do the three permission dimensions work? Token lifecycle? Consent UX? |
| 03 | [OPA Policy Engine](03-opa-policy.md) | Authorization | How does JWT-only policy work? Edge cases? Testing the Rego? |
| 04 | [Envoy AI Gateway & Routing](04-envoy-gateway-routing.md) | Traffic management | How are routes generated? Failover? Rate limiting per plane? |
| 05 | [Identity & Zero Trust](05-identity-zero-trust.md) | SPIFFE / WIF / mTLS | How does workload identity work? Cross-cloud agents? Certificate rotation? |
| 06 | [Deploy Engine](06-deploy-engine.md) | Orchestration | What happens during deploy? Rollback? Health checks? Failure recovery? |
| 07 | [Catalog Federation](07-catalog-federation.md) | Discovery | How are catalogs aggregated? Caching? Schema normalization? |
| 08 | [Data Model & Firestore](08-data-model-firestore.md) | Persistence | Collection design? Indexes? Consistency model? Migration strategy? |
| 09 | [Console UI](09-console-ui.md) | Frontend | Component architecture? State management? API contract? |
| 10 | [Observability](10-observability.md) | Monitoring | Metrics taxonomy? Trace propagation? Alerting strategy? Cost tracking? |
| 11 | [API Design](11-api-design.md) | AIPlex API | Endpoint inventory, request/response schemas, versioning, error handling |
| 12 | [Security Model](12-security-model.md) | Cross-cutting | Threat model, blast radius, supply chain, secrets management |
| 13 | [Developer Experience](13-developer-experience.md) | DX | aiplex.yaml, CLI, questionnaire system, 60-second deploy promise |
| 14 | [Performance Architecture](14-performance-architecture.md) | Rust/Go core | Why Go+Rust, idempotency guarantees, binary packaging |
| 15 | [Auth Alternatives](15-auth-alternatives-keycloak-rethink.md) | Auth rethink | Auth stack comparison — Keycloak vs Ory Hydra vs Dex vs Zitadel |
| 16 | [Delightful Onboarding](16-delightful-onboarding.md) | UX/DX | Zero-jargon onboarding, SDK, progressive disclosure, error messages |
| 17 | [Seamless Platform Bootstrap](17-seamless-platform-bootstrap.md) | Setup | No kubectl/helm/terraform — single binary, one command, Owner role only |
| 18 | [Capability Mesh](18-capability-mesh.md) | **100x rewrite** | One primitive that subsumes all four planes. URI grammar + structured `caps` JWT claim. |
| 19 | [CapabilityRoute CRD](19-capability-route-crd.md) | **100x rewrite** | Single CRD that replaces `MCPRoute` / `HTTPRoute` / `LLMRoute` / skill routes. |
| 20 | [MemPlex: Memory Plane](20-memplex-memory-plane.md) | **100x rewrite** | First new plane built native to the Capability Mesh — proof the abstraction holds. |
| 21 | [Runtime Consent & Trust Ledger](21-runtime-consent-and-trust-ledger.md) | **100x rewrite** | Just-in-time step-up consent + cryptographically-signed receipt chain. |
| 22 | [The 100x Roadmap](22-roadmap-100x.md) | **100x rewrite** | Phased, strict-superset migration that keeps every existing plane working through the cutover. |
| 23 | [Elegance vs. State of the Art](23-elegance-vs-state-of-art.md) | **Reference** | Concrete wins vs. competitors (Bedrock AgentCore, Solo agentgateway, LiteLLM, Auth0 GenAI, Smithery, Letta, Tetrate). What's shipped, where they're still ahead, and the elegance test. |
| 24 | [Agent-as-Cap, Workflow-as-Cap](24-agent-and-workflow-as-cap.md) | **Capability OS** | The architectural breakthrough: agents and workflows are first-class capability kinds. Vendor-portable, surgically-revocable, cross-vendor-composable. Specifically vs. AgentCore and Vertex Agent Engine. Shipped. |

> Docs **18–24** describe the capability OS architecture. Read 18–22 as a set in order, then 23 for positioning vs. competitors, then 24 for what makes AIPlex categorically different. They supersede the per-plane design in 02–10 — see [22 — Roadmap](22-roadmap-100x.md) for the phasing.

## How to Read These

- Start with **01-Architecture** for the end-to-end request flow
- Read **02-Auth** and **03-OPA** together -- they form the authorization stack
- **06-Deploy Engine** is the core business logic -- read after understanding auth and routing
- **12-Security Model** ties everything together from a threat perspective

## Conventions

- **Decision Records** are inline, marked with `> Decision:` blockquotes
- **Open Questions** are marked with `> Open:` blockquotes
- **Edge Cases** are called out in dedicated sections
- Code samples are illustrative, not copy-paste implementations
