package main

import (
	"context"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/vamsiramakrishnan/aiplex/internal/config"
	"github.com/vamsiramakrishnan/aiplex/pkg/telemetry"
)

// version is set at build time (-ldflags "-X main.version=..."); it is the
// telemetry service.version.
var version = "dev"

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	ctx = watchParent(ctx)

	// Config
	cfg, err := config.Load(ctx)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load config")
	}

	// Logging
	level, _ := zerolog.ParseLevel(cfg.LogLevel)
	zerolog.SetGlobalLevel(level)
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339})

	// Telemetry: OTLP export only when OTEL_EXPORTER_OTLP_ENDPOINT (or a
	// per-signal endpoint) is set; trace identifiers and /metrics always.
	tel, err := telemetry.Setup(ctx, telemetry.Options{ServiceVersion: version})
	if err != nil {
		log.Fatal().Err(err).Msg("telemetry configuration failed")
	}
	defer func() {
		flush, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tel.Shutdown(flush)
	}()
	log.Info().Bool("traces_exported", tel.TracesExported).Bool("metrics_exported", tel.MetricsExported).Bool("upstream_trace_propagation", tel.PropagateUpstream).Msg("telemetry configured")

	app, err := newServer(ctx, cfg, serverOptions{metrics: tel.MetricsHandler()})
	if err != nil {
		log.Fatal().Err(err).Msg("server configuration failed")
	}
	defer app.close()

	// Start server
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	tlsConfig, err := serverTLS(cfg)
	if err != nil {
		log.Fatal().Err(err).Msg("TLS listener configuration failed")
	}
	srv := &http.Server{TLSConfig: tlsConfig, Addr: addr, Handler: app.handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 64 << 10}

	go func() {
		log.Info().Str("addr", addr).Msg("aiplex-api starting")
		serve := srv.ListenAndServe
		if cfg.TLSCertFile != "" {
			serve = func() error { return srv.ListenAndServeTLS(cfg.TLSCertFile, cfg.TLSKeyFile) }
		}
		if err := serve(); err != nil && err != http.ErrServerClosed {
			log.Fatal().Err(err).Msg("server failed")
		}
	}()

	<-ctx.Done()
	log.Info().Msg("shutting down")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	srv.Shutdown(shutdownCtx)
}

// runsActionFromPath extracts the operator action name from a /runs/{id}/{action}
// path for idempotency caching. Returns "" if the path doesn't match.
func runsActionFromPath(r *http.Request) string {
	path := r.URL.Path
	// Last segment of /api/v1/runs/{id}/{action}
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		return path[i+1:]
	}
	return ""
}

func parseFloatEnv(name string, def float64) float64 {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	n, err := strconv.ParseFloat(v, 64)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

func parseIntEnv(name string, def int) int {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

func parseDurationEnv(name string, def time.Duration) time.Duration {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return def
	}
	return d
}

// watchParent stops the server when the process that started it goes away.
// `aiplex dev` sets AIPLEX_EXIT_WITH_PARENT=<its pid> so a local API never
// outlives a killed `aiplex dev` (SIGKILL cannot be trapped, and macOS has no
// parent-death signal); the API is re-parented and notices within a second.
func watchParent(ctx context.Context) context.Context {
	want, err := strconv.Atoi(os.Getenv("AIPLEX_EXIT_WITH_PARENT"))
	if err != nil || want <= 1 {
		return ctx
	}
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if os.Getppid() != want {
					log.Warn().Int("parent", want).Msg("parent process exited; shutting down")
					cancel()
					return
				}
			}
		}
	}()
	return ctx
}
