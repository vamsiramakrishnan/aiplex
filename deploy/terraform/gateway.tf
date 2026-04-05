# ─── Global HTTPS Load Balancer ──────────────────────────

resource "google_compute_global_address" "aiplex" {
  name    = "aiplex-ip"
  project = var.project_id
}

# ─── Certificate Manager (auto-provisioned, auto-renewed) ─

# Certificate Manager DNS authorization — proves domain ownership
resource "google_certificate_manager_dns_authorization" "aiplex" {
  name    = "aiplex-dns-auth"
  project = var.project_id
  domain  = var.domain
}

# Wildcard DNS authorization for *.domain
resource "google_certificate_manager_dns_authorization" "aiplex_wildcard" {
  name    = "aiplex-dns-auth-wildcard"
  project = var.project_id
  domain  = var.domain
}

# DNS records for certificate validation (auto-created in Cloud DNS)
resource "google_dns_record_set" "cert_validation" {
  name         = google_certificate_manager_dns_authorization.aiplex.dns_resource_record[0].name
  managed_zone = google_dns_managed_zone.aiplex.name
  type         = google_certificate_manager_dns_authorization.aiplex.dns_resource_record[0].type
  ttl          = 300
  rrdatas      = [google_certificate_manager_dns_authorization.aiplex.dns_resource_record[0].data]
  project      = var.project_id
}

resource "google_dns_record_set" "cert_validation_wildcard" {
  name         = google_certificate_manager_dns_authorization.aiplex_wildcard.dns_resource_record[0].name
  managed_zone = google_dns_managed_zone.aiplex.name
  type         = google_certificate_manager_dns_authorization.aiplex_wildcard.dns_resource_record[0].type
  ttl          = 300
  rrdatas      = [google_certificate_manager_dns_authorization.aiplex_wildcard.dns_resource_record[0].data]
  project      = var.project_id
}

# Certificate — auto-provisioned via DNS authorization, auto-renewed
resource "google_certificate_manager_certificate" "aiplex" {
  name    = "aiplex-cert"
  project = var.project_id

  managed {
    domains = [var.domain, "*.${var.domain}"]
    dns_authorizations = [
      google_certificate_manager_dns_authorization.aiplex.id,
      google_certificate_manager_dns_authorization.aiplex_wildcard.id,
    ]
  }
}

# Certificate map — binds cert to load balancer
resource "google_certificate_manager_certificate_map" "aiplex" {
  name    = "aiplex-cert-map"
  project = var.project_id
}

resource "google_certificate_manager_certificate_map_entry" "aiplex" {
  name         = "aiplex-cert-entry"
  project      = var.project_id
  map          = google_certificate_manager_certificate_map.aiplex.name
  certificates = [google_certificate_manager_certificate.aiplex.id]
  hostname     = var.domain
}

resource "google_certificate_manager_certificate_map_entry" "aiplex_wildcard" {
  name         = "aiplex-cert-entry-wildcard"
  project      = var.project_id
  map          = google_certificate_manager_certificate_map.aiplex.name
  certificates = [google_certificate_manager_certificate.aiplex.id]
  hostname     = "*.${var.domain}"
}

# ─── Cloud DNS ─────────────────────────────────────────────

# DNS zone — managed by Terraform
resource "google_dns_managed_zone" "aiplex" {
  count    = var.manage_dns ? 1 : 0
  name     = "aiplex-zone"
  dns_name = "${var.domain}."
  project  = var.project_id

  dnssec_config {
    state = "on"
  }
}

# A record pointing domain to LB IP
resource "google_dns_record_set" "aiplex_a" {
  count        = var.manage_dns ? 1 : 0
  name         = "${var.domain}."
  managed_zone = google_dns_managed_zone.aiplex[0].name
  type         = "A"
  ttl          = 300
  rrdatas      = [google_compute_global_address.aiplex.address]
  project      = var.project_id
}

# Wildcard A record
resource "google_dns_record_set" "aiplex_wildcard" {
  count        = var.manage_dns ? 1 : 0
  name         = "*.${var.domain}."
  managed_zone = google_dns_managed_zone.aiplex[0].name
  type         = "A"
  ttl          = 300
  rrdatas      = [google_compute_global_address.aiplex.address]
  project      = var.project_id
}

# ─── Artifact Registry ──────────────────────────────────────

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

# ─── Variables ──────────────────────────────────────────────

variable "manage_dns" {
  description = "Whether to create and manage a Cloud DNS zone (set false if DNS is external)"
  type        = bool
  default     = true
}

# ─── Outputs ────────────────────────────────────────────────

output "cluster_endpoint" {
  value     = google_container_cluster.aiplex.endpoint
  sensitive = true
}

output "static_ip" {
  value       = google_compute_global_address.aiplex.address
  description = "Global load balancer IP — point your domain here"
}

output "alloydb_primary_ip" {
  value     = google_alloydb_instance.primary.ip_address
  sensitive = true
}

output "artifact_registry" {
  value = "${var.region}-docker.pkg.dev/${var.project_id}/${google_artifact_registry_repository.aiplex.repository_id}"
}

output "dns_nameservers" {
  value       = var.manage_dns ? google_dns_managed_zone.aiplex[0].name_servers : []
  description = "Point your domain registrar's NS records to these nameservers"
}

output "cert_status" {
  value       = google_certificate_manager_certificate.aiplex.managed[0].state
  description = "Certificate provisioning status (ACTIVE = ready)"
}

output "domain" {
  value = var.domain
}

output "setup_instructions" {
  value = var.manage_dns ? join("\n", [
    "DNS is managed by Cloud DNS. Point your registrar's NS records to:",
    join(", ", google_dns_managed_zone.aiplex[0].name_servers),
    "",
    "Static IP: ${google_compute_global_address.aiplex.address}",
    "Cert status: ${google_certificate_manager_certificate.aiplex.managed[0].state}",
    "",
    "Once NS records propagate (~minutes), certs auto-provision via DNS-01.",
    "No manual cert management needed. Certs auto-renew."
  ]) : join("\n", [
    "DNS is managed externally. Create these records:",
    "",
    "  ${var.domain}   A   ${google_compute_global_address.aiplex.address}",
    "  *.${var.domain}  A   ${google_compute_global_address.aiplex.address}",
    "",
    "Cert status: ${google_certificate_manager_certificate.aiplex.managed[0].state}",
  ])
}
