package main

import (
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/vamsiramakrishnan/aiplex/console"
	"github.com/vamsiramakrishnan/aiplex/internal/cliconfig"
)

func consoleCmd() *cobra.Command {
	var port int

	cmd := &cobra.Command{
		Use:   "console",
		Short: "Launch the AIPlex console locally",
		Long: `Starts a local web server serving the AIPlex console.
API requests are proxied to your configured AIPlex instance.

The console is embedded in the CLI binary — no separate install needed.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Resolve API URL from config
			apiURL := resolveAPIURL()

			target, err := url.Parse(apiURL)
			if err != nil {
				return fmt.Errorf("invalid API URL %q: %w", apiURL, err)
			}

			// Reverse proxy for API calls
			proxy := httputil.NewSingleHostReverseProxy(target)

			// Resolve token for proxy auth
			resolvedToken := resolveToken()

			originalDirector := proxy.Director
			proxy.Director = func(req *http.Request) {
				originalDirector(req)
				if resolvedToken != "" {
					req.Header.Set("Authorization", "Bearer "+resolvedToken)
				}
			}

			// Embedded SPA filesystem
			distFS, err := fs.Sub(console.Dist, "dist")
			if err != nil {
				return fmt.Errorf("embedded console not found — rebuild with: cd console && npm run build")
			}
			spaHandler := spaFileServer(http.FS(distFS))

			// Router: API paths → proxy, everything else → SPA
			mux := http.NewServeMux()

			// Proxy API paths
			apiPrefixes := []string{"/api/", "/auth/", "/healthz", "/readyz", "/v0.1/", "/events/", "/oauth2/"}
			for _, prefix := range apiPrefixes {
				mux.Handle(prefix, proxy)
			}

			// SPA for everything else
			mux.Handle("/", spaHandler)

			addr := fmt.Sprintf("127.0.0.1:%d", port)
			localURL := fmt.Sprintf("http://%s", addr)

			fmt.Println()
			fmt.Printf("  AIPlex Console\n")
			fmt.Printf("  Local:   %s\n", localURL)
			fmt.Printf("  API:     %s\n", apiURL)
			fmt.Println()
			fmt.Println("  Press Ctrl+C to stop.")
			fmt.Println()

			openBrowser(localURL)

			return http.ListenAndServe(addr, mux)
		},
	}

	cmd.Flags().IntVar(&port, "port", 9090, "Local port for the console")
	return cmd
}

// spaFileServer serves static files, falling back to index.html for client-side routing.
func spaFileServer(root http.FileSystem) http.Handler {
	fileServer := http.FileServer(root)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// Try to serve the file directly
		f, err := root.Open(path)
		if err != nil {
			// File not found — serve index.html for SPA routing
			r.URL.Path = "/"
			fileServer.ServeHTTP(w, r)
			return
		}
		f.Close()
		fileServer.ServeHTTP(w, r)
	})
}

// resolveAPIURL returns the API URL from flags, env, or config.
func resolveAPIURL() string {
	if apiURL != "" {
		return apiURL
	}
	if u := os.Getenv("AIPLEX_URL"); u != "" {
		return u
	}
	cfg, err := cliconfig.Load()
	if err == nil {
		if ctx, err := cfg.Current(); err == nil && ctx.URL != "" {
			return ctx.URL
		}
	}
	return "http://localhost:8080"
}

// resolveToken returns the auth token from flags, env, or config.
func resolveToken() string {
	if token != "" {
		return token
	}
	if t := os.Getenv("AIPLEX_TOKEN"); t != "" {
		return t
	}
	cfg, err := cliconfig.Load()
	if err == nil {
		creds, err := cliconfig.LoadCredentials()
		if err == nil {
			if tok := creds.GetToken(cfg.CurrentContext); tok != nil {
				// Auto-refresh expired tokens
				if tok.AccessToken != "" && tok.RefreshToken != "" && tok.ExpiresAt != "" {
					if expiry, err := time.Parse(time.RFC3339, tok.ExpiresAt); err == nil {
						if time.Now().After(expiry.Add(-5 * time.Minute)) {
							// Token expired or expiring soon — try refresh
							if ctx, err := cfg.Current(); err == nil && ctx.URL != "" {
								tr, err := refreshToken(ctx.URL, tok.RefreshToken)
								if err == nil && tr.AccessToken != "" {
									storeToken(cfg.CurrentContext, tr.AccessToken, tr.RefreshToken)
									return tr.AccessToken
								}
							}
						}
					}
				}
				return tok.AccessToken
			}
		}
	}
	return ""
}
