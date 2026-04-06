package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/spf13/cobra"

	"github.com/vamsiramakrishnan/aiplex/internal/cliconfig"
)

var gcpRegions = []string{
	"us-central1", "us-east1", "us-east4", "us-west1", "us-west2",
	"europe-west1", "europe-west2", "europe-west4", "europe-north1",
	"asia-southeast1", "asia-northeast1", "asia-east1",
	"australia-southeast1", "southamerica-east1",
}

func initCmd() *cobra.Command {
	var (
		project string
		region  string
		domain  string
		dryRun  bool
		force   bool
	)

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Set up AIPlex on a GCP project",
		Long: `Interactive setup that detects your GCP environment, validates
prerequisites, generates Terraform variables, and configures the CLI.

What it does:
  1. Detects gcloud auth and active account
  2. Validates GCP project, billing, and required APIs
  3. Validates prerequisites (terraform, helm, kubectl)
  4. Generates terraform.tfvars for your environment
  5. Configures CLI context and credentials
  6. Tells you the exact next command to run

Examples:
  aiplex init                                            # interactive
  aiplex init --project my-project --region us-central1  # non-interactive
  aiplex init --dry-run                                  # validate only`,
		RunE: func(cmd *cobra.Command, args []string) error {
			reader := bufio.NewReader(os.Stdin)

			fmt.Println()
			fmt.Println("  ╔══════════════════════════════════════╗")
			fmt.Println("  ║         AIPlex Setup Wizard          ║")
			fmt.Println("  ╚══════════════════════════════════════╝")
			fmt.Println()

			// ── Step 0: Ensure tools are installed ───────────────

			fmt.Println("[0/6] Checking developer tools...")
			if err := ensureTools(); err != nil {
				fmt.Printf("  [WARN] %v\n", err)
			}
			fmt.Println()

			// ── Step 1: gcloud auth ──────────────────────────────

			fmt.Println("[1/6] Checking GCP authentication...")
			fmt.Println()

			gcpAccount := detectGCPAccount()
			gcpProject := detectGCPProject()

			if gcpAccount == "" {
				fmt.Println("  [FAIL] Not authenticated with gcloud")
				fmt.Println()
				fmt.Println("  Fix: Run these commands first:")
				fmt.Println("    gcloud auth login")
				fmt.Println("    gcloud auth application-default login")
				fmt.Println()
				fmt.Println("  Then re-run: aiplex init")
				return fmt.Errorf("gcloud not authenticated")
			}

			fmt.Printf("  Account:     %s\n", gcpAccount)
			if gcpProject != "" {
				fmt.Printf("  Project:     %s\n", gcpProject)
			}

			// Check application-default credentials (needed for Terraform)
			hasADC := checkApplicationDefaultCredentials()
			if hasADC {
				fmt.Println("  App Default: configured")
			} else {
				fmt.Println("  App Default: [WARN] not configured")
				fmt.Println("    Terraform needs this. Run: gcloud auth application-default login")
			}
			fmt.Println()

			// ── Step 2: Collect project info ─────────────────────

			fmt.Println("[2/6] Project Configuration")
			fmt.Println()

			// Project
			if project == "" {
				defaultProject := gcpProject
				project = prompt(reader, "  GCP Project ID", defaultProject)
			}
			if project == "" {
				return fmt.Errorf("project ID is required")
			}
			fmt.Println()

			// Region — show options BEFORE asking
			if region == "" {
				fmt.Println("  Available regions:")
				for i, r := range gcpRegions {
					marker := "  "
					if r == "us-central1" {
						marker = "> "
					}
					fmt.Printf("    %s%-25s", marker, r)
					if (i+1)%3 == 0 {
						fmt.Println()
					}
				}
				fmt.Println()
				region = prompt(reader, "  Region", "us-central1")
			}

			// Domain
			if domain == "" {
				defaultDomain := fmt.Sprintf("aiplex.%s.nip.io", project)
				fmt.Println()
				fmt.Println("  Domain options:")
				fmt.Println("    - Your own domain (recommended for production)")
				fmt.Printf("    - %s (auto-resolves, good for testing)\n", defaultDomain)
				fmt.Println()
				domain = prompt(reader, "  Domain", defaultDomain)
			}

			// Validate domain
			if ok, warning := validateDomain(domain); !ok {
				fmt.Printf("  [WARN] %s\n", warning)
				fmt.Println("  Continuing with this domain — you can update later with:")
				fmt.Println("    aiplex config set-context --domain <your-domain>")
				fmt.Println()
			}

			// Admin email — auto-detect from gcloud account
			defaultEmail := gcpAccount
			adminEmail := prompt(reader, "  Admin email", defaultEmail)
			fmt.Println()

			// ── Step 3: Validate GCP project ─────────────────────

			fmt.Println("[3/6] Validating GCP project...")

			// Project exists and accessible
			if err := validateGCPProject(project); err != nil {
				fmt.Printf("  [FAIL] %v\n", err)
				return fmt.Errorf("cannot access project %q — is gcloud authenticated?", project)
			}
			fmt.Println("  [pass] Project accessible")

			// Billing enabled
			billingOK := checkBillingEnabled(project)
			if billingOK {
				fmt.Println("  [pass] Billing enabled")
			} else {
				fmt.Println("  [WARN] Cannot verify billing — Terraform requires an active billing account")
				fmt.Println("    Check: https://console.cloud.google.com/billing/projects")
			}

			// Required APIs
			requiredAPIs := []string{
				"container.googleapis.com",
				"alloydb.googleapis.com",
				"firestore.googleapis.com",
				"secretmanager.googleapis.com",
				"certificatemanager.googleapis.com",
				"artifactregistry.googleapis.com",
				"iam.googleapis.com",
				"dns.googleapis.com",
				"mesh.googleapis.com",
			}
			enabledAPIs := checkAPIs(project, requiredAPIs)
			disabledAPIs := []string{}
			for _, api := range requiredAPIs {
				if enabledAPIs[api] {
					shortName := strings.TrimSuffix(api, ".googleapis.com")
					fmt.Printf("  [pass] %s\n", shortName)
				} else {
					disabledAPIs = append(disabledAPIs, api)
				}
			}
			if len(disabledAPIs) > 0 {
				fmt.Printf("  [info] %d API(s) will be auto-enabled by Terraform:\n", len(disabledAPIs))
				for _, api := range disabledAPIs {
					shortName := strings.TrimSuffix(api, ".googleapis.com")
					fmt.Printf("         - %s\n", shortName)
				}
			}

			// IAM permissions check
			role := checkIAMRole(project, gcpAccount)
			if role != "" {
				fmt.Printf("  [pass] IAM role: %s\n", role)
			} else {
				fmt.Println("  [WARN] Cannot verify IAM role — you need Owner or Editor on the project")
			}
			fmt.Println()

			// ── Step 4: Verify tools ─────────────────────────────

			fmt.Println("[4/6] Verifying tools...")
			toolChecks := []preflightCheck{
				checkBinaryVersion("gcloud", "--version", "Google Cloud SDK"),
				checkBinaryVersion("terraform", "version", "Terraform"),
				checkBinaryVersion("helm", "version --short", "Helm"),
				checkBinaryVersion("kubectl", "version --client --short", "kubectl"),
			}
			for _, c := range toolChecks {
				if c.passed {
					fmt.Printf("  [pass] %s\n", c.name)
				} else {
					fmt.Printf("  [FAIL] %s — %s\n", c.name, c.fix)
				}
			}
			fmt.Println()

			// ── Step 5: Check for existing config ────────────────

			cfg, err := cliconfig.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			ctxName := sanitizeContextName(project)
			if existing, ok := cfg.Contexts[ctxName]; ok && !force && !dryRun {
				fmt.Printf("[5/6] Context %q already exists:\n", ctxName)
				fmt.Printf("  URL:     %s\n", existing.URL)
				fmt.Printf("  Project: %s\n", existing.Project)
				fmt.Printf("  Region:  %s\n", existing.Region)
				fmt.Println()
				overwrite := prompt(reader, "  Overwrite? (y/N)", "N")
				if strings.ToLower(overwrite) != "y" {
					fmt.Println("  Keeping existing config. Use --force to overwrite.")
					fmt.Println()
					goto nextsteps
				}
			}

			// Generate terraform.tfvars
			fmt.Println("[5/6] Generating configuration...")
			{
				tfvarsPath := filepath.Join("deploy", "terraform", "terraform.tfvars")
				content := renderTFVars(project, region, domain, adminEmail)

				if dryRun {
					fmt.Println("  [dry-run] Would generate:")
					fmt.Printf("    %s\n", tfvarsPath)
					fmt.Println()
					fmt.Println(content)
				} else {
					if err := os.MkdirAll(filepath.Dir(tfvarsPath), 0755); err == nil {
						os.WriteFile(tfvarsPath, []byte(content), 0644)
						fmt.Printf("  Generated %s\n", tfvarsPath)
					}

					// Also generate .env for local dev
					envContent := renderDotEnv(project)
					if _, err := os.Stat(".env"); os.IsNotExist(err) {
						os.WriteFile(".env", []byte(envContent), 0644)
						fmt.Println("  Generated .env (local development)")
					}
				}
			}

			// Configure CLI context
			fmt.Println("[6/6] Configuring CLI...")
			{
				apiURL := fmt.Sprintf("https://%s", domain)
				cfg.SetContext(ctxName, apiURL, project, region, domain)
				if !dryRun {
					if err := cfg.Save(); err != nil {
						return fmt.Errorf("save config: %w", err)
					}
					dir, _ := cliconfig.Dir()
					fmt.Printf("  Context:  %q → %s\n", ctxName, apiURL)
					fmt.Printf("  Config:   %s/config.json\n", dir)
				} else {
					fmt.Printf("  [dry-run] Would create context %q\n", ctxName)
				}
			}
			fmt.Println()

		nextsteps:
			// ── Summary & Next Steps ─────────────────────────────

			fmt.Println("  ┌──────────────────────────────────────┐")
			fmt.Println("  │          Setup Complete               │")
			fmt.Println("  └──────────────────────────────────────┘")
			fmt.Println()
			fmt.Printf("  Project:  %s\n", project)
			fmt.Printf("  Region:   %s\n", region)
			fmt.Printf("  Domain:   %s\n", domain)
			fmt.Printf("  Account:  %s\n", gcpAccount)
			fmt.Println()
			fmt.Println("  Next: deploy everything with a single command:")
			fmt.Println()
			fmt.Println("    aiplex platform apply")
			fmt.Println()
			fmt.Println("  This will:")
			fmt.Println("    1. Create Terraform state bucket")
			fmt.Println("    2. Provision GKE, AlloyDB, Firestore, certs, DNS")
			fmt.Println("    3. Configure kubectl")
			fmt.Println("    4. Deploy AIPlex via Helm")
			fmt.Println("    5. Wait for SSL certificate provisioning")
			fmt.Println("    6. Run health check")
			fmt.Println()
			fmt.Println("  After deploy:")
			fmt.Println("    aiplex login                         # authenticate")
			fmt.Println("    aiplex apply -f examples/quickstart.json  # deploy first workload")
			fmt.Println("    aiplex status                        # see it running")
			fmt.Println()
			fmt.Println("  If anything goes wrong:")
			fmt.Println("    aiplex doctor                        # full diagnostic")

			return nil
		},
	}

	cmd.Flags().StringVar(&project, "project", "", "GCP project ID")
	cmd.Flags().StringVar(&region, "region", "", "GCP region")
	cmd.Flags().StringVar(&domain, "domain", "", "Custom domain for HTTPS")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Validate and show plan without writing files")
	cmd.Flags().BoolVar(&force, "force", false, "Overwrite existing context without asking")
	return cmd
}

// ─── GCP Detection Helpers ──────────────────────────────────

func detectGCPAccount() string {
	out, err := exec.Command("gcloud", "config", "get-value", "account", "--quiet").Output()
	if err != nil {
		return ""
	}
	account := strings.TrimSpace(string(out))
	if account == "(unset)" || account == "" {
		return ""
	}
	return account
}

func detectGCPProject() string {
	out, err := exec.Command("gcloud", "config", "get-value", "project", "--quiet").Output()
	if err != nil {
		return ""
	}
	project := strings.TrimSpace(string(out))
	if project == "(unset)" || project == "" {
		return ""
	}
	return project
}

func checkApplicationDefaultCredentials() bool {
	err := exec.Command("gcloud", "auth", "application-default", "print-access-token", "--quiet").Run()
	return err == nil
}

func validateGCPProject(project string) error {
	cmd := exec.Command("gcloud", "projects", "describe", project, "--quiet", "--format=value(projectId)")
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("cannot access project %q — check gcloud auth and permissions", project)
	}
	if strings.TrimSpace(string(out)) != project {
		return fmt.Errorf("unexpected project response")
	}
	return nil
}

func checkBillingEnabled(project string) bool {
	out, err := exec.Command("gcloud", "billing", "projects", "describe", project,
		"--format=value(billingEnabled)", "--quiet").Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "True"
}

func checkAPIs(project string, apis []string) map[string]bool {
	result := make(map[string]bool)
	out, err := exec.Command("gcloud", "services", "list",
		"--project", project, "--enabled", "--format=value(name)", "--quiet").Output()
	if err != nil {
		return result
	}
	enabled := strings.Split(strings.TrimSpace(string(out)), "\n")
	enabledSet := make(map[string]bool)
	for _, svc := range enabled {
		enabledSet[strings.TrimSpace(svc)] = true
	}
	for _, api := range apis {
		result[api] = enabledSet[api]
	}
	return result
}

func checkIAMRole(project, account string) string {
	out, err := exec.Command("gcloud", "projects", "get-iam-policy", project,
		"--flatten=bindings[].members",
		"--filter=bindings.members:"+account,
		"--format=value(bindings.role)",
		"--quiet").Output()
	if err != nil {
		return ""
	}
	roles := strings.TrimSpace(string(out))
	if strings.Contains(roles, "roles/owner") {
		return "Owner"
	}
	if strings.Contains(roles, "roles/editor") {
		return "Editor"
	}
	if roles != "" {
		// Return first role found
		lines := strings.Split(roles, "\n")
		return lines[0]
	}
	return ""
}

// ─── Preflight Checks ──────────────────────────────────────

type preflightCheck struct {
	name   string
	passed bool
	fix    string
}

func checkBinaryVersion(name, versionFlag, displayName string) preflightCheck {
	path, err := exec.LookPath(name)
	if err != nil {
		return preflightCheck{
			name:   displayName + " — not found",
			passed: false,
			fix:    fmt.Sprintf("Install %s", displayName),
		}
	}

	// Get version
	versionArgs := strings.Fields(versionFlag)
	out, err := exec.Command(path, versionArgs...).Output()
	if err != nil {
		return preflightCheck{
			name:   displayName + " — installed",
			passed: true,
		}
	}
	version := strings.TrimSpace(strings.Split(string(out), "\n")[0])
	if len(version) > 60 {
		version = version[:60]
	}
	return preflightCheck{
		name:   fmt.Sprintf("%s (%s)", displayName, version),
		passed: true,
	}
}

// ─── Prompt Helper ──────────────────────────────────────────

func prompt(reader *bufio.Reader, label, defaultVal string) string {
	if defaultVal != "" {
		fmt.Printf("%s [%s]: ", label, defaultVal)
	} else {
		fmt.Printf("%s: ", label)
	}
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)
	if input == "" {
		return defaultVal
	}
	return input
}

// ─── Template Rendering ─────────────────────────────────────

const tfvarsTmpl = `# Generated by: aiplex init
# Modify as needed, then deploy with: aiplex platform apply

project_id = "{{.Project}}"
region     = "{{.Region}}"
domain     = "{{.Domain}}"

# Admin
admin_email = "{{.AdminEmail}}"

# AlloyDB (vCPUs — min 2)
# Dev: 2, Staging: 4, Production: 8+
alloydb_cpu_count = 2

# DNS (set to false if you manage DNS externally)
manage_dns = true

# GKE Autopilot — no node sizing needed
# Artifact Registry
registry_name = "aiplex"

# Ory (auto-configured by Helm)
hydra_admin_url = "http://hydra-admin.aiplex-system.svc.cluster.local:4445"
`

func renderTFVars(project, region, domain, adminEmail string) string {
	t := template.Must(template.New("tfvars").Parse(tfvarsTmpl))
	var buf strings.Builder
	t.Execute(&buf, map[string]string{
		"Project":    project,
		"Region":     region,
		"Domain":     domain,
		"AdminEmail": adminEmail,
	})
	return buf.String()
}

func renderDotEnv(project string) string {
	return fmt.Sprintf(`# Generated by: aiplex init
# Local development environment

AIPLEX_HOST=0.0.0.0
AIPLEX_PORT=8080
LOG_LEVEL=debug
HYDRA_ADMIN_URL=http://localhost:4445
FIRESTORE_EMULATOR_HOST=localhost:8086
FIRESTORE_PROJECT_ID=%s
TRUST_DOMAIN=%s.svc.id.goog
`, project, project)
}

// validateDomain checks if a domain has valid DNS or is a known test domain.
func validateDomain(domain string) (ok bool, warning string) {
	// nip.io and sslip.io are auto-resolving test domains — always valid
	if strings.HasSuffix(domain, ".nip.io") || strings.HasSuffix(domain, ".sslip.io") {
		return true, ""
	}

	// Check DNS resolution
	out, err := exec.Command("dig", "+short", domain).Output()
	if err != nil {
		// dig not available — skip validation
		return true, ""
	}
	resolved := strings.TrimSpace(string(out))
	if resolved == "" {
		return false, fmt.Sprintf("DNS for %q does not resolve yet. You can configure DNS after deploy.", domain)
	}
	return true, ""
}

func sanitizeContextName(s string) string {
	s = strings.ReplaceAll(s, " ", "-")
	s = strings.ToLower(s)
	return s
}

// ensureTools installs mise and project tools if a .mise.toml exists.
func ensureTools() error {
	// Check for .mise.toml in repo root
	miseConfig := findMiseConfig()
	if miseConfig == "" {
		fmt.Println("  No .mise.toml found — skipping tool install")
		return nil
	}

	// Check if mise is installed
	misePath, err := exec.LookPath("mise")
	if err != nil {
		// Try ~/.local/bin
		home, _ := os.UserHomeDir()
		misePath = filepath.Join(home, ".local", "bin", "mise")
		if _, err := os.Stat(misePath); err != nil {
			fmt.Println("  Installing mise (tool version manager)...")
			installCmd := exec.Command("bash", "-c", "curl -fsSL https://mise.run | sh")
			installCmd.Stdout = os.Stdout
			installCmd.Stderr = os.Stderr
			if err := installCmd.Run(); err != nil {
				return fmt.Errorf("failed to install mise: %w", err)
			}
		}
	}

	// Trust the config
	trustCmd := exec.Command(misePath, "trust", miseConfig)
	trustCmd.Run() // ignore error if already trusted

	// Install tools
	fmt.Println("  Installing tools from .mise.toml...")
	installCmd := exec.Command(misePath, "install", "--yes")
	installCmd.Dir = filepath.Dir(miseConfig)
	installCmd.Stdout = os.Stdout
	installCmd.Stderr = os.Stderr
	if err := installCmd.Run(); err != nil {
		return fmt.Errorf("mise install failed: %w", err)
	}

	// Add mise shims to PATH so tools are available for the rest of init
	home, _ := os.UserHomeDir()
	shimsDir := filepath.Join(home, ".local", "share", "mise", "shims")
	os.Setenv("PATH", shimsDir+":"+os.Getenv("PATH"))

	fmt.Println("  [pass] All tools installed")
	return nil
}

func findMiseConfig() string {
	candidates := []string{
		".mise.toml",
		"../.mise.toml",
	}
	for _, c := range candidates {
		abs, err := filepath.Abs(c)
		if err == nil {
			if _, err := os.Stat(abs); err == nil {
				return abs
			}
		}
	}
	return ""
}
