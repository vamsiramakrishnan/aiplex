package registry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/models"

	"cloud.google.com/go/firestore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type configurationHead struct{ Generation int64 }

func configurationCollection(kind string) string {
	switch kind {
	case ConfigAgent:
		return "agents"
	case ConfigRoute:
		return "route_configs"
	case ConfigProvider:
		return "provider_configs"
	case ConfigRoleBinding:
		return "role_bindings"
	case ConfigUserScopes:
		return "user_scopes"
	}
	return ""
}
func (f *FirestoreStore) configurationRefs(kind, id string) (*firestore.DocumentRef, *firestore.DocumentRef, *firestore.DocumentRef) {
	digest := sha256.Sum256([]byte(configurationKey(kind, id)))
	return f.client.Collection(configurationCollection(kind)).Doc(id), f.client.Collection("configuration_revisions").Doc(hex.EncodeToString(digest[:])), f.client.Collection("configuration_revisions").Doc("head")
}
func configurationReadSnapshots(kind, id string, docs []*firestore.DocumentSnapshot) (ConfigurationRecord, configurationRevision, configurationHead, error) {
	var revision configurationRevision
	var head configurationHead
	if docs[1].Exists() {
		if err := docs[1].DataTo(&revision); err != nil {
			return ConfigurationRecord{}, revision, head, err
		}
	}
	if len(docs) > 2 && docs[2].Exists() {
		if err := docs[2].DataTo(&head); err != nil {
			return ConfigurationRecord{}, revision, head, err
		}
	}
	if head.Generation < 0 || revision.Revision < 0 {
		return ConfigurationRecord{}, revision, head, ErrConfigurationConflict
	}
	if !docs[0].Exists() {
		if docs[1].Exists() && !revision.Deleted {
			return ConfigurationRecord{}, revision, head, ErrConfigurationConflict
		}
		return ConfigurationRecord{}, revision, head, ErrNotFound
	}
	if revision.Deleted {
		return ConfigurationRecord{}, revision, head, ErrConfigurationConflict
	}
	value := configurationValue(kind)
	if err := docs[0].DataTo(value); err != nil {
		return ConfigurationRecord{}, revision, head, err
	}
	if docs[1].Exists() && configurationVersion(value) != revision.Revision {
		return ConfigurationRecord{}, revision, head, ErrConfigurationConflict
	}
	normalizeConfigurationVersion(value)
	record, err := configurationRecord(kind, id, value)
	return record, revision, head, err
}
func (f *FirestoreStore) ConfigurationGeneration(ctx context.Context) (int64, error) {
	var generation int64
	err := f.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		doc, err := tx.Get(f.client.Collection("configuration_revisions").Doc("head"))
		if status.Code(err) == codes.NotFound {
			return nil
		}
		if err != nil {
			return err
		}
		var head configurationHead
		if err = doc.DataTo(&head); err != nil {
			return err
		}
		if head.Generation < 0 {
			return ErrConfigurationConflict
		}
		generation = head.Generation
		return nil
	}, firestore.ReadOnly)
	return generation, err
}

func (f *FirestoreStore) GetConfiguration(ctx context.Context, kind, id string) (ConfigurationRecord, error) {
	if err := configurationIdentity(kind, id); err != nil {
		return ConfigurationRecord{}, err
	}
	native, revision, _ := f.configurationRefs(kind, id)
	var out ConfigurationRecord
	err := f.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		docs, err := tx.GetAll([]*firestore.DocumentRef{native, revision})
		if err != nil {
			return err
		}
		out, _, _, err = configurationReadSnapshots(kind, id, docs)
		return err
	}, firestore.ReadOnly)
	return out, err
}
func (f *FirestoreStore) mutateConfiguration(ctx context.Context, record ConfigurationRecord, expected, generation int64, deleting bool, modify func([]string) ([]string, error)) (ConfigurationRecord, error) {
	if err := configurationIdentity(record.Kind, record.ID); err != nil {
		return ConfigurationRecord{}, err
	}
	if err := validateConfigurationWrite(expected, generation); err != nil {
		return ConfigurationRecord{}, err
	}
	native, revision, headRef := f.configurationRefs(record.Kind, record.ID)
	var out ConfigurationRecord
	err := f.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		docs, err := tx.GetAll([]*firestore.DocumentRef{native, revision, headRef})
		if err != nil {
			return err
		}
		current, meta, head, err := configurationReadSnapshots(record.Kind, record.ID, docs)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		if deleting && errors.Is(err, ErrNotFound) {
			return ErrNotFound
		}
		wanted := expected
		candidate := record
		if modify != nil {
			var value configurationScopes
			if len(current.Spec) > 0 {
				if err := json.Unmarshal(current.Spec, &value); err != nil {
					return err
				}
			}
			scopes, err := modify(append([]string{}, value.Scopes...))
			if err != nil {
				return err
			}
			candidate.Spec, err = json.Marshal(configurationScopes{Scopes: scopes})
			if err != nil {
				return err
			}
			wanted = current.Revision
		}
		if current.Revision != wanted || (generation >= 0 && head.Generation != generation) || (deleting && expected == 0) {
			return ErrConfigurationConflict
		}
		last := current.Revision
		if meta.Revision > last {
			last = meta.Revision
		}
		next, err := nextConfigurationRevision(last)
		if err != nil {
			return err
		}
		if head.Generation == math.MaxInt64 {
			return ErrConfigurationConflict
		}
		if deleting {
			if err := tx.Delete(native); err != nil {
				return err
			}
		} else {
			value, err := decodeConfiguration(candidate, next)
			if err != nil {
				return err
			}
			if route, ok := value.(*models.LLMRouteConfig); ok {
				route.UpdatedAt = time.Now()
				if route.CreatedAt.IsZero() {
					route.CreatedAt = route.UpdatedAt
				}
			}
			out, err = configurationRecord(record.Kind, record.ID, value)
			if err != nil {
				return err
			}
			if err := tx.Set(native, value); err != nil {
				return err
			}
		}
		if err := tx.Set(revision, configurationRevision{Revision: next, Deleted: deleting}); err != nil {
			return err
		}
		return tx.Set(headRef, configurationHead{Generation: head.Generation + 1})
	})
	if err != nil {
		return ConfigurationRecord{}, fmt.Errorf("configuration mutation: %w", err)
	}
	return out, nil
}
func (f *FirestoreStore) PutConfiguration(ctx context.Context, record ConfigurationRecord, expected, generation int64) (ConfigurationRecord, error) {
	return f.mutateConfiguration(ctx, record, expected, generation, false, nil)
}
func (f *FirestoreStore) DeleteConfiguration(ctx context.Context, kind, id string, expected, generation int64) error {
	_, err := f.mutateConfiguration(ctx, ConfigurationRecord{Kind: kind, ID: id}, expected, generation, true, nil)
	return err
}
func (f *FirestoreStore) ModifyUserScopes(ctx context.Context, id string, modify func([]string) ([]string, error)) error {
	if modify == nil {
		return fmt.Errorf("scope mutation required")
	}
	_, err := f.mutateConfiguration(ctx, ConfigurationRecord{Kind: ConfigUserScopes, ID: id}, 0, -1, false, modify)
	return err
}

var _ VersionedConfigurationStore = (*FirestoreStore)(nil)
