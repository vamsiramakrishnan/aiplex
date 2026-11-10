package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/vamsiramakrishnan/aiplex/pkg/modelapi"
)

// accessPointFacadePath translates an access point's wire path into the path
// the selected surface's client facade (clientNativePath) accepts. Wire paths
// are the vendor-default ones (/v1/chat/completions, /v1/responses); Azure
// OpenAI and OpenAI-compatible surfaces expose those operations elsewhere, so
// the path is chosen from the surface's installed operations and base path.
// Every other wire/surface pair already shares its wire path with the facade.
func accessPointFacadePath(s modelapi.Surface, wire, native, path string) (string, error) {
	if s.Provider == nil {
		return path, nil
	}
	has := func(id string) bool {
		for _, op := range s.Operations {
			if op.ID == id {
				return true
			}
		}
		return false
	}
	switch s.Provider.ID {
	case modelapi.ProviderAzureOpenAI:
		switch wire {
		case WireOpenAIChat:
			if has("azure.v1.chat-completions") {
				return "/openai/v1/chat/completions", nil
			}
			if has("azure.deployments.chat-completions") {
				return "/openai/deployments/" + native + "/chat/completions", nil
			}
			return "", fmt.Errorf("azure-openai surface %q installs no chat-completions operation", s.ID)
		case WireOpenAIResponses:
			if has("azure.v1.responses") {
				return "/openai/v1/responses", nil
			}
			return "", fmt.Errorf("azure-openai surface %q installs no responses operation", s.ID)
		}
	case modelapi.ProviderOpenAICompatible:
		if wire == WireOpenAIChat {
			base := s.Provider.BasePath
			if base == "" {
				base = "/v1"
			}
			return base + "/chat/completions", nil
		}
	}
	return path, nil
}

// accessPointWireSample is a representative client path for a wire, with the
// native model substituted where the path carries it.
func accessPointWireSample(wire, native string) string {
	switch wire {
	case WireGenAI:
		return "/v1beta/models/" + native + ":generateContent"
	case WireBedrockConverse:
		return "/model/" + native + "/converse"
	case WireBedrockInvoke:
		return "/model/" + native + "/invoke"
	case WireGeminiLive:
		return geminiLivePath
	}
	return strings.SplitN(accessPointWirePath(wire), "?", 2)[0]
}

// accessPointSurfaceServes reports whether at least one wire that reaches the
// surface is accepted by its client facade for the given native model. It is
// the save-time counterpart of the translation done per request, so a model
// that could never be served is rejected when the access point is saved.
func accessPointSurfaceServes(s modelapi.Surface, native string) error {
	var last error
	for _, wire := range accessPointWireProtocols(s.Protocol) {
		method := http.MethodPost
		if accessPointWebSocket(wire) {
			method = http.MethodGet
		}
		path, err := accessPointFacadePath(s, wire, native, accessPointWireSample(wire, native))
		if err == nil {
			probe := httptest.NewRequest(method, "/", strings.NewReader(`{"model":"probe"}`))
			_, _, err = clientNativePath(s, probe, path)
		}
		if err == nil {
			return nil
		}
		last = err
	}
	if last == nil {
		last = fmt.Errorf("no access-point wire protocol reaches protocol %q", s.Protocol)
	}
	return last
}
