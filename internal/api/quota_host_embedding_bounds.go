package api

import (
	"encoding/json"
	"fmt"
)

// quotaEmbeddingRequest bounds an embedding request to inline text inputs.
// Input tokens are bounded by the attested per-request input ceiling, never
// estimated from bytes; there is no output.
func quotaEmbeddingRequest(operation string, body []byte) error {
	object, err := quotaJSONObject(body)
	if err != nil {
		return err
	}
	texts := func(raw json.RawMessage) bool {
		if quotaTextValue(raw) {
			return true
		}
		var values []string
		return json.Unmarshal(raw, &values) == nil && len(values) > 0 && len(values) <= 2048
	}
	geminiContent := func(raw json.RawMessage) bool { return quotaTextParts(raw, false) == nil }
	switch operation {
	case "openai.v1.embeddings", "azure.v1.embeddings", "azure.deployments.embeddings":
		if !quotaOnlyFields(object, "model", "input", "dimensions", "encoding_format", "user") || !texts(object["input"]) {
			return fmt.Errorf("inline text embedding input required")
		}
	case "gemini.models.embedContent", "models.embedContent":
		if !quotaOnlyFields(object, "model", "content", "taskType", "title", "outputDimensionality", "autoTruncate") || !geminiContent(object["content"]) {
			return fmt.Errorf("inline text content required")
		}
	case "gemini.models.batchEmbedContents":
		var requests []map[string]json.RawMessage
		if !quotaOnlyFields(object, "requests") || json.Unmarshal(object["requests"], &requests) != nil || len(requests) == 0 || len(requests) > 1024 {
			return fmt.Errorf("bounded embedding requests required")
		}
		for _, request := range requests {
			if !quotaOnlyFields(request, "model", "content", "taskType", "title", "outputDimensionality") || !geminiContent(request["content"]) {
				return fmt.Errorf("inline text content required")
			}
		}
	case "models.predict", "endpoints.predict":
		var instances []map[string]json.RawMessage
		if !quotaOnlyFields(object, "instances", "parameters") || json.Unmarshal(object["instances"], &instances) != nil || len(instances) == 0 || len(instances) > 1024 {
			return fmt.Errorf("bounded embedding instances required")
		}
		for _, instance := range instances {
			if !quotaOnlyFields(instance, "content", "task_type", "title") || !quotaTextValue(instance["content"]) {
				return fmt.Errorf("inline text instances required")
			}
		}
	default:
		return fmt.Errorf("unsupported embedding operation")
	}
	return nil
}
