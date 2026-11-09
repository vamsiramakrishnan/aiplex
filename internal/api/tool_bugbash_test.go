package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBugbashToolRequestShapeCannotAcknowledgeResponse(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "JSON", true: "SSE"}[stream], func(t *testing.T) {
			const ambiguous = `{"jsonrpc":"2.0","id":"rpc","method":"tools/call","params":{"name":"other"},"result":{"content":[]}}`
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if stream {
					w.Header().Set("Content-Type", "text/event-stream")
					io.WriteString(w, "data: "+ambiguous+"\n\n")
				} else {
					io.WriteString(w, ambiguous)
				}
			}))
			defer upstream.Close()
			t.Setenv("COHESION_HOST_CREDENTIAL", "operator-token")
			h, quota, request := protectedToolFixture(t, upstream.URL+"/mcp")
			if stream {
				managedToolStreamMode(t, h)
			}
			w := httptest.NewRecorder()
			h.NativeTool(w, request("ambiguous-response"))
			info, err := quota.Engines()["tenant"].GetAttempt(context.Background(), "tenant", w.Header().Get("X-Aiplex-Attempt-ID"))
			if err != nil || w.Code != 502 || info.Decision.State != "unknown" {
				t.Fatal("server request was accepted as a completed tool response", w.Code, info, err)
			}
			balance, err := quota.Engines()["tenant"].BudgetBalance(context.Background(), "tenant", info.Plan.Budgets[0].Key)
			if err != nil || balance.Held != 1 || balance.Consumed != 0 {
				t.Fatal("ambiguous response released exposure", balance, err)
			}
		})
	}
}
