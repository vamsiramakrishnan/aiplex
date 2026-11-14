package modelapi

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// What mainstream clients send is accepted: Go's writer, curl and httpx
// (raw UTF-8 in filename="..."), a quoted boundary, and the RFC 2231 form
// beside a plain filename.
func TestStrictMultipartAcceptsWhatClientsSend(t *testing.T) {
	file := func(disposition string) string {
		return "--" + rwBoundary + "\r\nContent-Disposition: " + disposition + "\r\nContent-Type: audio/wav\r\n\r\nx\r\n"
	}
	cases := map[string]struct{ body, contentType string }{
		"raw UTF-8 filename (curl, httpx, undici)": {file("form-data; name=\"file\"; filename=\"café 音.wav\"") + rwField("model", "m") + rwClose, rwType},
		"percent-encoded quote (httpx)":            {file(`form-data; name="file"; filename="a%22b.wav"`) + rwField("model", "m") + rwClose, rwType},
		"filename* beside a plain filename":        {file(`form-data; name="file"; filename="cafe.wav"; filename*=UTF-8''caf%C3%A9.wav`) + rwField("model", "m") + rwClose, rwType},
		"quoted boundary":                          {rwField("model", "m") + rwClose, `multipart/form-data; boundary="` + rwBoundary + `"`},
		"upper-case media type":                    {rwField("model", "m") + rwClose, `Multipart/Form-Data; boundary=` + rwBoundary},
	}
	for name, c := range cases {
		model, err := MultipartModel(strings.NewReader(c.body), c.contentType)
		if err != nil || model != "m" {
			t.Errorf("%s: %q, %v", name, model, err)
		}
	}
	// Go's own writer.
	var form bytes.Buffer
	w := multipart.NewWriter(&form)
	f, _ := w.CreateFormFile("file", "voice café.mp3")
	_, _ = f.Write([]byte("abc"))
	_ = w.WriteField("model", "m")
	_ = w.Close()
	if model, err := MultipartModel(&form, w.FormDataContentType()); err != nil || model != "m" {
		t.Errorf("multipart.Writer: %q, %v", model, err)
	}
}

func TestGatewayRefusesTwoContentTypeHeaders(t *testing.T) {
	calls := 0
	g, _ := mediaGateway(t, []string{"openai.v1.audio-transcriptions"}, []string{"cheap"}, func(w http.ResponseWriter, r *http.Request) { calls++ }, nil)
	body, contentType := fixtureForm(map[string]string{"model": "cheap"})
	r := httptest.NewRequest("POST", "/v1/audio/transcriptions", strings.NewReader(body))
	r.Header.Add("Content-Type", contentType)
	r.Header.Add("Content-Type", "multipart/form-data; boundary=X")
	w := httptest.NewRecorder()
	g.ServeHTTP(w, r)
	if w.Code != 400 || calls != 0 {
		t.Fatalf("%d calls=%d", w.Code, calls)
	}
}

// The gateway itself refuses the bodies two parsers read differently, so a
// direct call to a surface cannot meter one model and run another.
func TestGatewayRefusesMultipartThatParsersReadDifferently(t *testing.T) {
	calls := 0
	g, _ := mediaGateway(t, []string{"openai.v1.audio-transcriptions"}, []string{"cheap", "expensive"}, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"text":"x"}`)
	}, nil)
	x := "--X\r\nContent-Disposition: form-data; name=\"model\"\r\n\r\nexpensive\r\n--X\r\nContent-Disposition: form-data; name=\"file\"; filename=\"a\"\r\n\r\nx\r\n--X--\r\n"
	scenarioA := x + strings.ReplaceAll(strings.ReplaceAll(x, "expensive", "cheap"), "--X", "--Y")
	scenarioB := rwField("model", "cheap") + "--" + rwBoundary + "\r\nContent-Disposition: form-data; name=\"model\"; name*=UTF-8''x\r\n\r\nexpensive\r\n" + rwFile("file", "a", "audio/wav", "x") + rwClose
	for name, c := range map[string]struct{ body, contentType string }{
		"A: boundary and boundary*": {scenarioA, `multipart/form-data; boundary="X"; boundary*=UTF-8''Y`},
		"B: name*":                  {scenarioB, rwType},
		"preamble":                  {"junk\r\n" + rwField("model", "cheap") + rwFile("file", "a", "audio/wav", "x") + rwClose, rwType},
		"epilogue":                  {rwField("model", "cheap") + rwFile("file", "a", "audio/wav", "x") + rwClose + "more\r\n", rwType},
	} {
		r := httptest.NewRequest("POST", "/v1/audio/transcriptions", strings.NewReader(c.body))
		r.Header.Set("Content-Type", c.contentType)
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
	}
	if calls != 0 {
		t.Fatalf("%d refused uploads reached the provider", calls)
	}
	body, contentType := fixtureForm(map[string]string{"model": "cheap"})
	r := httptest.NewRequest("POST", "/v1/audio/transcriptions", strings.NewReader(body))
	r.Header.Set("Content-Type", contentType)
	w := httptest.NewRecorder()
	g.ServeHTTP(w, r)
	if w.Code != 200 || calls != 1 {
		t.Fatalf("a well-formed upload: %d calls=%d %s", w.Code, calls, w.Body)
	}
}
