package registry

import (
	"context"
	"errors"

	"cloud.google.com/go/firestore"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ReadIdentityAccess returns role and manual-grant policy from one authoritative
// store snapshot. It never persists derived grants or merges snapshots by time.
func (m *MemoryStore) ReadIdentityAccess(ctx context.Context, principal string) ([]models.RoleBinding, []string, int64, error) {
	if err := configurationIdentity(ConfigUserScopes, principal); err != nil {
		return nil, nil, 0, err
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, 0, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	bindings := make([]models.RoleBinding, 0, len(m.roleBindings))
	for _, binding := range m.roleBindings {
		bindings = append(bindings, *cloneAuthRoleBinding(binding))
	}
	return bindings, cloneAuthScopes(m.userScopes[principal]), m.configurationGeneration, nil
}
func (f *FirestoreStore) ReadIdentityAccess(ctx context.Context, principal string) ([]models.RoleBinding, []string, int64, error) {
	if err := configurationIdentity(ConfigUserScopes, principal); err != nil {
		return nil, nil, 0, err
	}
	var bindings []models.RoleBinding
	var manual []string
	var generation int64
	err := f.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		// Callback may retry: every attempt owns a new complete snapshot.
		bindings = []models.RoleBinding{}
		manual = []string{}
		generation = 0
		docs, err := tx.GetAll([]*firestore.DocumentRef{f.client.Collection("user_scopes").Doc(principal), f.client.Collection("configuration_revisions").Doc("head")})
		if err != nil {
			return err
		}
		if docs[0].Exists() {
			var scopes configurationScopes
			if err = docs[0].DataTo(&scopes); err != nil {
				return err
			}
			manual = append([]string{}, scopes.Scopes...)
		}
		if docs[1].Exists() {
			var head configurationHead
			if err = docs[1].DataTo(&head); err != nil {
				return err
			}
			if head.Generation < 0 {
				return ErrConfigurationConflict
			}
			generation = head.Generation
		}
		iter := tx.Documents(f.client.Collection("role_bindings"))
		defer iter.Stop()
		for {
			doc, err := iter.Next()
			if errors.Is(err, iterator.Done) {
				break
			}
			if err != nil {
				return err
			}
			var binding models.RoleBinding
			if err = doc.DataTo(&binding); err != nil {
				return err
			}
			normalizeConfigurationVersion(&binding)
			bindings = append(bindings, binding)
		}
		return nil
	}, firestore.ReadOnly)
	if status.Code(err) == codes.NotFound {
		return nil, nil, 0, ErrNotFound
	}
	return bindings, manual, generation, err
}
