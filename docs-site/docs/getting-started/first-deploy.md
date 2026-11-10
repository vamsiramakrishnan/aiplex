---
title: Deploy a tool
description: Sign in, deploy a catalog template, inspect the instance, or apply a declarative file.
---

# Deploy a tool

This page assumes a running AIPlex platform (see [Install the platform](./install-platform.md)) or a local API from the [Docker stack](./docker-stack.md). `aiplex dev` has no template catalog, so deploying a catalog template needs one of those. Template IDs and their configuration come from your catalog, so the commands below use placeholders.

## From the catalog

```bash
aiplex login
aiplex catalog list --plane mcplex
aiplex catalog get TEMPLATE_ID
aiplex deploy TEMPLATE_ID --plane mcplex --name my-tool
aiplex get instance INSTANCE_ID
```

`aiplex login` uses the OAuth device authorization grant (RFC 8628); `aiplex login --token <token>` stores a token directly. `--plane` is one of `mcplex`, `a2aplex` or `llmplex`. Replace `INSTANCE_ID` with the ID that `deploy` returns. `aiplex list` and `aiplex status` show instances and a dashboard summary, `aiplex logs` streams an instance's logs and `aiplex undeploy` removes it.

## Declaratively

Save the following as `aiplex.json`. Choose templates and their configuration from your catalog, and use model IDs and provider credentials enabled in your environment:

```json
{
  "version": "v1",
  "instances": [{
    "name": "kb-search",
    "template": "kb-search-server",
    "plane": "mcplex",
    "config": { "INDEX_PATH": "/data/curriculum" }
  }],
  "routes": [{
    "model_id": "gemini-2.5-flash",
    "backends": [
      { "provider": "google", "model_id": "gemini-2.5-flash", "weight": 80, "enabled": true },
      { "provider": "anthropic", "model_id": "claude-sonnet-4-20250514", "weight": 20, "enabled": true }
    ]
  }],
  "agents": [{
    "client_id": "tutor-agent",
    "display_name": "Tutor Agent",
    "auth_method": "client_credentials",
    "grant_types": ["client_credentials"],
    "allowed_scopes": [
      "mcp:tools:search_curriculum",
      "a2a:task:research",
      "llm:model:gemini-2.5-flash"
    ]
  }]
}
```

```bash
aiplex validate aiplex.json
aiplex apply -f aiplex.json
```

YAML works as well. The repository's `examples/` directory has declarative files to start from (`quickstart.yaml`, `quickstart.json`, `mcplex-only.yaml`); their template IDs must exist in your catalog. `examples/README.md` says which examples are tested.

These model routes configure Envoy. A ClientPlex gateway registration references a separately saved LLMPlex model surface (see [Connected Plexes](../library/connected-plexes.md#client-compatibility)); applying a route does not create that surface. Managed tool authorization and invocation follow the [MCP runtime guide](../library/mcp-runtime-authorization.md).

## Next

- Give a group of callers one URL in front of models and tools: [Access points](../guides/access-points.md).
- Connect an IDE or CLI: [Connect a client](./connect-client.md).
