---
sidebar_position: 1
title: Architecture Overview
description: High-level system architecture of AIPlex.
---

# Architecture Overview

AIPlex is deployed on GKE Autopilot with Cloud Service Mesh for mTLS. This page covers the full system topology.

## System Diagram

```
Agents / IDEs / CLIs / Other Agents
       │
       ▼
┌──────────────────────────────────────────────────────────────────┐
│  GKE Autopilot                                                    │
│                                                                   │
│  GKE Gateway API (Global HTTPS LB + IAP)                          │
│       │                                                           │
│       ▼                                                           │
│  Envoy AI Gateway                                                 │
│  ┌────────────────────────────────────────────────────────────┐   │
│  │  /mcp/*   → MCPRoute  (tool calls, SSE, sessions)          │   │
│  │  /a2a/*   → HTTPRoute (agent delegation, task routing)     │   │
│  │  /llm/*   → LLMRoute  (model inference, failover, cache)  │   │
│  │                                                            │   │
│  │  ext_authz → aiplex-authz (JWT scope check)                │   │
│  │  Rate limiting + circuit breaking (per plane)              │   │
│  │  OTel traces + metrics → Cloud Observability               │   │
│  └────────────────────────────────────────────────────────────┘   │
│       │ mTLS (Cloud Service Mesh)                                 │
│       │                                                           │
│  ┌────┴───────────────────────────────────────────────────────┐   │
│  │  namespace: aiplex-system                                   │   │
│  │  AIPlex API  │  Ory Hydra  │  Ory Kratos  │  Console       │   │
│  └─────────────────────────────────────────────────────────────┘   │
│       │ mTLS                                                      │
│  ┌────┴───────────────────────────────────────────────────────┐   │
│  │  namespace: mcplex     (MCP servers — tools)                │   │
│  │  namespace: a2aplex    (A2A agents — delegatable agents)    │   │
│  │  namespace: llmplex    (Envoy routes to external providers) │   │
│  └─────────────────────────────────────────────────────────────┘   │
│                                                                   │
│  External: Firestore │ AlloyDB │ Secret Manager │ CA Service      │
└───────────────────────────────────────────────────────────────────┘
```

## Components

### What AIPlex Builds

| Component | Language | Purpose |
|-----------|----------|---------|
| **AIPlex API** | Go | REST API — deploy engine, catalog, registry, consent handler |
| **AIPlex Console** | React/TypeScript | Web UI — catalog browser, deploy wizard, permission editor, dashboard |
| **aiplex-authz** | Rust | ext_authz service — JWT validation and scope checking |
| **AIPlex CLI** | Go | Command-line interface — deploy, manage, monitor |
| **AIPlex SDK** | Go | Client library for programmatic access |

### What AIPlex Configures

| Component | Purpose |
|-----------|---------|
| **Ory Hydra** | OAuth 2.1 server — token issuance, client registry |
| **Ory Kratos** | Identity — user login, social sign-in, MFA |
| **Envoy AI Gateway** | Traffic routing — MCPRoute, HTTPRoute, LLMRoute |
| **OPA** | Policy engine — fallback/dev mode (aiplex-authz replaces in prod) |

### What's Managed (GCP)

| Service | Purpose |
|---------|---------|
| **GKE Autopilot** | Kubernetes cluster |
| **Cloud Service Mesh** | mTLS, service discovery |
| **Firestore** | Instance and template storage |
| **AlloyDB** | Ory Hydra/Kratos database |
| **Secret Manager** | API keys, credentials |
| **CA Service** | SPIFFE certificate authority |
| **Cloud DNS** | DNS management |
| **Artifact Registry** | Container images |

## Key Design Decisions

### One Gateway for All Planes

Envoy AI Gateway handles MCP (MCPRoute), HTTP (HTTPRoute for A2A), and LLM (LLMRoute) natively. One gateway means one auth check, one rate limiter, one audit trail. Adding a plane is adding a route CRD.

### One Hydra for All Planes

An agent's permissions span planes. A single OAuth server with unified scopes means one token carries tools + agents + models. No cross-service token exchange.

### Separate Namespaces per Plane

Blast radius isolation. Network policies + mesh AuthorizationPolicy ensure a compromised MCP server can't reach A2A agents.

### Ory over Keycloak

Go-native (30MB vs 500MB images), 50MB vs 1.5GB RAM, sub-second startup. Consent as webhook means AIPlex owns the UX. Token hook for custom claims without Java SPI.

### Rust for Data Path, Go for Control Plane

aiplex-authz (Rust) handles the hot path — every request passes through it. 24x faster p50, 32x faster p99 vs OPA. AIPlex API (Go) handles deploy, catalog, registry — leveraging Go's K8s ecosystem.

## Phased Delivery

| Phase | Duration | What Ships |
|-------|----------|------------|
| 1: MCPlex | 4 weeks | Catalog, deploy, basic gateway |
| 2: Auth | 3 weeks | Ory Hydra/Kratos, OPA, consent, scopes |
| 3: Zero Trust | 2 weeks | mTLS, SPIFFE, WIF |
| 4: LLMPlex | 2 weeks | LLMRoute, cost tracking, failover |
| 5: A2APlex | 2 weeks | A2A routing, Agent Cards, delegation |
| 6: Observability | 1 week | OTel, dashboards, alerts |

MCPlex ships in 9 weeks (Phases 1-3). Each additional plane is 2 weeks of incremental work.

## Next

- [Request Flow](/docs/architecture/request-flow) — detailed request lifecycle
- [Auth Deep Dive](/docs/architecture/auth-deep-dive) — Ory integration details
