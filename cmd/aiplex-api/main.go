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
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Config
	cfg, err := config.Load(ctx)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load config")
	}

	// Logging
	level, _ := zerolog.ParseLevel(cfg.LogLevel)
	zerolog.SetGlobalLevel(level)
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339})

	app, err := newServer(ctx, cfg, serverOptions{})
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
	srv := &http.Server{TLSConfig: tlsConfig, Addr: addr, Handler: app.handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 90 * time.Second}

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
