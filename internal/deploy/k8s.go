package deploy

import (
	"context"

	"github.com/rs/zerolog/log"
)

// K8sClient abstracts Kubernetes operations.
// Production implementation uses client-go; tests use the no-op stub.
type K8sClient interface {
	// Apply creates or updates a K8s resource from YAML.
	Apply(ctx context.Context, manifest Manifest) error
	// Delete removes a K8s resource.
	Delete(ctx context.Context, apiVersion, kind, name, namespace string) error
}

// NoOpK8sClient is a stub that logs operations without touching a real cluster.
// Used for local development and testing.
type NoOpK8sClient struct{}

func NewNoOpK8sClient() *NoOpK8sClient {
	return &NoOpK8sClient{}
}

func (n *NoOpK8sClient) Apply(_ context.Context, m Manifest) error {
	log.Info().
		Str("kind", m.Kind).
		Str("name", m.Name).
		Str("namespace", m.Namespace).
		Msg("k8s apply (no-op)")
	return nil
}

func (n *NoOpK8sClient) Delete(_ context.Context, apiVersion, kind, name, namespace string) error {
	log.Info().
		Str("kind", kind).
		Str("name", name).
		Str("namespace", namespace).
		Msg("k8s delete (no-op)")
	return nil
}

// LiveK8sClient is implemented in k8s_live.go — applies manifests to a real
// cluster via kubectl with server-side apply semantics.
