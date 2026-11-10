package deploy

import (
	"context"
	"testing"

	"gopkg.in/yaml.v3"
)

func manifestFixture() Manifest {
	return Manifest{APIVersion: "v1", Kind: "ConfigMap", Name: "declared", Namespace: "owned"}
}

func TestManifestCannotOverrideOwnershipOrSmuggleDocuments(t *testing.T) {
	for _, tc := range []struct{ name, payload string }{
		{"invalid YAML", "metadata: [unterminated"},
		{"array document", "- name: other"},
		{"null document", "null"},
		{"duplicate keys", "kind: ConfigMap\nkind: Secret"},
		{"second resource", "kind: ConfigMap\n---\napiVersion: v1\nkind: Secret\nmetadata:\n  name: injected"},
		{"empty extra document", "kind: ConfigMap\n---\n"},
		{"kind override", "kind: Secret"},
		{"api version override", "apiVersion: apps/v1"},
		{"name override", "metadata:\n  name: other"},
		{"namespace override", "metadata:\n  namespace: other"},
		{"invalid metadata", "metadata: forged"},
		{"null name", "metadata:\n  name: null"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manifest := manifestFixture()
			manifest.YAML = tc.payload
			if rendered, err := marshalManifest(manifest); err == nil {
				t.Fatalf("unsafe payload rendered successfully: %s", rendered)
			}
			// Dry-run takes the same validation path as real application and
			// performs no API call for either accepted or rejected inputs.
			if err := NewLiveK8sClientConfigured(WithDryRun(true)).Apply(context.Background(), manifest); err == nil {
				t.Fatal("live client accepted unsafe payload")
			}
		})
	}
}

func TestManifestRetainsPayloadAndDeclaredIdentity(t *testing.T) {
	for _, payload := range []string{
		"data:\n  answer: yes",
		"metadata:\n  labels:\n    managed-by: custom-module\ndata:\n  answer: yes",
		`{"kind":"ConfigMap","metadata":{"name":"declared","namespace":"owned"},"data":{"answer":"yes"}}`,
	} {
		manifest := manifestFixture()
		manifest.YAML = payload
		rendered, err := marshalManifest(manifest)
		if err != nil {
			t.Fatal(err)
		}
		var actual map[string]any
		if err := yaml.Unmarshal(rendered, &actual); err != nil {
			t.Fatal(err)
		}
		metadata := actual["metadata"].(map[string]any)
		if actual["apiVersion"] != "v1" || actual["kind"] != "ConfigMap" || metadata["name"] != "declared" || metadata["namespace"] != "owned" {
			t.Fatalf("resource identity changed: %s", rendered)
		}
		if actual["data"].(map[string]any)["answer"] != "yes" {
			t.Fatalf("module payload was lost: %s", rendered)
		}
	}
}

func TestDeploymentConfigurationCannotInjectYAMLStructure(t *testing.T) {
	config := map[string]any{
		"INPUT":              "value\"\n      hostNetwork: true\n      injected: \"yes",
		"QUOTED\"\nINJECTED": "line one\nline two\twith a tab and \\backslash",
		"JSON":               `{"nested":"value","quote":"\\\""}`,
	}
	manifest := deployment("owned", "module", "example/image:latest", config, nil, 1)
	rendered, err := marshalManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var actual map[string]any
	if err := yaml.Unmarshal(rendered, &actual); err != nil {
		t.Fatal(err)
	}
	pod := actual["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)
	if _, injected := pod["hostNetwork"]; injected {
		t.Fatalf("configuration escaped into pod spec: %s", rendered)
	}
	containers := pod["containers"].([]any)
	if len(containers) != 1 {
		t.Fatalf("configuration injected containers: %s", rendered)
	}
	environment := containers[0].(map[string]any)["env"].([]any)
	if len(environment) != len(config) {
		t.Fatalf("configuration injected environment entries: %s", rendered)
	}
	for _, item := range environment {
		entry := item.(map[string]any)
		name := entry["name"].(string)
		if entry["value"] != config[name] {
			t.Errorf("configuration scalar failed roundtrip: %q=%q, want %q", name, entry["value"], config[name])
		}
	}
}
