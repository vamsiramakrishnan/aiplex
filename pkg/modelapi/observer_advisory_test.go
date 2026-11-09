package modelapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A legacy observer must not change what an uncontrolled gateway delivers:
// a malformed usage object leaves usage unobserved instead of failing the
// response that the gateway would deliver without an observer.
func TestLegacyObserverIsAdvisoryForMalformedUsage(t *testing.T) {
	const body = `{"candidates":[{"index":0}],"usageMetadata":{"promptTokenCount":"three"}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	defer server.Close()
	path := "/v1/projects/test-project/locations/global/publishers/google/models/model-a:generateContent"
	plain, _ := NewGateway(surface(t, server.URL, "models.generateContent"), options(t, server.URL, nil))
	without := serve(t, plain, "POST", path, `{}`, "")
	opts := options(t, server.URL, nil)
	observed := 0
	opts.Observe = func(context.Context, Owner, UsageObservation) { observed++ }
	observing, _ := NewGateway(surface(t, server.URL, "models.generateContent"), opts)
	with := serve(t, observing, "POST", path, `{}`, "")
	if without.Code != 200 || with.Code != without.Code || with.Body.String() != without.Body.String() || observed != 0 {
		t.Fatalf("observer changed the response: without=%d with=%d observed=%d", without.Code, with.Code, observed)
	}
}
