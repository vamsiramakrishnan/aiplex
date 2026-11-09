package registry

import (
	"context"
	"testing"

	pb "cloud.google.com/go/firestore/apiv1/firestorepb"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
)

func TestIdentityPolicySnapshotIsIndependentAndUsesOneRevision(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	if err := store.PutRoleBinding(ctx, &models.RoleBinding{ID: "binding", Group: "team", Role: models.RoleViewer, Scopes: []string{"llm:model:a"}}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetUserScopes(ctx, "principal", []string{"mcp:tools:manual"}); err != nil {
		t.Fatal(err)
	}
	bindings, scopes, revision, err := store.ReadIdentityAccess(ctx, "principal")
	if err != nil || revision != 2 || len(bindings) != 1 || len(scopes) != 1 {
		t.Fatal(bindings, scopes, revision, err)
	}
	bindings[0].Scopes[0] = "altered"
	scopes[0] = "altered"
	again, manual, _, err := store.ReadIdentityAccess(ctx, "principal")
	if err != nil || again[0].Scopes[0] != "llm:model:a" || manual[0] != "mcp:tools:manual" {
		t.Fatal("snapshot leaked mutable buffers", again, manual, err)
	}
}
func TestFirestoreIdentitySnapshotCannotMixRoleAndManualPolicyRevisions(t *testing.T) {
	store, wire := queryWireFixture(t)
	ctx := context.Background()
	if err := store.PutRoleBinding(ctx, &models.RoleBinding{ID: "binding", Group: "team", Role: models.RoleViewer}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetUserScopes(ctx, "principal", []string{"llm:model:old"}); err != nil {
		t.Fatal(err)
	}
	wire.mu.Lock()
	wire.beforeTransactionalQuery = func(s *queryWireServer) {
		// A concurrent commit after the manual-grant read changes BOTH inputs.
		// The actual SDK query must retain its transaction read timestamp.
		for name, doc := range s.documents {
			if name == "projects/query-test/databases/(default)/documents/role_bindings/binding" {
				doc.Fields["Role"] = &pb.Value{ValueType: &pb.Value_StringValue{StringValue: "admin"}}
			}
			if name == "projects/query-test/databases/(default)/documents/user_scopes/principal" {
				doc.Fields["scopes"] = &pb.Value{ValueType: &pb.Value_ArrayValue{ArrayValue: &pb.ArrayValue{Values: []*pb.Value{{ValueType: &pb.Value_StringValue{StringValue: "llm:model:new"}}}}}}
			}
		}
	}
	wire.mu.Unlock()
	bindings, manual, revision, err := store.ReadIdentityAccess(ctx, "principal")
	if err != nil || revision != 2 || len(bindings) != 1 || bindings[0].Role != models.RoleViewer || len(manual) != 1 || manual[0] != "llm:model:old" {
		t.Fatal("mixed identity policy snapshots", bindings, manual, revision, err)
	}
}
