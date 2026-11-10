---
title: Operations
description: Configure, network and qualify an AIPlex deployment.
---

# Operations

| Topic | Page |
| --- | --- |
| API environment variables | [Configuration](./configuration.md) |
| Mounting pinned identity and connection files with Helm | [Identity configuration for Helm](../library/helm-identity-config.md) |
| Scope checks at the gateway | [Authorization policy](./authorization-policy.md) |
| Module composition and external services | [Independent modules](../library/modules.md) |
| Private connectivity, identity and egress plans | [Networking and Anvil](./networking.md) |
| Evidence needed before enabling hard limits | [Production qualification](../library/production-qualification.md) |

For the first install, see [Install the platform](../getting-started/install-platform.md). Day-two commands: `aiplex platform status`, `aiplex doctor`, `aiplex health`, `aiplex status`, `aiplex upgrade` (an alias for `aiplex platform apply`), and `aiplex modules list` for the enabled modules.
