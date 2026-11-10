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
  aiplex platform preflight      # check permissions, APIs and billing
  aiplex platform apply          # deploy everything (runs the preflight first)
  aiplex platform status         # check infrastructure
  aiplex platform destroy        # tear down

Usage:
  aiplex platform [flags]
  aiplex platform [command]

Examples:
  aiplex platform status
  aiplex platform apply --yes

Available Commands:
  preflight   Check the Google Cloud permissions, APIs and billing the platform deploy needs
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

### `aiplex platform preflight`

```text
Checks, before anything is created, that the account in Application Default
Credentials (the credentials Terraform uses) holds every IAM permission the
AIPlex Terraform needs for the features your terraform.tfvars switches on,
that the APIs Terraform depends on are enabled, and that billing is enabled.

Permissions are tested with testIamPermissions on the project (and on the
organization for workforce identity pools, and on the Terraform state bucket
when it exists), in batches of at most 100. For each missing permission the
narrowest predefined role that grants it is shown, with the gcloud command
that grants it to you.

The check is advisory: organization policy, IAM deny policies and VPC Service
Controls can still block an apply this check passes. It has not been run
against a live project as part of AIPlex's own tests.

--print-role prints the optional custom deployer role (also checked in at
deploy/iam/aiplex-deployer-role.yaml); --print-bindings prints the predefined
roles per feature as gcloud commands. Neither calls Google.

Exit status is 3 when a required permission, API or billing is missing, and 1
when something could not be checked.

Usage:
  aiplex platform preflight [flags]

Examples:
  aiplex platform preflight
  aiplex platform preflight --project my-project -o json
  aiplex platform preflight --print-role > aiplex-deployer-role.yaml
  aiplex platform preflight --print-bindings

Flags:
  -h, --help                  help for preflight
      --organization string   Organization ID for workforce identity pools (default: organization_id in terraform.tfvars)
      --print-bindings        Print gcloud commands granting the predefined roles each enabled feature needs, and exit
      --print-role            Print the custom deployer role definition (YAML for gcloud iam roles create) and exit
      --project string        GCP project ID (default: the current context's project, then gcloud's)
      --tfvars string         terraform.tfvars that selects the features (default: deploy/terraform/terraform.tfvars)

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

### `aiplex platform apply`

```text
Runs the full deployment pipeline:

  0. Runs aiplex platform preflight and stops if a required permission,
     API or billing is missing (--skip-preflight to override)
  1. Creates GCS bucket for Terraform state (if needed)
  2. Runs terraform init + apply (GKE, AlloyDB, Firestore, certs, DNS)
  3. Configures kubectl context
  4. Runs helm install/upgrade (AIPlex API, Console, Ory, OPA, and the
     Gateway with its routes and Cloud Armor attachment)
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
  -h, --help             help for apply
      --skip-deploy      Skip Helm (only run Terraform)
      --skip-infra       Skip Terraform (only run Helm)
      --skip-preflight   Do not check permissions, APIs and billing before Terraform (prints a warning)
  -f, --values string    Helm values file override
  -y, --yes              Auto-approve Terraform changes

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

