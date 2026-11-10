package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/cliconfig"
)

// The "aiplex dev keeps state in memory" hint is only true, and only useful,
// when this CLI wrote something to a local API that has since restarted. So the
// CLI remembers when it last wrote successfully to each loopback API (by URL),
// and a not-found error mentions the restart only when the running aiplex dev
// started after that write. Nothing else is recorded: no paths, bodies or
// credentials.

const lastWriteFile = "last-write.json"

// writeRecorder notes successful mutating requests to a loopback API.
type writeRecorder struct {
	base http.RoundTripper
	url  string
}

func (w *writeRecorder) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := w.base.RoundTrip(r)
	if err == nil && resp.StatusCode/100 == 2 {
		switch r.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
			noteLocalWrite(w.url, time.Now())
		}
	}
	return resp, err
}

func lastWritePath() string {
	dir, err := cliconfig.Dir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, lastWriteFile)
}

func readLastWrites() map[string]string {
	out := map[string]string{}
	if p := lastWritePath(); p != "" {
		if raw, err := os.ReadFile(p); err == nil {
			_ = json.Unmarshal(raw, &out)
		}
	}
	return out
}

func noteLocalWrite(url string, at time.Time) {
	p := lastWritePath()
	if p == "" {
		return
	}
	writes := readLastWrites()
	writes[strings.TrimRight(url, "/")] = at.UTC().Format(time.RFC3339Nano)
	if len(writes) > 16 { // loopback URLs differ by port; keep the file small
		writes = map[string]string{strings.TrimRight(url, "/"): at.UTC().Format(time.RFC3339Nano)}
	}
	if raw, err := json.Marshal(writes); err == nil {
		_ = os.WriteFile(p, raw, 0o600)
	}
}

// devRestartedSinceLastWrite reports whether apiURL is the API of the running
// `aiplex dev` and that dev started after this CLI last wrote to apiURL, which
// is when "anything created before it restarted is gone" is the likely reason
// for a not-found.
func devRestartedSinceLastWrite(apiURL string) bool {
	st, _, err := readDevState()
	if err != nil || st == nil || strings.TrimRight(st.URL, "/") != strings.TrimRight(apiURL, "/") {
		return false
	}
	started, err := time.Parse(time.RFC3339, st.StartedAt)
	if err != nil {
		return false
	}
	raw, ok := readLastWrites()[strings.TrimRight(apiURL, "/")]
	if !ok {
		return false
	}
	wrote, err := time.Parse(time.RFC3339Nano, raw)
	return err == nil && started.After(wrote)
}
