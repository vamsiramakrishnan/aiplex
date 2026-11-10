package api

import (
	"net/http/httptest"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

// Server-tool request counts and the reported service tier are recorded;
// a conflicting tier within one exchange becomes "mixed".
func TestLedgerRecordsServerToolsAndServiceTier(t *testing.T) {
	h := &ConnectedHandler{}
	records := attachLedger(t, h)
	_, _, capture := h.beginUsage(httptest.NewRecorder(), httptest.NewRequest("POST", "/x", nil), "tenant", usageledger.PlaneLLM, "surface")
	capture.observe(map[string]int64{"input_tokens": 5, "output_tokens": 2, "server_tool_web_search_requests": 3, "server_tool_calls": 1, "cache_write_5m_tokens": 1, "cache_write_1h_tokens": 4, "cache_write_tokens": 5, "tool_use_tokens": 9}, true, "", true, "", map[string]string{"service_tier": "flex"})
	capture.observe(nil, true, "", true, "", map[string]string{"service_tier": "priority"})
	capture.finish()
	r := onlyRecord(t, records())
	if r.ServerTools["web_search"] != 3 || len(r.ServerTools) != 1 {
		t.Fatalf("server tools: %+v", r.ServerTools)
	}
	if r.ServiceTier != "mixed" {
		t.Fatalf("tier: %q", r.ServiceTier)
	}
	if r.Usage == nil || *r.Usage.CacheWrite1hTokens != 4 || *r.Usage.ToolUsePromptTokens != 9 {
		t.Fatalf("usage: %+v", r.Usage)
	}
	if r.UsageBound != "" {
		t.Fatalf("bound: %q", r.UsageBound)
	}
}
