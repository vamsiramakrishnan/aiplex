---
title: aiplex init
sidebar_label: init
description: "Set up AIPlex on a GCP project"
---

This page is generated from the output of `aiplex --help` for the CLI built from `cmd/aiplex-cli`. Regenerate it with `npm run gen:cli` in `docs-site/`.

```text
Interactive setup that detects your GCP environment, validates
prerequisites, generates Terraform variables, and configures the CLI.

What it does:
  1. Detects gcloud auth and active account
  2. Validates GCP project, billing, and required APIs
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

Flags:
      --domain string    Custom domain for HTTPS
      --dry-run          Validate and show plan without writing files
      --force            Overwrite existing context without asking
  -h, --help             help for init
      --project string   GCP project ID
      --region string    GCP region

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

