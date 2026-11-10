//go:build e2e

package e2e

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// 8. Concurrency and robustness. Run with E2E_RACE=1 to build the API with
// the race detector; the harness fails the test on any race report or panic.
func TestConcurrencyAndRobustness(t *testing.T) {
	e := modelEnv(t)
	healthy := func(what string) {
		t.Helper()
		if r := e.do("GET", "/readyz", ""); r.Status != 200 {
			t.Fatalf("server unhealthy after %s: %d %s", what, r.Status, r.Body)
		}
		if r := e.post("/ap/models/v1/chat/completions", `{"model":"gpt-e2e","messages":[{"role":"user","content":"still there?"}]}`); r.Status != 200 {
			t.Fatalf("model request after %s: %d %s", what, r.Status, r.Body)
		}
	}

	t.Run("50 parallel streams", func(t *testing.T) {
		var wg sync.WaitGroup
		errs := make(chan error, 50)
		client := &http.Client{Timeout: 60 * time.Second}
		for i := 0; i < 50; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				body := fmt.Sprintf(`{"model":"compat-e2e","stream":true,"messages":[{"role":"user","content":"stream %d"}]}`, i)
				req, _ := http.NewRequest("POST", e.stack.APIURL+"/ap/models/v1/chat/completions", strings.NewReader(body))
				req.Header.Set("Authorization", "Bearer "+e.stack.Token)
				req.Header.Set("Content-Type", "application/json")
				resp, err := client.Do(req)
				if err != nil {
					errs <- err
					return
				}
				defer resp.Body.Close()
				raw, _ := io.ReadAll(resp.Body)
				data := sseData(string(raw))
				if resp.StatusCode != 200 || len(data) == 0 || data[len(data)-1] != "[DONE]" || !strings.Contains(string(raw), `"usage"`) {
					errs <- fmt.Errorf("stream %d: %d %s", i, resp.StatusCode, raw)
				}
			}(i)
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Error(err)
		}
		eventually(t, 10*time.Second, func() error {
			r := e.cli("usage", "records", "--model", "compat-e2e", "--limit", "200", "-o", "json")
			var page struct {
				Records []struct {
					Status    string `json:"status"`
					Streaming bool   `json:"streaming"`
				} `json:"records"`
			}
			if err := json.Unmarshal([]byte(r.Out), &page); err != nil {
				return fmt.Errorf("records: %v %s", err, r)
			}
			ok := 0
			for _, rec := range page.Records {
				if rec.Status == "ok" && rec.Streaming {
					ok++
				}
			}
			if ok < 50 {
				return fmt.Errorf("ledger has %d ok streaming records, want 50", ok)
			}
			return nil
		})
		healthy("parallel streams")
	})

	t.Run("client disconnects mid-stream", func(t *testing.T) {
		req, _ := http.NewRequest("POST", e.stack.APIURL+"/ap/models/v1/chat/completions", strings.NewReader(`{"model":"gpt-slow","stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"slow"}]}`))
		req.Header.Set("Authorization", "Bearer "+e.stack.Token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		reader := bufio.NewReader(resp.Body)
		if _, err := reader.ReadString('\n'); err != nil {
			t.Fatalf("first chunk: %v", err)
		}
		resp.Body.Close() // disconnect while the upstream is still streaming
		eventually(t, 10*time.Second, func() error {
			r := e.cli("usage", "records", "--model", "gpt-slow", "-o", "json")
			if !strings.Contains(r.Out, `"client_disconnected"`) && !strings.Contains(r.Out, `"stream_aborted"`) {
				return fmt.Errorf("no disconnected record yet: %s", r.Out)
			}
			return nil
		})
		healthy("client disconnect")
	})

	t.Run("30 MB body", func(t *testing.T) {
		big := bytes.Repeat([]byte("a"), 30<<20)
		body := `{"model":"gpt-e2e","messages":[{"role":"user","content":"` + string(big) + `"}]}`
		r := e.post("/ap/models/v1/chat/completions", body)
		if r.Status != 413 {
			t.Fatalf("30 MB body: %d %.300s", r.Status, r.Body)
		}
		if _, ok := r.JSON(t)["error"].(map[string]any); !ok {
			t.Fatalf("413 not in the OpenAI shape: %.300s", r.Body)
		}
		r = e.post("/api/v1/llm/surfaces/huge", body, "If-Match", "0")
		if r.Status != 413 && r.Status != 405 {
			t.Fatalf("30 MB body on the control plane: %d %.300s", r.Status, r.Body)
		}
		healthy("30 MB body")
	})

	t.Run("malformed JSON", func(t *testing.T) {
		for _, c := range []struct{ path, shape string }{
			{"/ap/models/v1/chat/completions", `"error":{`},
			{"/ap/models/v1/messages", `"type":"error"`},
			{"/ap/models/v1beta/models/gemini-e2e:generateContent", `"status"`},
		} {
			r := e.post(c.path, `{"model": "gpt-e2e", "messages": [`, "anthropic-version", "2023-06-01")
			if r.Status != 400 {
				t.Fatalf("%s malformed: %d %s", c.path, r.Status, r.Body)
			}
			contains(t, r.Body, c.shape, c.path+" error shape")
		}
		r := e.do("PUT", "/api/v1/access-points/broken", `{"id":`, append(e.auth(), "If-Match", "0", "Content-Type", "application/json")...)
		if r.Status != 400 {
			t.Fatalf("malformed control-plane JSON: %d %s", r.Status, r.Body)
		}
		healthy("malformed JSON")
	})

	t.Run("upstream hangs", func(t *testing.T) {
		start := time.Now()
		r := e.post("/ap/models/v1/chat/completions", `{"model":"gpt-hang","messages":[{"role":"user","content":"x"}]}`)
		took := time.Since(start)
		if r.Status < 500 || took > 20*time.Second {
			t.Fatalf("hanging upstream: %d after %s: %s", r.Status, took, r.Body)
		}
		if _, ok := r.JSON(t)["error"].(map[string]any); !ok {
			t.Fatalf("timeout not in the OpenAI shape: %s", r.Body)
		}
		healthy("hanging upstream")
	})
}
