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
| `state` | always | storage | `roles/storage.admin` on the project (the CLI creates the bucket and turns on versioning); `roles/storage.objectUser` on the state bucket |
| `core` | always | alloydb, artifactregistry, certificatemanager, compute, container, firestore, iam, mesh, monitoring, privateca, secretmanager, servicenetworking | `alloydb.editor`, `artifactregistry.editor`, `certificatemanager.admin`, `compute.networkAdmin`, `compute.securityAdmin`, `container.clusterAdmin`, `container.developer`, `datastore.owner`, `iam.roleAdmin`, `iam.serviceAccountAdmin`, `iam.serviceAccountUser`, `iam.workloadIdentityPoolAdmin`, `monitoring.alertPolicyEditor`, `resourcemanager.projectIamAdmin`, `secretmanager.editor`, `secretmanager.secretAccessor`, `serviceusage.serviceUsageAdmin` |
| `dns` | `manage_dns = true` (default) | dns | `dns.editor` |
| `backup` | always | cloudscheduler, storage | `cloudscheduler.admin`, `iam.serviceAccountCreator`, `iam.serviceAccountDeleter` (plus roles already listed for core and state) |
| `binary-authorization` | always | binaryauthorization, cloudkms, containeranalysis | `binaryauthorization.attestorsAdmin`, `binaryauthorization.policyEditor`, `cloudkms.admin`, `cloudkms.publicKeyViewer`, `containeranalysis.admin` |
| `workforce-identity` | `organization_id` set | iam | `roles/iam.workforcePoolAdmin` **on the organization** (plus `iam.roleAdmin` and `resourcemanager.projectIamAdmin` on the project) |
| `alert-email` | `alert_email` set | monitoring | `monitoring.notificationChannelEditor` |
| `telemetry` | `otel_collector = true` (default; keep equal to Helm `otel.enabled` and `otel.collector`) | cloudtrace, monitoring | nothing beyond core (`iam.serviceAccountAdmin`, `resourcemanager.projectIamAdmin`) |

The Helm step of `aiplex platform apply` also creates namespaced Roles and
RoleBindings for the API. Kubernetes lets you create a Role only when you hold
its permissions yourself; `roles/container.developer` covers the objects in
those Roles. The table covers Terraform and the state bucket, not the Helm
step.

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
| `container.*` (namespaces, deployments, services, config maps, jobs, secrets, service accounts) | `helm_release`, `kubernetes_namespace`, `kubernetes_service_account`, `kubernetes_deployment`, `kubernetes_service` | For the Helm releases it depends on what the Ory charts render; for all of them the GKE mapping from Kubernetes calls to these permissions was not validated against a live cluster. |
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
| `aiplex-api` service account, secret payloads | `roles/secretmanager.secretAccessor` on the project (every secret) | `roles/secretmanager.secretAccessor` with the condition `resource.name.startsWith("projects/PROJECT_NUMBER/secrets/aiplex-")` (Terraform variable `api_secret_prefix`) |
| `aiplex-api` service account, secret writes and metadata | nothing, so writes failed | custom role `aiplexApiSecrets` (`secretmanager.secrets.create`, `secretmanager.secrets.get`, `secretmanager.versions.add`, `secretmanager.versions.destroy`, `secretmanager.versions.get`); condition: the same prefix, or a check on the project resource itself (see below) |
| `aiplex-api` service account, Firestore | `roles/datastore.user` on the project | `roles/datastore.user` with the condition `resource.name == "projects/PROJECT_ID/databases/(default)"` (variable `restrict_firestore_to_database`, default true) |
| `aiplex-api` Kubernetes service account | no RBAC | Role and RoleBinding `aiplex-api-deployer` in `aiplex-system`, `mcplex`, `a2aplex`, `llmplex` and `skillsplex` (Helm `api.rbac`): create, patch and delete (server-side apply and cleanup; no reads) on exactly the kinds `internal/deploy` renders. No ClusterRole; every object is namespaced. |
| `aiplex-alloydb-proxy` (new) | the proxy ran as `aiplex-api`, which had no AlloyDB access | own Kubernetes and Google service accounts with `roles/alloydb.client` and `roles/serviceusage.serviceUsageConsumer` on the project, the roles Google's Auth Proxy guide lists |
| `aiplex-otel-collector` (new) | the chart's collector ran as `aiplex-api` | own Kubernetes and Google service accounts (`otel_collector = true`) with `roles/cloudtrace.agent` and `roles/monitoring.metricWriter` on the project, the only level these roles support. The collector's exports and its settings are in [observability](observability.md#turn-it-on-in-gke-helm) |
| `aiplex-firestore-export` service account | `roles/datastore.importExportAdmin` on the project; `roles/storage.admin` on the backup bucket | custom role `aiplexFirestoreExporter` (`datastore.databases.export`, `datastore.databases.getMetadata`, `datastore.operations.get`; no import); `roles/storage.legacyBucketWriter` on the backup bucket (bucket get, object create, delete, list) |
| Cloud Build service account | `roles/containeranalysis.notes.attacher` on the project | the same role on the attestor's note only |
| `aiplex-external-agent` service account | `roles/iam.serviceAccountTokenCreator` on itself, plus IAP access | no token creator: federated AWS and Azure principals get access and ID tokens through `roles/iam.workloadIdentityUser`, and nothing in AIPlex signs blobs or JWTs with it |
| `aiplex-external-agent` and workforce groups | `roles/iap.httpsResourceAccessor` on the project | unchanged: the Terraform defines no IAP-protected backend service to bind on. The role holds one permission (`iap.webServiceVersions.accessViaIAP`). |

Pods: the API, OPA, console and collector run as non-root with a read-only
root filesystem, all capabilities dropped and no privilege escalation. Only the
API mounts a Kubernetes service account token (its deploy engine uses it).

A test (`TestDeployGrantsNoBroadRoles`) fails the build when anything under
`deploy/` grants `roles/owner` or `roles/editor`, a project-level
`roles/*.admin` / `roles/*Admin` that is not on an explicit allowlist (empty
today), `secretAccessor` at project level without a condition, `cluster-admin`,
or Kubernetes RBAC with a `*` verb, resource or API group.
`TestHelmAPIRoleMatchesDeployEngine` fails when the API's Role and the kinds
`internal/deploy` renders differ.

### Secrets the API reads and writes

Every secret the API reads or writes is named by the API with the `aiplex-`
prefix: tool secret references become `aiplex-tool-<tenant hash>-<ref>`,
sealed tool sessions `aiplex-tool-session-<hash>`, and OAuth client
credentials `aiplex-oauth-<hash>`. You still create tool secrets under the
tenant secret namespace that `aiplex tools auth-types` prints ("Secret
namespace"). A test checks that the code's prefix and the Terraform default
agree.

LLM route provider registrations (`secret_ref` on a provider) name a secret the
operator chose. With Secret Manager configured, the API now refuses a reference
that does not start with `aiplex-`, with an error that names the rule, and
reports a permission denial on the existence check the same way, instead of a
bare "validation is unavailable". Name those secrets `aiplex-...`.

How the write condition works. Google documents resource-name conditions for
secrets and secret versions, but not whether `secretmanager.secrets.create` is
checked on the project (the parent) or on the new secret's name. The condition
admits both cases: secrets and versions must carry the prefix, and a check made
on the project resource itself (`resource.type ==
"cloudresourcemanager.googleapis.com/Project"`) is allowed. If creation is
checked on the project, the API can create a secret with any name, but it can
never read, add versions to, destroy or read metadata of a secret outside the
prefix. This follows Google's resource-type-scoped condition pattern
(https://docs.cloud.google.com/iam/docs/configuring-resource-based-access) and
has not been validated live.

The enforcement is in IAM. If you change `api_secret_prefix`, the code still
names its secrets `aiplex-...`, so reads and writes fail.

### Still open

- **The API image cannot run in-cluster deploys.** The deploy engine shells out
  to `kubectl`, which the distroless API image does not contain. The RBAC above
  is what it needs once a client is in place.
- **The GKE cluster is not on the Terraform VPC.** `google_container_cluster`
  sets no `network` or `subnetwork`, so it uses the project's default network,
  and the firewall rules on `aiplex-network` do not apply to it. Moving an
  existing cluster to another network replaces it, so this was not changed.
- **Egress is not restricted.** The "Google APIs" egress rule allows that
  traffic, but nothing denies the rest (VPC firewalls allow egress by default).
  A deny-all egress rule was not added because the API must reach
  operator-chosen model providers, MCP servers, agents and identity providers
  on the internet. Restricting egress means a deny rule plus allow rules, or a
  proxy, for your providers.
- **The Gateway and its Cloud Armor attachment are manual.** The load balancer
  comes from the Gateway in `deploy/k8s/gateway.yaml`, which `aiplex platform
  apply` does not apply. That file now attaches `aiplex-waf` to the API and
  Console Services with `GCPBackendPolicy`; until it is applied, the policy
  filters nothing.
- **Workload identity providers carry placeholders.** `identity_pool.tf` has a
  placeholder AWS account ID (`123456789012`) and Azure issuer; set them before
  relying on external agent federation.
- **Direct Workload Identity Federation.** The API still uses a Google service
  account with `roles/iam.workloadIdentityUser` rather than federated
  principals, because it mints Google ID tokens for `gcp-adc` identity profiles
  and that path has not been validated with federated tokens.

## Upgrading an existing deployment

Run `terraform plan` (or `aiplex platform apply` without `--yes`) and read the
changes before approving. None of this has been run against a live project.

1. **Secrets.** The API's `secretAccessor` binding is replaced by one
   conditioned on the `aiplex-` prefix (created before the old one is removed),
   and a conditioned `aiplexApiSecrets` role is added for writes. The code only
   uses `aiplex-` secrets, so nothing it reads today breaks; it loses access to
   every other secret, including the AlloyDB password and the Hydra and Kratos
   secrets. LLM route providers whose `secret_ref` does not start with
   `aiplex-` are refused when saved: recreate those secrets under an `aiplex-`
   name.
2. **Firestore.** The API's `datastore.user` binding gets a database-name
   condition. If the API loses Firestore access, set
   `restrict_firestore_to_database = false` and apply again.
3. **Backups.** The export service account loses `datastore.databases.import`
   and bucket administration. Check that the next export (Cloud Scheduler job
   `aiplex-firestore-daily-export`) succeeds; if it fails on storage, grant
   `roles/storage.objectAdmin` on the backup bucket to
   `aiplex-firestore-export`.
4. **Cloud Armor.** The throttle rule counts per client IP; before, all clients
   shared one 500 requests per minute budget.
5. **DNS and certificate.** The certificate validation record carries the
   zone's `count` (a `moved` block keeps it in place). The second, wildcard DNS
   authorization and its record are removed, because one authorization covers
   the domain and its wildcard. Changing the certificate's
   `dns_authorizations` can make Terraform replace the certificate; TLS can be
   unavailable until the new one is `ACTIVE`.
6. **Apply refuses on a failed preflight.** `--skip-preflight` overrides it
   with a warning.
7. **Namespace.** Terraform now creates `aiplex-system` with Helm adoption
   metadata. If the namespace already exists, import it first
   (`terraform import kubernetes_namespace.aiplex_system aiplex-system`) and
   make sure it has the label `app.kubernetes.io/managed-by=Helm` and the
   annotations `meta.helm.sh/release-name=aiplex` and
   `meta.helm.sh/release-namespace=aiplex-system`.
8. **Terraform state bucket.** The fixed-name `aiplex-terraform-state` bucket is
   dropped from Terraform state by a `removed` block; the bucket itself is not
   deleted. The CLI keeps state in `PROJECT_ID-aiplex-tfstate` and now turns on
   versioning there. Delete the old bucket by hand once you have checked it
   holds nothing you need. `removed` blocks need Terraform 1.7, already the
   minimum.
9. **AlloyDB proxy identity.** The proxy moves to the new
   `aiplex-alloydb-proxy` service accounts; its pods restart.
10. **Collector identity.** The chart's collector moves to the `otel-collector`
    service account. Apply Terraform (`otel_collector = true`) before or with
    the Helm upgrade, or exports fail until the Google service account exists.
    If you run your own collector (`otel.collector: false`), set
    `otel_collector = false`. The Helm settings are in
    [observability](observability.md#turn-it-on-in-gke-helm).
11. **Console image.** The chart runs the console as uid 101 on port 8080 with a
    read-only root filesystem. Rebuild the console image from the new
    `console/Dockerfile` (unprivileged nginx) before upgrading the chart; the
    old `nginx:alpine` image will not start with these settings.
12. **External agent token creator.** The self-grant of
    `roles/iam.serviceAccountTokenCreator` is removed. A client that signed
    JWTs or blobs as `aiplex-external-agent` through the IAM API stops working;
    nothing in AIPlex does that.
13. **Kubernetes RBAC.** The chart adds Roles, RoleBindings and a `skillsplex`
    namespace. Set `api.rbac.create: false` to manage RBAC yourself.
14. **AlloyDB size.** `alloydb_cpu_count` is now a declared variable (default
    2). A tfvars written by an older `aiplex init` with 4 or 8 resizes the
    instance on the next apply. `aiplex init` no longer writes `admin_email`,
    `registry_name` or `hydra_admin_url`, which nothing used; remove them from
    an existing `terraform.tfvars` to silence Terraform's undeclared-variable
    warnings.

## Limits

- Not run against a live Google Cloud project; `terraform validate` and
  `helm lint` were not run as part of this change either.
- The preflight checks permissions, enabled APIs and billing. It does not
  check quotas, organization policies, deny policies, VPC Service Controls,
  or that names (buckets, domains) are free.
- Role contents change over time. The narrowest-role suggestions and the
  custom role reflect Google's reference as read on 2026-11-10.
