package connections

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/toolcredential"
	"golang.org/x/oauth2"
	"google.golang.org/api/idtoken"
)

// SecretReader is the subset of the Secret Manager wrapper tool secrets use.
type SecretReader interface {
	GetLatest(context.Context, string) ([]byte, error)
	Exists(context.Context, string) (bool, error)
}

// ToolSecrets resolves tool connection secret references in a tenant-scoped
// Secret Manager namespace: reference "name" for tenant T is the secret
// "aiplex-tool-<first 16 hex of sha256(T)>-name". A tenant therefore cannot
// reference another tenant's secrets or arbitrary project secrets.
type ToolSecrets struct{ Manager SecretReader }

func toolSecretPrefix(tenant string) string {
	sum := sha256.Sum256([]byte(tenant))
	return "aiplex-tool-" + hex.EncodeToString(sum[:8]) + "-"
}

// ToolSecretID is the Secret Manager secret ID for a tenant's reference.
func ToolSecretID(tenant, ref string) string { return toolSecretPrefix(tenant) + ref }

func (s ToolSecrets) Namespace(tenant string) string { return toolSecretPrefix(tenant) }

func (s ToolSecrets) Resolve(ctx context.Context, tenant, ref string) (string, error) {
	if s.Manager == nil || tenant == "" || !toolcredential.ValidSecretRef(ref) {
		return "", fmt.Errorf("tool secret unavailable")
	}
	value, err := s.Manager.GetLatest(ctx, ToolSecretID(tenant, ref))
	if err != nil {
		return "", fmt.Errorf("tool secret unavailable")
	}
	return string(value), nil
}

func (s ToolSecrets) Exists(ctx context.Context, tenant, ref string) (bool, error) {
	if s.Manager == nil || tenant == "" || !toolcredential.ValidSecretRef(ref) {
		return false, fmt.Errorf("tool secret storage unavailable")
	}
	return s.Manager.Exists(ctx, ToolSecretID(tenant, ref))
}

// DevelopmentToolSecrets resolves references from operator environment
// variables AIPLEX_TOOL_SECRET_<REF> (upper case, '-' as '_'). It is only for
// explicit development mode without Secret Manager and is not tenant-scoped.
type DevelopmentToolSecrets struct{}

func developmentSecretEnv(ref string) string {
	return "AIPLEX_TOOL_SECRET_" + strings.ToUpper(strings.ReplaceAll(ref, "-", "_"))
}

func (DevelopmentToolSecrets) Namespace(string) string { return "env:AIPLEX_TOOL_SECRET_" }

func (DevelopmentToolSecrets) Resolve(_ context.Context, _ string, ref string) (string, error) {
	if !toolcredential.ValidSecretRef(ref) {
		return "", fmt.Errorf("tool secret unavailable")
	}
	value := os.Getenv(developmentSecretEnv(ref))
	if value == "" {
		return "", fmt.Errorf("tool secret unavailable")
	}
	return value, nil
}

// IDToken issues a Google ID token for a fixed audience with the same
// Application Default Credentials an identity profile of type gcp-adc uses
// for access tokens. Sources are cached per tenant, profile and audience.
func (r *Runtime) IDToken(ctx context.Context, tenant, ref, audience string) (string, error) {
	identity, err := r.Identity(tenant, ref)
	if err != nil || identity.Type != "gcp-adc" {
		return "", fmt.Errorf("google ID tokens require a gcp-adc identity profile")
	}
	if audience == "" {
		return "", fmt.Errorf("fixed audience required")
	}
	key := "idtoken\x00" + tenant + "\x00" + ref + "\x00" + audience
	r.mu.Lock()
	source := r.sources[key]
	if source == nil {
		created, e := idtoken.NewTokenSource(context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Timeout: 10 * time.Second}), audience)
		if e != nil {
			r.mu.Unlock()
			return "", fmt.Errorf("Google ID token source unavailable")
		}
		source = oauth2.ReuseTokenSource(nil, created)
		r.sources[key] = source
	}
	r.mu.Unlock()
	token, err := source.Token()
	if err != nil || token.AccessToken == "" {
		return "", fmt.Errorf("Google ID token acquisition failed")
	}
	return token.AccessToken, nil
}
