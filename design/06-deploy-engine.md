# 06 — Deploy Engine

## Overview

The deploy engine is AIPlex's core orchestrator. It takes a template + config + plane and produces a running, identity-bound, route-registered, scope-registered instance. The same engine handles all three planes with plane-specific branching only where necessary.

---

## Deploy Flow (Detailed)

```
┌─────────────────────────────────────────────────────────┐
│  Input: plane, template, config, owner                   │
└───────────────────────┬─────────────────────────────────┘
                        ▼
┌─────────────────────────────────────────────────────────┐
│  1. Generate Instance ID                                 │
│     knowledge-base-{random_suffix}                       │
│     Format: {template_slug}-{6 alphanumeric chars}       │
└───────────────────────┬─────────────────────────────────┘
                        ▼
┌─────────────────────────────────────────────────────────┐
│  2. Create Identity (skip for llmplex)                   │
│     a. GCP Service Account                               │
│     b. Kubernetes ServiceAccount                         │
│     c. Workload Identity binding                         │
│     d. Wait for SPIFFE ID propagation                    │
└───────────────────────┬─────────────────────────────────┘
                        ▼
┌─────────────────────────────────────────────────────────┐
│  3. Create K8s Resources (skip for llmplex)              │
│     a. Deployment (image from template, config as env)   │
│     b. Service (ClusterIP, port 8080)                    │
│     c. NetworkPolicy (ingress: envoy only, egress: DNS)  │
│     d. Wait for pod Ready                                │
└───────────────────────┬─────────────────────────────────┘
                        ▼
┌─────────────────────────────────────────────────────────┐
│  4. Discover Capabilities                                │
│     mcplex:  POST /mcp → tools/list → tool names         │
│     a2aplex: POST / → tasks/list → task types            │
│     llmplex: from template.model_id                      │
└───────────────────────┬─────────────────────────────────┘
                        ▼
┌─────────────────────────────────────────────────────────┐
│  5. Register in Keycloak                                 │
│     a. Create client scopes (one per tool/task/model)    │
│     b. Create resource (instance as protected resource)  │
│     c. Set scope descriptions (human-readable)           │
└───────────────────────┬─────────────────────────────────┘
                        ▼
┌─────────────────────────────────────────────────────────┐
│  6. Create Route CRD                                     │
│     mcplex:  MCPRoute → /mcp/{instance_id}               │
│     a2aplex: HTTPRoute → /a2a/{instance_id}              │
│     llmplex: LLMRoute rule + AIServiceBackend            │
└───────────────────────┬─────────────────────────────────┘
                        ▼
┌─────────────────────────────────────────────────────────┐
│  7. Grant Owner Access                                   │
│     Add scopes to owner's Keycloak user policy           │
│     Owner can immediately use the deployed instance      │
└───────────────────────┬─────────────────────────────────┘
                        ▼
┌─────────────────────────────────────────────────────────┐
│  8. Persist to Firestore                                 │
│     instances/{id} → full instance record                │
│     deploy_history/{id} → audit entry                    │
└───────────────────────┬─────────────────────────────────┘
                        ▼
                   Return Instance
```

---

## Implementation

```python
# src/aiplex/deploy/engine.py

from aiplex.deploy.identity import create_managed_identity
from aiplex.deploy.manifests import create_k8s_deployment, create_k8s_service, create_network_policy
from aiplex.deploy.routes import apply_mcproute, apply_httproute, apply_llmroute
from aiplex.access.keycloak_client import KeycloakClient
from aiplex.registry.store import FirestoreStore
from aiplex.models.instance import Instance, InstanceStatus


class DeployEngine:
    def __init__(self, keycloak: KeycloakClient, store: FirestoreStore, k8s: K8sClient):
        self.keycloak = keycloak
        self.store = store
        self.k8s = k8s

    async def deploy(self, plane: str, template: Template, config: dict, owner: str) -> Instance:
        instance_id = self._generate_id(template.id)
        namespace = plane

        try:
            # Phase 1: Infrastructure
            spiffe_id = None
            if plane != "llmplex":
                spiffe_id = await create_managed_identity(
                    pool="aiplex-prod", namespace=namespace, id=instance_id)
                
                await self._create_k8s_resources(instance_id, template, config, namespace)
                await self._wait_for_ready(instance_id, namespace, timeout=120)

            # Phase 2: Capability Discovery
            scopes = await self._discover_scopes(plane, instance_id, template, namespace)

            # Phase 3: Auth Registration
            await self._register_in_keycloak(instance_id, template, scopes)
            await self._grant_owner_access(owner, instance_id, scopes)

            # Phase 4: Route Registration
            await self._create_route(plane, instance_id, template)

            # Phase 5: Persist
            instance = Instance(
                id=instance_id,
                plane=plane,
                template_id=template.id,
                owner=owner,
                namespace=namespace,
                spiffe_id=spiffe_id,
                scopes=scopes,
                status=InstanceStatus.RUNNING,
                config=config,
            )
            await self.store.write("instances", instance_id, instance.dict())
            await self.store.append("deploy_history", {
                "instance_id": instance_id,
                "action": "deploy",
                "plane": plane,
                "owner": owner,
                "template_id": template.id,
                "timestamp": utcnow(),
            })

            return instance

        except Exception as e:
            # Rollback on failure
            await self._rollback(instance_id, plane, namespace)
            raise DeployError(f"Deploy failed for {instance_id}: {e}") from e

    async def undeploy(self, instance_id: str) -> None:
        instance = await self.store.get("instances", instance_id)
        if not instance:
            raise NotFoundError(f"Instance {instance_id} not found")

        plane = instance["plane"]
        namespace = instance["namespace"]

        # Reverse order of deploy
        # 1. Delete route
        await self._delete_route(plane, instance_id)

        # 2. Delete Keycloak registration
        await self.keycloak.delete_resource(instance_id)
        for scope in instance["scopes"]:
            # Only delete scope if no other instance uses it
            if not await self._scope_used_elsewhere(scope, instance_id):
                await self.keycloak.delete_client_scope(scope)

        # 3. Delete K8s resources
        if plane != "llmplex":
            await self.k8s.delete("Deployment", instance_id, namespace)
            await self.k8s.delete("Service", instance_id, namespace)
            await self.k8s.delete("NetworkPolicy", f"{instance_id}-netpol", namespace)
            await self.k8s.delete("ServiceAccount", instance_id, namespace)

        # 4. Update Firestore
        await self.store.update("instances", instance_id, {"status": "terminated"})
        await self.store.append("deploy_history", {
            "instance_id": instance_id,
            "action": "undeploy",
            "timestamp": utcnow(),
        })

    async def _create_k8s_resources(self, instance_id, template, config, namespace):
        await create_k8s_deployment(
            instance_id=instance_id,
            image=template.image,
            config=config,
            namespace=namespace,
            resource_limits=template.resource_limits or {"cpu": "500m", "memory": "512Mi"},
        )
        await create_k8s_service(instance_id, namespace, port=8080)
        await create_network_policy(instance_id, namespace)

    async def _wait_for_ready(self, instance_id, namespace, timeout=120):
        """Poll deployment until at least one pod is Ready."""
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            deployment = await self.k8s.get("Deployment", instance_id, namespace)
            if deployment.get("status", {}).get("readyReplicas", 0) > 0:
                return
            await asyncio.sleep(2)
        raise DeployTimeoutError(f"{instance_id} not ready within {timeout}s")

    async def _discover_scopes(self, plane, instance_id, template, namespace):
        if plane == "mcplex":
            tools = await self._call_mcp_tools_list(instance_id, namespace)
            return [f"mcp:tools:{t['name']}" for t in tools]
        elif plane == "a2aplex":
            tasks = await self._call_a2a_tasks_list(instance_id, namespace)
            return [f"a2a:task:{t['type']}" for t in tasks]
        elif plane == "llmplex":
            return [f"llm:model:{template.model_id}"]

    async def _call_mcp_tools_list(self, instance_id, namespace):
        """Call the MCP server's tools/list method via cluster-internal HTTP."""
        url = f"http://{instance_id}.{namespace}.svc.cluster.local:8080/mcp"
        response = await self.http_client.post(url, json={
            "jsonrpc": "2.0",
            "method": "tools/list",
            "id": 1
        })
        return response.json()["result"]["tools"]

    async def _register_in_keycloak(self, instance_id, template, scopes):
        for scope in scopes:
            await self.keycloak.create_client_scope(
                name=scope,
                description=self._scope_description(scope, template),
            )
        await self.keycloak.create_resource(
            name=instance_id,
            display_name=template.display_name,
            scopes=scopes,
        )

    async def _rollback(self, instance_id, plane, namespace):
        """Best-effort cleanup on deploy failure."""
        try:
            await self._delete_route(plane, instance_id)
        except Exception:
            pass
        try:
            await self.keycloak.delete_resource(instance_id)
        except Exception:
            pass
        if plane != "llmplex":
            for kind in ["Deployment", "Service", "NetworkPolicy", "ServiceAccount"]:
                try:
                    name = f"{instance_id}-netpol" if kind == "NetworkPolicy" else instance_id
                    await self.k8s.delete(kind, name, namespace)
                except Exception:
                    pass
```

---

## K8s Manifest Generation

```python
# src/aiplex/deploy/manifests.py

async def create_k8s_deployment(instance_id, image, config, namespace, resource_limits):
    deployment = {
        "apiVersion": "apps/v1",
        "kind": "Deployment",
        "metadata": {
            "name": instance_id,
            "namespace": namespace,
            "labels": {
                "app": instance_id,
                "aiplex.io/managed": "true",
                "aiplex.io/plane": namespace,
            },
        },
        "spec": {
            "replicas": 1,
            "selector": {"matchLabels": {"app": instance_id}},
            "template": {
                "metadata": {
                    "labels": {"app": instance_id},
                    "annotations": {
                        "sidecar.istio.io/inject": "true",  # Mesh sidecar
                    },
                },
                "spec": {
                    "serviceAccountName": instance_id,
                    "containers": [{
                        "name": "main",
                        "image": image,
                        "ports": [{"containerPort": 8080}],
                        "env": [
                            {"name": k, "value": str(v)}
                            for k, v in config.items()
                        ],
                        "resources": {
                            "requests": resource_limits,
                            "limits": resource_limits,
                        },
                        "readinessProbe": {
                            "httpGet": {"path": "/health", "port": 8080},
                            "initialDelaySeconds": 5,
                            "periodSeconds": 10,
                        },
                        "livenessProbe": {
                            "httpGet": {"path": "/health", "port": 8080},
                            "initialDelaySeconds": 15,
                            "periodSeconds": 30,
                        },
                        "securityContext": {
                            "runAsNonRoot": True,
                            "readOnlyRootFilesystem": True,
                            "allowPrivilegeEscalation": False,
                            "capabilities": {"drop": ["ALL"]},
                        },
                    }],
                    "automountServiceAccountToken": True,
                },
            },
        },
    }
    await k8s_client.apply(deployment)
```

---

## Rollback Strategy

The deploy engine uses a **compensating transaction** pattern:

| Phase | Resource Created | Rollback Action |
|-------|-----------------|-----------------|
| Identity | GCP SA + K8s SA + WI binding | Delete all three |
| K8s | Deployment + Service + NetworkPolicy | Delete all three |
| Keycloak | Client scopes + resource | Delete scopes + resource |
| Route | MCPRoute / HTTPRoute / LLMRoute | Delete route CRD |
| Firestore | Instance record | Mark as `failed` |

Rollback is best-effort. If rollback itself fails, the instance is marked `failed` in Firestore and a manual cleanup alert is raised.

> Decision: No distributed transaction. Each phase is idempotent (uses `kubectl apply` / Keycloak upsert). Re-running deploy with the same instance ID is safe. The "failed" state triggers a cleanup job that runs every 5 minutes.

---

## Health Checks

### During Deploy (readiness wait)

```python
# Poll every 2s for up to 120s
# Check: deployment.status.readyReplicas > 0
# On timeout: rollback + raise DeployTimeoutError
```

### Post-Deploy (continuous)

| Check | Frequency | Mechanism | Action on Failure |
|-------|-----------|-----------|-------------------|
| Pod readiness | 10s | K8s readinessProbe | Remove from Service (no traffic) |
| Pod liveness | 30s | K8s livenessProbe | Restart pod |
| MCP ping | 60s | AIPlex API → tools/list | Mark instance `degraded` in Firestore |
| Route health | 60s | Envoy health check | Circuit breaker opens |

---

## Scaling

### Manual Scaling

```python
async def scale(instance_id: str, replicas: int) -> None:
    instance = await store.get("instances", instance_id)
    if instance["plane"] == "llmplex":
        raise InvalidOperationError("LLMPlex instances don't have pods to scale")
    
    await k8s_client.patch("Deployment", instance_id, instance["namespace"], {
        "spec": {"replicas": replicas}
    })
    await store.update("instances", instance_id, {"replicas": replicas})
```

### Auto-Scaling (Future)

```yaml
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: knowledge-base-xyz
  namespace: mcplex
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: knowledge-base-xyz
  minReplicas: 1
  maxReplicas: 10
  metrics:
    - type: Resource
      resource:
        name: cpu
        target:
          type: Utilization
          averageUtilization: 70
```

> Open: Should HPA be opt-in per template, or always-on? Templates could specify `autoscale: true` and min/max replicas.

---

## Config Updates (Hot Reload)

```python
async def update_config(instance_id: str, new_config: dict) -> None:
    instance = await store.get("instances", instance_id)
    if instance["plane"] == "llmplex":
        # LLMPlex: update LLMRoute weights or backend config
        await update_llmroute(instance_id, new_config)
    else:
        # MCPlex / A2APlex: rolling update of Deployment
        await k8s_client.patch("Deployment", instance_id, instance["namespace"], {
            "spec": {
                "template": {
                    "spec": {
                        "containers": [{
                            "name": "main",
                            "env": [
                                {"name": k, "value": str(v)}
                                for k, v in new_config.items()
                            ],
                        }]
                    }
                }
            }
        })
    
    await store.update("instances", instance_id, {"config": new_config})
```

Config updates trigger a rolling restart (K8s default behavior when env vars change).

---

## Edge Cases

### Deploy of same template twice
Each deployment gets a unique instance ID. Two instances of the same template run independently with separate identities, routes, and scopes. The scopes may overlap (e.g., both expose `mcp:tools:search`), and Keycloak handles this correctly (idempotent scope creation).

### Template image not pullable
K8s will report ImagePullBackOff. The readiness wait times out after 120s. Deploy engine rolls back. Instance is marked `failed`. The error message includes the ImagePullBackOff reason.

### MCP server returns no tools
`tools/list` returns an empty array. The instance is deployed with zero scopes. It's accessible (the route exists) but useless (no tool calls will be authorized). The admin sees this in the Console and can investigate.

### Keycloak unavailable during deploy
Phase 3 fails. Rollback deletes K8s resources and route. Instance is not created. The user retries when Keycloak is back.

### Concurrent deploys of the same template
Each gets a unique instance ID — no conflict. K8s resources, routes, and Keycloak registrations are all namespaced by instance ID.
