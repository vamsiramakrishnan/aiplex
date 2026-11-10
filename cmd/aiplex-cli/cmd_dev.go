package main

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
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/vamsiramakrishnan/aiplex/internal/cliconfig"
	"github.com/vamsiramakrishnan/aiplex/internal/devstack"
)

// devState is what `aiplex dev` records for `aiplex dev env` while it runs.
// It holds no secrets; the token is in the encrypted credentials file.
type devState struct {
	URL         string   `json:"url"`
	Context     string   `json:"context"`
	AccessPoint string   `json:"access_point"`
	Model       string   `json:"model"`
	Protocol    string   `json:"protocol"`
	Provider    string   `json:"provider,omitempty"`
	Models      []string `json:"models,omitempty"`
	PID         int      `json:"pid"`
	Console     string   `json:"console,omitempty"`
	ConsoleNote string   `json:"console_note,omitempty"`
	StartedAt   string   `json:"started_at,omitempty"`
	APILog      string   `json:"api_log,omitempty"`
}

func devStatePath() (string, error) {
	dir, err := cliconfig.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "dev.json"), nil
}

func devCmd() *cobra.Command {
	var (
		port, consolePort int
		provider, keyEnv  string
		model, apiBin     string
		contextName       string
		noConsole, open   bool
		noTools, detach   bool
		allowOrigins      []string
	)
	cmd := &cobra.Command{
		Use:   "dev",
		Short: "Run AIPlex locally with a mock model, a mock MCP server and the Console (no Docker, no cloud)",
		Long: `Starts a local AIPlex in development mode and shows it working end to end.

What runs, all on 127.0.0.1 and in memory (nothing persists after Ctrl+C):
  - aiplex-api in development mode (in-memory stores, loopback only)
  - a local token issuer: you are "developer@localhost" with the admin role
  - a built-in mock model provider with four deterministic models, one per
    wire: mock-gpt (OpenAI Chat Completions), mock-responses (OpenAI
    Responses), mock-claude (Anthropic Messages) and mock-gemini (Gemini
    generateContent), so Claude Code, Codex and the OpenAI, Anthropic and
    Google SDKs all work with no key (or a real provider with --provider and
    --api-key-env, which exposes one model)
  - a built-in MCP server with three tools (echo, add, reset_counter)
  - the Console, when this binary carries a built Console

What is created through the normal APIs:
  - model surfaces "local-models", "local-responses", "local-messages" and
    "local-gemini", access point "local", tool connection "local-tools" and,
    for the mock models, placeholder prices

The CLI context "local" is created and made current while this runs. The
previous context is restored when you stop it.

--detach runs it in the background (output in ~/.aiplex/dev.log); stop it with
"aiplex dev stop" and check it with "aiplex dev status". When aiplex dev stops
or is killed, the local API stops with it.

The local API calls only loopback upstreams and the public OpenAI, Anthropic
and Gemini APIs; allow another origin with --allow-origin https://host.

A real provider key is read from the environment variable you name with
--api-key-env. It is passed only to the local API process's environment; it
is never printed, accepted as a flag value, or written to disk.`,
		Example: `  aiplex dev
  aiplex dev --detach && aiplex dev status
  aiplex dev --provider openai --api-key-env OPENAI_API_KEY
  aiplex dev --provider anthropic --api-key-env ANTHROPIC_API_KEY --model claude-haiku-4-5
  eval "$(aiplex dev env)"    # in another terminal: AIPLEX_URL, AIPLEX_TOKEN, OPENAI_BASE_URL, ...`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			o := devOptions{port: port, consolePort: consolePort, provider: provider, keyEnv: keyEnv, model: model, apiBin: apiBin, contextName: contextName, noConsole: noConsole, open: open, noTools: noTools, allowOrigins: allowOrigins}
			if err := refuseIfDevRunning(); err != nil {
				return err
			}
			if detach {
				return runDevDetached(cmd, o)
			}
			return runDev(cmd.Context(), o)
		},
	}
	cmd.Flags().IntVar(&port, "port", 8080, "API port on 127.0.0.1")
	cmd.Flags().IntVar(&consolePort, "console-port", 9090, "Console port on 127.0.0.1")
	cmd.Flags().StringVar(&provider, "provider", "mock", "Model provider: mock, openai, anthropic or gemini")
	cmd.Flags().StringVar(&keyEnv, "api-key-env", "", "Name of the environment variable that holds the provider API key (not the key itself)")
	cmd.Flags().StringVar(&model, "model", "", "Model to expose (default: mock-gpt, gpt-4o-mini, claude-haiku-4-5 or gemini-2.5-flash)")
	cmd.Flags().StringVar(&apiBin, "api-bin", "", "Path to the aiplex-api binary (default: next to this binary, then PATH; built from source when run in the repository)")
	cmd.Flags().StringVar(&contextName, "context", "local", "CLI context to create for the local API")
	cmd.Flags().BoolVar(&noConsole, "no-console", false, "Do not serve the Console")
	cmd.Flags().BoolVar(&open, "open", false, "Open the Console in a browser")
	cmd.Flags().BoolVar(&noTools, "no-tools", false, "Do not start the built-in MCP server's tool connection")
	cmd.Flags().BoolVarP(&detach, "detach", "d", false, "Run in the background; stop with: aiplex dev stop")
	cmd.Flags().StringSliceVar(&allowOrigins, "allow-origin", nil, "Extra upstream origin the local API may call, e.g. https://mcp.example.com (repeatable)")
	cmd.AddCommand(devEnvCmd(), devStatusCmd(), devStopCmd())
	return cmd
}

type devOptions struct {
	port, consolePort int
	provider, keyEnv  string
	model, apiBin     string
	contextName       string
	noConsole, open   bool
	noTools           bool
	allowOrigins      []string
}

func runDev(parent context.Context, o devOptions) error {
	if parent == nil {
		parent = context.Background()
	}
	if strings.Contains(o.keyEnv, "=") || (o.keyEnv != "" && !validEnvName(o.keyEnv)) {
		return fmt.Errorf("--api-key-env takes the NAME of an environment variable (for example OPENAI_API_KEY), not a key")
	}
	if o.provider == "mock" && o.keyEnv != "" {
		return fmt.Errorf("--api-key-env applies to a real provider; add --provider openai, anthropic or gemini")
	}
	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()

	bin, err := findAPIBinary(ctx, o.apiBin)
	if err != nil {
		return err
	}
	logPath, logFile, err := devLogFile()
	if err != nil {
		return err
	}
	defer logFile.Close()

	fmt.Println()
	fmt.Println("  AIPlex local development")
	fmt.Println()
	started := time.Now()
	var origins []string
	if !o.noConsole {
		p := strconv.Itoa(o.consolePort)
		origins = []string{"http://127.0.0.1:" + p, "http://localhost:" + p}
	}
	extra, err := devAllowedOrigins(o.allowOrigins)
	if err != nil {
		return err
	}
	stack, err := devstack.Start(ctx, devstack.Options{APIBinary: bin, Port: o.port, Provider: o.provider, APIKeyEnv: o.keyEnv, Log: logFile, ConsoleOrigins: origins, ExtraOrigins: extra})
	if err != nil {
		return fmt.Errorf("%w\n  API log: %s", err, logPath)
	}
	defer stack.Stop()
	seeded, err := stack.Seed(ctx, devstack.SeedOptions{Provider: o.provider, Model: o.model, NoTools: o.noTools})
	if err != nil {
		return fmt.Errorf("seed local resources: %w\n  API log: %s", err, logPath)
	}

	previous, err := useDevContext(o.contextName, stack.APIURL, stack.Token)
	if err != nil {
		return err
	}
	defer restoreContext(o.contextName, previous)

	consoleURL := ""
	consoleNote := ""
	if !o.noConsole {
		consoleURL, consoleNote = startDevConsole(ctx, stack.APIURL, stack.Token, o.consolePort)
	}
	statePath, _ := devStatePath()
	state := devState{URL: stack.APIURL, Context: o.contextName, AccessPoint: seeded.AccessPoint, Model: seeded.Model, Protocol: seeded.Protocol, Provider: seeded.Provider, Models: seededModelNames(seeded), PID: os.Getpid(), Console: consoleURL, ConsoleNote: consoleNote, StartedAt: time.Now().UTC().Format(time.RFC3339), APILog: logPath}
	if raw, e := json.MarshalIndent(state, "", "  "); e == nil && statePath != "" {
		_ = os.WriteFile(statePath, raw, 0o600)
		defer os.Remove(statePath)
	}

	printDevSummary(stack, seeded, o, previous, consoleURL, consoleNote, logPath, time.Since(started), os.Getenv(devDetachedEnv) != "")
	if consoleURL != "" && o.open {
		_ = openBrowser(consoleURL)
	}

	select {
	case <-ctx.Done():
		fmt.Println()
		fmt.Println("  Stopping...")
	case <-stack.Exited():
		return fmt.Errorf("aiplex-api exited unexpectedly (%v); see %s", stack.ExitErr(), logPath)
	}
	return nil
}

func validEnvName(name string) bool {
	for i, r := range name {
		if !(r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (i > 0 && r >= '0' && r <= '9')) {
			return false
		}
	}
	return name != ""
}

func devLogFile() (string, *os.File, error) {
	dir, err := cliconfig.Dir()
	if err != nil {
		return "", nil, err
	}
	path := filepath.Join(dir, "dev-api.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return "", nil, err
	}
	return path, f, nil
}

// findAPIBinary resolves aiplex-api: the flag, AIPLEX_API_BIN, next to this
// executable, a fresh build when run inside the source repository, then PATH.
func findAPIBinary(ctx context.Context, flag string) (string, error) {
	if flag != "" {
		return requireExecutable(flag)
	}
	if env := os.Getenv("AIPLEX_API_BIN"); env != "" {
		return requireExecutable(env)
	}
	name := "aiplex-api"
	if exe, err := os.Executable(); err == nil {
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
		sibling := filepath.Join(filepath.Dir(exe), name)
		if _, err := os.Stat(sibling); err == nil {
			return sibling, nil
		}
	}
	if root := sourceRoot(); root != "" {
		if _, err := exec.LookPath("go"); err == nil {
			dir, err := cliconfig.Dir()
			if err != nil {
				return "", err
			}
			out := filepath.Join(dir, "bin", name)
			fmt.Println("  Building aiplex-api from", root, "...")
			build := exec.CommandContext(ctx, "go", "build", "-o", out, "./cmd/aiplex-api")
			build.Dir = root
			build.Stdout, build.Stderr = os.Stderr, os.Stderr
			if err := build.Run(); err != nil {
				return "", fmt.Errorf("build aiplex-api: %w", err)
			}
			return out, nil
		}
	}
	if path, err := exec.LookPath(name); err == nil {
		return path, nil
	}
	return "", fmt.Errorf("aiplex-api not found. Install it next to the aiplex CLI (release archives include it), run `make build` in the source repository, or pass --api-bin PATH")
}

// findAPIBinaryQuiet locates aiplex-api without building it.
func findAPIBinaryQuiet() (string, error) {
	if env := os.Getenv("AIPLEX_API_BIN"); env != "" {
		return requireExecutable(env)
	}
	name := "aiplex-api"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if exe, err := os.Executable(); err == nil {
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
		sibling := filepath.Join(filepath.Dir(exe), name)
		if _, err := os.Stat(sibling); err == nil {
			return sibling, nil
		}
	}
	return exec.LookPath(name)
}

func requireExecutable(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return "", fmt.Errorf("aiplex-api binary %q not found", path)
	}
	return path, nil
}

// sourceRoot returns the repository root when the working directory is inside
// the AIPlex source tree.
func sourceRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err == nil {
			if strings.Contains(string(data), "module github.com/vamsiramakrishnan/aiplex\n") {
				if _, err := os.Stat(filepath.Join(dir, "cmd", "aiplex-api")); err == nil {
					return dir
				}
			}
			return ""
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// useDevContext creates the local context with its token and makes it
// current. It returns the previously current context.
func useDevContext(name, url, tok string) (string, error) {
	cfg, err := cliconfig.Load()
	if err != nil {
		return "", fmt.Errorf("load CLI config: %w", err)
	}
	previous := cfg.CurrentContext
	if previous == name {
		previous = ""
	}
	cfg.SetContext(name, url, "", "", "")
	if err := cfg.Save(); err != nil {
		return "", fmt.Errorf("save CLI config: %w", err)
	}
	creds, err := cliconfig.LoadCredentials()
	if err != nil {
		return "", fmt.Errorf("load CLI credentials: %w", err)
	}
	creds.SetToken(name, &cliconfig.TokenEntry{AccessToken: tok, TokenType: "bearer", ExpiresAt: time.Now().Add(devstack.DevTokenTTL).UTC().Format(time.RFC3339)})
	if err := creds.Save(); err != nil {
		return "", fmt.Errorf("save CLI credentials: %w", err)
	}
	return previous, nil
}

// restoreContext switches back to the context that was current before
// `aiplex dev` and removes the local context's token, which is useless once
// the local issuer's key is gone.
func restoreContext(name, previous string) {
	if creds, err := cliconfig.LoadCredentials(); err == nil {
		delete(creds.Tokens, name)
		_ = creds.Save()
	}
	cfg, err := cliconfig.Load()
	if err != nil {
		return
	}
	if previous != "" {
		if _, ok := cfg.Contexts[previous]; ok && cfg.CurrentContext == name {
			cfg.CurrentContext = previous
			if cfg.Save() == nil {
				fmt.Printf("  Restored CLI context %q.\n", previous)
			}
		}
	}
}

func startDevConsole(ctx context.Context, apiURL, tok string, port int) (string, string) {
	if !consoleBuilt() {
		return "", "not in this binary (a source build without Node). Release binaries include it;\n                 from source: make console && make build (Node 22), then restart aiplex dev"
	}
	handler, err := consoleHandler(apiURL, tok)
	if err != nil {
		return "", err.Error()
	}
	l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		return "", fmt.Sprintf("port %d is in use; pass --console-port", port)
	}
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(l) }()
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()
	return "http://" + l.Addr().String(), ""
}

func printDevSummary(s *devstack.Stack, seeded *devstack.Seeded, o devOptions, previous, consoleURL, consoleNote, logPath string, took time.Duration, detached bool) {
	row := func(label, value string) { fmt.Printf("  %-14s %s\n", label, value) }
	row("API", s.APIURL+"  (development mode, in memory, loopback only)")
	row("You", devstack.DevEmail+"  (admin; token from a local issuer, valid 12h)")
	switch seeded.Provider {
	case "mock":
		names, wires := make([]string, 0, len(seeded.Models)), make([]string, 0, len(seeded.Models))
		for _, m := range seeded.Models {
			names = append(names, m.Name)
			wires = append(wires, devWireLabel(m.Protocol))
		}
		row("Models", strings.Join(names, ", ")+"  (built-in mock provider, no API key)")
		row("", "wires: "+strings.Join(wires, ", "))
	default:
		row("Model", seeded.Model+"  via "+seeded.Provider+" (key from $"+o.keyEnv+")")
	}
	if seeded.Connection != "" {
		row("Tools", seeded.Connection+": "+strings.Join(seeded.Tools, ", ")+"  (built-in MCP server)")
	}
	row("Access point", seeded.AccessPoint+"  ->  "+s.APIURL+"/ap/"+seeded.AccessPoint)
	if seeded.Price != "" {
		row("Price", "placeholder rates for each mock model, so estimated cost shows in usage")
	} else {
		row("Price", "none (prices are your contracted rates: aiplex usage prices set -f price.json)")
	}
	row("Quota", "off (optional: QuotaPlex reads an operator policy file named by AIPLEX_QUOTA_FILE)")
	ctxNote := "\"" + o.contextName + "\" (current"
	if previous != "" {
		ctxNote += "; \"" + previous + "\" is restored when you stop"
	}
	row("CLI context", ctxNote+")")
	if consoleURL != "" {
		row("Console", consoleURL)
	} else if !o.noConsole {
		row("Console", consoleNote)
	}
	row("API log", logPath)
	fmt.Println()
	fmt.Println("  WARNING: development mode serves unauthenticated requests as admin; never expose, tunnel or proxy " + strings.TrimPrefix(s.APIURL, "http://") + ".")
	fmt.Printf("\n  Ready in %.1fs. Try next, in another terminal:\n\n", took.Seconds())
	fmt.Println("    # 1. Send a chat request through the access point")
	fmt.Println(`    eval "$(aiplex dev env)"`)
	for _, line := range devTryLines(seeded) {
		fmt.Println("    " + line)
	}
	fmt.Println()
	fmt.Println("    # 2. See what it cost and who made it")
	fmt.Println("    aiplex usage records")
	fmt.Println()
	if consoleURL != "" {
		fmt.Println("    # 3. Open the Console (URL also shown by: aiplex dev status)")
		fmt.Println("    " + openCommand() + " " + consoleURL)
	} else {
		fmt.Println("    # 3. Point an MCP client or IDE at it, and see where the Console is")
		fmt.Println("    aiplex connect " + seeded.AccessPoint)
		fmt.Println("    aiplex dev status")
	}
	fmt.Println()
	if detached {
		fmt.Println("  Running in the background. Stop it with: aiplex dev stop")
	} else {
		fmt.Println("  Press Ctrl+C to stop. State is in memory and is discarded on stop.")
	}
}

// devWireLabel is the short name of a model wire protocol for the summary.
func devWireLabel(protocol string) string {
	switch protocol {
	case "openai-chat":
		return "OpenAI Chat"
	case "openai-responses":
		return "OpenAI Responses"
	case "anthropic-messages":
		return "Anthropic Messages"
	case "google-genai":
		return "Gemini generateContent"
	}
	return protocol
}

// openCommand is the shell command that opens a URL on this OS.
func openCommand() string {
	switch runtime.GOOS {
	case "darwin":
		return "open"
	case "windows":
		return "start"
	}
	return "xdg-open"
}

// devTryLines are the example requests for the seeded wire.
func devTryLines(seeded *devstack.Seeded) []string {
	ap := `"$AIPLEX_URL/ap/` + seeded.AccessPoint
	switch seeded.Protocol {
	case "anthropic-messages":
		return []string{
			"curl -s " + ap + `/v1/messages" \`,
			`  -H "x-api-key: $AIPLEX_TOKEN" -H "anthropic-version: 2023-06-01" -H "Content-Type: application/json" \`,
			`  -d '{"model":"` + seeded.Model + `","max_tokens":256,"messages":[{"role":"user","content":"Hello"}]}'`,
			"# (any Anthropic SDK works too: the eval line sets ANTHROPIC_BASE_URL and ANTHROPIC_API_KEY)",
		}
	case "google-genai":
		return []string{
			"curl -s " + ap + `/v1beta/models/` + seeded.Model + `:generateContent" \`,
			`  -H "x-goog-api-key: $AIPLEX_TOKEN" -H "Content-Type: application/json" \`,
			`  -d '{"contents":[{"role":"user","parts":[{"text":"Hello"}]}]}'`,
		}
	}
	return []string{
		"curl -s " + ap + `/v1/chat/completions" \`,
		`  -H "Authorization: Bearer $AIPLEX_TOKEN" -H "Content-Type: application/json" \`,
		`  -d '{"model":"` + seeded.Model + `","messages":[{"role":"user","content":"Hello"}]}'`,
		"# (any OpenAI SDK works too: the eval line sets OPENAI_BASE_URL and OPENAI_API_KEY)",
	}
}

func devEnvCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "env",
		Short: "Print shell exports for the running `aiplex dev` (use: eval \"$(aiplex dev env)\")",
		Long: `Prints AIPLEX_URL and AIPLEX_TOKEN for the running local API, and the base URL
and key variables that OpenAI and Anthropic SDKs read, pointed at the local
access point. The token is the local developer token; it is accepted only by
the local API that issued it and expires when that API stops.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, err := devStatePath()
			if err != nil {
				return err
			}
			raw, err := os.ReadFile(path)
			if errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("no local AIPlex is running; start one with: aiplex dev")
			}
			if err != nil {
				return err
			}
			var st devState
			if err := json.Unmarshal(raw, &st); err != nil {
				return fmt.Errorf("read %s: %w", path, err)
			}
			if !devReachable(st.URL) {
				return fmt.Errorf("the local AIPlex at %s is not responding; start it with: aiplex dev", st.URL)
			}
			creds, err := cliconfig.LoadCredentials()
			if err != nil {
				return err
			}
			entry := creds.GetToken(st.Context)
			if entry == nil || entry.AccessToken == "" {
				return fmt.Errorf("no token stored for context %q; restart: aiplex dev", st.Context)
			}
			ap := st.URL + "/ap/" + st.AccessPoint
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "export AIPLEX_URL=%s\n", shellQuote(st.URL))
			fmt.Fprintf(w, "export AIPLEX_TOKEN=%s\n", shellQuote(entry.AccessToken))
			switch {
			case st.Provider == "mock":
				// The mock serves every wire: export each SDK family.
				fmt.Fprintf(w, "export OPENAI_BASE_URL=%s\n", shellQuote(ap+"/v1"))
				fmt.Fprintln(w, `export OPENAI_API_KEY="$AIPLEX_TOKEN"`)
				fmt.Fprintf(w, "export ANTHROPIC_BASE_URL=%s\n", shellQuote(ap))
				fmt.Fprintln(w, `export ANTHROPIC_API_KEY="$AIPLEX_TOKEN"`)
				fmt.Fprintf(w, "export GOOGLE_GEMINI_BASE_URL=%s\n", shellQuote(ap))
				fmt.Fprintln(w, `export GEMINI_API_KEY="$AIPLEX_TOKEN"`)
			case st.Protocol == "anthropic-messages":
				fmt.Fprintf(w, "export ANTHROPIC_BASE_URL=%s\n", shellQuote(ap))
				fmt.Fprintln(w, `export ANTHROPIC_API_KEY="$AIPLEX_TOKEN"`)
			case st.Protocol == "google-genai":
				fmt.Fprintf(w, "export GOOGLE_GEMINI_BASE_URL=%s\n", shellQuote(ap))
				fmt.Fprintln(w, `export GEMINI_API_KEY="$AIPLEX_TOKEN"`)
			default:
				fmt.Fprintf(w, "export OPENAI_BASE_URL=%s\n", shellQuote(ap+"/v1"))
				fmt.Fprintln(w, `export OPENAI_API_KEY="$AIPLEX_TOKEN"`)
			}
			return nil
		},
	}
}

func devReachable(url string) bool {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(url + "/healthz")
	if err != nil {
		return false
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp.StatusCode == 200
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func seededModelNames(seeded *devstack.Seeded) []string {
	names := make([]string, 0, len(seeded.Models))
	for _, m := range seeded.Models {
		names = append(names, m.Name)
	}
	return names
}
