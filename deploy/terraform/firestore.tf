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
    field_path = "Plane"
    order      = "ASCENDING"
  }
  fields {
    field_path = "Status"
    order      = "ASCENDING"
  }
  fields {
    field_path = "DeployedAt"
    order      = "DESCENDING"
  }
}

resource "google_firestore_index" "instances_by_owner" {
  project    = var.project_id
  database   = google_firestore_database.aiplex.name
  collection = "instances"

  fields {
    field_path = "Owner"
    order      = "ASCENDING"
  }
  fields {
    field_path = "DeployedAt"
    order      = "DESCENDING"
  }
}

resource "google_firestore_index" "history_by_instance" {
  project    = var.project_id
  database   = google_firestore_database.aiplex.name
  collection = "deploy_history"

  fields {
    field_path = "InstanceID"
    order      = "ASCENDING"
  }
  fields {
    field_path = "Timestamp"
    order      = "DESCENDING"
  }
}

resource "google_firestore_index" "templates_by_plane" {
  project    = var.project_id
  database   = google_firestore_database.aiplex.name
  collection = "templates"

  fields {
    field_path = "Plane"
    order      = "ASCENDING"
  }
  fields {
    field_path = "Name"
    order      = "ASCENDING"
  }
}

# Typed model documents use Go field names; JSON tags do not rename Firestore
# fields. Equality filters precede Timestamp ordering and result limits.
resource "google_firestore_index" "usage_by_model" {
  project    = var.project_id
  database   = google_firestore_database.aiplex.name
  collection = "usage_records"
  fields {
    field_path = "ModelID"
    order      = "ASCENDING"
  }
  fields {
    field_path = "Timestamp"
    order      = "DESCENDING"
  }
}

resource "google_firestore_index" "usage_by_agent" {
  project    = var.project_id
  database   = google_firestore_database.aiplex.name
  collection = "usage_records"
  fields {
    field_path = "AgentID"
    order      = "ASCENDING"
  }
  fields {
    field_path = "Timestamp"
    order      = "DESCENDING"
  }
}

resource "google_firestore_index" "usage_by_model_agent" {
  project    = var.project_id
  database   = google_firestore_database.aiplex.name
  collection = "usage_records"
  fields {
    field_path = "ModelID"
    order      = "ASCENDING"
  }
  fields {
    field_path = "AgentID"
    order      = "ASCENDING"
  }
  fields {
    field_path = "Timestamp"
    order      = "DESCENDING"
  }
}

resource "google_firestore_index" "execution_runs_by_tenant_agent" {
  project    = var.project_id
  database   = google_firestore_database.aiplex.name
  collection = "execution_runs"
  fields {
    field_path = "TenantID"
    order      = "ASCENDING"
  }
  fields {
    field_path = "AgentID"
    order      = "ASCENDING"
  }
}

resource "google_firestore_index" "delegations_by_caller" {
  project    = var.project_id
  database   = google_firestore_database.aiplex.name
  collection = "delegations"
  fields {
    field_path = "CallerAgentID"
    order      = "ASCENDING"
  }
  fields {
    field_path = "StartedAt"
    order      = "DESCENDING"
  }
}

resource "google_firestore_index" "delegations_by_callee" {
  project    = var.project_id
  database   = google_firestore_database.aiplex.name
  collection = "delegations"
  fields {
    field_path = "CalleeAgentID"
    order      = "ASCENDING"
  }
  fields {
    field_path = "StartedAt"
    order      = "DESCENDING"
  }
}

resource "google_firestore_index" "delegations_by_trace" {
  project    = var.project_id
  database   = google_firestore_database.aiplex.name
  collection = "delegations"
  fields {
    field_path = "TraceID"
    order      = "ASCENDING"
  }
  fields {
    field_path = "StartedAt"
    order      = "DESCENDING"
  }
}

resource "google_firestore_index" "skills_by_agent" {
  project    = var.project_id
  database   = google_firestore_database.aiplex.name
  collection = "skill_invocations"
  fields {
    field_path = "AgentID"
    order      = "ASCENDING"
  }
  fields {
    field_path = "StartedAt"
    order      = "DESCENDING"
  }
}

resource "google_firestore_index" "skills_by_name" {
  project    = var.project_id
  database   = google_firestore_database.aiplex.name
  collection = "skill_invocations"
  fields {
    field_path = "SkillName"
    order      = "ASCENDING"
  }
  fields {
    field_path = "StartedAt"
    order      = "DESCENDING"
  }
}

resource "google_firestore_index" "skills_by_agent_name" {
  project    = var.project_id
  database   = google_firestore_database.aiplex.name
  collection = "skill_invocations"
  fields {
    field_path = "AgentID"
    order      = "ASCENDING"
  }
  fields {
    field_path = "SkillName"
    order      = "ASCENDING"
  }
  fields {
    field_path = "StartedAt"
    order      = "DESCENDING"
  }
}

# Usage ledger retention. Records live in per-tenant subcollections
# usage_ledger_tenants/{digest}/usage_ledger_records and carry expires_at;
# queries use only the automatic single-field index on started_at.
resource "google_firestore_field" "usage_ledger_ttl" {
  project    = var.project_id
  database   = google_firestore_database.aiplex.name
  collection = "usage_ledger_records"
  field      = "expires_at"

  ttl_config {}
}

# Policy denial retention. Denials reported by the authz sidecar and recorded
# by the API carry expires_at (30 days, api.DenialRetention); Firestore deletes
# them after that.
resource "google_firestore_field" "policy_denials_ttl" {
  project    = var.project_id
  database   = google_firestore_database.aiplex.name
  collection = "policy_denials"
  field      = "expires_at"

  ttl_config {}
}
