package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/spf13/cobra"

	"github.com/vamsiramakrishnan/aiplex/internal/cliconfig"
)

func loginCmd() *cobra.Command {
	var contextName string

	cmd := &cobra.Command{
		Use:   "login",
		Short: "Authenticate with your AIPlex instance",
		Long: `Login using OAuth 2.0 Device Authorization Grant (RFC 8628).

This opens a browser for you to approve the CLI, then stores credentials
in ~/.aiplex/credentials.json. No browser? Use the displayed code.

Examples:
  aiplex login                      # login to current context
  aiplex login --context prod       # login to specific context
  aiplex login --token <token>      # store a token directly`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := cliconfig.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			// Resolve which context to use
			ctxName := contextName
			if ctxName == "" {
				ctxName = cfg.CurrentContext
			}
			if ctxName == "" {
				return fmt.Errorf("no context set — run: aiplex config set-context <name> --url <url>")
			}
			ctx, ok := cfg.Contexts[ctxName]
			if !ok {
				return fmt.Errorf("context %q not found", ctxName)
			}

			// If --token was passed globally, just store it
			if token != "" {
				return storeToken(ctxName, token)
			}

			fmt.Printf("Logging in to %s (%s)...\n\n", ctxName, ctx.URL)

			// Step 1: Request device code from Hydra
			deviceResp, err := requestDeviceCode(ctx.URL)
			if err != nil {
				fmt.Println("Device flow not available. Falling back to manual token entry.")
				fmt.Println()
				fmt.Println("To get a token:")
				fmt.Printf("  1. Open %s/auth/login in your browser\n", ctx.URL)
				fmt.Println("  2. Copy the access token from the response")
				fmt.Println("  3. Run: aiplex login --token <your-token>")
				return nil
			}

			// Step 2: Show user the code
			fmt.Println("To authorize this CLI, visit:")
			fmt.Printf("  %s\n\n", deviceResp.VerificationURIComplete)
			fmt.Printf("  Or go to: %s\n", deviceResp.VerificationURI)
			fmt.Printf("  Enter code: %s\n\n", deviceResp.UserCode)
			fmt.Println("Waiting for authorization...")

			// Step 3: Poll for token
			tokenResp, err := pollForToken(ctx.URL, deviceResp.DeviceCode, deviceResp.Interval)
			if err != nil {
				return fmt.Errorf("authorization failed: %w", err)
			}

			// Step 4: Store credentials
			if err := storeToken(ctxName, tokenResp.AccessToken); err != nil {
				return err
			}

			fmt.Println()
			fmt.Printf("Logged in to %s.\n", ctxName)
			return nil
		},
	}

	cmd.Flags().StringVar(&contextName, "context", "", "Context to login to (default: current)")
	return cmd
}

type deviceCodeResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	ExpiresIn    int    `json:"expires_in,omitempty"`
	TokenType    string `json:"token_type"`
	Error        string `json:"error,omitempty"`
}

func requestDeviceCode(baseURL string) (*deviceCodeResponse, error) {
	resp, err := http.PostForm(baseURL+"/oauth2/device/auth", url.Values{
		"client_id": {"aiplex-cli"},
		"scope":     {"openid offline_access"},
	})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("device auth returned %d", resp.StatusCode)
	}
	var dr deviceCodeResponse
	if err := json.NewDecoder(resp.Body).Decode(&dr); err != nil {
		return nil, err
	}
	return &dr, nil
}

func pollForToken(baseURL, deviceCode string, interval int) (*tokenResponse, error) {
	if interval < 1 {
		interval = 5
	}
	client := &http.Client{Timeout: 10 * time.Second}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("timed out waiting for authorization")
		case <-time.After(time.Duration(interval) * time.Second):
		}

		resp, err := client.PostForm(baseURL+"/oauth2/token", url.Values{
			"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
			"client_id":   {"aiplex-cli"},
			"device_code": {deviceCode},
		})
		if err != nil {
			continue
		}

		var tr tokenResponse
		json.NewDecoder(resp.Body).Decode(&tr)
		resp.Body.Close()

		switch tr.Error {
		case "authorization_pending":
			continue
		case "slow_down":
			interval += 1
			continue
		case "":
			if tr.AccessToken != "" {
				return &tr, nil
			}
			continue
		default:
			return nil, fmt.Errorf("%s", tr.Error)
		}
	}
}

func storeToken(contextName, accessToken string) error {
	creds, err := cliconfig.LoadCredentials()
	if err != nil {
		return fmt.Errorf("load credentials: %w", err)
	}

	creds.SetToken(contextName, &cliconfig.TokenEntry{
		AccessToken: accessToken,
		TokenType:   "bearer",
		ExpiresAt:   time.Now().Add(24 * time.Hour).Format(time.RFC3339),
	})

	if err := creds.Save(); err != nil {
		return fmt.Errorf("save credentials: %w", err)
	}

	dir, _ := cliconfig.Dir()
	fmt.Printf("Credentials stored in %s/credentials.json\n", dir)
	return nil
}

func logoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Remove stored credentials for the current context",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := cliconfig.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			if cfg.CurrentContext == "" {
				return fmt.Errorf("no active context")
			}
			creds, err := cliconfig.LoadCredentials()
			if err != nil {
				return fmt.Errorf("load credentials: %w", err)
			}
			delete(creds.Tokens, cfg.CurrentContext)
			if err := creds.Save(); err != nil {
				return fmt.Errorf("save credentials: %w", err)
			}
			fmt.Printf("Logged out of %s.\n", cfg.CurrentContext)
			return nil
		},
	}
}

// init registers logout alongside login — but it's unused in this file.
// We add it via the init import pattern if needed.
func init() {
	// logoutCmd is added in main.go via root.AddCommand
}
