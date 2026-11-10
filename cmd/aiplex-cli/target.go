package main

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/cliconfig"
)

// target is where a command talks to and as whom. Every command resolves it
// the same way, in this order:
//
//	URL:   --url, $AIPLEX_URL, the context's URL, http://localhost:8080
//	token: --token, $AIPLEX_TOKEN, the context's stored token
//	context: --context, $AIPLEX_CONTEXT, the current context
type target struct {
	URL         string
	URLSource   string // "--url", "AIPLEX_URL", "context <name>", "default"
	Token       string
	TokenSource string // "--token", "AIPLEX_TOKEN", "context <name>", ""
	Context     string // the context consulted (may be empty)
	ExpiresAt   time.Time
}

// defaultAPIURL is used when nothing else names the API.
const defaultAPIURL = "http://localhost:8080"

// contextFlag is the global --context flag.
var contextFlag string

func resolveTarget() target {
	var t target
	cfg, _ := cliconfig.Load()
	var ctx *cliconfig.Context
	if cfg != nil {
		t.Context = cfg.CurrentContext
		ctx, _ = cfg.Current()
	}
	switch {
	case apiURL != "":
		t.URL, t.URLSource = apiURL, "--url"
	case os.Getenv("AIPLEX_URL") != "":
		t.URL, t.URLSource = os.Getenv("AIPLEX_URL"), "AIPLEX_URL"
	case ctx != nil && ctx.URL != "":
		t.URL, t.URLSource = ctx.URL, "context "+t.Context
	default:
		t.URL, t.URLSource = defaultAPIURL, "default"
	}
	switch {
	case token != "":
		t.Token, t.TokenSource = token, "--token"
	case os.Getenv("AIPLEX_TOKEN") != "":
		t.Token, t.TokenSource = os.Getenv("AIPLEX_TOKEN"), "AIPLEX_TOKEN"
	case cfg != nil && t.Context != "":
		creds, err := cliconfig.LoadCredentials()
		if err != nil {
			break
		}
		entry := creds.GetToken(t.Context)
		if entry == nil || entry.AccessToken == "" {
			break
		}
		t.Token, t.TokenSource = entry.AccessToken, "context "+t.Context
		if exp, err := time.Parse(time.RFC3339, entry.ExpiresAt); err == nil {
			t.ExpiresAt = exp
		}
		// Refresh a token that has expired or expires within five minutes.
		if entry.RefreshToken != "" && !t.ExpiresAt.IsZero() && time.Now().After(t.ExpiresAt.Add(-5*time.Minute)) && ctx != nil && ctx.URL != "" {
			if tr, err := refreshToken(ctx.URL, entry.RefreshToken); err == nil && tr.AccessToken != "" {
				_ = storeToken(t.Context, tr.AccessToken, tr.RefreshToken)
				t.Token, t.ExpiresAt = tr.AccessToken, time.Time{}
			}
		}
	}
	return t
}

// resolveAPIURL returns the API URL from flags, env, or config.
func resolveAPIURL() string { return resolveTarget().URL }

// resolveToken returns the auth token from flags, env, or config.
func resolveToken() string { return resolveTarget().Token }

// jwtExpiry reads the exp claim of a JWT without verifying it (for messages
// only; the server decides).
func jwtExpiry(tok string) (time.Time, bool) {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return time.Time{}, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}, false
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if json.Unmarshal(raw, &claims) != nil || claims.Exp == 0 {
		return time.Time{}, false
	}
	return time.Unix(claims.Exp, 0), true
}

// jwtClaims reads display claims of a JWT without verifying it.
func jwtClaims(tok string) map[string]any {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil
	}
	var claims map[string]any
	if json.Unmarshal(raw, &claims) != nil {
		return nil
	}
	return claims
}
