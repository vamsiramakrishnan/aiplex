package deploy

import (
	"context"
	"os"
	"os/exec"
	"reflect"
	"testing"
)

func TestK8sCleanupProcessFixture(t *testing.T) {
	if os.Getenv("AIPLEX_KUBECTL_CLEANUP_FIXTURE") == "1" {
		os.Exit(0)
	}
}

func TestLiveCleanupUsesInventoryAPIGroupAndRejectsFlagNames(t *testing.T) {
	previous := execCommandContext
	t.Cleanup(func() { execCommandContext = previous })
	var commands [][]string
	execCommandContext = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		commands = append(commands, append([]string{name}, args...))
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestK8sCleanupProcessFixture$")
		cmd.Env = append(os.Environ(), "AIPLEX_KUBECTL_CLEANUP_FIXTURE=1")
		return cmd
	}
	client := NewLiveK8sClientConfigured(WithKubeconfig("fixture-config"))
	if err := client.Delete(context.Background(), "gateway.networking.k8s.io/v1", "HTTPRoute", "owned", "mcplex"); err != nil {
		t.Fatal(err)
	}
	want := []string{"kubectl", "delete", "HTTPRoute.gateway.networking.k8s.io", "owned", "--ignore-not-found", "-n", "mcplex", "--kubeconfig", "fixture-config"}
	if len(commands) != 1 || !reflect.DeepEqual(commands[0], want) {
		t.Fatalf("cleanup discarded recorded group: %v", commands)
	}
	for _, resource := range []struct{ version, kind, name, namespace string }{
		{"v1", "Secret", "--all", "mcplex"},
		{"other/v1/extra", "HTTPRoute", "owned", "mcplex"},
		{"v1", "--all", "owned", "mcplex"},
		{"v1", "Secret", "owned", "--all-namespaces"},
	} {
		if err := client.Delete(context.Background(), resource.version, resource.kind, resource.name, resource.namespace); err == nil {
			t.Fatalf("invalid inventory invoked kubectl: %+v", resource)
		}
	}
	if len(commands) != 1 {
		t.Fatal("invalid cleanup inventory reached external execution")
	}
	if err := client.Delete(context.Background(), "v1", "Service", "owned", "mcplex"); err != nil {
		t.Fatal(err)
	}
	if commands[1][2] != "Service" {
		t.Fatal("core resource incorrectly group-qualified")
	}
}
