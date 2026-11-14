package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/vamsiramakrishnan/aiplex/internal/cliconfig"
	"github.com/vamsiramakrishnan/aiplex/internal/gcpsetup"
	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

func doctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose common issues and suggest fixes",
		Long: `Runs a comprehensive diagnostic of your AIPlex setup:
  - CLI configuration and credentials
  - GCP project access, and the deployer permissions, APIs and billing
    the Terraform needs (same check as aiplex platform preflight)
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

			// The API this CLI talks to: --url, AIPLEX_URL, the context, then
			// the default. A loopback API (aiplex dev, make run-local) has no
			// cloud project, cluster, DNS or Hydra to check.
			target := resolveAPIURL()
			local := isLoopbackURL(target)
			tokenSource := ""
			switch {
			case token != "":
				tokenSource = "--token"
			case os.Getenv("AIPLEX_TOKEN") != "":
				tokenSource = "AIPLEX_TOKEN"
			}

			// 1. CLI config
			fmt.Println("[1/7] CLI Configuration")
			cfg, err := cliconfig.Load()
			switch {
			case err != nil:
				fmt.Println("  [FAIL] Cannot load config — run: aiplex init")
				issues++
			case cfg.CurrentContext == "" && (apiURL != "" || os.Getenv("AIPLEX_URL") != ""):
				fmt.Printf("  [pass] API URL from flag/environment: %s\n", target)
			case cfg.CurrentContext == "" && local:
				fmt.Printf("  [WARN] No active context; using %s. Start a local API with: aiplex dev\n", target)
				warnings++
			case cfg.CurrentContext == "":
				fmt.Println("  [FAIL] No active context — run: aiplex dev (local) or aiplex init (cloud)")
				issues++
			default:
				ctx := cfg.Contexts[cfg.CurrentContext]
				fmt.Printf("  [pass] Context: %s → %s\n", cfg.CurrentContext, ctx.URL)
				if ctx.Project == "" && !local {
					fmt.Println("  [WARN] No GCP project set — run: aiplex config set-context <name> --project <id>")
					warnings++
				}
			}

			// Credentials
			creds, err := cliconfig.LoadCredentials()
			switch {
			case tokenSource != "":
				fmt.Printf("  [pass] Token from %s\n", tokenSource)
			case err != nil || cfg == nil:
				fmt.Println("  [WARN] No credentials file")
				warnings++
			case creds.GetToken(cfg.CurrentContext) == nil && local:
				fmt.Println("  [WARN] No token for this context; `aiplex dev` stores one while it runs")
				warnings++
			case creds.GetToken(cfg.CurrentContext) == nil:
				fmt.Println("  [WARN] Not authenticated — run: aiplex login")
				warnings++
			default:
				fmt.Println("  [pass] Authenticated")
			}
			fmt.Println()
			if local {
				fmt.Println("[2-4/7] Tools, GCP project, Kubernetes")
				fmt.Println("  [SKIP] Local API: cloud tooling, project and cluster checks do not apply")
				fmt.Println()
			}

			if !local {
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

						// Deployer permissions, APIs and billing: the same table and
						// check as aiplex platform preflight. Advisory here: the
						// platform may have been deployed by someone else.
						toggles, terr := gcpsetup.ReadToggles(filepath.Join(findTerraformDir(), "terraform.tfvars"))
						if terr != nil {
							toggles = gcpsetup.DefaultToggles()
						}
						report, _, perr := runGCPPreflight(cmd.Context(), preflightInput{project: ctx.Project, toggles: toggles})
						switch {
						case perr != nil:
							fmt.Printf("  [WARN] Permission preflight could not run: %v\n", perr)
							warnings++
						case report.OK:
							fmt.Println("  [pass] Deployer permissions, APIs and billing")
						default:
							for _, f := range report.Failures {
								fmt.Printf("  [WARN] %s\n", f)
							}
							fmt.Println("    Details and the roles to grant: aiplex platform preflight")
							warnings++
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

			}

			// 5. API connectivity
			fmt.Println("[5/7] API Server")
			{
				url := target

				client := &http.Client{Timeout: 5 * time.Second}
				resp, err := client.Get(url + "/healthz")
				if err != nil {
					fmt.Printf("  [FAIL] Cannot reach %s\n", url)
					if local {
						fmt.Println("    Fix: start the local API with: aiplex dev")
					} else {
						fmt.Println("    Fix: Is the API deployed? Check: aiplex platform status")
					}
					issues++
				} else {
					resp.Body.Close()
					fmt.Printf("  [pass] API healthy (%s)\n", url)
				}

				// Try an authenticated request. whoami answers for any signed-in
				// identity; the dashboard is admin-only and says nothing about
				// whether the token works.
				c := withoutRedirects(newClient())
				identity, err := c.GetIdentity(context.Background())
				var apiErr *aiplex.Error
				switch {
				case err == nil:
					name := identity.Identity.Email
					if name == "" {
						name = identity.Identity.Subject
					}
					if name == "" {
						name = "(anonymous: this API admits unauthenticated requests)"
					}
					roles := "none"
					if len(identity.Roles) > 0 {
						roles = strings.Join(identity.Roles, ", ")
					}
					fmt.Printf("  [pass] Authenticated as %s (roles: %s)\n", name, roles)
					if !slices.Contains(identity.Roles, "admin") {
						fmt.Println("  [INFO] Admin role not held: admin-only checks (such as the dashboard) are unavailable to this identity")
					}
				case errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusUnauthorized:
					fmt.Println("  [WARN] Token rejected or not signed in — run: aiplex login")
					warnings++
				case errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusForbidden:
					fmt.Println("  [INFO] Signed in, but this identity lacks the role for the identity lookup")
				default:
					// A 5xx, a connection error or any other answer: the API is not
					// giving a usable identity answer, which is a failure.
					fmt.Printf("  [FAIL] Identity check failed (%s)\n", simplifyError(err))
					issues++
				}
			}
			fmt.Println()

			// 6. Cert/DNS
			fmt.Println("[6/7] Certificate & DNS")
			if local {
				fmt.Println("  [SKIP] Local API")
			} else if cfg != nil && cfg.CurrentContext != "" {
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
			if local {
				fmt.Println("  [SKIP] Local API: Hydra and Kratos are not used by aiplex dev")
			} else {
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
				if local {
					fmt.Println("For a local API, run: aiplex dev")
				} else {
					fmt.Println("Common fix sequence:")
					fmt.Println("  1. aiplex init                 # configure project")
					fmt.Println("  2. aiplex platform apply       # deploy everything")
					fmt.Println("  3. aiplex login                # authenticate")
					fmt.Println("  4. aiplex health               # verify connectivity")
				}
				return fmt.Errorf("%d issue(s) found", issues)
			}

			return nil
		},
	}
}

// isLoopbackURL reports whether raw names a loopback host (a local API).
func isLoopbackURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := u.Hostname()
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
