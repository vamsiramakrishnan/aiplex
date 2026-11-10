package main

import (
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"github.com/vamsiramakrishnan/aiplex/console"
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

			mux, err := consoleHandler(apiURL, resolveToken())
			if err != nil {
				return err
			}
			if !consoleBuilt() {
				fmt.Println("  Note: this binary carries a placeholder Console page. Build the Console with")
				fmt.Println("  `make console && make build-cli` (Node 22), or run `cd console && npm ci && npm run dev`.")
			}

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

// consoleHandler serves the embedded Console and proxies API paths to apiURL,
// adding the bearer token (when set) to proxied requests.
func consoleHandler(apiURL, resolvedToken string) (http.Handler, error) {
	target, err := url.Parse(apiURL)
	if err != nil {
		return nil, fmt.Errorf("invalid API URL %q: %w", apiURL, err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		if resolvedToken != "" {
			req.Header.Set("Authorization", "Bearer "+resolvedToken)
		}
	}
	distFS, err := fs.Sub(console.Dist, "dist")
	if err != nil {
		return nil, fmt.Errorf("embedded console not found — rebuild with: cd console && npm run build")
	}
	mux := http.NewServeMux()
	for _, prefix := range []string{"/api/", "/auth/", "/healthz", "/readyz", "/v0.1/", "/events/", "/oauth2/"} {
		mux.Handle(prefix, proxy)
	}
	mux.Handle("/", spaFileServer(http.FS(distFS)))
	return mux, nil
}

// consoleBuilt reports whether the embedded Console is a real build rather
// than the placeholder page committed to the repository.
func consoleBuilt() bool {
	index, err := fs.ReadFile(console.Dist, "dist/index.html")
	return err == nil && strings.Contains(string(index), "<script")
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
