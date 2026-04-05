# 07 — Catalog Federation

## Overview

The catalog is AIPlex's discovery layer. It aggregates templates from multiple sources — official registries, cloud providers, community repositories, and local uploads — into a unified browsable catalog. Each plane has its own catalog sources, but the aggregation pattern is identical.

---

## Catalog Sources Per Plane

```
MCPlex:
  ├── Official MCP Registry (registry.modelcontextprotocol.io)
  ├── MACH Registry (registry.machalliance.org)
  ├── Google Cloud 1P MCP Servers
  ├── Custom registries (user-configured URLs)
  └── Local templates (Firestore)

A2APlex:
  ├── Local templates (Firestore)
  └── (Future: A2A registries as they emerge)

LLMPlex:
  ├── Built-in providers (Gemini, Claude, GPT, Bedrock, Ollama)
  └── Local templates (Firestore)
```

---

## Source Interface

```python
# src/aiplex/catalog/sources.py

from abc import ABC, abstractmethod
from typing import AsyncIterator

class CatalogSource(ABC):
    """Base class for all catalog sources."""
    
    @abstractmethod
    async def list_templates(
        self, 
        query: str | None = None,
        category: str | None = None,
        page: int = 1,
        page_size: int = 50,
    ) -> CatalogPage:
        """Return a page of templates from this source."""
        ...
    
    @abstractmethod
    async def get_template(self, template_id: str) -> Template | None:
        """Get a specific template by ID."""
        ...
    
    @abstractmethod
    async def health_check(self) -> SourceHealth:
        """Check if this source is reachable."""
        ...
    
    @property
    @abstractmethod
    def source_id(self) -> str:
        """Unique identifier for this source."""
        ...
    
    @property
    @abstractmethod
    def plane(self) -> str:
        """Which plane this source serves: mcplex, a2aplex, llmplex."""
        ...


class CatalogPage:
    templates: list[Template]
    total: int
    page: int
    page_size: int
    has_next: bool
```

---

## Source Implementations

### Official MCP Registry

```python
# src/aiplex/catalog/official_mcp.py

class OfficialMCPRegistry(CatalogSource):
    """Fetches from the official MCP Registry API (v0.1)."""
    
    BASE_URL = "https://registry.modelcontextprotocol.io/api/v0.1"
    
    def __init__(self):
        self._cache = TTLCache(maxsize=1000, ttl=300)  # 5 min cache
    
    async def list_templates(self, query=None, category=None, page=1, page_size=50):
        cache_key = f"list:{query}:{category}:{page}"
        if cache_key in self._cache:
            return self._cache[cache_key]
        
        params = {"page": page, "limit": page_size}
        if query:
            params["q"] = query
        if category:
            params["category"] = category
        
        response = await self.http.get(f"{self.BASE_URL}/servers", params=params)
        data = response.json()
        
        templates = [self._to_template(s) for s in data["servers"]]
        result = CatalogPage(
            templates=templates,
            total=data["total"],
            page=page,
            page_size=page_size,
            has_next=page * page_size < data["total"],
        )
        self._cache[cache_key] = result
        return result
    
    def _to_template(self, server: dict) -> Template:
        return Template(
            id=f"official-mcp:{server['id']}",
            source="official-mcp-registry",
            plane="mcplex",
            name=server["name"],
            description=server["description"],
            image=server.get("container_image"),
            repository=server.get("repository_url"),
            version=server.get("version", "latest"),
            category=server.get("category", "general"),
            tools=server.get("tools", []),
            config_schema=server.get("config_schema", {}),
            verified=server.get("verified", False),
        )
```

### Google Cloud 1P MCP Servers

```python
# src/aiplex/catalog/google_1p.py

class CloudAPIRegistry(CatalogSource):
    """Google Cloud first-party MCP servers (BigQuery, Cloud Storage, etc.)."""
    
    # Hardcoded for now; will move to a discovery API
    SERVERS = [
        {
            "id": "google-bigquery-mcp",
            "name": "Google BigQuery MCP",
            "description": "Query BigQuery datasets via MCP",
            "image": "us-docker.pkg.dev/cloud-mcp/servers/bigquery:latest",
            "tools": ["query", "list_datasets", "describe_table"],
            "config_schema": {
                "type": "object",
                "properties": {
                    "project_id": {"type": "string", "description": "GCP project ID"},
                    "dataset": {"type": "string", "description": "Default dataset"},
                },
                "required": ["project_id"],
            },
        },
        {
            "id": "google-cloud-storage-mcp",
            "name": "Google Cloud Storage MCP",
            "description": "Read/write Cloud Storage objects via MCP",
            "image": "us-docker.pkg.dev/cloud-mcp/servers/gcs:latest",
            "tools": ["read_object", "write_object", "list_objects"],
            "config_schema": {
                "type": "object",
                "properties": {
                    "bucket": {"type": "string"},
                },
                "required": ["bucket"],
            },
        },
        # ... more Google 1P servers
    ]
```

### Built-In LLM Providers

```python
# src/aiplex/catalog/providers.py

class BuiltInProviders(CatalogSource):
    """Built-in LLM provider templates."""
    
    PROVIDERS = {
        "gemini-2.5-flash": {
            "name": "Gemini 2.5 Flash",
            "provider": "google",
            "model_id": "gemini-2.5-flash",
            "capabilities": ["text", "vision", "code"],
            "pricing": {"input": 0.15, "output": 0.60},  # per 1M tokens
        },
        "gemini-2.5-pro": {
            "name": "Gemini 2.5 Pro",
            "provider": "google",
            "model_id": "gemini-2.5-pro",
            "capabilities": ["text", "vision", "code", "reasoning"],
            "pricing": {"input": 1.25, "output": 10.00},
        },
        "claude-sonnet": {
            "name": "Claude Sonnet 4",
            "provider": "anthropic",
            "model_id": "claude-sonnet-4-20250514",
            "capabilities": ["text", "vision", "code"],
            "pricing": {"input": 3.00, "output": 15.00},
        },
        "claude-opus": {
            "name": "Claude Opus 4",
            "provider": "anthropic",
            "model_id": "claude-opus-4-20250514",
            "capabilities": ["text", "vision", "code", "reasoning"],
            "pricing": {"input": 15.00, "output": 75.00},
        },
        "gpt-4o": {
            "name": "GPT-4o",
            "provider": "openai",
            "model_id": "gpt-4o",
            "capabilities": ["text", "vision", "code"],
            "pricing": {"input": 2.50, "output": 10.00},
        },
        # ... more providers
    }
```

### Local Templates (Firestore)

```python
# src/aiplex/catalog/local.py

class LocalTemplates(CatalogSource):
    """User-uploaded templates stored in Firestore."""
    
    def __init__(self, store: FirestoreStore, plane: str):
        self.store = store
        self._plane = plane
    
    async def list_templates(self, query=None, category=None, page=1, page_size=50):
        filters = [("plane", "==", self._plane)]
        if category:
            filters.append(("category", "==", category))
        
        templates = await self.store.query(
            "templates", 
            filters=filters,
            order_by="created_at",
            limit=page_size,
            offset=(page - 1) * page_size,
        )
        
        if query:
            # Simple text search (Firestore doesn't have full-text search)
            templates = [t for t in templates 
                        if query.lower() in t["name"].lower() 
                        or query.lower() in t.get("description", "").lower()]
        
        return CatalogPage(
            templates=[Template(**t) for t in templates],
            total=len(templates),  # Approximate for filtered queries
            page=page,
            page_size=page_size,
            has_next=len(templates) == page_size,
        )
```

---

## Catalog Aggregator

```python
# src/aiplex/catalog/sources.py

class CatalogAggregator:
    """Aggregates templates from all sources for a given plane."""
    
    def __init__(self):
        self.sources: dict[str, list[CatalogSource]] = {
            "mcplex": [
                OfficialMCPRegistry(),
                MACHRegistry(),
                CloudAPIRegistry(),
                # Custom registries loaded from config
            ],
            "a2aplex": [],
            "llmplex": [
                BuiltInProviders(),
            ],
        }
        # Local templates added for all planes
        for plane in self.sources:
            self.sources[plane].append(LocalTemplates(firestore, plane=plane))
    
    async def search(
        self,
        plane: str,
        query: str | None = None,
        category: str | None = None,
        source: str | None = None,
        page: int = 1,
        page_size: int = 50,
    ) -> AggregatedCatalogPage:
        sources = self.sources.get(plane, [])
        if source:
            sources = [s for s in sources if s.source_id == source]
        
        # Fetch from all sources in parallel
        results = await asyncio.gather(
            *[s.list_templates(query, category, page, page_size) for s in sources],
            return_exceptions=True,
        )
        
        # Merge results, skip failed sources
        all_templates = []
        failed_sources = []
        for src, result in zip(sources, results):
            if isinstance(result, Exception):
                failed_sources.append(SourceError(source=src.source_id, error=str(result)))
            else:
                all_templates.extend(result.templates)
        
        # Deduplicate by template ID
        seen = set()
        unique = []
        for t in all_templates:
            if t.id not in seen:
                seen.add(t.id)
                unique.append(t)
        
        return AggregatedCatalogPage(
            templates=unique[:page_size],
            total=len(unique),
            sources_queried=len(sources),
            sources_failed=failed_sources,
        )
```

---

## Template Schema

```python
# src/aiplex/models/template.py

class Template(BaseModel):
    id: str                          # Unique across all sources
    source: str                      # Which catalog source
    plane: str                       # "mcplex" | "a2aplex" | "llmplex"
    name: str                        # Human-readable name
    description: str                 # What this template does
    
    # MCPlex / A2APlex specific
    image: str | None = None         # Container image reference
    repository: str | None = None    # Source code repository
    version: str = "latest"
    
    # MCPlex specific
    tools: list[dict] | None = None  # Pre-declared tool list
    
    # A2APlex specific
    task_types: list[str] | None = None  # Supported task types
    agent_card: dict | None = None       # A2A Agent Card
    
    # LLMPlex specific
    model_id: str | None = None      # Provider model identifier
    provider: str | None = None      # "google", "anthropic", "openai", etc.
    capabilities: list[str] | None = None  # ["text", "vision", "code"]
    pricing: dict | None = None      # Token pricing info
    fallback_model_id: str | None = None
    
    # Common
    category: str = "general"
    config_schema: dict | None = None  # JSON Schema for deploy config
    resource_limits: dict | None = None  # CPU/memory requests
    verified: bool = False           # Verified by source registry
    tags: list[str] = []
    created_at: datetime | None = None
    updated_at: datetime | None = None
```

---

## Caching Strategy

| Source | Cache Location | TTL | Invalidation |
|--------|---------------|-----|-------------|
| Official MCP Registry | In-memory (TTLCache) | 5 min | TTL expiry |
| MACH Registry | In-memory (TTLCache) | 5 min | TTL expiry |
| Google 1P | Hardcoded (no cache needed) | — | Code deploy |
| Built-in providers | Hardcoded (no cache needed) | — | Code deploy |
| Local templates | Firestore (no cache) | — | Real-time |
| Custom registries | In-memory (TTLCache) | 10 min | TTL expiry |

> Decision: No Redis or external cache. In-memory TTLCache is sufficient because catalog data is read-heavy and tolerates staleness (5 min). Each API pod has its own cache — no cross-pod coordination needed.

---

## Config Schema Validation

Templates include a JSON Schema for their configuration. The Console renders this as a form, and the API validates config at deploy time:

```python
import jsonschema

async def validate_deploy_config(template: Template, config: dict) -> None:
    if template.config_schema:
        try:
            jsonschema.validate(config, template.config_schema)
        except jsonschema.ValidationError as e:
            raise InvalidConfigError(f"Config validation failed: {e.message}")
```

Example config schema for a BigQuery MCP server:
```json
{
  "type": "object",
  "properties": {
    "project_id": {
      "type": "string",
      "description": "GCP project ID",
      "pattern": "^[a-z][a-z0-9-]{4,28}[a-z0-9]$"
    },
    "dataset": {
      "type": "string",
      "description": "Default BigQuery dataset"
    },
    "max_rows": {
      "type": "integer",
      "default": 1000,
      "minimum": 1,
      "maximum": 100000
    }
  },
  "required": ["project_id"]
}
```

---

## Edge Cases

### Registry unavailable
The aggregator uses `return_exceptions=True` in `asyncio.gather`. Failed sources are reported in the response but don't block results from healthy sources.

### Duplicate templates across sources
Templates are deduplicated by ID. Source-prefixed IDs prevent collisions: `official-mcp:github-server` vs `mach:github-server` are different templates even if they wrap the same tool.

### Template image deleted from registry
Deploy will fail with `ImagePullBackOff`. The template remains in the catalog (it was valid when indexed). The Console should show image pull status.

### Malicious template in custom registry
Templates from custom registries are NOT trusted. Container images run in isolated pods with:
- Restricted SecurityContext (no root, read-only FS, no capabilities)
- Per-pod NetworkPolicy (no lateral movement)
- Resource limits (CPU/memory caps)
- SPIFFE identity (audit trail)

> Open: Should we scan container images before allowing deploy? Could use Binary Authorization or Artifact Analysis for verified sources.
