---
title: ClientPlex
description: Connect Antigravity, Codex and Claude clients to supported model endpoints with reviewed configuration plans.
---

# ClientPlex

ClientPlex answers: which client is being configured, which protocol can it speak, which tenant-owned destination can serve it, and what configuration does the developer need?

```text
Choose a client profile: Antigravity / Codex / Claude
                         |
                         v
       Save registration + credential/network references
                         |
                         v
       Resolve destination + check installed protocol
                         |
                         v
       Review configuration fragments and required hosts
                         |
                         v
       Developer merges configuration into native client
```

A destination can be a managed LLMPlex facade or an explicitly configured external gateway; direct mode uses the client's native provider authentication. ClientPlex does not install clients, issue credentials or grant model access. Credentials remain opaque references and plans leave existing workstation files intact. A rendered plan stays `configured-unverified` until the installed client, provider access and deployment have been qualified.

```bash
aiplex clients profiles
aiplex clients register --help
aiplex clients plan --help
aiplex clients list
```

For an access point, `aiplex connect ACCESS_POINT --client claude-code` prints the configuration directly; see [Connect a client](../getting-started/connect-client.md).

Read [ClientPlex](../library/clientplex.md) for profile and mode compatibility and the native Claude gateway artifacts, and [Connected Plexes](../library/connected-plexes.md#client-compatibility) for how destinations reference model surfaces.
