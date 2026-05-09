package deploy

import (
	"fmt"

	"github.com/vamsiramakrishnan/aiplex/internal/capability"
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
// kinds tool/task/skill/memory: ServiceAccount, Deployment, Service, NetworkPolicy.
// kind=model: no K8s workloads (Envoy handles model routing directly).
// kind=meta: no workloads (AIPlex API serves these itself).
func GenerateManifests(inst *models.Instance, tmpl *models.Template, trustDomain string) []Manifest {
	if inst.Kind == capability.KindModel || inst.Kind == capability.KindMeta {
		return nil
	}

	ns := inst.Namespace
	name := inst.ID
	image := tmpl.Image
	if image == "" {
		image = fmt.Sprintf("gcr.io/aiplex/%s:latest", tmpl.ID)
	}

	spiffeID := fmt.Sprintf("spiffe://%s/ns/%s/sa/%s", trustDomain, ns, name)

	return []Manifest{
		serviceAccount(name, ns, spiffeID),
		deployment(name, ns, image, inst.Config, inst.Replicas),
		service(name, ns),
		networkPolicy(name, ns),
	}
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

func deployment(name, ns, image string, config map[string]any, replicas int) Manifest {
	if replicas <= 0 {
		replicas = 1
	}

	// Build env vars from config
	envYAML := ""
	for k, v := range config {
		envYAML += fmt.Sprintf("        - name: %s\n          value: \"%v\"\n", k, v)
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
