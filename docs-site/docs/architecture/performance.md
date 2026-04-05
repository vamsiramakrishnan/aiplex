---
sidebar_position: 7
title: Performance
description: Language choices, latency budgets, and optimization architecture.
---

# Performance

AIPlex uses Rust for the data path and Go for the control plane, optimized for different workloads.

## Language Decisions

| Component | Language | Why |
|-----------|----------|-----|
| **aiplex-authz** | Rust | Every request passes through it. p50 = 0.05ms |
| **AIPlex API** | Go | K8s client-go, controller-runtime, fast dev velocity |
| **AIPlex CLI** | Go | Static binary, Cobra framework, cross-platform |
| **Console** | TypeScript/React | Standard web UI |

## aiplex-authz vs OPA

The Rust ext_authz service replaces OPA for production deployments:

| Metric | OPA (Rego) | aiplex-authz (Rust) | Improvement |
|--------|-----------|---------------------|-------------|
| p50 latency | 1.2ms | 0.05ms | 24x |
| p99 latency | 4.8ms | 0.15ms | 32x |
| Memory | 50MB | 8MB | 6x |
| Binary size | 30MB | 4MB | 7.5x |
| Cold start | 200ms | 5ms | 40x |

Same logic (JWT decode, scope check), same Envoy ext_authz gRPC protocol. The Rust version avoids the Rego interpreter overhead.

## Latency Budget

Target: **< 20ms total overhead** for auth + routing (excluding backend processing).

| Step | Target | Actual (Rust path) |
|------|--------|-------------------|
| TLS termination | 1ms | ~0.5ms |
| ext_authz | 0.05ms | 0.05ms p50 |
| Rate limit check | 1ms | ~0.5ms |
| mTLS handshake | 2ms | ~1ms (cached) |
| Route selection | 0.5ms | ~0.3ms |
| **Total** | **< 5ms** | **~2.3ms** |

## Scalability

### Envoy AI Gateway
- Horizontal scaling via GKE Autopilot
- Connection pooling to backends
- Circuit breakers prevent cascade failures

### AIPlex API
- Stateless (Firestore for persistence)
- Each pod caches catalog independently (no Redis)
- Horizontal scaling with readiness probes

### aiplex-authz
- DaemonSet (one per node, not sidecar per pod)
- In-memory JWT validation (no I/O)
- JWKS cached with background refresh

## Idempotency

All mutating operations are idempotent:

| Operation | Mechanism |
|-----------|-----------|
| K8s resource creation | Server-side apply (SSA) |
| Scope registration | Hydra create-if-not-exists |
| Instance record | Document key = instance ID |
| Deploy history | Append-only (auto-ID) |

`aiplex apply` can be run repeatedly without side effects.

## Cost Optimization

- **GKE Autopilot** — pay per pod, not per node
- **Ory Hydra/Kratos** — ~100MB total vs Keycloak's 1.5GB
- **DaemonSet authz** — one per node, not per pod
- **No Redis/cache tier** — in-process caching
- **LLMPlex budgets** — hard limits prevent cost overruns

## Next

- [Architecture Overview](/docs/architecture/overview) — full system diagram
- [Request Flow](/docs/architecture/request-flow) — detailed request lifecycle
