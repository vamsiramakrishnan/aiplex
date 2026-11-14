package api

import (
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/pkg/modelapi"
)

// Two Content-Type headers are refused: parsers differ on which one wins.
func TestAccessPointMultipartRefusesTwoContentTypeHeaders(t *testing.T) {
	f := multipartAliasFixture(t)
	for _, name := range []string{"asr-friendly", "asr-model"} {
		hidden := "--X\r\nContent-Disposition: form-data; name=\"model\"\r\n\r\nexpensive\r\n--X--\r\n"
		body := mpField("model", name) + mpFile(hidden) + mpEnd
		before := len(f.upstreamCalls())
		r := httptest.NewRequest("POST", "/ap/media/v1/audio/transcriptions", strings.NewReader(body))
		r.Header.Add("Content-Type", mpType)
		r.Header.Add("Content-Type", "multipart/form-data; boundary=X")
		w := httptest.NewRecorder()
		f.router.ServeHTTP(w, r)
		if w.Code != 400 || len(f.upstreamCalls()) != before {
			t.Fatalf("%s: %d %s (upstream calls %d)", name, w.Code, w.Body, len(f.upstreamCalls())-before)
		}
	}
}

// goFormModel reads the model field of a body with the standard library, the
// way the upstream hop does.
func goFormModel(t *testing.T, body, contentType string) []string {
	t.Helper()
	boundary := strings.Trim(strings.SplitN(strings.SplitN(contentType, "boundary=", 2)[1], ";", 2)[0], `"`)
	r := multipart.NewReader(strings.NewReader(body), boundary)
	var values []string
	for {
		p, err := r.NextPart()
		if err == io.EOF {
			return values
		}
		if err != nil {
			t.Fatalf("the forwarded body does not parse: %v", err)
		}
		if p.FormName() == "model" && p.FileName() == "" {
			v, _ := io.ReadAll(p)
			values = append(values, string(v))
		}
	}
}

// pythonFormFields parses a body with python-multipart (what FastAPI and vLLM
// use). It skips the test when Python or the package is not installed.
func pythonFormFields(t *testing.T, body, contentType string) map[string][]string {
	t.Helper()
	script := "import sys,json\nfrom python_multipart.multipart import parse_form\nf={}\n" +
		"def on_field(x): f.setdefault(x.field_name.decode(),[]).append((x.value or b'').decode())\n" +
		"def on_file(x): pass\n" +
		"parse_form({'Content-Type': sys.argv[1].encode()}, sys.stdin.buffer, on_field, on_file)\nprint(json.dumps(f))\n"
	cmd := exec.Command("python3", "-I", "-c", script, contentType)
	cmd.Stdin = strings.NewReader(body)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && strings.Contains(string(ee.Stderr), "ModuleNotFoundError") {
			t.Skip("python-multipart is not installed")
		}
		if _, lookErr := exec.LookPath("python3"); lookErr != nil {
			t.Skip("python3 is not installed")
		}
		t.Fatalf("python-multipart: %v %s", err, err.(*exec.ExitError).Stderr)
	}
	fields := map[string][]string{}
	if err := json.Unmarshal(out, &fields); err != nil {
		t.Fatal(err)
	}
	return fields
}

// Bodies two parsers read differently are refused for an alias and for a
// native name alike, and nothing is forwarded.
func TestAccessPointMultipartRefusesParserDifferentials(t *testing.T) {
	f := multipartAliasFixture(t)
	file := mpFile("RIFF")
	for _, name := range []string{"asr-friendly", "asr-model"} {
		x := "--X\r\nContent-Disposition: form-data; name=\"model\"\r\n\r\nexpensive\r\n--X--\r\n"
		y := "--Y\r\nContent-Disposition: form-data; name=\"model\"\r\n\r\n" + name + "\r\n--Y\r\nContent-Disposition: form-data; name=\"file\"; filename=\"a\"\r\n\r\nx\r\n--Y--\r\n"
		cases := map[string]struct{ body, contentType string }{
			"A: boundary and boundary*":      {x + y, `multipart/form-data; boundary="X"; boundary*=UTF-8''Y`},
			"B: name* on a later part":       {mpField("model", name) + file + "--" + mpBoundary + "\r\nContent-Disposition: form-data; name=\"model\"; name*=UTF-8''x\r\n\r\nexpensive\r\n" + mpEnd, mpType},
			"preamble":                       {"ignored preamble\r\n" + mpField("model", name) + file + mpEnd, mpType},
			"epilogue":                       {mpField("model", name) + file + mpEnd + "epilogue\r\n", mpType},
			"epilogue holding a second form": {mpField("model", name) + file + mpEnd + mpField("model", "expensive") + mpEnd, mpType},
			"name* on a file part":           {mpField("model", name) + "--" + mpBoundary + "\r\nContent-Disposition: form-data; name=\"file\"; filename=\"a\"; name*=UTF-8''model\r\n\r\nexpensive\r\n" + mpEnd, mpType},
			"filename* without a filename":   {mpField("model", name) + "--" + mpBoundary + "\r\nContent-Disposition: form-data; name=\"file\"; filename*=UTF-8''a.wav\r\n\r\nx\r\n" + mpEnd, mpType},
			"extra Content-Type parameter":   {mpField("model", name) + file + mpEnd, mpType + "; charset=utf-8"},
		}
		before := len(f.upstreamCalls())
		for label, c := range cases {
			w := f.do("POST", "/ap/media/v1/audio/transcriptions", c.body, "Content-Type", c.contentType)
			if w.Code != 400 {
				t.Errorf("%s (%s): %d %s", label, name, w.Code, w.Body)
			}
		}
		if len(f.upstreamCalls()) != before {
			t.Fatalf("a refused upload reached the upstream (%s)", name)
		}
	}
}

// For every body the access point forwards, the standard library and
// python-multipart read the model the gateway metered, and only that.
func TestAccessPointMultipartForwardedBodiesAreReadIdentically(t *testing.T) {
	f := multipartAliasFixture(t)
	records := attachLedger(t, f.h)
	utf8Name := "--" + mpBoundary + "\r\nContent-Disposition: form-data; name=\"file\"; filename=\"café 音.wav\"; filename*=UTF-8''caf%C3%A9.wav\r\nContent-Type: audio/wav\r\n\r\nRIFF\r\n"
	bodies := map[string]string{
		"model first":          mpField("model", "%s") + mpFile(mpAudio()) + mpField("language", "en") + mpEnd,
		"model last":           mpFile(mpAudio()) + mpField("model", "%s") + mpEnd,
		"non-ASCII file name":  utf8Name + mpField("model", "%s") + mpEnd,
		"single trailing CRLF": mpField("model", "%s") + mpFile("x") + mpEnd + "\r\n",
		"field named like it":  mpField("model_hint", "expensive") + mpField("model", "%s") + mpFile("x") + mpEnd,
		"file named model.wav": "--" + mpBoundary + "\r\nContent-Disposition: form-data; name=\"file\"; filename=\"model\"\r\n\r\nexpensive\r\n" + mpField("model", "%s") + mpEnd,
	}
	for label, template := range bodies {
		for _, name := range []string{"asr-friendly", "asr-model"} {
			body := strings.Replace(template, "%s", name, 1)
			before := len(f.upstreamCalls())
			if w := f.do("POST", "/ap/media/v1/audio/transcriptions", body, "Content-Type", mpType); w.Code != 200 {
				t.Fatalf("%s (%s): %d %s", label, name, w.Code, w.Body)
			}
			calls := f.upstreamCalls()
			if len(calls) != before+1 {
				t.Fatalf("%s: upstream calls %d", label, len(calls)-before)
			}
			forwarded := calls[len(calls)-1].Body
			if got := goFormModel(t, forwarded, mpType); len(got) != 1 || got[0] != "asr-model" {
				t.Errorf("%s (%s): Go reads %v from the forwarded body", label, name, got)
			}
			t.Run("python "+label+" "+name, func(t *testing.T) {
				if got := pythonFormFields(t, forwarded, mpType)["model"]; len(got) != 1 || got[0] != "asr-model" {
					t.Errorf("python-multipart reads %v", got)
				}
			})
		}
	}
	for _, r := range records() {
		if r.ModelID != "asr-model" {
			t.Fatalf("metered model %q", r.ModelID)
		}
	}
}

// Allowing aliases on images and audio surfaces also allows them on JSON
// bodies: speech and image generation are rewritten and recorded as native.
func TestAccessPointAliasRewritesJSONOnAudioAndImagesSurfaces(t *testing.T) {
	f := newAPFixture(t)
	speechOps, err := modelapi.SelectProviderOperations(modelapi.ProviderOpenAI, "openai.v1.audio-speech", "openai.v1.audio-transcriptions")
	if err != nil {
		t.Fatal(err)
	}
	imageOps, err := modelapi.SelectProviderOperations(modelapi.ProviderOpenAI, "openai.v1.images-generations")
	if err != nil {
		t.Fatal(err)
	}
	f.put("model-surface", "voice", ModelConnection{Surface: modelapi.Surface{ID: "voice", Protocol: modelapi.ProtocolOpenAIAudio, Origin: f.upstream.URL, Models: []string{"tts-native"}, Operations: speechOps, Provider: &modelapi.Provider{ID: modelapi.ProviderOpenAI}}, IdentityRef: "host"})
	f.put("model-surface", "pics", ModelConnection{Surface: modelapi.Surface{ID: "pics", Protocol: modelapi.ProtocolOpenAIImages, Origin: f.upstream.URL, Models: []string{"img-native"}, Operations: imageOps, Provider: &modelapi.Provider{ID: modelapi.ProviderOpenAI}}, IdentityRef: "host"})
	spec := apSpec()
	spec.ID = "json"
	spec.Models = append(spec.Models,
		AccessPointModel{Name: "tts-friendly", SurfaceRef: "voice", NativeModel: "tts-native"},
		AccessPointModel{Name: "img-friendly", SurfaceRef: "pics", NativeModel: "img-native"})
	if err = f.h.validateAccessPointModels(context.Background(), "tenant", spec.Models); err != nil {
		t.Fatal(err)
	}
	f.put(AccessPointKind, "json", spec)
	scopes := append(append([]string(nil), apAllScopes...), "llm:surface:voice:openai.v1.audio-speech", "llm:surface:pics:openai.v1.images-generations", "llm:model:tts-native", "llm:model:img-native")
	f.as(apUser("alice", "platform-eng"), scopes...)
	records := attachLedger(t, f.h)
	for _, c := range []struct{ path, body, want, native string }{
		{"/ap/json/v1/audio/speech", `{"model":"tts-friendly","input":"hi","voice":"alloy"}`, `{"model":"tts-native","input":"hi","voice":"alloy"}`, "tts-native"},
		{"/ap/json/v1/images/generations", `{"prompt":"a cat","model":"img-friendly","n":1}`, `{"prompt":"a cat","model":"img-native","n":1}`, "img-native"},
	} {
		w := f.do("POST", c.path, c.body, "Content-Type", "application/json")
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", c.path, w.Code, w.Body)
		}
		if got := f.lastUpstream().Body; got != c.want {
			t.Fatalf("%s upstream body\n got %s\nwant %s", c.path, got, c.want)
		}
	}
	got := records()
	if len(got) != 2 {
		t.Fatalf("%d ledger records", len(got))
	}
	seen := map[string]bool{}
	for _, r := range got {
		seen[r.ModelID] = true
	}
	if !seen["tts-native"] || !seen["img-native"] || len(seen) != 2 {
		t.Fatalf("ledger models: %v", seen)
	}
}
