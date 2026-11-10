package devstack

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func mockPost(t *testing.T, path, body string) string {
	t.Helper()
	srv := httptest.NewServer(&MockModels{})
	defer srv.Close()
	resp, err := http.Post(srv.URL+path, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("%s: %d %s", path, resp.StatusCode, raw)
	}
	return string(raw)
}

// The Responses mock answers JSON and SSE with usage on the terminal event
// and is deterministic.
func TestMockResponses(t *testing.T) {
	out := mockPost(t, "/v1/responses", `{"model":"mock-responses","input":"hi there"}`)
	for _, want := range []string{`"object":"response"`, `"status":"completed"`, `"input_tokens":2`, `"output_tokens":`, "You said: hi there"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %s in %s", want, out)
		}
	}
	stream := mockPost(t, "/v1/responses", `{"model":"mock-responses","stream":true,"input":[{"role":"user","content":[{"type":"input_text","text":"hi there"}]}]}`)
	created := strings.Index(stream, "event: response.created")
	delta := strings.Index(stream, "event: response.output_text.delta")
	completed := strings.Index(stream, "event: response.completed")
	if created < 0 || delta < created || completed < delta {
		t.Fatalf("event order wrong: %s", stream)
	}
	// Only the terminal event carries usage; the created snapshot has null.
	if strings.Count(stream, `"usage":{`) != 1 || !strings.Contains(stream[completed:], `"input_tokens":2`) {
		t.Fatalf("usage must appear once, on response.completed: %s", stream)
	}
	if mockPost(t, "/v1/responses", `{"model":"mock-responses","input":"x"}`) == "" {
		t.Fatal("empty")
	}
}

func TestMockResponsesRejectsMissingInput(t *testing.T) {
	srv := httptest.NewServer(&MockModels{})
	defer srv.Close()
	resp, err := http.Post(srv.URL+"/v1/responses", "application/json", strings.NewReader(`{"model":"m"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("status %d", resp.StatusCode)
	}
}
