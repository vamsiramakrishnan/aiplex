package devstack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Identity of the local developer. The group is bound to the admin role by the
// role-binding file the stack writes, so the developer token is an admin token
// for this local API only.
const (
	DevSubject   = "developer"
	DevEmail     = "developer@localhost"
	DevName      = "Local developer"
	DevGroup     = "aiplex-local-admins"
	DevAudience  = "aiplex-local"
	DevPoolID    = "aiplex-local"
	DevTenant    = "development"
	DevTokenTTL  = 12 * time.Hour
	adminBinding = "local-admin"
)

// DevScopes are granted to the developer group: every model, tool and agent
// action on this local API.
var DevScopes = []string{"llm:*", "mcp:*", "a2a:*", "skill:*", "aiplex:*"}

// Options configures a local stack.
type Options struct {
	// APIBinary is the aiplex-api executable to run.
	APIBinary string
	// Port for the API; 0 picks a free loopback port.
	Port int
	// Dir holds generated, secret-free configuration files. Empty creates a
	// temporary directory that Stop removes.
	Dir string
	// Provider selects the upstream wired for the default surface: "mock"
	// (default), "openai", "anthropic" or "gemini".
	Provider string
	// APIKeyEnv names the environment variable holding the real provider key.
	// The value is handed to the API process through its environment only.
	APIKeyEnv string
	// Log receives the API process output. Nil discards it.
	Log io.Writer
	// Env is extra environment for the API process (KEY=VALUE).
	Env []string
	// UpstreamTimeout bounds outbound connects and response headers (default
	// 15s, the endpoint policy default).
	UpstreamTimeout time.Duration
	// ConsoleOrigins are the browser origins allowed by CORS (CONSOLE_ORIGINS),
	// set explicitly so the setup does not rely on the development wildcard.
	ConsoleOrigins []string
	// ExtraOrigins are additional loopback or HTTPS origins the outbound
	// policy approves (for test upstreams).
	ExtraOrigins []string
}

// Stack is a running local stack.
type Stack struct {
	Issuer *Issuer
	Models *MockModels
	// ModelsB is a second, independent mock provider (its replies are
	// prefixed "[b]"), so routing can fall over from one backend to another.
	ModelsB    *MockModels
	ModelsBURL string
	MCP        *MockMCP
	APIURL     string
	ModelsURL  string
	MCPURL    string
	IssuerURL string
	Dir       string
	Token     string

	opts      Options
	ownDir    bool
	servers   []*http.Server
	cmd       *exec.Cmd
	exited    chan struct{}
	exitErr   error
	exitMu    sync.Mutex
	providerE string // env var the API reads the provider secret from
	env       []string
}

// Listen starts an HTTP server on a free loopback port and returns its URL.
func Listen(h http.Handler) (*http.Server, string, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, "", err
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(l) }()
	return srv, "http://" + l.Addr().String(), nil
}

// FreePort returns a currently unused loopback port.
func FreePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// ProviderOrigin is the HTTPS origin of a real provider.
func ProviderOrigin(provider string) (string, error) {
	switch provider {
	case "openai":
		return "https://api.openai.com", nil
	case "anthropic":
		return "https://api.anthropic.com", nil
	case "gemini":
		return "https://generativelanguage.googleapis.com", nil
	}
	return "", fmt.Errorf("unknown provider %q (use mock, openai, anthropic or gemini)", provider)
}

// Start boots the mock upstreams and token issuer, writes configuration and
// starts the API process. It does not seed resources; see Seed.
func Start(ctx context.Context, opts Options) (*Stack, error) {
	if opts.Provider == "" {
		opts.Provider = "mock"
	}
	if opts.Provider != "mock" {
		if _, err := ProviderOrigin(opts.Provider); err != nil {
			return nil, err
		}
		if opts.APIKeyEnv == "" {
			return nil, fmt.Errorf("--provider %s needs --api-key-env NAME (the environment variable that holds your %s API key)", opts.Provider, opts.Provider)
		}
		if os.Getenv(opts.APIKeyEnv) == "" {
			return nil, fmt.Errorf("environment variable %s is empty or unset; export your %s API key in it first", opts.APIKeyEnv, opts.Provider)
		}
	}
	if opts.APIBinary == "" {
		return nil, fmt.Errorf("API binary not set")
	}
	s := &Stack{opts: opts, Models: &MockModels{}, ModelsB: &MockModels{Name: "b"}, MCP: &MockMCP{}, exited: make(chan struct{})}
	ok := false
	defer func() {
		if !ok {
			s.Stop()
		}
	}()
	var err error
	if opts.Dir == "" {
		if s.Dir, err = os.MkdirTemp("", "aiplex-dev-"); err != nil {
			return nil, err
		}
		s.ownDir = true
	} else {
		s.Dir = opts.Dir
		if err = os.MkdirAll(s.Dir, 0o700); err != nil {
			return nil, err
		}
	}
	if s.Issuer, err = NewIssuer("", DevAudience); err != nil {
		return nil, err
	}
	for _, srv := range []struct {
		h   http.Handler
		dst *string
	}{{s.issuerHandler(), &s.IssuerURL}, {s.Models, &s.ModelsURL}, {s.ModelsB, &s.ModelsBURL}, {s.MCP, &s.MCPURL}} {
		server, url, e := Listen(srv.h)
		if e != nil {
			return nil, e
		}
		s.servers = append(s.servers, server)
		*srv.dst = url
	}
	s.Issuer.SetIssuer(s.IssuerURL)

	port := opts.Port
	if port == 0 {
		if port, err = FreePort(); err != nil {
			return nil, err
		}
	}
	if err = portFree(port); err != nil {
		return nil, err
	}
	s.APIURL = "http://127.0.0.1:" + strconv.Itoa(port)

	connectionsFile, err := s.writeConnections()
	if err != nil {
		return nil, err
	}
	bindingsFile := filepath.Join(s.Dir, "role-bindings.json")
	bindings := map[string]any{"role_bindings": []map[string]any{{
		"id": adminBinding, "group": DevGroup, "role": "admin", "provider": s.IssuerURL, "pool_id": DevPoolID,
		"scopes": DevScopes, "description": "Local developer (aiplex dev)",
	}, {
		"id": "local-deployer", "group": DevDeployerGroup, "role": "deployer", "provider": s.IssuerURL, "pool_id": DevPoolID,
		"scopes": DevScopes, "description": "Local deployer (aiplex dev token --role deployer)",
	}}}
	if err = writeJSONFile(bindingsFile, bindings); err != nil {
		return nil, err
	}

	env := filterEnv(os.Environ())
	env = append(env,
		"AIPLEX_DEV_MODE=true",
		"HOST=127.0.0.1",
		"PORT="+strconv.Itoa(port),
		"JWT_ISSUER="+s.IssuerURL,
		"JWT_AUDIENCE="+DevAudience,
		"JWT_JWKS_URL="+s.Issuer.JWKSURL(),
		"WORKFORCE_POOL_ID="+DevPoolID,
		"IDENTITY_WORKFORCE=true",
		"AIPLEX_ROLE_BINDINGS_FILE="+bindingsFile,
		"AIPLEX_CONNECTIONS_FILE="+connectionsFile,
		"AIPLEX_PUBLIC_URL="+s.APIURL,
		"AIPLEX_TENANT_ID="+DevTenant,
		"LOG_LEVEL=info",
	)
	if s.providerE != "" {
		// Hand the key to the API process only, under the development secret
		// name its secret reference resolves from. Never written to disk.
		env = append(env, s.providerE+"="+os.Getenv(opts.APIKeyEnv))
	}
	if opts.Provider == "mock" {
		// The extra mock surfaces need a credential reference; the mock accepts
		// any value, so this is a placeholder and not a secret.
		env = append(env, "AIPLEX_TOOL_SECRET_"+strings.ToUpper(strings.ReplaceAll(MockSecretRef, "-", "_"))+"=mock-credential-not-a-secret")
	}
	if len(opts.ConsoleOrigins) > 0 {
		env = append(env, "CONSOLE_ORIGINS="+strings.Join(opts.ConsoleOrigins, ","))
	}
	env = append(env, opts.Env...)
	s.env = env
	if err = s.launch(); err != nil {
		return nil, err
	}
	if err = s.WaitReady(ctx, 60*time.Second); err != nil {
		return nil, err
	}
	if s.Token, err = s.MintDeveloperToken(); err != nil {
		return nil, err
	}
	ok = true
	return s, nil
}

func (s *Stack) launch() error {
	s.cmd = exec.Command(s.opts.APIBinary)
	// The API exits when this process does, even if this process is killed
	// with SIGKILL (see watchParent in cmd/aiplex-api).
	s.cmd.Env = append(append([]string{}, s.env...), "AIPLEX_EXIT_WITH_PARENT="+strconv.Itoa(os.Getpid()))
	s.cmd.Dir = s.Dir
	out := s.opts.Log
	if out == nil {
		out = io.Discard
	}
	s.cmd.Stdout, s.cmd.Stderr = out, out
	if err := s.cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", s.opts.APIBinary, err)
	}
	exited := make(chan struct{})
	s.exited = exited
	cmd := s.cmd
	go func() {
		e := cmd.Wait()
		s.exitMu.Lock()
		s.exitErr = e
		s.exitMu.Unlock()
		close(exited)
	}()
	return nil
}

// Restart stops the API process and starts it again with the same
// configuration (same port, issuer and upstreams). In-memory state is lost.
func (s *Stack) Restart(ctx context.Context) error {
	s.StopAPI()
	if err := s.launch(); err != nil {
		return err
	}
	return s.WaitReady(ctx, 60*time.Second)
}

func portFree(port int) error {
	l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		return fmt.Errorf("port %d on 127.0.0.1 is already in use; stop what is using it or pass --port", port)
	}
	return l.Close()
}

// filterEnv drops variables that would change what the local API connects to
// (for example a cloud project that switches the store to Firestore).
func filterEnv(in []string) []string {
	drop := map[string]bool{"GCP_PROJECT": true, "GCP_PROJECT_ID": true, "FIRESTORE_PROJECT_ID": true, "FIRESTORE_DATABASE": true, "HYDRA_ADMIN_URL": true, "AIPLEX_TENANT_ID": true, "AIPLEX_ROLE_BINDINGS_FILE": true, "AIPLEX_CONNECTIONS_FILE": true, "JWT_ISSUER": true, "JWT_AUDIENCE": true, "JWT_JWKS_URL": true, "FIRESTORE_EMULATOR_HOST": true, "KUBERNETES_SERVICE_HOST": true, "AIPLEX_IDENTITY_PROVIDERS_FILE": true, "AIPLEX_QUOTA_FILE": true, "AIPLEX_MODULE_MANIFEST": true, "TLS_CERT_FILE": true, "TLS_KEY_FILE": true, "CLIENT_CA_FILE": true, "CONSOLE_ORIGINS": true, "AIPLEX_MODULES": true, "AIPLEX_EXIT_WITH_PARENT": true}
	out := make([]string, 0, len(in))
	for _, kv := range in {
		name, _, _ := strings.Cut(kv, "=")
		if drop[name] {
			continue
		}
		out = append(out, kv)
	}
	return out
}

func writeJSONFile(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// ProviderSecretRef is the secret reference the stack's real-provider surface
// uses; in development mode it resolves from AIPLEX_TOOL_SECRET_<REF>.
const ProviderSecretRef = "dev-provider-key"

func (s *Stack) writeConnections() (string, error) {
	origins := []string{s.ModelsURL, s.ModelsBURL, s.MCPURL, s.IssuerURL}
	origins = append(origins, s.opts.ExtraOrigins...)
	if s.opts.Provider != "mock" {
		origin, _ := ProviderOrigin(s.opts.Provider)
		origins = append(origins, origin)
		s.providerE = "AIPLEX_TOOL_SECRET_" + strings.ToUpper(strings.ReplaceAll(ProviderSecretRef, "-", "_"))
	}
	network := map[string]any{"allowed_origins": origins, "allow_loopback": true}
	if s.opts.UpstreamTimeout > 0 {
		network["timeout"] = int64(s.opts.UpstreamTimeout)
	}
	path := filepath.Join(s.Dir, "connections.json")
	return path, writeJSONFile(path, map[string]any{"network": network, "identities": []any{}})
}

// MintDeveloperToken returns an admin token for the local developer.
func (s *Stack) MintDeveloperToken() (string, error) {
	return s.Issuer.Mint(map[string]any{"sub": DevSubject, "email": DevEmail, "name": DevName, "groups": []string{DevGroup}}, DevTokenTTL)
}

// WaitReady polls /readyz until it answers 200 or the API process exits.
func (s *Stack) WaitReady(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second}
	for {
		select {
		case <-s.exited:
			return fmt.Errorf("aiplex-api exited during startup (%v); see its log for the reason", s.ExitErr())
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		resp, err := client.Get(s.APIURL + "/readyz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("aiplex-api did not become ready at %s within %s", s.APIURL, timeout)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// Exited is closed when the API process exits.
func (s *Stack) Exited() <-chan struct{} { return s.exited }

// ExitErr is the API process exit error, once it has exited.
func (s *Stack) ExitErr() error {
	s.exitMu.Lock()
	defer s.exitMu.Unlock()
	return s.exitErr
}

// StopAPI stops only the API process (SIGTERM, then SIGKILL after 10s).
func (s *Stack) StopAPI() {
	if s.cmd == nil || s.cmd.Process == nil {
		return
	}
	select {
	case <-s.exited:
		return
	default:
	}
	_ = s.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-s.exited:
	case <-time.After(10 * time.Second):
		_ = s.cmd.Process.Kill()
		<-s.exited
	}
}

// Stop stops the API process and the local servers and removes generated
// files the stack created.
func (s *Stack) Stop() {
	s.StopAPI()
	for _, srv := range s.servers {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = srv.Shutdown(ctx)
		cancel()
	}
	if s.ownDir && s.Dir != "" {
		_ = os.RemoveAll(s.Dir)
	}
}

// ErrNotReady is returned by helpers that need a running API.
var ErrNotReady = errors.New("local API is not running")
