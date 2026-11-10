package api

import (
	"bytes"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/vamsiramakrishnan/aiplex/pkg/modelapi"
)

// Client facade paths of the media and batch operations. An unmodified SDK
// pointed at /api/v1/llm/gateway/{id}/v1 (OpenAI) or /api/v1/llm/gateway/{id}
// (Anthropic) sends its native path; it maps to the native operation path
// unchanged, and the native gateway still matches the surface's operation
// allowlist.
//
//	openai     /v1/audio/{transcriptions,translations,speech}, /v1/images/{generations,edits},
//	           /v1/files, /v1/files/{id}/content, /v1/batches[/{id}[/cancel]]
//	anthropic  /v1/messages/batches[/{id}[/cancel|/results]]
var (
	openAIExtendedClientPath    = regexp.MustCompile(`^/(audio/(transcriptions|translations|speech)|images/(generations|edits)|files|files/[A-Za-z0-9][A-Za-z0-9._-]*/content|batches|batches/[A-Za-z0-9][A-Za-z0-9._-]*(/cancel)?)$`)
	anthropicExtendedClientPath = regexp.MustCompile(`^/v1/messages/batches(/[A-Za-z0-9][A-Za-z0-9._-]*(/cancel|/results)?)?$`)
	geminiExtendedClientPath    = regexp.MustCompile(`^/v1beta/(models/[A-Za-z0-9][A-Za-z0-9._-]*:(batchGenerateContent|predictLongRunning)|batches/[A-Za-z0-9][A-Za-z0-9._-]*(:cancel)?|models/[A-Za-z0-9][A-Za-z0-9._-]*/operations/[A-Za-z0-9][A-Za-z0-9._-]*)$`)
)

// extendedClientNativePath maps a facade path to a media or batch native path.
func extendedClientNativePath(s modelapi.Surface, r *http.Request, path string) (string, bool) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		return "", false
	}
	switch s.Provider.ID {
	case modelapi.ProviderOpenAI:
		local := strings.TrimPrefix(path, "/v1")
		if openAIExtendedClientPath.MatchString(local) {
			return "/v1" + local, true
		}
	case modelapi.ProviderAnthropic:
		if anthropicExtendedClientPath.MatchString(path) {
			return path, true
		}
	case modelapi.ProviderGeminiDeveloper:
		if geminiExtendedClientPath.MatchString(path) {
			return path, true
		}
	}
	return "", false
}

// Access-point wires for media operations. The model is the request's own
// model field (JSON body, or the multipart "model" form field); batches carry
// no single model and are not routed by access points.
const (
	WireOpenAIImages = "openai-images"
	WireOpenAIAudio  = "openai-audio"
)

func accessPointMediaWires() []accessPointWire {
	surfaces := []string{modelapi.ProtocolOpenAIChat, modelapi.ProtocolOpenAIResponses, modelapi.ProtocolOpenAIImages, modelapi.ProtocolOpenAIAudio}
	return []accessPointWire{
		{WireOpenAIImages, "/v1/images/{generations,edits}", surfaces},
		{WireOpenAIAudio, "/v1/audio/{transcriptions,translations,speech}", surfaces},
	}
}

// mountAccessPointMedia mounts the media wires under /ap/{apid}.
func (h *ConnectedHandler) mountAccessPointMedia(r chi.Router) {
	r.Post("/v1/images/generations", h.AccessPointModel(WireOpenAIImages))
	r.Post("/v1/images/edits", h.AccessPointModel(WireOpenAIImages))
	r.Post("/v1/audio/transcriptions", h.AccessPointModel(WireOpenAIAudio))
	r.Post("/v1/audio/translations", h.AccessPointModel(WireOpenAIAudio))
	r.Post("/v1/audio/speech", h.AccessPointModel(WireOpenAIAudio))
}

// multipartModel reads the "model" form field of an in-memory multipart body
// (access points read at most 8 MiB). File parts are skipped unread.
func multipartModel(data []byte, contentType string) (string, bool, error) {
	media, params, err := mime.ParseMediaType(contentType)
	if err != nil || media != "multipart/form-data" {
		return "", false, nil
	}
	reader := multipart.NewReader(bytes.NewReader(data), params["boundary"])
	model := ""
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", true, fmt.Errorf("invalid multipart body")
		}
		if part.FileName() == "" && part.FormName() == "model" {
			value, err := io.ReadAll(io.LimitReader(part, 1024))
			if err != nil || model != "" {
				return "", true, fmt.Errorf("the model form field must appear once")
			}
			model = string(value)
		}
	}
	if model == "" {
		return "", true, fmt.Errorf("request must name a model")
	}
	return model, true, nil
}
