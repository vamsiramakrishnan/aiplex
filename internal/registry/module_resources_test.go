package registry

import (
	"cloud.google.com/go/firestore"
	pb "cloud.google.com/go/firestore/apiv1/firestorepb"
	"context"
	"encoding/json"
	"errors"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/timestamppb"
	"math"
	"net"
	"reflect"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
)

func TestModuleResourceFirestoreWireRevision(t *testing.T) {
	r := resources.Record{TenantID: "tenant", Kind: "connection", ID: "native", Revision: math.MaxInt64, Spec: json.RawMessage(`{"provider":"google"}`)}
	doc, err := resourceDocument(r)
	if err != nil {
		t.Fatal(err)
	}
	// Firestore's encoder rejects uint64; the wire field must remain signed.
	if reflect.TypeOf(doc.Revision).Kind() != reflect.Int64 {
		t.Fatal("Firestore revision is not signed")
	}
	got, err := recordFromResourceDoc(doc)
	if err != nil || got.Revision != r.Revision || string(got.Spec) != string(r.Spec) {
		t.Fatalf("wire roundtrip: %+v %v", got, err)
	}
	r.Revision = uint64(math.MaxInt64) + 1
	if _, err = resourceDocument(r); !errors.Is(err, resources.ErrConflict) {
		t.Fatal("out-of-range revision accepted", err)
	}
	for _, rev := range []int64{0, -1} {
		doc.Revision = rev
		if _, err = recordFromResourceDoc(doc); err == nil {
			t.Fatalf("persisted invalid revision %d accepted", rev)
		}
	}
}
func TestModuleResourceNamespaceAndMalformedStorage(t *testing.T) {
	doc := resourceDoc{TenantID: "tenant-a", Kind: "connection", ID: "one", Revision: 1, Spec: `{}`}
	r, err := recordFromResourceDoc(doc)
	if err != nil {
		t.Fatal(err)
	}
	if sameNamespace(r, "tenant-b", "connection", "one") || sameNamespace(r, "tenant-a", "other", "one") || sameNamespace(r, "tenant-a", "connection", "two") {
		t.Fatal("namespace mismatch accepted")
	}
	if resourceKey("a", "bc", "d") == resourceKey("ab", "c", "d") {
		t.Fatal("ambiguous namespace key")
	}
	doc.Spec = `not JSON`
	if _, err = recordFromResourceDoc(doc); err == nil {
		t.Fatal("malformed persisted spec accepted")
	}
}

type moduleWireServer struct {
	pb.UnimplementedFirestoreServer
	commits chan *pb.CommitRequest
}

func (s *moduleWireServer) Commit(ctx context.Context, request *pb.CommitRequest) (*pb.CommitResponse, error) {
	s.commits <- request
	return &pb.CommitResponse{WriteResults: []*pb.WriteResult{{UpdateTime: timestamppb.Now()}}, CommitTime: timestamppb.Now()}, nil
}

// Exercise the real Firestore SDK encoder without an emulator or cloud calls.
// A uint64 DTO fails before the fake Commit RPC and would regress this test.
func TestModuleResourceRealFirestoreEncoding(t *testing.T) {
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	fake := &moduleWireServer{commits: make(chan *pb.CommitRequest, 1)}
	pb.RegisterFirestoreServer(server, fake)
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()
	connection, err := grpc.NewClient("passthrough:///module-wire", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := firestore.NewClient(ctx, "module-test", option.WithGRPCConn(connection), option.WithoutAuthentication())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	doc, err := resourceDocument(resources.Record{TenantID: "t", Kind: "connection", ID: "one", Revision: 1, Spec: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.Batch().Set(client.Collection("module_resources").Doc("one"), doc).Commit(ctx); err != nil {
		t.Fatal("Firestore rejected module DTO", err)
	}
	select {
	case request := <-fake.commits:
		if len(request.Writes) != 1 || request.Writes[0].GetUpdate().Fields["revision"].GetIntegerValue() != 1 {
			t.Fatalf("unexpected wire representation: %+v", request)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}
