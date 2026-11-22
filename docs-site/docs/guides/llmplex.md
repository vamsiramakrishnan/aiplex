---
title: LLMPlex
description: "Govern agent-to-model access: model surfaces, providers, metering profiles, batches and capacity."
---

# LLMPlex

Choose a provider and expose a model under the name your application uses. Your SDK sends the provider's native requests. LLMPlex forwards them and records the usage the provider reports.

Choose the entry point for your deployment:

- **Managed API.** Save a model surface with the provider, credentials, models and allowed operations. Reference it from an access point or a ClientPlex destination. Surfaces can support HTTP, SSE or live sessions.
- **Cluster gateway.** Configure weighted backends and fallback with an Envoy `LLMRoute`. Use `aiplex llm routes` or the `routes` section of an `aiplex apply` file.

See [Model providers](../library/model-providers.md) for supported protocols and providers.

## Commands

```bash
aiplex llm providers                  # providers and operations from the server's catalog
aiplex llm routes                     # Envoy routing rules
aiplex llm surfaces list
aiplex llm surfaces get openai-prod   # a readable summary; -o json or -o yaml for the full specification
aiplex llm surfaces create openai-prod --provider openai --models gpt-4o-mini --secret-ref openai-key
aiplex llm surfaces apply --help      # create or replace a surface from a file
aiplex llm usage                      # model usage records, from the usage ledger
aiplex llm costs                      # model usage and cost totals, from the usage ledger
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
