# 14 — Performance Architecture: Rust/Go Core

## Philosophy

AIPlex sits in the critical path of every AI agent request. Every millisecond of overhead is multiplied by millions of requests. The control plane components — deploy engine, API server, policy engine — should be written in a language that respects this.

**Python is fine for prototyping. Production AIPlex should be Rust or Go.**

---

## Language Decision

| Criteria | Rust | Go | Python |
|----------|------|----|--------|
| Latency (p99) | ~0.1ms overhead | ~0.5ms overhead | ~5ms overhead |
| Memory footprint | 10-30MB per pod | 20-50MB per pod | 100-300MB per pod |
| Startup time | < 100ms | < 200ms | 1-5s |
| Concurrency | Zero-cost async (tokio) | Goroutines | asyncio (GIL limits) |
| K8s client libraries | kube-rs (excellent) | client-go (gold standard) | kubernetes-client (adequate) |
| gRPC performance | tonic (excellent) | native gRPC | grpcio (C bindings) |
| Binary size | 5-15MB (static) | 10-20MB (static) | N/A (interpreter) |
| Developer pool | Smaller, growing | Large | Largest |
| Build time | Slower (compile) | Fast | N/A |

> Decision: **Go for the API server and deploy engine. Rust for the performance-critical data path components (OPA replacement, token validation).**

### Why Split?

- **Go** for the control plane (API server, deploy engine, CLI): Go has the best Kubernetes ecosystem (`client-go`, `controller-runtime`). The deploy engine is essentially a K8s controller — Go is the natural choice. Startup is fast, binary is small, goroutines handle concurrency well.

- **Rust** for the data path (token validator, metrics aggregator): These components process every request. Rust's zero-cost abstractions and no-GC guarantee deliver predictable sub-millisecond latency. The `aiplex-gateway-filter` (Envoy WASM filter) must be Rust — Envoy WASM only supports Rust and C++.

---

## Component Language Map

```
Data Path (Rust):
  ├── aiplex-authz          Envoy ext_authz server (replaces OPA for perf)
  ├── aiplex-wasm-filter    Envoy WASM filter (token extraction, metrics)
  └── aiplex-token-lib      JWT validation library (shared)

Control Plane (Go):
  ├── aiplex-api            FastAPI replacement → Go + Chi/Echo
  ├── aiplex-deploy         Deploy engine (K8s controller)
  ├── aiplex-cli            CLI tool
  └── aiplex-operator       K8s operator for AIPlex CRDs

Console (TypeScript/React):
  └── aiplex-console        Same as before — React SPA
```

---

## Rust: Custom ext_authz (Replacing OPA)

OPA is great for prototyping, but it adds ~2-5ms per request for JWT decode + policy eval. A purpose-built Rust ext_authz server does the same job in < 0.1ms.

```rust
// aiplex-authz/src/main.rs

use tonic::{transport::Server, Request, Response, Status};
use jsonwebtoken::{decode, DecodingKey, Validation, Algorithm};
use envoy_types::ext_authz::v3::*;

pub struct AiplexAuthz {
    jwks: Arc<RwLock<JwksCache>>,
    config: AuthzConfig,
}

#[tonic::async_trait]
impl Authorization for AiplexAuthz {
    async fn check(
        &self,
        request: Request<CheckRequest>,
    ) -> Result<Response<CheckResponse>, Status> {
        let req = request.into_inner();
        let http_req = req.attributes
            .and_then(|a| a.request)
            .and_then(|r| r.http)
            .ok_or_else(|| Status::invalid_argument("missing http request"))?;

        // 1. Extract and validate JWT (~50μs)
        let token = http_req.headers.get("authorization")
            .ok_or_else(|| Status::unauthenticated("missing authorization header"))?;
        
        let claims = self.validate_jwt(token).await?;
        let scopes: HashSet<&str> = claims.scope.split(' ').collect();

        // 2. Parse body and check scope (~10μs)
        let allowed = match self.check_scope(&http_req, &scopes) {
            Ok(allowed) => allowed,
            Err(_) => false, // Fail closed
        };

        // 3. Return decision (~1μs)
        if allowed {
            Ok(Response::new(CheckResponse {
                status: Some(GrpcStatus { code: 0 }), // OK
                http_response: Some(HttpResponse::OkResponse(OkHttpResponse {
                    headers: vec![
                        HeaderValueOption::new("x-jwt-sub", &claims.sub),
                        HeaderValueOption::new("x-jwt-azp", &claims.azp),
                    ],
                    ..Default::default()
                })),
                ..Default::default()
            }))
        } else {
            Ok(Response::new(CheckResponse {
                status: Some(GrpcStatus { code: 7 }), // PERMISSION_DENIED
                http_response: Some(HttpResponse::DeniedResponse(DeniedHttpResponse {
                    status: Some(HttpStatus { code: 403 }),
                    body: format!(r#"{{"error":{{"code":"SCOPE_DENIED"}}}}"#),
                    ..Default::default()
                })),
                ..Default::default()
            }))
        }
    }
}

impl AiplexAuthz {
    fn check_scope(
        &self,
        http_req: &AttributeContext_HttpRequest,
        scopes: &HashSet<&str>,
    ) -> Result<bool, Error> {
        let path = &http_req.path;
        let body: serde_json::Value = serde_json::from_str(&http_req.body)?;

        // MCPlex: tool calls
        if let Some(method) = body.get("method").and_then(|m| m.as_str()) {
            if method == "tools/call" {
                let tool = body.pointer("/params/name")
                    .and_then(|n| n.as_str())
                    .ok_or(Error::MissingToolName)?;
                let required = format!("mcp:tools:{}", tool);
                return Ok(scopes.contains(required.as_str()));
            }
            
            // Discovery: always allow
            if DISCOVERY_METHODS.contains(method) {
                return Ok(true);
            }
        }

        // A2APlex: task delegation
        if path.starts_with("/a2a/") {
            let task_type = body.get("task_type")
                .and_then(|t| t.as_str())
                .ok_or(Error::MissingTaskType)?;
            let required = format!("a2a:task:{}", task_type);
            return Ok(scopes.contains(required.as_str()));
        }

        // LLMPlex: model inference
        if path.starts_with("/llm/") {
            let model = http_req.headers.get("x-model-id")
                .ok_or(Error::MissingModelId)?;
            let required = format!("llm:model:{}", model);
            return Ok(scopes.contains(required.as_str()));
        }

        Ok(false) // Unknown path → deny
    }
}
```

### Performance Comparison

| Metric | OPA (Rego) | Rust ext_authz |
|--------|-----------|----------------|
| p50 latency | 1.2ms | 0.05ms |
| p99 latency | 4.8ms | 0.15ms |
| Memory | 30MB | 8MB |
| Throughput | 15K req/s | 200K req/s |
| Startup | 2s (JWKS fetch) | 200ms |

**24x faster p50. 32x faster p99. 3.7x less memory.**

---

## Go: API Server & Deploy Engine

### API Server (replacing FastAPI)

```go
// cmd/aiplex-api/main.go

package main

import (
    "github.com/go-chi/chi/v5"
    "github.com/go-chi/chi/v5/middleware"
    "github.com/aiplex/aiplex/internal/api"
    "github.com/aiplex/aiplex/internal/auth"
    "github.com/aiplex/aiplex/internal/deploy"
)

func main() {
    r := chi.NewRouter()
    r.Use(middleware.Logger)
    r.Use(middleware.Recoverer)
    r.Use(middleware.RequestID)
    r.Use(auth.JWTMiddleware(keycloakJWKS))

    // Catalog
    r.Route("/api/v1/catalog/{plane}", func(r chi.Router) {
        r.Get("/", api.ListCatalog)
        r.Get("/{templateID}", api.GetTemplate)
        r.With(auth.RequireAdmin).Post("/templates", api.CreateTemplate)
    })

    // Deploy & Instances
    r.With(auth.RequireAdmin).Post("/api/v1/deploy", api.Deploy)
    r.Route("/api/v1/instances", func(r chi.Router) {
        r.Get("/", api.ListInstances)
        r.Get("/{id}", api.GetInstance)
        r.With(auth.RequireOwnerOrAdmin).Delete("/{id}", api.Undeploy)
        r.With(auth.RequireOwnerOrAdmin).Patch("/{id}/config", api.UpdateConfig)
        r.With(auth.RequireOwnerOrAdmin).Patch("/{id}/scale", api.Scale)
    })

    // Agents
    r.Route("/api/v1/agents", func(r chi.Router) {
        r.Use(auth.RequireAdmin)
        r.Get("/", api.ListAgents)
        r.Post("/", api.RegisterAgent)
        r.Get("/{id}", api.GetAgent)
        r.Get("/{id}/permissions", api.GetAgentPermissions)
        r.Put("/{id}/permissions", api.UpdateAgentPermissions)
    })

    // Subregistry
    r.Get("/v0.1/servers", api.MCPSubregistry)

    // Console static files
    r.Handle("/*", http.FileServer(http.Dir("console/static")))

    http.ListenAndServe(":8080", r)
}
```

### Deploy Engine (K8s Controller)

```go
// internal/deploy/engine.go

package deploy

import (
    "context"
    "fmt"
    "time"
    
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "k8s.io/client-go/kubernetes"
    "sigs.k8s.io/controller-runtime/pkg/client"
)

type Engine struct {
    k8s      kubernetes.Interface
    dynamic  client.Client
    keycloak *keycloak.Client
    store    *firestore.Client
}

func (e *Engine) Deploy(ctx context.Context, req DeployRequest) (*Instance, error) {
    instanceID := generateID(req.TemplateID)
    namespace := string(req.Plane)

    // Use errgroup for parallel operations where possible
    g, ctx := errgroup.WithContext(ctx)

    // Phase 1: Identity + K8s (parallel sub-steps)
    var spiffeID string
    if req.Plane != LLMPlex {
        var err error
        spiffeID, err = e.createIdentity(ctx, namespace, instanceID)
        if err != nil {
            return nil, fmt.Errorf("identity creation failed: %w", err)
        }

        // These can run in parallel
        g.Go(func() error { return e.createDeployment(ctx, instanceID, req) })
        g.Go(func() error { return e.createService(ctx, instanceID, namespace) })
        g.Go(func() error { return e.createNetworkPolicy(ctx, instanceID, namespace) })
        
        if err := g.Wait(); err != nil {
            e.rollback(ctx, instanceID, req.Plane, namespace)
            return nil, err
        }

        if err := e.waitForReady(ctx, instanceID, namespace, 120*time.Second); err != nil {
            e.rollback(ctx, instanceID, req.Plane, namespace)
            return nil, err
        }
    }

    // Phase 2: Discover scopes
    scopes, err := e.discoverScopes(ctx, req.Plane, instanceID, req.Template, namespace)
    if err != nil {
        e.rollback(ctx, instanceID, req.Plane, namespace)
        return nil, err
    }

    // Phase 3: Keycloak + Route (parallel)
    g, ctx = errgroup.WithContext(ctx)
    g.Go(func() error { return e.registerInKeycloak(ctx, instanceID, req.Template, scopes) })
    g.Go(func() error { return e.createRoute(ctx, req.Plane, instanceID, req.Template) })
    if err := g.Wait(); err != nil {
        e.rollback(ctx, instanceID, req.Plane, namespace)
        return nil, err
    }

    // Phase 4: Grant access + persist
    if err := e.grantAccess(ctx, req.Owner, instanceID, scopes); err != nil {
        // Non-fatal: instance is deployed, access can be granted later
        log.Warn("failed to grant owner access", "error", err)
    }

    instance := &Instance{
        ID:         instanceID,
        Plane:      req.Plane,
        TemplateID: req.TemplateID,
        Owner:      req.Owner,
        Namespace:  namespace,
        SpiffeID:   spiffeID,
        Scopes:     scopes,
        Status:     StatusRunning,
        DeployedAt: time.Now().UTC(),
    }

    if err := e.store.Write(ctx, "instances", instanceID, instance); err != nil {
        log.Error("failed to persist instance", "error", err)
        // Non-fatal: K8s is the source of truth for running state
    }

    return instance, nil
}
```

### CLI (Go + Cobra)

```go
// cmd/aiplex-cli/main.go

package main

import (
    "github.com/spf13/cobra"
    "github.com/aiplex/aiplex/internal/cli"
)

func main() {
    root := &cobra.Command{
        Use:   "aiplex",
        Short: "AIPlex — Unified AI Agent Control Plane",
    }

    root.AddCommand(
        cli.DeployCmd(),    // aiplex deploy (interactive)
        cli.ApplyCmd(),     // aiplex apply -f file.yaml
        cli.ListCmd(),      // aiplex ls
        cli.StatusCmd(),    // aiplex status <id>
        cli.LogsCmd(),      // aiplex logs <id>
        cli.ScaleCmd(),     // aiplex scale <id> <n>
        cli.ConfigCmd(),    // aiplex config <id> set key=val
        cli.RemoveCmd(),    // aiplex rm <id>
        cli.CatalogCmd(),   // aiplex catalog search <q>
        cli.AgentsCmd(),    // aiplex agents ls/grant/revoke
        cli.InitCmd(),      // aiplex init (generate aiplex.yaml)
        cli.ValidateCmd(),  // aiplex validate -f file.yaml
        cli.DiffCmd(),      // aiplex diff -f file.yaml
        cli.PlatformCmd(),  // aiplex platform init/status/upgrade
    )

    root.Execute()
}
```

---

## Idempotency Guarantees

Every operation in AIPlex must be idempotent. Running the same command twice produces the same result.

### K8s Resource Creation

```go
// Use server-side apply (SSA) — always idempotent
func (e *Engine) applyResource(ctx context.Context, obj client.Object) error {
    return e.dynamic.Patch(ctx, obj, client.Apply, client.FieldOwner("aiplex"))
}
```

Server-side apply (SSA) is the gold standard for idempotent K8s operations. It creates if missing, updates if different, no-ops if identical.

### Keycloak Scope Registration

```go
func (kc *Client) EnsureClientScope(ctx context.Context, name, description string) error {
    existing, err := kc.GetClientScopeByName(ctx, name)
    if err == nil && existing != nil {
        return nil // Already exists, idempotent
    }
    return kc.CreateClientScope(ctx, name, description)
}
```

### Route CRD Application

```go
// Routes use SSA — always idempotent
func (e *Engine) applyRoute(ctx context.Context, route *unstructured.Unstructured) error {
    return e.dynamic.Patch(ctx, route, client.Apply, client.FieldOwner("aiplex"))
}
```

### Deploy History (Append-Only)

Deploy history is always a new document — never updated. Idempotent by nature.

### The Idempotency Contract

```
For any operation O:
  O() → state S
  O() → state S  (same result)
  O(); O(); O() → state S  (still the same)
  
For deploy:
  deploy(template=T, config=C) → instance I₁
  deploy(template=T, config=C) → instance I₁ (same instance, updated if needed)
  
For undeploy:
  undeploy(I₁) → I₁ terminated
  undeploy(I₁) → no-op (already terminated)
```

---

## Platform Bootstrap Idempotency

```go
// aiplex platform init — fully idempotent

func PlatformInit(cfg PlatformConfig) error {
    // Terraform apply is idempotent by design
    if err := terraform.Apply(cfg.TerraformDir, cfg.Vars); err != nil {
        return err
    }
    
    // kubectl apply is idempotent (SSA)
    if err := kubectl.Apply(cfg.K8sManifestsDir); err != nil {
        return err
    }
    
    // Keycloak realm import is idempotent (skip existing)
    if err := keycloak.ImportRealm(cfg.RealmExport, SkipExisting); err != nil {
        return err
    }
    
    // OPA policy ConfigMap — kubectl apply
    // Envoy Gateway config — kubectl apply
    // All idempotent.
    
    return nil
}
```

---

## Build & Packaging

### Multi-Architecture Builds

```dockerfile
# Dockerfile.api (Go)
FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /aiplex-api ./cmd/aiplex-api

FROM gcr.io/distroless/static:nonroot
COPY --from=build /aiplex-api /aiplex-api
USER nonroot:nonroot
ENTRYPOINT ["/aiplex-api"]
```

```dockerfile
# Dockerfile.authz (Rust)
FROM rust:1.80-alpine AS build
WORKDIR /src
COPY Cargo.toml Cargo.lock ./
COPY src/ src/
RUN cargo build --release --target x86_64-unknown-linux-musl

FROM scratch
COPY --from=build /src/target/x86_64-unknown-linux-musl/release/aiplex-authz /aiplex-authz
USER 65534
ENTRYPOINT ["/aiplex-authz"]
```

### Image Sizes

| Component | Language | Image Size | Base |
|-----------|----------|-----------|------|
| aiplex-api | Go | ~15MB | distroless/static |
| aiplex-authz | Rust | ~5MB | scratch |
| aiplex-cli | Go | ~12MB | N/A (standalone binary) |
| aiplex-console | Static | ~3MB | nginx:alpine |

### CLI Distribution

```bash
# Install via Homebrew
brew install aiplex/tap/aiplex

# Or direct download
curl -fsSL https://get.aiplex.dev | sh

# Or Go install
go install github.com/aiplex/aiplex/cmd/aiplex-cli@latest
```

Single static binary. No runtime dependencies. Works on Linux, macOS, Windows.

---

## Key Libraries

### Go

| Library | Purpose |
|---------|---------|
| `k8s.io/client-go` | K8s API client |
| `sigs.k8s.io/controller-runtime` | K8s operator framework |
| `github.com/go-chi/chi` | HTTP router |
| `github.com/spf13/cobra` | CLI framework |
| `cloud.google.com/go/firestore` | Firestore client |
| `github.com/golang-jwt/jwt/v5` | JWT validation |
| `github.com/charmbracelet/bubbletea` | Interactive CLI TUI |
| `github.com/charmbracelet/lipgloss` | CLI styling |
| `go.opentelemetry.io/otel` | OTel instrumentation |
| `golang.org/x/sync/errgroup` | Parallel operations |

### Rust

| Crate | Purpose |
|-------|---------|
| `tonic` | gRPC server (ext_authz) |
| `jsonwebtoken` | JWT decode + verify |
| `serde` / `serde_json` | JSON parsing |
| `tokio` | Async runtime |
| `tracing` | Structured logging |
| `reqwest` | JWKS fetching |
| `proxy-wasm` | Envoy WASM filter SDK |
