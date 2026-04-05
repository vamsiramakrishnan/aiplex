# Global HTTPS load balancer + Envoy AI Gateway
resource "google_compute_global_address" "aiplex" {
  name    = "aiplex-ip"
  project = var.project_id
}

# Managed SSL certificate
resource "google_compute_managed_ssl_certificate" "aiplex" {
  name    = "aiplex-cert"
  project = var.project_id

  managed {
    domains = [var.domain, "*.${var.domain}"]
  }
}

# Artifact Registry for container images
resource "google_artifact_registry_repository" "aiplex" {
  provider = google-beta

  location      = var.region
  repository_id = "aiplex"
  format        = "DOCKER"
  project       = var.project_id

  cleanup_policies {
    id     = "keep-recent"
    action = "KEEP"
    most_recent_versions {
      keep_count = 10
    }
  }
}

# DNS zone (if managing DNS via Cloud DNS)
resource "google_dns_managed_zone" "aiplex" {
  name     = "aiplex-zone"
  dns_name = "${var.domain}."
  project  = var.project_id
}

resource "google_dns_record_set" "aiplex_a" {
  name         = "${var.domain}."
  managed_zone = google_dns_managed_zone.aiplex.name
  type         = "A"
  ttl          = 300
  rrdatas      = [google_compute_global_address.aiplex.address]
  project      = var.project_id
}

# Outputs
output "cluster_endpoint" {
  value     = google_container_cluster.aiplex.endpoint
  sensitive = true
}

output "static_ip" {
  value = google_compute_global_address.aiplex.address
}

output "alloydb_primary_ip" {
  value     = google_alloydb_instance.primary.ip_address
  sensitive = true
}

output "artifact_registry" {
  value = "${var.region}-docker.pkg.dev/${var.project_id}/${google_artifact_registry_repository.aiplex.repository_id}"
}
