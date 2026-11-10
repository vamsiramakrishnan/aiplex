package api

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/vamsiramakrishnan/aiplex/pkg/clientplex"
	"github.com/vamsiramakrishnan/aiplex/pkg/modelapi"
)

// Client wire protocols served under /ap/{id}. Each lists the model-surface
// protocols that speak it natively; the existing client facade
// (clientNativePath) maps the client path onto the surface's native path, so
// new providers that reuse a surface protocol need no change here.
const (
	WireOpenAIChat      = "openai-chat-completions"
	WireOpenAIResponses = "openai-responses"
	WireAnthropic       = "anthropic-messages"
	WireGenAI           = "google-genai"
	// Bedrock Runtime native paths: the model is the path segment.
	WireBedrockConverse = "bedrock-converse"
	WireBedrockInvoke   = "bedrock-invoke"
	// WebSocket sessions. The model is selected at connect time by the
	// ?model= query parameter (the Realtime API's own selector; for Live it is
	// the access point's routing selector and must equal setup.model).
	WireOpenAIRealtime = "openai-realtime"
	WireGeminiLive     = "google-live"
)

// geminiLivePath is the Vertex Live (BidiGenerateContent) WebSocket path.
const geminiLivePath = "/ws/google.cloud.aiplatform.v1.LlmBidiService/BidiGenerateContent"

type accessPointWire struct {
	ID       string
	Path     string
	Surfaces []string
}

var accessPointWires = []accessPointWire{
	{WireOpenAIChat, "/v1/chat/completions", []string{clientplex.OpenAIChat}},
	{WireOpenAIResponses, "/v1/responses", []string{clientplex.Responses}},
	{WireAnthropic, "/v1/messages", []string{clientplex.Messages}},
	{WireGenAI, "/v1beta/models/{model}:{method}", []string{clientplex.GenAI}},
	{WireBedrockConverse, "/model/{model}/converse[-stream]", []string{modelapi.ProtocolBedrockConverse}},
	{WireBedrockInvoke, "/model/{model}/invoke[-with-response-stream]", []string{modelapi.ProtocolBedrockInvoke}},
	{WireOpenAIRealtime, "/v1/realtime?model={model} (WebSocket)", []string{modelapi.ProtocolOpenAIRealtime}},
	{WireGeminiLive, geminiLivePath + "?model={model} (WebSocket)", []string{clientplex.GenAI}},
	// Text and utility wires (client_gateway_utility.go).
	{WireOpenAIEmbeddings, "/v1/embeddings", []string{modelapi.ProtocolOpenAIEmbeddings}},
	{WireOpenAIModerations, "/v1/moderations", []string{modelapi.ProtocolOpenAIModerations}},
}

// accessPointWebSocket reports the session wires, which carry no body and
// are upgraded by the native gateway.
func accessPointWebSocket(protocol string) bool {
	return protocol == WireOpenAIRealtime || protocol == WireGeminiLive
}

// bedrockErrorType is the AWS error type (X-Amzn-ErrorType) for a status.
func bedrockErrorType(status int) string {
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return "AccessDeniedException"
	case status == http.StatusNotFound:
		return "ResourceNotFoundException"
	case status == http.StatusTooManyRequests:
		return "ThrottlingException"
	case status == http.StatusServiceUnavailable:
		return "ServiceUnavailableException"
	case status >= 500:
		return "InternalServerException"
	}
	return "ValidationException"
}

// accessPointWireProtocols lists the wire protocols that reach a surface.
func accessPointWireProtocols(surfaceProtocol string) []string {
	var out []string
	for _, wire := range accessPointWires {
		for _, s := range wire.Surfaces {
			if s == surfaceProtocol {
				out = append(out, wire.ID)
			}
		}
	}
	return out
}

func accessPointWirePath(id string) string {
	for _, wire := range accessPointWires {
		if wire.ID == id {
			return wire.Path
		}
	}
	return ""
}

func (h *ConnectedHandler) moduleEnabled(name string) bool {
	return h.EnabledModules == nil || h.EnabledModules[name]
}

func readCloser(b []byte) io.ReadCloser { return io.NopCloser(bytes.NewReader(b)) }

// ── protocol-native errors ────────────────────────────────────────────────

func openAIErrorType(status int) string {
	switch {
	case status == 401:
		return "authentication_error"
	case status == 403:
		return "permission_error"
	case status == 404:
		return "not_found_error"
	case status == 429:
		return "rate_limit_error"
	case status >= 500:
		return "server_error"
	}
	return "invalid_request_error"
}

func anthropicErrorType(status int) string {
	switch {
	case status == 401:
		return "authentication_error"
	case status == 403:
		return "permission_error"
	case status == 404:
		return "not_found_error"
	case status == 413:
		return "request_too_large"
	case status == 429:
		return "rate_limit_error"
	case status >= 500:
		return "api_error"
	}
	return "invalid_request_error"
}

func googleErrorStatus(status int) string {
	switch status {
	case 400, 413, 415:
		return "INVALID_ARGUMENT"
	case 401:
		return "UNAUTHENTICATED"
	case 403:
		return "PERMISSION_DENIED"
	case 404:
		return "NOT_FOUND"
	case 409:
		return "ABORTED"
	case 429:
		return "RESOURCE_EXHAUSTED"
	case 501:
		return "UNIMPLEMENTED"
	case 503:
		return "UNAVAILABLE"
	case 504:
		return "DEADLINE_EXCEEDED"
	}
	if status >= 500 {
		return "INTERNAL"
	}
	return "FAILED_PRECONDITION"
}

// nativeErrorBody renders one error in a wire protocol's own shape.
func nativeErrorBody(protocol string, status int, code, message string) []byte {
	var value any
	switch protocol {
	case WireAnthropic:
		value = map[string]any{"type": "error", "error": map[string]any{"type": anthropicErrorType(status), "message": message}}
	case WireGenAI, WireGeminiLive:
		value = map[string]any{"error": map[string]any{"code": status, "message": message, "status": googleErrorStatus(status)}}
	case WireBedrockConverse, WireBedrockInvoke:
		// AWS JSON protocol error: {"message"} plus the X-Amzn-ErrorType header.
		value = map[string]any{"message": message}
	default:
		value = map[string]any{"error": map[string]any{"message": message, "type": openAIErrorType(status), "param": nil, "code": strings.ToLower(code)}}
	}
	body, _ := json.Marshal(value)
	return body
}

func writeNativeError(w http.ResponseWriter, protocol string, status int, code, message string) {
	w.Header().Del("Content-Length")
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", "Bearer")
	}
	if protocol == WireBedrockConverse || protocol == WireBedrockInvoke {
		w.Header().Set("X-Amzn-ErrorType", bedrockErrorType(status))
	}
	w.WriteHeader(status)
	_, _ = w.Write(nativeErrorBody(protocol, status, code, message))
}

func modelErrorWriter(protocol string) accessPointErrorWriter {
	return func(w http.ResponseWriter, _ *http.Request, status int, code, message string) {
		writeNativeError(w, protocol, status, code, message)
	}
}

// nativeErrorWriter passes successful and streaming responses through
// untouched. Error responses produced by AIPlex itself (its JSON API errors
// and the gateways' plain-text refusals) are re-rendered in the calling
// protocol's error shape; provider error bodies pass through unchanged.
type nativeErrorWriter struct {
	http.ResponseWriter
	protocol  string
	status    int
	wrote     bool
	buffering bool
	overflow  bool
	buf       bytes.Buffer
}

const nativeErrorBuffer = 1 << 20

func (w *nativeErrorWriter) WriteHeader(status int) {
	if w.wrote {
		return
	}
	w.wrote = true
	if status >= 400 {
		w.buffering, w.status = true, status
		return
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *nativeErrorWriter) Write(b []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	if !w.buffering {
		return w.ResponseWriter.Write(b)
	}
	if !w.overflow && w.buf.Len()+len(b) <= nativeErrorBuffer {
		return w.buf.Write(b)
	}
	if !w.overflow {
		w.overflow = true
		w.ResponseWriter.WriteHeader(w.status)
		if _, err := w.ResponseWriter.Write(w.buf.Bytes()); err != nil {
			return 0, err
		}
		w.buf.Reset()
	}
	return w.ResponseWriter.Write(b)
}

// Hijack lets a WebSocket session upgrade through the writer. Errors before
// the upgrade are still re-rendered; after it the connection is the session's.
func (w *nativeErrorWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok || w.wrote {
		return nil, nil, fmt.Errorf("response does not support hijacking")
	}
	w.wrote = true
	return hijacker.Hijack()
}

func (w *nativeErrorWriter) Flush() {
	if w.buffering {
		return
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// aiplexError recognises an error this server produced, as opposed to a
// provider error body relayed from upstream.
func aiplexError(contentType string, body []byte) (code, message string, ok bool) {
	media := strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	if media == "text/plain" {
		message = strings.TrimSpace(string(body))
		if len(message) > 512 {
			message = message[:512]
		}
		return "", message, true
	}
	if media != "application/json" {
		return "", "", false
	}
	var probe map[string]json.RawMessage
	if json.Unmarshal(body, &probe) != nil {
		return "", "", false
	}
	if _, upstreamShape := probe["error"]; upstreamShape {
		return "", "", false
	}
	if json.Unmarshal(probe["code"], &code) != nil || json.Unmarshal(probe["message"], &message) != nil || code == "" {
		return "", "", false
	}
	for key := range probe {
		if key != "code" && key != "message" && key != "request_id" && key != "details" {
			return "", "", false
		}
	}
	return code, message, true
}

func (w *nativeErrorWriter) finish() {
	if !w.buffering || w.overflow {
		return
	}
	if code, message, ok := aiplexError(w.Header().Get("Content-Type"), w.buf.Bytes()); ok {
		if code == "" {
			code = googleErrorStatus(w.status)
		}
		w.Header().Del("X-Content-Type-Options")
		writeNativeError(w.ResponseWriter, w.protocol, w.status, code, message)
		return
	}
	w.ResponseWriter.WriteHeader(w.status)
	_, _ = w.ResponseWriter.Write(w.buf.Bytes())
}

// ── model request routing ─────────────────────────────────────────────────

func accessPointLocalPath(r *http.Request) (string, bool) {
	if r.URL.RawPath != "" {
		return "", false
	}
	prefix := "/ap/" + chi.URLParam(r, "apid")
	if !strings.HasPrefix(r.URL.Path, prefix+"/") {
		return "", false
	}
	return strings.TrimPrefix(r.URL.Path, prefix), true
}

func bodyModel(data []byte) (string, error) {
	if err := modelapi.ValidateJSON(data); err != nil {
		return "", fmt.Errorf("request body must be one unambiguous JSON object")
	}
	var probe struct {
		Model *string `json:"model"`
	}
	if json.Unmarshal(data, &probe) != nil || probe.Model == nil || *probe.Model == "" {
		return "", fmt.Errorf("request body must name a model")
	}
	return *probe.Model, nil
}

func rewriteBodyModel(data []byte, model string) ([]byte, error) {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(model)
	if err != nil {
		return nil, err
	}
	payload["model"] = encoded
	return json.Marshal(payload)
}

// AccessPointModel serves one client wire protocol. The model name selects
// the access point's route to a saved surface; the request is then handed to
// the existing client facade and managed native handler unchanged.
func (h *ConnectedHandler) AccessPointModel(protocol string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		fail := modelErrorWriter(protocol)
		tenant, ap, call, ok := h.accessPointGate(w, r, fail)
		if !ok {
			return
		}
		if !h.moduleEnabled("llmplex") {
			fail(w, r, http.StatusNotFound, "MODULE_DISABLED", "model endpoints are disabled")
			return
		}
		local, ok := accessPointLocalPath(r)
		if !ok {
			fail(w, r, http.StatusBadRequest, "INVALID_PATH", "invalid request path")
			return
		}
		data, err := io.ReadAll(io.LimitReader(r.Body, (8<<20)+1))
		if err != nil || len(data) > 8<<20 {
			fail(w, r, http.StatusRequestEntityTooLarge, "REQUEST_TOO_LARGE", "request body exceeds 8 MiB")
			return
		}
		var name, method string
		switch {
		case protocol == WireGenAI:
			match := genaiClientPath.FindStringSubmatch(local)
			if match == nil && genaiBodyModelPath(local) {
				// Developer API creations carry models/{model} in the body.
				name, err = bodyModel(data)
				name = strings.TrimPrefix(name, "models/")
				break
			}
			if match == nil {
				fail(w, r, http.StatusNotFound, "UNSUPPORTED_OPERATION", "unsupported GenAI model operation")
				return
			}
			name, method = match[1], match[2]
		case protocol == WireOpenAIModerations:
			name, err = moderationModelName(data)
		case protocol == WireBedrockConverse || protocol == WireBedrockInvoke:
			match := bedrockClientPath.FindStringSubmatch(local)
			converse := match != nil && strings.HasPrefix(match[2], "converse")
			if match == nil || converse != (protocol == WireBedrockConverse) {
				fail(w, r, http.StatusNotFound, "UNSUPPORTED_OPERATION", "unsupported Bedrock Runtime operation")
				return
			}
			name, method = match[1], match[2]
		case accessPointWebSocket(protocol):
			query := r.URL.Query()
			if len(data) != 0 || len(query) != 1 || len(query["model"]) != 1 || query.Get("model") == "" {
				fail(w, r, http.StatusBadRequest, "INVALID_REQUEST", "a WebSocket session is selected by exactly one ?model= query parameter")
				return
			}
			name = query.Get("model")
		default:
			name, err = bodyModel(data)
		}
		if err != nil {
			fail(w, r, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
			return
		}
		var entry *AccessPointModel
		for i := range ap.Models {
			if ap.Models[i].Name == name {
				entry = &ap.Models[i]
			}
		}
		if entry == nil {
			fail(w, r, http.StatusNotFound, "MODEL_NOT_FOUND", fmt.Sprintf("model %q is not available through this access point", name))
			return
		}
		connection, err := h.accessPointSurface(r.Context(), tenant, entry.SurfaceRef)
		if err != nil {
			fail(w, r, http.StatusServiceUnavailable, "SURFACE_UNAVAILABLE", "model surface is unavailable")
			return
		}
		serving := accessPointWireProtocols(connection.Surface.Protocol)
		reachable := false
		for _, p := range serving {
			if p == protocol {
				reachable = true
			}
		}
		if !reachable {
			paths := make([]string, 0, len(serving))
			for _, p := range serving {
				paths = append(paths, p+" ("+accessPointWirePath(p)+")")
			}
			fail(w, r, http.StatusBadRequest, "PROTOCOL_MISMATCH", fmt.Sprintf("model %q is not served through %s; it is served through: %s", name, protocol, strings.Join(paths, ", ")))
			return
		}
		if ap.QoS != nil && h.Quota == nil {
			fail(w, r, http.StatusServiceUnavailable, "QUOTA_UNAVAILABLE", "access point QoS requires quota enforcement, which is not configured")
			return
		}
		path, body := local, data
		rawQuery := r.URL.RawQuery
		if accessPointWebSocket(protocol) {
			body = nil
			rawQuery = url.Values{"model": {entry.NativeModel}}.Encode()
		}
		if entry.Name != entry.NativeModel {
			switch {
			case protocol == WireGenAI && method != "":
				path = "/v1beta/models/" + entry.NativeModel + ":" + method
			case protocol == WireBedrockConverse || protocol == WireBedrockInvoke:
				// The path selects the model; the body is relayed untouched.
				path = "/model/" + entry.NativeModel + "/" + method
			case protocol == WireOpenAIRealtime:
				// rawQuery already carries the native model.
			case protocol == WireAnthropic && accessPointAliasRewritable(connection.Surface):
				if body, err = rewriteBodyModel(data, entry.NativeModel); err != nil {
					fail(w, r, http.StatusBadRequest, "INVALID_REQUEST", "request body must be one JSON object")
					return
				}
			default:
				fail(w, r, http.StatusServiceUnavailable, "MODEL_ALIAS_UNSUPPORTED", "this surface relays the body verbatim; the model name must be the native name")
				return
			}
		}
		inner := delegateRequest(r, call, "/api/v1/llm/gateway/"+entry.SurfaceRef+path, entry.SurfaceRef, body)
		inner.URL.RawQuery = rawQuery
		writer := &nativeErrorWriter{ResponseWriter: w, protocol: protocol}
		defer writer.finish()
		h.ClientGateway(writer, inner)
	}
}

type accessPointListedModel struct {
	Entry     AccessPointModel
	ModelID   string
	Publisher string
	Protocols []string
}

// listedModels returns the access point's models the caller may use now.
func (h *ConnectedHandler) listedModels(r *http.Request, tenant string, ap AccessPoint) []accessPointListedModel {
	out := []accessPointListedModel{}
	for _, entry := range ap.Models {
		connection, err := h.accessPointSurface(r.Context(), tenant, entry.SurfaceRef)
		if err != nil {
			continue
		}
		modelID, err := connection.Surface.ModelID(entry.NativeModel)
		if err != nil || h.authorize(r, "llm:model:"+modelID) != nil {
			continue
		}
		out = append(out, accessPointListedModel{Entry: entry, ModelID: modelID, Publisher: connection.Surface.Publisher, Protocols: accessPointWireProtocols(connection.Surface.Protocol)})
	}
	return out
}

// AccessPointListModels serves GET /ap/{id}/v1/models in the OpenAI shape,
// or the Anthropic shape when the request carries anthropic-version.
func (h *ConnectedHandler) AccessPointListModels(w http.ResponseWriter, r *http.Request) {
	protocol := WireOpenAIChat
	if r.Header.Get("anthropic-version") != "" {
		protocol = WireAnthropic
	}
	tenant, ap, _, ok := h.accessPointGate(w, r, modelErrorWriter(protocol))
	if !ok {
		return
	}
	if !h.moduleEnabled("llmplex") {
		writeNativeError(w, protocol, http.StatusNotFound, "MODULE_DISABLED", "model endpoints are disabled")
		return
	}
	listed := h.listedModels(r, tenant, ap)
	data := make([]map[string]any, 0, len(listed))
	for _, m := range listed {
		if protocol == WireAnthropic {
			data = append(data, map[string]any{"type": "model", "id": m.Entry.Name, "display_name": m.Entry.Name, "created_at": "1970-01-01T00:00:00Z", "protocols": m.Protocols})
			continue
		}
		owner := m.Publisher
		if owner == "" {
			owner = "aiplex"
		}
		data = append(data, map[string]any{"id": m.Entry.Name, "object": "model", "created": 0, "owned_by": owner, "protocols": m.Protocols})
	}
	w.Header().Set("Cache-Control", "no-store")
	if protocol == WireAnthropic {
		page := map[string]any{"data": data, "has_more": false, "first_id": nil, "last_id": nil}
		if len(data) > 0 {
			page["first_id"], page["last_id"] = data[0]["id"], data[len(data)-1]["id"]
		}
		JSON(w, http.StatusOK, page)
		return
	}
	JSON(w, http.StatusOK, map[string]any{"object": "list", "data": data})
}

// AccessPointListGenAIModels serves GET /ap/{id}/v1beta/models in the Google
// GenAI shape, listing only models reachable through that protocol.
func (h *ConnectedHandler) AccessPointListGenAIModels(w http.ResponseWriter, r *http.Request) {
	tenant, ap, _, ok := h.accessPointGate(w, r, modelErrorWriter(WireGenAI))
	if !ok {
		return
	}
	if !h.moduleEnabled("llmplex") {
		writeNativeError(w, WireGenAI, http.StatusNotFound, "MODULE_DISABLED", "model endpoints are disabled")
		return
	}
	models := []map[string]any{}
	for _, m := range h.listedModels(r, tenant, ap) {
		for _, p := range m.Protocols {
			if p == WireGenAI {
				models = append(models, map[string]any{"name": "models/" + m.Entry.Name, "displayName": m.Entry.Name, "supportedGenerationMethods": []string{"generateContent", "streamGenerateContent", "countTokens", "embedContent"}})
			}
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	JSON(w, http.StatusOK, map[string]any{"models": models})
}
