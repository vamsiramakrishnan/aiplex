# Binary Authorization — only cryptographically signed images run on GKE

# KMS key ring for signing attestations
resource "google_kms_key_ring" "aiplex" {
  name     = "aiplex-keys"
  location = var.region
  project  = var.project_id

  depends_on = [google_project_service.apis]
}

# Asymmetric signing key for Binary Authorization
resource "google_kms_crypto_key" "signer" {
  name     = "aiplex-signer"
  key_ring = google_kms_key_ring.aiplex.id
  purpose  = "ASYMMETRIC_SIGN"

  version_template {
    algorithm        = "EC_SIGN_P256_SHA256"
    protection_level = "SOFTWARE"
  }

  lifecycle {
    prevent_destroy = true
  }
}

# Binary Authorization attestor
resource "google_binary_authorization_attestor" "aiplex" {
  name    = "aiplex-attestor"
  project = var.project_id

  attestation_authority_note {
    note_reference = google_container_analysis_note.aiplex.name

    public_keys {
      id = google_kms_crypto_key.signer.id
      pkix_public_key {
        public_key_pem      = data.google_kms_crypto_key_version.signer.public_key[0].pem
        signature_algorithm = "ECDSA_P256_SHA256"
      }
    }
  }
}

# Container Analysis note (required by attestor)
resource "google_container_analysis_note" "aiplex" {
  name    = "aiplex-attestor-note"
  project = var.project_id

  attestation_authority {
    hint {
      human_readable_name = "AIPlex Build Attestor"
    }
  }
}

# Get the public key from KMS
data "google_kms_crypto_key_version" "signer" {
  crypto_key = google_kms_crypto_key.signer.id
}

# Binary Authorization policy on the GKE cluster
resource "google_binary_authorization_policy" "aiplex" {
  project = var.project_id

  global_policy_evaluation_mode = "ENABLE"

  default_admission_rule {
    evaluation_mode  = "REQUIRE_ATTESTATION"
    enforcement_mode = "ENFORCED_BLOCK_AND_AUDIT_LOG"

    require_attestations_by = [
      google_binary_authorization_attestor.aiplex.name,
    ]
  }

  # Allow system namespaces (GKE system images)
  admission_whitelist_patterns {
    name_pattern = "gcr.io/google-containers/*"
  }
  admission_whitelist_patterns {
    name_pattern = "gcr.io/gke-release/*"
  }
  admission_whitelist_patterns {
    name_pattern = "gke.gcr.io/*"
  }
  # Allow Ory images
  admission_whitelist_patterns {
    name_pattern = "docker.io/oryd/*"
  }
  # Allow OPA
  admission_whitelist_patterns {
    name_pattern = "docker.io/openpolicyagent/*"
  }
  # Allow OTel
  admission_whitelist_patterns {
    name_pattern = "docker.io/otel/*"
  }

  # Cluster-specific override: aiplex cluster requires attestation
  cluster_admission_rules {
    cluster                 = "${var.region}.aiplex"
    evaluation_mode         = "REQUIRE_ATTESTATION"
    enforcement_mode        = "ENFORCED_BLOCK_AND_AUDIT_LOG"
    require_attestations_by = [
      google_binary_authorization_attestor.aiplex.name,
    ]
  }
}

# Cloud Build service account needs KMS signing permission
resource "google_kms_crypto_key_iam_member" "cloudbuild_signer" {
  crypto_key_id = google_kms_crypto_key.signer.id
  role          = "roles/cloudkms.signerVerifier"
  member        = "serviceAccount:${data.google_project.current.number}@cloudbuild.gserviceaccount.com"
}

# Cloud Build needs Binary Authorization attestor permission
resource "google_binary_authorization_attestor_iam_member" "cloudbuild" {
  project  = var.project_id
  attestor = google_binary_authorization_attestor.aiplex.name
  role     = "roles/binaryauthorization.attestorsVerifier"
  member   = "serviceAccount:${data.google_project.current.number}@cloudbuild.gserviceaccount.com"
}

data "google_project" "current" {
  project_id = var.project_id
}

# Grant Cloud Build permission to create attestations
resource "google_project_iam_member" "cloudbuild_containeranalysis" {
  project = var.project_id
  role    = "roles/containeranalysis.notes.attacher"
  member  = "serviceAccount:${data.google_project.current.number}@cloudbuild.gserviceaccount.com"
}
