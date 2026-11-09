package toolauth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"
)

// MemoryCredentials is a development/test backend. Production hosts supply a
// credential-store adapter backed by their secret manager, not resource JSON.
type MemoryCredentials struct {
	mu      sync.RWMutex
	records map[CredentialKey]Credential
}

func NewMemoryCredentials() *MemoryCredentials {
	return &MemoryCredentials{records: map[CredentialKey]Credential{}}
}
func cloneCredential(c Credential) Credential {
	c.RedirectURIs = append([]string(nil), c.RedirectURIs...)
	c.GrantTypes = append([]string(nil), c.GrantTypes...)
	return c
}
func credentialReference(key CredentialKey) string {
	data, _ := json.Marshal(key)
	sum := sha256.Sum256(data)
	return "oauth-registration/" + hex.EncodeToString(sum[:])
}
func (m *MemoryCredentials) Get(ctx context.Context, key CredentialKey) (Credential, string, error) {
	if e := ctx.Err(); e != nil {
		return Credential{}, "", e
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	c, ok := m.records[key]
	if !ok {
		return Credential{}, "", ErrCredentialsNotFound
	}
	return cloneCredential(c), credentialReference(key), nil
}
func (m *MemoryCredentials) Put(ctx context.Context, key CredentialKey, c Credential) (string, error) {
	if e := ctx.Err(); e != nil {
		return "", e
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.records == nil {
		m.records = map[CredentialKey]Credential{}
	}
	m.records[key] = cloneCredential(c)
	return credentialReference(key), nil
}
