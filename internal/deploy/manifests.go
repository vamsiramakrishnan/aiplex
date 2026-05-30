package deploy

import (
	"fmt"
	"sort"

	"github.com/vamsiramakrishnan/aiplex/internal/models"
)

// Manifest represents a K8s resource to be applied.
type Manifest struct {
	APIVersion string
	Kind       string
	Name       string
	Namespace  string
	YAML       string
}

// GenerateManifests produces all K8s resources needed for a deployment.
// For MCPlex/A2APlex: ServiceAccount, Deployment, Service, NetworkPolicy.
// For LLMPlex: no K8s workloads (Envoy handles model routing directly).
//
// When inst.Runtime.Engine == "tape" the result also includes the
// env-scoped tape-server + tape-reactors resources (idempotent on
// (Namespace, Name) so re-applying across multiple Tape-backed agents
// is a no-op after the first), and the agent's Deployment gets the
// AIPLEX_* + TAPE_URL env vars injected for the Python SDK's
// `RunIdentity.from_env()` to read.
func GenerateManifests(inst *models.Instance, tmpl *models.Template, trustDomain string) []Manifest {
	if inst.Plane == models.PlaneLLMPlex {
		return nil // Envoy AI Gateway handles LLM routing — no pods needed
	}

	ns := inst.Namespace
	name := inst.ID
	image := tmpl.Image
	if image == "" {
		image = fmt.Sprintf("gcr.io/aiplex/%s:latest", tmpl.ID)
	}

	spiffeID := fmt.Sprintf("spiffe://%s/ns/%s/sa/%s", trustDomain, ns, name)
	extraEnv := tapeAgentEnv(inst, "")
	configEnv := filterRuntimeEnv(inst.Config)

	manifests := []Manifest{
		serviceAccount(name, ns, spiffeID),
		deployment(name, ns, image, configEnv, extraEnv, inst.Replicas),
		service(name, ns),
		networkPolicy(name, ns),
	}
	manifests = append(manifests, tapeRuntimeManifests(inst)...)
	return manifests
}

// filterRuntimeEnv drops AIPlex-internal config keys from the template
// env-var loop so they don't land on the pod as confusing
// FORCE_RUNTIME_CHANGE=true env vars. The runtime config has its own
// dedicated path (tapeRuntimeManifests + tapeAgentEnv).
//
// Internal keys:
//   * runtime               — RuntimeConfig block (handled separately)
//   * force_runtime_change  — bypass flag for PR 11 item 16
func filterRuntimeEnv(config map[string]any) map[string]any {
	if config == nil {
		return nil
	}
	internal := map[string]struct{}{
		"runtime":              {},
		"force_runtime_change": {},
	}
	out := make(map[string]any, len(config))
	for k, v := range config {
		if _, isInternal := internal[k]; isInternal {
			continue
		}
		out[k] = v
	}
	return out
}

func serviceAccount(name, ns, spiffeID string) Manifest {
	return Manifest{
		APIVersion: "v1",
		Kind:       "ServiceAccount",
		Name:       name,
		Namespace:  ns,
		YAML: fmt.Sprintf(`apiVersion: v1
kind: ServiceAccount
metadata:
  name: %s
  namespace: %s
  annotations:
    iam.gke.io/gcp-service-account: %s@PROJECT_ID.iam.gserviceaccount.com
  labels:
    app.kubernetes.io/name: %s
    app.kubernetes.io/managed-by: aiplex
    aiplex.io/spiffe-id: "%s"
`, name, ns, name, name, spiffeID),
	}
}

func deployment(name, ns, image string, config map[string]any, extra map[string]string, replicas int) Manifest {
	if replicas <= 0 {
		replicas = 1
	}

	// Build env vars from config + extras. Sort both so the YAML is
	// byte-stable across deploys (otherwise map iteration order
	// produces noisy diffs in K8s `apply --diff`).
	envYAML := ""
	configKeys := make([]string, 0, len(config))
	for k := range config {
		configKeys = append(configKeys, k)
	}
	sort.Strings(configKeys)
	for _, k := range configKeys {
		envYAML += fmt.Sprintf("        - name: %s\n          value: \"%v\"\n", k, config[k])
	}
	extraKeys := make([]string, 0, len(extra))
	for k := range extra {
		extraKeys = append(extraKeys, k)
	}
	sort.Strings(extraKeys)
	for _, k := range extraKeys {
		envYAML += fmt.Sprintf("        - name: %s\n          value: %q\n", k, extra[k])
	}

	return Manifest{
		APIVersion: "apps/v1",
		Kind:       "Deployment",
		Name:       name,
		Namespace:  ns,
		YAML: fmt.Sprintf(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: %s
  namespace: %s
  labels:
    app.kubernetes.io/name: %s
    app.kubernetes.io/managed-by: aiplex
spec:
  replicas: %d
  selector:
    matchLabels:
      app.kubernetes.io/name: %s
  template:
    metadata:
      labels:
        app.kubernetes.io/name: %s
    spec:
      serviceAccountName: %s
      containers:
      - name: %s
        image: %s
        ports:
        - containerPort: 8080
          name: http
          protocol: TCP
        env:
%s        resources:
          requests:
            cpu: 100m
            memory: 128Mi
          limits:
            cpu: 500m
            memory: 512Mi
        livenessProbe:
          httpGet:
            path: /healthz
            port: http
          initialDelaySeconds: 5
          periodSeconds: 10
        readinessProbe:
          httpGet:
            path: /readyz
            port: http
          initialDelaySeconds: 3
          periodSeconds: 5
`, name, ns, name, replicas, name, name, name, name, image, envYAML),
	}
}

func service(name, ns string) Manifest {
	return Manifest{
		APIVersion: "v1",
		Kind:       "Service",
		Name:       name,
		Namespace:  ns,
		YAML: fmt.Sprintf(`apiVersion: v1
kind: Service
metadata:
  name: %s
  namespace: %s
  labels:
    app.kubernetes.io/name: %s
    app.kubernetes.io/managed-by: aiplex
spec:
  selector:
    app.kubernetes.io/name: %s
  ports:
  - name: http
    port: 8080
    targetPort: http
    protocol: TCP
`, name, ns, name, name),
	}
}

func networkPolicy(name, ns string) Manifest {
	return Manifest{
		APIVersion: "networking.k8s.io/v1",
		Kind:       "NetworkPolicy",
		Name:       name + "-ingress",
		Namespace:  ns,
		YAML: fmt.Sprintf(`apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: %s-ingress
  namespace: %s
  labels:
    app.kubernetes.io/managed-by: aiplex
spec:
  podSelector:
    matchLabels:
      app.kubernetes.io/name: %s
  policyTypes:
  - Ingress
  ingress:
  - from:
    - namespaceSelector:
        matchLabels:
          kubernetes.io/metadata.name: aiplex-system
    ports:
    - protocol: TCP
      port: 8080
`, name, ns, name),
	}
}
