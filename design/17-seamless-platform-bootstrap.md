# 17 — Seamless Platform Bootstrap: No kubectl, No IAM, No Helm

## The Bar

A user with **one thing** — a GCP project with Owner role — runs **one command** and gets a fully operational AIPlex platform. They never:

- Install kubectl
- Install Helm
- Install Terraform
- Configure IAM policies
- Create service accounts
- Set up Cloud SQL
- Touch a YAML file
- Open the GCP Console
- Know what a namespace is

**One binary. One command. One login. Done.**

---

## What the User Does

```bash
# Step 1: Install (one line, like Homebrew or Rust)
curl -fsSL https://get.aiplex.dev | sh

# Step 2: Login to GCP (opens browser)
aiplex login

# Step 3: Setup (one command, 3 questions)
aiplex platform setup
```

That's it. Three commands. The rest is watching a progress bar.

---

## The `aiplex login` Experience

```
$ aiplex login

  Opening browser for Google Cloud login...
  ✓ Logged in as vamsi@example.com

  Available projects:
    1. my-school-prod (us-central1)
    2. my-school-dev (us-east1)
    3. personal-project (us-west1)

? Which project? 1
  ✓ Using my-school-prod
```

**Under the hood:**
- Uses `gcloud auth application-default login` flow (browser-based OAuth)
- If `gcloud` isn't installed, the AIPlex CLI embeds its own OAuth flow (no dependency)
- Stores credentials in `~/.aiplex/credentials.json` (same format as ADC)
- Verifies the user has Owner or Editor role on the project

If the user doesn't have sufficient permissions:
```
✗ You need Owner or Editor role on project "my-school-prod"

  Ask your GCP admin to run:
    gcloud projects add-iam-policy-binding my-school-prod \
      --member="user:vamsi@example.com" \
      --role="roles/owner"

  Or use a project where you have Owner access.
```

---

## The `aiplex platform setup` Experience

```
$ aiplex platform setup

? Region (where to deploy):
  ❯ us-central1 (Iowa — low latency, lowest cost)
    us-east1 (South Carolina)
    europe-west1 (Belgium)
    asia-southeast1 (Singapore)

? Custom domain (optional, press Enter to skip):
  > aiplex.myschool.edu

? Admin email (for login):
  > admin@myschool.edu

  Setting up AIPlex on my-school-prod...

  Phase 1: Infrastructure                    [3-5 min]
    ✓ Enabling required APIs
    ✓ Creating compute cluster
    ✓ Creating database
    ✓ Creating storage
    ✓ Creating secrets vault

  Phase 2: Platform Services                 [2-3 min]
    ✓ Deploying auth services
    ✓ Deploying API server
    ��� Deploying web console
    ✓ Configuring gateway
    ✓ Setting up monitoring

  Phase 3: Security                          [1-2 min]
    ✓ Configuring encryption
    ✓ Setting up access policies
    ✓ Creating admin account

  ✓ AIPlex is ready!  (Total: 6m 23s)

  Console:  https://aiplex.myschool.edu
  Login:    admin@myschool.edu (check email for password)

  Quick start:
    aiplex deploy      # Deploy your first tool
    aiplex connect     # Connect your first agent

  DNS setup:
    Add a CNAME record for aiplex.myschool.edu:
      aiplex.myschool.edu → 34.110.xxx.xxx.bc.googleusercontent.com
```

### What "Enabling required APIs" does (user never sees)

```go
apis := []string{
    "container.googleapis.com",        // GKE
    "sqladmin.googleapis.com",         // Cloud SQL
    "firestore.googleapis.com",        // Firestore
    "secretmanager.googleapis.com",    // Secret Manager
    "certificatemanager.googleapis.com", // TLS certs
    "iam.googleapis.com",              // IAM
    "privateca.googleapis.com",        // Certificate Authority
    "mesh.googleapis.com",             // Service Mesh
}
for _, api := range apis {
    enableAPI(project, api)  // Idempotent
}
```

### What "Creating compute cluster" does (user never sees)

```go
// Terraform embedded in the CLI binary (no Terraform install needed)
// Uses go-terraform or pulumi-go for programmatic IaC

cluster := &gke.Cluster{
    Name:     "aiplex",
    Location: region,
    Autopilot: true,
    // Autopilot means: no node pools, no node config, no SSH
    // Google manages everything — we just deploy pods
}
```

### What "Deploying auth services" does (user never sees)

```go
// Helm charts embedded in the CLI binary (no Helm install needed)
// Uses helm-go SDK for programmatic Helm releases

hydra := helm.Release{
    Chart: "ory/hydra",  // bundled in CLI
    Values: map[string]interface{}{
        "hydra.config.urls.self.issuer": fmt.Sprintf("https://%s/oauth2", domain),
        "hydra.config.urls.consent":     fmt.Sprintf("https://%s/api/v1/consent", domain),
        "hydra.config.urls.login":       fmt.Sprintf("https://%s/login", domain),
        "dsn": cloudSQLConnectionString,
    },
}

kratos := helm.Release{
    Chart: "ory/kratos",  // bundled in CLI
    Values: map[string]interface{}{
        "kratos.config.selfservice.default_browser_return_url": fmt.Sprintf("https://%s/", domain),
        "dsn": cloudSQLConnectionString,
    },
}
```

---

## No External Tool Dependencies

The AIPlex CLI is a single static binary that embeds everything:

| What | How It's Embedded | Why |
|------|------------------|-----|
| Terraform | [go-terraform](https://github.com/hashicorp/terraform-exec) or Pulumi Go SDK | No `terraform` install |
| Helm | [helm-go](https://github.com/helm/helm/pkg) | No `helm` install |
| kubectl | [client-go](https://github.com/kubernetes/client-go) | No `kubectl` install |
| gcloud auth | OAuth2 flow (Go net/http) | No `gcloud` install |
| K8s manifests | Embedded in binary (go:embed) | No YAML files on disk |
| Helm charts | Embedded in binary (go:embed) | No chart repository access |
| OPA policy | Embedded in binary (go:embed) | No external policy files |

```go
import "embed"

//go:embed deploy/terraform/*.tf
var terraformFiles embed.FS

//go:embed deploy/k8s/*.yaml
var k8sManifests embed.FS

//go:embed deploy/ory/*.yaml
var oryConfigs embed.FS

//go:embed policies/*.rego
var opaPolicies embed.FS
```

The user downloads one ~50MB binary. It contains everything. No package managers, no runtime dependencies, no network fetches during setup (except to GCP APIs).

---

## IAM: Created Automatically, Never by the User

The CLI creates all necessary IAM resources using the user's Owner credentials:

```go
func setupIAM(project string) error {
    // Create service accounts (all automated)
    serviceAccounts := []struct {
        name  string
        roles []string
    }{
        {
            name:  "aiplex-api",
            roles: []string{
                "roles/firestore.user",
                "roles/secretmanager.secretAccessor",
            },
        },
        {
            name:  "aiplex-hydra",
            roles: []string{"roles/cloudsql.client"},
        },
        {
            name:  "aiplex-kratos",
            roles: []string{"roles/cloudsql.client"},
        },
    }

    for _, sa := range serviceAccounts {
        createServiceAccount(project, sa.name)
        for _, role := range sa.roles {
            grantRole(project, sa.name, role)
        }
        // Bind KSA → GSA via Workload Identity (automatic)
        bindWorkloadIdentity(project, "aiplex-system", sa.name)
    }

    // Create Workload Identity Pool (for external agents)
    createWIPool(project, "aiplex-prod")

    return nil
}
```

The user's Owner role gives the CLI permission to create all of this. The user never runs `gcloud iam` commands.

---

## State Management: Recoverable, Resumable

### What if setup fails halfway?

```
$ aiplex platform setup

  Phase 1: Infrastructure
    ✓ Enabling required APIs
    ✓ Creating compute cluster
    ✗ Creating database (Cloud SQL quota exceeded)

  Setup paused. Fix the issue and re-run:
    aiplex platform setup --resume

  To check quotas:
    https://console.cloud.google.com/iam-admin/quotas?project=my-school-prod
    Look for "Cloud SQL instances" and request an increase.
```

Re-running `aiplex platform setup` is always safe (idempotent). It skips completed steps and retries failed ones.

### State tracking

```
~/.aiplex/
├── credentials.json     # GCP auth
├── config.yaml          # Project, region, domain
├── platform-state.json  # Which setup steps completed
└── kubeconfig           # Auto-generated, never touched by user
```

The user never opens or edits any of these files. They're internal state.

---

## Upgrades: One Command

```
$ aiplex platform upgrade

  Current version: v1.2.0
  Available: v1.3.0

  Changes:
    • Improved LLM cost tracking
    • New tool templates: Jira, Confluence, Notion
    • Security patches for Ory Hydra

  Upgrading...
    ✓ Updated API server (rolling update, zero downtime)
    ✓ Updated auth services (rolling update, zero downtime)
    ✓ Updated gateway config
    ✓ Database migrations applied
    ✓ Console updated

  ✓ Upgraded to v1.3.0
    Your tools and agents are unaffected.
```

No Helm upgrade commands. No kubectl rollout. No database migration scripts. One command.

---

## Teardown: Clean Exit

```
$ aiplex platform destroy

  ⚠ This will delete ALL AIPlex resources from my-school-prod:
    • 3 deployed tools
    • 2 registered agents
    • All permissions and access rules
    • The compute cluster and database

  This cannot be undone.

? Type "destroy my-school-prod" to confirm:
  > destroy my-school-prod

  Destroying AIPlex...
    ✓ Tools undeployed
    ✓ Routes deleted
    ✓ Auth services removed
    ✓ Database deleted
    ✓ Cluster deleted
    ✓ IAM resources cleaned up

  ✓ AIPlex removed from my-school-prod
    No GCP resources remain. Your project is clean.
```

---

## Architecture of the CLI

```
aiplex CLI (single Go binary, ~50MB)
├── cmd/
│   ├── login.go           # GCP OAuth flow
│   ├── platform_setup.go  # Full platform bootstrap
│   ├── platform_upgrade.go
│   ���── platform_destroy.go
│   ├── deploy.go          # Tool/agent/model deploy
│   ├── connect.go         # Agent connection wizard
│   ├── allow.go           # Permission grants
│   ├── ls.go              # List instances
│   ├── status.go          # Instance health
│   ├── logs.go            # Instance logs
│   └── ...
├── internal/
│   ├── infra/
│   ���   ├── terraform.go   # Programmatic Terraform (go-terraform)
��   │   ├── helm.go        # Programmatic Helm (helm-go)
│   │   ├── k8s.go         # Programmatic kubectl (client-go)
│   │   └── gcp.go         # GCP API client
│   ├─�� auth/
│   │   ├── gcp_login.go   # Browser-based GCP OAuth
│   │   └── credentials.go # Credential storage
│   ├── ui/
│   │   ├── spinner.go     # Progress indicators
│   │   ├── prompt.go      # Interactive questions (bubbletea)
│   │   └── table.go       # Formatted output (lipgloss)
│   └── embedded/          # go:embed assets
│       ├���─ terraform/
│       ├── k8s/
│       ├── ory/
���       └── policies/
└── embed.go               # //go:embed directives
```

### Key libraries

| Library | Purpose |
|---------|---------|
| `github.com/charmbracelet/bubbletea` | Interactive TUI (prompts, spinners) |
| `github.com/charmbracelet/lipgloss` | Terminal styling |
| `github.com/hashicorp/terraform-exec` | Programmatic Terraform |
| `helm.sh/helm/v3/pkg` | Programmatic Helm |
| `k8s.io/client-go` | Programmatic K8s |
| `cloud.google.com/go` | GCP APIs |
| `github.com/spf13/cobra` | CLI framework |

---

## The Promise

| Persona | What They Install | What They Run | What They Never Touch |
|---------|------------------|---------------|----------------------|
| Platform admin | `aiplex` CLI | `aiplex login` + `aiplex platform setup` | kubectl, helm, terraform, gcloud, GCP Console IAM |
| Developer | `aiplex` CLI | `aiplex deploy` + `aiplex connect` | Any infrastructure tool |
| Agent builder | `aiplex` SDK | `pip install aiplex` + 3 lines of code | CLI, YAML, any config |
| End user | Nothing | Uses agent (tools governed by AIPlex transparently) | Everything |

**If a user needs to install anything besides the AIPlex CLI, we failed. If they need to learn any infrastructure concept, we failed. If they need to open the GCP Console for anything beyond billing, we failed.**
