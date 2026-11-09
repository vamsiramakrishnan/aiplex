package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/vamsiramakrishnan/aiplex/pkg/modelapi"
)

var genaiClientPath = regexp.MustCompile(`^/v1beta/models/([A-Za-z0-9._@-]+):(generateContent|streamGenerateContent|countTokens|embedContent)$`)

// ClientGateway adapts only documented client wire paths to the configured
// Cloud surface. Authorization, ownership and server credentials stay in the
// native handler; no raw provider URL is exposed as a client gateway base.
func (h *ConnectedHandler) ClientGateway(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.tenant(w, r)
	if !ok {
		return
	}
	id := idParam(r)
	rec, e := h.Store.Get(r.Context(), tenant, "model-surface", id)
	if e != nil {
		resourceError(w, r, e)
		return
	}
	var connection ModelConnection
	if e = json.Unmarshal(rec.Spec, &connection); e != nil {
		resourceError(w, r, e)
		return
	}
	local := strings.TrimPrefix(r.URL.Path, "/api/v1/llm/gateway/"+id)
	path, body, e := clientNativePath(connection.Surface, r, local)
	if e != nil {
		Error(w, r, 400, "UNSUPPORTED_CLIENT_OPERATION", e.Error())
		return
	}
	cloned := r.Clone(r.Context())
	copiedURL := *r.URL
	cloned.URL = &copiedURL
	cloned.URL.Path = "/api/v1/llm/native/" + id + path
	cloned.URL.RawPath = ""
	if body != nil {
		cloned.Body = io.NopCloser(bytes.NewReader(body))
		cloned.ContentLength = int64(len(body))
		cloned.Header = cloned.Header.Clone()
		cloned.Header.Del("Content-Length")
	}
	h.NativeModel(w, cloned)
}
func clientNativePath(s modelapi.Surface, r *http.Request, path string) (string, []byte, error) {
	if strings.ContainsAny(path, "%\\\r\n") || strings.Contains(path, "..") {
		return "", nil, fmt.Errorf("invalid client path")
	}
	if s.Provider != nil {
		return providerClientNativePath(s, r, path)
	}
	base := "/" + s.APIVersion + "/projects/" + s.Project + "/locations/" + s.Location
	if path == "/ws/google.cloud.aiplatform.v1.LlmBidiService/BidiGenerateContent" && r.Method == http.MethodGet {
		// Vertex Live: the WebSocket path is fixed; the model is in setup.
		return path, nil, nil
	}
	switch s.Protocol {
	case "google-genai":
		match := genaiClientPath.FindStringSubmatch(path)
		if match == nil || r.Method != "POST" {
			return "", nil, fmt.Errorf("GenAI operation unsupported by client facade")
		}
		return base + "/publishers/" + s.Publisher + "/models/" + match[1] + ":" + match[2], nil, nil
	case "openai-chat":
		if r.Method != "POST" || (path != "/chat/completions" && path != "/v1/chat/completions") || s.Endpoint == "" {
			return "", nil, fmt.Errorf("chat-completions endpoint required")
		}
		return base + "/endpoints/" + s.Endpoint + "/chat/completions", nil, nil
	case "openai-responses":
		path = strings.TrimPrefix(path, "/v1")
		if s.Endpoint == "" || !(path == "/responses" || strings.HasPrefix(path, "/responses/")) {
			return "", nil, fmt.Errorf("Responses endpoint required")
		}
		return base + "/endpoints/" + s.Endpoint + path, nil, nil
	case "vertex-anthropic":
		// Claude Code's Vertex client supplies the canonical project/model path.
		if !strings.HasPrefix(path, base+"/publishers/anthropic/models/") {
			return "", nil, fmt.Errorf("Vertex path must match configured project/location")
		}
		return path, nil, nil
	case "anthropic-messages":
		if s.Publisher != "anthropic" || r.Method != "POST" || path != "/v1/messages" {
			return "", nil, fmt.Errorf("Anthropic Messages requires an Anthropic Cloud surface")
		}
		if clientOperationReady(s, "anthropic.messages") {
			// An explicitly installed Messages operation keeps its native path,
			// body and headers. Only Vertex operations require a translated body.
			return path, nil, nil
		}
		data, e := io.ReadAll(io.LimitReader(r.Body, (8<<20)+1))
		if e != nil || len(data) > 8<<20 {
			return "", nil, fmt.Errorf("request body exceeds bound")
		}
		if e = modelapi.ValidateJSON(data); e != nil {
			return "", nil, fmt.Errorf("invalid native JSON: %w", e)
		}
		var payload map[string]json.RawMessage
		if e = json.Unmarshal(data, &payload); e != nil {
			return "", nil, e
		}
		var model string
		if e = json.Unmarshal(payload["model"], &model); e != nil || !regexp.MustCompile(`^[A-Za-z0-9._@-]+$`).MatchString(model) {
			return "", nil, fmt.Errorf("model required")
		}
		delete(payload, "model")
		payload["anthropic_version"] = json.RawMessage(`"vertex-2023-10-16"`)
		method := "rawPredict"
		var stream bool
		if raw := payload["stream"]; raw != nil {
			if e = json.Unmarshal(raw, &stream); e != nil {
				return "", nil, fmt.Errorf("stream must be a boolean")
			}
		}
		if stream {
			method = "streamRawPredict"
		}
		body, e := json.Marshal(payload)
		return base + "/publishers/anthropic/models/" + url.PathEscape(model) + ":" + method, body, e
	default:
		return "", nil, fmt.Errorf("client protocol requires an explicit adapter")
	}
}
