package deploy

import (
	"context"
	"fmt"

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

// LiveK8sClient uses client-go to interact with a real K8s cluster.
// It applies manifests via server-side apply using dynamic client.
type LiveK8sClient struct {
	// In production, this would hold:
	// dynamicClient dynamic.Interface
	// discoveryClient discovery.DiscoveryInterface
	// mapper meta.RESTMapper
}

// NewLiveK8sClient creates a client-go backed K8s client.
// For now, returns a stub — will be fully wired when client-go is added.
func NewLiveK8sClient() (*LiveK8sClient, error) {
	// TODO: wire up client-go
	// config, err := rest.InClusterConfig()
	// if err != nil {
	//     config, err = clientcmd.BuildConfigFromFlags("", filepath.Join(homedir.HomeDir(), ".kube", "config"))
	// }
	// dynamicClient, err := dynamic.NewForConfig(config)
	// discoveryClient, err := discovery.NewDiscoveryClientForConfig(config)
	return &LiveK8sClient{}, nil
}

func (l *LiveK8sClient) Apply(ctx context.Context, m Manifest) error {
	// TODO: implement with server-side apply
	// 1. Parse YAML to unstructured.Unstructured
	// 2. Resolve GVR from apiVersion + kind using discovery
	// 3. Use dynamicClient.Resource(gvr).Namespace(ns).Apply()
	return fmt.Errorf("live K8s client not yet implemented — use NoOpK8sClient for development")
}

func (l *LiveK8sClient) Delete(ctx context.Context, apiVersion, kind, name, namespace string) error {
	// TODO: implement with dynamic client
	return fmt.Errorf("live K8s client not yet implemented — use NoOpK8sClient for development")
}
