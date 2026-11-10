# Workload Identity Pool for external agents (AWS, Azure, on-prem)
resource "google_iam_workload_identity_pool" "aiplex" {
  provider                  = google-beta
  project                   = var.project_id
  workload_identity_pool_id = "aiplex-agents"
  display_name              = "AIPlex Agent Identity Pool"
  description               = "Workload identity pool for external AI agents"
}

# AWS provider (agents running on AWS)
resource "google_iam_workload_identity_pool_provider" "aws" {
  provider                           = google-beta
  project                            = var.project_id
  workload_identity_pool_id          = google_iam_workload_identity_pool.aiplex.workload_identity_pool_id
  workload_identity_pool_provider_id = "aws-agents"
  display_name                       = "AWS Agents"

  aws {
    account_id = "123456789012" # Placeholder — configure per deployment
  }

  attribute_mapping = {
    "google.subject"        = "assertion.arn"
    "attribute.aws_account" = "assertion.account"
    "attribute.aws_role"    = "assertion.arn.extract('assumed-role/{role}/')"
  }
}

# Azure AD provider (agents running on Azure)
resource "google_iam_workload_identity_pool_provider" "azure" {
  provider                           = google-beta
  project                            = var.project_id
  workload_identity_pool_id          = google_iam_workload_identity_pool.aiplex.workload_identity_pool_id
  workload_identity_pool_provider_id = "azure-agents"
  display_name                       = "Azure Agents"

  oidc {
    issuer_uri = "https://sts.windows.net/AZURE_TENANT_ID/" # Configure per deployment
  }

  attribute_mapping = {
    "google.subject"       = "assertion.sub"
    "attribute.tenant_id"  = "assertion.tid"
  }
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

# Allow AWS agents with specific roles to impersonate the external agent SA
resource "google_service_account_iam_member" "aws_agent_wif" {
  service_account_id = google_service_account.external_agent.name
  role               = "roles/iam.workloadIdentityUser"
  member             = "principalSet://iam.googleapis.com/${google_iam_workload_identity_pool.aiplex.name}/attribute.aws_role/${var.aws_agent_role}"
}

variable "aws_agent_role" {
  description = "AWS IAM role name that external agents assume (e.g. aiplex-agent)"
  type        = string
  default     = "aiplex-agent"
}

# Allow Azure AD service principals to impersonate the external agent SA
resource "google_service_account_iam_member" "azure_agent_wif" {
  service_account_id = google_service_account.external_agent.name
  role               = "roles/iam.workloadIdentityUser"
  member             = "principalSet://iam.googleapis.com/${google_iam_workload_identity_pool.aiplex.name}/attribute.tenant_id/${var.azure_agent_tenant_id}"
}

variable "azure_agent_tenant_id" {
  description = "Azure AD tenant ID for external agents"
  type        = string
  default     = ""
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

# Allow external agent SA to create tokens for Hydra client_credentials flow
resource "google_service_account_iam_member" "external_agent_token_creator" {
  service_account_id = google_service_account.external_agent.name
  role               = "roles/iam.serviceAccountTokenCreator"
  member             = "serviceAccount:${google_service_account.external_agent.email}"
}

# ── Outputs ──

output "workload_identity_pool_name" {
  value = google_iam_workload_identity_pool.aiplex.name
}

output "external_agent_sa_email" {
  value = google_service_account.external_agent.email
}
