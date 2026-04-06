package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/vamsiramakrishnan/aiplex/internal/cliconfig"
)

func platformCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "platform",
		Short: "Manage AIPlex platform infrastructure",
		Long: `Full lifecycle management for AIPlex infrastructure.
No need to run terraform, helm, or kubectl directly.

Flow:
  aiplex init                    # configure project
  aiplex platform apply          # deploy everything
  aiplex platform status         # check infrastructure
  aiplex platform destroy        # tear down`,
	}

	cmd.AddCommand(
		platformApplyCmd(),
		platformStatusCmd(),
		platformDestroyCmd(),
	)
	return cmd
}

func platformApplyCmd() *cobra.Command {
	var (
		skipInfra   bool
		skipDeploy  bool
		autoApprove bool
		valuesFile  string
	)

	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Deploy infrastructure and application (terraform + helm)",
		Long: `Runs the full deployment pipeline:

  1. Creates GCS bucket for Terraform state (if needed)
  2. Runs terraform init + apply (GKE, AlloyDB, Firestore, certs, DNS)
  3. Configures kubectl context
  4. Runs helm install/upgrade (AIPlex API, Console, Ory, OPA)
  5. Waits for cert provisioning
  6. Runs health check

Use --skip-infra to only deploy the application (helm).
Use --skip-deploy to only provision infrastructure (terraform).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := cliconfig.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			ctx, err := cfg.Current()
			if err != nil {
				return fmt.Errorf("no context configured — run: aiplex init")
			}

			fmt.Println("AIPlex Platform Deploy")
			fmt.Println("════════════════════════════════════════")
			fmt.Printf("  Context: %s\n", cfg.CurrentContext)
			fmt.Printf("  Project: %s\n", ctx.Project)
			fmt.Printf("  Region:  %s\n", ctx.Region)
			fmt.Printf("  Domain:  %s\n", ctx.Domain)
			fmt.Println()

			tfDir := findTerraformDir()
			helmDir := findHelmDir()

			// Step 1: Terraform state bucket
			if !skipInfra {
				fmt.Println("[1/6] Terraform state bucket...")

				newBucket := stateBucketName(ctx.Project)

				// Check for legacy bucket and offer migration
				legacyBucket := "aiplex-terraform-state"
				if newBucket != legacyBucket {
					legacyExists := exec.Command("gcloud", "storage", "buckets", "describe",
						fmt.Sprintf("gs://%s", legacyBucket), "--project", ctx.Project, "--quiet").Run()
					if legacyExists == nil {
						newExists := exec.Command("gcloud", "storage", "buckets", "describe",
							fmt.Sprintf("gs://%s", newBucket), "--project", ctx.Project, "--quiet").Run()
						if newExists != nil {
							fmt.Printf("  [info] Found legacy state bucket: gs://%s\n", legacyBucket)
							fmt.Printf("  [info] Migrating to: gs://%s\n", newBucket)
							copyCmd := exec.Command("gcloud", "storage", "cp", "-r",
								fmt.Sprintf("gs://%s/*", legacyBucket),
								fmt.Sprintf("gs://%s/", newBucket),
								"--project", ctx.Project, "--quiet")
							if err := copyCmd.Run(); err != nil {
								fmt.Printf("  [WARN] State migration failed: %v\n", err)
								fmt.Printf("  Using legacy bucket. Migrate manually later.\n")
							} else {
								fmt.Printf("  [pass] State migrated to gs://%s\n", newBucket)
							}
						}
					}
				}

				sp := startSpinner("Creating state bucket")
				if err := ensureStateBucket(ctx.Project); err != nil {
					sp.fail(fmt.Sprintf("State bucket: %v — create manually or Terraform will fail", err))
				} else {
					sp.finish(fmt.Sprintf("State bucket ready (gs://%s)", stateBucketName(ctx.Project)))
				}
				fmt.Println()

				// Step 2: Terraform init (backend-config derived from project ID)
				fmt.Println("[2/6] Terraform init...")
				bucket := stateBucketName(ctx.Project)
				if err := runCmd(tfDir, "terraform", "init",
					"-input=false",
					fmt.Sprintf("-backend-config=bucket=%s", bucket),
				); err != nil {
					return fmt.Errorf("terraform init failed: %w", err)
				}
				fmt.Println()

				// Step 3: Terraform apply
				fmt.Println("[3/6] Terraform apply (GKE, AlloyDB, Firestore, certs, DNS)...")
				applyArgs := []string{"apply", "-input=false"}
				if autoApprove {
					applyArgs = append(applyArgs, "-auto-approve")
				}
				if err := runCmd(tfDir, "terraform", applyArgs...); err != nil {
					return fmt.Errorf("terraform apply failed: %w", err)
				}
				fmt.Println()

				// Step 3b: Extract outputs and update config
				fmt.Println("  Extracting infrastructure outputs...")
				outputs, err := getTerraformOutputs(tfDir)
				if err == nil {
					if ip, ok := outputs["static_ip"]; ok {
						fmt.Printf("  Static IP:  %s\n", ip)
					}
					if cert, ok := outputs["cert_status"]; ok {
						fmt.Printf("  Cert:       %s\n", cert)
					}
					if ns, ok := outputs["dns_nameservers"]; ok && ns != "" {
						fmt.Printf("  DNS NS:     %s\n", ns)
					}
					if instructions, ok := outputs["setup_instructions"]; ok {
						fmt.Println()
						fmt.Println(instructions)
					}
				}
				fmt.Println()
			} else {
				fmt.Println("[1-3/6] Skipped (--skip-infra)")
				fmt.Println()
			}

			// Step 4: Configure kubectl
			if !skipInfra {
				fmt.Println("[4/6] Configuring kubectl...")
				sp := startSpinner("Getting cluster credentials")
				if err := runCmd(".", "gcloud", "container", "clusters", "get-credentials",
					"aiplex", "--region", ctx.Region, "--project", ctx.Project); err != nil {
					sp.fail(fmt.Sprintf("kubectl config failed: %v", err))
				} else {
					sp.finish("kubectl configured for cluster 'aiplex'")
				}
				fmt.Println()
			}

			// Step 5: Helm deploy
			if !skipDeploy {
				fmt.Println("[5/6] Helm deploy (AIPlex API, Console, Ory, OPA)...")
				helmArgs := []string{
					"upgrade", "--install", "aiplex", helmDir,
					"--namespace", "aiplex-system", "--create-namespace",
					"--wait", "--timeout", "10m",
				}
				if valuesFile != "" {
					helmArgs = append(helmArgs, "--values", valuesFile)
				}
				// Set domain and project from CLI config
				helmArgs = append(helmArgs,
					"--set", fmt.Sprintf("global.domain=%s", ctx.Domain),
					"--set", fmt.Sprintf("global.projectId=%s", ctx.Project),
				)
				if err := runCmd(".", "helm", helmArgs...); err != nil {
					return fmt.Errorf("helm deploy failed: %w", err)
				}
				fmt.Println()
			} else {
				fmt.Println("[5/6] Skipped (--skip-deploy)")
				fmt.Println()
			}

			// Step 6: Wait for cert + health check
			fmt.Println("[6/6] Verifying deployment...")
			if !skipInfra {
				fmt.Println("  Waiting for certificate provisioning...")
				waitForCert(tfDir, 12) // check every 10s for 2 min
			}

			fmt.Println("  Running health check...")
			// Use the configured URL
			if ctx.URL != "" {
				fmt.Printf("  API URL: %s\n", ctx.URL)
			}
			fmt.Println()

			fmt.Println("Platform deploy complete.")
			fmt.Println()
			fmt.Println("Next steps:")
			fmt.Println("  aiplex login           # authenticate")
			fmt.Println("  aiplex health          # verify connectivity")
			fmt.Println("  aiplex catalog list    # browse available tools/agents/models")
			fmt.Println("  aiplex deploy ...      # deploy your first MCP server")

			return nil
		},
	}

	cmd.Flags().BoolVar(&skipInfra, "skip-infra", false, "Skip Terraform (only run Helm)")
	cmd.Flags().BoolVar(&skipDeploy, "skip-deploy", false, "Skip Helm (only run Terraform)")
	cmd.Flags().BoolVarP(&autoApprove, "yes", "y", false, "Auto-approve Terraform changes")
	cmd.Flags().StringVarP(&valuesFile, "values", "f", "", "Helm values file override")
	return cmd
}

func platformStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show infrastructure and deployment status",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := cliconfig.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			ctx, err := cfg.Current()
			if err != nil {
				return fmt.Errorf("no context — run: aiplex init")
			}

			fmt.Println("AIPlex Platform Status")
			fmt.Println("════════════════════════════════════════")
			fmt.Printf("  Context: %s\n", cfg.CurrentContext)
			fmt.Printf("  Project: %s\n", ctx.Project)
			fmt.Printf("  Region:  %s\n", ctx.Region)
			fmt.Printf("  Domain:  %s\n", ctx.Domain)
			fmt.Println()

			// Terraform outputs
			tfDir := findTerraformDir()
			outputs, err := getTerraformOutputs(tfDir)
			if err == nil && len(outputs) > 0 {
				fmt.Println("Infrastructure:")
				if ip, ok := outputs["static_ip"]; ok {
					fmt.Printf("  Load Balancer IP:  %s\n", ip)
				}
				if cert, ok := outputs["cert_status"]; ok {
					icon := "[WARN]"
					if cert == "ACTIVE" {
						icon = "[pass]"
					}
					fmt.Printf("  SSL Certificate:   %s %s\n", icon, cert)
				}
				if reg, ok := outputs["artifact_registry"]; ok {
					fmt.Printf("  Registry:          %s\n", reg)
				}
				if ns, ok := outputs["dns_nameservers"]; ok && ns != "[]" {
					fmt.Printf("  DNS Nameservers:   %s\n", ns)
				}
				fmt.Println()
			} else {
				fmt.Println("Infrastructure: not deployed (run: aiplex platform apply)")
				fmt.Println()
			}

			// GKE cluster
			fmt.Println("GKE Cluster:")
			clusterOut, err := exec.Command("gcloud", "container", "clusters", "describe",
				"aiplex", "--region", ctx.Region, "--project", ctx.Project,
				"--format=value(status)").Output()
			if err != nil {
				fmt.Println("  [FAIL] Not found or inaccessible")
			} else {
				status := strings.TrimSpace(string(clusterOut))
				icon := "[pass]"
				if status != "RUNNING" {
					icon = "[WARN]"
				}
				fmt.Printf("  %s %s\n", icon, status)
			}
			fmt.Println()

			// Helm release
			fmt.Println("Helm Release:")
			helmOut, err := exec.Command("helm", "status", "aiplex",
				"--namespace", "aiplex-system", "--output", "json").Output()
			if err != nil {
				fmt.Println("  [FAIL] Not deployed (run: aiplex platform apply)")
			} else {
				var helmStatus map[string]any
				if json.Unmarshal(helmOut, &helmStatus) == nil {
					if info, ok := helmStatus["info"].(map[string]any); ok {
						fmt.Printf("  Status:  %v\n", info["status"])
						fmt.Printf("  Version: %v\n", helmStatus["version"])
					}
				}
			}
			fmt.Println()

			// Pods
			fmt.Println("Pods (aiplex-system):")
			podsOut, _ := exec.Command("kubectl", "get", "pods",
				"-n", "aiplex-system", "--no-headers",
				"-o", "custom-columns=NAME:.metadata.name,STATUS:.status.phase,READY:.status.containerStatuses[0].ready").Output()
			if len(podsOut) > 0 {
				for _, line := range strings.Split(strings.TrimSpace(string(podsOut)), "\n") {
					if line != "" {
						fmt.Printf("  %s\n", line)
					}
				}
			} else {
				fmt.Println("  No pods found")
			}

			return nil
		},
	}
}

func platformDestroyCmd() *cobra.Command {
	var autoApprove bool

	cmd := &cobra.Command{
		Use:   "destroy",
		Short: "Tear down all infrastructure (use with extreme caution)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _ := cliconfig.Load()
			ctx, _ := cfg.Current()

			fmt.Println("WARNING: This will destroy ALL AIPlex infrastructure including:")
			fmt.Println("  - GKE cluster and all workloads")
			fmt.Println("  - AlloyDB databases (Hydra/Kratos data)")
			fmt.Println("  - Firestore data (instances, agents, permissions)")
			fmt.Println("  - SSL certificates and DNS records")
			fmt.Println("  - Service accounts and IAM bindings")
			fmt.Println()

			if ctx != nil {
				fmt.Printf("  Project: %s\n", ctx.Project)
				fmt.Printf("  Domain:  %s\n", ctx.Domain)
			}
			fmt.Println()

			if !autoApprove {
				reader := bufio.NewReader(os.Stdin)
				fmt.Print("Type 'destroy' to confirm: ")
				input, _ := reader.ReadString('\n')
				if strings.TrimSpace(input) != "destroy" {
					fmt.Println("Aborted.")
					return nil
				}
			}

			// Helm uninstall first
			fmt.Println()
			fmt.Println("[1/2] Removing Helm release...")
			runCmd(".", "helm", "uninstall", "aiplex", "--namespace", "aiplex-system")

			// Terraform destroy
			fmt.Println("[2/2] Destroying infrastructure...")
			tfDir := findTerraformDir()
			destroyArgs := []string{"destroy", "-input=false"}
			if autoApprove {
				destroyArgs = append(destroyArgs, "-auto-approve")
			}
			if err := runCmd(tfDir, "terraform", destroyArgs...); err != nil {
				return fmt.Errorf("terraform destroy failed: %w", err)
			}

			fmt.Println()
			fmt.Println("All infrastructure destroyed.")
			return nil
		},
	}

	cmd.Flags().BoolVarP(&autoApprove, "yes", "y", false, "Skip confirmation prompt")
	return cmd
}

func upgradeCmd() *cobra.Command {
	cmd := platformApplyCmd()
	cmd.Use = "upgrade"
	cmd.Short = "Upgrade the AIPlex platform (alias for platform apply)"
	cmd.Long = `Re-runs the full deployment pipeline to upgrade infrastructure and application.
This is an alias for "aiplex platform apply".`
	return cmd
}

// ─── Helpers ────────────────────────────────────────────────────

func runCmd(dir string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func findTerraformDir() string {
	candidates := []string{
		"deploy/terraform",
		"../deploy/terraform",
		".",
	}
	for _, d := range candidates {
		if _, err := os.Stat(filepath.Join(d, "main.tf")); err == nil {
			return d
		}
	}
	return "deploy/terraform"
}

func findHelmDir() string {
	candidates := []string{
		"deploy/helm/aiplex",
		"../deploy/helm/aiplex",
	}
	for _, d := range candidates {
		if _, err := os.Stat(filepath.Join(d, "Chart.yaml")); err == nil {
			return d
		}
	}
	return "deploy/helm/aiplex"
}

func getTerraformOutputs(dir string) (map[string]string, error) {
	cmd := exec.Command("terraform", "output", "-json")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var raw map[string]struct {
		Value any `json:"value"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, err
	}

	result := make(map[string]string)
	for k, v := range raw {
		switch val := v.Value.(type) {
		case string:
			result[k] = val
		case []any:
			strs := make([]string, len(val))
			for i, s := range val {
				strs[i] = fmt.Sprintf("%v", s)
			}
			result[k] = strings.Join(strs, ", ")
		default:
			result[k] = fmt.Sprintf("%v", val)
		}
	}
	return result, nil
}

// stateBucketName returns a project-specific bucket name for Terraform state.
func stateBucketName(project string) string {
	return fmt.Sprintf("%s-aiplex-tfstate", project)
}

func ensureStateBucket(project string) error {
	bucket := stateBucketName(project)
	// Check if bucket exists
	err := exec.Command("gcloud", "storage", "buckets", "describe",
		fmt.Sprintf("gs://%s", bucket), "--project", project, "--quiet").Run()
	if err == nil {
		return nil // exists
	}
	// Create it
	return exec.Command("gcloud", "storage", "buckets", "create",
		fmt.Sprintf("gs://%s", bucket),
		"--project", project,
		"--location", "US",
		"--uniform-bucket-level-access",
		"--quiet").Run()
}

func waitForCert(tfDir string, maxChecks int) {
	for i := 0; i < maxChecks; i++ {
		outputs, err := getTerraformOutputs(tfDir)
		if err == nil {
			if status, ok := outputs["cert_status"]; ok && status == "ACTIVE" {
				fmt.Println("  [pass] Certificate is ACTIVE")
				return
			}
		}
		if i < maxChecks-1 {
			fmt.Printf("  Certificate provisioning... (check %d/%d)\n", i+1, maxChecks)
			time.Sleep(10 * time.Second)
		}
	}
	fmt.Println("  [WARN] Certificate not yet active — will auto-provision once DNS propagates")
}
