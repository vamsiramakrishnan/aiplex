package secrets

import (
	"context"
	"fmt"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
)

// Manager wraps GCP Secret Manager for validating and reading secrets.
type Manager struct {
	client    *secretmanager.Client
	projectID string
}

// NewManager creates a Secret Manager client.
// Returns nil manager (no-op) if projectID is empty (local dev mode).
func NewManager(ctx context.Context, projectID string) (*Manager, error) {
	if projectID == "" {
		return nil, nil
	}
	client, err := secretmanager.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("secret manager client: %w", err)
	}
	return &Manager{client: client, projectID: projectID}, nil
}

// Close releases the Secret Manager client resources.
func (m *Manager) Close() error {
	if m == nil || m.client == nil {
		return nil
	}
	return m.client.Close()
}

// Exists checks if a secret exists in Secret Manager.
func (m *Manager) Exists(ctx context.Context, secretID string) (bool, error) {
	if m == nil {
		return true, nil // no-op in local dev
	}
	name := fmt.Sprintf("projects/%s/secrets/%s", m.projectID, secretID)
	_, err := m.client.GetSecret(ctx, &secretmanagerpb.GetSecretRequest{Name: name})
	if err != nil {
		return false, nil
	}
	return true, nil
}

// GetLatest reads the latest version of a secret.
func (m *Manager) GetLatest(ctx context.Context, secretID string) ([]byte, error) {
	if m == nil {
		return nil, fmt.Errorf("secret manager not configured")
	}
	name := fmt.Sprintf("projects/%s/secrets/%s/versions/latest", m.projectID, secretID)
	result, err := m.client.AccessSecretVersion(ctx, &secretmanagerpb.AccessSecretVersionRequest{Name: name})
	if err != nil {
		return nil, fmt.Errorf("access secret %q: %w", secretID, err)
	}
	return result.Payload.Data, nil
}

// Create creates a new secret and adds the first version with the given data.
func (m *Manager) Create(ctx context.Context, secretID string, data []byte) error {
	if m == nil {
		return nil // no-op in local dev
	}

	// Create the secret
	name := fmt.Sprintf("projects/%s", m.projectID)
	secret, err := m.client.CreateSecret(ctx, &secretmanagerpb.CreateSecretRequest{
		Parent:   name,
		SecretId: secretID,
		Secret: &secretmanagerpb.Secret{
			Replication: &secretmanagerpb.Replication{
				Replication: &secretmanagerpb.Replication_Automatic_{
					Automatic: &secretmanagerpb.Replication_Automatic{},
				},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("create secret %q: %w", secretID, err)
	}

	// Add the version
	_, err = m.client.AddSecretVersion(ctx, &secretmanagerpb.AddSecretVersionRequest{
		Parent: secret.Name,
		Payload: &secretmanagerpb.SecretPayload{
			Data: data,
		},
	})
	if err != nil {
		return fmt.Errorf("add secret version %q: %w", secretID, err)
	}
	return nil
}
