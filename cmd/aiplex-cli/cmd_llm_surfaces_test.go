package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func runLLMX(t *testing.T, server *httptest.Server, format string, args ...string) (string, error) {
	t.Helper()
	oldURL, oldToken, oldOutput := apiURL, token, output
	apiURL, token, output = server.URL, "session", format
	t.Cleanup(func() { apiURL, token, output = oldURL, oldToken, oldOutput })
	var err error
	out := captureUsageStdout(t, func() {
		root := &cobra.Command{Use: "aiplex", SilenceUsage: true, SilenceErrors: true}
		root.AddCommand(llmCmd(), usageCmd(), connectCmd())
		root.SetArgs(args)
		err = root.Execute()
	})
	return out, err
}

const llmxDescriptor = `{"id":"bedrock","protocols":["bedrock-converse","bedrock-invoke"],"credential_schemes":["aws-sigv4","bearer"],"operations":[{"id":"bedrock.converse","method":"POST","path":"/model/{model}/converse","transport":"http","action":"invoke","source_url":"https://d"},{"id":"bedrock.converse-stream","method":"POST","path":"/model/{model}/converse-stream","transport":"aws-eventstream","action":"invoke","source_url":"https://d"}]}`

func TestLLMProvidersListsDescriptorCatalog(t *testing.T) {
	var asked []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/llm/operations" {
			p := r.URL.Query().Get("provider")
			asked = append(asked, p)
			w.Write([]byte(strings.Replace(llmxDescriptor, `"id":"bedrock"`, `"id":"`+p+`"`, 1)))
			return
		}
		if r.URL.Path == "/api/v1/llm/providers" {
			w.Write([]byte(`[{"provider":"legacy","display_name":"Legacy","enabled":true}]`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	out, err := runLLMX(t, server, "table", "llm", "providers")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(asked, ",") != "openai,anthropic,azure-openai,bedrock,openai-compatible,gemini-developer" {
		t.Fatalf("providers queried: %v", asked)
	}
	for _, want := range []string{"gemini-developer", "bedrock-converse,bedrock-invoke", "aws-sigv4, bearer", "OPERATIONS"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	out, err = runLLMX(t, server, "table", "llm", "providers", "bedrock")
	if err != nil || !strings.Contains(out, "bedrock.converse-stream") || !strings.Contains(out, "aws-eventstream") {
		t.Fatal(out, err)
	}
	out, err = runLLMX(t, server, "table", "llm", "providers", "--configured")
	if err != nil || !strings.Contains(out, "legacy") {
		t.Fatal(out, err)
	}
}

const llmxSurfaceYAML = `
surface:
  id: bedrock-prod
  protocol: bedrock-converse
  origin: https://bedrock-runtime.us-east-1.amazonaws.com
  models: [anthropic.claude-x]
  operations:
    - {id: bedrock.converse, method: POST, path: "/model/{model}/converse", transport: http, action: invoke, source_url: "https://d"}
  provider: {id: bedrock, region: us-east-1, future_provider_field: kept}
  websocket: {metering: session, max_session_seconds: 600}
auth: {type: bearer, secret_ref: bedrock-key}
capacity_policy: {cooldown_enabled: true, max_cooldown_seconds: 30}
future_top_level: kept
`

func TestLLMSurfacesApplyGetListDelete(t *testing.T) {
	var stored json.RawMessage
	var revision uint64
	var puts []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		write := func() {
			w.Write([]byte(`{"tenant_id":"t","kind":"model-surface","id":"bedrock-prod","revision":` + string(rune('0'+revision)) + `,"spec":` + string(stored) + `}`))
		}
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/v1/llm/surfaces/bedrock-prod":
			if stored == nil {
				w.WriteHeader(404)
				w.Write([]byte(`{"error":{"code":"NOT_FOUND","message":"resource not found"}}`))
				return
			}
			write()
		case r.Method == "GET" && r.URL.Path == "/api/v1/llm/surfaces/":
			w.Write([]byte(`[{"tenant_id":"t","kind":"model-surface","id":"bedrock-prod","revision":` + string(rune('0'+revision)) + `,"spec":` + string(stored) + `}]`))
		case r.Method == "PUT":
			puts = append(puts, r.Header.Get("If-Match"))
			if r.Header.Get("If-Match") != string(rune('0'+revision)) && !(revision == 0 && r.Header.Get("If-Match") == "0") {
				w.WriteHeader(409)
				w.Write([]byte(`{"error":{"code":"REVISION_CONFLICT","message":"changed"}}`))
				return
			}
			stored, _ = io.ReadAll(r.Body)
			revision++
			write()
		case r.Method == "DELETE":
			if r.Header.Get("If-Match") != string(rune('0'+revision)) {
				w.WriteHeader(409)
				return
			}
			stored = nil
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	file := filepath.Join(t.TempDir(), "surface.yaml")
	if err := os.WriteFile(file, []byte(llmxSurfaceYAML), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := runLLMX(t, server, "table", "llm", "surfaces", "apply", "-f", file)
	if err != nil || !strings.Contains(out, "revision 1") {
		t.Fatal(out, err)
	}
	for _, kept := range []string{`"future_provider_field":"kept"`, `"future_top_level":"kept"`, `"secret_ref":"bedrock-key"`, `"max_session_seconds":600`} {
		if !strings.Contains(string(stored), kept) {
			t.Errorf("%s not sent: %s", kept, stored)
		}
	}
	if strings.Contains(string(stored), "secret_value") {
		t.Fatal("a secret value field appeared")
	}
	// Re-apply replaces at the revision read just before.
	if out, err = runLLMX(t, server, "table", "llm", "surfaces", "apply", "-f", file); err != nil || !strings.Contains(out, "revision 2") {
		t.Fatal(out, err)
	}
	if strings.Join(puts, ",") != "0,1" {
		t.Fatalf("If-Match sequence: %v", puts)
	}
	// An explicit stale revision is refused with guidance.
	if _, err = runLLMX(t, server, "table", "llm", "surfaces", "apply", "-f", file, "--revision", "1"); err == nil || !strings.Contains(err.Error(), "llm surfaces get bedrock-prod") {
		t.Fatalf("stale revision: %v", err)
	}
	// The record printed by `get` can be applied back unchanged.
	got, err := runLLMX(t, server, "json", "llm", "surfaces", "get", "bedrock-prod")
	if err != nil || !strings.Contains(got, `"revision": 2`) || !strings.Contains(got, "future_top_level") {
		t.Fatal(got, err)
	}
	roundTrip := filepath.Join(t.TempDir(), "record.json")
	os.WriteFile(roundTrip, []byte(got), 0o600)
	if out, err = runLLMX(t, server, "table", "llm", "surfaces", "apply", "-f", roundTrip); err != nil || !strings.Contains(out, "revision 3") {
		t.Fatal(out, err)
	}
	table, err := runLLMX(t, server, "table", "llm", "surfaces", "list")
	if err != nil || !strings.Contains(table, "bedrock") || !strings.Contains(table, "secret-ref:bedrock-key") || !strings.Contains(table, "AUTH (reference)") {
		t.Fatal(table, err)
	}
	if out, err = runLLMX(t, server, "table", "llm", "surfaces", "delete", "bedrock-prod"); err != nil || !strings.Contains(out, "Deleted") {
		t.Fatal(out, err)
	}
	if _, err = runLLMX(t, server, "table", "llm", "surfaces", "apply", "-f", filepath.Join(t.TempDir(), "missing.yaml")); err == nil {
		t.Fatal("missing file accepted")
	}
	empty := filepath.Join(t.TempDir(), "empty.yaml")
	os.WriteFile(empty, []byte("surface: {protocol: x}\n"), 0o600)
	if _, err = runLLMX(t, server, "table", "llm", "surfaces", "apply", "-f", empty); err == nil || !strings.Contains(err.Error(), "surface.id") {
		t.Fatal("surface without id accepted", err)
	}
}

func TestLLMCapacityShowsUnknownNotZero(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/llm/surfaces/oa/capacity":
			w.Write([]byte(`{"surface_id":"oa","observed":true,"scope":"replica-local last observation; advisory","limits":{"provider":"openai","observed_at":"2026-01-01T00:00:00Z","classification":"rate_limited","retry_after_ms":2000,"requests":{"limit":100,"remaining":0},"tokens":{"limit":1000}},"capacity_policy":{"enabled":true,"policy":{"cooldown_enabled":true,"max_cooldown_seconds":60,"default_weight":1,"signal_max_age_seconds":60},"cooldown":{"phase":"cooling","retry_after_ms":2000}}}`))
		case "/api/v1/llm/surfaces/idle/capacity":
			w.Write([]byte(`{"surface_id":"idle","observed":false,"scope":"replica-local last observation; advisory","capacity_policy":{"enabled":false}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	out, err := runLLMX(t, server, "table", "llm", "capacity", "oa")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"rate_limited", "remaining 0 of 100", "remaining unknown of 1000", "input:     not reported", "cooldown: cooling", "retry:     after 2s", "advisory"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	idle, err := runLLMX(t, server, "table", "llm", "capacity", "idle")
	if err != nil || !strings.Contains(idle, "no provider limit observation") || !strings.Contains(idle, "policy:    off") {
		t.Fatal(idle, err)
	}
	if js, err := runLLMX(t, server, "json", "llm", "capacity", "oa"); err != nil || !strings.Contains(js, `"phase": "cooling"`) {
		t.Fatal(js, err)
	}
}

func TestLLMBatchesListStatesPlainlyAboutExposure(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Query().Get("state") != "open" {
			t.Error("state filter not sent", r.URL.RawQuery)
		}
		if r.URL.Query().Get("cursor") == "" {
			w.Write([]byte(`{"batches":[{"id":"i1","surface_id":"oa","provider":"openai","batch_id":"b1","attempt_id":"att-1","create_operation":"op","state":"open","ended":false,"expected":null}],"next_cursor":"i1"}`))
			return
		}
		w.Write([]byte(`{"batches":[{"id":"i2","surface_id":"oa","provider":"openai","batch_id":"b2","create_operation":"op","state":"open","provider_status":"completed","ended":true,"expected":0}]}`))
	}))
	defer server.Close()
	out, err := runLLMX(t, server, "table", "llm", "batches", "list", "--state", "open", "--all")
	if err != nil || calls != 2 {
		t.Fatal(out, err, calls)
	}
	var b1, b2 string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, " b1 ") {
			b1 = l
		}
		if strings.Contains(l, " b2 ") {
			b2 = l
		}
	}
	if !strings.HasSuffix(strings.TrimSpace(b1), "unknown    unknown") || !strings.HasSuffix(strings.TrimSpace(b2), "completed  0") {
		t.Fatalf("unknown vs zero item count:\n%s", out)
	}
	if !strings.Contains(out, "holds quota exposure and a concurrency slot until its results are fetched through AIPlex") {
		t.Fatal("exposure statement missing:\n" + out)
	}
}

func TestConnectListsUtilityMediaAndSessionEndpoints(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"access_point_id":"ide","client":"generic","base_urls":{"openai":"https://x/ap/ide/v1"},"endpoints":[
{"protocol":"openai-embeddings","transport":"http","url":"https://x/ap/ide/v1/embeddings","label":"Embeddings","models":["e"]},
{"protocol":"openai-images","transport":"http","url":"https://x/ap/ide/v1/images/generations","label":"Image generation","models":["i"]},
{"protocol":"openai-audio","transport":"http","url":"https://x/ap/ide/v1/audio/speech","label":"Speech synthesis","models":["a"]},
{"protocol":"bedrock-converse","transport":"http-stream","url":"https://x/ap/ide/model/{model}/converse-stream","label":"ConverseStream","models":["b"]},
{"protocol":"openai-realtime","transport":"websocket","url":"wss://x/ap/ide/v1/realtime","label":"OpenAI Realtime","query":["model"],"models":["r"]}],
"models":[],"auth":{"scheme":"bearer","header":"Authorization","token_env":"AIPLEX_TOKEN","caller_kinds":[],"steps":[]},"snippets":[],"notes":[]}`))
	}))
	defer server.Close()
	out, err := runLLMX(t, server, "table", "connect", "ide")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"https://x/ap/ide/v1/embeddings", "Image generation [openai-images, http]", "Speech synthesis", "ConverseStream [bedrock-converse, http-stream]", "wss://x/ap/ide/v1/realtime (requires ?model=…)"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
}
