package modelapi

// Gemini Developer API (generativelanguage.googleapis.com) provider surface.
//
// Documentation facts, read from Google's current reference (the docs moved
// from ai.google.dev to aistudio.google.com):
//
//   - base URL https://generativelanguage.googleapis.com/v1beta; API key in the
//     x-goog-api-key request header (https://aistudio.google.com/llms-full.txt,
//     Get Started examples). The key is never placed in the URL query: the
//     operator credential is a secret-reference api_key injected in that
//     header, and a caller-supplied key= parameter or x-goog-api-key header is
//     dropped (providerRequestHeaders, utilityQuery).
//   - the model is part of the path: POST /v1beta/models/{model}:generateContent,
//     :streamGenerateContent (?alt=sse for SSE; the JSON-array form otherwise),
//     :countTokens, :embedContent, :batchEmbedContents
//     (https://aistudio.google.com/api-reference/generate-content,
//     .../api-reference/tokens/count-tokens, .../api-reference/embeddings).
//   - cachedContents: POST/GET /v1beta/cachedContents, GET/PATCH/DELETE
//     /v1beta/cachedContents/{id}; names are cachedContents/{id}, model is
//     models/{model}; only the expiration can be updated
//     (https://aistudio.google.com/api-reference/caching).
//   - Interactions: POST /v1beta/interactions (?alt=sse with "stream": true),
//     GET /v1beta/interactions/{id}, POST /v1beta/interactions/{id}/cancel,
//     DELETE /v1beta/interactions/{id}; the Api-Revision header pins the
//     revision (https://aistudio.google.com/api-reference/interactions).
//
// Not documented by the provider's reference: whether GET /interactions/{id} accepts a
// stream/last_event_id resume query on the Developer API (documented for
// Vertex only), the list pagination parameter names of cachedContents.list
// (pageSize/pageToken are the Google API standard and are forwarded), and
// whether batchEmbedContents responses carry usageMetadata (it is read when
// present, never invented).

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
)

const geminiRef = "https://aistudio.google.com/api-reference/"

func geminiDeveloperOps() []utilityOp {
	sseQuery := map[string]func(string) bool{"alt": queryEquals("sse")}
	op := func(id, method, path string, transport Transport, action Action, kind string, stateful bool, ref string, spec utilitySpec) utilityOp {
		return utilityOp{providerOp{Operation{ID: id, Method: method, Path: path, Transport: transport, Action: action, ResourceKind: kind, Stateful: stateful, SourceURL: geminiRef + ref}, FormatGeminiDeveloper, ProtocolGoogleGenAI}, ProviderGeminiDeveloper, spec}
	}
	model := "/v1beta/models/{model}"
	usage := utilitySpec{kind: utilStandard, cumulative: true}
	return []utilityOp{
		op("gemini.models.generateContent", "POST", model+":generateContent", HTTP, Invoke, "", false, "generate-content", usage),
		op("gemini.models.streamGenerateContent", "POST", model+":streamGenerateContent", SSE, Invoke, "", false, "generate-content/stream-generate-content", utilitySpec{kind: utilStandard, cumulative: true, query: sseQuery}),
		op("gemini.models.countTokens", "POST", model+":countTokens", HTTP, Invoke, "", false, "tokens/count-tokens", utilitySpec{kind: utilNoUsage, stateless: true}),
		op("gemini.models.embedContent", "POST", model+":embedContent", HTTP, Invoke, "", false, "embeddings", utilitySpec{kind: utilEmbedding, stateless: true}),
		op("gemini.models.batchEmbedContents", "POST", model+":batchEmbedContents", HTTP, Invoke, "", false, "embeddings", utilitySpec{kind: utilEmbedding, stateless: true}),
		// Cached content: management calls. usageMetadata.totalTokenCount is the
		// size of the cache, repeated on every get/list, and how creation is billed
		// is unconfirmed, so none of these is read for usage.
		op("gemini.cachedContents.create", "POST", "/v1beta/cachedContents", HTTP, Create, "cache", true, "caching", utilitySpec{kind: utilNoUsage, asynchronous: true}),
		op("gemini.cachedContents.list", "GET", "/v1beta/cachedContents", HTTP, List, "cache", true, "caching", utilitySpec{kind: utilNoUsage, stateless: true, noBody: true, asynchronous: true, query: map[string]func(string) bool{"pageSize": queryMatches(digitsQuery), "pageToken": queryMatches(tokenQuery)}}),
		op("gemini.cachedContents.get", "GET", "/v1beta/cachedContents/{id}", HTTP, Read, "cache", true, "caching", utilitySpec{kind: utilNoUsage, noBody: true, asynchronous: true}),
		op("gemini.cachedContents.update", "PATCH", "/v1beta/cachedContents/{id}", HTTP, Update, "cache", true, "caching", utilitySpec{kind: utilNoUsage, asynchronous: true, query: map[string]func(string) bool{"updateMask": queryMatches(maskQuery)}}),
		op("gemini.cachedContents.delete", "DELETE", "/v1beta/cachedContents/{id}", HTTP, Delete, "cache", true, "caching", utilitySpec{kind: utilNoUsage, noBody: true, asynchronous: true}),
		op("gemini.interactions.create", "POST", "/v1beta/interactions", HTTP, Create, "interaction", true, "interactions", utilitySpec{kind: utilStandard, cumulative: true, query: sseQuery}),
		op("gemini.interactions.get", "GET", "/v1beta/interactions/{id}", HTTP, Read, "interaction", true, "interactions", utilitySpec{kind: utilNoUsage, noBody: true, asynchronous: true}),
		op("gemini.interactions.cancel", "POST", "/v1beta/interactions/{id}/cancel", HTTP, Cancel, "interaction", true, "interactions", utilitySpec{kind: utilNoUsage, noBody: true, asynchronous: true}),
		op("gemini.interactions.delete", "DELETE", "/v1beta/interactions/{id}", HTTP, Delete, "interaction", true, "interactions", utilitySpec{kind: utilNoUsage, noBody: true, asynchronous: true}),
	}
}

// geminiModelName strips the optional models/ collection prefix.
func geminiModelName(name string) string { return strings.TrimPrefix(name, "models/") }

// geminiBodyModel checks a body "model" member against the model the path (or
// the allowlist) selected. Returns the bare model name.
func (g *Gateway) geminiBodyModel(raw json.RawMessage, md *Metadata) error {
	var name string
	if json.Unmarshal(raw, &name) != nil || name == "" {
		return forbidden("body model is not allowed")
	}
	bare := geminiModelName(name)
	if !g.allowedModel(bare) {
		return forbidden("body model is not allowed")
	}
	if md.Model != "" && md.Model != bare {
		return forbidden("body model differs from path model")
	}
	md.Model = bare
	return nil
}

// geminiCacheReference checks that a cachedContent reference belongs to the
// caller before the request can reach upstream.
func (g *Gateway) geminiCacheReference(ctx context.Context, owner Owner, raw json.RawMessage, md *Metadata) error {
	if raw == nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil
	}
	var name string
	if json.Unmarshal(raw, &name) != nil {
		return badRequest("cachedContent must be a string")
	}
	id, err := g.providerResourceID("cache", name)
	if err != nil || g.options.Ownership == nil {
		return forbidden("cache ownership required")
	}
	resource := Resource{"cache", id}
	md.References = append(md.References, resource)
	if g.options.Ownership.Check(ctx, owner, resource) != nil {
		return forbidden("cache ownership denied")
	}
	return nil
}

func (g *Gateway) validateGeminiDeveloperBody(ctx context.Context, owner Owner, op Operation, md *Metadata, body []byte) ([]byte, error) {
	doc, err := utilityObject(body, "model", "cachedContent", "previous_interaction_id", "agent", "stream", "requests", "generateContentRequest")
	if err != nil {
		return nil, err
	}
	switch op.ID {
	case "gemini.models.generateContent", "gemini.models.streamGenerateContent":
		if raw := doc["model"]; raw != nil {
			if err = g.geminiBodyModel(raw, md); err != nil {
				return nil, err
			}
		}
		if err = g.geminiCacheReference(ctx, owner, doc["cachedContent"], md); err != nil {
			return nil, err
		}
	case "gemini.models.countTokens":
		// The request is either {contents} or {generateContentRequest}; the
		// nested request carries its own model and cache reference.
		if raw := doc["model"]; raw != nil {
			if err = g.geminiBodyModel(raw, md); err != nil {
				return nil, err
			}
		}
		if raw := doc["generateContentRequest"]; raw != nil {
			var nested map[string]json.RawMessage
			if json.Unmarshal(raw, &nested) != nil || nested == nil {
				return nil, badRequest("generateContentRequest must be an object")
			}
			if nested["model"] != nil {
				if err = g.geminiBodyModel(nested["model"], md); err != nil {
					return nil, err
				}
			}
			if err = g.geminiCacheReference(ctx, owner, nested["cachedContent"], md); err != nil {
				return nil, err
			}
		}
	case "gemini.models.embedContent":
		if raw := doc["model"]; raw != nil {
			if err = g.geminiBodyModel(raw, md); err != nil {
				return nil, err
			}
		}
	case "gemini.models.batchEmbedContents":
		var requests []map[string]json.RawMessage
		if raw := doc["requests"]; raw == nil || json.Unmarshal(raw, &requests) != nil || len(requests) == 0 {
			return nil, badRequest("requests must be a non-empty array")
		}
		for _, request := range requests {
			// Every element names its model; each must be the path model.
			if request["model"] != nil {
				if err = g.geminiBodyModel(request["model"], md); err != nil {
					return nil, err
				}
			}
		}
	case "gemini.cachedContents.create":
		raw := doc["model"]
		if raw == nil {
			return nil, forbidden("model is required")
		}
		if err = g.geminiBodyModel(raw, md); err != nil {
			return nil, err
		}
	case "gemini.cachedContents.update":
		// Only the expiration is updatable; the resource is addressed by the
		// path and its ownership was checked in preflight.
	case "gemini.interactions.create":
		if doc["agent"] != nil {
			return nil, forbidden("agents are not served by this surface")
		}
		raw := doc["model"]
		if raw == nil {
			return nil, forbidden("model is required")
		}
		if err = g.geminiBodyModel(raw, md); err != nil {
			return nil, err
		}
		if raw = doc["stream"]; raw != nil {
			var streaming bool
			if json.Unmarshal(raw, &streaming) != nil {
				return nil, badRequest("stream must be a boolean")
			}
			if streaming {
				md.Transport = SSE
			}
		}
		if raw = doc["previous_interaction_id"]; raw != nil && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			var id string
			if json.Unmarshal(raw, &id) != nil || !validSegment(id) {
				return nil, badRequest("invalid previous interaction")
			}
			resource := Resource{"interaction", id}
			md.References = append(md.References, resource)
			if g.options.Ownership == nil || g.options.Ownership.Check(ctx, owner, resource) != nil {
				return nil, forbidden("previous interaction ownership denied")
			}
		}
	}
	if md.Model == "" && op.ID != "gemini.cachedContents.update" {
		return nil, forbidden("model is required")
	}
	return body, nil
}
