---
title: Networking and Anvil
description: Prepare private connectivity, identity and egress configuration, and optionally execute reviewed plans through Anvil.
---

# Networking and Anvil

The Networking module prepares private connectivity, identity and egress configuration as native network artifacts, with optional reviewed execution through Anvil. Saved intent, a successful apply and observed readiness are separate states.

Anvil is an optional provider for infrastructure actions that AIPlex does not model natively. It is not a second identity authority, an automatic IaC compiler, or a replacement for the native networking plans.

## Commands

```bash
aiplex resources list network-profile
aiplex resources plan --help
aiplex infra approve --help      # review and approve a saved plan
aiplex infra execute --help
aiplex infra observe --help
aiplex infra receipt --help
```

Network profiles and deployment intents are tenant resources under `/api/v1/network/`.

## Pages

| Topic | Page |
| --- | --- |
| Where networking sits among the connected modules | [Connected Plexes](../library/connected-plexes.md#native-networking-and-anvil-fallback) |
| Anvil objects, host configuration, integrity, execution and recovery | [Anvil deployment provider](../library/anvil-deployment-provider.md) |
| Evidence required before production | [Production qualification](../library/production-qualification.md) |
