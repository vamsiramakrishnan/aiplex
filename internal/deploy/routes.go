package deploy

import (
	"fmt"
	"strings"

	"github.com/vamsiramakrishnan/aiplex/internal/capability"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
)

// GenerateRoute produces the unified CapabilityRoute CRD for an instance.
// One CRD per capability the instance provides — all instances and all kinds
// flow through the same route shape, with a small per-kind override block for
// kind=model (LLM provider failover, semantic cache).
func GenerateRoute(inst *models.Instance, tmpl *models.Template, gatewayName string) []Manifest {
	caps := inst.Capabilities
	if len(caps) == 0 && tmpl != nil {
		caps = tmpl.CapSet()
	}
	if len(caps) == 0 {
		return nil
	}

	out := make([]Manifest, 0, len(caps))
	for _, c := range caps {
		out = append(out, capabilityRoute(inst, tmpl, c, gatewayName))
	}

	// Model kind needs an additional AIServiceBackend (Envoy AI Gateway primitive)
	// alongside the CapabilityRoute, until we replace LLMRoute end-to-end.
	if inst.Kind == capability.KindModel && tmpl != nil {
		out = append(out, modelBackend(inst, tmpl))
	}

	return out
}

// capabilityRoute renders one cap → one CapabilityRoute manifest.
func capabilityRoute(inst *models.Instance, tmpl *models.Template, c capability.Cap, gatewayName string) Manifest {
	uri, _ := capability.ParseURI(c.URI)
	name := fmt.Sprintf("cap-%s-%s", inst.ID, sanitize(uri.PathSegment()))
	if len(name) > 63 {
		name = name[:63]
	}

	actions := c.Actions
	if len(actions) == 0 {
		actions = capability.MustSpec(uri.Kind).Actions
	}
	actionsList := strings.Join(actions, ", ")

	pathTmpl := fmt.Sprintf("/cap/%s", uri.PathSegment())

	provider := inst.SpiffeID
	if provider == "" {
		provider = fmt.Sprintf("spiffe://aiplex/ns/%s/sa/%s", inst.Namespace, inst.ID)
	}

	overrides := ""
	if uri.Kind == capability.KindModel && tmpl != nil {
		overrides = fmt.Sprintf(`
  kindOverrides:
    model:
      modelId: %q
      provider: %q
      backendRef:
        name: %s-backend
`, tmpl.ModelID, tmpl.Provider, inst.ID)
	}

	return Manifest{
		APIVersion: "aiplex.dev/v1alpha1",
		Kind:       "CapabilityRoute",
		Name:       name,
		Namespace:  "aiplex-system",
		YAML: fmt.Sprintf(`apiVersion: aiplex.dev/v1alpha1
kind: CapabilityRoute
metadata:
  name: %s
  namespace: aiplex-system
  labels:
    app.kubernetes.io/managed-by: aiplex
    aiplex.io/instance-id: %s
    aiplex.io/cap-kind: %s
spec:
  capability:
    uri: %s
    kind: %s
    name: %s
    version: %s
    provider:
      spiffeId: %q
      kind: KubernetesService
      name: %s
      namespace: %s
      port: 8080
    auth:
      requiredActions: [%s]
  routing:
    parentRefs:
      - name: %s
        namespace: aiplex-system
    pathTemplate: %q
    timeout: 30s%s
`, name, inst.ID, uri.Kind, c.URI, uri.Kind, uri.Name, uri.Version,
			provider, inst.ID, inst.Namespace, actionsList,
			gatewayName, pathTmpl, overrides),
	}
}

// modelBackend emits the Envoy AIServiceBackend that the model CapabilityRoute
// references. Kept as a separate manifest so it can be GC-ed independently.
func modelBackend(inst *models.Instance, tmpl *models.Template) Manifest {
	provider := tmpl.Provider
	if provider == "" {
		provider = "google"
	}
	secret := provider + "-api-key"
	return Manifest{
		APIVersion: "aigateway.envoyproxy.io/v1alpha1",
		Kind:       "AIServiceBackend",
		Name:       inst.ID + "-backend",
		Namespace:  "aiplex-system",
		YAML: fmt.Sprintf(`apiVersion: aigateway.envoyproxy.io/v1alpha1
kind: AIServiceBackend
metadata:
  name: %s-backend
  namespace: aiplex-system
  labels:
    app.kubernetes.io/managed-by: aiplex
    aiplex.io/instance-id: %s
spec:
  provider: %s
  model: %s
  apiKey:
    secretRef:
      name: %s
`, inst.ID, inst.ID, provider, tmpl.ModelID, secret),
	}
}

// GenerateRoutesFromConfig produces failover-aware model routes from an
// LLMRouteConfig. Used by the LLM admin endpoints.
func GenerateRoutesFromConfig(config *models.LLMRouteConfig, gatewayName string) []Manifest {
	uri := capability.New(capability.KindModel, config.ModelID, "v1")
	name := "cap-llm-" + sanitize(config.ModelID)
	if len(name) > 63 {
		name = name[:63]
	}

	var backends []Manifest
	var weights []string
	for _, b := range config.Backends {
		if !b.Enabled {
			continue
		}
		backendName := fmt.Sprintf("%s-%s-backend", sanitize(config.ModelID), b.Provider)
		secret := b.SecretRef
		if secret == "" {
			secret = b.Provider + "-api-key"
		}
		backends = append(backends, Manifest{
			APIVersion: "aigateway.envoyproxy.io/v1alpha1",
			Kind:       "AIServiceBackend",
			Name:       backendName,
			Namespace:  "aiplex-system",
			YAML: fmt.Sprintf(`apiVersion: aigateway.envoyproxy.io/v1alpha1
kind: AIServiceBackend
metadata:
  name: %s
  namespace: aiplex-system
spec:
  provider: %s
  model: %s
  apiKey:
    secretRef:
      name: %s
`, backendName, b.Provider, b.ModelID, secret),
		})
		weights = append(weights, fmt.Sprintf("        - {name: %s, weight: %d}", backendName, b.Weight))
	}

	var fallbacks []string
	for _, fb := range config.Fallbacks {
		fallbacks = append(fallbacks, fmt.Sprintf("        - %s-backend", sanitize(fb)))
	}

	overrides := fmt.Sprintf(`
  kindOverrides:
    model:
      modelId: %q
      backends:
%s`, config.ModelID, strings.Join(weights, "\n"))
	if len(fallbacks) > 0 {
		overrides += fmt.Sprintf("\n      fallback:\n%s", strings.Join(fallbacks, "\n"))
	}

	route := Manifest{
		APIVersion: "aiplex.dev/v1alpha1",
		Kind:       "CapabilityRoute",
		Name:       name,
		Namespace:  "aiplex-system",
		YAML: fmt.Sprintf(`apiVersion: aiplex.dev/v1alpha1
kind: CapabilityRoute
metadata:
  name: %s
  namespace: aiplex-system
spec:
  capability:
    uri: %s
    kind: model
    name: %s
    version: v1
    auth:
      requiredActions: [complete]
  routing:
    parentRefs:
      - name: %s
        namespace: aiplex-system
    pathTemplate: %q%s
`, name, uri.String(), config.ModelID, gatewayName,
			"/cap/"+uri.PathSegment(), overrides),
	}

	return append([]Manifest{route}, backends...)
}

// sanitize lowercases and replaces non-RFC1123 chars in s with '-'.
// Dots are preserved (RFC 1123 subdomain rules) so model IDs like
// "gemini-2.5-flash" survive intact.
func sanitize(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-.")
}
