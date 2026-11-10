package api

import (
	"testing"

	"github.com/vamsiramakrishnan/aiplex/pkg/modelapi"
)

// A provider surface serves only the media wires whose operations it
// installs: an OpenAI surface with chat or responses alone must not advertise
// image or audio endpoints that every request would be refused on.
func TestAccessPointMediaWiresRequireInstalledOperations(t *testing.T) {
	provider := modelapi.Provider{ID: modelapi.ProviderOpenAI}
	for _, tc := range []struct {
		name     string
		protocol string
		ops      []string
	}{
		{"chat only", modelapi.ProtocolOpenAIChat, []string{"openai.v1.chat-completions"}},
		{"responses only", modelapi.ProtocolOpenAIResponses, []string{"openai.v1.responses"}},
	} {
		ops, err := modelapi.SelectProviderOperations(provider.ID, tc.ops...)
		if err != nil {
			t.Fatal(err)
		}
		surface := modelapi.Surface{ID: "s", Protocol: tc.protocol, Origin: "https://api.openai.com", Models: []string{"m"}, Operations: ops, Provider: &provider}
		served := accessPointServedWires(surface, "m")
		for _, wire := range served {
			if wire == WireOpenAIImages || wire == WireOpenAIAudio {
				t.Fatalf("%s: %s advertised though the surface installs %v", tc.name, wire, tc.ops)
			}
		}
		if len(served) == 0 {
			t.Fatalf("%s: surface serves no wire", tc.name)
		}
	}
}
