package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// quotaStoreDump renders every quota record of the tenant, kind by kind, as
// indented JSON. Ports of the fake tool server are replaced, so the dump is
// stable across runs.
func quotaStoreDump(t *testing.T, store quotaplex.Store) []byte {
	t.Helper()
	kinds := []string{"attempt", "budget", "rate", "concurrency", "ledger", "usage", "terminal-evidence", "terminal-work", "terminal-attempt", "terminal-pending", "attempt-recovery", "quota-binding-quarantine", "quota-host-config", "policy", "policy-head", "lineage", "price-book"}
	out := map[string][]quotaplex.Record{}
	err := store.View(context.Background(), "tenant", func(tx quotaplex.Tx) error {
		for _, kind := range kinds {
			records, err := tx.Scan(kind, "", 50)
			if err != nil {
				return err
			}
			if len(records) > 0 {
				out[kind] = records
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return regexp.MustCompile(`127\.0\.0\.1:[0-9]+`).ReplaceAll(raw, []byte("127.0.0.1:PORT"))
}

// A tool binding that declares no response-size or duration meter must admit,
// settle and store exactly what it did before those meters existed. The
// golden file was written from the code before the change
// (AIPLEX_WRITE_TOOL_GOLDEN=1 regenerates it; do not regenerate it to make a
// change pass).
func TestToolCallOptOutStoredStateIsUnchanged(t *testing.T) {
	// The tool server is served in process at a fixed endpoint, so the
	// configuration digests (which pin the endpoint) are stable.
	t.Setenv("COHESION_HOST_CREDENTIAL", "operator-tool-token")
	h, quota, request := protectedToolFixture(t, "http://127.0.0.1:9/mcp")
	inProcess := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		w := httptest.NewRecorder()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, managedToolResponse)
		return w.Result(), nil
	})}
	h.Runtime.HTTP, h.Runtime.Streaming = inProcess, inProcess
	w := httptest.NewRecorder()
	h.NativeTool(w, request("golden"))
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	// A second call is refused by the one-call policy budget: the refusal's
	// stored shape is part of the golden too.
	w = httptest.NewRecorder()
	h.NativeTool(w, request("golden-refused"))
	if w.Code != 429 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	got := quotaStoreDump(t, quota.store)
	const golden = "testdata/tool_call_opt_out_store.golden.json"
	if os.Getenv("AIPLEX_WRITE_TOOL_GOLDEN") == "1" {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		gotLines, wantLines := bytes.Split(got, []byte("\n")), bytes.Split(want, []byte("\n"))
		for i := 0; i < len(gotLines) || i < len(wantLines); i++ {
			var g, w []byte
			if i < len(gotLines) {
				g = gotLines[i]
			}
			if i < len(wantLines) {
				w = wantLines[i]
			}
			if !bytes.Equal(g, w) {
				t.Fatalf("the stored quota state of a binding without the new meters changed at line %d:\n got  %s\n want %s", i+1, g, w)
			}
		}
	}
}
