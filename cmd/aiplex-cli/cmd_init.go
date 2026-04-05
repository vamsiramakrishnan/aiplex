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
	"us-central1", "us-east1", "us-west1",
	"europe-west1", "europe-west4",
	"asia-southeast1", "asia-northeast1",
}

func initCmd() *cobra.Command {
	var (
		project string
		region  string
		domain  string
		dryRun  bool
	)

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Set up AIPlex on a GCP project",
		Long: `Interactive setup that validates prerequisites, generates Terraform
variables, and configures the CLI context.

What it does:
  1. Validates GCP prerequisites (project, APIs, permissions)
  2. Generates terraform.tfvars for your environment
  3. Creates a CLI context pointing to your instance
  4. Provides next steps for deployment

Examples:
  aiplex init                                          # interactive
  aiplex init --project my-project --region us-central1  # non-interactive
  aiplex init --dry-run                                # validate only`,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println("AIPlex Setup")
			fmt.Println("════════════════════════════════════════")
			fmt.Println()

			reader := bufio.NewReader(os.Stdin)

			// Step 1: Validate gcloud
			fmt.Println("[1/5] Checking prerequisites...")
			checks := runPreflightChecks()
			allPassed := true
			for _, c := range checks {
				if c.passed {
					fmt.Printf("  [pass] %s\n", c.name)
				} else {
					fmt.Printf("  [FAIL] %s — %s\n", c.name, c.fix)
					allPassed = true // don't block, just warn
				}
			}
			fmt.Println()

			// Step 2: Collect project info
			fmt.Println("[2/5] GCP Project Configuration")
			if project == "" {
				project = detectGCPProject()
				project = prompt(reader, "  GCP Project ID", project)
			}
			if region == "" {
				region = prompt(reader, "  GCP Region", "us-central1")
				fmt.Printf("  Available: %s\n", strings.Join(gcpRegions, ", "))
			}
			if domain == "" {
				defaultDomain := fmt.Sprintf("aiplex.%s.example.com", project)
				domain = prompt(reader, "  Domain (for HTTPS)", defaultDomain)
			}

			adminEmail := prompt(reader, "  Admin email", "")
			fmt.Println()

			// Step 3: Validate GCP project
			fmt.Println("[3/5] Validating GCP project...")
			if err := validateGCPProject(project); err != nil {
				fmt.Printf("  [WARN] %v\n", err)
				fmt.Println("  Continuing anyway — Terraform will enable required APIs.")
			} else {
				fmt.Println("  [pass] Project accessible")
			}
			fmt.Println()

			// Step 4: Generate terraform.tfvars
			fmt.Println("[4/5] Generating configuration...")
			tfvarsPath := filepath.Join("deploy", "terraform", "terraform.tfvars")
			if dryRun {
				fmt.Println("  [dry-run] Would generate:")
				fmt.Printf("    %s\n", tfvarsPath)
				content := renderTFVars(project, region, domain, adminEmail)
				fmt.Println()
				fmt.Println(content)
			} else {
				content := renderTFVars(project, region, domain, adminEmail)
				if err := os.WriteFile(tfvarsPath, []byte(content), 0644); err != nil {
					// Try current directory as fallback
					tfvarsPath = "terraform.tfvars"
					if err := os.WriteFile(tfvarsPath, []byte(content), 0644); err != nil {
						return fmt.Errorf("write tfvars: %w", err)
					}
				}
				fmt.Printf("  Generated %s\n", tfvarsPath)
			}
			fmt.Println()

			// Step 5: Configure CLI context
			fmt.Println("[5/5] Configuring CLI...")
			apiURL := fmt.Sprintf("https://%s", domain)
			cfg, err := cliconfig.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			ctxName := sanitizeContextName(project)
			cfg.SetContext(ctxName, apiURL, project, region, domain)
			if !dryRun {
				if err := cfg.Save(); err != nil {
					return fmt.Errorf("save config: %w", err)
				}
				fmt.Printf("  Created context %q → %s\n", ctxName, apiURL)
			} else {
				fmt.Printf("  [dry-run] Would create context %q → %s\n", ctxName, apiURL)
			}
			fmt.Println()

			if !allPassed {
				fmt.Println("Some preflight checks failed. Fix them before deploying.")
				fmt.Println()
			}

			// Next steps
			fmt.Println("Setup complete. Next steps:")
			fmt.Println()
			fmt.Println("  1. Deploy infrastructure:")
			fmt.Println("     cd deploy/terraform")
			fmt.Println("     terraform init")
			fmt.Println("     terraform plan")
			fmt.Println("     terraform apply")
			fmt.Println()
			fmt.Println("  2. Deploy AIPlex:")
			fmt.Println("     helm install aiplex deploy/helm/aiplex \\")
			fmt.Printf("       --namespace aiplex-system \\")
			fmt.Println()
			fmt.Printf("       --values deploy/helm/aiplex/values.yaml\n")
			fmt.Println()
			fmt.Println("  3. Login and verify:")
			fmt.Println("     aiplex login")
			fmt.Println("     aiplex health")
			fmt.Println("     aiplex status")
			fmt.Println()
			fmt.Println("  Or use make:")
			fmt.Println("     make infra        # terraform apply")
			fmt.Println("     make deploy        # helm install")
			fmt.Println("     make verify        # health + status")

			return nil
		},
	}

	cmd.Flags().StringVar(&project, "project", "", "GCP project ID")
	cmd.Flags().StringVar(&region, "region", "", "GCP region")
	cmd.Flags().StringVar(&domain, "domain", "", "Custom domain for HTTPS")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Validate and show plan without writing files")
	return cmd
}

type preflightCheck struct {
	name   string
	passed bool
	fix    string
}

func runPreflightChecks() []preflightCheck {
	checks := []preflightCheck{
		checkBinary("gcloud", "Install: https://cloud.google.com/sdk/docs/install"),
		checkBinary("terraform", "Install: https://developer.hashicorp.com/terraform/install"),
		checkBinary("helm", "Install: https://helm.sh/docs/intro/install/"),
		checkBinary("kubectl", "Install: https://kubernetes.io/docs/tasks/tools/"),
	}
	return checks
}

func checkBinary(name, fix string) preflightCheck {
	_, err := exec.LookPath(name)
	return preflightCheck{
		name:   name + " installed",
		passed: err == nil,
		fix:    fix,
	}
}

func detectGCPProject() string {
	out, err := exec.Command("gcloud", "config", "get-value", "project", "--quiet").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
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

const tfvarsTmpl = `# Generated by: aiplex init
# Modify as needed, then run: terraform apply

project_id = "{{.Project}}"
region     = "{{.Region}}"
domain     = "{{.Domain}}"

# Admin
admin_email = "{{.AdminEmail}}"

# AlloyDB
alloydb_cpu_count = 2       # 2 for dev, 8+ for prod

# GKE Autopilot — no node sizing needed

# Artifact Registry
registry_name = "aiplex"

# Ory (deployed via Helm, configured in deploy/ory/)
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

func sanitizeContextName(s string) string {
	s = strings.ReplaceAll(s, " ", "-")
	s = strings.ToLower(s)
	return s
}
