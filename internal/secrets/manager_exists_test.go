package secrets

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	pb "cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	"github.com/googleapis/gax-go/v2"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

type existsWireServer struct {
	pb.UnimplementedSecretManagerServiceServer
	code     codes.Code
	requests atomic.Int32
	entered  chan struct{}
}

func (s *existsWireServer) GetSecret(ctx context.Context, request *pb.GetSecretRequest) (*pb.Secret, error) {
	s.requests.Add(1)
	if request.Name != "projects/fixture-project/secrets/fixture-secret" {
		return nil, status.Error(codes.InvalidArgument, "unexpected secret coordinate")
	}
	if s.entered != nil {
		close(s.entered)
		<-ctx.Done()
		return nil, status.FromContextError(ctx.Err()).Err()
	}
	if s.code != codes.OK {
		return nil, status.Error(s.code, "fixture metadata lookup failed")
	}
	return &pb.Secret{Name: request.Name}, nil
}

func existsFixture(t *testing.T, code codes.Code, cancelDuringRPC bool) (*Manager, *existsWireServer) {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	backend := &existsWireServer{code: code}
	if cancelDuringRPC {
		backend.entered = make(chan struct{})
	}
	pb.RegisterSecretManagerServiceServer(server, backend)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	connection, err := grpc.NewClient("passthrough:///secrets-wire", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	client, err := secretmanager.NewClient(context.Background(), option.WithGRPCConn(connection), option.WithoutAuthentication())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	// Exercise each original status directly rather than masking unavailable
	// behind the SDK's ordinary retries until the test deadline expires.
	client.CallOptions.GetSecret = []gax.CallOption{gax.WithRetry(func() gax.Retryer { return nil })}
	return &Manager{client: client, projectID: "fixture-project"}, backend
}

func TestExistsRealSDKDistinguishesAbsenceFromLookupFailure(t *testing.T) {
	for _, code := range []codes.Code{codes.OK, codes.NotFound, codes.PermissionDenied, codes.Unavailable} {
		t.Run(code.String(), func(t *testing.T) {
			manager, backend := existsFixture(t, code, false)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			exists, err := manager.Exists(ctx, "fixture-secret")
			switch code {
			case codes.OK:
				if !exists || err != nil {
					t.Fatalf("existing secret refused: %t %v", exists, err)
				}
			case codes.NotFound:
				if exists || err != nil {
					t.Fatalf("absence must be false,nil: %t %v", exists, err)
				}
			default:
				if exists || err == nil || status.Code(err) != code {
					t.Fatalf("lookup failure became absence: exists=%t error=%v code=%v", exists, err, status.Code(err))
				}
			}
			if backend.requests.Load() != 1 {
				t.Fatalf("expected one actual SDK RPC, got %d", backend.requests.Load())
			}
		})
	}
}

func TestExistsRealSDKPropagatesCancellation(t *testing.T) {
	manager, backend := existsFixture(t, codes.OK, true)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	type result struct {
		exists bool
		err    error
	}
	done := make(chan result, 1)
	go func() { exists, err := manager.Exists(ctx, "fixture-secret"); done <- result{exists, err} }()
	select {
	case <-backend.entered:
		cancel()
	case <-ctx.Done():
		t.Fatal("SDK request did not reach metadata server")
	}
	select {
	case result := <-done:
		if result.exists || result.err == nil || status.Code(result.err) != codes.Canceled && !errors.Is(result.err, context.Canceled) {
			t.Fatalf("cancellation disguised as absence: %+v", result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("canceled metadata lookup did not return")
	}
}

func TestExistsNilManagerKeepsDevelopmentCompatibility(t *testing.T) {
	var manager *Manager
	exists, err := manager.Exists(context.Background(), "fixture-secret")
	if !exists || err != nil {
		t.Fatalf("nil dev manager changed: %t %v", exists, err)
	}
}
