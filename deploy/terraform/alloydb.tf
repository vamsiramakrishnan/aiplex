# AlloyDB cluster for Ory Hydra + Kratos
resource "google_alloydb_cluster" "aiplex" {
  cluster_id = "aiplex-auth"
  location   = var.region
  project    = var.project_id

  network_config {
    network = google_compute_network.aiplex.id
  }

  initial_user {
    user     = "postgres"
    password = random_password.alloydb_password.result
  }

  automated_backup_policy {
    enabled = true
    backup_window = "02:00"
    weekly_schedule {
      days_of_week = ["MONDAY", "WEDNESDAY", "FRIDAY"]
    }
    quantity_based_retention {
      count = 7
    }
  }

  depends_on = [
    google_project_service.apis,
    google_service_networking_connection.private_vpc,
  ]
}

# Primary instance
resource "google_alloydb_instance" "primary" {
  cluster       = google_alloydb_cluster.aiplex.name
  instance_id   = "aiplex-auth-primary"
  instance_type = "PRIMARY"

  machine_config {
    cpu_count = 2
  }

  depends_on = [google_alloydb_cluster.aiplex]
}

# VPC for private connectivity
resource "google_compute_network" "aiplex" {
  name                    = "aiplex-network"
  auto_create_subnetworks = false
  project                 = var.project_id
}

resource "google_compute_subnetwork" "aiplex" {
  name          = "aiplex-subnet"
  ip_cidr_range = "10.0.0.0/20"
  region        = var.region
  network       = google_compute_network.aiplex.id
  project       = var.project_id

  secondary_ip_range {
    range_name    = "pods"
    ip_cidr_range = "10.1.0.0/16"
  }
  secondary_ip_range {
    range_name    = "services"
    ip_cidr_range = "10.2.0.0/20"
  }
}

# Private services access for AlloyDB
resource "google_compute_global_address" "private_ip" {
  name          = "aiplex-private-ip"
  purpose       = "VPC_PEERING"
  address_type  = "INTERNAL"
  prefix_length = 16
  network       = google_compute_network.aiplex.id
  project       = var.project_id
}

resource "google_service_networking_connection" "private_vpc" {
  network                 = google_compute_network.aiplex.id
  service                 = "servicenetworking.googleapis.com"
  reserved_peering_ranges = [google_compute_global_address.private_ip.name]
}

resource "random_password" "alloydb_password" {
  length  = 32
  special = true
}

# Store password in Secret Manager
resource "google_secret_manager_secret" "alloydb_password" {
  secret_id = "alloydb-password"
  project   = var.project_id

  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "alloydb_password" {
  secret      = google_secret_manager_secret.alloydb_password.id
  secret_data = random_password.alloydb_password.result
}
