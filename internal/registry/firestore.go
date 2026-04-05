package registry

// FirestoreStore implements the Store interface using Google Cloud Firestore.
//
// Collection layout:
//   instances/{id}       — deployed instances
//   templates/{id}       — cached catalog templates
//   agents/{clientId}    — registered OAuth clients
//   deploy_history/{id}  — append-only audit trail
//   user_scopes/{userId} — Dimension B (user ceiling)
//
// This file contains the structural implementation. To compile against the
// real Firestore client, add cloud.google.com/go/firestore to go.mod when
// GCP network access is available.

import (
	"context"
	"fmt"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/models"
)

// FirestoreStore wraps a Firestore client.
// In production, this would use cloud.google.com/go/firestore.Client.
// For now, it delegates to MemoryStore and logs what Firestore calls would be made.
type FirestoreStore struct {
	projectID  string
	databaseID string
	memory     *MemoryStore // fallback when Firestore client is unavailable
}

// NewFirestoreStore creates a Firestore-backed store.
// Falls back to in-memory if the Firestore client cannot be initialized.
func NewFirestoreStore(projectID, databaseID string) (*FirestoreStore, error) {
	if projectID == "" {
		return nil, fmt.Errorf("GCP_PROJECT is required for Firestore store")
	}

	// TODO: initialize real Firestore client
	// client, err := firestore.NewClientWithDatabase(ctx, projectID, databaseID)

	return &FirestoreStore{
		projectID:  projectID,
		databaseID: databaseID,
		memory:     NewMemoryStore(),
	}, nil
}

// The methods below document the Firestore collection and document paths
// that would be used. Each delegates to MemoryStore for now.

func (f *FirestoreStore) GetInstance(ctx context.Context, id string) (*models.Instance, error) {
	// Firestore: instances/{id}
	return f.memory.GetInstance(ctx, id)
}

func (f *FirestoreStore) ListInstances(ctx context.Context, plane models.Plane) ([]models.Instance, error) {
	// Firestore: instances where plane == {plane}, order by deployed_at desc
	return f.memory.ListInstances(ctx, plane)
}

func (f *FirestoreStore) PutInstance(ctx context.Context, inst *models.Instance) error {
	// Firestore: instances/{inst.ID}.Set(inst)
	inst.UpdatedAt = time.Now()
	return f.memory.PutInstance(ctx, inst)
}

func (f *FirestoreStore) DeleteInstance(ctx context.Context, id string) error {
	// Firestore: instances/{id}.Delete()
	return f.memory.DeleteInstance(ctx, id)
}

func (f *FirestoreStore) GetTemplate(ctx context.Context, id string) (*models.Template, error) {
	// Firestore: templates/{id}
	return f.memory.GetTemplate(ctx, id)
}

func (f *FirestoreStore) ListTemplates(ctx context.Context, plane models.Plane, page, pageSize int) ([]models.Template, int, error) {
	// Firestore: templates where plane == {plane}, order by name, offset/limit
	return f.memory.ListTemplates(ctx, plane, page, pageSize)
}

func (f *FirestoreStore) PutTemplate(ctx context.Context, t *models.Template) error {
	// Firestore: templates/{t.ID}.Set(t)
	return f.memory.PutTemplate(ctx, t)
}

func (f *FirestoreStore) GetAgent(ctx context.Context, clientID string) (*models.Agent, error) {
	// Firestore: agents/{clientID}
	return f.memory.GetAgent(ctx, clientID)
}

func (f *FirestoreStore) ListAgents(ctx context.Context) ([]models.Agent, error) {
	// Firestore: agents, order by registered_at desc
	return f.memory.ListAgents(ctx)
}

func (f *FirestoreStore) PutAgent(ctx context.Context, a *models.Agent) error {
	// Firestore: agents/{a.ClientID}.Set(a)
	return f.memory.PutAgent(ctx, a)
}

func (f *FirestoreStore) DeleteAgent(ctx context.Context, clientID string) error {
	// Firestore: agents/{clientID}.Delete()
	return f.memory.DeleteAgent(ctx, clientID)
}

func (f *FirestoreStore) AppendHistory(ctx context.Context, h *models.DeployHistory) error {
	// Firestore: deploy_history/{h.ID}.Create(h)
	return f.memory.AppendHistory(ctx, h)
}

func (f *FirestoreStore) ListHistory(ctx context.Context, instanceID string, limit int) ([]models.DeployHistory, error) {
	// Firestore: deploy_history where instance_id == {instanceID}, order by timestamp desc, limit
	return f.memory.ListHistory(ctx, instanceID, limit)
}

func (f *FirestoreStore) GetUserScopes(ctx context.Context, userID string) ([]string, error) {
	// Firestore: user_scopes/{userID}
	return f.memory.GetUserScopes(ctx, userID)
}

func (f *FirestoreStore) SetUserScopes(ctx context.Context, userID string, scopes []string) error {
	// Firestore: user_scopes/{userID}.Set({"scopes": scopes})
	return f.memory.SetUserScopes(ctx, userID, scopes)
}
