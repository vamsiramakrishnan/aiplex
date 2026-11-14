package modelapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/contracts"
	"github.com/vamsiramakrishnan/aiplex/pkg/execution"
	"github.com/vamsiramakrishnan/aiplex/pkg/providerlimits"
)

// Media and batch provider operations: OpenAI audio (transcriptions,
// translations, speech), images (generations, edits), Batch with the Files
// endpoints it needs, and Anthropic Message Batches. Their usage is not plain
// text tokens on one response, so they are served by this file rather than
// the text path: units are observed as first-class counters (units.go) and
// batch usage settles once from complete results (batch.go).
//
// Request paths and auth follow the vendors' references (retrieved 2026-11-10):
//
//	OpenAI   https://developers.openai.com/api/reference/resources/audio/subresources/transcriptions/methods/create
//	         https://developers.openai.com/api/reference/resources/audio/subresources/translations/methods/create
//	         https://developers.openai.com/api/reference/resources/audio/subresources/speech/methods/create
//	         https://developers.openai.com/api/reference/resources/images/methods/generate (and /edit)
//	         https://developers.openai.com/api/reference/resources/batches/methods/create
//	         https://developers.openai.com/api/reference/resources/files/methods/content
//	Anthropic https://platform.claude.com/docs/en/build-with-claude/batch-processing
//	         https://platform.claude.com/docs/en/api/messages/batches/results
//
// Credentials are the surface's (Authorization: Bearer for OpenAI, x-api-key
// for Anthropic); caller credentials never cross (providerRequestHeaders).

// Protocols and usage formats of the extended operations.
const (
	ProtocolOpenAIAudio        = "openai-audio"
	ProtocolOpenAIImages       = "openai-images"
	ProtocolOpenAIBatches      = "openai-batches"
	ProtocolAnthropicBatches   = "anthropic-message-batches"
	FormatOpenAITranscription  = "openai-transcription"
	FormatOpenAISpeech         = "openai-speech"
	FormatOpenAIImages         = "openai-images"
	FormatOpenAIBatch          = "openai-batch"
	FormatAnthropicBatch       = "anthropic-message-batches"
	openAIBatchFileResource    = "file"
	providerBatchResource      = "batch"
	extendedResultsPartResults = "results"
)

type extendedKind int

const (
	kindMediaJSON extendedKind = iota + 1
	kindMediaMultipart
	kindSpeech
	kindFileUpload
	kindBatchCreate
	kindBatchRead
	kindBatchList
	kindBatchResults
	kindFileContent
)

type extendedSpec struct {
	kind     extendedKind
	provider string
	format   string
}

var extendedSpecs = map[string]extendedSpec{
	"openai.v1.audio-transcriptions":    {kindMediaMultipart, ProviderOpenAI, FormatOpenAITranscription},
	"openai.v1.audio-translations":      {kindMediaMultipart, ProviderOpenAI, FormatOpenAITranscription},
	"openai.v1.audio-speech":            {kindSpeech, ProviderOpenAI, FormatOpenAISpeech},
	"openai.v1.images-generations":      {kindMediaJSON, ProviderOpenAI, FormatOpenAIImages},
	"openai.v1.images-edits":            {kindMediaMultipart, ProviderOpenAI, FormatOpenAIImages},
	"openai.v1.files-create":            {kindFileUpload, ProviderOpenAI, FormatOpenAIBatch},
	"openai.v1.files-content":           {kindFileContent, ProviderOpenAI, FormatOpenAIBatch},
	"openai.v1.batches-create":          {kindBatchCreate, ProviderOpenAI, FormatOpenAIBatch},
	"openai.v1.batches-get":             {kindBatchRead, ProviderOpenAI, FormatOpenAIBatch},
	"openai.v1.batches-cancel":          {kindBatchRead, ProviderOpenAI, FormatOpenAIBatch},
	"openai.v1.batches-list":            {kindBatchList, ProviderOpenAI, FormatOpenAIBatch},
	"anthropic.message-batches-create":  {kindBatchCreate, ProviderAnthropic, FormatAnthropicBatch},
	"anthropic.message-batches-get":     {kindBatchRead, ProviderAnthropic, FormatAnthropicBatch},
	"anthropic.message-batches-cancel":  {kindBatchRead, ProviderAnthropic, FormatAnthropicBatch},
	"anthropic.message-batches-list":    {kindBatchList, ProviderAnthropic, FormatAnthropicBatch},
	"anthropic.message-batches-results": {kindBatchResults, ProviderAnthropic, FormatAnthropicBatch},
}

// withExtendedOperations appends the media and batch operations to the
// provider catalog (one contiguous block per provider).
func withExtendedOperations(catalog map[string][]providerOp) map[string][]providerOp {
	ref := "https://developers.openai.com/api/reference/resources"
	anthropicRef := "https://platform.claude.com/docs/en/build-with-claude/batch-processing"
	op := func(id, method, path string, transport Transport, action Action, kind string, stateful bool, source, format, protocol string) providerOp {
		return providerOp{Operation{ID: id, Method: method, Path: path, Transport: transport, Action: action, ResourceKind: kind, Stateful: stateful, SourceURL: source}, format, protocol}
	}
	catalog[ProviderOpenAI] = append(catalog[ProviderOpenAI],
		op("openai.v1.audio-transcriptions", "POST", "/v1/audio/transcriptions", HTTP, Invoke, "", false, ref+"/audio/subresources/transcriptions/methods/create", FormatOpenAITranscription, ProtocolOpenAIAudio),
		op("openai.v1.audio-translations", "POST", "/v1/audio/translations", HTTP, Invoke, "", false, ref+"/audio/subresources/translations/methods/create", FormatOpenAITranscription, ProtocolOpenAIAudio),
		op("openai.v1.audio-speech", "POST", "/v1/audio/speech", HTTP, Invoke, "", false, ref+"/audio/subresources/speech/methods/create", FormatOpenAISpeech, ProtocolOpenAIAudio),
		op("openai.v1.images-generations", "POST", "/v1/images/generations", HTTP, Invoke, "", false, ref+"/images/methods/generate", FormatOpenAIImages, ProtocolOpenAIImages),
		op("openai.v1.images-edits", "POST", "/v1/images/edits", HTTP, Invoke, "", false, ref+"/images/methods/edit", FormatOpenAIImages, ProtocolOpenAIImages),
		op("openai.v1.files-create", "POST", "/v1/files", HTTP, Create, openAIBatchFileResource, true, ref+"/files/methods/create", FormatOpenAIBatch, ProtocolOpenAIBatches),
		op("openai.v1.files-content", "GET", "/v1/files/{id}/content", HTTP, Read, openAIBatchFileResource, true, ref+"/files/methods/content", FormatOpenAIBatch, ProtocolOpenAIBatches),
		op("openai.v1.batches-create", "POST", "/v1/batches", HTTP, Create, providerBatchResource, true, ref+"/batches/methods/create", FormatOpenAIBatch, ProtocolOpenAIBatches),
		op("openai.v1.batches-get", "GET", "/v1/batches/{id}", HTTP, Read, providerBatchResource, true, ref+"/batches/methods/retrieve", FormatOpenAIBatch, ProtocolOpenAIBatches),
		op("openai.v1.batches-cancel", "POST", "/v1/batches/{id}/cancel", HTTP, Cancel, providerBatchResource, true, ref+"/batches/methods/cancel", FormatOpenAIBatch, ProtocolOpenAIBatches),
		op("openai.v1.batches-list", "GET", "/v1/batches", HTTP, List, providerBatchResource, true, ref+"/batches/methods/list", FormatOpenAIBatch, ProtocolOpenAIBatches),
		liveVoiceCatalog(),
	)
	catalog[ProviderAnthropic] = append(catalog[ProviderAnthropic],
		op("anthropic.message-batches-create", "POST", "/v1/messages/batches", HTTP, Create, providerBatchResource, true, anthropicRef, FormatAnthropicBatch, ProtocolAnthropicBatches),
		op("anthropic.message-batches-get", "GET", "/v1/messages/batches/{id}", HTTP, Read, providerBatchResource, true, anthropicRef, FormatAnthropicBatch, ProtocolAnthropicBatches),
		op("anthropic.message-batches-cancel", "POST", "/v1/messages/batches/{id}/cancel", HTTP, Cancel, providerBatchResource, true, anthropicRef, FormatAnthropicBatch, ProtocolAnthropicBatches),
		op("anthropic.message-batches-list", "GET", "/v1/messages/batches", HTTP, List, providerBatchResource, true, anthropicRef, FormatAnthropicBatch, ProtocolAnthropicBatches),
		op("anthropic.message-batches-results", "GET", "/v1/messages/batches/{id}/results", HTTP, Read, providerBatchResource, true, "https://platform.claude.com/docs/en/api/messages/batches/results", FormatAnthropicBatch, ProtocolAnthropicBatches),
	)
	catalog[ProviderGeminiDeveloper] = append(catalog[ProviderGeminiDeveloper], geminiExtendedOperations()...)
	return catalog
}

// extendedCoverage is the execution coverage of operations outside the text
// table in coverage.go. Media exchanges are one request each (a media-units
// profile may also meter their units); batch operations are asynchronous:
// HTTP completion of create never settles the job's work.
func extendedCoverage(op Operation) ExecutionCoverage {
	if op.ID == openAILiveOperation {
		// One admitted request per session, like the other WebSocket relays.
		return ExecutionCoverage{RequestCount: true}
	}
	spec, ok := extendedSpecs[op.ID]
	if !ok {
		return ExecutionCoverage{}
	}
	switch spec.kind {
	case kindBatchCreate, kindBatchRead, kindBatchList, kindBatchResults, kindFileContent, kindFileUpload, kindPredictLongRunning, kindOperationRead:
		return ExecutionCoverage{RequestCount: true, Asynchronous: true}
	}
	return ExecutionCoverage{RequestCount: true}
}

// MediaQuotaOperation reports operations a media-units quota profile may
// bind: their terminal response reports the units it settles.
func MediaQuotaOperation(id string) bool {
	switch id {
	case "openai.v1.audio-transcriptions", "openai.v1.audio-translations", "openai.v1.images-generations", "openai.v1.images-edits":
		return true
	}
	return false
}

// BatchCreateOperation reports batch-create operations a batch-usage quota
// profile may bind.
func BatchCreateOperation(id string) bool {
	return id == "openai.v1.batches-create" || id == "anthropic.message-batches-create" || id == "gemini.models.batchGenerateContent"
}

// ExtendedOperation reports whether an operation is served by this file.
func ExtendedOperation(id string) bool { _, ok := extendedSpecs[id]; return ok }

func (g *Gateway) maxUploadBytes() int64 {
	limit := g.options.MaxUploadBytes
	if limit <= 0 {
		limit = DefaultMaxUploadBytes
	}
	if limit > 1<<30 {
		limit = 1 << 30
	}
	return limit
}

// serveProviderExtended serves one media or batch operation. It returns false
// for every other operation, which stays on the text path.
func (g *Gateway) serveProviderExtended(w http.ResponseWriter, r *http.Request, trusted contracts.RequestContext, owner Owner, op Operation, params map[string]string) bool {
	spec, ok := extendedSpecs[op.ID]
	if !ok || spec.provider != g.surface.Provider.ID {
		return false
	}
	metadata := Metadata{Transport: op.Transport}
	if model := params["model"]; model != "" {
		if !g.allowedModel(model) {
			http.Error(w, "model is not allowed", http.StatusForbidden)
			return true
		}
		metadata.Model = model
	}
	if id := params["id"]; id != "" {
		if !validSegment(id) {
			http.Error(w, "invalid resource identifier", http.StatusBadRequest)
			return true
		}
		metadata.Resource = Resource{op.ResourceKind, id}
		if g.options.Ownership == nil || g.options.Ownership.Check(r.Context(), owner, metadata.Resource) != nil {
			http.Error(w, "resource ownership denied", http.StatusForbidden)
			return true
		}
	}
	if spec.kind == kindBatchList && (g.options.AllowShared == nil || !g.options.AllowShared(r.Context(), trusted, g.Surface(), op)) {
		http.Error(w, "shared listing requires explicit authorization", http.StatusForbidden)
		return true
	}
	var body []byte
	var upload *spooledUpload
	defer func() { upload.Close() }()
	var err error
	images := int64(1)
	switch spec.kind {
	case kindMediaJSON, kindSpeech, kindBatchCreate, kindPredictLongRunning:
		body, err = io.ReadAll(io.LimitReader(r.Body, g.options.MaxBodyBytes+1))
		if err != nil || int64(len(body)) > g.options.MaxBodyBytes {
			http.Error(w, "invalid or oversized request body", http.StatusBadRequest)
			return true
		}
		images, err = g.validateExtendedJSON(r.Context(), owner, op, spec, &metadata, body)
	case kindMediaMultipart, kindFileUpload:
		// Exactly one Content-Type: the one validated is the one forwarded.
		if len(r.Header.Values("Content-Type")) != 1 {
			err = badRequest("a multipart request needs exactly one Content-Type header")
			break
		}
		upload, err = spoolMultipart(r.Body, r.Header.Get("Content-Type"), g.maxUploadBytes())
		if err == nil {
			images, err = g.validateExtendedForm(op, spec, &metadata, upload)
			body = upload.canonical()
		}
	default:
		probe, readErr := io.ReadAll(io.LimitReader(r.Body, 1))
		if readErr != nil || len(probe) != 0 {
			err = badRequest("this operation takes no request body")
		}
	}
	if err != nil {
		writeProviderRequestError(w, err)
		return true
	}
	headers, err := g.providerRequestHeaders(r.Header)
	if err != nil {
		writeProviderRequestError(w, err)
		return true
	}
	if err = g.options.Authorize(r.Context(), trusted, g.Surface(), op, metadata); err != nil {
		http.Error(w, "operation denied", http.StatusForbidden)
		return true
	}
	// Extended operations (images, audio, files, batches) have no response
	// extractor: the hooks refuse them up front when a response hook applies.
	body, _, lease, err := g.prepareDispatch(r, trusted, op, metadata, body)
	if err != nil {
		writeDispatchError(w, err)
		return true
	}
	var run *executionRun
	if lease != nil {
		run = &executionRun{lease: lease, ctx: r.Context()}
		defer run.close()
		if receipt, ok := lease.(execution.Receipt); ok {
			if id := receipt.AttemptID(); id != "" && len(id) <= 1024 && !strings.ContainsAny(id, "\r\n") {
				w.Header().Set(execution.AttemptHeader, id)
			}
		}
	}
	upstream, err := url.Parse(g.upstreamOrigin() + r.URL.EscapedPath())
	if err != nil {
		http.Error(w, "invalid upstream request", http.StatusBadRequest)
		return true
	}
	upstream.RawQuery = ""
	if spec.kind == kindBatchList {
		// Pagination only; no other caller query crosses.
		query := url.Values{}
		for _, key := range []string{"limit", "after", "before_id", "after_id"} {
			if value := r.URL.Query().Get(key); value != "" && len(value) <= 256 && validSegment(value) {
				query.Set(key, value)
			}
		}
		upstream.RawQuery = query.Encode()
	}
	var reader io.Reader = bytes.NewReader(body)
	length := int64(len(body))
	switch {
	case upload != nil:
		if reader, err = upload.reader(); err != nil {
			http.Error(w, "upload spool unavailable", http.StatusServiceUnavailable)
			return true
		}
		length = upload.size
	case spec.kind != kindMediaJSON && spec.kind != kindSpeech && spec.kind != kindBatchCreate && spec.kind != kindPredictLongRunning:
		reader, length = http.NoBody, 0
	}
	request, err := http.NewRequestWithContext(r.Context(), r.Method, upstream.String(), reader)
	if err != nil {
		http.Error(w, "invalid upstream request", http.StatusBadRequest)
		return true
	}
	request.ContentLength = length
	request.Header = headers
	if err = g.options.Credential.Authenticate(r.Context(), request); err != nil {
		http.Error(w, "upstream identity unavailable", http.StatusServiceUnavailable)
		return true
	}
	if err = run.beforeDispatch(r.Context()); err != nil {
		writeExecutionError(w, err)
		return true
	}
	// Media exchanges are observed for routing but settle a transport
	// failure as uncertain (their leases do not take NotSent).
	response, _, err := g.do(request)
	if err != nil {
		if finishErr := run.finish(execution.Result{Err: err}); finishErr != nil {
			writeExecutionError(w, finishErr)
			return true
		}
		http.Error(w, "upstream unavailable", http.StatusBadGateway)
		return true
	}
	defer response.Body.Close()
	if g.options.ObserveLimits != nil {
		g.options.ObserveLimits(r.Context(), owner, providerlimits.Parse(g.surface.Provider.ID, response.StatusCode, response.Header, time.Now()))
	}
	if g.handleUpstreamAuth(w, r, run, response) {
		return true
	}
	g.noteRejectionClass(r, response)
	x := extendedExchange{g: g, w: w, r: r, owner: owner, op: op, spec: spec, run: run, response: response, metadata: metadata, images: images, body: body}
	x.serve()
	return true
}

// validateExtendedJSON validates a JSON media or batch-create body and
// returns the requested image count (1 when not an image request).
func (g *Gateway) validateExtendedJSON(ctx context.Context, owner Owner, op Operation, spec extendedSpec, metadata *Metadata, body []byte) (int64, error) {
	if len(bytes.TrimSpace(body)) == 0 || validateJSON(body) != nil {
		return 0, badRequest("a native JSON request body is required")
	}
	var doc map[string]json.RawMessage
	if json.Unmarshal(body, &doc) != nil || doc == nil {
		return 0, badRequest("a native JSON object is required")
	}
	for key := range doc {
		for _, reserved := range []string{"model", "stream", "stream_format", "n", "requests", "input_file_id", "endpoint"} {
			if strings.EqualFold(key, reserved) && key != reserved {
				return 0, forbidden("ambiguous security field spelling")
			}
		}
	}
	if spec.provider == ProviderGeminiDeveloper {
		// The model is the path segment; every model named in the body must be it.
		return 1, g.geminiBatchModels(body, metadata.Model)
	}
	if spec.kind == kindBatchCreate {
		return 1, g.validateBatchCreate(ctx, owner, spec, metadata, doc)
	}
	var model string
	if raw := doc["model"]; raw == nil || json.Unmarshal(raw, &model) != nil || !g.allowedModel(model) {
		return 0, forbidden("model is required and must be allowed")
	}
	metadata.Model = model
	if raw := doc["stream"]; raw != nil {
		var stream bool
		if json.Unmarshal(raw, &stream) != nil {
			return 0, badRequest("stream must be a boolean")
		}
		if stream {
			metadata.Transport = SSE
		}
	}
	if raw := doc["stream_format"]; raw != nil {
		var format string
		if json.Unmarshal(raw, &format) != nil || (format != "sse" && format != "audio") {
			return 0, badRequest("stream_format must be sse or audio")
		}
		if format == "sse" {
			metadata.Transport = SSE
		}
	}
	n := int64(1)
	if raw := doc["n"]; raw != nil {
		if json.Unmarshal(raw, &n) != nil || n < 1 || n > 10 {
			return 0, badRequest("n must be an integer from 1 to 10")
		}
	}
	return n, nil
}

func (g *Gateway) validateBatchCreate(ctx context.Context, owner Owner, spec extendedSpec, metadata *Metadata, doc map[string]json.RawMessage) error {
	switch spec.provider {
	case ProviderOpenAI:
		if doc["model"] != nil {
			return forbidden("batch models are selected per request line")
		}
		var fileID, endpoint string
		if json.Unmarshal(doc["input_file_id"], &fileID) != nil || !validSegment(fileID) {
			return badRequest("input_file_id is required")
		}
		if json.Unmarshal(doc["endpoint"], &endpoint) != nil {
			return badRequest("endpoint is required")
		}
		switch endpoint {
		case "/v1/responses", "/v1/chat/completions", "/v1/embeddings", "/v1/completions", "/v1/moderations", "/v1/images/generations", "/v1/images/edits", "/v1/videos":
		default:
			return badRequest("unsupported batch endpoint")
		}
		// Only an input file uploaded through this surface by the same owner.
		metadata.References = append(metadata.References, Resource{openAIBatchFileResource, fileID})
		if g.options.Ownership == nil || g.options.Ownership.Check(ctx, owner, Resource{openAIBatchFileResource, fileID}) != nil {
			return forbidden("batch input file ownership denied")
		}
	case ProviderAnthropic:
		var requests []struct {
			CustomID string                     `json:"custom_id"`
			Params   map[string]json.RawMessage `json:"params"`
		}
		if json.Unmarshal(doc["requests"], &requests) != nil || len(requests) == 0 || len(requests) > 100_000 {
			return badRequest("requests must be a non-empty list")
		}
		models := map[string]bool{}
		for _, item := range requests {
			var model string
			if item.CustomID == "" || item.Params == nil || json.Unmarshal(item.Params["model"], &model) != nil || !g.allowedModel(model) {
				return forbidden("every batch request needs a custom_id and an allowed model")
			}
			for key := range item.Params {
				if strings.EqualFold(key, "model") && key != "model" {
					return forbidden("ambiguous security field spelling")
				}
			}
			models[model] = true
		}
		if len(models) == 1 {
			for model := range models {
				metadata.Model = model
			}
		}
	}
	return nil
}

// validateExtendedForm validates the fields of a multipart media request or
// batch input upload.
func (g *Gateway) validateExtendedForm(op Operation, spec extendedSpec, metadata *Metadata, upload *spooledUpload) (int64, error) {
	if upload.files == 0 {
		return 0, badRequest("a file part is required")
	}
	if spec.kind == kindFileUpload {
		purpose, ok, err := upload.field("purpose")
		if err != nil {
			return 0, err
		}
		if !ok || purpose != "batch" {
			// Only batch input files: other purposes are not ownership-scoped
			// consumers of this surface.
			return 0, forbidden("only purpose=batch uploads are served")
		}
		return 1, nil
	}
	model, ok, err := upload.field("model")
	if err != nil {
		return 0, err
	}
	if !ok || !g.allowedModel(model) {
		return 0, forbidden("model is required and must be allowed")
	}
	metadata.Model = model
	if stream, ok, err := upload.field("stream"); err != nil {
		return 0, err
	} else if ok {
		switch stream {
		case "true":
			metadata.Transport = SSE
		case "false":
		default:
			return 0, badRequest("stream must be true or false")
		}
	}
	n := int64(1)
	if raw, ok, err := upload.field("n"); err != nil {
		return 0, err
	} else if ok {
		if json.Unmarshal([]byte(raw), &n) != nil || n < 1 || n > 10 {
			return 0, badRequest("n must be an integer from 1 to 10")
		}
	}
	return n, nil
}

// extendedExchange handles one upstream response.
type extendedExchange struct {
	g        *Gateway
	w        http.ResponseWriter
	r        *http.Request
	owner    Owner
	op       Operation
	spec     extendedSpec
	run      *executionRun
	response *http.Response
	metadata Metadata
	images   int64
	body     []byte
}

func (x *extendedExchange) success() bool {
	return x.response.StatusCode >= 200 && x.response.StatusCode < 300
}

func (x *extendedExchange) mediaType() string {
	media, _, _ := mime.ParseMediaType(x.response.Header.Get("Content-Type"))
	return media
}

func (x *extendedExchange) writeHeaders() {
	x.g.copyProviderResponseHeaders(x.w.Header(), x.response.Header, x.response.StatusCode)
	x.w.WriteHeader(x.response.StatusCode)
}

// emit forwards one observation to the lease (or the legacy observer).
func (x *extendedExchange) emit(value UsageObservation) error {
	value.Source, value.SurfaceID, value.OperationID = "upstream", x.g.surface.ID, x.op.ID
	if value.Counters == nil {
		value.Counters = map[string]int64{}
	}
	if x.run != nil {
		if err := x.run.observe(value); err != nil {
			return err
		}
	}
	x.g.emitLegacy(x.r.Context(), x.owner, &value)
	return nil
}

func (x *extendedExchange) readBounded() ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(x.response.Body, x.g.options.MaxBodyBytes+1))
	if err == nil && int64(len(data)) > x.g.options.MaxBodyBytes {
		err = errors.New("response observation bound exceeded")
	}
	return data, err
}

func (x *extendedExchange) fail(err error, status int, message string) {
	_ = x.run.finish(execution.Result{StatusCode: x.response.StatusCode, Err: err})
	http.Error(x.w, message, status)
}

func (x *extendedExchange) serve() {
	if x.response.StatusCode >= 300 && x.response.StatusCode < 400 {
		x.fail(errors.New("upstream redirect refused"), http.StatusBadGateway, "upstream redirects are not supported")
		return
	}
	switch x.spec.kind {
	case kindBatchCreate, kindBatchRead, kindBatchList, kindBatchResults, kindFileContent, kindFileUpload:
		if x.spec.provider == ProviderGeminiDeveloper && x.spec.kind != kindBatchCreate && x.spec.kind != kindBatchRead {
			break
		}
		// Every batch exchange is labelled batch on the ledger, whatever its outcome.
		if err := x.batchMarker(); err != nil {
			x.fail(err, http.StatusBadGateway, "upstream response could not be acknowledged")
			return
		}
	}
	if !x.success() {
		data, err := x.readBounded()
		if err != nil {
			x.fail(err, http.StatusBadGateway, "upstream response exceeds observation bound")
			return
		}
		// The same not-processed rule as the text paths (rejections.go).
		if err = x.run.finish(execution.Result{StatusCode: x.response.StatusCode, ResponseComplete: true, Rejected: x.g.rejectedBeforeProcessing(x.response.StatusCode)}); err != nil {
			writeExecutionError(x.w, err)
			return
		}
		x.writeHeaders()
		_, _ = x.w.Write(data)
		return
	}
	switch x.spec.kind {
	case kindMediaJSON, kindMediaMultipart, kindSpeech:
		x.serveMedia()
	case kindFileUpload:
		x.serveFileUpload()
	case kindBatchCreate:
		x.serveBatchCreate()
	case kindBatchRead:
		x.serveBatchRead()
	case kindBatchList:
		x.serveJSONPassthrough()
	case kindBatchResults:
		x.serveResults(x.op.ID, x.metadata.Resource.ID, extendedResultsPartResults)
	case kindFileContent:
		x.serveFileContent()
	case kindPredictLongRunning:
		x.servePrediction()
	case kindOperationRead:
		x.serveJSONPassthrough()
	}
}

// serveJSONPassthrough relays a bounded JSON body after the exchange settles.
func (x *extendedExchange) serveJSONPassthrough() {
	data, err := x.readBounded()
	if err != nil {
		x.fail(err, http.StatusBadGateway, "upstream response exceeds observation bound")
		return
	}
	if err = x.run.finish(execution.Result{StatusCode: x.response.StatusCode, ResponseComplete: true}); err != nil {
		writeExecutionError(x.w, err)
		return
	}
	x.writeHeaders()
	_, _ = x.w.Write(data)
}

// streamPassthrough relays a body of unknown size (audio, transcript text)
// without buffering it, then finishes the exchange.
func (x *extendedExchange) streamPassthrough() {
	x.writeHeaders()
	_, err := io.Copy(flushWriter{x.w}, x.response.Body)
	if err == nil && x.r.Context().Err() != nil {
		err = x.r.Context().Err()
	}
	_ = x.run.finish(execution.Result{StatusCode: x.response.StatusCode, ResponseComplete: err == nil, Err: err})
}

type flushWriter struct{ w http.ResponseWriter }

func (f flushWriter) Write(p []byte) (int, error) {
	n, err := f.w.Write(p)
	if flusher, ok := f.w.(http.Flusher); ok {
		flusher.Flush()
	}
	return n, err
}
