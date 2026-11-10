# Google Cloud permissions

What the person or pipeline that deploys AIPlex needs on Google Cloud, what
the running platform's identities are granted, and how to check before you
apply.

**Not validated against a live project.** The permission table, the
preflight, the custom role and the narrowed runtime grants below were built
from Google's documentation and checked by unit tests with a fake Google API.
None of it has been run against a real Google Cloud project yet. Treat the
first deployment with these changes as a test.

## Check before you deploy

```bash
aiplex platform preflight                     # project from the current context
aiplex platform preflight --project my-project -o json
```

The preflight reads `deploy/terraform/terraform.tfvars` to see which features
are on, then, with your Application Default Credentials (the credentials
Terraform uses, from `gcloud auth application-default login`):

- calls `testIamPermissions` on the project for every permission the enabled
  features need, in batches of at most 100; on the organization for workforce
  identity pools; and on the Terraform state bucket when it already exists;
- lists enabled APIs, separating "disabled" from "cannot check";
- reads the project's billing state.

For each missing permission it shows the narrowest predefined role that holds
it and the `gcloud ... add-iam-policy-binding` command that grants that role to
the account it checked. It exits with status 3 when a required permission,
a bootstrap API or billing is missing, and 1 when a permission could not be
checked (fail closed). A permission whose need is *unverified* (see below) is
reported as a warning and does not fail the check.

`aiplex platform apply` runs the same check before it creates anything and
refuses on failure; `--skip-preflight` skips it with a warning. `aiplex init`
runs it as information (it no longer asks for Owner or Editor), and
`aiplex doctor` reports its failures as warnings.

**The check is advisory.** `testIamPermissions` answers from allow policies.
Organization policy constraints, IAM deny policies, VPC Service Controls
perimeters, quotas and conditions on specific resources can still stop an apply
that the preflight passed. Equally, a resource that already exists is not
re-created, so an existing deployment may apply with fewer permissions.

Never paste the output of `gcloud auth print-access-token` anywhere; the
preflight does not print tokens and does not need you to.

## What the deployer needs

The single source of truth is the table in
[`internal/gcpsetup/requirements.go`](../internal/gcpsetup/requirements.go).
A test parses every `resource` and `data` block in `deploy/terraform/*.tf`
and fails when a resource type has no entry, so the table cannot fall behind
the Terraform. Another test fails when the API list in `main.tf` differs from
the table.

| Feature | On when | APIs Terraform enables | Predefined roles that cover it |
| --- | --- | --- | --- |
| `state` | always | storage | `roles/storage.editor` and `roles/storage.bucketViewer` on the project; `roles/storage.objectUser` on the state bucket |
| `core` | always | alloydb, artifactregistry, certificatemanager, compute, container, firestore, iam, mesh, monitoring, privateca, secretmanager, servicenetworking | `alloydb.editor`, `artifactregistry.editor`, `certificatemanager.admin`, `compute.networkAdmin`, `compute.securityAdmin`, `container.clusterAdmin`, `container.developer`, `datastore.owner`, `iam.serviceAccountAdmin`, `iam.serviceAccountUser`, `iam.workloadIdentityPoolAdmin`, `monitoring.alertPolicyEditor`, `resourcemanager.projectIamAdmin`, `secretmanager.editor`, `secretmanager.secretAccessor`, `serviceusage.serviceUsageAdmin` |
| `dns` | `manage_dns = true` (default) | dns | `dns.editor` |
| `backup` | always | cloudscheduler, storage | `cloudscheduler.admin`, `iam.roleAdmin`, `iam.serviceAccountCreator`, `iam.serviceAccountDeleter`, `storage.admin` (plus `iam.serviceAccountUser` and `resourcemanager.projectIamAdmin` from core) |
| `binary-authorization` | always | binaryauthorization, cloudkms, containeranalysis | `binaryauthorization.attestorsAdmin`, `binaryauthorization.policyEditor`, `cloudkms.admin`, `cloudkms.publicKeyViewer`, `containeranalysis.admin` |
| `workforce-identity` | `organization_id` set | iam | `roles/iam.workforcePoolAdmin` **on the organization** (plus `iam.roleAdmin` and `resourcemanager.projectIamAdmin` on the project) |
| `alert-email` | `alert_email` set | monitoring | `monitoring.notificationChannelEditor` |

`cloudresourcemanager.googleapis.com` and `serviceusage.googleapis.com` must
be enabled before Terraform runs (Terraform enables every other API through
them); the preflight fails when either is disabled.

`aiplex platform preflight --print-bindings` prints the role grants above as
`gcloud` commands for your account and enabled features.
`aiplex platform preflight -o json` lists every permission it found missing.

### How the table was built, and what is unverified

Permission names, and which predefined roles contain them, come from Google's
IAM role reference (`https://docs.cloud.google.com/iam/docs/roles-permissions/<service>`),
read on 2026-11-10; each table entry cites its page. "Narrowest role" means the
predefined role that contains the permission with the fewest other
permissions, preferring a role of the same service and skipping basic, legacy
and service-agent roles.

Every permission in the table exists in that reference. A permission is marked
**unverified** when Google's documentation did not confirm that the Terraform
operation needs it:

| Permission | Resource | Why unverified |
| --- | --- | --- |
| `iam.serviceAccounts.actAs` | `google_container_cluster` | GKE documents Service Account User for a user-managed node service account; Autopilot with the default Compute Engine service account is not covered. (It is verified for the Cloud Scheduler job, which attaches a service account.) |
| `compute.networks.addPeering` | `google_service_networking_connection` | The private services access guide names `roles/compute.networkAdmin`, not individual permissions. |
| `compute.networks.updatePolicy` | `google_compute_firewall` | Not stated for firewall rule creation. |
| `containeranalysis.notes.attachOccurrence` | `google_binary_authorization_attestor` | The permission checked on the note when an attestor references it is not stated. |
| `datastore.schemas.get`, `datastore.schemas.update` | `google_firestore_field` (TTL) | The role reference names index permissions `datastore.schemas.*`; the exact check for field TTL updates is not stated. |
| `container.*` (deployments, services, config maps, jobs, secrets, service accounts) | `helm_release`, `kubernetes_deployment`, `kubernetes_service` | Depends on what the Ory charts render; not enumerated against the charts. |
| `storage.objects.*` on the state bucket | Terraform GCS backend | Not stated as a permission list by Google. |

## The custom deployer role (optional)

[`deploy/iam/aiplex-deployer-role.yaml`](../deploy/iam/aiplex-deployer-role.yaml)
holds every project-level permission in the table and nothing else. It is
generated (`aiplex platform preflight --print-role` prints the same file) and a
test fails when it is stale.

```bash
gcloud iam roles create aiplexDeployer --project PROJECT --file deploy/iam/aiplex-deployer-role.yaml
gcloud projects add-iam-policy-binding PROJECT --member=user:YOU \
  --role=projects/PROJECT/roles/aiplexDeployer --condition=None
```

Workforce pools are organization resources, so their permissions are not in
the project role; grant `roles/iam.workforcePoolAdmin` on the organization when
`organization_id` is set.

Trade-offs:

- **It needs maintenance.** When the Terraform gains a resource, the table and
  the file change, and every project's role must be updated
  (`gcloud iam roles update aiplexDeployer --project PROJECT --file ...`).
  Predefined roles follow Google's changes automatically.
- **Whoever can edit the role can widen it.** Anyone with
  `iam.roles.update` in the project can add permissions to `aiplexDeployer`.
  Keep `roles/iam.roleAdmin` away from people who should not decide what the
  deployer may do.
- **Some permissions may not be allowed in custom roles.** `gcloud` reports them
  when you create the role; this has not been tried.
- The deployer can still grant roles (`resourcemanager.projects.setIamPolicy`
  is needed for the runtime bindings), so the deployer role is privileged in
  either form. Run applies from a pipeline identity, not a person's account,
  where you can.

## What the running platform is granted

| Identity | Before | Now |
| --- | --- | --- |
| `aiplex-api` service account | `roles/secretmanager.secretAccessor` on the project (every secret) | `roles/secretmanager.secretAccessor` on the project **with the condition** `resource.name.startsWith("projects/PROJECT_NUMBER/secrets/aiplex-")` (Terraform variable `api_secret_prefix`) |
| `aiplex-api` service account | `roles/datastore.user` on the project | `roles/datastore.user` **with the condition** `resource.name == "projects/PROJECT_ID/databases/(default)"` (variable `restrict_firestore_to_database`, default true) |
| `aiplex-firestore-export` service account | `roles/datastore.importExportAdmin` on the project; `roles/storage.admin` on the backup bucket | custom role `aiplexFirestoreExporter` (`datastore.databases.export`, `datastore.databases.getMetadata`, `datastore.operations.get`; no import); `roles/storage.legacyBucketWriter` on the backup bucket (bucket get, object create, delete, list) |
| Cloud Build service account | `roles/containeranalysis.notes.attacher` on the project | the same role on the attestor's note only |
| `aiplex-external-agent` and workforce groups | `roles/iap.httpsResourceAccessor` on the project | unchanged: the Terraform defines no IAP-protected backend service to bind on. The role holds one permission (`iap.webServiceVersions.accessViaIAP`). |

A test (`TestDeployGrantsNoBroadRoles`) fails the build when anything under
`deploy/` grants `roles/owner` or `roles/editor`, a project-level
`roles/*.admin` / `roles/*Admin` that is not on an explicit allowlist (empty
today), `secretAccessor` at project level without a condition, `cluster-admin`,
or Kubernetes RBAC with a `*` verb, resource or API group.

### Secrets the API can read

Every secret the API reads or writes is named by the API with the `aiplex-`
prefix: tool secret references become `aiplex-tool-<tenant hash>-<ref>`,
sealed tool sessions `aiplex-tool-session-<hash>`, and OAuth client
credentials `aiplex-oauth-<hash>`. So the prefix condition does not change
which secret references work: you still create tool secrets under the tenant
secret namespace that `aiplex tools auth-types` prints ("Secret namespace").
A test checks that the code's prefix and the Terraform default agree.

The enforcement is in IAM only. If you change `api_secret_prefix`, the API's
reads fail with a permission error; the code does not read the variable.

Model provider registrations (`secret_ref`) check that a secret exists with
Secret Manager metadata. `roles/secretmanager.secretAccessor` does not include
`secretmanager.secrets.get`, so this check could not succeed with the runtime
grant before this change either.

### Known gaps in the runtime grants (not changed here)

- **Secret writes.** Sealing a tool session and storing OAuth client
  registrations create secrets and add versions. The API service account has
  no `secretmanager.secrets.create`, `secretmanager.versions.add` or
  `secretmanager.versions.destroy`, before or after this change, so those
  paths fail in a Terraform-deployed project until an operator grants them
  (for example `roles/secretmanager.secretVersionManager` with the same
  prefix condition, plus a way to create secrets).
- **AlloyDB proxy.** The `alloydb-proxy` deployment runs as the `aiplex-api`
  Kubernetes service account, whose Google service account has no
  `roles/alloydb.client`.
- **Workload Identity.** The API still uses a Google service account with
  `roles/iam.workloadIdentityUser` rather than direct Workload Identity
  Federation principals (`principal://.../subject/ns/aiplex-system/sa/aiplex-api`).
  The API mints Google ID tokens for `gcp-adc` identity profiles, and that path
  has not been validated with federated tokens. Switching is a follow-up.
- **Kubernetes RBAC.** The chart grants the API no Role. The deploy engine
  talks to the Kubernetes API in-cluster, so in-cluster deploys need a
  namespaced Role an operator binds; the API keeps its service account token
  for that reason. The console and OPA no longer mount one.
- **Console container.** It still runs as root (the image is `nginx:alpine` on
  port 80). Running it as non-root with a read-only root filesystem needs an
  unprivileged nginx image first. The API and OPA now run as non-root with a
  read-only root filesystem, no capabilities and no privilege escalation.

## Upgrading an existing deployment

Run `terraform plan` (or `aiplex platform apply` without `--yes`) and read the
IAM changes before approving:

- The API's Secret Manager and Firestore bindings are replaced by conditional
  bindings (created before the old ones are removed). If the API cannot read
  Firestore afterwards, set `restrict_firestore_to_database = false` and apply
  again.
- Any secret the API was reading **without** the `aiplex-` prefix stops being
  readable. The code only reads `aiplex-` secrets, so this matters only if you
  changed the code or granted something by hand.
- The backup service account loses `datastore.databases.import` and bucket
  administration. Check the next scheduled export (Cloud Scheduler job
  `aiplex-firestore-daily-export`) succeeds; if it fails with a storage
  permission error, grant `roles/storage.objectAdmin` on the backup bucket to
  `aiplex-firestore-export` and report it.
- The Cloud Armor throttle rule now counts per client IP
  (`enforce_on_key = "IP"`); before, all clients shared one 500 requests per
  minute budget.
- The certificate validation DNS records now carry the zone's `count`;
  `moved` blocks keep existing records in place.

## Limits

- Not run against a live Google Cloud project; `terraform validate` and
  `helm lint` were not run as part of this change either.
- The preflight checks permissions, enabled APIs and billing. It does not
  check quotas, organization policies, deny policies, VPC Service Controls,
  or that names (buckets, domains) are free.
- Role contents change over time. The narrowest-role suggestions and the
  custom role reflect Google's reference as read on 2026-11-10.
