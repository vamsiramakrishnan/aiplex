terraform {
  required_version = ">= 1.7"
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 5.30"
    }
    google-beta = {
      source  = "hashicorp/google-beta"
      version = "~> 5.30"
    }
    helm = {
      source  = "hashicorp/helm"
      version = "~> 2.14"
    }
    kubernetes = {
      source  = "hashicorp/kubernetes"
      version = "~> 2.31"
    }
  }
  # Backend configured dynamically by `aiplex platform apply`:
  #   terraform init -backend-config="bucket={project_id}-aiplex-tfstate"
  # This avoids hardcoding the bucket name per environment.
  backend "gcs" {
    prefix = "aiplex"
  }
}

variable "project_id" {
  description = "GCP project ID"
  type        = string
}

variable "region" {
  description = "GCP region"
  type        = string
  default     = "us-central1"
}

variable "domain" {
  description = "Domain for AIPlex (e.g. aiplex.example.com)"
  type        = string
}

provider "google" {
  project = var.project_id
  region  = var.region
}

provider "google-beta" {
  project = var.project_id
  region  = var.region
}

# Enable required APIs. This list must equal gcpsetup.TerraformAPIs()
# (internal/gcpsetup); TestTerraformAPIListMatchesTable fails when they differ.
# cloudresourcemanager and serviceusage are not here: they must already be
# enabled for Terraform to enable anything (aiplex platform preflight checks).
resource "google_project_service" "apis" {
  for_each = toset([
    "alloydb.googleapis.com",
    "artifactregistry.googleapis.com",
    "binaryauthorization.googleapis.com",
    "certificatemanager.googleapis.com",
    "cloudkms.googleapis.com",
    "cloudscheduler.googleapis.com",
    "cloudtrace.googleapis.com",
    "compute.googleapis.com",
    "container.googleapis.com",
    "containeranalysis.googleapis.com",
    "dns.googleapis.com",
    "firestore.googleapis.com",
    "iam.googleapis.com",
    "mesh.googleapis.com",
    "monitoring.googleapis.com",
    "privateca.googleapis.com",
    "secretmanager.googleapis.com",
    "servicenetworking.googleapis.com",
    "storage.googleapis.com",
  ])
  service            = each.value
  disable_on_destroy = false
}
