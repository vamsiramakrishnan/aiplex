package main

import (
	"encoding/json"
	"strings"
	"testing"
)

const getHistoryBody = `[
{"id":"h-1","instance_id":"i-1","action":"deploy","plane":"mcplex","owner":"team-a","performed_by":"alice","timestamp":"2026-11-01T10:00:00Z","success":true},
{"id":"h-2","instance_id":"i-1","action":"undeploy","plane":"mcplex","owner":"team-a","timestamp":"2026-11-02T10:00:00Z","success":false,"error":"boom"}
]`

func TestGetInstanceHistory(t *testing.T) {
	var calls []spCall
	spServer(t, &calls, map[string]string{
		"GET /api/v1/instances/i-1/history": getHistoryBody,
		"GET /api/v1/instances/i-1":         `{"id":"i-1","plane":"mcplex","status":"running"}`,
	})

	out, err := runSP(t, "", getCmd(), "instance", "i-1", "--history")
	if err != nil {
		t.Fatal(out, err)
	}
	if len(calls) != 1 || calls[0].path != "/api/v1/instances/i-1/history" {
		t.Fatalf("calls %+v", calls)
	}
	for _, want := range []string{"deploy", "undeploy", "alice", "boom", "2 history event(s)"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	// The second event has no performed_by: that is unknown, not blank.
	var undeployLine string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "undeploy") {
			undeployLine = l
		}
	}
	if !strings.Contains(undeployLine, "unknown") {
		t.Errorf("missing performed_by must show unknown: %q", undeployLine)
	}
}

func TestGetInstanceWithoutHistoryAsksForDetails(t *testing.T) {
	var calls []spCall
	spServer(t, &calls, map[string]string{
		"GET /api/v1/instances/i-1": `{"id":"i-1","plane":"mcplex","status":"running"}`,
	})
	if _, err := runSP(t, "", getCmd(), "instance", "i-1"); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0].path != "/api/v1/instances/i-1" {
		t.Fatalf("without --history only the instance is read: %+v", calls)
	}
}

func TestGetInstanceHistoryEmpty(t *testing.T) {
	var calls []spCall
	spServer(t, &calls, map[string]string{"GET /api/v1/instances/i-2/history": `[]`})
	out, err := runSP(t, "", getCmd(), "instance", "i-2", "--history")
	if err != nil || !strings.Contains(out, "No history recorded") {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestGetInstanceHistoryStructuredOutput(t *testing.T) {
	var calls []spCall
	spServer(t, &calls, map[string]string{"GET /api/v1/instances/i-1/history": getHistoryBody})

	output = "json"
	out, err := runSP(t, "", getCmd(), "instance", "i-1", "--history")
	var got []map[string]any
	if err != nil || json.Unmarshal([]byte(out), &got) != nil || len(got) != 2 || got[1]["action"] != "undeploy" {
		t.Fatalf("json: %v\n%s", err, out)
	}

	output = "yaml"
	out, err = runSP(t, "", getCmd(), "instance", "i-1", "--history")
	if err != nil || !strings.Contains(out, "action: undeploy") {
		t.Fatalf("yaml: %v\n%s", err, out)
	}
}
