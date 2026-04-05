# Backup and disaster recovery configuration.

# ── Firestore Scheduled Export ──
# Uses Cloud Scheduler + Cloud Functions to export Firestore daily to GCS.

resource "google_storage_bucket" "firestore_backups" {
  name          = "${var.project_id}-aiplex-firestore-backups"
  project       = var.project_id
  location      = var.region
  force_destroy = false

  lifecycle_rule {
    condition {
      age = 30 # Keep backups for 30 days
    }
    action {
      type = "Delete"
    }
  }

  versioning {
    enabled = true
  }

  uniform_bucket_level_access = true
}

# Service account for Firestore export operations
resource "google_service_account" "firestore_export" {
  account_id   = "aiplex-firestore-export"
  display_name = "AIPlex Firestore Export"
  project      = var.project_id
}

resource "google_project_iam_member" "firestore_export_datastore" {
  project = var.project_id
  role    = "roles/datastore.importExportAdmin"
  member  = "serviceAccount:${google_service_account.firestore_export.email}"
}

resource "google_storage_bucket_iam_member" "firestore_export_writer" {
  bucket = google_storage_bucket.firestore_backups.name
  role   = "roles/storage.admin"
  member = "serviceAccount:${google_service_account.firestore_export.email}"
}

# Cloud Scheduler job — triggers Firestore export daily at 03:00 UTC
resource "google_cloud_scheduler_job" "firestore_export" {
  name        = "aiplex-firestore-daily-export"
  project     = var.project_id
  region      = var.region
  schedule    = "0 3 * * *"
  description = "Daily Firestore export to GCS for disaster recovery"
  time_zone   = "UTC"

  http_target {
    http_method = "POST"
    uri         = "https://firestore.googleapis.com/v1/projects/${var.project_id}/databases/(default):exportDocuments"
    body = base64encode(jsonencode({
      outputUriPrefix = "gs://${google_storage_bucket.firestore_backups.name}/exports"
      collectionIds   = [] # Empty = export all collections
    }))
    headers = {
      "Content-Type" = "application/json"
    }
    oauth_token {
      service_account_email = google_service_account.firestore_export.email
    }
  }

  depends_on = [google_project_service.apis]
}

# ── Terraform State Bucket Versioning ──
# Ensure the state bucket has versioning enabled for recovery.
# (The bucket itself is created by `aiplex init` / platform apply)

resource "google_storage_bucket" "terraform_state" {
  name          = "aiplex-terraform-state"
  project       = var.project_id
  location      = var.region
  force_destroy = false

  versioning {
    enabled = true
  }

  lifecycle_rule {
    condition {
      num_newer_versions = 10 # Keep last 10 state versions
    }
    action {
      type = "Delete"
    }
  }

  uniform_bucket_level_access = true
}
