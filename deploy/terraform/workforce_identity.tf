# Workforce Identity Federation — human users and groups from external IdPs
# authenticating to AIPlex itself. This is separate from the workload identity
# pool in identity_pool.tf which handles machine-to-machine (agent) auth.

variable "workforce_pool_id" {
  description = "Workforce identity pool ID"
  type        = string
  default     = "aiplex-users"
}

variable "organization_id" {
  description = "Google Cloud organization ID (required for workforce pools)"
  type        = string
}

# ── Workforce Identity Pool ──

resource "google_iam_workforce_pool" "aiplex_users" {
  provider          = google-beta
  workforce_pool_id = var.workforce_pool_id
  parent            = "organizations/${var.organization_id}"
  location          = "global"
  display_name      = "AIPlex Users"
  description       = "Workforce identity pool for human users accessing AIPlex via corporate IdPs"

  session_duration = "28800s" # 8 hours
}

# ── Google Workspace / Cloud Identity OIDC Provider ──

resource "google_iam_workforce_pool_provider" "google_workspace" {
  provider              = google-beta
  workforce_pool_id     = google_iam_workforce_pool.aiplex_users.workforce_pool_id
  location              = google_iam_workforce_pool.aiplex_users.location
  workforce_pool_provider_id = "google-workspace"
  display_name          = "Google Workspace"
  description           = "Google Workspace users (students, teachers, admins)"

  oidc {
    issuer_uri = "https://accounts.google.com"
    client_id  = var.google_workspace_client_id
    web_sso_config {
      response_type             = "CODE"
      assertion_claims_behavior = "MERGE_USER_INFO_OVER_ID_TOKEN_CLAIMS"
    }
  }

  attribute_mapping = {
    "google.subject"       = "assertion.sub"
    "google.display_name"  = "assertion.name"
    "attribute.email"      = "assertion.email"
    "attribute.groups"     = "assertion.groups"
    "attribute.domain"     = "assertion.hd"
  }

  # Restrict to specific Google Workspace domain(s)
  attribute_condition = "assertion.hd == '${var.google_workspace_domain}'"
}

variable "google_workspace_client_id" {
  description = "OAuth client ID for Google Workspace OIDC"
  type        = string
}

variable "google_workspace_domain" {
  description = "Google Workspace domain (e.g. school.edu)"
  type        = string
}

# ── Azure AD OIDC Provider (corporate users via Azure) ──

resource "google_iam_workforce_pool_provider" "azure_ad" {
  provider              = google-beta
  workforce_pool_id     = google_iam_workforce_pool.aiplex_users.workforce_pool_id
  location              = google_iam_workforce_pool.aiplex_users.location
  workforce_pool_provider_id = "azure-ad"
  display_name          = "Azure AD"
  description           = "Azure AD users from corporate tenants"

  oidc {
    issuer_uri = "https://login.microsoftonline.com/${var.azure_tenant_id}/v2.0"
    client_id  = var.azure_ad_client_id
    web_sso_config {
      response_type             = "CODE"
      assertion_claims_behavior = "MERGE_USER_INFO_OVER_ID_TOKEN_CLAIMS"
    }
  }

  attribute_mapping = {
    "google.subject"       = "assertion.sub"
    "google.display_name"  = "assertion.name"
    "attribute.email"      = "assertion.email"
    "attribute.groups"     = "assertion.groups"
    "attribute.tenant_id"  = "assertion.tid"
  }
}

variable "azure_tenant_id" {
  description = "Azure AD tenant ID for corporate users"
  type        = string
  default     = ""
}

variable "azure_ad_client_id" {
  description = "Azure AD OAuth client ID"
  type        = string
  default     = ""
}

# ── Okta OIDC Provider ──

resource "google_iam_workforce_pool_provider" "okta" {
  count    = var.okta_issuer_uri != "" ? 1 : 0
  provider = google-beta

  workforce_pool_id          = google_iam_workforce_pool.aiplex_users.workforce_pool_id
  location                   = google_iam_workforce_pool.aiplex_users.location
  workforce_pool_provider_id = "okta"
  display_name               = "Okta"
  description                = "Okta SSO users"

  oidc {
    issuer_uri = var.okta_issuer_uri
    client_id  = var.okta_client_id
    web_sso_config {
      response_type             = "CODE"
      assertion_claims_behavior = "MERGE_USER_INFO_OVER_ID_TOKEN_CLAIMS"
    }
  }

  attribute_mapping = {
    "google.subject"       = "assertion.sub"
    "google.display_name"  = "assertion.name"
    "attribute.email"      = "assertion.email"
    "attribute.groups"     = "assertion.groups"
  }
}

variable "okta_issuer_uri" {
  description = "Okta OIDC issuer URI (leave empty to disable)"
  type        = string
  default     = ""
}

variable "okta_client_id" {
  description = "Okta OAuth client ID"
  type        = string
  default     = ""
}

# ── IAM Policy Bindings: Workforce Groups → GCP Project Roles ──
#
# These grant WIF-authenticated groups the ability to call the AIPlex API
# (which runs on GKE with IAP). The AIPlex API then handles fine-grained
# authorization (Dimension A/B/C scopes) internally via Ory Hydra tokens.

# AIPlex Admins — full access to the AIPlex API + GCP project viewer
resource "google_project_iam_member" "wif_admins_iap" {
  project = var.project_id
  role    = "roles/iap.httpsResourceAccessor"
  member  = "principalSet://iam.googleapis.com/${google_iam_workforce_pool.aiplex_users.name}/group/aiplex-admins"
}

# AIPlex Deployers — can access AIPlex API via IAP (deploy + manage instances)
resource "google_project_iam_member" "wif_deployers_iap" {
  project = var.project_id
  role    = "roles/iap.httpsResourceAccessor"
  member  = "principalSet://iam.googleapis.com/${google_iam_workforce_pool.aiplex_users.name}/group/aiplex-deployers"
}

# AIPlex Viewers — read-only access via IAP
resource "google_project_iam_member" "wif_viewers_iap" {
  project = var.project_id
  role    = "roles/iap.httpsResourceAccessor"
  member  = "principalSet://iam.googleapis.com/${google_iam_workforce_pool.aiplex_users.name}/group/aiplex-viewers"
}

# ── Custom IAM Role for AIPlex API Access ──
# This restricts what WIF-authenticated principals can do at the GCP level.

resource "google_project_iam_custom_role" "aiplex_user" {
  role_id     = "aiplexUser"
  title       = "AIPlex User"
  description = "Minimal GCP permissions for accessing the AIPlex platform via IAP"
  project     = var.project_id
  permissions = [
    "iap.webServiceVersions.accessViaIAP",
  ]
}

# ── Outputs ──

output "workforce_pool_name" {
  description = "Full resource name of the workforce identity pool"
  value       = google_iam_workforce_pool.aiplex_users.name
}

output "workforce_pool_id" {
  value = google_iam_workforce_pool.aiplex_users.workforce_pool_id
}

output "google_workspace_provider" {
  description = "Google Workspace provider resource name"
  value       = google_iam_workforce_pool_provider.google_workspace.name
}
