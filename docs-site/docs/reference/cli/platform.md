---
title: aiplex platform
sidebar_label: platform
description: "Deploy, inspect or destroy the AIPlex platform on Google Cloud"
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
  aiplex platform [flags]
  aiplex platform [command]

Examples:
  aiplex platform status
  aiplex platform apply --yes

Available Commands:
  apply       Deploy infrastructure and application (terraform + helm)
  status      Show infrastructure and deployment status
  destroy     Tear down all infrastructure (use with extreme caution)

Flags:
  -h, --help   help for platform

Global Flags:
      --context string   Use this CLI context for one command (env: AIPLEX_CONTEXT)
      --debug            Show debug output, including raw API errors
      --no-color         Disable colour (also: NO_COLOR=1)
  -o, --output string    Output format: table, wide (every column, untruncated), json or yaml (default "table")
      --token string     Bearer token (env: AIPLEX_TOKEN)
      --url string       AIPlex API URL (env: AIPLEX_URL)

Use "aiplex platform [command] --help" for more information about a command.

Exit codes:
  0  success
  1  error (including server errors)
  2  invalid input: bad flags or arguments, or a specification the server rejected
  3  not signed in, token rejected, or not permitted
  4  not found
  5  conflict: the resource changed since it was read, or already exists
  6  the API could not be reached
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

Examples:
  aiplex platform apply
  aiplex platform apply --skip-infra --yes

Flags:
  -h, --help            help for apply
      --skip-deploy     Skip Helm (only run Terraform)
      --skip-infra      Skip Terraform (only run Helm)
  -f, --values string   Helm values file override
  -y, --yes             Auto-approve Terraform changes

Global Flags:
      --context string   Use this CLI context for one command (env: AIPLEX_CONTEXT)
      --debug            Show debug output, including raw API errors
      --no-color         Disable colour (also: NO_COLOR=1)
  -o, --output string    Output format: table, wide (every column, untruncated), json or yaml (default "table")
      --token string     Bearer token (env: AIPLEX_TOKEN)
      --url string       AIPlex API URL (env: AIPLEX_URL)

Exit codes:
  0  success
  1  error (including server errors)
  2  invalid input: bad flags or arguments, or a specification the server rejected
  3  not signed in, token rejected, or not permitted
  4  not found
  5  conflict: the resource changed since it was read, or already exists
  6  the API could not be reached
```

### `aiplex platform status`

```text
Show infrastructure and deployment status

Usage:
  aiplex platform status [flags]

Examples:
  aiplex platform status

Flags:
  -h, --help   help for status

Global Flags:
      --context string   Use this CLI context for one command (env: AIPLEX_CONTEXT)
      --debug            Show debug output, including raw API errors
      --no-color         Disable colour (also: NO_COLOR=1)
  -o, --output string    Output format: table, wide (every column, untruncated), json or yaml (default "table")
      --token string     Bearer token (env: AIPLEX_TOKEN)
      --url string       AIPlex API URL (env: AIPLEX_URL)

Exit codes:
  0  success
  1  error (including server errors)
  2  invalid input: bad flags or arguments, or a specification the server rejected
  3  not signed in, token rejected, or not permitted
  4  not found
  5  conflict: the resource changed since it was read, or already exists
  6  the API could not be reached
```

### `aiplex platform destroy`

```text
Tear down all infrastructure (use with extreme caution)

Usage:
  aiplex platform destroy [flags]

Examples:
  aiplex platform destroy --yes

Flags:
  -h, --help   help for destroy
  -y, --yes    Skip confirmation prompt

Global Flags:
      --context string   Use this CLI context for one command (env: AIPLEX_CONTEXT)
      --debug            Show debug output, including raw API errors
      --no-color         Disable colour (also: NO_COLOR=1)
  -o, --output string    Output format: table, wide (every column, untruncated), json or yaml (default "table")
      --token string     Bearer token (env: AIPLEX_TOKEN)
      --url string       AIPlex API URL (env: AIPLEX_URL)

Exit codes:
  0  success
  1  error (including server errors)
  2  invalid input: bad flags or arguments, or a specification the server rejected
  3  not signed in, token rejected, or not permitted
  4  not found
  5  conflict: the resource changed since it was read, or already exists
  6  the API could not be reached
```

