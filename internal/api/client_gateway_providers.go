package api

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/vamsiramakrishnan/aiplex/pkg/modelapi"
)

// Provider client facade paths. An unmodified provider SDK pointed at
// /api/v1/llm/gateway/{id} (or the SDK's documented base path below it)
// sends its native path; the facade maps it to the surface's native
// operation path without touching the body. The native gateway still
// matches the exact operation allowlist, so unmapped or uninstalled
// operations fail closed. Access-point routing reuses clientNativePath.
//
//	openai             base_url .../gateway/{id}/v1    → /v1/chat/completions, /v1/responses
//	anthropic          base_url .../gateway/{id}       → /v1/messages
//	azure-openai (v1)  base_url .../gateway/{id}/openai/v1 → /openai/v1/{chat/completions,responses}
//	azure-openai (dep) azure_endpoint .../gateway/{id} → /openai/deployments/{d}/chat/completions
//	bedrock            endpoint_url .../gateway/{id}   → /model/{m}/{converse,converse-stream,invoke,invoke-with-response-stream}
//	openai-compatible  base_url .../gateway/{id}{base} → {base}/chat/completions
var (
	bedrockClientPath  = regexp.MustCompile(`^/model/([A-Za-z0-9._:@-]+)/(converse|converse-stream|invoke|invoke-with-response-stream)$`)
	azureDeploymentRef = regexp.MustCompile(`^/openai/deployments/([A-Za-z0-9._:@-]+)/chat/completions$`)
)

func providerClientNativePath(s modelapi.Surface, r *http.Request, path string) (string, []byte, error) {
	if native, ok := utilityClientPath(s, r.Method, path); ok {
		return native, nil, nil
	}
	if native, ok := extendedClientNativePath(s, r, path); ok {
		return native, nil, nil
	}
	if r.Method == http.MethodGet && s.Provider.ID == modelapi.ProviderOpenAI {
		// WebSocket sessions (Realtime, Responses WebSocket mode) upgrade a GET.
		switch strings.TrimPrefix(path, "/v1") {
		case "/realtime":
			return "/v1/realtime", nil, nil
		case "/live/sessions":
			return "/v1/live/sessions", nil, nil
		case "/responses":
			return "/v1/responses", nil, nil
		}
	}
	if r.Method != http.MethodPost {
		return "", nil, fmt.Errorf("provider client operations are POST")
	}
	switch s.Provider.ID {
	case modelapi.ProviderOpenAI:
		switch strings.TrimPrefix(path, "/v1") {
		case "/chat/completions":
			return "/v1/chat/completions", nil, nil
		case "/responses":
			return "/v1/responses", nil, nil
		}
	case modelapi.ProviderAnthropic:
		if path == "/v1/messages" {
			return path, nil, nil
		}
	case modelapi.ProviderAzureOpenAI:
		if path == "/openai/v1/chat/completions" || path == "/openai/v1/responses" || azureDeploymentRef.MatchString(path) {
			return path, nil, nil
		}
	case modelapi.ProviderBedrock:
		if bedrockClientPath.MatchString(path) {
			return path, nil, nil
		}
	case modelapi.ProviderOpenAICompatible:
		base := s.Provider.BasePath
		if base == "" {
			base = "/v1"
		}
		if path == base+"/chat/completions" || path == "/chat/completions" {
			return base + "/chat/completions", nil, nil
		}
	}
	return "", nil, fmt.Errorf("%s client path is not supported by this surface", s.Provider.ID)
}

// providerClientSurfaceReady reports whether a provider surface installs a
// canonical operation for its client protocol.
func providerClientSurfaceReady(surface modelapi.Surface) bool {
	if surface.Validate() != nil {
		return false
	}
	for _, operation := range surface.Operations {
		if modelapi.OperationProtocol(surface.Provider.ID, operation.ID) == surface.Protocol {
			return true
		}
	}
	return false
}
