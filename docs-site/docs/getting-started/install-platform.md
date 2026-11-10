---
title: Install the platform
description: Provision AIPlex on GCP with aiplex init and aiplex platform apply.
---

# Install the platform

The platform install provisions GKE Autopilot, AlloyDB, Firestore, certificates and DNS with Terraform, then installs the AIPlex API, Console, Ory and the authorization policy with Helm. It requires GCP project credentials and the deployment tools that `aiplex init` installs.

## 1. Prepare identity configuration

The API refuses to start without pinned identity configuration, and `aiplex platform apply` checks your Helm values before it changes infrastructure. Prepare `operator-values.yaml` as described in [Mount identity and connection configuration](../library/helm-identity-config.md).

## 2. Build the CLI

```bash
make console
make build
export PATH="$PWD/bin:$PATH"
```

## 3. Initialize and apply

```bash
aiplex init
aiplex platform apply --values operator-values.yaml
```

`aiplex platform apply` runs the whole pipeline: it creates a GCS bucket for Terraform state if needed, runs Terraform init and apply, configures the kubectl context, runs Helm install or upgrade, waits for certificate provisioning and runs a health check. `--skip-infra` runs only Helm and `--skip-deploy` runs only Terraform.

```bash
aiplex platform status     # infrastructure and deployment status
aiplex doctor              # diagnose common problems
aiplex platform destroy    # tear everything down
```

`aiplex quickstart` runs the same pipeline end to end and then deploys an example workload and opens the Console.

The equivalent Make targets are `make infra-plan`, `make infra`, `make deploy-dry`, `make deploy` and `make verify`.

## Next

- Sign in and deploy a tool: [Deploy a tool](./first-deploy.md).
- Configure the identity providers people and workloads use: [Identity guides](../guides/identity.md).
- Review the qualification steps before enabling hard limits: [Production qualification](../library/production-qualification.md).
