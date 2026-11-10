package modelapi

import (
	"fmt"
	"net/http"
	"testing"
)

// The service tier and server-tool counters reach the execution lease on the
// Gemini Developer API path, not only the JSON inspector.
func TestGeminiTierAndToolsReachTheLease(t *testing.T) {
	up := newProviderUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, fixtureBody(t, "google", "gemini-generate.json"))
	})
	s := geminiDASurface("gemini.models.generateContent")(up.URL)
	lease := &modelLease{}
	g := providerGateway(t, s, headerCredential("X-Goog-Api-Key", ""), lease, nil)
	w := providerServe(g, "POST", "/v1beta/models/model-a:generateContent", `{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`, nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var tier string
	var tools int64
	for _, o := range lease.observations {
		if v := o.Dimensions["service_tier"]; v != "" {
			tier = v
		}
		if n := o.Counters["server_tool_google_search_requests"]; n > 0 {
			tools = n
		}
	}
	if tier != "standard" || tools != 0 {
		t.Fatalf("tier=%q tools=%d observations=%+v", tier, tools, lease.observations)
	}
}
