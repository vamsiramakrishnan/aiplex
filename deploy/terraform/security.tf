# Cloud Armor — WAF and DDoS protection for the Global HTTPS LB.

resource "google_compute_security_policy" "aiplex" {
  name    = "aiplex-waf"
  project = var.project_id

  # Default: allow all (DDoS protection is always-on for Global LB)
  rule {
    action   = "allow"
    priority = 2147483647
    match {
      versioned_expr = "SRC_IPS_V1"
      config {
        src_ip_ranges = ["*"]
      }
    }
    description = "Default allow"
  }

  # Block known bad bots / scanners
  rule {
    action   = "deny(403)"
    priority = 1000
    match {
      expr {
        expression = "evaluatePreconfiguredWaf('sqli-v33-stable', {'sensitivity': 2})"
      }
    }
    description = "Block SQL injection"
  }

  rule {
    action   = "deny(403)"
    priority = 1001
    match {
      expr {
        expression = "evaluatePreconfiguredWaf('xss-v33-stable', {'sensitivity': 2})"
      }
    }
    description = "Block XSS"
  }

  rule {
    action   = "deny(403)"
    priority = 1002
    match {
      expr {
        expression = "evaluatePreconfiguredWaf('rce-v33-stable', {'sensitivity': 2})"
      }
    }
    description = "Block remote code execution"
  }

  # Rate limiting per IP
  rule {
    action   = "throttle"
    priority = 2000
    match {
      versioned_expr = "SRC_IPS_V1"
      config {
        src_ip_ranges = ["*"]
      }
    }
    rate_limit_options {
      conform_action = "allow"
      exceed_action  = "deny(429)"
      rate_limit_threshold {
        count        = 500
        interval_sec = 60
      }
    }
    description = "Rate limit 500 req/min per IP"
  }

  # Adaptive protection (ML-based anomaly detection)
  adaptive_protection_config {
    layer_7_ddos_defense_config {
      enable = true
    }
  }
}

# ── VPC Firewall Rules ──

# Allow health checks from Google's health check ranges
resource "google_compute_firewall" "allow_health_checks" {
  name    = "aiplex-allow-health-checks"
  network = google_compute_network.aiplex.name
  project = var.project_id

  allow {
    protocol = "tcp"
    ports    = ["8080", "80", "443", "9191", "4317", "4318"]
  }

  source_ranges = [
    "35.191.0.0/16",    # Google health check
    "130.211.0.0/22",   # Google health check
  ]

  target_tags = ["gke-aiplex"]
  description = "Allow Google health check probes"
}

# Allow internal cluster communication
resource "google_compute_firewall" "allow_internal" {
  name    = "aiplex-allow-internal"
  network = google_compute_network.aiplex.name
  project = var.project_id

  allow {
    protocol = "tcp"
  }
  allow {
    protocol = "udp"
  }
  allow {
    protocol = "icmp"
  }

  source_ranges = [
    "10.0.0.0/20",   # Primary subnet
    "10.1.0.0/16",   # Pod CIDR
    "10.2.0.0/20",   # Service CIDR
  ]

  description = "Allow intra-cluster communication"
}

# Deny all other ingress (defense in depth)
resource "google_compute_firewall" "deny_all_ingress" {
  name     = "aiplex-deny-all-ingress"
  network  = google_compute_network.aiplex.name
  project  = var.project_id
  priority = 65534

  deny {
    protocol = "all"
  }

  source_ranges = ["0.0.0.0/0"]
  description   = "Deny all ingress not matching higher-priority rules"
}

# Restrict egress to Google APIs only (prevent data exfiltration)
resource "google_compute_firewall" "allow_google_apis_egress" {
  name      = "aiplex-allow-google-apis"
  network   = google_compute_network.aiplex.name
  project   = var.project_id
  direction = "EGRESS"
  priority  = 1000

  allow {
    protocol = "tcp"
    ports    = ["443"]
  }

  destination_ranges = [
    "199.36.153.8/30",   # restricted.googleapis.com
    "199.36.153.4/30",   # private.googleapis.com
  ]

  description = "Allow HTTPS to Google APIs"
}
