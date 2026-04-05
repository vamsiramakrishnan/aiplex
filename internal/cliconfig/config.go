// Package cliconfig manages persistent CLI configuration in ~/.aiplex/.
//
// It supports multiple named contexts (dev, staging, prod) and stores
// credentials separately from config for security.
package cliconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Config is the top-level CLI configuration stored in ~/.aiplex/config.json.
type Config struct {
	CurrentContext string              `json:"current_context"`
	Contexts       map[string]*Context `json:"contexts"`
}

// Context holds connection settings for a single AIPlex environment.
type Context struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	Project string `json:"project,omitempty"`  // GCP project ID
	Region  string `json:"region,omitempty"`   // GCP region
	Domain  string `json:"domain,omitempty"`   // Custom domain
}

// Credentials stores auth tokens separately from config.
type Credentials struct {
	Tokens map[string]*TokenEntry `json:"tokens"` // keyed by context name
}

// TokenEntry holds a single context's auth credentials.
type TokenEntry struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	ExpiresAt    string `json:"expires_at,omitempty"`
	TokenType    string `json:"token_type,omitempty"`
}

var (
	ErrNoContext  = errors.New("no context set — run: aiplex config set-context <name>")
	ErrNotFound  = errors.New("context not found")
)

// Dir returns the ~/.aiplex directory path, creating it if needed.
func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home dir: %w", err)
	}
	dir := filepath.Join(home, ".aiplex")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("create config dir: %w", err)
	}
	return dir, nil
}

// Load reads the config from ~/.aiplex/config.json.
func Load() (*Config, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "config.json")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &Config{Contexts: make(map[string]*Context)}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if cfg.Contexts == nil {
		cfg.Contexts = make(map[string]*Context)
	}
	return &cfg, nil
}

// Save writes the config to ~/.aiplex/config.json.
func (c *Config) Save() error {
	dir, err := Dir()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	return os.WriteFile(filepath.Join(dir, "config.json"), data, 0600)
}

// Current returns the active context, or an error if none is set.
func (c *Config) Current() (*Context, error) {
	if c.CurrentContext == "" {
		return nil, ErrNoContext
	}
	ctx, ok := c.Contexts[c.CurrentContext]
	if !ok {
		return nil, fmt.Errorf("context %q: %w", c.CurrentContext, ErrNotFound)
	}
	return ctx, nil
}

// SetContext creates or updates a named context and makes it current.
func (c *Config) SetContext(name, url, project, region, domain string) {
	c.Contexts[name] = &Context{
		Name:    name,
		URL:     url,
		Project: project,
		Region:  region,
		Domain:  domain,
	}
	c.CurrentContext = name
}

// LoadCredentials reads tokens from ~/.aiplex/credentials.json.
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
	var creds Credentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, fmt.Errorf("parse credentials: %w", err)
	}
	if creds.Tokens == nil {
		creds.Tokens = make(map[string]*TokenEntry)
	}
	return &creds, nil
}

// Save writes credentials to ~/.aiplex/credentials.json with restricted permissions.
func (c *Credentials) Save() error {
	dir, err := Dir()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal credentials: %w", err)
	}
	return os.WriteFile(filepath.Join(dir, "credentials.json"), data, 0600)
}

// GetToken returns the token for the given context, or nil.
func (c *Credentials) GetToken(contextName string) *TokenEntry {
	if c == nil || c.Tokens == nil {
		return nil
	}
	return c.Tokens[contextName]
}

// SetToken stores a token for a context.
func (c *Credentials) SetToken(contextName string, entry *TokenEntry) {
	c.Tokens[contextName] = entry
}
