package main

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/spf13/cobra"

	"github.com/vamsiramakrishnan/aiplex/console"
	"github.com/vamsiramakrishnan/aiplex/internal/api"
	"github.com/vamsiramakrishnan/aiplex/internal/auth"
	"github.com/vamsiramakrishnan/aiplex/internal/capability"
	"github.com/vamsiramakrishnan/aiplex/internal/catalog"
	"github.com/vamsiramakrishnan/aiplex/internal/deploy"
	"github.com/vamsiramakrishnan/aiplex/internal/memplex"
	"github.com/vamsiramakrishnan/aiplex/internal/registry"
	"github.com/vamsiramakrishnan/aiplex/internal/workflow"
)

// upCmd boots a single-binary AIPlex node on localhost — no Hydra, no
// Kratos, no Envoy, no GKE. Embedded auth (Ed25519 signs the caps claim),
// in-memory store, all kinds wired through the same KindHook abstraction
// the production stack uses. The point: put the Capability primitive in
// someone's hands in <30 seconds.
//
// See design/25-the-problem-we-solve.md.
func upCmd() *cobra.Command {
	var (
		port      int
		dataDir   string
		noBrowser bool
		noOpenCap bool
	)
	cmd := &cobra.Command{
		Use:   "up",
		Short: "Run a local AIPlex node (single binary, no cloud)",
		Long: `Start a fully-functional AIPlex node on localhost. Embedded everything:
  - Auth (Ed25519 signer, no Hydra)
  - All capability kinds (tool, task, model, skill, memory, agent, workflow)
  - Memory broker + workflow executor in-process
  - Console served on the same port

State persists in ~/.aiplex/. Same Capability protocol as production AIPlex.

Try:
  aiplex up                         # boots http://localhost:8080
  aiplex up --port 9000             # alternate port
  aiplex up --no-browser            # skip auto-launch
  aiplex up --data-dir ./aiplex     # state in ./aiplex instead of ~/.aiplex`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if dataDir == "" {
				home, _ := os.UserHomeDir()
				dataDir = filepath.Join(home, ".aiplex")
			}
			if err := os.MkdirAll(dataDir, 0o700); err != nil {
				return fmt.Errorf("create data dir: %w", err)
			}

			// Local auth signer — mints + verifies caps-bearing JWTs.
			signer, err := auth.NewLocalSigner(filepath.Join(dataDir, "local.key"), "aiplex://local")
			if err != nil {
				return fmt.Errorf("init local signer: %w", err)
			}

			// In-memory store. (sqlite persistence is the next iteration.)
			store := registry.NewMemoryStore()
			ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer cancel()

			// Seed every built-in catalog source so the node has something to
			// browse and deploy out of the box.
			seedCatalog(ctx, store)

			// Default user identity. Anyone running the binary IS the user;
			// we mint a token granting wildcard caps so curl/SDK work without
			// per-cap setup. Production deployments would never do this.
			subject := localUserSubject()
			adminCaps := wildcardCapsForKinds()
			if err := store.SetUserCaps(ctx, subject, adminCaps); err != nil {
				return fmt.Errorf("seed user caps: %w", err)
			}

			// Build the same router as cmd/aiplex-api/main.go but with local
			// signer in front of every protected route, and the broker +
			// workflow executor mounted directly.
			r, broker, wfExec := buildLocalRouter(ctx, store, signer, port)
			_ = broker
			_ = wfExec

			// Mint a fresh admin token and persist it to dataDir so other
			// CLI commands (aiplex list, aiplex deploy, …) Just Work without
			// `aiplex login`.
			token, err := signer.Mint(subject, adminCaps, "", 30*24*time.Hour)
			if err != nil {
				return fmt.Errorf("mint local token: %w", err)
			}
			tokenPath := filepath.Join(dataDir, "token")
			if err := os.WriteFile(tokenPath, []byte(token), 0o600); err != nil {
				return fmt.Errorf("persist local token: %w", err)
			}

			addr := fmt.Sprintf("127.0.0.1:%d", port)
			srv := &http.Server{Addr: addr, Handler: r, ReadHeaderTimeout: 10 * time.Second}

			ready := make(chan struct{})
			var wg sync.WaitGroup
			wg.Add(1)
			go func() {
				defer wg.Done()
				close(ready)
				if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					fmt.Fprintf(os.Stderr, "server: %v\n", err)
				}
			}()

			<-ready
			// Print the welcome banner; this is the experience.
			printUpBanner(addr, dataDir, subject, len(adminCaps))

			if !noBrowser {
				url := fmt.Sprintf("http://%s/", addr)
				_ = openBrowser(url)
			}

			<-ctx.Done()
			fmt.Println("\nShutting down…")
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer shutdownCancel()
			_ = srv.Shutdown(shutdownCtx)
			wg.Wait()
			_ = noOpenCap
			return nil
		},
	}

	cmd.Flags().IntVar(&port, "port", 8080, "Port to listen on (localhost only)")
	cmd.Flags().StringVar(&dataDir, "data-dir", "", "Where to store key, token, and state (default ~/.aiplex)")
	cmd.Flags().BoolVar(&noBrowser, "no-browser", false, "Skip opening the Console in a browser")
	cmd.Flags().BoolVar(&noOpenCap, "no-open-cap", false, "(reserved)")
	return cmd
}

// buildLocalRouter wires the same handlers cmd/aiplex-api uses, but with
// local-signer auth and in-process memory + workflow.
func buildLocalRouter(ctx context.Context, store registry.Store, signer *auth.LocalSigner, port int) (*chi.Mux, *memplex.Broker, *workflow.Executor) {
	// Catalog aggregator — every kind has a local source.
	sources := []catalog.Source{
		catalog.NewLocalSource(store, capability.KindTool),
		catalog.NewLocalSource(store, capability.KindTask),
		catalog.NewLocalSource(store, capability.KindModel),
		catalog.NewLocalSource(store, capability.KindSkill),
		catalog.NewLocalSource(store, capability.KindMemory),
		catalog.NewLocalSource(store, capability.KindAgent),
		catalog.NewLocalSource(store, capability.KindWorkflow),
		catalog.NewBuiltInProviders(),
		catalog.NewBuiltInSkills(),
		catalog.NewBuiltInMemory(),
		catalog.NewBuiltInAgents(),
		catalog.NewBuiltInWorkflows(),
	}
	agg := catalog.NewAggregator(sources)

	engine := deploy.NewEngine(store, "local.aiplex")

	memBroker := memplex.NewBroker(memplex.NewLocalBackend())
	engine.RegisterKindHook(capability.KindMemory, memBroker)

	gatewayURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	wfExec := workflow.NewExecutor(workflow.NewHTTPInvoker(gatewayURL), 50)
	engine.RegisterKindHook(capability.KindWorkflow, wfExec)
	wfServer := workflow.NewServer(wfExec)

	catalogH := api.NewCatalogHandler(agg, store)
	instanceH := api.NewInstanceHandler(store, engine)
	agentH := api.NewAgentHandler(store)
	dashH := api.NewDashboardHandler(store)

	r := chi.NewRouter()
	r.Use(api.RequestID)
	r.Use(api.Logger)

	// Public endpoints.
	r.Get("/healthz", api.Health)
	r.Get("/auth/jwks", jwksHandler(signer))

	// Protected endpoints — local-mode signer verifies the bearer.
	r.Group(func(r chi.Router) {
		r.Use(localAuthMiddleware(signer))
		r.Route("/api/v1", func(r chi.Router) {
			r.Get("/catalog", catalogH.List)
			r.Get("/catalog/{id}", catalogH.Get)
			r.Get("/instances", instanceH.List)
			r.Post("/instances", instanceH.Deploy)
			r.Get("/instances/{id}", instanceH.Get)
			r.Delete("/instances/{id}", instanceH.Undeploy)
			r.Get("/instances/{id}/history", instanceH.History)
			r.Get("/agents", agentH.List)
			r.Post("/agents", agentH.Register)
			r.Get("/agents/{clientId}", agentH.Get)
			r.Delete("/agents/{clientId}", agentH.Delete)
			r.Get("/dashboard/stats", dashH.GetStats)
			r.Get("/dashboard/denials", dashH.ListPolicyDenials)
		})
		r.Mount("/cap/memory/", memBroker)
		r.Mount("/cap/workflow/", wfServer)
	})

	// Console — served from the embedded React build at the root path.
	dist, err := fs.Sub(console.Dist, "dist")
	if err == nil {
		r.Handle("/*", http.FileServer(http.FS(dist)))
	}

	return r, memBroker, wfExec
}

// seedCatalog populates the in-memory store with every built-in template so
// the catalog has something useful right out of the box.
func seedCatalog(ctx context.Context, store registry.Store) {
	for _, src := range []catalog.Source{
		catalog.NewBuiltInProviders(),
		catalog.NewBuiltInSkills(),
		catalog.NewBuiltInMemory(),
		catalog.NewBuiltInAgents(),
		catalog.NewBuiltInWorkflows(),
	} {
		templates, err := src.Fetch(ctx)
		if err != nil {
			continue
		}
		for i := range templates {
			store.PutTemplate(ctx, &templates[i])
		}
	}
}

// localUserSubject returns the subject to use for the local-mode user.
// "{os-user}@local" so multi-user laptops don't collide.
func localUserSubject() string {
	if u := os.Getenv("USER"); u != "" {
		return u + "@local"
	}
	return "local@local"
}

// wildcardCapsForKinds grants the local user a wildcard cap per kind so
// tool/model/memory/etc calls all resolve. In production this is replaced
// by per-user grants from Hydra consent.
func wildcardCapsForKinds() capability.CapSet {
	out := make(capability.CapSet, 0, len(capability.AllKinds()))
	for _, k := range capability.AllKinds() {
		out = append(out, capability.Cap{
			URI: fmt.Sprintf("cap://%s/*@v1", k),
			// Empty Actions means "all actions of this kind" — see capability.Cap.Allows.
		})
	}
	return out
}

// printUpBanner writes the run banner to stdout. This IS the user
// experience for `aiplex up`; treat the layout with care.
func printUpBanner(addr, dataDir, subject string, capCount int) {
	fmt.Println()
	fmt.Println("  ┌─────────────────────────────────────────────────────────────┐")
	fmt.Printf("  │  AIPlex node ready at  %-37s│\n", "http://"+addr)
	fmt.Println("  ├─────────────────────────────────────────────────────────────┤")
	fmt.Printf("  │  user      %-49s│\n", subject)
	fmt.Printf("  │  caps      %-49s│\n", fmt.Sprintf("%d wildcard grants (one per kind)", capCount))
	fmt.Printf("  │  data dir  %-49s│\n", dataDir)
	fmt.Printf("  │  token     %-49s│\n", filepath.Join(dataDir, "token"))
	fmt.Println("  └─────────────────────────────────────────────────────────────┘")
	fmt.Println()
	fmt.Println("  Try:")
	fmt.Printf("    AIPLEX_URL=http://%s AIPLEX_TOKEN=$(cat %s) aiplex catalog list\n",
		addr, filepath.Join(dataDir, "token"))
	fmt.Printf("    aiplex demo                           # deploy the Ember tutor scenario\n")
	fmt.Println()
	fmt.Println("  Press Ctrl-C to stop.")
	fmt.Println()
}

// localAuthMiddleware verifies the Authorization: Bearer <token> header
// against the local signer. On success it stamps the subject into a header
// downstream handlers can read. Bypassed for /healthz and /auth/jwks.
func localAuthMiddleware(s *auth.LocalSigner) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authz := r.Header.Get("Authorization")
			if authz == "" {
				http.Error(w, "missing Authorization header", http.StatusUnauthorized)
				return
			}
			if len(authz) < 8 || authz[:7] != "Bearer " {
				http.Error(w, "expected Bearer token", http.StatusUnauthorized)
				return
			}
			claims, err := s.Verify(authz[7:])
			if err != nil {
				http.Error(w, "invalid token: "+err.Error(), http.StatusUnauthorized)
				return
			}
			// Stamp the subject so handlers (extractOwner, etc.) see it.
			r.Header.Set("X-AIPlex-Caller", claims.Subject)
			r.Header.Set("X-AIPlex-User", claims.Subject)
			next.ServeHTTP(w, r)
		})
	}
}

// jwksHandler exposes the local signer's public key so external verifiers
// (e.g. another AIPlex node accepting federated tokens) can validate
// locally-issued tokens without a roundtrip.
func jwksHandler(s *auth.LocalSigner) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"keys":[{"kty":"OKP","crv":"Ed25519","kid":%q,"x":%q,"alg":"EdDSA"}]}`,
			s.KeyID(), encodeURLBase64(s.PublicKey()))
	}
}

func encodeURLBase64(b []byte) string {
	const enc = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	out := make([]byte, ((len(b)+2)/3)*4)
	di, si := 0, 0
	n := (len(b) / 3) * 3
	for si < n {
		val := uint(b[si])<<16 | uint(b[si+1])<<8 | uint(b[si+2])
		out[di] = enc[val>>18&0x3F]
		out[di+1] = enc[val>>12&0x3F]
		out[di+2] = enc[val>>6&0x3F]
		out[di+3] = enc[val&0x3F]
		si += 3
		di += 4
	}
	rem := len(b) - si
	if rem == 0 {
		return string(out[:di])
	}
	val := uint(b[si]) << 16
	if rem == 2 {
		val |= uint(b[si+1]) << 8
	}
	out[di] = enc[val>>18&0x3F]
	out[di+1] = enc[val>>12&0x3F]
	if rem == 2 {
		out[di+2] = enc[val>>6&0x3F]
		di += 3
	} else {
		di += 2
	}
	return string(out[:di])
}
