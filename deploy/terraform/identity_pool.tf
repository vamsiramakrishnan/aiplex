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
