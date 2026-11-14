package main

import (
	"encoding/json"
	"io"
	"strings"
	"testing"
)

const skillsInvocationsBody = `[{"id":"inv-1","agent_id":"agent-a","instance_id":"","skill_name":"summarize","status":"ok","started_at":"2026-11-01T10:00:00Z","reported_at":"2026-11-01T10:00:01Z"}]`

func TestSkillsInvocationsLimitFlag(t *testing.T) {
	var calls []spCall
	spServer(t, &calls, map[string]string{"GET /api/v1/skills/invocations": skillsInvocationsBody})

	out, err := runSP(t, "", skillsCmd(), "invocations", "--limit", "5", "--agent", "agent-a")
	if err != nil {
		t.Fatal(out, err)
	}
	if len(calls) != 1 || calls[0].query != "agent_id=agent-a&limit=5" {
		t.Fatalf("calls %+v", calls)
	}
	if !strings.Contains(out, "inv-1") || !strings.Contains(out, "1 invocation(s)") {
		t.Fatalf("table:\n%s", out)
	}

	calls = nil
	if _, err := runSP(t, "", skillsCmd(), "invocations"); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0].query != "limit=100" {
		t.Fatalf("the default limit is the server's 100: %+v", calls)
	}
}

func TestSkillsInvocationsLimitValidation(t *testing.T) {
	for _, arg := range []string{"--limit=0", "--limit=-3", "--limit=1001", "--limit=many"} {
		var calls []spCall
		spServer(t, &calls, map[string]string{"GET /api/v1/skills/invocations": skillsInvocationsBody})
		_, err := runSP(t, "", skillsCmd(), "invocations", arg)
		if err == nil {
			t.Errorf("%s must be refused", arg)
			continue
		}
		if code := reportError(io.Discard, skillsCmd(), nil, err); code != 2 {
			t.Errorf("%s: exit code %d, want 2 (%v)", arg, code, err)
		}
		if len(calls) != 0 {
			t.Errorf("%s must not call the API: %+v", arg, calls)
		}
	}
}

func TestSkillsInvocationsStructuredOutput(t *testing.T) {
	var calls []spCall
	spServer(t, &calls, map[string]string{"GET /api/v1/skills/invocations": skillsInvocationsBody})

	output = "json"
	out, err := runSP(t, "", skillsCmd(), "invocations", "--limit", "1")
	var got []map[string]any
	if err != nil || json.Unmarshal([]byte(out), &got) != nil || len(got) != 1 || got[0]["skill_name"] != "summarize" {
		t.Fatalf("json: %v\n%s", err, out)
	}

	output = "yaml"
	out, err = runSP(t, "", skillsCmd(), "invocations", "--limit", "1")
	if err != nil || !strings.Contains(out, "skill_name: summarize") {
		t.Fatalf("yaml: %v\n%s", err, out)
	}
}
