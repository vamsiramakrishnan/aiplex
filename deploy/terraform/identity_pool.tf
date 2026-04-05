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
resource "google_service_account_iam_member" "aiplex_api_workload_identity" {
  service_account_id = google_service_account.aiplex_api.name
  role               = "roles/iam.workloadIdentityUser"
  member             = "serviceAccount:${var.project_id}.svc.id.goog[aiplex-system/aiplex-api]"
}

# Firestore access for AIPlex API
resource "google_project_iam_member" "aiplex_api_firestore" {
  project = var.project_id
  role    = "roles/datastore.user"
  member  = "serviceAccount:${google_service_account.aiplex_api.email}"
}

# Secret Manager access for AIPlex API
resource "google_project_iam_member" "aiplex_api_secrets" {
  project = var.project_id
  role    = "roles/secretmanager.secretAccessor"
  member  = "serviceAccount:${google_service_account.aiplex_api.email}"
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

# Grant the external agent SA minimal permissions: IAP access + token creator
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
