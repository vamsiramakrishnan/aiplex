package api

import (
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/vamsiramakrishnan/aiplex/pkg/modelapi"
)

// Text and utility operations on provider surfaces: client-facing paths
// (facade and access points). Native operations are in pkg/modelapi/ops_*.go.
//
// Facade (/api/v1/llm/gateway/{id}…), the SDK's own path under its base URL:
//
//	openai            POST /v1/embeddings, /v1/moderations
//	                  GET|DELETE /v1/responses/{id}, POST /v1/responses/{id}/cancel
//	azure-openai      POST /openai/v1/embeddings, /openai/deployments/{d}/embeddings
//	                  GET|DELETE /openai/v1/responses/{id}, POST …/{id}/cancel
//	anthropic         POST /v1/messages/count_tokens
//	gemini-developer  POST /v1beta/models/{m}:{generateContent|streamGenerateContent|
//	                       countTokens|embedContent|batchEmbedContents}
//	                  /v1beta/cachedContents[/{id}], /v1beta/interactions[/{id}[/cancel]]
//	                  GET geminiDeveloperLivePath (Live WebSocket)
const (
	WireOpenAIEmbeddings  = "openai-embeddings"
	WireOpenAIModerations = "openai-moderations"
)

// geminiDeveloperLivePath is the Gemini Developer API Live (BidiGenerateContent)
// WebSocket path, the path the Google GenAI SDK opens with an API key.
const geminiDeveloperLivePath = "/ws/google.ai.generativelanguage.v1beta.GenerativeService.BidiGenerateContent"

var (
	utilityID          = `[A-Za-z0-9][A-Za-z0-9._@-]*`
	geminiModelPath    = regexp.MustCompile(`^/v1beta/models/[A-Za-z0-9][A-Za-z0-9._@-]*:(generateContent|streamGenerateContent|countTokens|embedContent|batchEmbedContents)$`)
	geminiCachePath    = regexp.MustCompile(`^/v1beta/cachedContents(/` + utilityID + `)?$`)
	geminiInteractions = regexp.MustCompile(`^/v1beta/interactions(/` + utilityID + `(/cancel)?)?$`)
	openAIResponseID   = regexp.MustCompile(`^/responses/` + utilityID + `(/cancel)?$`)
	azureResponseID    = regexp.MustCompile(`^/openai/v1/responses/` + utilityID + `(/cancel)?$`)
	azureEmbeddingDep  = regexp.MustCompile(`^/openai/deployments/[A-Za-z0-9._:@-]+/embeddings$`)
)

// utilityClientPath maps a client path to the surface's native operation path.
// Only operations the surface installed are mapped; anything else falls
// through to the provider's original mapping and fails closed.
func utilityClientPath(s modelapi.Surface, method, path string) (string, bool) {
	native, ok := utilityNativePath(s, path)
	if !ok {
		return "", false
	}
	if _, _, installed := s.OperationFor(method, native); !installed {
		return "", false
	}
	return native, true
}

func utilityNativePath(s modelapi.Surface, path string) (string, bool) {
	switch s.Provider.ID {
	case modelapi.ProviderOpenAI:
		local := strings.TrimPrefix(path, "/v1")
		if local == "/embeddings" || local == "/moderations" || openAIResponseID.MatchString(local) {
			return "/v1" + local, true
		}
	case modelapi.ProviderAzureOpenAI:
		if path == "/openai/v1/embeddings" || azureEmbeddingDep.MatchString(path) || azureResponseID.MatchString(path) {
			return path, true
		}
	case modelapi.ProviderAnthropic:
		if path == "/v1/messages/count_tokens" {
			return path, true
		}
	case modelapi.ProviderGeminiDeveloper:
		if geminiModelPath.MatchString(path) || geminiCachePath.MatchString(path) || geminiInteractions.MatchString(path) || path == geminiDeveloperLivePath {
			return path, true
		}
	}
	return "", false
}

// genaiBodyModelPath reports Developer API creations whose model is in the body.
func genaiBodyModelPath(local string) bool {
	return local == "/v1beta/cachedContents" || local == "/v1beta/interactions"
}

// mountAccessPointUtility mounts the utility routes below /ap/{id}.
func (h *ConnectedHandler) mountAccessPointUtility(r chi.Router) {
	r.Post("/v1/embeddings", h.AccessPointModel(WireOpenAIEmbeddings))
	r.Post("/v1/moderations", h.AccessPointModel(WireOpenAIModerations))
	r.Post("/v1/messages/count_tokens", h.AccessPointModel(WireAnthropic))
	// Body-routed Developer API creations (model in the body).
	r.Post("/v1beta/cachedContents", h.AccessPointModel(WireGenAI))
	r.Post("/v1beta/interactions", h.AccessPointModel(WireGenAI))
	// Model-less operations: see AccessPointResource.
	responses := h.AccessPointResource(WireOpenAIResponses)
	r.Get("/v1/responses/{rid}", responses)
	r.Delete("/v1/responses/{rid}", responses)
	r.Post("/v1/responses/{rid}/cancel", responses)
	genai := h.AccessPointResource(WireGenAI)
	r.Get("/v1beta/cachedContents", genai)
	r.Get("/v1beta/cachedContents/{rid}", genai)
	r.Patch("/v1beta/cachedContents/{rid}", genai)
	r.Delete("/v1beta/cachedContents/{rid}", genai)
	r.Get("/v1beta/interactions/{rid}", genai)
	r.Delete("/v1beta/interactions/{rid}", genai)
	r.Post("/v1beta/interactions/{rid}/cancel", genai)
}

// moderationModelName is the access-point routing name of a Moderations
// request: the body model, or the API default when the body names none.
func moderationModelName(data []byte) (string, error) {
	if err := modelapi.ValidateJSON(data); err != nil {
		return "", err
	}
	var probe struct {
		Model *string `json:"model"`
	}
	if json.Unmarshal(data, &probe) != nil {
		return "", io.ErrUnexpectedEOF
	}
	if probe.Model == nil || *probe.Model == "" {
		return modelapi.DefaultModerationModel, nil
	}
	return *probe.Model, nil
}

// AccessPointResource serves operations that name no model: a stored resource
// addressed by ID (Responses retrieve/cancel/delete, cached contents and
// interactions get/update/cancel/delete) or the project-wide cached-content
// list. The surface is chosen by this rule:
//
//  1. Among the access point's surfaces that speak this wire protocol and have
//     installed the operation, an ID-addressed operation is served by the one
//     surface through which the caller created the resource (resource
//     ownership, the same binding previous_response_id uses). No owner means
//     404 in the caller's native error shape, indistinguishable from a
//     resource that does not exist; several owners (not expected, resource
//     IDs are unique upstream) means 409.
//  2. An operation with neither model nor ID (cached-content list) is served
//     only when exactly one surface qualifies; otherwise it is rejected with
//     400 naming the ambiguity. The gateway additionally requires the explicit
//     shared-listing grant for it.
//
// Caller admission, native errors, quota/ledger dimensions and the operation
// scope check are those of every other access-point model request: the request
// is delegated through the client facade to the managed native handler.
func (h *ConnectedHandler) AccessPointResource(protocol string) http.HandlerFunc {
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
		data, err := io.ReadAll(io.LimitReader(r.Body, (1<<20)+1))
		if err != nil || len(data) > 1<<20 {
			fail(w, r, http.StatusRequestEntityTooLarge, "REQUEST_TOO_LARGE", "request body exceeds 1 MiB")
			return
		}
		identity := GetWIFIdentity(r.Context())
		type candidate struct {
			ref string
			id  string
			op  modelapi.Operation
		}
		var candidates, owned []candidate
		seen := map[string]bool{}
		var refs []string
		for _, entry := range ap.Models {
			// A resource created through a routed model lives on the backend
			// that served it: every backend surface is a candidate owner.
			for _, b := range entry.allBackends() {
				refs = append(refs, b.SurfaceRef)
			}
		}
		for _, ref := range refs {
			entry := struct{ SurfaceRef string }{ref}
			if seen[entry.SurfaceRef] {
				continue
			}
			seen[entry.SurfaceRef] = true
			record, err := h.Store.Get(r.Context(), tenant, "model-surface", entry.SurfaceRef)
			if err != nil {
				continue
			}
			var connection ModelConnection
			if json.Unmarshal(record.Spec, &connection) != nil || connection.Surface.Provider == nil || connection.Surface.Validate() != nil {
				continue
			}
			served := false
			for _, p := range accessPointWireProtocols(connection.Surface.Protocol) {
				served = served || p == protocol
			}
			if !served {
				continue
			}
			native, _, err := clientNativePath(connection.Surface, r, local)
			if err != nil {
				continue
			}
			op, params, ok := connection.Surface.OperationFor(r.Method, native)
			if !ok {
				continue
			}
			c := candidate{ref: entry.SurfaceRef, id: params["id"], op: op}
			candidates = append(candidates, c)
			if c.id != "" && op.Stateful {
				ownership := resourceOwnership{store: h.Store, namespace: strconv.FormatUint(record.Revision, 10)}
				if ownership.Check(r.Context(), modelapi.Owner{TenantID: tenant, PrincipalID: identity.PrincipalID, SurfaceID: entry.SurfaceRef}, modelapi.Resource{Kind: op.ResourceKind, ID: c.id}) == nil {
					owned = append(owned, c)
				}
			}
		}
		if len(candidates) == 0 {
			fail(w, r, http.StatusNotFound, "UNSUPPORTED_OPERATION", "no model surface of this access point serves this operation")
			return
		}
		chosen := candidates[0]
		if chosen.id != "" {
			switch len(owned) {
			case 0:
				fail(w, r, http.StatusNotFound, "NOT_FOUND", "resource not found")
				return
			case 1:
				chosen = owned[0]
			default:
				fail(w, r, http.StatusConflict, "AMBIGUOUS_RESOURCE", "resource is owned through more than one surface of this access point")
				return
			}
		} else if len(candidates) > 1 {
			fail(w, r, http.StatusBadRequest, "AMBIGUOUS_SURFACE", "this operation names no model or resource and this access point has several surfaces serving it; use an access point with exactly one")
			return
		}
		if ap.QoS != nil && h.Quota == nil {
			fail(w, r, http.StatusServiceUnavailable, "QUOTA_UNAVAILABLE", "access point QoS requires quota enforcement, which is not configured")
			return
		}
		inner := delegateRequest(r, call, "/api/v1/llm/gateway/"+chosen.ref+local, chosen.ref, data)
		writer := &nativeErrorWriter{ResponseWriter: w, protocol: protocol, call: call}
		defer writer.finish()
		h.ClientGateway(writer, inner)
	}
}
