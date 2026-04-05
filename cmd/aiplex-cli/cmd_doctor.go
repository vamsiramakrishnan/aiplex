package main

import (
	"context"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/vamsiramakrishnan/aiplex/internal/cliconfig"
)

func doctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose common issues and suggest fixes",
		Long: `Runs a comprehensive diagnostic of your AIPlex setup:
  - CLI configuration and credentials
  - GCP project access and permissions
  - Kubernetes cluster connectivity
  - API server health
  - Auth services (Hydra/Kratos)
  - Certificate and DNS status
  - Common permission and networking issues`,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println("AIPlex Doctor")
			fmt.Println("════════════════════════════════════════")
			fmt.Println()

			issues := 0
			warnings := 0

			// 1. CLI config
			fmt.Println("[1/7] CLI Configuration")
			cfg, err := cliconfig.Load()
			if err != nil {
				fmt.Println("  [FAIL] Cannot load config — run: aiplex init")
				issues++
			} else if cfg.CurrentContext == "" {
				fmt.Println("  [FAIL] No active context — run: aiplex init")
				issues++
			} else {
				ctx := cfg.Contexts[cfg.CurrentContext]
				fmt.Printf("  [pass] Context: %s → %s\n", cfg.CurrentContext, ctx.URL)

				if ctx.Project == "" {
					fmt.Println("  [WARN] No GCP project set — run: aiplex config set-context <name> --project <id>")
					warnings++
				}
			}

			// Credentials
			creds, err := cliconfig.LoadCredentials()
			if err != nil || cfg == nil {
				fmt.Println("  [WARN] No credentials file")
				warnings++
			} else if creds.GetToken(cfg.CurrentContext) == nil {
				fmt.Println("  [WARN] Not authenticated — run: aiplex login")
				warnings++
			} else {
				fmt.Println("  [pass] Authenticated")
			}
			fmt.Println()

			// 2. Tool dependencies
			fmt.Println("[2/7] Required Tools")
			tools := map[string]string{
				"gcloud":    "GCP CLI",
				"terraform": "Infrastructure",
				"helm":      "Deployment",
				"kubectl":   "Cluster management",
				"docker":    "Local development",
			}
			for tool, purpose := range tools {
				if _, err := exec.LookPath(tool); err != nil {
					fmt.Printf("  [WARN] %s (%s) — not installed\n", tool, purpose)
					warnings++
				} else {
					fmt.Printf("  [pass] %s\n", tool)
				}
			}
			fmt.Println()

			// 3. GCP project
			fmt.Println("[3/7] GCP Project")
			if cfg != nil && cfg.CurrentContext != "" {
				ctx := cfg.Contexts[cfg.CurrentContext]
				if ctx.Project != "" {
					out, err := exec.Command("gcloud", "projects", "describe", ctx.Project,
						"--format=value(projectId)", "--quiet").Output()
					if err != nil {
						fmt.Printf("  [FAIL] Cannot access project %s\n", ctx.Project)
						fmt.Println("    Fix: gcloud auth login && gcloud config set project " + ctx.Project)
						issues++
					} else {
						fmt.Printf("  [pass] Project: %s\n", strings.TrimSpace(string(out)))
					}

					// Check required APIs
					requiredAPIs := []string{"container", "alloydb", "firestore", "secretmanager"}
					for _, api := range requiredAPIs {
						svc := api + ".googleapis.com"
						err := exec.Command("gcloud", "services", "list", "--project", ctx.Project,
							"--filter=name:"+svc, "--format=value(name)", "--quiet").Run()
						if err != nil {
							fmt.Printf("  [WARN] API %s may not be enabled\n", svc)
							warnings++
						}
					}
				} else {
					fmt.Println("  [SKIP] No project configured")
				}
			}
			fmt.Println()

			// 4. Kubernetes
			fmt.Println("[4/7] Kubernetes Cluster")
			out, err := exec.Command("kubectl", "cluster-info", "--request-timeout=5s").Output()
			if err != nil {
				fmt.Println("  [FAIL] Cannot connect to cluster")
				fmt.Println("    Fix: aiplex platform apply (or: gcloud container clusters get-credentials aiplex)")
				issues++
			} else {
				firstLine := strings.Split(string(out), "\n")[0]
				fmt.Printf("  [pass] %s\n", firstLine)

				// Check aiplex namespace
				nsOut, _ := exec.Command("kubectl", "get", "ns", "aiplex-system",
					"--no-headers", "-o=custom-columns=:.status.phase").Output()
				if strings.TrimSpace(string(nsOut)) == "Active" {
					fmt.Println("  [pass] aiplex-system namespace exists")
				} else {
					fmt.Println("  [WARN] aiplex-system namespace not found — run: aiplex platform apply")
					warnings++
				}
			}
			fmt.Println()

			// 5. API connectivity
			fmt.Println("[5/7] API Server")
			if cfg != nil && cfg.CurrentContext != "" {
				ctx := cfg.Contexts[cfg.CurrentContext]
				url := ctx.URL
				if url == "" {
					url = "http://localhost:8080"
				}

				client := &http.Client{Timeout: 5 * time.Second}
				resp, err := client.Get(url + "/healthz")
				if err != nil {
					fmt.Printf("  [FAIL] Cannot reach %s\n", url)
					fmt.Println("    Fix: Is the API deployed? Check: aiplex platform status")
					issues++
				} else {
					resp.Body.Close()
					fmt.Printf("  [pass] API healthy (%s)\n", url)
				}

				// Try an authenticated request
				c := newClient()
				_, err = c.GetDashboardStats(context.Background())
				if err != nil {
					fmt.Println("  [WARN] Authenticated request failed — check your token")
					warnings++
				} else {
					fmt.Println("  [pass] Authenticated access working")
				}
			}
			fmt.Println()

			// 6. Cert/DNS
			fmt.Println("[6/7] Certificate & DNS")
			if cfg != nil && cfg.CurrentContext != "" {
				ctx := cfg.Contexts[cfg.CurrentContext]
				if ctx.Domain != "" {
					// Try HTTPS
					client := &http.Client{Timeout: 5 * time.Second}
					httpsURL := fmt.Sprintf("https://%s/healthz", ctx.Domain)
					resp, err := client.Get(httpsURL)
					if err != nil {
						errStr := err.Error()
						if strings.Contains(errStr, "certificate") || strings.Contains(errStr, "x509") {
							fmt.Println("  [FAIL] SSL certificate invalid or not provisioned")
							fmt.Println("    Fix: Certificate auto-provisions after DNS propagates.")
							fmt.Println("    Check: aiplex platform status")
							issues++
						} else if strings.Contains(errStr, "no such host") {
							fmt.Println("  [FAIL] DNS not resolving for " + ctx.Domain)
							fmt.Println("    Fix: Point domain NS records to Cloud DNS nameservers")
							fmt.Println("    Check: aiplex platform status (shows nameservers)")
							issues++
						} else {
							fmt.Printf("  [WARN] HTTPS check failed: %s\n", simplifyError(err))
							warnings++
						}
					} else {
						resp.Body.Close()
						fmt.Printf("  [pass] HTTPS working at %s\n", ctx.Domain)
					}
				} else {
					fmt.Println("  [SKIP] No domain configured")
				}
			}
			fmt.Println()

			// 7. Common issues
			fmt.Println("[7/7] Common Issues Check")
			// Check if Hydra pods are running
			hydraOut, _ := exec.Command("kubectl", "get", "pods", "-n", "aiplex-system",
				"-l", "app.kubernetes.io/name=hydra", "--no-headers",
				"-o=custom-columns=:.status.phase").Output()
			if strings.TrimSpace(string(hydraOut)) == "Running" {
				fmt.Println("  [pass] Hydra (OAuth) running")
			} else if len(hydraOut) > 0 {
				fmt.Printf("  [WARN] Hydra status: %s\n", strings.TrimSpace(string(hydraOut)))
				warnings++
			}

			kratosOut, _ := exec.Command("kubectl", "get", "pods", "-n", "aiplex-system",
				"-l", "app.kubernetes.io/name=kratos", "--no-headers",
				"-o=custom-columns=:.status.phase").Output()
			if strings.TrimSpace(string(kratosOut)) == "Running" {
				fmt.Println("  [pass] Kratos (Identity) running")
			} else if len(kratosOut) > 0 {
				fmt.Printf("  [WARN] Kratos status: %s\n", strings.TrimSpace(string(kratosOut)))
				warnings++
			}
			fmt.Println()

			// Summary
			fmt.Println("════════════════════════════════════════")
			if issues == 0 && warnings == 0 {
				fmt.Println("All checks passed. Your AIPlex setup looks healthy.")
			} else if issues == 0 {
				fmt.Printf("%d warning(s), no critical issues.\n", warnings)
			} else {
				fmt.Printf("%d issue(s), %d warning(s) found.\n", issues, warnings)
				fmt.Println()
				fmt.Println("Common fix sequence:")
				fmt.Println("  1. aiplex init                 # configure project")
				fmt.Println("  2. aiplex platform apply       # deploy everything")
				fmt.Println("  3. aiplex login                # authenticate")
				fmt.Println("  4. aiplex health               # verify connectivity")
			}

			return nil
		},
	}
}
