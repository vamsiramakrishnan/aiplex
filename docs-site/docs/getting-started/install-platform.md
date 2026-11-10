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

## 3. Check permissions

You do not need Owner or Editor. The account that deploys needs the IAM permissions the Terraform uses for the features your `terraform.tfvars` turns on, `cloudresourcemanager` and `serviceusage` APIs enabled, and billing. Check with:

```bash
gcloud auth application-default login       # the credentials Terraform uses
aiplex platform preflight --project my-project
```

It lists, per feature, the permissions you are missing, the narrowest predefined role that grants each, and the `gcloud` command that grants it. `aiplex platform preflight --print-bindings` prints the predefined roles for every enabled feature; `--print-role` prints an optional custom deployer role. The check is advisory (organization policy, deny policies and VPC Service Controls can still block an apply) and has not been run against a live project. See [Google Cloud permissions](../library/gcp-permissions.md) for the full table, what the running platform is granted, and the upgrade notes.

## 4. Initialize and apply

```bash
aiplex init
aiplex platform apply --values operator-values.yaml
```

`aiplex platform apply` runs the preflight first and stops if a required permission, API or billing is missing (`--skip-preflight` overrides it, with a warning). It then runs the whole pipeline: it creates a GCS bucket for Terraform state if needed, runs Terraform init and apply, configures the kubectl context, runs Helm install or upgrade, waits for certificate provisioning and runs a health check. `--skip-infra` runs only Helm and `--skip-deploy` runs only Terraform.

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
