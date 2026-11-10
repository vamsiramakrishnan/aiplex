package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/pkg/modelapi"
)

// Access-point save errors name the offending field in details.field while
// the message stays the human-readable sentence it always was.
func TestAccessPointSaveErrorsCarryFieldPath(t *testing.T) {
	f := newAPFixture(t)
	mediaOps, err := modelapi.SelectProviderOperations(modelapi.ProviderOpenAI, "openai.v1.images-generations")
	if err != nil {
		t.Fatal(err)
	}
	media := modelapi.Surface{ID: "media", Protocol: modelapi.ProtocolOpenAIImages, Origin: f.upstream.URL, Models: []string{"img-model"}, Operations: mediaOps, Provider: &modelapi.Provider{ID: modelapi.ProviderOpenAI}}
	f.put("model-surface", media.ID, ModelConnection{Surface: media, IdentityRef: "host"})
	cases := []struct {
		name    string
		mutate  func(*AccessPoint)
		field   string // "" means no details.field
		message string
	}{
		{"unknown surface", func(a *AccessPoint) { a.Models[1].SurfaceRef = "missing" }, "models/1/surface_ref", `model surface "missing" does not resolve in this tenant`},
		{"bad native model", func(a *AccessPoint) { a.Models[0].NativeModel = "a..b" }, "models/0/native_model", "models entries need name, surface_ref and native_model"},
		{"bad model name", func(a *AccessPoint) { a.Models[2].Name = "has space" }, "models/2/name", "models entries need name, surface_ref and native_model"},
		{"missing surface ref", func(a *AccessPoint) { a.Models[3].SurfaceRef = "" }, "models/3/surface_ref", "models entries need name, surface_ref and native_model"},
		{"duplicate model name", func(a *AccessPoint) { a.Models[1].Name = a.Models[0].Name }, "models/1/name", "is not unique"},
		{"unlisted native model", func(a *AccessPoint) { a.Models[0].NativeModel = "not-listed" }, "models/0/native_model", "native model"},
		{"alias on verbatim surface", func(a *AccessPoint) {
			a.Models[3] = AccessPointModel{Name: "friendly", SurfaceRef: "media", NativeModel: "img-model"}
		}, "models/3/name", "so name must equal native_model"},
		{"duplicate tool", func(a *AccessPoint) { a.Tools[1].ConnectionRef = "github" }, "tools/1/connection_ref", "is listed twice"},
		{"unknown tool", func(a *AccessPoint) { a.Tools[2].ConnectionRef = "nope" }, "tools/2/connection_ref", `tool connection "nope" does not resolve in this tenant`},
		{"bad expose", func(a *AccessPoint) { a.Tools[1].Expose = []string{""} }, "tools/1/expose", "expose"},
		{"caller kind", func(a *AccessPoint) { a.CallerKinds = []string{CallerUser, "robot"} }, "caller_kinds/1", "caller_kinds entries must be unique"},
		{"no caller kinds", func(a *AccessPoint) { a.CallerKinds = nil }, "caller_kinds", "caller_kinds requires"},
		{"qos scope", func(a *AccessPoint) {
			a.QoS = &AccessPointQoS{Class: "gold", Scope: "tenant", Limits: &AccessPointLimit{Concurrency: 1}}
		}, "qos/scope", "qos.scope must be principal or access_point"},
		{"qos class", func(a *AccessPoint) { a.QoS = &AccessPointQoS{Class: "Gold!"} }, "qos/class", "qos.class must match"},
		{"qos limits", func(a *AccessPoint) { a.QoS = &AccessPointQoS{Class: "gold", Limits: &AccessPointLimit{}} }, "qos/limits", "at least one limit"},
		{"nothing exposed", func(a *AccessPoint) { a.Models, a.Tools = nil, nil }, "", "exposes at least one"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			spec := apSpec()
			c.mutate(&spec)
			raw, _ := json.Marshal(spec)
			w := f.do("PUT", "/api/v1/access-points/ide", string(raw))
			if w.Code != 400 {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
			var body struct {
				Code    string         `json:"code"`
				Message string         `json:"message"`
				Details map[string]any `json:"details"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Code != "INVALID_SPEC" || !strings.Contains(body.Message, c.message) {
				t.Fatalf("message changed: %+v", body)
			}
			got, has := body.Details["field"]
			if c.field == "" && has || c.field != "" && got != c.field {
				t.Fatalf("details.field = %v (present %t), want %q; message %q", got, has, c.field, body.Message)
			}
		})
	}
}
