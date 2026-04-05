# Firestore database for AIPlex metadata
resource "google_firestore_database" "aiplex" {
  project     = var.project_id
  name        = "(default)"
  location_id = var.region
  type        = "FIRESTORE_NATIVE"

  # Optimistic concurrency via ETags
  concurrency_mode        = "OPTIMISTIC"
  app_engine_integration_mode = "DISABLED"

  depends_on = [google_project_service.apis]
}

# Composite indexes for common queries
resource "google_firestore_index" "instances_by_plane" {
  project    = var.project_id
  database   = google_firestore_database.aiplex.name
  collection = "instances"

  fields {
    field_path = "plane"
    order      = "ASCENDING"
  }
  fields {
    field_path = "status"
    order      = "ASCENDING"
  }
  fields {
    field_path = "deployed_at"
    order      = "DESCENDING"
  }
}

resource "google_firestore_index" "instances_by_owner" {
  project    = var.project_id
  database   = google_firestore_database.aiplex.name
  collection = "instances"

  fields {
    field_path = "owner"
    order      = "ASCENDING"
  }
  fields {
    field_path = "deployed_at"
    order      = "DESCENDING"
  }
}

resource "google_firestore_index" "history_by_instance" {
  project    = var.project_id
  database   = google_firestore_database.aiplex.name
  collection = "deploy_history"

  fields {
    field_path = "instance_id"
    order      = "ASCENDING"
  }
  fields {
    field_path = "timestamp"
    order      = "DESCENDING"
  }
}

resource "google_firestore_index" "templates_by_plane" {
  project    = var.project_id
  database   = google_firestore_database.aiplex.name
  collection = "templates"

  fields {
    field_path = "plane"
    order      = "ASCENDING"
  }
  fields {
    field_path = "name"
    order      = "ASCENDING"
  }
}
