package deploy

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"sync"

	"github.com/vamsiramakrishnan/aiplex/internal/models"
)

// RouteGenerator produces the routing resources owned by a module. Generators
// must be safe for concurrent use and must not mutate the supplied resources.
type RouteGenerator func(*models.Instance, *models.Template, string) ([]Manifest, error)

var (
	ErrRouteGeneratorMissing   = errors.New("route generator not registered")
	ErrRouteGeneratorDuplicate = errors.New("route generator already registered")
)

// RouteRegistry is a per-platform registry, rather than process-global mutable
// state. Modules register their route implementation at composition time.
// An empty registry intentionally enables no planes.
type RouteRegistry struct {
	mu         sync.RWMutex
	generators map[models.Plane]RouteGenerator
}

func NewRouteRegistry() *RouteRegistry {
	return &RouteRegistry{generators: make(map[models.Plane]RouteGenerator)}
}

// Register rejects duplicate ownership instead of replacing an existing module.
func (r *RouteRegistry) Register(plane models.Plane, generator RouteGenerator) error {
	if r == nil || strings.TrimSpace(string(plane)) == "" || string(plane) != strings.TrimSpace(string(plane)) || generator == nil {
		return fmt.Errorf("route registration requires a registry, plane, and generator")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.generators == nil {
		r.generators = make(map[models.Plane]RouteGenerator)
	}
	if _, exists := r.generators[plane]; exists {
		return fmt.Errorf("%w: %s", ErrRouteGeneratorDuplicate, plane)
	}
	r.generators[plane] = generator
	return nil
}

func (r *RouteRegistry) Has(plane models.Plane) bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, exists := r.generators[plane]
	return exists
}

// Generate validates module ownership before invoking a generator. Errors never
// become an apparently successful deployment with no route resources.
func (r *RouteRegistry) Generate(inst *models.Instance, tmpl *models.Template, gatewayName string) ([]Manifest, error) {
	if inst == nil || tmpl == nil {
		return nil, fmt.Errorf("route generation requires an instance and template")
	}
	if tmpl.Plane != "" && tmpl.Plane != inst.Plane {
		return nil, fmt.Errorf("template plane %q does not match instance plane %q", tmpl.Plane, inst.Plane)
	}
	if strings.TrimSpace(gatewayName) == "" {
		return nil, fmt.Errorf("route generation requires a gateway name")
	}
	if r == nil {
		return nil, fmt.Errorf("%w: %s", ErrRouteGeneratorMissing, inst.Plane)
	}
	r.mu.RLock()
	generator, exists := r.generators[inst.Plane]
	r.mu.RUnlock()
	if !exists {
		return nil, fmt.Errorf("%w: %s", ErrRouteGeneratorMissing, inst.Plane)
	}
	manifests, err := generator(inst, tmpl, gatewayName)
	if err != nil {
		return nil, fmt.Errorf("generate %s routes: %w", inst.Plane, err)
	}
	if len(manifests) == 0 {
		return nil, fmt.Errorf("route generator %q returned no resources", inst.Plane)
	}
	for _, manifest := range manifests {
		if manifest.APIVersion == "" || manifest.Kind == "" || manifest.Name == "" || manifest.Namespace == "" || manifest.YAML == "" {
			return nil, fmt.Errorf("route generator %q returned an incomplete resource", inst.Plane)
		}
		if _, err := marshalManifest(manifest); err != nil {
			return nil, fmt.Errorf("route generator %q returned an invalid resource: %w", inst.Plane, err)
		}
	}
	return manifests, nil
}

// NewBuiltinRouteRegistry composes the bundled module route generators. A
// deployment can instead supply a registry containing only enabled modules.
func NewBuiltinRouteRegistry() *RouteRegistry {
	r := NewRouteRegistry()
	for _, registration := range []struct {
		plane    models.Plane
		generate RouteGenerator
	}{
		{models.PlaneMCPlex, func(i *models.Instance, _ *models.Template, gateway string) ([]Manifest, error) {
			return []Manifest{mcpRoute(i, gateway)}, nil
		}},
		{models.PlaneA2APlex, func(i *models.Instance, _ *models.Template, gateway string) ([]Manifest, error) {
			return []Manifest{httpRoute(i, gateway)}, nil
		}},
		{models.PlaneLLMPlex, func(i *models.Instance, t *models.Template, gateway string) ([]Manifest, error) {
			return llmRoute(i, t, gateway), nil
		}},
		{models.PlaneSkillsPlex, func(i *models.Instance, _ *models.Template, gateway string) ([]Manifest, error) {
			return []Manifest{skillRoute(i, gateway)}, nil
		}},
	} {
		// Registrations above are unique constants with non-nil generators.
		if err := r.Register(registration.plane, registration.generate); err != nil {
			panic(err)
		}
	}
	return r
}

// NewRouteRegistryForPlanes enables only the selected bundled modules. Unknown
// names and duplicate selections are configuration errors.
func NewRouteRegistryForPlanes(planes ...models.Plane) (*RouteRegistry, error) {
	all := NewBuiltinRouteRegistry()
	selected := NewRouteRegistry()
	for _, plane := range planes {
		generator, exists := all.generators[plane]
		if !exists {
			return nil, fmt.Errorf("%w: %s", ErrRouteGeneratorMissing, plane)
		}
		if err := selected.Register(plane, generator); err != nil {
			return nil, err
		}
	}
	return selected, nil
}

// GenerateRoute is the compatibility helper for bundled route rendering. New
// integration paths should use RouteRegistry.Generate to retain error details.
// Failure returns no resources, never a partial result.
func GenerateRoute(inst *models.Instance, tmpl *models.Template, gatewayName string) []Manifest {
	routes, _ := NewBuiltinRouteRegistry().Generate(inst, tmpl, gatewayName)
	return routes
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

func skillRoute(inst *models.Instance, gatewayName string) Manifest {
	return Manifest{
		APIVersion: "gateway.networking.k8s.io/v1",
		Kind:       "HTTPRoute",
		Name:       "skill-" + inst.ID,
		Namespace:  inst.Namespace,
		YAML: fmt.Sprintf(`apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: skill-%s
  namespace: %s
  labels:
    app.kubernetes.io/managed-by: aiplex
    aiplex.io/instance-id: %s
    aiplex.io/plane: skillsplex
spec:
  parentRefs:
  - name: %s
    namespace: aiplex-system
  rules:
  - matches:
    - path:
        type: PathPrefix
        value: /skills/%s
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

	// Default to provider-based secret name
	secretName := provider + "-api-key"

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
      name: %s
`, backendName, inst.ID, provider, modelID, secretName),
		},
	}
}

var routeNamePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$`)
var routeScalarPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@+-]*$`)

var v2BackendName = regexp.MustCompile(`^llm-[0-9a-f]{16}-[0-9a-f]{32}-backend$`)

// LLMBackendName preserves legacy resources while new route configurations use
// collision-resistant tuple identities. The model tag allows a persisted retired
// inventory to retain its ownership scope after the provider leaves Backends.
func LLMBackendName(model, provider string, version int) (string, error) {
	if model == "" || !validRouteName("llm-"+model) || !validRouteName(provider) {
		return "", fmt.Errorf("invalid model or provider backend identity")
	}
	name := model + "-" + provider + "-backend"
	switch version {
	case 0:
	case 2:
		owner := sha256.Sum256([]byte(model))
		tuple := sha256.Sum256([]byte(model + "\x00" + provider))
		name = "llm-" + hex.EncodeToString(owner[:8]) + "-" + hex.EncodeToString(tuple[:16]) + "-backend"
	default:
		return "", fmt.Errorf("unsupported backend naming version")
	}
	if !validRouteName(name) {
		return "", fmt.Errorf("invalid backend resource name")
	}
	return name, nil
}

// LLMBackendOwnedByModel validates names recovered from server-owned inventory.
// It never makes caller-supplied retired names authoritative.
func LLMBackendOwnedByModel(model, name string) bool {
	if !validRouteName(name) {
		return false
	}
	if v2BackendName.MatchString(name) {
		owner := sha256.Sum256([]byte(model))
		return strings.HasPrefix(name, "llm-"+hex.EncodeToString(owner[:8])+"-")
	}
	return strings.HasPrefix(name, model+"-") && strings.HasSuffix(name, "-backend")
}

// GenerateRoutesFromConfig is the bundled compatibility renderer. Admission
// paths use GenerateRoutesFromConfigChecked to preserve validation errors.
func GenerateRoutesFromConfig(config *models.LLMRouteConfig, gatewayName string) []Manifest {
	manifests, _ := GenerateRoutesFromConfigChecked(config, gatewayName)
	return manifests
}

// GenerateRoutesFromConfigChecked validates external configuration before any
// route is stored or applied. Dynamic scalars cannot inject YAML structure.
func GenerateRoutesFromConfigChecked(config *models.LLMRouteConfig, gatewayName string) ([]Manifest, error) {
	return GenerateRoutesWithFallbacksChecked(config, gatewayName, nil)
}

// GenerateRoutesWithFallbacksChecked resolves ordered fallback model IDs to the
// actual provider backend names owned by their saved route configurations. A
// fallback is never rendered as a guessed, nonexistent model-only backend.
func GenerateRoutesWithFallbacksChecked(config *models.LLMRouteConfig, gatewayName string, fallbacks map[string]*models.LLMRouteConfig) ([]Manifest, error) {
	if err := validateLLMRouteConfig(config, gatewayName); err != nil {
		return nil, err
	}
	var refs []string
	seen := map[string]bool{}
	for _, id := range config.Fallbacks {
		fallback := fallbacks[id]
		if id == config.ModelID || seen[id] || fallback == nil || fallback.ModelID != id {
			return nil, fmt.Errorf("fallback requires a distinct saved route configuration")
		}
		seen[id] = true
		if err := validateLLMRouteConfig(fallback, gatewayName); err != nil {
			return nil, fmt.Errorf("invalid fallback route configuration")
		}
		for _, backend := range fallback.Backends {
			if backend.Enabled {
				name, err := LLMBackendName(id, backend.Provider, fallback.BackendNamingVersion)
				if err != nil {
					return nil, err
				}
				refs = append(refs, name)
			}
		}
	}
	manifests := renderRoutesFromConfig(config, gatewayName, refs)
	for _, manifest := range manifests {
		if _, err := marshalManifest(manifest); err != nil {
			return nil, fmt.Errorf("invalid generated route: %w", err)
		}
	}
	return manifests, nil
}

func validateLLMRouteConfig(config *models.LLMRouteConfig, gatewayName string) error {
	if config == nil || !validRouteName(gatewayName) || !validRouteName("llm-"+config.ModelID) {
		return fmt.Errorf("invalid route model or gateway name")
	}
	if config.ModelID == "" {
		return fmt.Errorf("model ID is required")
	}
	if len(config.Backends) > 128 || len(config.Fallbacks) > 32 {
		return fmt.Errorf("route backend or fallback inventory exceeds supported limits")
	}
	if config.BackendNamingVersion != 0 && config.BackendNamingVersion != 2 {
		return fmt.Errorf("unsupported backend naming version")
	}
	if config.CacheTTL < 0 || config.RateLimit != nil && (config.RateLimit.RequestsPerMinute < 0 || config.RateLimit.TokensPerMinute < 0) {
		return fmt.Errorf("cache and reporting limits cannot be negative")
	}
	if b := config.Budget; b != nil {
		if b.MaxDailyCostUSD < 0 || b.MaxMonthlyCostUSD < 0 || b.MaxDailyTokens < 0 || b.AlertThreshold < 0 || b.AlertThreshold > 100 || math.IsNaN(b.MaxDailyCostUSD) || math.IsInf(b.MaxDailyCostUSD, 0) || math.IsNaN(b.MaxMonthlyCostUSD) || math.IsInf(b.MaxMonthlyCostUSD, 0) || math.IsNaN(b.AlertThreshold) || math.IsInf(b.AlertThreshold, 0) {
			return fmt.Errorf("invalid reporting budget")
		}
	}
	seen := make(map[string]bool)
	totalWeight := 0
	for _, backend := range config.Backends {
		if !validRouteName(backend.Provider) || !routeScalarPattern.MatchString(backend.ModelID) ||
			(backend.SecretRef != "" && !validRouteName(backend.SecretRef)) || backend.Weight < 0 || backend.Weight > 100 {
			return fmt.Errorf("invalid backend configuration")
		}
		if !backend.Enabled {
			continue
		}
		name, err := LLMBackendName(config.ModelID, backend.Provider, config.BackendNamingVersion)
		if err != nil || seen[name] {
			return fmt.Errorf("duplicate or invalid backend name %q", name)
		}
		seen[name] = true
		totalWeight += backend.Weight
	}
	if len(seen) == 0 || totalWeight != 100 {
		return fmt.Errorf("enabled backend weights must sum to 100")
	}
	for _, fallback := range config.Fallbacks {
		if !validRouteName(fallback + "-backend") {
			return fmt.Errorf("invalid fallback model ID")
		}
	}
	return nil
}

func validRouteName(value string) bool {
	if len(value) == 0 || len(value) > 253 || !routeNamePattern.MatchString(value) {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if len(label) == 0 || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
	}
	return true
}

// GenerateRoutesFromConfig creates Envoy LLMRoute + AIServiceBackend manifests
// from a route configuration with weighted backends and fallbacks.
func renderRoutesFromConfig(config *models.LLMRouteConfig, gatewayName string, fallbackRefs []string) []Manifest {
	var manifests []Manifest
	var backendRefs []string

	for _, backend := range config.Backends {
		if !backend.Enabled {
			continue
		}
		backendName, _ := LLMBackendName(config.ModelID, backend.Provider, config.BackendNamingVersion)

		secretName := backend.SecretRef
		if secretName == "" {
			secretName = backend.Provider + "-api-key"
		}

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
      name: %s`, backendName, backend.Provider, backend.ModelID, secretName)

		manifests = append(manifests, Manifest{
			APIVersion: "aigateway.envoyproxy.io/v1alpha1",
			Kind:       "AIServiceBackend",
			Name:       backendName,
			Namespace:  "aiplex-system",
			YAML:       backendYAML,
		})

		backendRefs = append(backendRefs, fmt.Sprintf("        - name: %s\n          weight: %d", backendName, backend.Weight))
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

	if len(fallbackRefs) > 0 {
		var fallbackLines []string
		for _, fb := range fallbackRefs {
			fallbackLines = append(fallbackLines, fmt.Sprintf("        - name: %s", fb))
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
