# 08 — Data Model & Firestore

## Overview

Firestore stores instance metadata, cached catalog templates, and the append-only deploy audit trail. It is NOT the source of truth for authorization (that's Keycloak) or routing (that's K8s CRDs). Firestore holds the "what's deployed" state that the Console and API read.

---

## Collection Design

### `instances/{id}`

The primary collection. One document per deployed instance across all planes.

```json
{
  "id": "knowledge-base-xyz",
  "plane": "mcplex",
  "template_id": "official-mcp:kb-search-server",
  "template_source": "official-mcp-registry",
  "owner": "admin@school.edu",
  "namespace": "mcplex",
  "spiffe_id": "spiffe://aiplex-prod.global.123456.workload.id.goog/ns/mcplex/sa/knowledge-base-xyz",
  "scopes": [
    "mcp:tools:search_curriculum",
    "mcp:tools:get_document"
  ],
  "config": {
    "project_id": "school-prod",
    "bucket": "curriculum-docs"
  },
  "status": "running",
  "replicas": 1,
  "deployed_at": "2026-04-05T10:00:00Z",
  "updated_at": "2026-04-05T10:00:00Z",
  "deployed_by": "admin@school.edu",
  
  "health": {
    "last_check": "2026-04-05T10:05:00Z",
    "status": "healthy",
    "latency_ms": 12
  }
}
```

**Status values:**
| Status | Meaning |
|--------|---------|
| `provisioning` | Deploy in progress |
| `running` | Healthy and routable |
| `degraded` | Running but health checks failing |
| `stopped` | Manually stopped (pods scaled to 0) |
| `failed` | Deploy failed, needs cleanup |
| `terminated` | Undeployed, kept for audit |

### `templates/{id}`

Cached catalog entries for faster browsing and offline access.

```json
{
  "id": "official-mcp:kb-search-server",
  "source": "official-mcp-registry",
  "plane": "mcplex",
  "name": "Knowledge Base Search",
  "description": "Search and retrieve documents from a knowledge base",
  "image": "ghcr.io/mcp-servers/kb-search:v2.1.0",
  "repository": "https://github.com/mcp-servers/kb-search",
  "version": "v2.1.0",
  "category": "search",
  "tools": [
    {"name": "search_curriculum", "description": "Search curriculum documents"},
    {"name": "get_document", "description": "Retrieve a specific document"}
  ],
  "config_schema": {
    "type": "object",
    "properties": {
      "project_id": {"type": "string"},
      "bucket": {"type": "string"}
    },
    "required": ["project_id", "bucket"]
  },
  "resource_limits": {"cpu": "500m", "memory": "512Mi"},
  "verified": true,
  "tags": ["search", "knowledge-base", "rag"],
  "cached_at": "2026-04-05T09:00:00Z",
  "source_updated_at": "2026-04-01T00:00:00Z"
}
```

### `deploy_history/{auto-id}`

Append-only audit trail. Every deploy, undeploy, config change, and scaling event.

```json
{
  "instance_id": "knowledge-base-xyz",
  "action": "deploy",
  "plane": "mcplex",
  "template_id": "official-mcp:kb-search-server",
  "owner": "admin@school.edu",
  "performed_by": "admin@school.edu",
  "config": {"project_id": "school-prod", "bucket": "curriculum-docs"},
  "timestamp": "2026-04-05T10:00:00Z",
  "duration_ms": 8500,
  "success": true,
  "error": null
}
```

**Action types:**
- `deploy` — New instance created
- `undeploy` — Instance terminated
- `config_update` — Environment variables changed
- `scale` — Replica count changed
- `restart` — Pod restart triggered
- `rollback` — Deploy failed, cleanup performed

---

## Indexes

Firestore requires composite indexes for queries with multiple filters or ordering.

```
# firestore.indexes.json

{
  "indexes": [
    {
      "collectionGroup": "instances",
      "fields": [
        {"fieldPath": "plane", "order": "ASCENDING"},
        {"fieldPath": "status", "order": "ASCENDING"},
        {"fieldPath": "deployed_at", "order": "DESCENDING"}
      ]
    },
    {
      "collectionGroup": "instances",
      "fields": [
        {"fieldPath": "owner", "order": "ASCENDING"},
        {"fieldPath": "deployed_at", "order": "DESCENDING"}
      ]
    },
    {
      "collectionGroup": "instances",
      "fields": [
        {"fieldPath": "plane", "order": "ASCENDING"},
        {"fieldPath": "owner", "order": "ASCENDING"},
        {"fieldPath": "status", "order": "ASCENDING"}
      ]
    },
    {
      "collectionGroup": "templates",
      "fields": [
        {"fieldPath": "plane", "order": "ASCENDING"},
        {"fieldPath": "category", "order": "ASCENDING"},
        {"fieldPath": "cached_at", "order": "DESCENDING"}
      ]
    },
    {
      "collectionGroup": "deploy_history",
      "fields": [
        {"fieldPath": "instance_id", "order": "ASCENDING"},
        {"fieldPath": "timestamp", "order": "DESCENDING"}
      ]
    },
    {
      "collectionGroup": "deploy_history",
      "fields": [
        {"fieldPath": "performed_by", "order": "ASCENDING"},
        {"fieldPath": "timestamp", "order": "DESCENDING"}
      ]
    }
  ]
}
```

---

## Firestore Store Implementation

```python
# src/aiplex/registry/store.py

from google.cloud import firestore

class FirestoreStore:
    def __init__(self, project_id: str, database_id: str = "(default)"):
        self.db = firestore.AsyncClient(project=project_id, database=database_id)
    
    async def get(self, collection: str, doc_id: str) -> dict | None:
        doc = await self.db.collection(collection).document(doc_id).get()
        return doc.to_dict() if doc.exists else None
    
    async def write(self, collection: str, doc_id: str, data: dict) -> None:
        await self.db.collection(collection).document(doc_id).set(data)
    
    async def update(self, collection: str, doc_id: str, updates: dict) -> None:
        updates["updated_at"] = firestore.SERVER_TIMESTAMP
        await self.db.collection(collection).document(doc_id).update(updates)
    
    async def delete(self, collection: str, doc_id: str) -> None:
        await self.db.collection(collection).document(doc_id).delete()
    
    async def append(self, collection: str, data: dict) -> str:
        """Append with auto-generated ID (for audit logs)."""
        data["timestamp"] = data.get("timestamp", firestore.SERVER_TIMESTAMP)
        doc_ref = self.db.collection(collection).document()
        await doc_ref.set(data)
        return doc_ref.id
    
    async def query(
        self,
        collection: str,
        filters: list[tuple] | None = None,
        order_by: str | None = None,
        order_direction: str = "DESCENDING",
        limit: int = 50,
        offset: int = 0,
    ) -> list[dict]:
        ref = self.db.collection(collection)
        
        if filters:
            for field, op, value in filters:
                ref = ref.where(field, op, value)
        
        if order_by:
            direction = (firestore.Query.DESCENDING 
                        if order_direction == "DESCENDING" 
                        else firestore.Query.ASCENDING)
            ref = ref.order_by(order_by, direction=direction)
        
        if offset > 0:
            ref = ref.offset(offset)
        
        ref = ref.limit(limit)
        
        docs = ref.stream()
        return [doc.to_dict() async for doc in docs]
    
    async def count(self, collection: str, filters: list[tuple] | None = None) -> int:
        ref = self.db.collection(collection)
        if filters:
            for field, op, value in filters:
                ref = ref.where(field, op, value)
        result = await ref.count().get()
        return result[0][0].value
```

---

## Consistency Model

Firestore provides strong consistency for single-document reads and queries within a single entity group. AIPlex uses this as follows:

| Operation | Consistency | Why It's OK |
|-----------|-------------|-------------|
| Read instance by ID | Strong | Always correct |
| List instances by plane | Strong (with index) | Always correct |
| Deploy (write instance) | Strong | Single document write |
| Audit log append | Eventual (different doc) | Audit can be slightly delayed |
| Template cache read | Eventual | Stale cache is acceptable |

### No Transactions Needed

The deploy engine writes to Firestore last (after K8s + Keycloak). If Firestore write fails, K8s resources exist but the instance isn't tracked. The cleanup job detects orphaned K8s resources (label-based) and either re-registers them or cleans them up.

> Decision: No Firestore transactions. Each document is independent. The deploy engine's rollback handles partial failures at the application level. Simpler than coordinating Firestore + K8s + Keycloak in a single transaction.

---

## Data Lifecycle

| Collection | Retention | Cleanup |
|-----------|-----------|---------|
| `instances` | Indefinite (status field tracks lifecycle) | `terminated` instances soft-deleted after 90 days |
| `templates` | Until next cache refresh | Stale templates (> 24h) re-fetched |
| `deploy_history` | 1 year | Firestore TTL policy auto-deletes |

### TTL Policy for Deploy History

```python
# Set via Firestore Admin API or Terraform
# Documents in deploy_history auto-delete 365 days after timestamp field
```

---

## Security

### Firestore Rules (Defense in Depth)

Even though AIPlex API is the only client, Firestore security rules provide a safety net:

```
rules_version = '2';
service cloud.firestore {
  match /databases/{database}/documents {
    // Only AIPlex API service account can read/write
    match /{document=**} {
      allow read, write: if request.auth.token.email == 'aiplex-api@PROJECT_ID.iam.gserviceaccount.com';
    }
  }
}
```

### No Secrets in Firestore

Config fields in instance documents contain non-sensitive configuration only (project IDs, bucket names, feature flags). API keys and credentials are stored in Secret Manager and referenced by K8s secret names.

---

## Edge Cases

### Firestore quota limits
Firestore allows 10,000 writes/second. AIPlex deploy rate is orders of magnitude below this. The main read load is Console browsing — also well within limits.

### Document size limit
Firestore documents max at 1MB. Instance documents are typically < 5KB. Template documents with large config schemas could approach limits but are unlikely to exceed them.

### Offline / degraded mode
If Firestore is unreachable, the API returns cached data where available and 503 for writes. Running instances are unaffected (data plane is independent of Firestore).
