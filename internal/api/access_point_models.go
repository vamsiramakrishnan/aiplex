package api

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/vamsiramakrishnan/aiplex/pkg/clientplex"
	"github.com/vamsiramakrishnan/aiplex/pkg/errorcode"
	"github.com/vamsiramakrishnan/aiplex/pkg/modelapi"
	"github.com/vamsiramakrishnan/aiplex/pkg/routing"
	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
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

func init() { accessPointWires = append(accessPointWires, accessPointMediaWires()...) }

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
	if hookCode(code) {
		// Request-hook refusals carry their stable code in each protocol's
		// own place for a machine-readable reason.
		switch protocol {
		case WireAnthropic:
			value.(map[string]any)["error"].(map[string]any)["code"] = strings.ToLower(code)
		case WireGenAI, WireGeminiLive:
			value.(map[string]any)["error"].(map[string]any)["details"] = []any{map[string]any{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "reason": code, "domain": "aiplex"}}
		case WireBedrockConverse, WireBedrockInvoke:
			value.(map[string]any)["code"] = strings.ToLower(code)
		}
	}
	body, _ := json.Marshal(value)
	return body
}

// hookCode reports a request-hook refusal code (HOOK_DENIED and friends).
func hookCode(code string) bool { return strings.HasPrefix(strings.ToUpper(code), "HOOK_") }

// writeNativeError renders one refusal in a wire protocol's own shape. It
// writes the body and status only; the producer of the refusal marks it with
// X-Aiplex-Error-Code (refuseNative), because only the producer knows the
// refusal is AIPlex's own and not a provider's error.
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

// refuseNative is writeNativeError for a refusal AIPlex produces itself: it
// also marks the response with the refusal's code (pkg/errorcode).
func refuseNative(w http.ResponseWriter, protocol string, status int, code, message string) {
	errorcode.Set(w.Header(), code)
	writeNativeError(w, protocol, status, code, message)
}

func modelErrorWriter(protocol string) accessPointErrorWriter {
	return func(w http.ResponseWriter, _ *http.Request, status int, code, message string) {
		refuseNative(w, protocol, status, code, message)
	}
}

// nativeErrorWriter passes successful and streaming responses through
// untouched. Error responses produced by AIPlex itself (its JSON API errors
// and the gateways' plain-text refusals) are re-rendered in the calling
// protocol's error shape; provider error bodies pass through unchanged.
type nativeErrorWriter struct {
	http.ResponseWriter
	protocol  string
	call      *accessPointCall // set when the refusal can be explained
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
		if w.status == http.StatusForbidden && w.call != nil && w.call.missingGrant != "" {
			// The access point admitted this caller, but a model grant is
			// missing: say which, and how to add it.
			code, message = "permission_denied", w.call.missingGrant
		}
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

// rewriteBodyModel replaces the value of the request's top-level "model"
// string with model and leaves every other byte of the body (key order,
// whitespace, unknown fields, escapes, numbers, nested "model" keys) exactly
// as sent. The body must be one unambiguous JSON object: duplicate keys at any
// level, including a second top-level "model" (also when spelled with
// escapes), are rejected. The response's "model" field is the provider's and
// stays the native name; only the request is rewritten.
func rewriteBodyModel(data []byte, model string) ([]byte, error) {
	if err := modelapi.ValidateJSON(data); err != nil {
		return nil, fmt.Errorf("request body must be one unambiguous JSON object")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	if open, err := dec.Token(); err != nil || open != json.Delim('{') {
		return nil, fmt.Errorf("request body must be one JSON object")
	}
	start, end := -1, -1
	for dec.More() {
		keyToken, err := dec.Token()
		key, isString := keyToken.(string)
		if err != nil || !isString {
			return nil, fmt.Errorf("request body must be one JSON object")
		}
		var value json.RawMessage
		if err = dec.Decode(&value); err != nil {
			return nil, fmt.Errorf("request body must be one JSON object")
		}
		if key != "model" {
			continue
		}
		if start >= 0 {
			return nil, fmt.Errorf("request body names the model twice")
		}
		end = int(dec.InputOffset())
		start = end - len(value)
		if start < 0 || !bytes.Equal(data[start:end], value) || len(value) < 2 || value[0] != '"' {
			return nil, fmt.Errorf("request body model must be a string")
		}
	}
	if start < 0 {
		return nil, fmt.Errorf("request body must name a model")
	}
	encoded, err := json.Marshal(model)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(data)-(end-start)+len(encoded))
	out = append(out, data[:start]...)
	out = append(out, encoded...)
	return append(out, data[end:]...), nil
}

// accessPointBodyModelWire reports whether a wire carries the model as a
// top-level JSON string field of the request body that can be rewritten in
// place. Multipart image and audio uploads carry it as a form field next to
// file parts and are rewritten by rewriteMultipartAlias instead.
func accessPointBodyModelWire(protocol, contentType string) bool {
	switch protocol {
	case WireOpenAIChat, WireOpenAIResponses, WireAnthropic, WireOpenAIEmbeddings:
		return true
	case WireOpenAIImages, WireOpenAIAudio:
		media, _, _ := mime.ParseMediaType(contentType)
		return media != "multipart/form-data"
	}
	return false
}

// accessPointMultipartModelWire reports whether the request is an image or
// audio upload whose model is the "model" field of a multipart form.
func accessPointMultipartModelWire(protocol, contentType string) bool {
	if protocol != WireOpenAIImages && protocol != WireOpenAIAudio {
		return false
	}
	media, _, _ := mime.ParseMediaType(contentType)
	return media == "multipart/form-data"
}

// accessPointMultipartName reads the model of a multipart upload for every
// request, whether the client used an alias or a native name. The strict
// parser (the one the rewriter and the gateway use) decides, and the standard
// library must read the same model; a body the two read differently, or that
// is outside the strict subset, is refused before anything is forwarded.
func accessPointMultipartName(data []byte, contentType string) (string, error) {
	name, err := modelapi.MultipartModel(bytes.NewReader(data), contentType)
	if err != nil {
		return "", err
	}
	if std, form, err := multipartModel(data, contentType); err != nil || !form || std != name {
		return "", fmt.Errorf("invalid multipart body")
	}
	return name, nil
}

// rewriteMultipartAlias returns the upload with the client-facing model name
// replaced by the native one and every other byte as sent. The body is
// already bounded (the access point reads at most 8 MiB before it routes), so
// the rewritten copy is bounded too; the rewriter itself holds neither a
// part nor the file, wherever the model field sits. The result is checked
// against the standard multipart parser, which the upstream hop uses: the
// field it finds must be the native name, and the length must differ by
// exactly the difference of the two names.
func rewriteMultipartAlias(data []byte, contentType, alias, native string) ([]byte, error) {
	out := bytes.NewBuffer(make([]byte, 0, len(data)+len(native)))
	original, err := modelapi.RewriteMultipartModel(out, bytes.NewReader(data), contentType, native)
	if err != nil {
		return nil, err
	}
	if original != alias || out.Len() != len(data)+len(native)-len(alias) {
		return nil, fmt.Errorf("invalid multipart body")
	}
	if model, form, err := multipartModel(out.Bytes(), contentType); err != nil || !form || model != native {
		return nil, fmt.Errorf("invalid multipart body")
	}
	return out.Bytes(), nil
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
		// The call travels with the request from here on, so every ledger record
		// of it (a refusal before a backend is chosen included) takes its access
		// point, caller kind and tags from the call, in beginUsage.
		r = r.WithContext(withAccessPointCall(r.Context(), call))
		if !h.moduleEnabled("llmplex") {
			fail(w, r, http.StatusNotFound, "MODULE_DISABLED", "model endpoints are disabled")
			return
		}
		local, ok := accessPointLocalPath(r)
		if !ok {
			fail(w, r, http.StatusBadRequest, "INVALID_PATH", "invalid request path")
			return
		}
		local, deployment, azure := accessPointAzureLocal(local)
		data, err := io.ReadAll(io.LimitReader(r.Body, (8<<20)+1))
		if err != nil || len(data) > 8<<20 {
			fail(w, r, http.StatusRequestEntityTooLarge, "REQUEST_TOO_LARGE", "request body exceeds 8 MiB")
			return
		}
		var name, method string
		var bodyHasModel bool
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
		case protocol == WireOpenAIImages || protocol == WireOpenAIAudio:
			if accessPointMultipartModelWire(protocol, r.Header.Get("Content-Type")) {
				if len(r.Header.Values("Content-Type")) != 1 {
					err = fmt.Errorf("a multipart request needs exactly one Content-Type header")
				} else {
					name, err = accessPointMultipartName(data, r.Header.Get("Content-Type"))
				}
			} else {
				name, err = bodyModel(data)
			}
		case deployment != "":
			// Azure deployment routes name the model in the path.
			name = deployment
			bodyHasModel, err = deploymentBodyModel(data, deployment)
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
			// The caller passed the access point's gate, so the refusal is
			// recorded: who asked for what, with the requested name bounded
			// and sanitized because it is caller-controlled text. Requests
			// that fail the gate (unauthenticated, not admitted) write
			// nothing, so they cannot fill the ledger.
			unknownModel(h, w, r, tenant, call, name, fail)
			return
		}
		if ap.QoS != nil && h.Quota == nil {
			fail(w, r, http.StatusServiceUnavailable, "QUOTA_UNAVAILABLE", "access point QoS requires quota enforcement, which is not configured")
			return
		}
		in := accessPointClientRequest{protocol: protocol, local: local, deployment: deployment, azure: azure, data: data, name: name, method: method, bodyHasModel: bodyHasModel}
		if entry.routed() {
			h.serveRoutedModel(w, r, tenant, ap, call, *entry, in)
			return
		}
		inner, failure := h.accessPointBackendRequest(r, tenant, call, entry.primary(), in)
		if failure != nil {
			fail(w, r, failure.status, failure.code, failure.message)
			return
		}
		writer := &nativeErrorWriter{ResponseWriter: w, protocol: protocol, call: call}
		defer writer.finish()
		h.ClientGateway(writer, inner)
	}
}

// accessPointClientRequest is what the access point read from the caller's
// request before choosing a backend.
type accessPointClientRequest struct {
	protocol     string
	local        string // the path below /ap/{id}
	deployment   string // Azure deployment route segment, if any
	azure        bool
	data         []byte // the caller's body
	name         string // the client-facing model name
	method       string // GenAI or Bedrock method from the path
	bodyHasModel bool
}

// backendFailure is a refusal raised while building a backend request.
type backendFailure struct {
	status        int
	code, message string
}

// accessPointBackendRequest builds the delegated request that asks one
// backend (a saved surface and its native model name) for the caller's
// request: the model name is rewritten where the wire carries it and every
// other byte is left as sent.
func (h *ConnectedHandler) accessPointBackendRequest(r *http.Request, tenant string, call *accessPointCall, backend routing.Backend, in accessPointClientRequest) (*http.Request, *backendFailure) {
	protocol, data := in.protocol, in.data
	connection, err := h.accessPointSurface(r.Context(), tenant, backend.SurfaceRef)
	if err != nil {
		return nil, &backendFailure{http.StatusServiceUnavailable, "SURFACE_UNAVAILABLE", "model surface is unavailable"}
	}
	serving := accessPointWireProtocols(connection.Surface.Protocol)
	reachable := false
	for _, p := range serving {
		if p == protocol {
			reachable = true
		}
	}
	if !reachable {
		served := accessPointServedWires(connection.Surface, backend.NativeModel)
		paths := make([]string, 0, len(served))
		for _, p := range served {
			paths = append(paths, p+" ("+accessPointWirePath(p)+")")
		}
		return nil, &backendFailure{http.StatusBadRequest, "PROTOCOL_MISMATCH", fmt.Sprintf("model %q is not served through %s; it is served through: %s", in.name, protocol, strings.Join(paths, ", "))}
	}
	path, body := in.local, data
	rawQuery := r.URL.RawQuery
	if in.azure {
		// api-version is accepted and ignored: the surface pins its own.
		rawQuery = dropAPIVersion(rawQuery)
	}
	if in.deployment != "" && !in.bodyHasModel {
		// A deployment-route body without a model gets the native one, so
		// every surface sees the model where its own wire expects it.
		if body, err = insertBodyModel(data, backend.NativeModel); err != nil {
			return nil, &backendFailure{http.StatusBadRequest, "INVALID_REQUEST", err.Error()}
		}
		data = body
	}
	if accessPointWebSocket(protocol) {
		body = nil
		rawQuery = url.Values{"model": {backend.NativeModel}}.Encode()
	}
	if in.name != backend.NativeModel {
		switch {
		case protocol == WireGenAI && in.method != "":
			path = "/v1beta/models/" + backend.NativeModel + ":" + in.method
		case protocol == WireBedrockConverse || protocol == WireBedrockInvoke:
			// The path selects the model; the body is relayed untouched.
			path = "/model/" + backend.NativeModel + "/" + in.method
		case protocol == WireOpenAIRealtime:
			// rawQuery already carries the native model.
		case accessPointBodyModelWire(protocol, r.Header.Get("Content-Type")) && accessPointAliasRewritable(connection.Surface):
			// Only the request's model value changes; the response keeps
			// the provider's native model name.
			if body, err = rewriteBodyModel(data, backend.NativeModel); err != nil {
				return nil, &backendFailure{http.StatusBadRequest, "INVALID_REQUEST", err.Error()}
			}
		case accessPointMultipartModelWire(protocol, r.Header.Get("Content-Type")) && accessPointAliasRewritable(connection.Surface):
			// The upload is copied through unchanged except for the one
			// "model" form field; Content-Length follows the new body.
			if body, err = rewriteMultipartAlias(data, r.Header.Get("Content-Type"), in.name, backend.NativeModel); err != nil {
				return nil, &backendFailure{http.StatusBadRequest, "INVALID_REQUEST", err.Error()}
			}
		default:
			return nil, &backendFailure{http.StatusServiceUnavailable, "MODEL_ALIAS_UNSUPPORTED", "this wire relays the body verbatim; the model name must be the native name"}
		}
	}
	if path, err = accessPointFacadePath(connection.Surface, protocol, backend.NativeModel, path); err != nil {
		return nil, &backendFailure{http.StatusServiceUnavailable, "SURFACE_UNSUPPORTED", "model surface cannot serve this protocol"}
	}
	inner := delegateRequest(r, call, "/api/v1/llm/gateway/"+backend.SurfaceRef+path, backend.SurfaceRef, body)
	inner.URL.RawQuery = rawQuery
	return inner, nil
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
		// A routed model is listed when the caller may use any backend; it
		// is described by the first such backend (backends serve the same
		// wires, which is checked when the access point is saved).
		for _, backend := range entry.allBackends() {
			connection, err := h.accessPointSurface(r.Context(), tenant, backend.SurfaceRef)
			if err != nil {
				continue
			}
			modelID, err := connection.Surface.ModelID(backend.NativeModel)
			if err != nil || h.authorize(r, "llm:model:"+modelID) != nil {
				continue
			}
			out = append(out, accessPointListedModel{Entry: entry, ModelID: modelID, Publisher: connection.Surface.Publisher, Protocols: accessPointServedWires(connection.Surface, backend.NativeModel)})
			break
		}
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
		refuseNative(w, protocol, http.StatusNotFound, "MODULE_DISABLED", "model endpoints are disabled")
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
		refuseNative(w, WireGenAI, http.StatusNotFound, "MODULE_DISABLED", "model endpoints are disabled")
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

// missingGrantMessage explains a 403 from an admitted caller who lacks model
// grants. Admission lets a caller reach the access point; the model, surface
// and agent grants are separate and are set by an operator. The missing grants
// are named exactly; the suggested command grants the whole surface
// (llm:surface:{surface}:*) rather than the one operation. `user-scopes set`
// replaces the whole list, so the message tells the operator to keep the
// caller's existing scopes.
func missingGrantMessage(principal string, missing []string) string {
	if principal == "" {
		principal = "<principal-id>"
	}
	var suggest []string
	seen := map[string]bool{}
	for _, scope := range missing {
		if rest, ok := strings.CutPrefix(scope, "llm:surface:"); ok {
			if surface, _, found := strings.Cut(rest, ":"); found {
				scope = "llm:surface:" + surface + ":*"
			}
		}
		if !seen[scope] {
			seen[scope] = true
			suggest = append(suggest, scope)
		}
	}
	return fmt.Sprintf("this access point admits you, but your account is missing the grant(s): %s. An operator can add them with: aiplex agents user-scopes set %s --scopes %s (set replaces the list: include the scopes shown by `aiplex agents user-scopes get %s`)", strings.Join(missing, ", "), principal, strings.Join(suggest, ","), principal)
}

// maxRecordedModelName bounds the requested model name stored on a record.
const maxRecordedModelName = 128

// unknownModel answers 404 MODEL_NOT_FOUND and writes a ledger record for the
// admitted caller: status error, error_class model_not_found, no usage, no
// cost, and the requested name (sanitized, at most 128 characters) as
// model_id.
func unknownModel(h *ConnectedHandler, w http.ResponseWriter, r *http.Request, tenant string, call *accessPointCall, name string, fail accessPointErrorWriter) {
	w, r, capture := h.beginUsage(w, r, tenant, usageledger.PlaneLLM, "")
	defer capture.finish()
	capture.set(func(rec *usageledger.Record) {
		rec.Operation = "unknown-model"
		rec.ModelID = boundedModelName(name)
		if call != nil {
			rec.AccessPointID, rec.CallerKind = call.ID, call.CallerKind
		}
	})
	capture.uncosted()
	capture.noteClass("model_not_found")
	fail(w, r, http.StatusNotFound, "MODEL_NOT_FOUND", fmt.Sprintf("model %q is not available through this access point", name))
}

// boundedModelName makes a caller-supplied model name safe to store: control
// characters and invalid UTF-8 are dropped and the result is cut to
// maxRecordedModelName characters.
func boundedModelName(name string) string {
	var b strings.Builder
	count := 0
	for _, c := range strings.ToValidUTF8(name, "") {
		if c < 0x20 || c == 0x7f || c == 0xfffd {
			continue
		}
		if count == maxRecordedModelName {
			break
		}
		b.WriteRune(c)
		count++
	}
	return strings.TrimSpace(b.String())
}
