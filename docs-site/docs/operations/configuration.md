---
title: Configuration
description: Environment variables read by the AIPlex API, and the CLI contexts that point at it.
---

# Configuration

The API reads its configuration from environment variables when it starts (`internal/config/config.go`). In a cluster, Helm sets them; locally, export them as in [Local stack with Docker](../getting-started/docker-stack.md), or use [`aiplex dev`](../getting-started/local-development.md), which sets them for you.

## Server

| Variable | Default | Meaning |
| --- | --- | --- |
| `PORT` | `8080` | Listener port |
| `HOST` | `0.0.0.0` | Listener address. With `AIPLEX_DEV_MODE=true` it must be `127.0.0.1`, `::1` or `localhost`, or startup fails. |
| `AIPLEX_DEV_MODE` | `false` | Development authentication. Never enable in a cluster. |
| `AIPLEX_PUBLIC_URL` | empty | Trusted HTTPS origin of the API. Needed for `aiplex connect`, client metadata and overview routes. |
| `AIPLEX_TENANT_ID` | empty | Trusted single-tenant binding |
| `TLS_CERT_FILE`, `TLS_KEY_FILE`, `CLIENT_CA_FILE` | empty | Serving certificate, key and client CA |
| `CONSOLE_ORIGINS` | empty | Comma-separated Console origins for CORS. Empty means permissive, which is for local development only. |
| `LOG_LEVEL` | `info` | Log level |
| `OTEL_EXPORTER_OTLP_ENDPOINT` and the other standard `OTEL_*` variables | empty | OpenTelemetry export. Off unless an endpoint is set. See [Observability](../library/observability.md). `OTEL_ENDPOINT` is a deprecated alias of `OTEL_EXPORTER_OTLP_ENDPOINT`. |
| `AIPLEX_TRACE_PROPAGATE_UPSTREAM` | `true` | Send W3C `traceparent`/`tracestate` on upstream model, tool and agent requests. `false` strips them. |
| `AIPLEX_METRICS_TOKEN` | empty | Bearer secret for `GET /metrics`. Unset, the route answers 503 (except in development mode). |

## Modules and files

| Variable | Default | Meaning |
| --- | --- | --- |
| `AIPLEX_MODULES` | `mcplex,a2aplex,llmplex,skillsplex,clientplex,networking` | Enabled modules. Add `quotaplex` to opt in to QuotaPlex; an empty list starts the shared shell only. |
| `AIPLEX_MODULE_MANIFEST` | empty | Manifest for external module services; see [Independent modules](../library/modules.md) |
| `AIPLEX_ROLE_BINDINGS_FILE` | empty | Role bindings file |
| `AIPLEX_IDENTITY_PROVIDERS_FILE` | empty | Mounted identity provider configuration |
| `AIPLEX_CONNECTIONS_FILE` | empty | Connected module providers and connections |
| `AIPLEX_QUOTA_FILE` | empty | Operator quota configuration |

## Identity

| Variable | Default | Meaning |
| --- | --- | --- |
| `JWT_ISSUER`, `JWT_AUDIENCE`, `JWT_JWKS_URL` | empty | Pinned token verification. The JWKS URL must be HTTPS. Alternative to `AIPLEX_IDENTITY_PROVIDERS_FILE`. |
| `IDENTITY_WORKFORCE` | `true` | Accept workforce identities |
| `TRUST_DOMAIN` | `aiplex-prod.global` | Workload identity trust domain |
| `WIF_POOL` | `aiplex-agents` | Workload identity federation pool |
| `WORKFORCE_POOL_ID` | `aiplex-users` | Workforce pool |
| `HYDRA_ADMIN_URL`, `HYDRA_PUBLIC_URL` | `http://hydra-admin:4445`, `http://hydra-public:4444` | Ory Hydra |
| `KRATOS_ADMIN_URL`, `KRATOS_PUBLIC_URL` | `http://kratos-admin:4434`, `http://kratos-public:4433` | Ory Kratos |

How to mount these in a cluster, and the agent and user federation variables, are in [Mount identity and connection configuration](../library/helm-identity-config.md).

## Storage and gateway

| Variable | Default | Meaning |
| --- | --- | --- |
| `GCP_PROJECT` | empty | Project for Firestore |
| `FIRESTORE_DATABASE` | `(default)` | Firestore database. `FIRESTORE_EMULATOR_HOST` points the client at the emulator. |
| `GATEWAY_NAMESPACE`, `GATEWAY_NAME` | `aiplex-system`, `aiplex-gateway` | Envoy AI Gateway that generated routes attach to |

## CLI contexts

The CLI keeps named contexts in `~/.aiplex/`:

```bash
aiplex config set-context prod --url https://aiplex.example.com
aiplex config use-context prod
aiplex config show
```

`--url` and `--token` (environment `AIPLEX_URL` and `AIPLEX_TOKEN`) override the current context for one command. See the [CLI reference](../reference/cli/config.md).

## aiplex-authz

The `ext_authz` service has its own settings (`JWT_ISSUER`, `JWT_AUDIENCE`, `JWT_JWKS_URL`, `AUTHZ_BIND`, `PORT`, `AUTHZ_DEV_MODE`, `DENIAL_WEBHOOK_URL`); see [Authorization policy](./authorization-policy.md).
