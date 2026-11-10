package main

import (
	"strings"
	"testing"
)

// The state bucket is project-scoped and always versioned, whether the CLI
// creates it or finds it.
func TestStateBucketCommandsCreateAndVersion(t *testing.T) {
	create := stateBucketCommands("p1", false)
	if len(create) != 2 || !strings.Contains(strings.Join(create[0], " "), "buckets create gs://p1-aiplex-tfstate") {
		t.Fatalf("create: %v", create)
	}
	existing := stateBucketCommands("p1", true)
	if len(existing) != 1 {
		t.Fatalf("existing bucket must not be re-created: %v", existing)
	}
	for _, cmds := range [][][]string{create, existing} {
		last := strings.Join(cmds[len(cmds)-1], " ")
		if !strings.Contains(last, "buckets update gs://p1-aiplex-tfstate --versioning") {
			t.Fatalf("versioning not enabled: %q", last)
		}
	}
}
