package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/pkg/clientplex"
	"github.com/vamsiramakrishnan/aiplex/pkg/modelapi"
)

func FuzzBugbashVertexMessagesTranslation(f *testing.F) {
	for _, body := range []string{
		`{"model":"model","messages":[{"role":"user","content":"hello"}],"max_tokens":3}`,
		`{"model":"model","messages":[],"stream":true,"max_tokens":3,"native_extension":{"value":1}}`,
		`{"model":"model","model":"changed","max_tokens":3}`,
		`{"model":"model","messages":[{"role":"user","role":"assistant"}]}`,
		`null`, `[]`, `{"model":"model","stream":"true"}`,
	} {
		f.Add(body)
	}
	f.Fuzz(func(t *testing.T, body string) {
		if len(body) > 8192 {
			t.Skip()
		}
		surface := modelapi.Surface{Protocol: clientplex.Messages, APIVersion: "v1", Project: "project", Location: "global", Publisher: "anthropic"}
		surface.Operations, _ = modelapi.SelectOperations("models.rawPredict", "models.streamRawPredict")
		r := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
		path, translated, err := clientNativePath(surface, r, "/v1/messages")
		if err != nil {
			return
		}
		if !json.Valid(translated) || modelapi.ValidateJSON([]byte(body)) != nil {
			t.Fatal("accepted translation hid malformed or duplicate native input")
		}
		var original, actual map[string]any
		decode := func(body []byte, target any) error {
			decoder := json.NewDecoder(strings.NewReader(string(body)))
			decoder.UseNumber()
			return decoder.Decode(target)
		}
		if decode([]byte(body), &original) != nil || decode(translated, &actual) != nil {
			t.Fatal("accepted translation was not JSON")
		}
		model, ok := original["model"].(string)
		if !ok {
			t.Fatal("missing native model")
		}
		method := "rawPredict"
		if original["stream"] == true {
			method = "streamRawPredict"
		}
		if path != "/v1/projects/project/locations/global/publishers/anthropic/models/"+model+":"+method {
			t.Fatal("translation escaped pinned target", path)
		}
		delete(original, "model")
		original["anthropic_version"] = "vertex-2023-10-16"
		if !reflect.DeepEqual(original, actual) {
			t.Fatal("translation changed unrelated native fields")
		}
	})
}
