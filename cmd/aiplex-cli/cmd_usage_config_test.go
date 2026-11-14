package main

import (
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func TestUsageConfigTable(t *testing.T) {
	var calls []spCall
	spServer(t, &calls, map[string]string{"GET /api/v1/usage/config": `{"trace_url_template":"https://traces.example/{trace_id}","feedback":true,"feedback_comments":false,"tag_keys_allowed":["env","team"]}`})

	out, err := runSP(t, "", usageCmd(), "config")
	if err != nil {
		t.Fatal(out, err)
	}
	if len(calls) != 1 || calls[0].path != "/api/v1/usage/config" {
		t.Fatalf("calls %+v", calls)
	}
	for _, want := range []string{"https://traces.example/{trace_id}", "Feedback", "yes", "Feedback comments", "no", "env, team"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

// An explicit empty value is a value the API returned: not set, no, no, none.
func TestUsageConfigExplicitEmptyValues(t *testing.T) {
	var calls []spCall
	spServer(t, &calls, map[string]string{"GET /api/v1/usage/config": `{"trace_url_template":"","feedback":false,"feedback_comments":false,"tag_keys_allowed":[]}`})

	out, err := runSP(t, "", usageCmd(), "config")
	if err != nil {
		t.Fatal(out, err)
	}
	for _, want := range []string{"not set", "none"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	noRows := 0
	for _, l := range strings.Split(out, "\n") {
		if f := strings.Fields(l); len(f) > 0 && f[0] == "Feedback" && f[len(f)-1] == "no" {
			noRows++
		}
	}
	if noRows != 2 {
		t.Errorf("want two \"no\" feedback rows, got %d:\n%s", noRows, out)
	}
	if strings.Contains(out, "unknown") {
		t.Errorf("values the API returned must not read as unknown:\n%s", out)
	}
}

// A response without the fields is not a value: each one reads as unknown.
func TestUsageConfigAbsentFieldsAreUnknown(t *testing.T) {
	var calls []spCall
	spServer(t, &calls, map[string]string{"GET /api/v1/usage/config": `{}`})

	out, err := runSP(t, "", usageCmd(), "config")
	if err != nil {
		t.Fatal(out, err)
	}
	if got := strings.Count(out, "unknown"); got != 4 {
		t.Errorf("want four unknown values, got %d:\n%s", got, out)
	}
	if strings.Contains(out, "not set") || strings.Contains(out, "none") {
		t.Errorf("absent fields must not read as set or empty:\n%s", out)
	}
}

func TestUsageConfigRefusesArguments(t *testing.T) {
	var calls []spCall
	spServer(t, &calls, map[string]string{"GET /api/v1/usage/config": `{}`})
	_, err := runSP(t, "", usageCmd(), "config", "extra")
	if err == nil {
		t.Fatal("extra argument must be refused")
	}
	if code := reportError(io.Discard, usageCmd(), nil, err); code != 2 {
		t.Errorf("exit code %d, want 2 (%v)", code, err)
	}
	if len(calls) != 0 {
		t.Errorf("a refused call must not reach the API: %+v", calls)
	}
}

func TestUsageConfigStructuredOutput(t *testing.T) {
	var calls []spCall
	spServer(t, &calls, map[string]string{"GET /api/v1/usage/config": `{"trace_url_template":"","feedback":true,"feedback_comments":true,"tag_keys_allowed":["env"]}`})

	output = "json"
	out, err := runSP(t, "", usageCmd(), "config")
	var got map[string]any
	if err != nil || json.Unmarshal([]byte(out), &got) != nil || got["feedback"] != true || got["feedback_comments"] != true {
		t.Fatalf("json: %v\n%s", err, out)
	}

	output = "yaml"
	out, err = runSP(t, "", usageCmd(), "config")
	if err != nil || !strings.Contains(out, "feedback: true") || !strings.Contains(out, "tag_keys_allowed:") {
		t.Fatalf("yaml: %v\n%s", err, out)
	}
}
