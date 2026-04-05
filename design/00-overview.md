# AIPlex Design Documents

This folder contains detailed design documents for each major subsystem of AIPlex. While `CLAUDE.md` provides the big picture, these documents go deep into implementation details, edge cases, failure modes, and technical decisions.

## Document Index

| # | Document | Subsystem | Key Questions Answered |
|---|----------|-----------|----------------------|
| 01 | [Architecture & Request Flow](01-architecture-request-flow.md) | System-wide | How does a request travel from agent to backend? What happens at each hop? |
| 02 | [Auth & Keycloak](02-auth-keycloak.md) | Access control | How do the three permission dimensions work? Token lifecycle? Consent UX? |
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
| 15 | [Auth Alternatives](15-auth-alternatives-keycloak-rethink.md) | Auth rethink | Keycloak vs Ory Hydra vs Dex vs Zitadel — lighter, faster, Go-native |

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
