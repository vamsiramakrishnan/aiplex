package api

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"strings"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/pkg/modelapi"
)

// Media wires: the model is the JSON body's or the multipart form's model
// field; the request reaches the surface's native path unchanged, with
// OpenAI-shaped errors and access-point ledger dimensions.
func TestAccessPointMediaWiresRouteImagesAndAudio(t *testing.T) {
	f := newAPFixture(t)
	ops, err := modelapi.SelectProviderOperations(modelapi.ProviderOpenAI, "openai.v1.images-generations", "openai.v1.audio-transcriptions")
	if err != nil {
		t.Fatal(err)
	}
	surface := modelapi.Surface{ID: "media", Protocol: modelapi.ProtocolOpenAIImages, Origin: f.upstream.URL, Models: []string{"img-model", "asr-model"}, Operations: ops, Provider: &modelapi.Provider{ID: modelapi.ProviderOpenAI}}
	f.put("model-surface", surface.ID, ModelConnection{Surface: surface, IdentityRef: "host"})
	spec := apSpec()
	spec.ID = "media"
	spec.Models = append(spec.Models, AccessPointModel{Name: "img-model", SurfaceRef: "media", NativeModel: "img-model"}, AccessPointModel{Name: "asr-model", SurfaceRef: "media", NativeModel: "asr-model"})
	if err = f.h.validateAccessPointModels(context.Background(), "tenant", spec.Models); err != nil {
		t.Fatal(err)
	}
	f.put(AccessPointKind, "media", spec)
	scopes := append(append([]string(nil), apAllScopes...), "llm:surface:media:openai.v1.images-generations", "llm:surface:media:openai.v1.audio-transcriptions", "llm:model:img-model", "llm:model:asr-model")
	f.as(apUser("alice", "platform-eng"), scopes...)
	records := attachLedger(t, f.h)

	body := `{"model":"img-model","prompt":"x"}`
	w := f.do("POST", "/ap/media/v1/images/generations", body, "Content-Type", "application/json")
	if w.Code != 200 {
		t.Fatalf("images through access point: %d %s", w.Code, w.Body)
	}
	calls := f.upstreamCalls()
	if last := calls[len(calls)-1]; last.Path != "/v1/images/generations" || last.Body != body || last.Authorization != "Bearer operator-upstream-token" {
		t.Fatalf("upstream call: %+v", last)
	}
	var form bytes.Buffer
	mw := multipart.NewWriter(&form)
	part, _ := mw.CreateFormFile("file", "a.wav")
	_, _ = part.Write([]byte("RIFF-audio"))
	_ = mw.WriteField("model", "asr-model")
	_ = mw.Close()
	w = f.do("POST", "/ap/media/v1/audio/transcriptions", form.String(), "Content-Type", mw.FormDataContentType())
	if w.Code != 200 {
		t.Fatalf("transcription through access point: %d %s", w.Code, w.Body)
	}
	calls = f.upstreamCalls()
	if last := calls[len(calls)-1]; last.Path != "/v1/audio/transcriptions" || last.Body != form.String() {
		t.Fatalf("multipart not relayed unchanged: %+v", last.Path)
	}
	for _, r := range records() {
		if r.AccessPointID != "media" || r.CallerKind == "" || r.Provider != modelapi.ProviderOpenAI || (r.ModelID != "img-model" && r.ModelID != "asr-model") {
			t.Fatalf("ledger dimensions: %+v", r)
		}
	}
	// Unknown model: OpenAI-shaped error from the access point.
	w = f.do("POST", "/ap/media/v1/images/generations", `{"model":"other","prompt":"x"}`, "Content-Type", "application/json")
	var shaped struct {
		Error struct {
			Type string `json:"type"`
		} `json:"error"`
	}
	if w.Code != 404 || json.Unmarshal(w.Body.Bytes(), &shaped) != nil || shaped.Error.Type != "not_found_error" {
		t.Fatalf("native error shape: %d %s", w.Code, w.Body)
	}
	// A caller without the model grant is refused before any upstream call.
	before := len(f.upstreamCalls())
	f.as(apUser("alice", "platform-eng"), append(append([]string(nil), apAllScopes...), "llm:surface:media:openai.v1.images-generations")...)
	w = f.do("POST", "/ap/media/v1/images/generations", body, "Content-Type", "application/json")
	if w.Code != 403 || len(f.upstreamCalls()) != before || !strings.Contains(w.Body.String(), "permission_error") {
		t.Fatalf("ungranted model: %d %s", w.Code, w.Body)
	}
}
