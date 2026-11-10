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

# The export caller needs datastore.databases.export (and reads the database
# metadata and the export operation). roles/datastore.importExportAdmin would
# also grant datastore.databases.import, which can overwrite live data, so the
# export service account gets a custom role with export only. Role contents:
# https://docs.cloud.google.com/iam/docs/roles-permissions/firestore
resource "google_project_iam_custom_role" "firestore_exporter" {
  project     = var.project_id
  role_id     = "aiplexFirestoreExporter"
  title       = "AIPlex Firestore Exporter"
  description = "Start Firestore exports and read their status; no import."
  permissions = [
    "datastore.databases.export",
    "datastore.databases.getMetadata",
    "datastore.operations.get",
  ]
}

resource "google_project_iam_member" "firestore_export_datastore" {
  project = var.project_id
  role    = google_project_iam_custom_role.firestore_exporter.id
  member  = "serviceAccount:${google_service_account.firestore_export.email}"
}

# Bucket access for the export. Google's export guide lists
# storage.buckets.get, storage.objects.create, storage.objects.delete and
# storage.objects.list for a custom role on the bucket
# (https://docs.cloud.google.com/datastore/docs/export-import-entities);
# roles/storage.legacyBucketWriter holds those on this one bucket, without the
# storage.buckets.setIamPolicy, update and delete that roles/storage.admin had.
# Not validated against a live export: if exports fail with a storage
# permission error, see docs/gcp-permissions.md.
resource "google_storage_bucket_iam_member" "firestore_export_writer" {
  bucket = google_storage_bucket.firestore_backups.name
  role   = "roles/storage.legacyBucketWriter"
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
