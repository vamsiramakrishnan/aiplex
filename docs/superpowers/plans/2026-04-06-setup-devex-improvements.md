# Setup DevEx Improvements — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Bring AIPlex CLI setup experience to world-class level across 3 tiers — fixing credential security, token lifecycle, progress feedback, convenience commands, and polished interactive UX.

**Architecture:** All changes are in the CLI layer (`cmd/aiplex-cli/`) and config package (`internal/cliconfig/`). No API server changes. Each feature is an independent command or enhancement to an existing command. Shell completion uses Cobra's built-in generator.

**Tech Stack:** Go 1.24, Cobra v1.10.2, stdlib `os/exec` for browser open, `crypto/aes` for credential encryption, `tabwriter` for output.

---

## File Structure

| File | Action | Responsibility |
|------|--------|----------------|
| `cmd/aiplex-cli/cmd_whoami.go` | Create | `aiplex whoami` command |
| `cmd/aiplex-cli/cmd_login.go` | Modify | Token refresh, browser open, expiry warning |
| `cmd/aiplex-cli/cmd_health.go` | Modify | Pod log tailing on failure |
| `cmd/aiplex-cli/cmd_config.go` | Modify | Add `ctx` alias subcommand |
| `cmd/aiplex-cli/cmd_platform.go` | Modify | Progress spinners, upgrade alias |
| `cmd/aiplex-cli/cmd_init.go` | Modify | Domain validation, interactive region picker |
| `cmd/aiplex-cli/cmd_quickstart.go` | Create | `aiplex quickstart` end-to-end command |
| `cmd/aiplex-cli/cmd_completion.go` | Create | `aiplex completion` shell completions |
| `cmd/aiplex-cli/cmd_version.go` | Create | `aiplex version` with upgrade check |
| `cmd/aiplex-cli/browser.go` | Create | Cross-platform browser open helper |
| `cmd/aiplex-cli/spinner.go` | Create | Terminal spinner for long operations |
| `cmd/aiplex-cli/main.go` | Modify | Register new commands |
| `internal/cliconfig/config.go` | Modify | Add credential encryption helpers |
| `internal/cliconfig/crypto.go` | Create | AES-GCM encryption for credentials |
| `internal/cliconfig/crypto_test.go` | Create | Encryption round-trip tests |

---

### Task 1: `aiplex whoami` command

**Files:**
- Create: `cmd/aiplex-cli/cmd_whoami.go`
- Modify: `cmd/aiplex-cli/main.go`

- [ ] **Step 1: Create cmd_whoami.go**

```go
package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/vamsiramakrishnan/aiplex/internal/cliconfig"
)

func whoamiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Show current context, account, and auth status",
		Long:  `Displays the active context, GCP project, region, domain, and authentication status.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := cliconfig.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			if cfg.CurrentContext == "" {
				fmt.Println("No active context. Run: aiplex init")
				return nil
			}

			ctx, err := cfg.Current()
			if err != nil {
				return fmt.Errorf("current context: %w", err)
			}

			fmt.Printf("Context:  %s\n", cfg.CurrentContext)
			fmt.Printf("URL:      %s\n", ctx.URL)
			if ctx.Project != "" {
				fmt.Printf("Project:  %s\n", ctx.Project)
			}
			if ctx.Region != "" {
				fmt.Printf("Region:   %s\n", ctx.Region)
			}
			if ctx.Domain != "" {
				fmt.Printf("Domain:   %s\n", ctx.Domain)
			}

			// Auth status
			creds, err := cliconfig.LoadCredentials()
			if err == nil {
				tok := creds.GetToken(cfg.CurrentContext)
				if tok != nil && tok.AccessToken != "" {
					fmt.Println("Auth:     authenticated")
				} else {
					fmt.Println("Auth:     not authenticated (run: aiplex login)")
				}
			} else {
				fmt.Println("Auth:     unknown")
			}

			return nil
		},
	}
}
```

- [ ] **Step 2: Register in main.go**

Add `whoamiCmd()` to the Onboarding group in `main.go`. Find the block that adds init/login/logout/config/health/doctor/platform and add after `configCmd()`:

```go
	whoamiCmd(),
```

The line to add it after is the `configCmd()` registration (around line 80 in main.go).

- [ ] **Step 3: Build and verify**

Run: `go build ./cmd/aiplex-cli/ && ./aiplex-cli whoami`

Expected: "No active context. Run: aiplex init" (since no context is configured in this env).

- [ ] **Step 4: Commit**

```bash
git add cmd/aiplex-cli/cmd_whoami.go cmd/aiplex-cli/main.go
git commit -m "feat(cli): add aiplex whoami command"
```

---

### Task 2: Cross-platform browser open helper

**Files:**
- Create: `cmd/aiplex-cli/browser.go`

- [ ] **Step 1: Create browser.go**

```go
package main

import (
	"os/exec"
	"runtime"
)

// openBrowser opens a URL in the user's default browser.
// Returns an error if the browser cannot be opened, but callers
// should treat this as non-fatal (user can open URL manually).
func openBrowser(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	default: // linux, freebsd, etc.
		return exec.Command("xdg-open", url).Start()
	}
}
```

- [ ] **Step 2: Build to verify**

Run: `go build ./cmd/aiplex-cli/`

Expected: Clean build, no errors.

- [ ] **Step 3: Commit**

```bash
git add cmd/aiplex-cli/browser.go
git commit -m "feat(cli): add cross-platform browser open helper"
```

---

### Task 3: Auto-open browser on login + token refresh

**Files:**
- Modify: `cmd/aiplex-cli/cmd_login.go`

- [ ] **Step 1: Add browser open to device flow**

In `cmd_login.go`, find the block that prints the verification URL (inside `loginCmd()`, after `requestDeviceCode()` succeeds). It currently prints the URL. Add `openBrowser()` call right after the URL display:

Find this section (around line 60-70):
```go
			fmt.Println()
			fmt.Printf("  Open:  %s\n", dcr.VerificationURIComplete)
			fmt.Printf("  Code:  %s\n", dcr.UserCode)
```

Replace with:
```go
			fmt.Println()
			fmt.Printf("  Open:  %s\n", dcr.VerificationURIComplete)
			fmt.Printf("  Code:  %s\n", dcr.UserCode)
			fmt.Println()

			// Auto-open browser
			if err := openBrowser(dcr.VerificationURIComplete); err == nil {
				fmt.Println("  Browser opened automatically.")
			} else {
				fmt.Println("  Open the URL above in your browser.")
			}
```

- [ ] **Step 2: Add token refresh function**

Add this function at the end of `cmd_login.go`:

```go
// refreshToken attempts to exchange a refresh token for a new access token.
// Returns nil if no refresh token is available or if the refresh fails.
func refreshToken(baseURL, refreshTok string) (*tokenResponse, error) {
	data := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshTok},
		"client_id":     {"aiplex-cli"},
	}

	resp, err := http.PostForm(baseURL+"/oauth2/token", data)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var tr tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return nil, err
	}
	if tr.Error != "" {
		return nil, fmt.Errorf("refresh failed: %s", tr.Error)
	}
	return &tr, nil
}
```

- [ ] **Step 3: Store refresh token in login flow**

In `storeToken()`, the function currently only stores the access token. Find `storeToken` (around line 179) and replace it:

```go
func storeToken(contextName, accessToken, refreshTok string) error {
	creds, err := cliconfig.LoadCredentials()
	if err != nil {
		return fmt.Errorf("load credentials: %w", err)
	}

	entry := &cliconfig.TokenEntry{
		AccessToken: accessToken,
		TokenType:   "bearer",
		ExpiresAt:   time.Now().Add(24 * time.Hour).Format(time.RFC3339),
	}
	if refreshTok != "" {
		entry.RefreshToken = refreshTok
	}

	creds.SetToken(contextName, entry)
	return creds.Save()
}
```

- [ ] **Step 4: Update all storeToken callsites to pass refresh token**

Find all calls to `storeToken` in `cmd_login.go`. There are two:

1. Manual token entry (around line 45): `storeToken(ctxName, token, "")` — no refresh token for manual entry.
2. Device flow success (around line 80): `storeToken(ctxName, tr.AccessToken, tr.RefreshToken)`

Update each callsite to pass the third argument.

- [ ] **Step 5: Add auto-refresh to newClient() in main.go**

In `main.go`, find `newClient()` (lines 21-61). After loading the token from credentials, add a check for token expiry and auto-refresh. Find the section that loads credentials (around line 45-55) and add after the token is loaded:

```go
		// Auto-refresh expired tokens
		if tok != nil && tok.AccessToken != "" && tok.RefreshToken != "" && tok.ExpiresAt != "" {
			if expiry, err := time.Parse(time.RFC3339, tok.ExpiresAt); err == nil {
				if time.Now().After(expiry.Add(-5 * time.Minute)) {
					// Token expired or expiring soon — try refresh
					ctx, _ := cfg.Current()
					if ctx != nil {
						tr, err := refreshToken(ctx.URL, tok.RefreshToken)
						if err == nil && tr.AccessToken != "" {
							storeToken(cfg.CurrentContext, tr.AccessToken, tr.RefreshToken)
							resolvedToken = tr.AccessToken
						}
					}
				}
			}
		}
```

Add `"time"` to the imports in `main.go`.

- [ ] **Step 6: Build and verify**

Run: `go build ./cmd/aiplex-cli/`

Expected: Clean build.

- [ ] **Step 7: Commit**

```bash
git add cmd/aiplex-cli/cmd_login.go cmd/aiplex-cli/main.go
git commit -m "feat(cli): auto-open browser on login, add token refresh"
```

---

### Task 4: Credential encryption

**Files:**
- Create: `internal/cliconfig/crypto.go`
- Create: `internal/cliconfig/crypto_test.go`
- Modify: `internal/cliconfig/config.go`

- [ ] **Step 1: Write failing test for encryption round-trip**

Create `internal/cliconfig/crypto_test.go`:

```go
package cliconfig

import (
	"testing"
)

func TestEncryptDecrypt_RoundTrip(t *testing.T) {
	key := deriveKey("test-machine-id")
	plaintext := []byte(`{"tokens":{"ctx":{"access_token":"secret123"}}}`)

	encrypted, err := encrypt(plaintext, key)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if string(encrypted) == string(plaintext) {
		t.Fatal("encrypted should differ from plaintext")
	}

	decrypted, err := decrypt(encrypted, key)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if string(decrypted) != string(plaintext) {
		t.Fatalf("got %q, want %q", decrypted, plaintext)
	}
}

func TestEncryptDecrypt_WrongKey(t *testing.T) {
	key1 := deriveKey("machine-1")
	key2 := deriveKey("machine-2")
	plaintext := []byte("secret data")

	encrypted, err := encrypt(plaintext, key1)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	_, err = decrypt(encrypted, key2)
	if err == nil {
		t.Fatal("expected error decrypting with wrong key")
	}
}

func TestDeriveKey_Deterministic(t *testing.T) {
	k1 := deriveKey("same-input")
	k2 := deriveKey("same-input")
	if string(k1) != string(k2) {
		t.Fatal("same input should produce same key")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cliconfig/ -run TestEncrypt -v`

Expected: FAIL — `encrypt`, `decrypt`, `deriveKey` not defined.

- [ ] **Step 3: Implement crypto.go**

Create `internal/cliconfig/crypto.go`:

```go
package cliconfig

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"strings"
)

// deriveKey creates a 32-byte AES key from a seed string using SHA-256.
func deriveKey(seed string) []byte {
	h := sha256.Sum256([]byte("aiplex-credentials:" + seed))
	return h[:]
}

// encrypt encrypts plaintext using AES-256-GCM.
func encrypt(plaintext, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// decrypt decrypts AES-256-GCM ciphertext.
func decrypt(ciphertext, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(ciphertext) < gcm.NonceSize() {
		return nil, fmt.Errorf("ciphertext too short")
	}
	nonce := ciphertext[:gcm.NonceSize()]
	return gcm.Open(nil, nonce, ciphertext[gcm.NonceSize():], nil)
}

// machineID returns a stable machine identifier for key derivation.
// Falls back to hostname if /etc/machine-id is not available.
func machineID() string {
	// Linux: /etc/machine-id
	if data, err := os.ReadFile("/etc/machine-id"); err == nil {
		id := strings.TrimSpace(string(data))
		if id != "" {
			return id
		}
	}
	// macOS: IOPlatformUUID via ioreg
	// Fallback: hostname + username
	host, _ := os.Hostname()
	user := os.Getenv("USER")
	if user == "" {
		user = os.Getenv("USERNAME")
	}
	return host + ":" + user
}

// credentialKey returns the AES key for encrypting credentials on this machine.
func credentialKey() []byte {
	return deriveKey(machineID())
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/cliconfig/ -run TestEncrypt -v && go test ./internal/cliconfig/ -run TestDeriveKey -v`

Expected: All PASS.

- [ ] **Step 5: Wire encryption into Credentials Save/Load**

In `internal/cliconfig/config.go`, modify `(*Credentials).Save()` (lines 147-157) and `LoadCredentials()` (lines 123-144).

Replace `(*Credentials).Save()`:

```go
// Save writes credentials to ~/.aiplex/credentials.json, encrypted at rest.
func (c *Credentials) Save() error {
	dir, err := Dir()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal credentials: %w", err)
	}
	encrypted, err := encrypt(data, credentialKey())
	if err != nil {
		// Fall back to plaintext if encryption fails
		return os.WriteFile(filepath.Join(dir, "credentials.json"), data, 0600)
	}
	return os.WriteFile(filepath.Join(dir, "credentials.json"), encrypted, 0600)
}
```

Replace `LoadCredentials()`:

```go
// LoadCredentials reads tokens from ~/.aiplex/credentials.json.
// Handles both encrypted and plaintext formats for backward compatibility.
func LoadCredentials() (*Credentials, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "credentials.json")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &Credentials{Tokens: make(map[string]*TokenEntry)}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read credentials: %w", err)
	}

	// Try decrypting first (new format)
	decrypted, err := decrypt(data, credentialKey())
	if err == nil {
		data = decrypted
	}
	// If decryption fails, try parsing as plaintext (backward compat)

	var creds Credentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, fmt.Errorf("parse credentials: %w", err)
	}
	if creds.Tokens == nil {
		creds.Tokens = make(map[string]*TokenEntry)
	}
	return &creds, nil
}
```

- [ ] **Step 6: Run all cliconfig tests**

Run: `go test ./internal/cliconfig/ -v`

Expected: All PASS (existing tests + new crypto tests).

- [ ] **Step 7: Commit**

```bash
git add internal/cliconfig/crypto.go internal/cliconfig/crypto_test.go internal/cliconfig/config.go
git commit -m "feat(cli): encrypt credentials at rest with AES-256-GCM"
```

---

### Task 5: Terminal spinner for long operations

**Files:**
- Create: `cmd/aiplex-cli/spinner.go`

- [ ] **Step 1: Create spinner.go**

```go
package main

import (
	"fmt"
	"os"
	"sync"
	"time"

	"golang.org/x/term"
)

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

type spinner struct {
	msg    string
	stop   chan struct{}
	done   chan struct{}
	mu     sync.Mutex
}

// startSpinner begins an animated spinner with a message.
// Returns a spinner that must be stopped with .finish() or .fail().
func startSpinner(msg string) *spinner {
	s := &spinner{
		msg:  msg,
		stop: make(chan struct{}),
		done: make(chan struct{}),
	}

	// Only animate if stdout is a terminal
	if !term.IsTerminal(int(os.Stdout.Fd())) {
		fmt.Printf("  %s...\n", msg)
		close(s.done)
		return s
	}

	go func() {
		defer close(s.done)
		i := 0
		for {
			select {
			case <-s.stop:
				return
			default:
				s.mu.Lock()
				fmt.Printf("\r  %s %s...", spinnerFrames[i%len(spinnerFrames)], s.msg)
				s.mu.Unlock()
				i++
				time.Sleep(80 * time.Millisecond)
			}
		}
	}()
	return s
}

// finish stops the spinner with a success message.
func (s *spinner) finish(msg string) {
	select {
	case <-s.stop:
		return // already stopped
	default:
	}
	close(s.stop)
	<-s.done
	fmt.Printf("\r  [pass] %s\n", msg)
}

// fail stops the spinner with a failure message.
func (s *spinner) fail(msg string) {
	select {
	case <-s.stop:
		return
	default:
	}
	close(s.stop)
	<-s.done
	fmt.Printf("\r  [FAIL] %s\n", msg)
}
```

- [ ] **Step 2: Add golang.org/x/term dependency**

Run: `go get golang.org/x/term`

- [ ] **Step 3: Build to verify**

Run: `go build ./cmd/aiplex-cli/`

Expected: Clean build.

- [ ] **Step 4: Commit**

```bash
git add cmd/aiplex-cli/spinner.go go.mod go.sum
git commit -m "feat(cli): add terminal spinner for long-running operations"
```

---

### Task 6: Progress spinners in platform apply

**Files:**
- Modify: `cmd/aiplex-cli/cmd_platform.go`

- [ ] **Step 1: Add spinners to state bucket creation**

In `cmd_platform.go`, replace the state bucket step (inside `platformApplyCmd`, around line 85-91):

```go
			// Step 1: Terraform state bucket
			if !skipInfra {
				fmt.Println("[1/6] Terraform state bucket...")
				sp := startSpinner("Creating state bucket")
				if err := ensureStateBucket(ctx.Project); err != nil {
					sp.fail(fmt.Sprintf("State bucket: %v — create manually or Terraform will fail", err))
				} else {
					sp.finish(fmt.Sprintf("State bucket ready (gs://%s)", stateBucketName(ctx.Project)))
				}
				fmt.Println()
```

- [ ] **Step 2: Add spinner to kubectl config step**

Replace the kubectl configuration step (around line 136-145):

```go
			// Step 4: Configure kubectl
			if !skipInfra {
				fmt.Println("[4/6] Configuring kubectl...")
				sp := startSpinner("Getting cluster credentials")
				if err := runCmd(".", "gcloud", "container", "clusters", "get-credentials",
					"aiplex", "--region", ctx.Region, "--project", ctx.Project); err != nil {
					sp.fail(fmt.Sprintf("kubectl config failed: %v", err))
				} else {
					sp.finish("kubectl configured for cluster 'aiplex'")
				}
				fmt.Println()
			}
```

- [ ] **Step 3: Build and verify**

Run: `go build ./cmd/aiplex-cli/`

Expected: Clean build.

- [ ] **Step 4: Commit**

```bash
git add cmd/aiplex-cli/cmd_platform.go
git commit -m "feat(cli): add progress spinners to platform apply"
```

---

### Task 7: `aiplex upgrade` alias + pod logs on health failure

**Files:**
- Modify: `cmd/aiplex-cli/cmd_platform.go`
- Modify: `cmd/aiplex-cli/cmd_health.go`
- Modify: `cmd/aiplex-cli/main.go`

- [ ] **Step 1: Add upgrade command as alias to platform apply**

In `cmd_platform.go`, add this function after `platformDestroyCmd()`:

```go
func upgradeCmd() *cobra.Command {
	cmd := platformApplyCmd()
	cmd.Use = "upgrade"
	cmd.Short = "Upgrade the AIPlex platform (alias for platform apply)"
	cmd.Long = `Re-runs the full deployment pipeline to upgrade infrastructure and application.
This is an alias for "aiplex platform apply".`
	return cmd
}
```

- [ ] **Step 2: Register upgrade in main.go**

Add `upgradeCmd()` to the root command registration, near the platform command.

- [ ] **Step 3: Add pod log tailing to health command on failure**

In `cmd_health.go`, find the section after all health checks are complete where it shows troubleshooting hints (around line 90-100, where `failCount > 0`). Add pod log fetching:

```go
			if failCount > 0 {
				fmt.Println()
				fmt.Println("Troubleshooting:")
				fmt.Println("  Fetching logs from unhealthy pods...")
				fmt.Println()
				// Show recent logs from aiplex-system pods
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
```

Add `"os/exec"` and `"strings"` to the imports in `cmd_health.go` if not already present.

- [ ] **Step 4: Build and verify**

Run: `go build ./cmd/aiplex-cli/`

Expected: Clean build.

- [ ] **Step 5: Commit**

```bash
git add cmd/aiplex-cli/cmd_platform.go cmd/aiplex-cli/cmd_health.go cmd/aiplex-cli/main.go
git commit -m "feat(cli): add upgrade alias, pod logs on health failure"
```

---

### Task 8: Domain validation in init

**Files:**
- Modify: `cmd/aiplex-cli/cmd_init.go`

- [ ] **Step 1: Add domain validation function**

Add this function at the end of `cmd_init.go`, before `sanitizeContextName`:

```go
// validateDomain checks if a domain has valid DNS or is a known test domain.
func validateDomain(domain string) (ok bool, warning string) {
	// nip.io and sslip.io are auto-resolving test domains — always valid
	if strings.HasSuffix(domain, ".nip.io") || strings.HasSuffix(domain, ".sslip.io") {
		return true, ""
	}

	// Check DNS resolution
	out, err := exec.Command("dig", "+short", domain).Output()
	if err != nil {
		// dig not available — skip validation
		return true, ""
	}
	resolved := strings.TrimSpace(string(out))
	if resolved == "" {
		return false, fmt.Sprintf("DNS for %q does not resolve yet. You can configure DNS after deploy.", domain)
	}
	return true, ""
}
```

- [ ] **Step 2: Call validation after domain input**

In the init wizard, find the domain input section (around line 134 where `domain = prompt(reader, "  Domain", defaultDomain)`). Add validation right after:

```go
			// Validate domain
			if ok, warning := validateDomain(domain); !ok {
				fmt.Printf("  [WARN] %s\n", warning)
				fmt.Println("  Continuing with this domain — you can update later with:")
				fmt.Println("    aiplex config set-context --domain <your-domain>")
				fmt.Println()
			}
```

- [ ] **Step 3: Build and verify**

Run: `go build ./cmd/aiplex-cli/`

Expected: Clean build.

- [ ] **Step 4: Commit**

```bash
git add cmd/aiplex-cli/cmd_init.go
git commit -m "feat(cli): validate domain DNS during init"
```

---

### Task 9: `aiplex ctx` shorthand

**Files:**
- Modify: `cmd/aiplex-cli/cmd_config.go`
- Modify: `cmd/aiplex-cli/main.go`

- [ ] **Step 1: Add ctx command**

In `cmd_config.go`, add this function:

```go
func ctxCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ctx [name]",
		Short: "Switch context (shorthand for config use-context)",
		Long: `Switch to a named context. Without arguments, shows the current context.

Examples:
  aiplex ctx              # show current context
  aiplex ctx production   # switch to production`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := cliconfig.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			if len(args) == 0 {
				// No args — show current
				if cfg.CurrentContext == "" {
					fmt.Println("No active context. Run: aiplex init")
				} else {
					fmt.Println(cfg.CurrentContext)
				}
				return nil
			}

			name := args[0]
			if _, ok := cfg.Contexts[name]; !ok {
				available := contextNames(cfg)
				return fmt.Errorf("context %q not found. Available: %s", name, strings.Join(available, ", "))
			}

			cfg.CurrentContext = name
			if err := cfg.Save(); err != nil {
				return fmt.Errorf("save config: %w", err)
			}
			fmt.Printf("Switched to context %q\n", name)
			return nil
		},
	}
}
```

Add `"strings"` to imports in `cmd_config.go` if not already present.

- [ ] **Step 2: Register in main.go**

Add `ctxCmd()` to the root command registration.

- [ ] **Step 3: Build and verify**

Run: `go build ./cmd/aiplex-cli/ && ./aiplex-cli ctx`

Expected: "No active context. Run: aiplex init"

- [ ] **Step 4: Commit**

```bash
git add cmd/aiplex-cli/cmd_config.go cmd/aiplex-cli/main.go
git commit -m "feat(cli): add aiplex ctx shorthand for context switching"
```

---

### Task 10: `aiplex quickstart` command

**Files:**
- Create: `cmd/aiplex-cli/cmd_quickstart.go`
- Modify: `cmd/aiplex-cli/main.go`

- [ ] **Step 1: Create cmd_quickstart.go**

```go
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/vamsiramakrishnan/aiplex/internal/cliconfig"
)

func quickstartCmd() *cobra.Command {
	var (
		project     string
		region      string
		autoApprove bool
	)

	cmd := &cobra.Command{
		Use:   "quickstart",
		Short: "Zero to running platform in one command",
		Long: `Runs the full setup pipeline end-to-end:

  1. aiplex init      — configure project and install tools
  2. platform apply   — deploy infrastructure + application
  3. deploy example   — deploy quickstart workload
  4. open console     — open the AIPlex console in browser

This is the fastest way to go from nothing to a running AIPlex platform.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println()
			fmt.Println("  ╔══════════════════════════════════════╗")
			fmt.Println("  ║       AIPlex Quickstart               ║")
			fmt.Println("  ╚══════════════════════════════════════╝")
			fmt.Println()

			// Step 1: Init (if no context exists)
			cfg, _ := cliconfig.Load()
			if cfg.CurrentContext == "" || project != "" {
				fmt.Println("━━━ Step 1/4: Initialize ━━━━━━━━━━━━━━━")
				fmt.Println()
				initArgs := []string{"init"}
				if project != "" {
					initArgs = append(initArgs, "--project", project)
				}
				if region != "" {
					initArgs = append(initArgs, "--region", region)
				}
				self, _ := os.Executable()
				initCmd := exec.Command(self, initArgs...)
				initCmd.Stdout = os.Stdout
				initCmd.Stderr = os.Stderr
				initCmd.Stdin = os.Stdin
				if err := initCmd.Run(); err != nil {
					return fmt.Errorf("init failed: %w", err)
				}
				fmt.Println()
			} else {
				fmt.Printf("━━━ Step 1/4: Initialize (using context %q) ━━━\n", cfg.CurrentContext)
				fmt.Println()
			}

			// Step 2: Platform apply
			fmt.Println("━━━ Step 2/4: Deploy Platform ━━━━━━━━━━")
			fmt.Println()
			self, _ := os.Executable()
			applyArgs := []string{"platform", "apply"}
			if autoApprove {
				applyArgs = append(applyArgs, "--yes")
			}
			applyCmd := exec.Command(self, applyArgs...)
			applyCmd.Stdout = os.Stdout
			applyCmd.Stderr = os.Stderr
			applyCmd.Stdin = os.Stdin
			if err := applyCmd.Run(); err != nil {
				return fmt.Errorf("platform apply failed: %w", err)
			}
			fmt.Println()

			// Step 3: Deploy quickstart example
			fmt.Println("━━━ Step 3/4: Deploy Example Workload ━━")
			fmt.Println()
			exampleFile := findQuickstartFile()
			if exampleFile != "" {
				applyExCmd := exec.Command(self, "apply", "-f", exampleFile)
				applyExCmd.Stdout = os.Stdout
				applyExCmd.Stderr = os.Stderr
				if err := applyExCmd.Run(); err != nil {
					fmt.Printf("  [WARN] Example deploy failed: %v\n", err)
					fmt.Println("  You can deploy manually later: aiplex apply -f examples/quickstart.yaml")
				} else {
					fmt.Println("  [pass] Example workload deployed")
				}
			} else {
				fmt.Println("  [WARN] No quickstart example found. Deploy manually later.")
			}
			fmt.Println()

			// Step 4: Open console
			fmt.Println("━━━ Step 4/4: Open Console ━━━━━━━━━━━━━")
			fmt.Println()
			cfg, _ = cliconfig.Load()
			if ctx, err := cfg.Current(); err == nil && ctx.URL != "" {
				consoleURL := ctx.URL
				fmt.Printf("  Console: %s\n", consoleURL)
				if err := openBrowser(consoleURL); err == nil {
					fmt.Println("  Browser opened.")
				} else {
					fmt.Println("  Open the URL above in your browser.")
				}
			}

			fmt.Println()
			fmt.Println("  ╔══════════════════════════════════════╗")
			fmt.Println("  ║     Quickstart Complete!              ║")
			fmt.Println("  ╚══════════════════════════════════════╝")
			fmt.Println()
			fmt.Println("  Next steps:")
			fmt.Println("    aiplex login           # authenticate")
			fmt.Println("    aiplex status           # see what's running")
			fmt.Println("    aiplex catalog list     # browse tools/agents/models")
			fmt.Println()

			return nil
		},
	}

	cmd.Flags().StringVar(&project, "project", "", "GCP project ID (skips interactive prompt)")
	cmd.Flags().StringVar(&region, "region", "", "GCP region")
	cmd.Flags().BoolVarP(&autoApprove, "yes", "y", false, "Auto-approve Terraform changes")
	return cmd
}

func findQuickstartFile() string {
	candidates := []string{
		"examples/quickstart.yaml",
		"examples/quickstart.json",
		"../examples/quickstart.yaml",
	}
	for _, c := range candidates {
		abs, _ := filepath.Abs(c)
		if _, err := os.Stat(abs); err == nil {
			return abs
		}
	}
	return ""
}
```

- [ ] **Step 2: Register in main.go**

Add `quickstartCmd()` to the root command registration, in the Onboarding group.

- [ ] **Step 3: Build and verify**

Run: `go build ./cmd/aiplex-cli/ && ./aiplex-cli quickstart --help`

Expected: Shows help text with --project, --region, --yes flags.

- [ ] **Step 4: Commit**

```bash
git add cmd/aiplex-cli/cmd_quickstart.go cmd/aiplex-cli/main.go
git commit -m "feat(cli): add aiplex quickstart — zero to platform in one command"
```

---

### Task 11: Shell completion

**Files:**
- Create: `cmd/aiplex-cli/cmd_completion.go`
- Modify: `cmd/aiplex-cli/main.go`

- [ ] **Step 1: Create cmd_completion.go**

```go
package main

import (
	"os"

	"github.com/spf13/cobra"
)

func completionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "completion [bash|zsh|fish|powershell]",
		Short: "Generate shell completion scripts",
		Long: `Generate shell completion scripts for aiplex.

Add to your shell profile:

  # Bash
  echo 'source <(aiplex completion bash)' >> ~/.bashrc

  # Zsh
  echo 'source <(aiplex completion zsh)' >> ~/.zshrc

  # Fish
  aiplex completion fish > ~/.config/fish/completions/aiplex.fish`,
		Args:                  cobra.ExactArgs(1),
		DisableFlagsInUseLine: true,
		ValidArgs:             []string{"bash", "zsh", "fish", "powershell"},
		RunE: func(cmd *cobra.Command, args []string) error {
			switch args[0] {
			case "bash":
				return cmd.Root().GenBashCompletion(os.Stdout)
			case "zsh":
				return cmd.Root().GenZshCompletion(os.Stdout)
			case "fish":
				return cmd.Root().GenFishCompletion(os.Stdout, true)
			case "powershell":
				return cmd.Root().GenPowerShellCompletionWithDesc(os.Stdout)
			default:
				return cmd.Help()
			}
		},
	}
	return cmd
}
```

- [ ] **Step 2: Register in main.go**

Add `completionCmd()` to the root command registration.

- [ ] **Step 3: Build and test**

Run: `go build ./cmd/aiplex-cli/ && ./aiplex-cli completion bash | head -5`

Expected: Outputs bash completion script header.

- [ ] **Step 4: Commit**

```bash
git add cmd/aiplex-cli/cmd_completion.go cmd/aiplex-cli/main.go
git commit -m "feat(cli): add shell completion for bash/zsh/fish/powershell"
```

---

### Task 12: Version command with upgrade check

**Files:**
- Create: `cmd/aiplex-cli/cmd_version.go`
- Modify: `cmd/aiplex-cli/main.go`

- [ ] **Step 1: Create cmd_version.go**

```go
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"time"

	"github.com/spf13/cobra"
)

// Set via ldflags at build time: -ldflags "-X main.version=v0.1.0 -X main.commit=abc123"
var (
	version = "dev"
	commit  = "unknown"
)

func versionCmd() *cobra.Command {
	var checkUpgrade bool

	cmd := &cobra.Command{
		Use:   "version",
		Short: "Show CLI version and check for updates",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("aiplex version %s\n", version)
			fmt.Printf("  commit:   %s\n", commit)
			fmt.Printf("  go:       %s\n", runtime.Version())
			fmt.Printf("  os/arch:  %s/%s\n", runtime.GOOS, runtime.GOARCH)

			if checkUpgrade {
				fmt.Println()
				checkForUpgrade()
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&checkUpgrade, "check", false, "Check for newer version")
	return cmd
}

func checkForUpgrade() {
	if version == "dev" {
		fmt.Println("  Running dev build — version check skipped.")
		return
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("https://api.github.com/repos/vamsiramakrishnan/aiplex/releases/latest")
	if err != nil {
		fmt.Println("  Could not check for updates.")
		return
	}
	defer resp.Body.Close()

	var release struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return
	}

	if release.TagName != "" && release.TagName != version {
		fmt.Printf("  Update available: %s → %s\n", version, release.TagName)
		fmt.Printf("  Download: %s\n", release.HTMLURL)
	} else {
		fmt.Println("  You are up to date.")
	}
}
```

- [ ] **Step 2: Wire version/commit into build**

In `Makefile`, update the `LDFLAGS` line (line 24) to inject version info:

```makefile
VERSION     := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT      := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
LDFLAGS     := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT)
```

- [ ] **Step 3: Register in main.go**

Add `versionCmd()` to the root command registration.

- [ ] **Step 4: Build and test**

Run: `make build && ./bin/aiplex version`

Expected: Shows version, commit, go version, os/arch.

- [ ] **Step 5: Commit**

```bash
git add cmd/aiplex-cli/cmd_version.go cmd/aiplex-cli/main.go Makefile
git commit -m "feat(cli): add version command with upgrade check"
```

---

### Task 13: Terraform state auto-migration

**Files:**
- Modify: `cmd/aiplex-cli/cmd_platform.go`

- [ ] **Step 1: Add migration check to ensureStateBucket**

In `cmd_platform.go`, modify the state bucket step in `platformApplyCmd`. Before creating the new bucket, check if the old hardcoded bucket exists and offer migration. Replace the Step 1 block:

```go
			// Step 1: Terraform state bucket
			if !skipInfra {
				fmt.Println("[1/6] Terraform state bucket...")
				newBucket := stateBucketName(ctx.Project)

				// Check for legacy bucket and offer migration
				legacyBucket := "aiplex-terraform-state"
				if newBucket != legacyBucket {
					legacyExists := exec.Command("gcloud", "storage", "buckets", "describe",
						fmt.Sprintf("gs://%s", legacyBucket), "--project", ctx.Project, "--quiet").Run()
					if legacyExists == nil {
						newExists := exec.Command("gcloud", "storage", "buckets", "describe",
							fmt.Sprintf("gs://%s", newBucket), "--project", ctx.Project, "--quiet").Run()
						if newExists != nil {
							fmt.Printf("  [info] Found legacy state bucket: gs://%s\n", legacyBucket)
							fmt.Printf("  [info] Migrating to: gs://%s\n", newBucket)
							// Copy state
							copyCmd := exec.Command("gcloud", "storage", "cp", "-r",
								fmt.Sprintf("gs://%s/*", legacyBucket),
								fmt.Sprintf("gs://%s/", newBucket),
								"--project", ctx.Project, "--quiet")
							if err := copyCmd.Run(); err != nil {
								fmt.Printf("  [WARN] State migration failed: %v\n", err)
								fmt.Printf("  Using legacy bucket. Migrate manually later.\n")
							} else {
								fmt.Printf("  [pass] State migrated to gs://%s\n", newBucket)
							}
						}
					}
				}

				sp := startSpinner("Ensuring state bucket")
				if err := ensureStateBucket(ctx.Project); err != nil {
					sp.fail(fmt.Sprintf("State bucket: %v", err))
				} else {
					sp.finish(fmt.Sprintf("State bucket ready (gs://%s)", newBucket))
				}
				fmt.Println()
```

Add `"os/exec"` to imports if not already present.

- [ ] **Step 2: Build and verify**

Run: `go build ./cmd/aiplex-cli/`

Expected: Clean build.

- [ ] **Step 3: Commit**

```bash
git add cmd/aiplex-cli/cmd_platform.go
git commit -m "feat(cli): auto-detect and migrate legacy terraform state bucket"
```

---

### Task 14: Register all new commands in main.go

**Files:**
- Modify: `cmd/aiplex-cli/main.go`

- [ ] **Step 1: Add all new commands to root**

In `main.go`, find the command registration block. Ensure ALL new commands are registered. The full onboarding group should be:

```go
	// ── Onboarding ──
	root.AddCommand(
		initCmd(),
		loginCmd(),
		logoutCmd(),
		configCmd(),
		whoamiCmd(),
		ctxCmd(),
		healthCmd(),
		doctorCmd(),
		platformCmd(),
		quickstartCmd(),
		upgradeCmd(),
		versionCmd(),
		completionCmd(),
	)
```

- [ ] **Step 2: Add time import for token refresh**

Add `"time"` to the imports in `main.go` if not already present.

- [ ] **Step 3: Full build + test**

Run: `make build && make test`

Expected: All builds succeed, all tests pass.

- [ ] **Step 4: Verify all commands show in help**

Run: `./bin/aiplex --help`

Expected: Shows all commands including whoami, ctx, quickstart, upgrade, version, completion.

- [ ] **Step 5: Commit**

```bash
git add cmd/aiplex-cli/main.go
git commit -m "feat(cli): register all new commands — whoami, ctx, quickstart, upgrade, version, completion"
```

---

### Task 15: Final integration test

- [ ] **Step 1: Run full test suite**

Run: `make test`

Expected: All existing tests pass. No regressions.

- [ ] **Step 2: Verify CLI help output**

Run: `./bin/aiplex --help`

Verify all commands are listed.

- [ ] **Step 3: Verify individual commands**

Run each:
```bash
./bin/aiplex whoami
./bin/aiplex ctx
./bin/aiplex version
./bin/aiplex completion bash | head -3
./bin/aiplex quickstart --help
./bin/aiplex upgrade --help
```

- [ ] **Step 4: Verify build with version injection**

Run: `make build && ./bin/aiplex version`

Expected: Shows git tag/hash, not "dev"/"unknown".

- [ ] **Step 5: Final commit if any loose changes**

```bash
git status
# If any uncommitted files:
git add -A && git commit -m "chore: finalize setup devex improvements"
```
