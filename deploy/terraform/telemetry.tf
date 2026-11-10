# Identity for the in-cluster OpenTelemetry collector (Helm
# templates/otel-collector.yaml), which exports to Cloud Trace and Cloud
# Monitoring. Keep this variable equal to the chart's
# `otel.enabled && otel.collector` (both default true).
#
# A dedicated service account, so the collector does not borrow the API's
# identity. roles/cloudtrace.agent and roles/monitoring.metricWriter can only
# be granted at project level; neither is an admin role.
variable "otel_collector" {
  description = "Create the OpenTelemetry collector's identity (match Helm otel.enabled and otel.collector)"
  type        = bool
  default     = true
}

resource "google_service_account" "otel_collector" {
  count        = var.otel_collector ? 1 : 0
  account_id   = "aiplex-otel-collector"
  display_name = "AIPlex OpenTelemetry collector"
  project      = var.project_id
}

resource "google_service_account_iam_member" "otel_collector_workload_identity" {
  count              = var.otel_collector ? 1 : 0
  service_account_id = google_service_account.otel_collector[0].name
  role               = "roles/iam.workloadIdentityUser"
  member             = "serviceAccount:${var.project_id}.svc.id.goog[aiplex-system/otel-collector]"
}

resource "google_project_iam_member" "otel_collector_trace" {
  count   = var.otel_collector ? 1 : 0
  project = var.project_id
  role    = "roles/cloudtrace.agent"
  member  = "serviceAccount:${google_service_account.otel_collector[0].email}"
}

resource "google_project_iam_member" "otel_collector_metrics" {
  count   = var.otel_collector ? 1 : 0
  project = var.project_id
  role    = "roles/monitoring.metricWriter"
  member  = "serviceAccount:${google_service_account.otel_collector[0].email}"
}
