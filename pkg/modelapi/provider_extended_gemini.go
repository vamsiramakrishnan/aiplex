package modelapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Gemini Developer API batch and long-running media operations, added to the
// gemini-developer descriptor (ops_gemini_developer.go).
//
// Read in https://aistudio.google.com/api-reference/batch-api (2026-11-10):
// POST /v1beta/models/{model}:batchGenerateContent enqueues a batch; GET
// /v1beta/batches/{id} returns its latest state; POST
// /v1beta/batches/{id}:cancel. The batch name is "batches/{batch_id}". Output
// is either inlinedResponses (each item a GenerateContentResponse "response"
// or a Status "error") or a responsesFile. From the reference page
// docs/reference/usage-formats/google.md (section 9): states
// BATCH_STATE_{PENDING,RUNNING,SUCCEEDED,FAILED,CANCELLED,EXPIRED} and
// batchStats.requestCount; no job-level token totals.
//
// Settlement: inline results arrive in the terminal batch object itself, so
// the retrieve exchange that first observes a terminal batch whose inline
// items account for batchStats.requestCount settles it (once, as for the
// other providers). The responsesFile form's per-line wrapper is UNCONFIRMED:
// such a batch is never settled from it and keeps its exposure. Whether the
// create response is the batch or a long-running Operation wrapper is not
// stated; only a top-level name "batches/{id}" is accepted as its identity.
//
// Video (Veo) on the Developer API: POST /v1beta/models/{model}:predictLongRunning,
// poll the operation (google.md section 11). The documented done result
// (response.generateVideoResponse.generatedSamples[].video.uri) carries no
// usage and no duration, so video_output_milliseconds stays unknown; both
// exchanges are counted as requests only.
const (
	kindPredictLongRunning extendedKind = iota + 100
	kindOperationRead
)

func init() {
	for id, kind := range map[string]extendedKind{
		"gemini.models.batchGenerateContent": kindBatchCreate,
		"gemini.batches.get":                 kindBatchRead,
		"gemini.batches.cancel":              kindBatchRead,
		"gemini.models.predictLongRunning":   kindPredictLongRunning,
		"gemini.operations.get":              kindOperationRead,
	} {
		extendedSpecs[id] = extendedSpec{kind, ProviderGeminiDeveloper, FormatGeminiDeveloper}
	}
}

func geminiExtendedOperations() []providerOp {
	ref := "https://aistudio.google.com/api-reference/batch-api"
	video := "https://aistudio.google.com/api-reference/generate-content"
	op := func(id, method, path string, action Action, kind, source string) providerOp {
		return providerOp{Operation{ID: id, Method: method, Path: path, Transport: HTTP, Action: action, ResourceKind: kind, Stateful: true, SourceURL: source}, FormatGeminiDeveloper, ProtocolGoogleGenAI}
	}
	return []providerOp{
		op("gemini.models.batchGenerateContent", "POST", "/v1beta/models/{model}:batchGenerateContent", Create, providerBatchResource, ref),
		op("gemini.batches.get", "GET", "/v1beta/batches/{id}", Read, providerBatchResource, ref),
		op("gemini.batches.cancel", "POST", "/v1beta/batches/{id}:cancel", Cancel, providerBatchResource, ref),
		op("gemini.models.predictLongRunning", "POST", "/v1beta/models/{model}:predictLongRunning", Create, "prediction-operation", video),
		op("gemini.operations.get", "GET", "/v1beta/models/{model}/operations/{id}", Read, "prediction-operation", video),
	}
}

// batchObjectID returns the provider id of a batch (or batch-create) object.
func batchObjectID(provider string, data []byte) (string, map[string]json.RawMessage, error) {
	if provider != ProviderGeminiDeveloper {
		return objectID(data)
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(data, &object) != nil || object == nil {
		return "", nil, fmt.Errorf("invalid batch response")
	}
	var name string
	_ = json.Unmarshal(object["name"], &name)
	id := strings.TrimPrefix(name, "batches/")
	if !strings.HasPrefix(name, "batches/") || !validSegment(id) {
		return "", nil, fmt.Errorf("batch response omitted batches/{id}")
	}
	return id, object, nil
}

// operationID returns the operation id of models/{model}/operations/{id}.
func operationID(data []byte, model string) (string, error) {
	var object struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(data, &object) != nil {
		return "", fmt.Errorf("invalid operation response")
	}
	prefix := "models/" + strings.TrimPrefix(model, "models/") + "/operations/"
	id := strings.TrimPrefix(object.Name, prefix)
	if !strings.HasPrefix(object.Name, prefix) || !validSegment(id) {
		return "", fmt.Errorf("operation response omitted its name")
	}
	return id, nil
}

// applyGeminiBatchObject folds a Developer API batch resource into the
// record, aggregating its inline responses when the batch is terminal.
func applyGeminiBatchObject(object map[string]json.RawMessage, record *BatchRecord) {
	var state string
	_ = json.Unmarshal(object["state"], &state)
	record.Status = state
	switch state {
	case "BATCH_STATE_SUCCEEDED", "BATCH_STATE_FAILED", "BATCH_STATE_CANCELLED", "BATCH_STATE_EXPIRED":
	default:
		return
	}
	record.Ended = true
	var stats map[string]json.RawMessage
	if json.Unmarshal(object["batchStats"], &stats) == nil {
		// int64 fields are JSON strings in proto3 JSON; accept both forms.
		raw := bytes.Trim(stats["requestCount"], `"`)
		if n, err := strconv.ParseInt(string(raw), 10, 64); err == nil && n >= 0 {
			record.Expected = n
		}
	}
	var output struct {
		Inlined *struct {
			Items []struct {
				Metadata struct {
					Key string `json:"key"`
				} `json:"metadata"`
				Response json.RawMessage `json:"response"`
				Error    json.RawMessage `json:"error"`
			} `json:"inlinedResponses"`
		} `json:"inlinedResponses"`
	}
	if json.Unmarshal(object["output"], &output) != nil || output.Inlined == nil {
		return // responsesFile (unconfirmed line wrapper) or no output: no part
	}
	var lines []batchLine
	for i, item := range output.Inlined.Items {
		id := item.Metadata.Key
		if id == "" {
			id = "#" + strconv.Itoa(i)
		}
		switch {
		case item.Response != nil && item.Error == nil:
			counters, model, err := nativeBodyCounters(ProviderGeminiDeveloper, item.Response)
			if err != nil {
				return
			}
			lines = append(lines, batchLine{id: id, succeeded: true, counters: counters, model: model})
		case item.Error != nil && item.Response == nil:
			lines = append(lines, batchLine{id: id})
		default:
			return
		}
	}
	seen := map[string]bool{}
	for _, line := range lines {
		if seen[line.id] {
			return
		}
		seen[line.id] = true
	}
	if record.Parts == nil {
		record.Parts = map[string]BatchPart{}
	}
	record.Parts[extendedResultsPartResults] = aggregateBatchLines(lines)
}

// geminiBatchModels checks every model named in a batch-create body against
// the surface allowlist and the path model.
func (g *Gateway) geminiBatchModels(doc json.RawMessage, pathModel string) error {
	var root any
	if json.Unmarshal(doc, &root) != nil {
		return badRequest("invalid native JSON")
	}
	var visit func(any) error
	visit = func(value any) error {
		switch v := value.(type) {
		case map[string]any:
			for key, child := range v {
				if strings.EqualFold(key, "model") {
					model, ok := child.(string)
					if key != "model" || !ok || !g.allowedModel(strings.TrimPrefix(model, "models/")) || strings.TrimPrefix(model, "models/") != pathModel {
						return forbidden("every batch request model must be the allowed path model")
					}
				}
				if err := visit(child); err != nil {
					return err
				}
			}
		case []any:
			for _, child := range v {
				if err := visit(child); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return visit(root)
}
