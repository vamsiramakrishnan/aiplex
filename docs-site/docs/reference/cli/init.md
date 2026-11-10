---
title: aiplex init
sidebar_label: init
description: "Set up AIPlex on a Google Cloud project (guided)"
---

This page is generated from the output of `aiplex --help` for the CLI built from `cmd/aiplex-cli`. Regenerate it with `npm run gen:cli` in `docs-site/`.

```text
Interactive setup that detects your GCP environment, validates
prerequisites, generates Terraform variables, and configures the CLI.

What it does:
  1. Detects gcloud auth and active account
  2. Validates GCP project, billing, required APIs and the IAM permissions
     the Terraform needs (the same check as aiplex platform preflight)
  3. Validates prerequisites (terraform, helm, kubectl)
  4. Generates terraform.tfvars for your environment
  5. Configures CLI context and credentials
  6. Tells you the exact next command to run

Examples:
  aiplex init                                            # interactive
  aiplex init --project my-project --region us-central1  # non-interactive
  aiplex init --dry-run                                  # validate only

Usage:
  aiplex init [flags]

Examples:
  aiplex init
  aiplex init --project my-project --region us-central1 --dry-run

Flags:
      --domain string    Custom domain for HTTPS
      --dry-run          Validate and show plan without writing files
      --force            Overwrite existing context without asking
  -h, --help             help for init
      --project string   GCP project ID
      --region string    GCP region

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

