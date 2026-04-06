package deploy

import (
	"fmt"
	"strings"

	"github.com/vamsiramakrishnan/aiplex/internal/models"
)

// GenerateRoute produces the appropriate route CRD for the given plane.
// MCPlex → MCPRoute, A2APlex → HTTPRoute, LLMPlex → LLMRoute + AIServiceBackend.
func GenerateRoute(inst *models.Instance, tmpl *models.Template, gatewayName string) []Manifest {
	switch inst.Plane {
	case models.PlaneMCPlex:
		return []Manifest{mcpRoute(inst, gatewayName)}
	case models.PlaneA2APlex:
		return []Manifest{httpRoute(inst, gatewayName)}
	case models.PlaneLLMPlex:
		return llmRoute(inst, tmpl, gatewayName)
	default:
		return nil
	}
}

func mcpRoute(inst *models.Instance, gatewayName string) Manifest {
	return Manifest{
		APIVersion: "aigateway.envoyproxy.io/v1alpha1",
		Kind:       "MCPRoute",
		Name:       "mcp-" + inst.ID,
		Namespace:  inst.Namespace,
		YAML: fmt.Sprintf(`apiVersion: aigateway.envoyproxy.io/v1alpha1
kind: MCPRoute
metadata:
  name: mcp-%s
  namespace: %s
  labels:
    app.kubernetes.io/managed-by: aiplex
    aiplex.io/instance-id: %s
spec:
  parentRefs:
  - name: %s
    namespace: aiplex-system
  path: "/mcp/%s"
  backendRefs:
  - name: %s
    namespace: %s
    path: "/mcp"
  securityPolicy:
    oauth:
      issuer: "https://aiplex.example.com/auth/realms/aiplex"
`, inst.ID, inst.Namespace, inst.ID, gatewayName, inst.ID, inst.ID, inst.Namespace),
	}
}

func httpRoute(inst *models.Instance, gatewayName string) Manifest {
	return Manifest{
		APIVersion: "gateway.networking.k8s.io/v1",
		Kind:       "HTTPRoute",
		Name:       "a2a-" + inst.ID,
		Namespace:  inst.Namespace,
		YAML: fmt.Sprintf(`apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: a2a-%s
  namespace: %s
  labels:
    app.kubernetes.io/managed-by: aiplex
    aiplex.io/instance-id: %s
spec:
  parentRefs:
  - name: %s
    namespace: aiplex-system
  rules:
  - matches:
    - path:
        type: PathPrefix
        value: /a2a/%s
    backendRefs:
    - name: %s
      namespace: %s
      port: 8080
`, inst.ID, inst.Namespace, inst.ID, gatewayName, inst.ID, inst.ID, inst.Namespace),
	}
}

func llmRoute(inst *models.Instance, tmpl *models.Template, gatewayName string) []Manifest {
	provider := tmpl.Provider
	if provider == "" {
		provider = "google"
	}
	modelID := tmpl.ModelID
	backendName := inst.ID + "-backend"

	return []Manifest{
		{
			APIVersion: "aigateway.envoyproxy.io/v1alpha1",
			Kind:       "LLMRoute",
			Name:       "llm-" + inst.ID,
			Namespace:  "aiplex-system",
			YAML: fmt.Sprintf(`apiVersion: aigateway.envoyproxy.io/v1alpha1
kind: LLMRoute
metadata:
  name: llm-%s
  namespace: aiplex-system
  labels:
    app.kubernetes.io/managed-by: aiplex
    aiplex.io/instance-id: %s
spec:
  parentRefs:
  - name: %s
    namespace: aiplex-system
  rules:
  - matches:
    - headers:
      - name: x-model-id
        value: %s
    backendRefs:
    - name: %s
      weight: 100
`, inst.ID, inst.ID, gatewayName, modelID, backendName),
		},
		{
			APIVersion: "aigateway.envoyproxy.io/v1alpha1",
			Kind:       "AIServiceBackend",
			Name:       backendName,
			Namespace:  "aiplex-system",
			YAML: fmt.Sprintf(`apiVersion: aigateway.envoyproxy.io/v1alpha1
kind: AIServiceBackend
metadata:
  name: %s
  namespace: aiplex-system
  labels:
    app.kubernetes.io/managed-by: aiplex
    aiplex.io/instance-id: %s
spec:
  provider: %s
  model: %s
  apiKey:
    secretRef:
      name: %s-api-key
`, backendName, inst.ID, provider, modelID, provider),
		},
	}
}

// GenerateRoutesFromConfig creates Envoy LLMRoute + AIServiceBackend manifests
// from a route configuration with weighted backends and fallbacks.
func GenerateRoutesFromConfig(config *models.LLMRouteConfig, gatewayName string) []Manifest {
	var manifests []Manifest
	var backendRefs []string

	for _, backend := range config.Backends {
		if !backend.Enabled {
			continue
		}
		backendName := fmt.Sprintf("%s-%s-backend", config.ModelID, backend.Provider)

		backendYAML := fmt.Sprintf(`apiVersion: aigateway.envoyproxy.io/v1alpha1
kind: AIServiceBackend
metadata:
  name: %s
  namespace: aiplex-system
spec:
  provider: %s
  model: %s
  apiKey:
    secretRef:
      name: %s-api-key`, backendName, backend.Provider, backend.ModelID, backend.Provider)

		manifests = append(manifests, Manifest{
			APIVersion: "aigateway.envoyproxy.io/v1alpha1",
			Kind:       "AIServiceBackend",
			Name:       backendName,
			Namespace:  "aiplex-system",
			YAML:       backendYAML,
		})

		backendRefs = append(backendRefs, fmt.Sprintf("    - name: %s\n      weight: %d", backendName, backend.Weight))
	}

	routeYAML := fmt.Sprintf(`apiVersion: aigateway.envoyproxy.io/v1alpha1
kind: LLMRoute
metadata:
  name: llm-%s
  namespace: aiplex-system
spec:
  parentRefs:
    - name: %s
  rules:
    - matches:
        - headers:
            - name: x-model-id
              value: %s
      backendRefs:
%s`, config.ModelID, gatewayName, config.ModelID, strings.Join(backendRefs, "\n"))

	if len(config.Fallbacks) > 0 {
		var fallbackLines []string
		for _, fb := range config.Fallbacks {
			fallbackLines = append(fallbackLines, fmt.Sprintf("        - name: %s-backend", fb))
		}
		routeYAML += fmt.Sprintf("\n      fallback:\n%s", strings.Join(fallbackLines, "\n"))
	}

	manifests = append([]Manifest{{
		APIVersion: "aigateway.envoyproxy.io/v1alpha1",
		Kind:       "LLMRoute",
		Name:       "llm-" + config.ModelID,
		Namespace:  "aiplex-system",
		YAML:       routeYAML,
	}}, manifests...)

	return manifests
}
