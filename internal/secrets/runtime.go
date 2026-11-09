package secrets

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// GetVersion reads an exact immutable version, never a mutable latest alias.
func (m *Manager) GetVersion(ctx context.Context, name string) ([]byte, error) {
	if m == nil || m.client == nil || !m.exactSecretVersion(name) {
		return nil, fmt.Errorf("approved exact secret version required")
	}
	result, e := m.client.AccessSecretVersion(ctx, &secretmanagerpb.AccessSecretVersionRequest{Name: name})
	if e != nil {
		return nil, e
	}
	if result.Payload == nil {
		return nil, fmt.Errorf("empty secret payload")
	}
	return append([]byte(nil), result.Payload.Data...), nil
}
func (m *Manager) DestroyVersion(ctx context.Context, name string) error {
	if m == nil || m.client == nil || !m.exactSecretVersion(name) {
		return fmt.Errorf("approved exact secret version required")
	}
	_, e := m.client.DestroySecretVersion(ctx, &secretmanagerpb.DestroySecretVersionRequest{Name: name})
	if e == nil || status.Code(e) == codes.NotFound {
		return nil
	}
	if status.Code(e) == codes.FailedPrecondition {
		current, readErr := m.client.GetSecretVersion(ctx, &secretmanagerpb.GetSecretVersionRequest{Name: name})
		if readErr == nil && current.State == secretmanagerpb.SecretVersion_DESTROYED {
			return nil
		}
	}
	return e
}

func (m *Manager) exactSecretVersion(name string) bool {
	if m == nil {
		return false
	}
	parts := strings.Split(name, "/")
	if len(parts) != 6 || parts[0] != "projects" || parts[1] != m.projectID || parts[2] != "secrets" || parts[3] == "" || parts[4] != "versions" {
		return false
	}
	version, err := strconv.ParseUint(parts[5], 10, 63)
	return err == nil && version > 0 && strconv.FormatUint(version, 10) == parts[5]
}
