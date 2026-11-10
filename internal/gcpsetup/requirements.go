// Package gcpsetup is the single source of truth for what setting up AIPlex
// on Google Cloud needs from the person (or pipeline) that runs it: the IAM
// permissions per Terraform feature, the level each is checked at, and the
// APIs that must be (or will be) enabled.
//
// The table is derived from every resource in deploy/terraform/*.tf.
// TestEveryTerraformResourceTypeHasRequirements fails when a resource type
// appears there without an entry here, so the two cannot drift silently.
//
// Where the permissions come from. Permission names, and which predefined
// roles hold them, were read from Google's IAM role reference pages on
// 2026-11-10 (one page per service, cited per entry as
// https://docs.cloud.google.com/iam/docs/roles-permissions/<service>). A
// permission is listed as verified when it exists in that reference and is
// the permission for the create, read, update or delete call Terraform makes
// for the resource. A permission whose need could not be confirmed in Google's
// documentation is marked Unverified with the reason; the preflight output
// shows those separately instead of guessing silently.
//
// None of this has been run against a live Google Cloud project.
package gcpsetup

import (
	"sort"
	"strings"
)

// Level is the resource level a permission is checked (and granted) at.
type Level string

const (
	// LevelProject permissions are tested with projects.testIamPermissions.
	LevelProject Level = "project"
	// LevelOrganization permissions are tested with
	// organizations.testIamPermissions (workforce identity pools live there).
	LevelOrganization Level = "organization"
	// LevelStateBucket permissions are tested on the Terraform state bucket
	// when it exists, and at project level otherwise.
	LevelStateBucket Level = "state-bucket"
)

// FeatureID names a group of Terraform resources that is switched on or off
// together.
type FeatureID string

const (
	FeatureState      FeatureID = "state"
	FeatureCore       FeatureID = "core"
	FeatureDNS        FeatureID = "dns"
	FeatureBackup     FeatureID = "backup"
	FeatureBinaryAuth FeatureID = "binary-authorization"
	FeatureWorkforce  FeatureID = "workforce-identity"
	FeatureAlertEmail FeatureID = "alert-email"
	FeatureTelemetry  FeatureID = "telemetry"
	// FeatureDedicatedNetwork is the GKE cluster on the aiplex VPC with Cloud NAT.
	FeatureDedicatedNetwork FeatureID = "dedicated-network"
	// FeatureExternalAgents is the AWS and Azure workload identity providers.
	FeatureExternalAgents FeatureID = "external-agent-federation"
)

// Feature is one switchable part of the Terraform.
type Feature struct {
	ID    FeatureID
	Title string
	// Toggle says which Terraform variable turns the feature on.
	Toggle string
	// Default reports whether the feature is on with default variables.
	Default bool
	// APIs Terraform enables for the feature (google_project_service.apis).
	APIs []string
}

// Permission is one IAM permission the deployer needs.
type Permission struct {
	Name  string
	Level Level
	// Unverified is non-empty when the need for this permission could not be
	// confirmed in Google's documentation; it says why.
	Unverified string
}

// Requirement lists what one Terraform resource type needs within a feature.
type Requirement struct {
	// Resource is the Terraform resource or data source type, e.g.
	// "google_container_cluster" or "data.google_project".
	Resource    string
	Feature     FeatureID
	Permissions []Permission
	// Source is the Google documentation the permissions were read from.
	Source string
	Note   string
}

const refBase = "https://docs.cloud.google.com/iam/docs/roles-permissions/"

// BootstrapAPIs must already be enabled before Terraform can run: Terraform
// enables every other API through them, and the preflight itself calls them.
var BootstrapAPIs = []string{
	"cloudresourcemanager.googleapis.com",
	"serviceusage.googleapis.com",
}

// Features lists every feature. Order is the order output is shown in.
var Features = []Feature{
	{ID: FeatureState, Title: "Terraform state bucket (created by aiplex platform apply)", Toggle: "always", Default: true,
		APIs: []string{"storage.googleapis.com"}},
	{ID: FeatureCore, Title: "Core platform (GKE, AlloyDB, Firestore, networking, certificates, identity pool, monitoring)", Toggle: "always", Default: true,
		APIs: []string{
			"alloydb.googleapis.com",
			"artifactregistry.googleapis.com",
			"certificatemanager.googleapis.com",
			"compute.googleapis.com",
			"container.googleapis.com",
			"firestore.googleapis.com",
			"iam.googleapis.com",
			"mesh.googleapis.com",
			"monitoring.googleapis.com",
			"privateca.googleapis.com",
			"secretmanager.googleapis.com",
			"servicenetworking.googleapis.com",
		}},
	{ID: FeatureDNS, Title: "Cloud DNS zone and records", Toggle: "manage_dns = true (default)", Default: true,
		APIs: []string{"dns.googleapis.com"}},
	{ID: FeatureBackup, Title: "Firestore daily export to Cloud Storage", Toggle: "always", Default: true,
		APIs: []string{"cloudscheduler.googleapis.com", "storage.googleapis.com"}},
	{ID: FeatureBinaryAuth, Title: "Binary Authorization (KMS signer, attestor, policy)", Toggle: "always", Default: true,
		APIs: []string{"binaryauthorization.googleapis.com", "cloudkms.googleapis.com", "containeranalysis.googleapis.com"}},
	{ID: FeatureWorkforce, Title: "Workforce identity federation (organization level)", Toggle: `organization_id != ""`, Default: false,
		APIs: []string{"iam.googleapis.com"}},
	{ID: FeatureAlertEmail, Title: "Alert e-mail notification channel", Toggle: `alert_email != ""`, Default: false,
		APIs: []string{"monitoring.googleapis.com"}},
	{ID: FeatureTelemetry, Title: "OpenTelemetry collector identity (Cloud Trace, Cloud Monitoring)", Toggle: "otel_collector = true (default)", Default: true,
		APIs: []string{"cloudtrace.googleapis.com", "monitoring.googleapis.com"}},
	{ID: FeatureDedicatedNetwork, Title: "GKE cluster on the dedicated VPC (Private Google Access, Cloud Router and NAT)", Toggle: `cluster_network = "aiplex" (default "default"; aiplex init writes "aiplex" for new installs)`, Default: false,
		APIs: []string{"compute.googleapis.com"}},
	{ID: FeatureExternalAgents, Title: "AWS and Azure workload identity providers for external agents", Toggle: `aws_account_id != "" or azure_agent_tenant_id != ""`, Default: false,
		APIs: []string{"iam.googleapis.com"}},
}

func p(names ...string) []Permission {
	out := make([]Permission, len(names))
	for i, n := range names {
		out[i] = Permission{Name: n, Level: LevelProject}
	}
	return out
}

func unverified(why string, names ...string) []Permission {
	out := p(names...)
	for i := range out {
		out[i].Unverified = why
	}
	return out
}

func join(groups ...[]Permission) []Permission {
	var out []Permission
	for _, g := range groups {
		out = append(out, g...)
	}
	return out
}

func crud(prefix string, verbs ...string) []string {
	out := make([]string, len(verbs))
	for i, v := range verbs {
		out[i] = prefix + "." + v
	}
	return out
}

const (
	whyClusterSA     = "GKE docs require Service Account User on a user-managed node service account; whether Autopilot with the default Compute Engine service account needs actAs is not stated"
	whyNetworkPeer   = "VPC private services access docs name roles/compute.networkAdmin, not individual permissions"
	whyFirewall      = "not confirmed in Google docs for firewall rule creation"
	whyHelm          = "depends on the objects the Ory Hydra and Kratos charts render; not enumerated against the chart. The AIPlex chart's Gateway API objects (Gateway, HTTPRoute, ReferenceGrant, GCPBackendPolicy, HealthCheckPolicy) are custom resources, assumed to be authorized as container.thirdPartyObjects.*; Google's reference does not name Gateway kinds"
	whyAttestor      = "attestor creation references a Container Analysis note; the permission checked on the note is not stated"
	whyFirestoreTTL  = "TTL is configured through the fields API; the role reference names index (schema) permissions, the exact check for fields.patch is not stated"
	whyStateObject   = "Terraform's GCS backend reads, writes, lists and deletes state and lock objects; not stated as a permission list in Google docs"
	whyClusterSubnet = "GKE docs name Compute Network User on the host project for Shared VPC; whether creating a cluster on a same-project custom subnetwork checks compute.subnetworks.use is not stated"
	whyKubernetes    = "GKE authorizes Kubernetes API calls through RBAC and container.* IAM permissions; the mapping for these objects was not validated against a live cluster"
)

// Requirements is the table. One entry per (resource type, feature).
var Requirements = []Requirement{
	// ── Terraform state bucket (cmd/aiplex-cli ensureStateBucket and the GCS backend)
	{Resource: "cli:state-bucket", Feature: FeatureState, Source: refBase + "storage",
		Permissions: join(p("storage.buckets.create", "storage.buckets.get", "storage.buckets.update"),
			[]Permission{
				{Name: "storage.objects.create", Level: LevelStateBucket, Unverified: whyStateObject},
				{Name: "storage.objects.delete", Level: LevelStateBucket, Unverified: whyStateObject},
				{Name: "storage.objects.get", Level: LevelStateBucket, Unverified: whyStateObject},
				{Name: "storage.objects.list", Level: LevelStateBucket, Unverified: whyStateObject},
			})},

	// ── Core
	{Resource: "google_project_service", Feature: FeatureCore, Source: refBase + "serviceusage",
		Permissions: p("serviceusage.services.enable", "serviceusage.services.get", "serviceusage.services.list")},
	{Resource: "data.google_project", Feature: FeatureCore, Source: refBase + "resourcemanager",
		Permissions: p("resourcemanager.projects.get")},
	{Resource: "data.google_client_config", Feature: FeatureCore, Note: "reads the caller's own access token; no IAM permission"},
	{Resource: "google_container_cluster", Feature: FeatureCore, Source: refBase + "container",
		Permissions: join(p(crud("container.clusters", "create", "delete", "get", "update")...), p("container.operations.get"),
			unverified(whyClusterSA, "iam.serviceAccounts.actAs"))},
	{Resource: "google_compute_network", Feature: FeatureCore, Source: refBase + "compute",
		Permissions: join(p(crud("compute.networks", "create", "delete", "get")...), p("compute.globalOperations.get"))},
	{Resource: "google_compute_subnetwork", Feature: FeatureCore, Source: refBase + "compute",
		Permissions: join(p(crud("compute.subnetworks", "create", "delete", "get", "update")...), p("compute.regionOperations.get"))},
	{Resource: "google_compute_global_address", Feature: FeatureCore, Source: refBase + "compute",
		Permissions: join(p(crud("compute.globalAddresses", "create", "delete", "get")...), p("compute.globalOperations.get"))},
	{Resource: "google_service_networking_connection", Feature: FeatureCore, Source: refBase + "servicenetworking",
		Permissions: join(p("servicenetworking.services.addPeering", "servicenetworking.services.deleteConnection", "servicenetworking.operations.get"),
			unverified(whyNetworkPeer, "compute.networks.addPeering"))},
	{Resource: "google_compute_firewall", Feature: FeatureCore, Source: refBase + "compute",
		Permissions: join(p(crud("compute.firewalls", "create", "delete", "get", "update")...),
			unverified(whyFirewall, "compute.networks.updatePolicy"))},
	{Resource: "google_compute_security_policy", Feature: FeatureCore, Source: refBase + "compute",
		Permissions: p(crud("compute.securityPolicies", "create", "delete", "get", "update")...)},
	{Resource: "google_alloydb_cluster", Feature: FeatureCore, Source: refBase + "alloydb",
		Permissions: join(p(crud("alloydb.clusters", "create", "delete", "get", "update")...), p("alloydb.operations.get"))},
	{Resource: "google_alloydb_instance", Feature: FeatureCore, Source: refBase + "alloydb",
		Permissions: p(crud("alloydb.instances", "create", "delete", "get", "update")...)},
	{Resource: "null_resource", Feature: FeatureCore, Source: refBase + "alloydb",
		Note:        "local-exec runs `gcloud alloydb databases create` for the hydra and kratos databases",
		Permissions: p("alloydb.databases.create")},
	{Resource: "random_password", Feature: FeatureCore, Note: "generated locally; no IAM permission"},
	{Resource: "google_secret_manager_secret", Feature: FeatureCore, Source: refBase + "secretmanager",
		Permissions: p(crud("secretmanager.secrets", "create", "delete", "get", "update")...)},
	{Resource: "google_secret_manager_secret_version", Feature: FeatureCore, Source: refBase + "secretmanager",
		Permissions: p(crud("secretmanager.versions", "access", "add", "destroy", "get")...)},
	{Resource: "google_firestore_database", Feature: FeatureCore, Source: refBase + "firestore",
		Permissions: join(p(crud("datastore.databases", "create", "delete", "get", "update")...), p("datastore.operations.get"))},
	{Resource: "google_firestore_index", Feature: FeatureCore, Source: refBase + "firestore",
		Note:        "the role reference names index definitions datastore.schemas.*",
		Permissions: p(crud("datastore.schemas", "create", "delete", "get")...)},
	{Resource: "google_firestore_field", Feature: FeatureCore, Source: refBase + "firestore",
		Permissions: unverified(whyFirestoreTTL, "datastore.schemas.get", "datastore.schemas.update")},
	{Resource: "google_compute_router", Feature: FeatureDedicatedNetwork, Source: refBase + "compute",
		Note:        "gke.tf: Cloud Router for the dedicated network's NAT",
		Permissions: join(p(crud("compute.routers", "create", "delete", "get", "update")...), p("compute.regionOperations.get"))},
	{Resource: "google_compute_router_nat", Feature: FeatureDedicatedNetwork, Source: refBase + "compute",
		Note:        "gke.tf: Cloud NAT is configured on the router, so it needs router update; subnetworks.use for the NAT subnetwork is not separately stated",
		Permissions: join(p("compute.routers.get", "compute.routers.update"), p("compute.regionOperations.get"))},
	{Resource: "google_compute_subnetwork", Feature: FeatureDedicatedNetwork, Source: refBase + "compute",
		Note:        "alloydb.tf: private_ip_google_access is turned on for the dedicated network",
		Permissions: p("compute.subnetworks.setPrivateIpGoogleAccess")},
	{Resource: "google_container_cluster", Feature: FeatureDedicatedNetwork, Source: refBase + "compute",
		Note:        "creating a cluster on a custom subnetwork",
		Permissions: unverified(whyClusterSubnet, "compute.subnetworks.use")},
	{Resource: "google_compute_global_address", Feature: FeatureCore, Source: refBase + "compute",
		Note: "load balancer address (gateway.tf)", Permissions: p("compute.globalAddresses.create", "compute.globalAddresses.get")},
	{Resource: "google_certificate_manager_dns_authorization", Feature: FeatureCore, Source: refBase + "certificatemanager",
		Permissions: join(p(crud("certificatemanager.dnsauthorizations", "create", "delete", "get", "update")...), p("certificatemanager.operations.get"))},
	{Resource: "google_certificate_manager_certificate", Feature: FeatureCore, Source: refBase + "certificatemanager",
		Permissions: join(p(crud("certificatemanager.certs", "create", "delete", "get", "update")...), p("certificatemanager.dnsauthorizations.use"))},
	{Resource: "google_certificate_manager_certificate_map", Feature: FeatureCore, Source: refBase + "certificatemanager",
		Permissions: p(crud("certificatemanager.certmaps", "create", "delete", "get", "update")...)},
	{Resource: "google_certificate_manager_certificate_map_entry", Feature: FeatureCore, Source: refBase + "certificatemanager",
		Permissions: join(p(crud("certificatemanager.certmapentries", "create", "delete", "get", "update")...), p("certificatemanager.certs.use"))},
	{Resource: "google_dns_record_set", Feature: FeatureDNS, Source: refBase + "dns",
		Note:        "certificate validation records and A records live in the Terraform-managed zone",
		Permissions: join(p(crud("dns.resourceRecordSets", "create", "delete", "get", "update")...), p("dns.changes.create", "dns.changes.get", "dns.managedZones.get"))},
	{Resource: "google_artifact_registry_repository", Feature: FeatureCore, Source: refBase + "artifactregistry",
		Permissions: p(crud("artifactregistry.repositories", "create", "delete", "get", "update")...)},
	{Resource: "google_monitoring_alert_policy", Feature: FeatureCore, Source: refBase + "monitoring",
		Permissions: p(crud("monitoring.alertPolicies", "create", "delete", "get", "update")...)},
	{Resource: "google_iam_workload_identity_pool", Feature: FeatureCore, Source: refBase + "iam",
		Permissions: p(crud("iam.googleapis.com/workloadIdentityPools", "create", "delete", "get", "update")...)},
	{Resource: "google_iam_workload_identity_pool_provider", Feature: FeatureExternalAgents, Source: refBase + "iam",
		Permissions: p(crud("iam.googleapis.com/workloadIdentityPoolProviders", "create", "delete", "get", "update")...)},
	{Resource: "google_service_account", Feature: FeatureCore, Source: refBase + "iam",
		Permissions: p(crud("iam.serviceAccounts", "create", "delete", "get", "update")...)},
	{Resource: "google_service_account_iam_member", Feature: FeatureCore, Source: refBase + "iam",
		Permissions: p("iam.serviceAccounts.getIamPolicy", "iam.serviceAccounts.setIamPolicy")},
	{Resource: "google_project_iam_member", Feature: FeatureCore, Source: refBase + "resourcemanager",
		Note:        "project-level bindings for the runtime service accounts",
		Permissions: p("resourcemanager.projects.getIamPolicy", "resourcemanager.projects.setIamPolicy")},
	{Resource: "google_project_iam_custom_role", Feature: FeatureBackup, Source: refBase + "iam",
		Note:        "aiplexFirestoreExporter: the export service account's narrowed role",
		Permissions: p(crud("iam.roles", "create", "delete", "get", "update")...)},
	{Resource: "kubernetes_deployment", Feature: FeatureCore, Source: refBase + "container",
		Permissions: join(p("container.clusters.get"), unverified(whyKubernetes, crud("container.deployments", "create", "delete", "get", "update")...))},
	{Resource: "kubernetes_namespace", Feature: FeatureCore, Source: refBase + "container",
		Permissions: unverified(whyKubernetes, crud("container.namespaces", "create", "delete", "get", "update")...)},
	{Resource: "kubernetes_service_account", Feature: FeatureCore, Source: refBase + "container",
		Permissions: unverified(whyKubernetes, crud("container.serviceAccounts", "create", "delete", "get", "update")...)},
	{Resource: "google_project_iam_custom_role", Feature: FeatureCore, Source: refBase + "iam",
		Note:        "aiplexApiSecrets: the API's secret write and metadata role",
		Permissions: p(crud("iam.roles", "create", "delete", "get", "update")...)},
	{Resource: "kubernetes_service", Feature: FeatureCore, Source: refBase + "container",
		Permissions: unverified(whyKubernetes, crud("container.services", "create", "delete", "get", "update")...)},
	{Resource: "helm_release", Feature: FeatureCore, Source: refBase + "container",
		Permissions: unverified(whyHelm,
			"container.configMaps.create", "container.configMaps.delete", "container.configMaps.get", "container.configMaps.update",
			"container.deployments.create", "container.deployments.delete", "container.deployments.get", "container.deployments.update",
			"container.jobs.create", "container.jobs.delete", "container.jobs.get", "container.jobs.update",
			"container.secrets.create", "container.secrets.delete", "container.secrets.get", "container.secrets.update",
			"container.serviceAccounts.create", "container.serviceAccounts.delete", "container.serviceAccounts.get", "container.serviceAccounts.update",
			"container.services.create", "container.services.delete", "container.services.get", "container.services.update",
			"container.thirdPartyObjects.create", "container.thirdPartyObjects.delete", "container.thirdPartyObjects.get", "container.thirdPartyObjects.update")},

	// ── DNS (manage_dns)
	{Resource: "google_dns_managed_zone", Feature: FeatureDNS, Source: refBase + "dns",
		Permissions: p(crud("dns.managedZones", "create", "delete", "get", "update")...)},

	// ── Backup
	{Resource: "google_storage_bucket", Feature: FeatureBackup, Source: refBase + "storage",
		Permissions: p(crud("storage.buckets", "create", "delete", "get", "update")...)},
	{Resource: "google_storage_bucket_iam_member", Feature: FeatureBackup, Source: refBase + "storage",
		Permissions: p("storage.buckets.getIamPolicy", "storage.buckets.setIamPolicy")},
	{Resource: "google_service_account", Feature: FeatureBackup, Source: refBase + "iam",
		Permissions: p(crud("iam.serviceAccounts", "create", "delete", "get")...)},
	{Resource: "google_project_iam_member", Feature: FeatureBackup, Source: refBase + "resourcemanager",
		Permissions: p("resourcemanager.projects.getIamPolicy", "resourcemanager.projects.setIamPolicy")},
	{Resource: "google_cloud_scheduler_job", Feature: FeatureBackup, Source: "https://docs.cloud.google.com/scheduler/docs/http-target-auth",
		Note:        "attaching the export service account to the job's OAuth token needs actAs on it",
		Permissions: join(p(crud("cloudscheduler.jobs", "create", "delete", "get", "update")...), p("iam.serviceAccounts.actAs"))},

	// ── Binary Authorization
	{Resource: "google_kms_key_ring", Feature: FeatureBinaryAuth, Source: refBase + "cloudkms",
		Permissions: p("cloudkms.keyRings.create", "cloudkms.keyRings.get")},
	{Resource: "google_kms_crypto_key", Feature: FeatureBinaryAuth, Source: refBase + "cloudkms",
		Permissions: p(crud("cloudkms.cryptoKeys", "create", "get", "update")...)},
	{Resource: "data.google_kms_crypto_key_version", Feature: FeatureBinaryAuth, Source: refBase + "cloudkms",
		Permissions: p("cloudkms.cryptoKeyVersions.get", "cloudkms.cryptoKeyVersions.viewPublicKey")},
	{Resource: "google_kms_crypto_key_iam_member", Feature: FeatureBinaryAuth, Source: refBase + "cloudkms",
		Permissions: p("cloudkms.cryptoKeys.getIamPolicy", "cloudkms.cryptoKeys.setIamPolicy")},
	{Resource: "google_container_analysis_note", Feature: FeatureBinaryAuth, Source: refBase + "containeranalysis",
		Permissions: p(crud("containeranalysis.notes", "create", "delete", "get", "update")...)},
	{Resource: "google_container_analysis_note_iam_member", Feature: FeatureBinaryAuth, Source: refBase + "containeranalysis",
		Permissions: p("containeranalysis.notes.getIamPolicy", "containeranalysis.notes.setIamPolicy")},
	{Resource: "google_binary_authorization_attestor", Feature: FeatureBinaryAuth, Source: refBase + "binaryauthorization",
		Permissions: join(p(crud("binaryauthorization.attestors", "create", "delete", "get", "update")...),
			unverified(whyAttestor, "containeranalysis.notes.attachOccurrence"))},
	{Resource: "google_binary_authorization_attestor_iam_member", Feature: FeatureBinaryAuth, Source: refBase + "binaryauthorization",
		Permissions: p("binaryauthorization.attestors.getIamPolicy", "binaryauthorization.attestors.setIamPolicy")},
	{Resource: "google_binary_authorization_policy", Feature: FeatureBinaryAuth, Source: refBase + "binaryauthorization",
		Permissions: p("binaryauthorization.policy.get", "binaryauthorization.policy.update")},

	// ── Workforce identity (organization level)
	{Resource: "google_iam_workforce_pool", Feature: FeatureWorkforce, Source: refBase + "iam",
		Permissions: orgLevel(crud("iam.googleapis.com/workforcePools", "create", "delete", "get", "update")...)},
	{Resource: "google_iam_workforce_pool_provider", Feature: FeatureWorkforce, Source: refBase + "iam",
		Permissions: orgLevel(crud("iam.googleapis.com/workforcePoolProviders", "create", "delete", "get", "update")...)},
	{Resource: "google_project_iam_member", Feature: FeatureWorkforce, Source: refBase + "resourcemanager",
		Note:        "IAP access for workforce groups",
		Permissions: p("resourcemanager.projects.getIamPolicy", "resourcemanager.projects.setIamPolicy")},
	{Resource: "google_project_iam_custom_role", Feature: FeatureWorkforce, Source: refBase + "iam",
		Permissions: p(crud("iam.roles", "create", "delete", "get", "update")...)},

	// ── Telemetry (otel_collector)
	{Resource: "google_service_account", Feature: FeatureTelemetry, Source: refBase + "iam",
		Permissions: p(crud("iam.serviceAccounts", "create", "delete", "get", "update")...)},
	{Resource: "google_service_account_iam_member", Feature: FeatureTelemetry, Source: refBase + "iam",
		Permissions: p("iam.serviceAccounts.getIamPolicy", "iam.serviceAccounts.setIamPolicy")},
	{Resource: "google_project_iam_member", Feature: FeatureTelemetry, Source: refBase + "resourcemanager",
		Note:        "roles/cloudtrace.agent and roles/monitoring.metricWriter for the collector",
		Permissions: p("resourcemanager.projects.getIamPolicy", "resourcemanager.projects.setIamPolicy")},

	// ── Alert e-mail
	{Resource: "google_monitoring_notification_channel", Feature: FeatureAlertEmail, Source: refBase + "monitoring",
		Permissions: p(crud("monitoring.notificationChannels", "create", "delete", "get", "update")...)},
}

func orgLevel(names ...string) []Permission {
	out := p(names...)
	for i := range out {
		out[i].Level = LevelOrganization
	}
	return out
}

// FeatureByID returns the feature with id.
func FeatureByID(id FeatureID) (Feature, bool) {
	for _, f := range Features {
		if f.ID == id {
			return f, true
		}
	}
	return Feature{}, false
}

// FeaturePermissions returns the de-duplicated permissions a feature needs,
// sorted by level then name. When the same permission appears both verified
// and unverified, it is reported verified.
func FeaturePermissions(id FeatureID) []Permission {
	seen := map[string]int{}
	var out []Permission
	for _, r := range Requirements {
		if r.Feature != id {
			continue
		}
		for _, perm := range r.Permissions {
			key := string(perm.Level) + "\x00" + perm.Name
			if i, ok := seen[key]; ok {
				if perm.Unverified == "" {
					out[i].Unverified = ""
				}
				continue
			}
			seen[key] = len(out)
			out = append(out, perm)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Level != out[j].Level {
			return out[i].Level < out[j].Level
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// TerraformAPIs returns every API google_project_service.apis must enable:
// the union of all features' APIs, sorted. Bootstrap APIs are not included;
// they must be on before Terraform runs.
func TerraformAPIs() []string {
	set := map[string]bool{}
	for _, f := range Features {
		for _, a := range f.APIs {
			set[a] = true
		}
	}
	return sortedKeys(set)
}

// AllAPIs returns bootstrap and Terraform-managed APIs, sorted.
func AllAPIs() []string {
	set := map[string]bool{}
	for _, a := range BootstrapAPIs {
		set[a] = true
	}
	for _, a := range TerraformAPIs() {
		set[a] = true
	}
	return sortedKeys(set)
}

// DeployerRolePermissions returns the permissions of the optional custom
// deployer role: every project- and state-bucket-level permission of every
// feature. Organization-level permissions cannot be in a project custom role.
func DeployerRolePermissions() []string {
	set := map[string]bool{}
	for _, f := range Features {
		for _, perm := range FeaturePermissions(f.ID) {
			if perm.Level != LevelOrganization {
				set[perm.Name] = true
			}
		}
	}
	return sortedKeys(set)
}

func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ShortAPI trims ".googleapis.com".
func ShortAPI(api string) string { return strings.TrimSuffix(api, ".googleapis.com") }
