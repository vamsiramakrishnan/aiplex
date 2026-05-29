package deploy

import (
	"fmt"
	"sort"
	"strings"

	"github.com/vamsiramakrishnan/aiplex/internal/models"
)

// tapeRuntimeNamespace is where the per-environment tape-server +
// reactors live. One Tape server per env (not per agent), so agents in
// any plane that opt into the durable runtime share the same backend.
const tapeRuntimeNamespace = "aiplex-system"

// Default container image. The Tape repo ships releases as
// `tape-server_<version>_<target>.tar.gz`; for the GKE / Cloud Run
// images the convention is ghcr.io/vamsiramakrishnan/tape-server:<version>.
// Overridable per-environment via the runtime config in PR 4's model
// (Store.SecretRef etc are already there; image override is a follow-up).
const tapeServerImage = "ghcr.io/vamsiramakrishnan/tape-server:latest"

// tapeServerPort is the gRPC port the tape-server listens on (matches
// the Tape repo's Helm chart default; see
// durable-agents/tape/deploy/gcp/k8s/chart/tape/templates/server.yaml).
const tapeServerPort = 7878

// tapeServiceDNS is the in-cluster DNS name used as TAPE_URL on agent
// pods. Constant because the Service is created in a fixed namespace
// under a fixed name (tape-server-deploys are idempotent per env).
const tapeServiceDNS = "tape-server.aiplex-system.svc.cluster.local"

// tapeRuntimeManifests generates the env-scoped Tape infrastructure
// (server + reactors + Service) that backs every Tape-enabled agent in
// the cluster. The resources are stable per (Namespace, Name), so
// applying them on every Tape-backed agent deploy is a no-op after the
// first one — that's how we get "one tape-server per env" without
// tracking deploy-order state.
//
// Returns nil when the instance doesn't use the Tape engine, so callers
// can unconditionally splice the result into their manifest list.
func tapeRuntimeManifests(inst *models.Instance) []Manifest {
	if inst.Runtime.Engine != models.RuntimeEngineTape {
		return nil
	}
	rc := inst.Runtime
	return []Manifest{
		tapeServerDeployment(rc),
		tapeServerService(),
		tapeReactorsDeployment(rc),
	}
}

// tapeAgentEnv returns the AIPLEX_* + TAPE_URL env vars that get
// injected onto an agent pod backed by the Tape runtime. The keys
// mirror what `tape.adk.identity.RunIdentity.from_env()` reads in the
// Python SDK (see durable-agents tape/sdk/python/tape/adk/identity.py).
//
// AIPLEX_SCOPES is the space-joined Instance.Scopes — the same string
// the gateway authz layer already enforces, threaded through Tape so
// scoped effects re-check at the journal boundary.
func tapeAgentEnv(inst *models.Instance, gatewayName string) map[string]string {
	if inst.Runtime.Engine != models.RuntimeEngineTape {
		return nil
	}
	env := map[string]string{
		"TAPE_URL":           fmt.Sprintf("tape://%s:%d", tapeServiceDNS, tapeServerPort),
		"AIPLEX_AGENT_ID":    inst.ID,
		"AIPLEX_INSTANCE_ID": inst.ID,
		"AIPLEX_ACTOR":       inst.SpiffeID,
		"AIPLEX_TENANT_ID":   tenantFromLabels(inst.Labels),
		"AIPLEX_SUBJECT":     inst.DeployedBy,
		"AIPLEX_ROUTE":       agentRoute(inst, gatewayName),
		"AIPLEX_SCOPES":      strings.Join(inst.Scopes, " "),
	}
	if labels := serializeLabels(inst.Labels); labels != "" {
		env["AIPLEX_LABELS"] = labels
	}
	return env
}

// tenantFromLabels extracts the tenant id from a conventional
// "aiplex.tenant" label, falling back to "default" — the controller
// always writes the label on Tape-enabled instances, but a hand-crafted
// YAML may omit it.
func tenantFromLabels(labels map[string]string) string {
	if t, ok := labels["aiplex.tenant"]; ok && t != "" {
		return t
	}
	return "default"
}

// serializeLabels turns the instance's labels map into the
// "k=v,k=v"-style string that RunIdentity.from_env parses. Sorted so
// the manifest YAML is byte-stable across deploys.
func serializeLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%s", k, labels[k]))
	}
	return strings.Join(parts, ",")
}

// agentRoute is the gateway path this agent is exposed under. Mirrors
// the route generation in routes.go but kept inline because the env
// var is just a hint for the journal — not authoritative.
func agentRoute(inst *models.Instance, _ string) string {
	switch inst.Plane {
	case models.PlaneMCPlex:
		return "/mcp/" + inst.ID
	case models.PlaneA2APlex:
		return "/a2a/" + inst.ID
	case models.PlaneLLMPlex:
		return "/llm/" + inst.ID
	default:
		return "/" + string(inst.Plane) + "/" + inst.ID
	}
}

func tapeServerDeployment(rc models.RuntimeConfig) Manifest {
	// TAPE_STORE comes from the SecretRef when the store is durable
	// (postgres / alloydb / bigtable). SQLite uses an in-pod file —
	// dev only; production fails validation in RuntimeConfig.Validate.
	// The server itself doesn't need the reactor flags; those live on
	// the reactors deployment below.
	storeURL := tapeStoreEnvSource(rc.Store)
	outboxEnv := tapeOutboxEnvSource(rc.Outbox)

	return Manifest{
		APIVersion: "apps/v1",
		Kind:       "Deployment",
		Name:       "tape-server",
		Namespace:  tapeRuntimeNamespace,
		YAML: fmt.Sprintf(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: tape-server
  namespace: %s
  labels:
    app.kubernetes.io/name: tape-server
    app.kubernetes.io/component: durable-runtime
    app.kubernetes.io/managed-by: aiplex
spec:
  replicas: 1
  selector:
    matchLabels:
      app.kubernetes.io/name: tape-server
  template:
    metadata:
      labels:
        app.kubernetes.io/name: tape-server
    spec:
      serviceAccountName: tape-server
      containers:
      - name: tape-server
        image: %s
        args:
        - --listen
        - 0.0.0.0:%d
        ports:
        - containerPort: %d
          name: grpc
          protocol: TCP
        env:
%s%s        resources:
          requests:
            cpu: 200m
            memory: 256Mi
          limits:
            cpu: 1000m
            memory: 1Gi
        livenessProbe:
          tcpSocket: { port: grpc }
          initialDelaySeconds: 5
          periodSeconds: 10
`, tapeRuntimeNamespace, tapeServerImage, tapeServerPort, tapeServerPort,
			storeURL, outboxEnv),
	}
}

func tapeServerService() Manifest {
	return Manifest{
		APIVersion: "v1",
		Kind:       "Service",
		Name:       "tape-server",
		Namespace:  tapeRuntimeNamespace,
		YAML: fmt.Sprintf(`apiVersion: v1
kind: Service
metadata:
  name: tape-server
  namespace: %s
  labels:
    app.kubernetes.io/name: tape-server
    app.kubernetes.io/managed-by: aiplex
spec:
  selector:
    app.kubernetes.io/name: tape-server
  ports:
  - name: grpc
    port: %d
    targetPort: grpc
    protocol: TCP
`, tapeRuntimeNamespace, tapeServerPort),
	}
}

// tapeReactorsDeployment runs the recovery / reconciler / timers /
// outbox / compensation loops in a single sidecar pod. Per-reactor
// scaling is a follow-up; today they all share one pod for cost &
// operational simplicity (matches the Tape Helm chart default).
func tapeReactorsDeployment(rc models.RuntimeConfig) Manifest {
	storeURL := tapeStoreEnvSource(rc.Store)
	reactorEnv := tapeReactorEnvSource(rc.Reactors)
	return Manifest{
		APIVersion: "apps/v1",
		Kind:       "Deployment",
		Name:       "tape-reactors",
		Namespace:  tapeRuntimeNamespace,
		YAML: fmt.Sprintf(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: tape-reactors
  namespace: %s
  labels:
    app.kubernetes.io/name: tape-reactors
    app.kubernetes.io/component: durable-runtime
    app.kubernetes.io/managed-by: aiplex
spec:
  replicas: 1
  selector:
    matchLabels:
      app.kubernetes.io/name: tape-reactors
  template:
    metadata:
      labels:
        app.kubernetes.io/name: tape-reactors
    spec:
      serviceAccountName: tape-reactors
      containers:
      - name: tape-reactors
        image: %s
        args: ["--mode", "reactors"]
        env:
        - name: TAPE_URL
          value: tape://%s:%d
%s%s        resources:
          requests:
            cpu: 100m
            memory: 128Mi
          limits:
            cpu: 500m
            memory: 512Mi
`, tapeRuntimeNamespace, tapeServerImage, tapeServiceDNS, tapeServerPort,
			storeURL, reactorEnv),
	}
}

// tapeStoreEnvSource emits the TAPE_STORE env var. For sqlite, a literal
// file path. For postgres / alloydb / bigtable, a Secret reference so
// the manifest YAML never carries the connection string in plaintext.
func tapeStoreEnvSource(s models.RuntimeStoreConfig) string {
	switch s.Type {
	case models.RuntimeStoreSQLite, "":
		return `        - name: TAPE_STORE
          value: sqlite:/var/lib/tape/tape.db
`
	default:
		return fmt.Sprintf(`        - name: TAPE_STORE
          valueFrom:
            secretKeyRef:
              name: %s
              key: url
`, s.SecretRef)
	}
}

func tapeReactorEnvSource(r models.RuntimeReactorsConfig) string {
	flag := func(name string, on bool) string {
		v := "0"
		if on {
			v = "1"
		}
		return fmt.Sprintf("        - name: %s\n          value: \"%s\"\n", name, v)
	}
	return flag("TAPE_REACTOR_RECOVERY", r.Recovery) +
		flag("TAPE_REACTOR_RECONCILER", r.Reconciler) +
		flag("TAPE_REACTOR_TIMERS", r.Timers) +
		flag("TAPE_REACTOR_OUTBOX", r.Outbox) +
		flag("TAPE_REACTOR_COMPENSATION", r.Compensation)
}

func tapeOutboxEnvSource(o models.RuntimeOutboxConfig) string {
	if o.Sink == "" || o.Sink == models.RuntimeOutboxLog {
		return `        - name: TAPE_OUTBOX_SINK
          value: log
`
	}
	out := fmt.Sprintf(`        - name: TAPE_OUTBOX_SINK
          value: %s
`, o.Sink)
	if o.Topic != "" {
		out += fmt.Sprintf(`        - name: TAPE_OUTBOX_TOPIC
          value: %s
`, o.Topic)
	}
	return out
}
