---
title: LLMPlex
description: "Govern agent-to-model access: model surfaces, providers, metering profiles, batches and capacity."
---

# LLMPlex

LLMPlex routes and forwards native model operations. It covers HTTP, SSE and Live protocols, client wire facades, and provider surfaces for OpenAI, Anthropic, Azure OpenAI, Amazon Bedrock, the Gemini Developer API, OpenAI-compatible endpoints and the Google Cloud surfaces. Available protocols and models are explicit operator configuration; nothing is inferred from a model name.

There are two routing layers, and they are separate:

- **Envoy model routes** (`LLMRoute`) configure weighted backends and fallbacks in the gateway. Manage them with `aiplex llm routes` or the `routes` section of an `aiplex apply` file.
- **Model surfaces** are saved resources the API uses for managed, native-protocol execution. A ClientPlex destination and an access point reference a surface; applying a route does not create one.

## Commands

```bash
aiplex llm providers                  # providers and operations from the server's catalog
aiplex llm routes                     # Envoy routing rules
aiplex llm surfaces list
aiplex llm surfaces get openai-prod   # a readable summary; -o json or -o yaml for the full specification
aiplex llm surfaces create openai-prod --provider openai --models gpt-4o-mini --secret-ref openai-key
aiplex llm surfaces apply --help      # create or replace a surface from a file
aiplex llm usage                      # recorded model usage
aiplex llm costs                      # usage and cost summary
aiplex llm capacity --help            # last observed provider limits and cooldown (admin)
aiplex llm batches list
```

## Pages

| Topic | Page |
| --- | --- |
| Surfaces, native operations, state isolation | [Native model surfaces](../library/model-surfaces.md) |
| Provider surfaces, caller authentication, WebSocket sessions, capacity policy | [Model providers](../library/model-providers.md) |
| What each provider format meters | [Metering profiles](../library/model-metering-profiles.md) and the [usage-format coverage matrix](../library/reference/usage-formats/coverage.md) |
| Provider batch jobs | [Model batches](../library/model-batches.md) |
| Capacity policy (cooldown and headroom gate) | [Model providers: capacity policy](../library/model-providers.md#capacity-policy) |
| Pointing a client at a surface | [ClientPlex](./clientplex.md) and [Connected Plexes](../library/connected-plexes.md#client-compatibility) |

## Related

- [QuotaPlex](./quotaplex.md) meters and admits managed model calls when enabled.
- [Usage ledger](../library/usage-ledger.md) records every managed exchange.
- API: [Model surfaces](../api/model-surfaces.md) and [Model batches](../api/model-batches.md).
