# Workload Identity Pool for external agents (AWS, Azure, on-prem)
resource "google_iam_workload_identity_pool" "aiplex" {
  provider                  = google-beta
  project                   = var.project_id
  workload_identity_pool_id = "aiplex-agents"
  display_name              = "AIPlex Agent Identity Pool"
  description               = "Workload identity pool for external AI agents"
}

# External identity providers. Each is created only when its variables are
# set, so a deployment without agents on AWS or Azure carries no provider that
# trusts a placeholder account or tenant. Direct agent federation from those
# clouds stays off until you set them.

variable "aws_account_id" {
  description = "AWS account ID whose IAM roles external agents assume. Empty (default) creates no AWS provider."
  type        = string
  default     = ""

  validation {
    condition     = var.aws_account_id == "" || can(regex("^[0-9]{12}$", var.aws_account_id))
    error_message = "aws_account_id must be empty or a 12-digit AWS account ID."
  }
}

variable "azure_agent_tenant_id" {
  description = "Azure AD tenant ID for external agents (a GUID). Empty (default) creates no Azure provider."
  type        = string
  default     = ""

  validation {
    condition     = var.azure_agent_tenant_id == "" || can(regex("^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$", var.azure_agent_tenant_id))
    error_message = "azure_agent_tenant_id must be empty or an Azure AD tenant GUID."
  }
}

variable "aws_agent_role" {
  description = "AWS IAM role name that external agents assume (e.g. aiplex-agent). Used only when aws_account_id is set."
  type        = string
  default     = "aiplex-agent"

  validation {
    condition     = can(regex("^[A-Za-z0-9+=,.@_-]{1,64}$", var.aws_agent_role))
    error_message = "aws_agent_role must be a valid AWS IAM role name (1-64 characters: letters, digits and + = , . @ _ -)."
  }
}

locals {
  aws_agents_enabled   = var.aws_account_id != ""
  azure_agents_enabled = var.azure_agent_tenant_id != ""
}

# AWS provider (agents running on AWS)
resource "google_iam_workload_identity_pool_provider" "aws" {
  count                              = local.aws_agents_enabled ? 1 : 0
  provider                           = google-beta
  project                            = var.project_id
  workload_identity_pool_id          = google_iam_workload_identity_pool.aiplex.workload_identity_pool_id
  workload_identity_pool_provider_id = "aws-agents"
  display_name                       = "AWS Agents"

  aws {
    account_id = var.aws_account_id
  }

  attribute_mapping = {
    "google.subject"        = "assertion.arn"
    "attribute.aws_account" = "assertion.account"
    "attribute.aws_role"    = "assertion.arn.extract('assumed-role/{role}/')"
  }
}

# Azure AD provider (agents running on Azure)
resource "google_iam_workload_identity_pool_provider" "azure" {
  count                              = local.azure_agents_enabled ? 1 : 0
  provider                           = google-beta
  project                            = var.project_id
  workload_identity_pool_id          = google_iam_workload_identity_pool.aiplex.workload_identity_pool_id
  workload_identity_pool_provider_id = "azure-agents"
  display_name                       = "Azure Agents"

  oidc {
    issuer_uri = "https://sts.windows.net/${var.azure_agent_tenant_id}/"
  }

  attribute_mapping = {
    "google.subject"      = "assertion.sub"
    "attribute.tenant_id" = "assertion.tid"
  }
}

# Existing deployments created both providers without an index; keep them in
# place when their variables are set. When they are not set, Terraform plans to
# destroy the placeholder provider (see docs/gcp-permissions.md, upgrading).
moved {
  from = google_iam_workload_identity_pool_provider.aws
  to   = google_iam_workload_identity_pool_provider.aws[0]
}

moved {
  from = google_iam_workload_identity_pool_provider.azure
  to   = google_iam_workload_identity_pool_provider.azure[0]
}

# Service accounts for AIPlex system components
resource "google_service_account" "aiplex_api" {
  account_id   = "aiplex-api"
  display_name = "AIPlex API"
  project      = var.project_id
}

resource "google_service_account" "aiplex_console" {
  account_id   = "aiplex-console"
  display_name = "AIPlex Console"
  project      = var.project_id
}

# Bind K8s SA → GCP SA (Workload Identity)
#
# Kept as a Google service account plus roles/iam.workloadIdentityUser rather
# than direct Workload Identity Federation principals
# (principal://.../subject/ns/aiplex-system/sa/aiplex-api): the API mints
# Google ID tokens for gcp-adc identity profiles (internal/connections
# IDToken), and that path has not been validated with federated tokens. A
# follow-up can switch once that is tested; see docs/gcp-permissions.md.
resource "google_service_account_iam_member" "aiplex_api_workload_identity" {
  service_account_id = google_service_account.aiplex_api.name
  role               = "roles/iam.workloadIdentityUser"
  member             = "serviceAccount:${var.project_id}.svc.id.goog[aiplex-system/aiplex-api]"
}

# Firestore access for AIPlex API, limited to the AIPlex database with an IAM
# condition on the database resource name (projects/PROJECT_ID/databases/ID;
# https://docs.cloud.google.com/iam/docs/conditions-resource-attributes).
# Set restrict_firestore_to_database = false to fall back to the unconditional
# project-level binding if the condition ever denies access in your project.
variable "restrict_firestore_to_database" {
  description = "Limit the API's roles/datastore.user binding to the AIPlex Firestore database with an IAM condition"
  type        = bool
  default     = true
}

resource "google_project_iam_member" "aiplex_api_firestore" {
  project = var.project_id
  role    = "roles/datastore.user"
  member  = "serviceAccount:${google_service_account.aiplex_api.email}"

  dynamic "condition" {
    for_each = var.restrict_firestore_to_database ? [1] : []
    content {
      title       = "aiplex-firestore-database"
      description = "Only the AIPlex Firestore database"
      expression  = "resource.name == \"projects/${var.project_id}/databases/${google_firestore_database.aiplex.name}\""
    }
  }

  lifecycle {
    create_before_destroy = true
  }
}

# Secret Manager read access for AIPlex API, limited by an IAM condition to
# secrets whose ID starts with api_secret_prefix. Every secret the API reads
# is named by the API itself with that prefix (tool secrets
# aiplex-tool-<tenant hash>-<ref>, tool sessions aiplex-tool-session-<hash>,
# OAuth client credentials aiplex-oauth-<hash>; see internal/connections), so
# the API cannot read Terraform's own secrets (AlloyDB password, Hydra and
# Kratos secrets) or any other secret in the project. Secret resource names in
# conditions use the project number
# (https://docs.cloud.google.com/iam/docs/conditions-resource-attributes).
variable "api_secret_prefix" {
  description = "Secret ID prefix the AIPlex API may read; must match the prefix the API names its secrets with"
  type        = string
  default     = "aiplex-"
}

resource "google_project_iam_member" "aiplex_api_secrets" {
  project = var.project_id
  role    = "roles/secretmanager.secretAccessor"
  member  = "serviceAccount:${google_service_account.aiplex_api.email}"

  condition {
    title       = "aiplex-api-secrets"
    description = "Only secrets named ${var.api_secret_prefix}*"
    expression  = "resource.name.startsWith(\"projects/${data.google_project.current.number}/secrets/${var.api_secret_prefix}\")"
  }

  lifecycle {
    create_before_destroy = true
  }
}

# Secret writes and metadata for AIPlex API: sealing tool sessions and storing
# OAuth client registrations create secrets, add versions and destroy old
# ones (internal/secrets, internal/connections); the model-provider reference
# check reads secret metadata. A custom role holds exactly those permissions.
#
# The condition follows Google's resource-based access pattern
# (https://docs.cloud.google.com/iam/docs/configuring-resource-based-access):
# secrets and secret versions must carry the prefix. Google does not document
# whether secretmanager.secrets.create is evaluated against the project (the
# parent) or the new secret's name, so the condition also admits a check on
# the project resource itself. In the first case the API can create a secret
# of any name in the project but can never read, add to or destroy one
# outside the prefix; in the second, creation is prefix-limited too.
# Not validated against a live project.
resource "google_project_iam_custom_role" "api_secrets" {
  project     = var.project_id
  role_id     = "aiplexApiSecrets"
  title       = "AIPlex API secret writer"
  description = "Create AIPlex secrets, add and destroy their versions, read their metadata. No payload access (that is the secretAccessor binding)."
  permissions = [
    "secretmanager.secrets.create",
    "secretmanager.secrets.get",
    "secretmanager.versions.add",
    "secretmanager.versions.destroy",
    "secretmanager.versions.get",
  ]
}

resource "google_project_iam_member" "aiplex_api_secret_writer" {
  project = var.project_id
  role    = google_project_iam_custom_role.api_secrets.id
  member  = "serviceAccount:${google_service_account.aiplex_api.email}"

  condition {
    title       = "aiplex-api-secret-writes"
    description = "Secrets named ${var.api_secret_prefix}*, or creation checked on the project"
    expression  = "resource.name.startsWith(\"projects/${data.google_project.current.number}/secrets/${var.api_secret_prefix}\") || resource.type == \"cloudresourcemanager.googleapis.com/Project\""
  }
}

# AlloyDB Auth Proxy (deployed by ory.tf as the aiplex-alloydb-proxy
# Kubernetes service account) connects with roles/alloydb.client; Google's
# Auth Proxy guide also lists roles/serviceusage.serviceUsageConsumer for the
# principal that calls the AlloyDB Admin API
# (https://docs.cloud.google.com/alloydb/docs/auth-proxy/connect).
# The proxy gets its own Google service account so the API does not hold
# database connect rights it never uses.
resource "google_service_account" "alloydb_proxy" {
  account_id   = "aiplex-alloydb-proxy"
  display_name = "AIPlex AlloyDB Auth Proxy"
  project      = var.project_id
}

resource "google_service_account_iam_member" "alloydb_proxy_workload_identity" {
  service_account_id = google_service_account.alloydb_proxy.name
  role               = "roles/iam.workloadIdentityUser"
  member             = "serviceAccount:${var.project_id}.svc.id.goog[aiplex-system/aiplex-alloydb-proxy]"
}

resource "google_project_iam_member" "alloydb_proxy_client" {
  project = var.project_id
  role    = "roles/alloydb.client"
  member  = "serviceAccount:${google_service_account.alloydb_proxy.email}"
}

resource "google_project_iam_member" "alloydb_proxy_service_usage" {
  project = var.project_id
  role    = "roles/serviceusage.serviceUsageConsumer"
  member  = "serviceAccount:${google_service_account.alloydb_proxy.email}"
}

# ── External Agent WIF → GCP Service Account Bindings ──
#
# External agents (AWS, Azure) authenticate via WIF and impersonate a
# dedicated GCP service account scoped to AIPlex API access only.

resource "google_service_account" "external_agent" {
  account_id   = "aiplex-external-agent"
  display_name = "AIPlex External Agent"
  description  = "Service account for WIF-authenticated external agents"
  project      = var.project_id
}

# Allow AWS agents with the named role in the configured account to
# impersonate the external agent SA (only when aws_account_id is set).
resource "google_service_account_iam_member" "aws_agent_wif" {
  count              = local.aws_agents_enabled ? 1 : 0
  service_account_id = google_service_account.external_agent.name
  role               = "roles/iam.workloadIdentityUser"
  member             = "principalSet://iam.googleapis.com/${google_iam_workload_identity_pool.aiplex.name}/attribute.aws_role/${var.aws_agent_role}"
}

moved {
  from = google_service_account_iam_member.aws_agent_wif
  to   = google_service_account_iam_member.aws_agent_wif[0]
}

# Allow Azure AD service principals of the configured tenant to impersonate
# the external agent SA (only when azure_agent_tenant_id is set).
resource "google_service_account_iam_member" "azure_agent_wif" {
  count              = local.azure_agents_enabled ? 1 : 0
  service_account_id = google_service_account.external_agent.name
  role               = "roles/iam.workloadIdentityUser"
  member             = "principalSet://iam.googleapis.com/${google_iam_workload_identity_pool.aiplex.name}/attribute.tenant_id/${var.azure_agent_tenant_id}"
}

moved {
  from = google_service_account_iam_member.azure_agent_wif
  to   = google_service_account_iam_member.azure_agent_wif[0]
}

# Grant the external agent SA minimal permissions: IAP access + token creator.
# Project level because this Terraform defines no IAP-protected backend
# service to bind on (the load balancer is created from Kubernetes Gateway
# resources); roles/iap.httpsResourceAccessor holds one permission,
# iap.webServiceVersions.accessViaIAP.
resource "google_project_iam_member" "external_agent_iap" {
  project = var.project_id
  role    = "roles/iap.httpsResourceAccessor"
  member  = "serviceAccount:${google_service_account.external_agent.email}"
}

# No roles/iam.serviceAccountTokenCreator on itself: the federated AWS and
# Azure principals already get access and ID tokens for this account through
# roles/iam.workloadIdentityUser above (getAccessToken, getOpenIdToken), and
# nothing in AIPlex signs blobs or JWTs with it. The self-grant added only
# signBlob, signJwt and implicit delegation.

# ── Outputs ──

output "workload_identity_pool_name" {
  value = google_iam_workload_identity_pool.aiplex.name
}

output "external_agent_sa_email" {
  value = google_service_account.external_agent.email
}
