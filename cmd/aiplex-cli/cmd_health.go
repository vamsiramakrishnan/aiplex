package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/vamsiramakrishnan/aiplex/internal/cliconfig"
)

func healthCmd() *cobra.Command {
	var verbose bool

	cmd := &cobra.Command{
		Use:   "health",
		Short: "Check connectivity to all AIPlex components",
		Long: `Runs connectivity checks against the AIPlex API, auth services,
and gateway. Useful for diagnosing setup issues.

Checks:
  - API server reachability (/healthz)
  - Auth service (Hydra) availability
  - Kratos identity service availability
  - Gateway routing (MCPlex, A2APlex, LLMPlex paths)
  - DNS resolution for custom domain`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Resolve base URL
			url := apiURL
			if url == "" {
				cfg, err := cliconfig.Load()
				if err == nil {
					if ctx, err := cfg.Current(); err == nil {
						url = ctx.URL
					}
				}
			}
			if url == "" {
				url = "http://localhost:8080"
			}

			fmt.Println("AIPlex Health Check")
			fmt.Println("════════════════════════════════════════")
			fmt.Printf("Target: %s\n\n", url)

			client := &http.Client{Timeout: 5 * time.Second}
			allHealthy := true

			checks := []healthCheck{
				{name: "API Server", path: "/healthz"},
				{name: "API Readiness", path: "/readyz"},
				{name: "Catalog", path: "/api/v1/catalog"},
				{name: "Instances", path: "/api/v1/instances"},
				{name: "Dashboard", path: "/api/v1/dashboard/stats"},
			}

			// Auth checks (these might be on different hosts in prod)
			authChecks := []healthCheck{
				{name: "Hydra (OAuth)", path: "/auth/login"},
			}

			// Run core checks
			fmt.Println("Core Services:")
			for _, c := range checks {
				result := runHealthCheck(client, url+c.path, verbose)
				if result.healthy {
					fmt.Printf("  [pass] %-20s %dms\n", c.name, result.latencyMs)
				} else {
					fmt.Printf("  [FAIL] %-20s %s\n", c.name, result.error)
					allHealthy = false
				}
			}
			fmt.Println()

			// Run auth checks
			fmt.Println("Auth Services:")
			for _, c := range authChecks {
				result := runHealthCheck(client, url+c.path, verbose)
				if result.healthy {
					fmt.Printf("  [pass] %-20s %dms\n", c.name, result.latencyMs)
				} else {
					fmt.Printf("  [WARN] %-20s %s\n", c.name, result.error)
				}
			}
			fmt.Println()

			// Try to get dashboard stats for a richer view
			c := newClient()
			stats, err := c.GetDashboardStats(context.Background())
			if err == nil {
				fmt.Println("Platform Status:")
				fmt.Printf("  Instances: %d (%d running)\n", stats.TotalInstances, stats.RunningInstances)
				fmt.Printf("  Agents:    %d\n", stats.RegisteredAgents)
				fmt.Printf("  Planes:    %d active\n", stats.ActivePlanes)
				if stats.PolicyDenials > 0 {
					fmt.Printf("  Denials:   %d (last 24h)\n", stats.PolicyDenials)
				}
				fmt.Println()
			}

			if verbose {
				fmt.Println("Configuration:")
				cfg, err := cliconfig.Load()
				if err == nil {
					if ctx, err := cfg.Current(); err == nil {
						fmt.Printf("  Context:  %s\n", cfg.CurrentContext)
						fmt.Printf("  Project:  %s\n", ctx.Project)
						fmt.Printf("  Region:   %s\n", ctx.Region)
						fmt.Printf("  Domain:   %s\n", ctx.Domain)
					}
				}
				creds, err := cliconfig.LoadCredentials()
				if err == nil && cfg != nil {
					if t := creds.GetToken(cfg.CurrentContext); t != nil {
						fmt.Println("  Auth:     authenticated")
					} else {
						fmt.Println("  Auth:     not authenticated — run: aiplex login")
					}
				}
				fmt.Println()
			}

			if allHealthy {
				fmt.Println("All checks passed.")
			} else {
				fmt.Println()
				fmt.Println("Troubleshooting:")
				fmt.Println("  Fetching logs from unhealthy pods...")
				fmt.Println()
				logsOut, err := exec.Command("kubectl", "logs",
					"-n", "aiplex-system",
					"-l", "app.kubernetes.io/part-of=aiplex",
					"--tail=20",
					"--all-containers",
				).CombinedOutput()
				if err == nil && len(logsOut) > 0 {
					fmt.Println("  Recent pod logs (last 20 lines):")
					for _, line := range strings.Split(strings.TrimSpace(string(logsOut)), "\n") {
						fmt.Printf("    %s\n", line)
					}
				} else {
					fmt.Println("  Could not fetch pod logs. Run manually:")
					fmt.Println("    kubectl logs -n aiplex-system -l app.kubernetes.io/part-of=aiplex --tail=50")
				}
				fmt.Println()
				fmt.Println("  Common issues:")
				fmt.Println("  - Is the API server running? Check: kubectl -n aiplex-system get pods")
				fmt.Println("  - Is the domain configured? Check DNS and SSL cert")
				fmt.Println("  - Are you authenticated? Run: aiplex login")
				fmt.Println("  - For local dev: make run-local (starts on :8080)")
			}

			return nil
		},
	}

	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Show detailed diagnostics")
	return cmd
}

type healthCheck struct {
	name string
	path string
}

type healthResult struct {
	healthy   bool
	latencyMs int64
	status    int
	error     string
}

func runHealthCheck(client *http.Client, url string, verbose bool) healthResult {
	start := time.Now()
	resp, err := client.Get(url)
	latency := time.Since(start).Milliseconds()

	if err != nil {
		return healthResult{
			healthy: false,
			error:   simplifyError(err),
		}
	}
	defer resp.Body.Close()

	// 2xx and 3xx are healthy
	if resp.StatusCode < 400 {
		return healthResult{
			healthy:   true,
			latencyMs: latency,
			status:    resp.StatusCode,
		}
	}

	// Try to read error body
	errMsg := fmt.Sprintf("HTTP %d", resp.StatusCode)
	if verbose {
		var body map[string]any
		if json.NewDecoder(resp.Body).Decode(&body) == nil {
			if msg, ok := body["message"].(string); ok {
				errMsg = fmt.Sprintf("HTTP %d: %s", resp.StatusCode, msg)
			}
		}
	}

	return healthResult{
		healthy: false,
		status:  resp.StatusCode,
		error:   errMsg,
	}
}

func simplifyError(err error) string {
	msg := err.Error()
	if contains(msg, "connection refused") {
		return "connection refused — server not running?"
	}
	if contains(msg, "no such host") {
		return "DNS resolution failed — check domain"
	}
	if contains(msg, "timeout") {
		return "connection timed out — check network/firewall"
	}
	if contains(msg, "certificate") {
		return "TLS error — check SSL certificate"
	}
	return msg
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsStr(s, sub))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
