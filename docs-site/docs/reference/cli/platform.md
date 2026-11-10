---
title: aiplex platform
sidebar_label: platform
description: "Manage AIPlex platform infrastructure"
---

This page is generated from the output of `aiplex --help` for the CLI built from `cmd/aiplex-cli`. Regenerate it with `npm run gen:cli` in `docs-site/`.

```text
Full lifecycle management for AIPlex infrastructure.
No need to run terraform, helm, or kubectl directly.

Flow:
  aiplex init                    # configure project
  aiplex platform apply          # deploy everything
  aiplex platform status         # check infrastructure
  aiplex platform destroy        # tear down

Usage:
  aiplex platform [command]

Available Commands:
  apply       Deploy infrastructure and application (terraform + helm)
  destroy     Tear down all infrastructure (use with extreme caution)
  status      Show infrastructure and deployment status

Flags:
  -h, --help   help for platform

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)

Use "aiplex platform [command] --help" for more information about a command.
```

### `aiplex platform apply`

```text
Runs the full deployment pipeline:

  1. Creates GCS bucket for Terraform state (if needed)
  2. Runs terraform init + apply (GKE, AlloyDB, Firestore, certs, DNS)
  3. Configures kubectl context
  4. Runs helm install/upgrade (AIPlex API, Console, Ory, OPA)
  5. Waits for cert provisioning
  6. Runs health check

Use --skip-infra to only deploy the application (helm).
Use --skip-deploy to only provision infrastructure (terraform).

Usage:
  aiplex platform apply [flags]

Flags:
  -h, --help            help for apply
      --skip-deploy     Skip Helm (only run Terraform)
      --skip-infra      Skip Terraform (only run Helm)
  -f, --values string   Helm values file override
  -y, --yes             Auto-approve Terraform changes

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

### `aiplex platform destroy`

```text
Tear down all infrastructure (use with extreme caution)

Usage:
  aiplex platform destroy [flags]

Flags:
  -h, --help   help for destroy
  -y, --yes    Skip confirmation prompt

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

### `aiplex platform status`

```text
Show infrastructure and deployment status

Usage:
  aiplex platform status [flags]

Flags:
  -h, --help   help for status

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

