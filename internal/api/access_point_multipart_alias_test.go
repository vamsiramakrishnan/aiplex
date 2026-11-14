package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/pkg/modelapi"
)

// multipartAliasFixture is an access point that shows clients "asr-friendly"
// and "img-friendly" for the native "asr-model" and "img-model", next to
// entries that expose the same native models under their own names.
func multipartAliasFixture(t *testing.T) *apFixture {
	t.Helper()
	f := newAPFixture(t)
	ops, err := modelapi.SelectProviderOperations(modelapi.ProviderOpenAI, "openai.v1.audio-transcriptions", "openai.v1.audio-translations", "openai.v1.images-edits")
	if err != nil {
		t.Fatal(err)
	}
	surface := modelapi.Surface{ID: "media", Protocol: modelapi.ProtocolOpenAIAudio, Origin: f.upstream.URL, Models: []string{"asr-model", "img-model"}, Operations: ops, Provider: &modelapi.Provider{ID: modelapi.ProviderOpenAI}}
	f.put("model-surface", surface.ID, ModelConnection{Surface: surface, IdentityRef: "host"})
	spec := apSpec()
	spec.ID = "media"
	spec.Models = append(spec.Models,
		AccessPointModel{Name: "asr-friendly", SurfaceRef: "media", NativeModel: "asr-model"},
		AccessPointModel{Name: "asr-model", SurfaceRef: "media", NativeModel: "asr-model"},
		AccessPointModel{Name: "img-friendly", SurfaceRef: "media", NativeModel: "img-model"},
		AccessPointModel{Name: "img-model", SurfaceRef: "media", NativeModel: "img-model"})
	if err = f.h.validateAccessPointModels(context.Background(), "tenant", spec.Models); err != nil {
		t.Fatalf("an alias on a media surface is refused when the access point is saved: %v", err)
	}
	f.put(AccessPointKind, "media", spec)
	scopes := append(append([]string(nil), apAllScopes...), "llm:surface:media:openai.v1.audio-transcriptions", "llm:surface:media:openai.v1.audio-translations", "llm:surface:media:openai.v1.images-edits", "llm:model:asr-model", "llm:model:img-model")
	f.as(apUser("alice", "platform-eng"), scopes...)
	return f
}

const mpBoundary = "AliasBoundary7MA4YWxkTrZu0gW"

func mpField(name, value string) string {
	return "--" + mpBoundary + "\r\nContent-Disposition: form-data; name=\"" + name + "\"\r\n\r\n" + value + "\r\n"
}

func mpFile(content string) string {
	return "--" + mpBoundary + "\r\nContent-Disposition: form-data; name=\"file\"; filename=\"clip é.wav\"\r\nContent-Type: audio/wav\r\n\r\n" + content + "\r\n"
}

const mpEnd = "--" + mpBoundary + "--\r\n"
const mpType = "multipart/form-data; boundary=" + mpBoundary

// mpAudio is binary content that contains every byte value and boundary
// look-alikes.
func mpAudio() string {
	var b strings.Builder
	for i := 0; i < 256; i++ {
		b.WriteByte(byte(i))
	}
	b.WriteString("\r\n--" + mpBoundary + "-nearly\r\n--other\r\n")
	return b.String()
}

func TestAccessPointAliasRewritesMultipartModelAndNothingElse(t *testing.T) {
	f := multipartAliasFixture(t)
	for _, c := range []struct{ name, path, alias, native, body string }{
		{"audio, model first", "/ap/media/v1/audio/transcriptions", "asr-friendly", "asr-model", mpField("model", "%s") + mpFile(mpAudio()) + mpField("language", "en") + mpEnd},
		{"audio, model after the file", "/ap/media/v1/audio/transcriptions", "asr-friendly", "asr-model", mpFile(mpAudio()) + mpField("temperature", "0.2") + mpField("model", "%s") + mpEnd},
		{"translation", "/ap/media/v1/audio/translations", "asr-friendly", "asr-model", mpFile(mpAudio()) + mpField("model", "%s") + mpEnd},
		{"image edit", "/ap/media/v1/images/edits", "img-friendly", "img-model", mpField("prompt", "add a hat") + mpField("model", "%s") + mpFile("PNG") + mpField("n", "2") + mpEnd},
	} {
		t.Run(c.name, func(t *testing.T) {
			sent := strings.Replace(c.body, "%s", c.alias, 1)
			want := strings.Replace(c.body, "%s", c.native, 1)
			w := f.do("POST", c.path, sent, "Content-Type", mpType)
			if w.Code != 200 {
				t.Fatalf("%d %s", w.Code, w.Body)
			}
			got := f.lastUpstream()
			if got.Body != want {
				t.Fatalf("upstream body\n got %q\nwant %q", got.Body, want)
			}
			if got.ContentType != mpType {
				t.Fatalf("content type changed: %q", got.ContentType)
			}
			if got.ContentLength != int64(len(want)) {
				t.Fatalf("Content-Length %d, body is %d bytes", got.ContentLength, len(want))
			}
			if strings.Contains(w.Body.String(), "friendly") {
				t.Fatalf("alias leaked into the response: %s", w.Body)
			}
			// The native name needs no rewrite and is relayed as it came.
			w = f.do("POST", c.path, want, "Content-Type", mpType)
			if w.Code != 200 || f.lastUpstream().Body != want {
				t.Fatalf("native name: %d", w.Code)
			}
		})
	}
}

func TestAccessPointAliasRefusesAmbiguousMultipartBodies(t *testing.T) {
	f := multipartAliasFixture(t)
	file := mpFile("RIFF")
	cases := map[string]struct{ body, contentType string }{
		"two model fields, both friendly":  {mpField("model", "asr-friendly") + file + mpField("model", "asr-friendly") + mpEnd, mpType},
		"friendly then native":             {mpField("model", "asr-friendly") + file + mpField("model", "asr-model") + mpEnd, mpType},
		"native then friendly":             {mpField("model", "asr-model") + file + mpField("model", "asr-friendly") + mpEnd, mpType},
		"model sent as a file":             {"--" + mpBoundary + "\r\nContent-Disposition: form-data; name=\"model\"; filename=\"m\"\r\n\r\nasr-friendly\r\n" + file + mpEnd, mpType},
		"model with a content type":        {"--" + mpBoundary + "\r\nContent-Disposition: form-data; name=\"model\"\r\nContent-Type: application/json\r\n\r\nasr-friendly\r\n" + file + mpEnd, mpType},
		"differently cased model field":    {mpField("Model", "asr-friendly") + mpField("model", "asr-friendly") + file + mpEnd, mpType},
		"no model":                         {file + mpEnd, mpType},
		"no closing delimiter":             {mpField("model", "asr-friendly") + file, mpType},
		"truncated":                        {mpField("model", "asr-friendly") + file[:60], mpType},
		"no boundary":                      {mpField("model", "asr-friendly") + file + mpEnd, "multipart/form-data"},
		"boundary does not match the body": {mpField("model", "asr-friendly") + file + mpEnd, "multipart/form-data; boundary=elsewhere"},
	}
	before := len(f.upstreamCalls())
	for name, c := range cases {
		w := f.do("POST", "/ap/media/v1/audio/transcriptions", c.body, "Content-Type", c.contentType)
		if w.Code != 400 && w.Code != 403 {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
	}
	if len(f.upstreamCalls()) != before {
		t.Fatal("a refused multipart body reached the upstream")
	}
}

// The upload is metered the same whichever name the client used: the
// delegated request carries the native model, so quota binding, grants,
// pricing and the ledger see one thing.
func TestAccessPointAliasMultipartIsMeteredLikeTheNativeName(t *testing.T) {
	f := multipartAliasFixture(t)
	records := attachLedger(t, f.h)
	upload := func(model string) string {
		return mpField("model", model) + mpFile(mpAudio()) + mpField("language", "en") + mpEnd
	}
	if w := f.do("POST", "/ap/media/v1/audio/transcriptions", upload("asr-model"), "Content-Type", mpType); w.Code != 200 {
		t.Fatalf("native: %d %s", w.Code, w.Body)
	}
	if w := f.do("POST", "/ap/media/v1/audio/transcriptions", upload("asr-friendly"), "Content-Type", mpType); w.Code != 200 {
		t.Fatalf("alias: %d %s", w.Code, w.Body)
	}
	got := records()
	if len(got) != 2 {
		t.Fatalf("%d ledger records, want 2", len(got))
	}
	view := func(i int) string {
		r := got[i]
		raw, _ := json.Marshal(map[string]any{"provider": r.Provider, "resource": r.ResourceID, "operation": r.Operation, "model": r.ModelID, "version": r.ModelVersion,
			"status": r.Status, "http": r.HTTPStatus, "error": r.ErrorClass, "usage": r.Usage, "cost": r.Cost, "bound": r.UsageBound, "ap": r.AccessPointID, "kind": r.CallerKind})
		return string(raw)
	}
	if view(0) != view(1) {
		t.Fatalf("metering differs\n native %s\n  alias %s", view(0), view(1))
	}
	if !strings.Contains(view(0), `"model":"asr-model"`) {
		t.Fatalf("ledger model is not the native model: %s", view(0))
	}
	calls := f.upstreamCalls()
	if len(calls) != 2 || calls[0].Body != calls[1].Body || calls[0].ContentLength != calls[1].ContentLength {
		t.Fatal("the upstream received different bytes for the native and the alias request")
	}
}

// The access point reads at most 8 MiB. A larger upload is refused whichever
// name it uses and wherever the model field sits; nothing is forwarded.
func TestAccessPointAliasMultipartKeepsTheBodyLimit(t *testing.T) {
	f := multipartAliasFixture(t)
	big := strings.Repeat("x", 8<<20)
	before := len(f.upstreamCalls())
	for _, body := range []string{mpField("model", "asr-friendly") + mpFile(big) + mpEnd, mpFile(big) + mpField("model", "asr-friendly") + mpEnd} {
		if w := f.do("POST", "/ap/media/v1/audio/transcriptions", body, "Content-Type", mpType); w.Code != 413 {
			t.Fatalf("oversized upload: %d", w.Code)
		}
	}
	if len(f.upstreamCalls()) != before {
		t.Fatal("an oversized upload reached the upstream")
	}
	// Just under the limit, with the model after the file: handled.
	body := mpFile(strings.Repeat("x", 7<<20)) + mpField("model", "asr-friendly") + mpEnd
	if w := f.do("POST", "/ap/media/v1/audio/transcriptions", body, "Content-Type", mpType); w.Code != 200 {
		t.Fatalf("large upload with the model last: %d %s", w.Code, w.Body)
	}
	if got := f.lastUpstream(); !strings.Contains(got.Body, "asr-model") || strings.Contains(got.Body, "asr-friendly") || got.ContentLength != int64(len(body))-int64(len("asr-friendly")-len("asr-model")) {
		t.Fatalf("large upload not rewritten: length %d", got.ContentLength)
	}
}
