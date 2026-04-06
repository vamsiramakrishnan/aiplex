# Ory Hydra + Kratos deployed via Helm on GKE

# Hydra databases in AlloyDB
resource "null_resource" "hydra_db" {
  provisioner "local-exec" {
    command = <<-EOT
      gcloud alloydb databases create hydra \
        --cluster=aiplex-auth \
        --region=${var.region} \
        --project=${var.project_id}
    EOT
  }
  depends_on = [google_alloydb_instance.primary]
}

resource "null_resource" "kratos_db" {
  provisioner "local-exec" {
    command = <<-EOT
      gcloud alloydb databases create kratos \
        --cluster=aiplex-auth \
        --region=${var.region} \
        --project=${var.project_id}
    EOT
  }
  depends_on = [google_alloydb_instance.primary]
}

# AlloyDB Auth Proxy deployment (for in-cluster connectivity)
resource "kubernetes_deployment" "alloydb_proxy" {
  metadata {
    name      = "alloydb-proxy"
    namespace = "aiplex-system"
    labels = {
      app = "alloydb-proxy"
    }
  }

  spec {
    replicas = 2
    selector {
      match_labels = {
        app = "alloydb-proxy"
      }
    }
    template {
      metadata {
        labels = {
          app = "alloydb-proxy"
        }
      }
      spec {
        container {
          name  = "alloydb-proxy"
          image = "gcr.io/alloydb-connectors/alloydb-auth-proxy:latest"
          args = [
            "--structured-logs",
            "--port=5432",
            "projects/${var.project_id}/locations/${var.region}/clusters/aiplex-auth/instances/aiplex-auth-primary",
          ]
          port {
            container_port = 5432
          }
          resources {
            requests = {
              cpu    = "100m"
              memory = "128Mi"
            }
          }
        }
        service_account_name = "aiplex-api"
      }
    }
  }

  depends_on = [google_alloydb_instance.primary]
}

resource "kubernetes_service" "alloydb_proxy" {
  metadata {
    name      = "alloydb-proxy"
    namespace = "aiplex-system"
  }
  spec {
    selector = {
      app = "alloydb-proxy"
    }
    port {
      port        = 5432
      target_port = 5432
    }
  }
}

# Hydra secrets
resource "google_secret_manager_secret" "hydra_system" {
  secret_id = "hydra-system-secret"
  project   = var.project_id
  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "hydra_system" {
  secret      = google_secret_manager_secret.hydra_system.id
  secret_data = random_password.hydra_system.result
}

resource "random_password" "hydra_system" {
  length  = 32
  special = false
}

resource "google_secret_manager_secret" "hydra_pairwise_salt" {
  secret_id = "hydra-pairwise-salt"
  project   = var.project_id
  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "hydra_pairwise_salt" {
  secret      = google_secret_manager_secret.hydra_pairwise_salt.id
  secret_data = random_password.hydra_pairwise_salt.result
}

resource "random_password" "hydra_pairwise_salt" {
  length  = 32
  special = false
}

resource "google_secret_manager_secret" "kratos_cookie" {
  secret_id = "kratos-cookie-secret"
  project   = var.project_id
  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "kratos_cookie" {
  secret      = google_secret_manager_secret.kratos_cookie.id
  secret_data = random_password.kratos_cookie.result
}

resource "random_password" "kratos_cookie" {
  length  = 32
  special = false
}

resource "google_secret_manager_secret" "kratos_cipher" {
  secret_id = "kratos-cipher-secret"
  project   = var.project_id
  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "kratos_cipher" {
  secret      = google_secret_manager_secret.kratos_cipher.id
  secret_data = random_password.kratos_cipher.result
}

resource "random_password" "kratos_cipher" {
  length  = 32
  special = false
}

# Ory Hydra Helm release
resource "helm_release" "hydra" {
  name       = "hydra"
  namespace  = "aiplex-system"
  repository = "https://k8s.ory.sh/helm/charts"
  chart      = "hydra"
  version    = "0.46.0"

  values = [
    yamlencode({
      hydra = {
        config = {
          dsn = "postgres://hydra:${random_password.alloydb_password.result}@alloydb-proxy:5432/hydra?sslmode=disable"
          urls = {
            self = { issuer = "https://${var.domain}/auth/realms/aiplex" }
            consent = "https://${var.domain}/auth/consent"
            login   = "https://${var.domain}/auth/login"
            logout  = "https://${var.domain}/auth/logout"
          }
          oauth2 = {
            expose_internal_errors = false
            token_hook = {
              url = "http://aiplex-api.aiplex-system.svc.cluster.local:8080/auth/token-hook"
            }
          }
          strategies = {
            access_token = "jwt"
            scope        = "exact"
          }
          secrets = { system = [random_password.hydra_system.result] }
          oidc = {
            subject_identifiers = {
              supported_types = ["public", "pairwise"]
              pairwise = { salt = random_password.hydra_pairwise_salt.result }
            }
          }
        }
      }
      deployment = {
        resources = {
          requests = { cpu = "100m", memory = "128Mi" }
          limits   = { cpu = "500m", memory = "256Mi" }
        }
      }
      service = { admin = { enabled = true } }
    })
  ]

  depends_on = [
    google_container_cluster.aiplex,
    kubernetes_service.alloydb_proxy,
    null_resource.hydra_db,
  ]
}

# Ory Kratos Helm release
resource "helm_release" "kratos" {
  name       = "kratos"
  namespace  = "aiplex-system"
  repository = "https://k8s.ory.sh/helm/charts"
  chart      = "kratos"
  version    = "0.46.0"

  values = [
    yamlencode({
      kratos = {
        config = {
          dsn = "postgres://kratos:${random_password.alloydb_password.result}@alloydb-proxy:5432/kratos?sslmode=disable"
          secrets = {
            cookie = [random_password.kratos_cookie.result]
            cipher = [random_password.kratos_cipher.result]
          }
          serve = {
            public = {
              base_url = "https://${var.domain}/.ory/kratos/public/"
            }
          }
          identity = {
            default_schema_id = "user_v1"
            schemas = [{
              id  = "user_v1"
              url = "file:///etc/kratos/identity-schema.json"
            }]
          }
        }
        identitySchemas = {
          "identity-schema.json" = file("${path.module}/../ory/kratos-identity-schema.json")
        }
      }
      deployment = {
        resources = {
          requests = { cpu = "100m", memory = "128Mi" }
          limits   = { cpu = "500m", memory = "256Mi" }
        }
      }
    })
  ]

  depends_on = [
    google_container_cluster.aiplex,
    kubernetes_service.alloydb_proxy,
    null_resource.kratos_db,
  ]
}
