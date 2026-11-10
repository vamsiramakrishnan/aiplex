# Which VPC the cluster runs on.
#
#   "default" - the project's default network. What every deployment before this
#               variable used; the VPC firewall rules in security.tf do NOT
#               apply to it. Unset behaves exactly as before.
#   "aiplex"  - the dedicated aiplex-network / aiplex-subnet (alloydb.tf): the
#               firewall rules apply, pods and services use the subnet's
#               secondary ranges, nodes reach Google APIs through Private
#               Google Access, and a Cloud Router with Cloud NAT gives the
#               private nodes internet egress (model providers, MCP servers,
#               image pulls). `aiplex init` writes "aiplex" for new installs.
#
# A cluster's network cannot be changed in place: switching an EXISTING cluster
# from "default" to "aiplex" makes Terraform REPLACE it, which deletes the
# cluster and every workload on it. Plan, back up, and expect downtime; see
# docs/gcp-permissions.md ("Moving an existing cluster to the dedicated
# network"). deletion_protection = true stops an accidental replace.
variable "cluster_network" {
  description = "Network for the GKE cluster: \"default\" (the project's default network, the original behaviour) or \"aiplex\" (the dedicated VPC from alloydb.tf). Changing it on an existing cluster replaces the cluster."
  type        = string
  default     = "default"

  validation {
    condition     = contains(["default", "aiplex"], var.cluster_network)
    error_message = "cluster_network must be \"default\" or \"aiplex\"."
  }
}

locals {
  dedicated_network = var.cluster_network == "aiplex"
}

# Cloud NAT for the dedicated network. The cluster has private nodes, which have
# no external IP, so without NAT they cannot pull images from outside Google or
# reach model providers and other services on the internet. Google's guide for
# private GKE clusters: https://docs.cloud.google.com/nat/docs/gke-example
# Only created for cluster_network = "aiplex". Not validated against a live
# project.
resource "google_compute_router" "aiplex" {
  count   = local.dedicated_network ? 1 : 0
  name    = "aiplex-router"
  network = google_compute_network.aiplex.id
  region  = var.region
  project = var.project_id
}

resource "google_compute_router_nat" "aiplex" {
  count                              = local.dedicated_network ? 1 : 0
  name                               = "aiplex-nat"
  router                             = google_compute_router.aiplex[0].name
  region                             = var.region
  project                            = var.project_id
  nat_ip_allocate_option             = "AUTO_ONLY"
  source_subnetwork_ip_ranges_to_nat = "LIST_OF_SUBNETWORKS"

  # Node and pod addresses of the cluster subnet, and no other subnet.
  subnetwork {
    name                    = google_compute_subnetwork.aiplex.id
    source_ip_ranges_to_nat = ["ALL_IP_RANGES"]
  }

  log_config {
    enable = true
    filter = "ERRORS_ONLY"
  }
}

# GKE Autopilot cluster with Cloud Service Mesh
resource "google_container_cluster" "aiplex" {
  provider = google-beta

  name     = "aiplex"
  location = var.region
  project  = var.project_id

  enable_autopilot = true

  # Dedicated VPC (cluster_network = "aiplex"). With the default, network and
  # subnetwork stay unset and GKE uses the project's default network, so an
  # existing cluster sees no change. A custom VPC needs a VPC-native cluster
  # with named secondary ranges for pods and services (subnet "aiplex-subnet",
  # alloydb.tf); Autopilot clusters are always VPC-native:
  # https://docs.cloud.google.com/kubernetes-engine/docs/how-to/alias-ips
  network    = local.dedicated_network ? google_compute_network.aiplex.id : null
  subnetwork = local.dedicated_network ? google_compute_subnetwork.aiplex.id : null

  dynamic "ip_allocation_policy" {
    for_each = local.dedicated_network ? [1] : []
    content {
      cluster_secondary_range_name  = "pods"
      services_secondary_range_name = "services"
    }
  }

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

  # NAT before nodes, so the first image pulls have a route out.
  depends_on = [google_project_service.apis, google_compute_router_nat.aiplex]
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
