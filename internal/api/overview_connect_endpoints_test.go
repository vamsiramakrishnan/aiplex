package api

import "testing"

func TestOverviewEndpointsCoverUtilityAndMediaWires(t *testing.T) {
	wires := map[string]string{WireOpenAIEmbeddings: "e", WireOpenAIModerations: "m", WireOpenAIImages: "i", WireOpenAIAudio: "a"}
	models := []OverviewConnectModel{{Name: "e", Protocols: []string{WireOpenAIEmbeddings}}, {Name: "i", Protocols: []string{WireOpenAIImages, WireOpenAIAudio}}}
	got := overviewEndpoints("https://x.example", "https://x.example/ap/ide", wires, models)
	urls := map[string]bool{}
	for _, e := range got {
		urls[e.URL] = true
		if e.Transport != "http" {
			t.Fatal("unexpected transport", e)
		}
	}
	for _, path := range []string{"/v1/embeddings", "/v1/moderations", "/v1/images/generations", "/v1/images/edits", "/v1/audio/transcriptions", "/v1/audio/translations", "/v1/audio/speech"} {
		if !urls["https://x.example/ap/ide"+path] {
			t.Errorf("missing endpoint %s in %+v", path, got)
		}
	}
	if overviewWireFamily(WireOpenAIEmbeddings) != "openai" || overviewWireFamily(WireOpenAIAudio) != "openai" {
		t.Fatal("utility wires must use the OpenAI base URL")
	}
}
