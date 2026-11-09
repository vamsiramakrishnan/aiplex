package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/models"
)

type roleBindingWriter interface {
	PutRoleBinding(context.Context, *models.RoleBinding) error
}
type roleBindingFile struct {
	Bindings []models.RoleBinding `json:"role_bindings"`
}

// Bootstrap bindings are explicitly managed by the deployment operator. IDs,
// source namespaces and roles are validated before any write. This gives a
// fresh deployment an administrative entry point without an unauthenticated API.
func bootstrapRoleBindings(ctx context.Context, store roleBindingWriter, path string) error {
	if path == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	payload, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil {
		return err
	}
	if len(payload) > 1<<20 {
		return fmt.Errorf("role binding file exceeds 1MiB")
	}
	var file roleBindingFile
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&file); err != nil {
		return err
	}
	if err = decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("trailing role binding data")
	}
	seen := map[string]bool{}
	for _, b := range file.Bindings {
		if b.ID == "" || b.Group == "" || b.Provider == "" || b.PoolID == "" || seen[b.ID] {
			return fmt.Errorf("bootstrap binding requires unique id, group, provider and pool_id")
		}
		seen[b.ID] = true
		switch b.Role {
		case models.RoleAdmin, models.RoleDeployer, models.RoleViewer, models.RoleAgent:
		default:
			return fmt.Errorf("invalid bootstrap role %q", b.Role)
		}
	}
	for _, b := range file.Bindings {
		if b.CreatedAt.IsZero() {
			b.CreatedAt = time.Now().UTC()
		}
		b.CreatedBy = "deployment-operator"
		if err = store.PutRoleBinding(ctx, &b); err != nil {
			return fmt.Errorf("bootstrap binding %s: %w", b.ID, err)
		}
	}
	return nil
}
