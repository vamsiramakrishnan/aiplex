//go:build e2e

package e2e

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/internal/devstack"
)

// The dev stack's mock provider serves no multipart operation, so this test
// puts a small recording upstream behind an audio surface and sends a real
// transcription upload through the real aiplex-api binary, with a model alias.
func TestMultipartModelAliasThroughAccessPoint(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	var lengths []int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies, lengths = append(bodies, string(body)), append(lengths, r.ContentLength)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"text":"hello","usage":{"type":"duration","seconds":2}}`)
	}))
	defer upstream.Close()
	e := start(t, withProviderSecrets, func(o *devstack.Options) { o.ExtraOrigins = append(o.ExtraOrigins, upstream.URL) })
	surface := `surface:
  id: asr
  protocol: openai-audio
  origin: ` + upstream.URL + `
  models: [asr-native]
  operations: [openai.v1.audio-transcriptions]
  provider: {id: openai}
auth: {type: bearer, secret_ref: openai-key}
`
	contains(t, e.mustCLI("llm", "surfaces", "apply", "-f", writeFile(t, "asr.yaml", surface)).Out, "Applied model surface asr", "surface apply")
	ap := "id: speech\ndisplay_name: Speech\ncaller_kinds: [user, agent, client]\nmodels:\n  - {name: asr-friendly, surface_ref: asr, native_model: asr-native}\n"
	e.mustCLI("access-points", "apply", "-f", writeFile(t, "speech.yaml", ap))

	const boundary = "e2eBoundary0123456789"
	field := func(name, value string) string {
		return "--" + boundary + "\r\nContent-Disposition: form-data; name=\"" + name + "\"\r\n\r\n" + value + "\r\n"
	}
	file := "--" + boundary + "\r\nContent-Disposition: form-data; name=\"file\"; filename=\"a.wav\"\r\nContent-Type: audio/wav\r\n\r\nRIFF\x00\x01\r\n--" + boundary + "-x\r\n\r\n"
	contentType := "multipart/form-data; boundary=" + boundary
	for name, parts := range map[string][2]string{
		"model first": {field("model", "asr-friendly") + file + field("language", "en") + "--" + boundary + "--\r\n", field("model", "asr-native") + file + field("language", "en") + "--" + boundary + "--\r\n"},
		"model last":  {file + field("model", "asr-friendly") + "--" + boundary + "--\r\n", file + field("model", "asr-native") + "--" + boundary + "--\r\n"},
	} {
		t.Run(name, func(t *testing.T) {
			r := e.post("/ap/speech/v1/audio/transcriptions", parts[0], "Content-Type", contentType)
			if r.Status != 200 {
				t.Fatalf("%d %s", r.Status, r.Body)
			}
			notContains(t, r.Body, "friendly", "alias in the response")
			mu.Lock()
			defer mu.Unlock()
			if got := bodies[len(bodies)-1]; got != parts[1] {
				t.Fatalf("upstream body\n got %q\nwant %q", got, parts[1])
			}
			if lengths[len(lengths)-1] != int64(len(parts[1])) {
				t.Fatalf("Content-Length %d, body %d", lengths[len(lengths)-1], len(parts[1]))
			}
		})
	}
	t.Run("two model fields are refused", func(t *testing.T) {
		mu.Lock()
		before := len(bodies)
		mu.Unlock()
		r := e.post("/ap/speech/v1/audio/transcriptions", field("model", "asr-friendly")+file+field("model", "asr-friendly")+"--"+boundary+"--\r\n", "Content-Type", contentType)
		if r.Status != 400 && r.Status != 403 {
			t.Fatalf("%d %s", r.Status, r.Body)
		}
		mu.Lock()
		defer mu.Unlock()
		if len(bodies) != before {
			t.Fatal("a refused upload reached the upstream")
		}
	})
}
