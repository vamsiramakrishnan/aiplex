package cloudagents

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
)

// SessionStore binds provider-generated DirectLine conversation IDs to the
// initiating principal. Production stores should persist this mapping. Unknown
// conversations fail closed after restart; they are never adopted from a caller.
type SessionStore interface {
	Bind(context.Context, string, string, string, string) error
	Check(context.Context, string, string, string, string) error
}
type MemorySessionStore struct {
	mu     sync.Mutex
	owners map[string]string
}

func NewMemorySessionStore() *MemorySessionStore {
	return &MemorySessionStore{owners: map[string]string{}}
}
func sessionKey(tenant, connection, session string) string {
	return tenant + "\x00" + connection + "\x00" + session
}
func (s *MemorySessionStore) Bind(_ context.Context, tenant, principal, connection, session string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := sessionKey(tenant, connection, session)
	if owner := s.owners[key]; owner != "" && owner != principal {
		return fmt.Errorf("conversation belongs to another principal")
	}
	s.owners[key] = principal
	return nil
}
func (s *MemorySessionStore) Check(_ context.Context, tenant, principal, connection, session string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.owners[sessionKey(tenant, connection, session)] != principal {
		return fmt.Errorf("unknown conversation or conversation belongs to another principal")
	}
	return nil
}
func scopedSession(tenant, principal, connection, logical string) string {
	digest := sha256.Sum256([]byte(sessionKey(tenant, connection, principal) + "\x00" + logical))
	return "aiplex-" + hex.EncodeToString(digest[:])
}

type Option func(*Invoker)

func WithSessionStore(store SessionStore) Option { return func(v *Invoker) { v.sessions = store } }
