# GKE Autopilot cluster with Cloud Service Mesh
resource "google_container_cluster" "aiplex" {
  provider = google-beta

  name     = "aiplex"
  location = var.region
  project  = var.project_id

  enable_autopilot = true

  # Binary Authorization — only signed images can run
  binary_authorization {
    evaluation_mode = "PROJECT_SINGLETON_POLICY_ENFORCE"
  }

  # Cloud Service Mesh (managed Istio)
  mesh {
    management = "MANAGEMENT_AUTOMATIC"
  }

  # Gateway API
  gateway_api_config {
    channel = "CHANNEL_STANDARD"
  }

  # Workload Identity
  workload_identity_config {
    workload_pool = "${var.project_id}.svc.id.goog"
  }

  # Private cluster
  private_cluster_config {
    enable_private_nodes    = true
    enable_private_endpoint = false
    master_ipv4_cidr_block  = "172.16.0.0/28"
  }

  # Logging and monitoring
  logging_config {
    enable_components = ["SYSTEM_COMPONENTS", "WORKLOADS"]
  }
  monitoring_config {
    enable_components = ["SYSTEM_COMPONENTS", "WORKLOADS"]
    managed_prometheus {
      enabled = true
    }
  }

  release_channel {
    channel = "RAPID"
  }

  deletion_protection = true

  depends_on = [google_project_service.apis]
}

# Kubernetes provider config
data "google_client_config" "default" {}

provider "kubernetes" {
  host                   = "https://${google_container_cluster.aiplex.endpoint}"
  token                  = data.google_client_config.default.access_token
  cluster_ca_certificate = base64decode(google_container_cluster.aiplex.master_auth[0].cluster_ca_certificate)
}

provider "helm" {
  kubernetes {
    host                   = "https://${google_container_cluster.aiplex.endpoint}"
    token                  = data.google_client_config.default.access_token
    cluster_ca_certificate = base64decode(google_container_cluster.aiplex.master_auth[0].cluster_ca_certificate)
  }
}
