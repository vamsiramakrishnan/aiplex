package connections

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/vamsiramakrishnan/aiplex/internal/secrets"
	"github.com/vamsiramakrishnan/aiplex/pkg/toolauth"
)

// SecretToolSessions persists OAuth runtime secrets in version-specific Secret
// Manager references. The resource store contains only these opaque references.
type SecretToolSessions struct{ Manager *secrets.Manager }
type toolSecretEnvelope struct {
	Binding      string `json:"binding"`
	Verifier     string `json:"verifier,omitempty"`
	AccessToken  string `json:"access_token,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
}

func (s SecretToolSessions) Seal(ctx context.Context, binding string, value toolauth.SessionSecrets) (string, error) {
	if s.Manager == nil || len(binding) != 64 {
		return "", fmt.Errorf("session secret manager required")
	}
	var nonce [16]byte
	if _, e := rand.Read(nonce[:]); e != nil {
		return "", e
	}
	name := toolSessionSecretID(binding, nonce[:])
	data, e := json.Marshal(toolSecretEnvelope{binding, value.Verifier, value.AccessToken, value.RefreshToken})
	if e != nil {
		return "", e
	}
	return s.Manager.PutVersion(ctx, name, data)
}
// toolSessionSecretID names the Secret Manager secret for one sealed session.
// Like every secret ID the API uses it starts with SecretIDPrefix, which the
// Terraform IAM condition on the API's secretAccessor binding relies on.
func toolSessionSecretID(binding string, nonce []byte) string {
	hash := sha256.Sum256(append([]byte(binding), nonce...))
	return SecretIDPrefix + "tool-session-" + hex.EncodeToString(hash[:])
}

func (s SecretToolSessions) Open(ctx context.Context, binding, ref string) (toolauth.SessionSecrets, error) {
	if s.Manager == nil || !strings.Contains(ref, "/secrets/aiplex-tool-session-") {
		return toolauth.SessionSecrets{}, fmt.Errorf("session secret reference denied")
	}
	raw, e := s.Manager.GetVersion(ctx, ref)
	if e != nil {
		return toolauth.SessionSecrets{}, fmt.Errorf("session secret unavailable")
	}
	var value toolSecretEnvelope
	if json.Unmarshal(raw, &value) != nil || value.Binding != binding {
		return toolauth.SessionSecrets{}, fmt.Errorf("session secret binding mismatch")
	}
	return toolauth.SessionSecrets{Verifier: value.Verifier, AccessToken: value.AccessToken, RefreshToken: value.RefreshToken}, nil
}
func (s SecretToolSessions) Destroy(ctx context.Context, ref string) error {
	if s.Manager == nil || !strings.Contains(ref, "/secrets/aiplex-tool-session-") {
		return fmt.Errorf("session secret reference denied")
	}
	return s.Manager.DestroyVersion(ctx, ref)
}
