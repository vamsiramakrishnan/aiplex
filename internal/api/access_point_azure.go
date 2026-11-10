package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"

	"github.com/go-chi/chi/v5"
	"github.com/vamsiramakrishnan/aiplex/pkg/modelapi"
)

// Azure OpenAI SDK path shapes below /ap/{id}. An Azure OpenAI client pointed
// at an access point sends the paths Azure documents; they are mapped onto the
// access point's existing OpenAI wires, so the model resolves, authorizes,
// meters and routes exactly as it does on /v1/...:
//
//	/ap/{id}/openai/v1/chat/completions                       → openai-chat-completions
//	/ap/{id}/openai/v1/responses[/{rid}[/cancel]]             → openai-responses
//	/ap/{id}/openai/v1/embeddings                             → openai-embeddings
//	/ap/{id}/openai/deployments/{deployment}/chat/completions → openai-chat-completions
//	/ap/{id}/openai/deployments/{deployment}/embeddings       → openai-embeddings
//
// On the deployment shapes the deployment is the access point's model name.
// The target model surface may be any provider that serves the wire (Azure or
// not); the surface's own native path is chosen by accessPointFacadePath.
// The api-version query parameter is accepted and ignored: as on the gateway
// path, an Azure surface's version is pinned by the operator, never taken from
// the caller. The api-key header is accepted as the AIPlex token by
// NativeKeyHeaders.
var azureAPDeployment = regexp.MustCompile(`^/openai/deployments/([A-Za-z0-9._:@-]+)/(chat/completions|embeddings)$`)

// accessPointAzureLocal maps an Azure-shaped access-point path to the wire's
// canonical /v1 path. deployment is non-empty for the deployment shapes and
// azure reports whether the path was Azure-shaped at all. Other paths are
// returned unchanged.
func accessPointAzureLocal(local string) (canonical, deployment string, azure bool) {
	switch local {
	case "/openai/v1/chat/completions", "/openai/v1/responses", "/openai/v1/embeddings":
		return local[len("/openai"):], "", true
	}
	if m := azureAPDeployment.FindStringSubmatch(local); m != nil {
		return "/v1/" + m[2], m[1], true
	}
	return local, "", false
}

// mountAccessPointAzure mounts the Azure-shaped routes below /ap/{id}.
func (h *ConnectedHandler) mountAccessPointAzure(r chi.Router) {
	r.Post("/openai/v1/chat/completions", h.AccessPointModel(WireOpenAIChat))
	r.Post("/openai/v1/responses", h.AccessPointModel(WireOpenAIResponses))
	r.Post("/openai/v1/embeddings", h.AccessPointModel(WireOpenAIEmbeddings))
	r.Post("/openai/deployments/{deployment}/chat/completions", h.AccessPointModel(WireOpenAIChat))
	r.Post("/openai/deployments/{deployment}/embeddings", h.AccessPointModel(WireOpenAIEmbeddings))
	// Model-less response operations resolve like their /v1 counterparts.
	responses := h.AccessPointResource(WireOpenAIResponses)
	r.Get("/openai/v1/responses/{rid}", responses)
	r.Delete("/openai/v1/responses/{rid}", responses)
	r.Post("/openai/v1/responses/{rid}/cancel", responses)
}

// dropAPIVersion removes the api-version query parameter, which is never
// forwarded: the surface pins its own.
func dropAPIVersion(rawQuery string) string {
	query, err := url.ParseQuery(rawQuery)
	if err != nil {
		return rawQuery
	}
	if _, ok := query["api-version"]; !ok {
		return rawQuery
	}
	query.Del("api-version")
	return query.Encode()
}

// deploymentBodyModel checks a deployment-route body: it must be one JSON
// object, and a "model" in it must name the deployment. present reports
// whether the body carried a model at all (the Azure SDK sends it; raw REST
// clients need not).
func deploymentBodyModel(data []byte, deployment string) (present bool, err error) {
	if err = modelapi.ValidateJSON(data); err != nil {
		return false, fmt.Errorf("request body must be one unambiguous JSON object")
	}
	var probe struct {
		Model *string `json:"model"`
	}
	if json.Unmarshal(data, &probe) != nil {
		return false, fmt.Errorf("request body must be one JSON object with a string model")
	}
	if probe.Model == nil {
		return false, nil
	}
	if *probe.Model != deployment {
		return true, fmt.Errorf("body model differs from path deployment")
	}
	return true, nil
}

// insertBodyModel adds "model" as the first member of the JSON object body and
// leaves every other byte as sent. The body must already be validated.
func insertBodyModel(data []byte, model string) ([]byte, error) {
	open := bytes.IndexByte(data, '{')
	if open < 0 || len(bytes.TrimSpace(data[:open])) != 0 {
		return nil, fmt.Errorf("request body must be one JSON object")
	}
	encoded, err := json.Marshal(model)
	if err != nil {
		return nil, err
	}
	rest := data[open+1:]
	member := append([]byte(`"model":`), encoded...)
	if next := bytes.TrimLeft(rest, " \t\r\n"); len(next) > 0 && next[0] != '}' {
		member = append(member, ',')
	}
	out := make([]byte, 0, len(data)+len(member))
	out = append(out, data[:open+1]...)
	out = append(out, member...)
	return append(out, rest...), nil
}
