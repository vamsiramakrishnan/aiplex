# Cloud Monitoring alerting policies for AIPlex.

# ── Notification channel (email) ──

variable "alert_email" {
  description = "Email address for alert notifications"
  type        = string
  default     = ""
}

resource "google_monitoring_notification_channel" "email" {
  count        = var.alert_email != "" ? 1 : 0
  display_name = "AIPlex Alerts"
  type         = "email"
  project      = var.project_id
  labels = {
    email_address = var.alert_email
  }
}

locals {
  notification_channels = var.alert_email != "" ? [google_monitoring_notification_channel.email[0].name] : []
}

# ── API Server Health ──

resource "google_monitoring_alert_policy" "api_health" {
  display_name = "AIPlex API Unhealthy"
  project      = var.project_id
  combiner     = "OR"

  conditions {
    display_name = "API pod restart rate"
    condition_threshold {
      filter          = "resource.type = \"k8s_container\" AND resource.labels.container_name = \"aiplex-api\" AND metric.type = \"kubernetes.io/container/restart_count\""
      comparison      = "COMPARISON_GT"
      threshold_value = 3
      duration        = "300s"
      aggregations {
        alignment_period   = "300s"
        per_series_aligner = "ALIGN_DELTA"
      }
    }
  }

  notification_channels = local.notification_channels
  alert_strategy {
    auto_close = "1800s"
  }
}

# ── High Error Rate ──

resource "google_monitoring_alert_policy" "high_error_rate" {
  display_name = "AIPlex High 5xx Error Rate"
  project      = var.project_id
  combiner     = "OR"

  conditions {
    display_name = "5xx error rate > 5%"
    condition_threshold {
      filter          = "resource.type = \"k8s_container\" AND resource.labels.namespace_name = \"aiplex-system\" AND metric.type = \"logging.googleapis.com/log_entry_count\" AND metric.labels.severity = \"ERROR\""
      comparison      = "COMPARISON_GT"
      threshold_value = 50
      duration        = "300s"
      aggregations {
        alignment_period   = "300s"
        per_series_aligner = "ALIGN_RATE"
      }
    }
  }

  notification_channels = local.notification_channels
}

# ── Certificate Expiration ──

resource "google_monitoring_alert_policy" "cert_expiry" {
  display_name = "AIPlex TLS Certificate Expiring"
  project      = var.project_id
  combiner     = "OR"

  conditions {
    display_name = "Certificate not ACTIVE"
    condition_threshold {
      filter          = "resource.type = \"certificatemanager.googleapis.com/Certificate\" AND metric.type = \"certificatemanager.googleapis.com/certificate/is_active\""
      comparison      = "COMPARISON_LT"
      threshold_value = 1
      duration        = "600s"
      aggregations {
        alignment_period   = "600s"
        per_series_aligner = "ALIGN_MIN"
      }
    }
  }

  notification_channels = local.notification_channels
}

# ── OPA Policy Denials Spike ──

resource "google_monitoring_alert_policy" "policy_denials" {
  display_name = "AIPlex Policy Denial Spike"
  project      = var.project_id
  combiner     = "OR"

  conditions {
    display_name = "OPA denials > 100/5min"
    condition_threshold {
      filter          = "resource.type = \"k8s_container\" AND resource.labels.container_name = \"opa\" AND metric.type = \"logging.googleapis.com/log_entry_count\""
      comparison      = "COMPARISON_GT"
      threshold_value = 100
      duration        = "300s"
      aggregations {
        alignment_period   = "300s"
        per_series_aligner = "ALIGN_RATE"
      }
    }
  }

  notification_channels = local.notification_channels
}

# ── AlloyDB High CPU ──

resource "google_monitoring_alert_policy" "alloydb_cpu" {
  display_name = "AlloyDB High CPU"
  project      = var.project_id
  combiner     = "OR"

  conditions {
    display_name = "AlloyDB CPU > 80%"
    condition_threshold {
      filter          = "resource.type = \"alloydb.googleapis.com/Instance\" AND metric.type = \"alloydb.googleapis.com/database/cpu/utilization\""
      comparison      = "COMPARISON_GT"
      threshold_value = 0.8
      duration        = "600s"
      aggregations {
        alignment_period   = "300s"
        per_series_aligner = "ALIGN_MEAN"
      }
    }
  }

  notification_channels = local.notification_channels
}

# ── GKE Node Pool Pressure ──

resource "google_monitoring_alert_policy" "node_pressure" {
  display_name = "GKE Memory Pressure"
  project      = var.project_id
  combiner     = "OR"

  conditions {
    display_name = "Node memory utilization > 85%"
    condition_threshold {
      filter          = "resource.type = \"k8s_node\" AND metric.type = \"kubernetes.io/node/memory/allocatable_utilization\""
      comparison      = "COMPARISON_GT"
      threshold_value = 0.85
      duration        = "600s"
      aggregations {
        alignment_period   = "300s"
        per_series_aligner = "ALIGN_MEAN"
      }
    }
  }

  notification_channels = local.notification_channels
}
