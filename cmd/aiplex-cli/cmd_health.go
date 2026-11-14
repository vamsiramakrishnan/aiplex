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
	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
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
			// Resolve base URL and token the way every command does:
			// --url, AIPLEX_URL, the context; --token, AIPLEX_TOKEN, the context.
			tgt := resolveTarget()
			url := tgt.URL

			fmt.Println("AIPlex Health Check")
			fmt.Println("════════════════════════════════════════")
			fmt.Printf("Target: %s\n\n", url)

			client := newHealthHTTPClient()
			allHealthy := true
			warnings := 0

			checks := []healthCheck{
				{name: "API Server", path: "/healthz"},
				{name: "API Readiness", path: "/readyz"},
				{name: "Catalog", path: "/api/v1/catalog", signedIn: true},
				{name: "Instances", path: "/api/v1/instances", signedIn: true},
				{name: "Dashboard", path: "/api/v1/dashboard/stats", signedIn: true},
			}

			// Auth checks (these might be on different hosts in prod)
			authChecks := []healthCheck{
				{name: "Hydra (OAuth)", path: "/auth/login"},
			}

			// Run core checks. Only the signed-in API routes carry the token.
			fmt.Println("Core Services:")
			for _, c := range checks {
				bearer := ""
				if c.signedIn {
					bearer = tgt.Token
				}
				result := runHealthCheck(client, url+c.path, bearer, c.signedIn, verbose)
				switch {
				case result.healthy:
					fmt.Printf("  [pass] %-20s %dms\n", c.name, result.latencyMs)
				case result.failed:
					fmt.Printf("  [FAIL] %-20s %s\n", c.name, result.error)
					allHealthy = false
				default:
					fmt.Printf("  [WARN] %-20s %s\n", c.name, result.error)
					warnings++
				}
			}
			fmt.Println()

			// Run auth checks (a loopback API from `aiplex dev` has no Hydra/Kratos)
			fmt.Println("Auth Services:")
			if isLoopbackURL(url) {
				fmt.Println("  [SKIP] local API: identity comes from the local issuer")
				authChecks = nil
			}
			for _, c := range authChecks {
				// Auth services may be on other hosts, so they never get the token.
				// A bare GET to the login endpoint can answer 400 (no login
				// challenge) or redirect to the login UI, which is normal, so only
				// an unreachable service or a 5xx fails this check.
				result := runHealthCheck(client, url+c.path, "", false, verbose)
				switch {
				case result.healthy:
					fmt.Printf("  [pass] %-20s %dms\n", c.name, result.latencyMs)
				case result.failed && (result.status == 0 || result.status >= 500):
					fmt.Printf("  [FAIL] %-20s %s\n", c.name, result.error)
					allHealthy = false
				default:
					fmt.Printf("  [WARN] %-20s %s\n", c.name, result.error)
					warnings++
				}
			}
			fmt.Println()

			// Try to get dashboard stats for a richer view
			c := withoutRedirects(newClient())
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
				if warnings == 0 {
					fmt.Println("All checks passed.")
				} else {
					fmt.Printf("Reachable, with %d check(s) to look at (see [WARN] above).\n", warnings)
				}
			} else {
				fmt.Println()
				fmt.Println("Troubleshooting:")
				if isLoopbackURL(url) {
					fmt.Println("  Local API: is `aiplex dev` running? Its log is ~/.aiplex/dev-api.log")
					return fmt.Errorf("health checks failed")
				}
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
				fmt.Println("  - For local dev: aiplex dev (starts on 127.0.0.1:8080)")
				return fmt.Errorf("health checks failed")
			}

			return nil
		},
	}

	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Show detailed diagnostics")
	return cmd
}

type healthCheck struct {
	name     string
	path     string
	signedIn bool // the route is one of the API's signed-in routes
}

type healthResult struct {
	healthy   bool
	failed    bool // a failure: unreachable, or an answer that is not 2xx (see runHealthCheck)
	latencyMs int64
	status    int // 0 when no answer was received
	error     string
}

// newHealthHTTPClient is the client the health probes use. It does not follow
// redirects: Go keeps the Authorization header when a redirect goes to another
// port or to an https-to-http downgrade on the same host, so a followed
// redirect could hand the token to a host the caller never configured.
func newHealthHTTPClient() *http.Client {
	return &http.Client{Timeout: 5 * time.Second, CheckRedirect: noFollowRedirects}
}

// withoutRedirects stops an SDK client from following redirects, for the same
// reason as newHealthHTTPClient: its requests carry the bearer token.
func withoutRedirects(c *aiplex.Client) *aiplex.Client {
	if c.HTTPClient != nil {
		c.HTTPClient.CheckRedirect = noFollowRedirects
	}
	return c
}

// noFollowRedirects is the SDK's own function: one implementation.
func noFollowRedirects(req *http.Request, via []*http.Request) error {
	return aiplex.NoFollowRedirects(req, via)
}

// runHealthCheck GETs target once. The bearer is sent only when signedIn is
// true, so the probes that carry the caller's token are the signed-in API
// routes, which are built from the configured API URL. Auth-service probes
// pass no bearer.
//
// Only a 2xx answer is healthy. A connection error, or any other answer, is a
// failure. The exception is a 401 or 403 on a signed-in route: that is a
// sign-in or role answer, reported as a warning.
func runHealthCheck(client *http.Client, target, bearer string, signedIn, verbose bool) healthResult {
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		return healthResult{failed: true, error: simplifyError(err)}
	}
	if signedIn && bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}

	start := time.Now()
	resp, err := client.Do(req)
	latency := time.Since(start).Milliseconds()

	if err != nil {
		return healthResult{
			failed: true,
			error:  simplifyError(err),
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return healthResult{
			healthy:   true,
			latencyMs: latency,
			status:    resp.StatusCode,
		}
	}

	errMsg := httpStatusMessage(resp.StatusCode)
	if verbose {
		var body map[string]any
		if json.NewDecoder(resp.Body).Decode(&body) == nil {
			if msg, ok := body["message"].(string); ok {
				errMsg += ": " + msg
			}
		}
	}

	softAnswer := signedIn && (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden)
	return healthResult{
		failed: !softAnswer,
		status: resp.StatusCode,
		error:  errMsg,
	}
}

// httpStatusMessage says what a non-2xx answer means for a health check.
func httpStatusMessage(code int) string {
	switch {
	case code == http.StatusUnauthorized:
		return "HTTP 401: not signed in, or token rejected — run: aiplex login"
	case code == http.StatusForbidden:
		return "HTTP 403: this identity lacks the role for this check"
	case code >= 300 && code < 400:
		return fmt.Sprintf("HTTP %d: redirect not followed", code)
	default:
		return fmt.Sprintf("HTTP %d", code)
	}
}

func simplifyError(err error) string {
	msg := err.Error()
	if strings.Contains(msg, "connection refused") {
		return "connection refused — server not running?"
	}
	if strings.Contains(msg, "no such host") {
		return "DNS resolution failed — check domain"
	}
	if strings.Contains(msg, "timeout") {
		return "connection timed out — check network/firewall"
	}
	if strings.Contains(msg, "certificate") {
		return "TLS error — check SSL certificate"
	}
	return msg
}
