package deploy

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v3"
)

// LiveK8sClient applies manifests to a real K8s cluster by writing YAML
// and invoking kubectl via exec. This avoids the heavy client-go dependency
// while providing production-grade server-side apply semantics.
//
// When running in-cluster (GKE), kubectl is available in the pod and uses
// the mounted ServiceAccount token. For local dev, it uses ~/.kube/config.
type LiveK8sClient struct {
	kubeconfig string
	dryRun     bool
}

// LiveK8sOption configures the live K8s client.
type LiveK8sOption func(*LiveK8sClient)

// WithKubeconfig sets a custom kubeconfig path.
func WithKubeconfig(path string) LiveK8sOption {
	return func(c *LiveK8sClient) { c.kubeconfig = path }
}

// WithDryRun enables dry-run mode (generates manifests without applying).
func WithDryRun(dryRun bool) LiveK8sOption {
	return func(c *LiveK8sClient) { c.dryRun = dryRun }
}

// NewLiveK8sClientConfigured creates a K8s client with options.
func NewLiveK8sClientConfigured(opts ...LiveK8sOption) *LiveK8sClient {
	c := &LiveK8sClient{}
	for _, opt := range opts {
		opt(c)
	}
	if c.kubeconfig == "" {
		// Try in-cluster first, fall back to default kubeconfig
		if _, err := os.Stat("/var/run/secrets/kubernetes.io/serviceaccount/token"); err == nil {
			// Running in-cluster — kubectl uses SA token automatically
		} else {
			home, _ := os.UserHomeDir()
			c.kubeconfig = filepath.Join(home, ".kube", "config")
		}
	}
	return c
}

// CapabilityDiscoveryEnabled permits probes only after a real apply. A dry-run
// does not create workloads, so discovery would target nonexistent services.
func (c *LiveK8sClient) CapabilityDiscoveryEnabled() bool { return !c.dryRun }

// Apply creates or updates a K8s resource using server-side apply.
func (c *LiveK8sClient) Apply(ctx context.Context, m Manifest) error {
	yamlBytes, err := marshalManifest(m)
	if err != nil {
		return fmt.Errorf("marshal manifest %s/%s: %w", m.Kind, m.Name, err)
	}

	logger := log.Ctx(ctx).With().
		Str("kind", m.Kind).
		Str("name", m.Name).
		Str("namespace", m.Namespace).
		Logger()

	if c.dryRun {
		logger.Info().Msg("k8s apply (dry-run)")
		return nil
	}

	// Write YAML to temp file for kubectl apply
	tmpFile, err := os.CreateTemp("", "aiplex-manifest-*.yaml")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.Write(yamlBytes); err != nil {
		tmpFile.Close()
		return fmt.Errorf("write manifest: %w", err)
	}
	tmpFile.Close()

	args := []string{"apply", "--server-side", "--force-conflicts", "-f", tmpFile.Name()}
	if m.Namespace != "" {
		args = append(args, "-n", m.Namespace)
	}
	if c.kubeconfig != "" {
		args = append(args, "--kubeconfig", c.kubeconfig)
	}

	out, err := execCommand(ctx, "kubectl", args...)
	if err != nil {
		logger.Error().Err(err).Str("output", out).Msg("k8s apply failed")
		return fmt.Errorf("kubectl apply %s/%s: %s: %w", m.Kind, m.Name, out, err)
	}

	logger.Info().Msg("k8s apply succeeded")
	return nil
}

// Delete removes a K8s resource.
func (c *LiveK8sClient) Delete(ctx context.Context, apiVersion, kind, name, namespace string) error {
	logger := log.Ctx(ctx).With().
		Str("kind", kind).
		Str("name", name).
		Str("namespace", namespace).
		Logger()

	if c.dryRun {
		logger.Info().Msg("k8s delete (dry-run)")
		return nil
	}

	args := []string{"delete", kind, name, "--ignore-not-found"}
	if namespace != "" {
		args = append(args, "-n", namespace)
	}
	if c.kubeconfig != "" {
		args = append(args, "--kubeconfig", c.kubeconfig)
	}

	out, err := execCommand(ctx, "kubectl", args...)
	if err != nil {
		logger.Warn().Err(err).Str("output", out).Msg("k8s delete failed")
		return fmt.Errorf("kubectl delete %s/%s: %s: %w", kind, name, out, err)
	}

	logger.Info().Msg("k8s delete succeeded")
	return nil
}

// marshalManifest converts a Manifest to YAML bytes.
func marshalManifest(m Manifest) ([]byte, error) {
	if strings.TrimSpace(m.APIVersion) == "" || strings.TrimSpace(m.Kind) == "" || strings.TrimSpace(m.Name) == "" {
		return nil, fmt.Errorf("manifest requires apiVersion, kind, and name")
	}
	doc := map[string]any{
		"apiVersion": m.APIVersion,
		"kind":       m.Kind,
		"metadata": map[string]any{
			"name":      m.Name,
			"namespace": m.Namespace,
		},
	}

	// A module owns exactly the resource identified by Manifest. Never apply
	// undecodable raw content, accept extra documents, or permit an embedded
	// document to redirect an operation to a different kind/name/namespace.
	if strings.TrimSpace(m.YAML) != "" {
		var content map[string]any
		decoder := yaml.NewDecoder(strings.NewReader(m.YAML))
		if err := decoder.Decode(&content); err != nil {
			return nil, fmt.Errorf("invalid manifest YAML: %w", err)
		}
		if content == nil {
			return nil, fmt.Errorf("manifest must be an object")
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			return nil, fmt.Errorf("manifest must contain exactly one YAML document")
		}
		for field, expected := range map[string]string{"apiVersion": m.APIVersion, "kind": m.Kind} {
			if value, present := content[field]; present {
				if actual, ok := value.(string); !ok || actual != expected {
					return nil, fmt.Errorf("manifest %s does not match declared resource", field)
				}
			}
		}
		metadata := map[string]any{"name": m.Name, "namespace": m.Namespace}
		if value, present := content["metadata"]; present {
			actual, ok := value.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("manifest metadata must be an object")
			}
			for field, expected := range map[string]string{"name": m.Name, "namespace": m.Namespace} {
				if value, present := actual[field]; present {
					if text, ok := value.(string); !ok || text != expected {
						return nil, fmt.Errorf("manifest metadata.%s does not match declared resource", field)
					}
				}
			}
			for key, value := range actual {
				metadata[key] = value
			}
		}
		// Merge content into doc
		for k, v := range content {
			doc[k] = v
		}
		doc["metadata"] = metadata
	}

	return yaml.Marshal(doc)
}

// execCommand runs an external command and returns its combined output.
func execCommand(ctx context.Context, name string, args ...string) (string, error) {
	// Import os/exec inline to avoid global dependency
	cmd := execCommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}
