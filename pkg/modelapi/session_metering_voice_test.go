package modelapi

import (
	"strings"
	"testing"
)

// Live voice is billed by session seconds: token metering is refused at save
// time; session metering stays available.
func TestTokenMeteringRejectedForLiveVoiceSurfaces(t *testing.T) {
	ops, err := SelectProviderOperations(ProviderOpenAI, openAILiveOperation)
	if err != nil {
		t.Fatal(err)
	}
	s := Surface{ID: "voice", Protocol: ProtocolOpenAILive, Origin: "https://api.openai.com", Models: []string{"m"}, Operations: ops, Provider: &Provider{ID: ProviderOpenAI}}
	s.WebSocket = &WebSocketSessions{Metering: SessionMeteringToken}
	if err := s.Validate(); err == nil || !strings.Contains(err.Error(), "session seconds") {
		t.Fatal("token metering accepted for a Live voice surface", err)
	}
	s.WebSocket = &WebSocketSessions{Metering: SessionMeteringSession, MaxSessionSeconds: 600}
	if err := s.Validate(); err != nil {
		t.Fatal("session metering refused for Live voice", err)
	}
}
