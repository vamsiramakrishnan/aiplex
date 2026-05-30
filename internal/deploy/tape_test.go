package deploy_test

import (
	"strings"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/internal/deploy"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
)

// tapeInstance returns a Tape-backed A2A agent instance for the
// manifest tests below. The runtime config matches what the deploy
// engine builds from `runtime:` in the YAML — see PR 4's
// runtimeFromConfig.
func tapeInstance() *models.Instance {
	return &models.Instance{
		ID:        "treasury-agent-abc123",
		Plane:     models.PlaneA2APlex,
		Namespace: "a2aplex",
		Replicas:  1,
		Owner:     "vamsi@example.com",
		SpiffeID:  "spiffe://test.local/ns/a2aplex/sa/treasury-agent-abc123",
		Scopes:    []string{"mcp:tools:bank_wire", "llm:model:gemini-2.5-pro"},
		Labels: map[string]string{
			"aiplex.tenant": "acme",
			"aiplex.plane":  "a2a",
		},
		DeployedBy: "vamsi@example.com",
		Runtime: models.RuntimeConfig{
			Engine:  models.RuntimeEngineTape,
			Durable: true,
			Store: models.RuntimeStoreConfig{
				Type:      models.RuntimeStoreAlloyDB,
				SecretRef: "tape-store-url",
			},
			Reactors: models.RuntimeReactorsConfig{
				Recovery: true, Reconciler: true, Timers: true,
				Outbox: true, Compensation: true,
			},
			Outbox: models.RuntimeOutboxConfig{
				Sink:  models.RuntimeOutboxPubSub,
				Topic: "aiplex-tape-events",
			},
		},
	}
}

// TestGenerateManifests_TapeRuntime — a Tape-enabled instance emits the
// per-agent resources AND the env-scoped tape-server + reactors.
func TestGenerateManifests_TapeRuntime(t *testing.T) {
	inst := tapeInstance()
	tmpl := &models.Template{ID: "treasury-agent", Image: "ghcr.io/acme/treasury:v1"}

	manifests := deploy.GenerateManifests(inst, tmpl, "test.local")

	// 4 per-agent + 3 runtime = 7
	if len(manifests) != 7 {
		t.Fatalf("expected 7 manifests (4 agent + 3 tape), got %d", len(manifests))
	}

	want := map[string]string{
		"ServiceAccount/" + inst.ID:       "a2aplex",
		"Deployment/" + inst.ID:           "a2aplex",
		"Service/" + inst.ID:              "a2aplex",
		"NetworkPolicy/" + inst.ID + "-ingress": "a2aplex",
		"Deployment/tape-server":           "aiplex-system",
		"Service/tape-server":              "aiplex-system",
		"Deployment/tape-reactors":         "aiplex-system",
	}
	got := map[string]string{}
	for _, m := range manifests {
		got[m.Kind+"/"+m.Name] = m.Namespace
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("missing or wrong namespace for %s: want %s, got %s", k, v, got[k])
		}
	}
}

// TestGenerateManifests_TapeEnvInjection — the agent pod's Deployment
// carries TAPE_URL + AIPLEX_* env vars derived from the instance.
func TestGenerateManifests_TapeEnvInjection(t *testing.T) {
	inst := tapeInstance()
	tmpl := &models.Template{ID: "treasury-agent", Image: "ghcr.io/acme/treasury:v1"}

	manifests := deploy.GenerateManifests(inst, tmpl, "test.local")
	var agentDeploy string
	for _, m := range manifests {
		if m.Kind == "Deployment" && m.Name == inst.ID {
			agentDeploy = m.YAML
		}
	}
	if agentDeploy == "" {
		t.Fatalf("agent deployment manifest not found")
	}

	wantEnv := map[string]string{
		"TAPE_URL":           "tape://tape-server.aiplex-system.svc.cluster.local:7878",
		"AIPLEX_AGENT_ID":    inst.ID,
		"AIPLEX_INSTANCE_ID": inst.ID,
		"AIPLEX_ACTOR":       inst.SpiffeID,
		"AIPLEX_TENANT_ID":   "acme",
		"AIPLEX_SUBJECT":     "vamsi@example.com",
		"AIPLEX_ROUTE":       "/a2a/" + inst.ID,
		"AIPLEX_SCOPES":      "mcp:tools:bank_wire llm:model:gemini-2.5-pro",
		// labels sorted: aiplex.plane only. aiplex.tenant is stripped
		// because it already lives on tape_runs.tenant_id (see
		// labelKeysWithDedicatedColumns in tape.go).
		"AIPLEX_LABELS": "aiplex.plane=a2a",
	}
	for k, v := range wantEnv {
		// Check `- name: K` appears immediately followed by the value
		// (deployment() emits quoted-value form for extras).
		marker := "- name: " + k
		if !strings.Contains(agentDeploy, marker) {
			t.Errorf("missing env var %s on agent deployment", k)
			continue
		}
		if !strings.Contains(agentDeploy, v) {
			t.Errorf("env %s: expected value %q to appear in YAML, not found", k, v)
		}
	}

	// Regression guard for the dedicated-columns strip: aiplex.tenant
	// gets its own AIPLEX_TENANT_ID env var and its own tape_runs
	// column. It must not also appear inside AIPLEX_LABELS — that would
	// write the same value into labels_json on every journal row.
	if strings.Contains(agentDeploy, "aiplex.plane=a2a,aiplex.tenant=acme") ||
		strings.Contains(agentDeploy, "aiplex.tenant=acme,aiplex.plane=a2a") {
		t.Errorf("AIPLEX_LABELS leaks aiplex.tenant — should be stripped because tenant_id has a dedicated column")
	}
}

// TestGenerateManifests_NoneRuntime — the v1 path: no runtime config,
// no tape-server, no AIPLEX_* env vars. Existing tests already cover
// this implicitly; this test makes the absence explicit.
func TestGenerateManifests_NoneRuntime(t *testing.T) {
	inst := &models.Instance{
		ID:        "plain-agent-abc",
		Plane:     models.PlaneA2APlex,
		Namespace: "a2aplex",
		Runtime:   models.NoneRuntime(),
	}
	tmpl := &models.Template{ID: "plain", Image: "gcr.io/x:1"}

	manifests := deploy.GenerateManifests(inst, tmpl, "test.local")
	if len(manifests) != 4 {
		t.Fatalf("expected 4 manifests for engine=none, got %d", len(manifests))
	}
	for _, m := range manifests {
		if strings.Contains(m.YAML, "AIPLEX_") {
			t.Errorf("engine=none deployment unexpectedly carries AIPLEX_* env: %s", m.Name)
		}
		if strings.Contains(m.YAML, "TAPE_URL") {
			t.Errorf("engine=none deployment unexpectedly carries TAPE_URL: %s", m.Name)
		}
		if m.Name == "tape-server" || m.Name == "tape-reactors" {
			t.Errorf("engine=none emitted runtime resource: %s", m.Name)
		}
	}
}

// TestTapeServerManifest_StoreSecretRef — postgres/alloydb/bigtable
// stores must reference a Secret rather than inlining a connection
// string. Validation already enforces this at admission; the manifest
// generator must also honour it.
func TestTapeServerManifest_StoreSecretRef(t *testing.T) {
	inst := tapeInstance()
	tmpl := &models.Template{ID: "treasury-agent", Image: "ghcr.io/acme/treasury:v1"}
	manifests := deploy.GenerateManifests(inst, tmpl, "test.local")

	var serverYAML string
	for _, m := range manifests {
		if m.Kind == "Deployment" && m.Name == "tape-server" {
			serverYAML = m.YAML
		}
	}
	if serverYAML == "" {
		t.Fatal("tape-server deployment not found")
	}
	// Should reference the secret by name, not inline a connection URL.
	if !strings.Contains(serverYAML, "secretKeyRef") {
		t.Error("expected secretKeyRef for TAPE_STORE, got inline value")
	}
	if !strings.Contains(serverYAML, "name: tape-store-url") {
		t.Error("expected secret name to be tape-store-url")
	}
}

// TestTapeServerManifest_SqliteFallback — sqlite stores use a literal
// in-pod path. Dev-only but the codepath has to exist for local stack.
func TestTapeServerManifest_SqliteFallback(t *testing.T) {
	inst := tapeInstance()
	inst.Runtime.Store = models.RuntimeStoreConfig{Type: models.RuntimeStoreSQLite}
	tmpl := &models.Template{ID: "treasury-agent", Image: "ghcr.io/acme/treasury:v1"}

	var serverYAML string
	for _, m := range deploy.GenerateManifests(inst, tmpl, "test.local") {
		if m.Kind == "Deployment" && m.Name == "tape-server" {
			serverYAML = m.YAML
		}
	}
	if !strings.Contains(serverYAML, "sqlite:/var/lib/tape/tape.db") {
		t.Errorf("expected sqlite literal value, got: %s", serverYAML)
	}
	if strings.Contains(serverYAML, "secretKeyRef") {
		t.Error("sqlite store should not use a secretKeyRef")
	}
}

// TestTapeReactorsManifest_FlagsOnOff — each reactor's flag is emitted
// independently. Used by AIPlex's deploy admin to disable specific
// loops (e.g. turn off outbox in dev to avoid clobbering a shared
// Pub/Sub topic).
func TestTapeReactorsManifest_FlagsOnOff(t *testing.T) {
	inst := tapeInstance()
	inst.Runtime.Reactors = models.RuntimeReactorsConfig{
		Recovery: true, Reconciler: false, Timers: true,
		Outbox: false, Compensation: true,
	}
	tmpl := &models.Template{ID: "treasury-agent", Image: "ghcr.io/acme/treasury:v1"}

	var reactorsYAML string
	for _, m := range deploy.GenerateManifests(inst, tmpl, "test.local") {
		if m.Kind == "Deployment" && m.Name == "tape-reactors" {
			reactorsYAML = m.YAML
		}
	}
	checks := map[string]string{
		"TAPE_REACTOR_RECOVERY":     "\"1\"",
		"TAPE_REACTOR_RECONCILER":   "\"0\"",
		"TAPE_REACTOR_TIMERS":       "\"1\"",
		"TAPE_REACTOR_OUTBOX":       "\"0\"",
		"TAPE_REACTOR_COMPENSATION": "\"1\"",
	}
	for k, want := range checks {
		// Look for the env-var name + the expected value on the next
		// `value:` line. Cheap substring check; precision left to
		// integration tests.
		idx := strings.Index(reactorsYAML, "name: "+k)
		if idx < 0 {
			t.Errorf("reactors deployment missing %s", k)
			continue
		}
		rest := reactorsYAML[idx:]
		if !strings.Contains(rest[:120], want) {
			t.Errorf("%s: expected %s nearby, got: %.120s", k, want, rest)
		}
	}
}

// TestTapeOutboxEnv_PubsubTopic — sink=pubsub emits both SINK and TOPIC.
func TestTapeOutboxEnv_PubsubTopic(t *testing.T) {
	inst := tapeInstance() // configured with sink=pubsub, topic=aiplex-tape-events
	tmpl := &models.Template{ID: "treasury-agent", Image: "ghcr.io/acme/treasury:v1"}

	var serverYAML string
	for _, m := range deploy.GenerateManifests(inst, tmpl, "test.local") {
		if m.Kind == "Deployment" && m.Name == "tape-server" {
			serverYAML = m.YAML
		}
	}
	if !strings.Contains(serverYAML, "TAPE_OUTBOX_SINK") ||
		!strings.Contains(serverYAML, "pubsub") {
		t.Error("expected TAPE_OUTBOX_SINK=pubsub on tape-server")
	}
	if !strings.Contains(serverYAML, "TAPE_OUTBOX_TOPIC") ||
		!strings.Contains(serverYAML, "aiplex-tape-events") {
		t.Error("expected TAPE_OUTBOX_TOPIC=aiplex-tape-events on tape-server")
	}
}
