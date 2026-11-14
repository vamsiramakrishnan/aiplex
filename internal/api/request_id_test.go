package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

func idOf(h http.Handler, inbound string) string {
	r := httptest.NewRequest("GET", "/ap/x/v1/models", nil)
	if inbound != "" {
		r.Header.Set("X-Request-Id", inbound)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w.Header().Get("X-Aiplex-Request-Id")
}

// A generated id is unique across processes and restarts: two instances (two
// processes) writing to one ledger do not both answer for their first request
// with the same id.
func TestGeneratedRequestIDsAreUniqueAcrossProcesses(t *testing.T) {
	ok := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	a, b := requestIDMiddleware("00112233aabbccdd")(ok), requestIDMiddleware("ffeeddcc99887766")(ok)
	first, other := idOf(a, ""), idOf(b, "")
	if first == other || first != "req-00112233aabbccdd-1" || idOf(a, "") != "req-00112233aabbccdd-2" {
		t.Fatalf("%q %q", first, other)
	}
	real := idOf(RequestID(ok), "")
	if !requestIDPattern.MatchString(real) || !usageledger.IsServerRequestID(real) || real == idOf(RequestID(ok), "") {
		t.Fatalf("process id %q", real)
	}
	if real == idOf(RequestID(ok), "") {
		t.Fatal("repeated")
	}
}

// A caller cannot pass off its own X-Request-Id as a server-generated one, and
// an id of the generated form is never taken from a caller.
func TestCallerRequestIDsAndTheGeneratedForm(t *testing.T) {
	ok := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	h := requestIDMiddleware("00112233aabbccdd")(ok)
	if got := idOf(h, "req-abc-1"); got != "req-abc-1" || usageledger.IsServerRequestID(got) {
		t.Fatalf("a caller's own id is kept and is a correlation key: %q", got)
	}
	for _, forged := range []string{"req-5", "req-00112233aabbccdd-9", "req-ffffffffffffffff-1"} {
		if got := idOf(h, forged); got == forged || !usageledger.IsServerRequestID(got) {
			t.Fatalf("%q passed off as generated: %q", forged, got)
		}
	}
}
