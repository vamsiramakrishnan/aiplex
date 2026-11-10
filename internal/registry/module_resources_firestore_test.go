package registry

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"cloud.google.com/go/firestore"
	pb "cloud.google.com/go/firestore/apiv1/firestorepb"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Exercise real SDK transaction encoding/decoding without cloud access. The
// server intentionally supports only the document CAS operations used here.
type resourceStateServer struct {
	pb.UnimplementedFirestoreServer
	mu        sync.Mutex
	documents map[string]*pb.Document
	deleted   bool
}

func (*resourceStateServer) BeginTransaction(context.Context, *pb.BeginTransactionRequest) (*pb.BeginTransactionResponse, error) {
	return &pb.BeginTransactionResponse{Transaction: []byte("transaction")}, nil
}
func (*resourceStateServer) Rollback(context.Context, *pb.RollbackRequest) (*emptypb.Empty, error) {
	return &emptypb.Empty{}, nil
}
func (s *resourceStateServer) BatchGetDocuments(request *pb.BatchGetDocumentsRequest, stream pb.Firestore_BatchGetDocumentsServer) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, name := range request.Documents {
		response := &pb.BatchGetDocumentsResponse{ReadTime: timestamppb.Now()}
		if doc := s.documents[name]; doc != nil {
			response.Result = &pb.BatchGetDocumentsResponse_Found{Found: proto.Clone(doc).(*pb.Document)}
		} else {
			response.Result = &pb.BatchGetDocumentsResponse_Missing{Missing: name}
		}
		if e := stream.Send(response); e != nil {
			return e
		}
	}
	return nil
}
func (s *resourceStateServer) Commit(_ context.Context, request *pb.CommitRequest) (*pb.CommitResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := timestamppb.Now()
	response := &pb.CommitResponse{CommitTime: now}
	for _, write := range request.Writes {
		if name := write.GetDelete(); name != "" {
			s.deleted = true
			delete(s.documents, name)
		} else if doc := write.GetUpdate(); doc != nil {
			copy := proto.Clone(doc).(*pb.Document)
			copy.CreateTime = now
			copy.UpdateTime = now
			s.documents[copy.Name] = copy
		}
		response.WriteResults = append(response.WriteResults, &pb.WriteResult{UpdateTime: now})
	}
	return response, nil
}

func TestFirestoreResourceDeleteRecreateKeepsCASGeneration(t *testing.T) {
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	backend := &resourceStateServer{documents: map[string]*pb.Document{}}
	pb.RegisterFirestoreServer(server, backend)
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()
	connection, e := grpc.NewClient("passthrough:///resource-state", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if e != nil {
		t.Fatal(e)
	}
	defer connection.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, e := firestore.NewClient(ctx, "resource-test", option.WithGRPCConn(connection), option.WithoutAuthentication())
	if e != nil {
		t.Fatal(e)
	}
	defer client.Close()
	store := &firestoreResources{client: client}
	r := resources.Record{TenantID: "tenant", Kind: "model-surface", ID: "surface", Spec: json.RawMessage(`{"model":"old"}`)}
	first, e := store.Put(ctx, r, 0)
	if e != nil {
		t.Fatal(e)
	}
	if e = store.Delete(ctx, r.TenantID, r.Kind, r.ID, first.Revision); e != nil {
		t.Fatal(e)
	}
	if _, e = store.Get(ctx, r.TenantID, r.Kind, r.ID); !errors.Is(e, resources.ErrNotFound) {
		t.Fatal("deleted resource visible", e)
	}
	backend.mu.Lock()
	for _, document := range backend.documents {
		if !document.Fields["deleted"].GetBooleanValue() || document.Fields["revision"].GetIntegerValue() != int64(first.Revision) || document.Fields["spec"].GetStringValue() != "null" {
			t.Errorf("invalid tombstone wire representation: %+v", document)
		}
	}
	physicalDelete := backend.deleted
	backend.mu.Unlock()
	if physicalDelete {
		t.Fatal("physical delete discarded revision history")
	}
	if _, e = store.Put(ctx, r, first.Revision); !errors.Is(e, resources.ErrConflict) {
		t.Fatal("stale update resurrected tombstone", e)
	}
	r.Spec = json.RawMessage(`{"model":"new"}`)
	recreated, e := store.Put(ctx, r, 0)
	if e != nil || recreated.Revision <= first.Revision {
		t.Fatal("recreation reused revision", recreated, e)
	}
	if _, e = store.Put(ctx, r, first.Revision); !errors.Is(e, resources.ErrConflict) {
		t.Fatal("stale ETag overwrote recreated surface", e)
	}
	if e = store.Delete(ctx, r.TenantID, r.Kind, r.ID, first.Revision); !errors.Is(e, resources.ErrConflict) {
		t.Fatal("stale ETag deleted recreated surface", e)
	}
}
