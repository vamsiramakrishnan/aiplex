---
name: aiplex-gcp-setup
description: Walk the user through deploying AIPlex on Google Cloud with aiplex init and aiplex platform. Use when the user wants to host their own AIPlex on a GCP project.
---

# Set up AIPlex on Google Cloud

This deploys real infrastructure and costs money. Explain each step before running it, and never run `aiplex platform apply` or `aiplex platform destroy` without the user's explicit yes in this conversation.

## 1. Prerequisites

Ask for the GCP project ID and region. Check that `gcloud`, `terraform`, `helm` and `kubectl` are installed and that `gcloud auth list` shows the right account. `aiplex init` validates these and says what is missing.

## 2. Validate without writing anything

```bash
aiplex init --project my-project --region us-central1 --dry-run
```

Read the output with the user: account, project, billing, enabled APIs, and the permission check. `aiplex init` reports which permissions the Terraform needs and the account lacks; it does not require Owner or Editor. Say what it reports rather than skipping it.

## 3. Generate the configuration

```bash
aiplex init --project my-project --region us-central1
```

This writes the Terraform variables and a CLI context. It prints the exact next command.

## 4. Check permissions before applying

If your `aiplex` has a permission preflight under `platform` (look at `aiplex platform --help` for a `preflight` command), run it first; it lists missing permissions and the narrowest role that grants each. If it is not there, this version has no preflight, and `apply` will fail on the first missing permission instead.

## 5. Deploy, with the user's approval

```bash
aiplex platform apply
```

Then check it:

```bash
aiplex platform status
aiplex health
aiplex whoami
```

## 6. Connect a harness

Hand over to the `aiplex-onboard` skill with the new context and an access point ID.

## Tearing down

`aiplex platform destroy` removes everything. Only run it when the user asks for it by name, and show them what it will remove first.
