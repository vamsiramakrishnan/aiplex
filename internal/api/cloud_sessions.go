package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
)

type cloudSessions struct{ store resources.Store }

func conversationKey(connection, session string) string {
	b, _ := json.Marshal([]string{connection, session})
	d := sha256.Sum256(b)
	return hex.EncodeToString(d[:])
}
func (s cloudSessions) Bind(ctx context.Context, t, p, c, id string) error {
	body, _ := json.Marshal(map[string]string{"principal_id": p, "connection_id": c, "session_id": id})
	_, e := s.store.Put(ctx, resources.Record{TenantID: t, Kind: "agent-session-owner", ID: conversationKey(c, id), Spec: body}, 0)
	if errors.Is(e, resources.ErrConflict) {
		return s.Check(ctx, t, p, c, id)
	}
	return e
}
func (s cloudSessions) Check(ctx context.Context, t, p, c, id string) error {
	rec, e := s.store.Get(ctx, t, "agent-session-owner", conversationKey(c, id))
	if e != nil {
		return e
	}
	var owner map[string]string
	if e = json.Unmarshal(rec.Spec, &owner); e != nil {
		return e
	}
	if owner["principal_id"] != p || owner["connection_id"] != c || owner["session_id"] != id {
		return fmt.Errorf("conversation owner mismatch")
	}
	return nil
}
