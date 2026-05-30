package api_test

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/vamsiramakrishnan/aiplex/internal/api"
	pb "github.com/vamsiramakrishnan/durable-agents/tape/sdk/go/tapepb"
)

// fakeTapeServer records every admin RPC the adapter forwards.
type fakeTapeServer struct {
	pb.UnimplementedTapeServer
	mu        chan struct{}
	resumeIDs []string
	endIDs    []string
	endStatus []pb.RunStatus
	signalIDs []string
	signalGates []string
	errOn     map[string]error
}

func newFakeTapeServer() *fakeTapeServer {
	return &fakeTapeServer{mu: make(chan struct{}, 1), errOn: map[string]error{}}
}

func (f *fakeTapeServer) ResumeRun(ctx context.Context, req *pb.ResumeRunRequest) (*pb.ResumeRunResponse, error) {
	if e, ok := f.errOn["ResumeRun"]; ok {
		return nil, e
	}
	f.resumeIDs = append(f.resumeIDs, req.RunId)
	return &pb.ResumeRunResponse{}, nil
}

func (f *fakeTapeServer) EndRun(ctx context.Context, req *pb.EndRunRequest) (*pb.EndRunResponse, error) {
	if e, ok := f.errOn["EndRun"]; ok {
		return nil, e
	}
	f.endIDs = append(f.endIDs, req.RunId)
	f.endStatus = append(f.endStatus, req.Status)
	return &pb.EndRunResponse{}, nil
}

func (f *fakeTapeServer) SendSignal(ctx context.Context, req *pb.SendSignalRequest) (*pb.SendSignalResponse, error) {
	if e, ok := f.errOn["SendSignal"]; ok {
		return nil, e
	}
	f.signalIDs = append(f.signalIDs, req.RunId)
	f.signalGates = append(f.signalGates, req.GateName)
	return &pb.SendSignalResponse{}, nil
}

// spinUpServer launches the fake gRPC service on a bufconn listener
// and exposes a TAPE_URL the GRPCTapeAdmin can dial.
func spinUpServer(t *testing.T) (*fakeTapeServer, func()) {
	t.Helper()
	listener := bufconn.Listen(1 << 16)
	srv := grpc.NewServer()
	fake := newFakeTapeServer()
	pb.RegisterTapeServer(srv, fake)
	go func() { _ = srv.Serve(listener) }()

	// Hijack the bufconn into an actual local TCP socket so
	// tape.Dial(url) (which doesn't know about bufconn) can reach it.
	tcpListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	url := "tape://" + tcpListener.Addr().String()
	tcpListener.Close()
	listener2, err := net.Listen("tcp", "127.0.0.1:"+portOf(url))
	if err != nil {
		t.Fatal(err)
	}
	srv2 := grpc.NewServer()
	pb.RegisterTapeServer(srv2, fake)
	go func() { _ = srv2.Serve(listener2) }()

	t.Setenv("TAPE_URL", url)
	return fake, func() {
		srv.Stop()
		srv2.Stop()
		listener.Close()
		listener2.Close()
	}
}

func portOf(url string) string {
	// url is tape://127.0.0.1:NNNN
	i := len(url) - 1
	for i >= 0 && url[i] != ':' {
		i--
	}
	if i < 0 {
		return ""
	}
	return url[i+1:]
}

func newAdmin(t *testing.T) *api.GRPCTapeAdmin {
	t.Helper()
	a, ok := api.NewGRPCTapeAdmin()
	if !ok {
		t.Fatal("NewGRPCTapeAdmin returned !ok despite TAPE_URL being set")
	}
	return a
}

func TestGRPCTapeAdmin_NoEnv_ReturnsNotOK(t *testing.T) {
	t.Setenv("TAPE_URL", "")
	if _, ok := api.NewGRPCTapeAdmin(); ok {
		t.Error("expected (nil, false) when TAPE_URL is empty")
	}
}

func TestGRPCTapeAdmin_Redrive_CallsResumeRun(t *testing.T) {
	fake, cleanup := spinUpServer(t)
	defer cleanup()
	admin := newAdmin(t)
	defer admin.Close()

	if err := admin.Redrive(context.Background(), "run-1"); err != nil {
		t.Fatalf("Redrive failed: %v", err)
	}
	if len(fake.resumeIDs) != 1 || fake.resumeIDs[0] != "run-1" {
		t.Errorf("expected ResumeRun(run-1), got %+v", fake.resumeIDs)
	}
}

func TestGRPCTapeAdmin_Reconcile_AlsoCallsResumeRun(t *testing.T) {
	fake, cleanup := spinUpServer(t)
	defer cleanup()
	admin := newAdmin(t)
	defer admin.Close()

	if err := admin.Reconcile(context.Background(), "run-2"); err != nil {
		t.Fatal(err)
	}
	if len(fake.resumeIDs) != 1 || fake.resumeIDs[0] != "run-2" {
		t.Errorf("expected ResumeRun(run-2), got %+v", fake.resumeIDs)
	}
}

func TestGRPCTapeAdmin_Compensate_AlsoCallsResumeRun(t *testing.T) {
	fake, cleanup := spinUpServer(t)
	defer cleanup()
	admin := newAdmin(t)
	defer admin.Close()

	if err := admin.Compensate(context.Background(), "run-3"); err != nil {
		t.Fatal(err)
	}
	if len(fake.resumeIDs) != 1 || fake.resumeIDs[0] != "run-3" {
		t.Errorf("expected ResumeRun(run-3), got %+v", fake.resumeIDs)
	}
}

func TestGRPCTapeAdmin_Cancel_CallsEndRunWithCancelled(t *testing.T) {
	fake, cleanup := spinUpServer(t)
	defer cleanup()
	admin := newAdmin(t)
	defer admin.Close()

	if err := admin.Cancel(context.Background(), "run-4", "test reason"); err != nil {
		t.Fatal(err)
	}
	if len(fake.endIDs) != 1 || fake.endIDs[0] != "run-4" {
		t.Errorf("expected EndRun(run-4), got %+v", fake.endIDs)
	}
	if fake.endStatus[0] != pb.RunStatus_RUN_STATUS_CANCELLED {
		t.Errorf("expected RUN_STATUS_CANCELLED, got %v", fake.endStatus[0])
	}
}

func TestGRPCTapeAdmin_Signal_RequiresGateName(t *testing.T) {
	_, cleanup := spinUpServer(t)
	defer cleanup()
	admin := newAdmin(t)
	defer admin.Close()

	if err := admin.Signal(context.Background(), "run-5", "", ""); err == nil {
		t.Error("expected error for empty gate_name")
	}
}

func TestGRPCTapeAdmin_Signal_PropagatesGate(t *testing.T) {
	fake, cleanup := spinUpServer(t)
	defer cleanup()
	admin := newAdmin(t)
	defer admin.Close()

	if err := admin.Signal(context.Background(), "run-6", "approval", `{"ok":true}`); err != nil {
		t.Fatal(err)
	}
	if len(fake.signalIDs) != 1 || fake.signalGates[0] != "approval" {
		t.Errorf("expected SendSignal(run-6, approval), got ids=%+v gates=%+v",
			fake.signalIDs, fake.signalGates)
	}
}

func TestGRPCTapeAdmin_TapeError_SurfacesVerbatim(t *testing.T) {
	fake, cleanup := spinUpServer(t)
	defer cleanup()
	fake.errOn["ResumeRun"] = status.Error(codes.Unavailable, "tape: out for lunch")

	admin := newAdmin(t)
	defer admin.Close()

	err := admin.Redrive(context.Background(), "run-7")
	if err == nil {
		t.Fatal("expected error to bubble up")
	}
	if got := status.Code(err); got != codes.Unavailable {
		t.Errorf("expected Unavailable, got %v", got)
	}
}

func TestGRPCTapeAdmin_ContextDeadline_Honored(t *testing.T) {
	_, cleanup := spinUpServer(t)
	defer cleanup()
	admin := newAdmin(t)
	defer admin.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()
	time.Sleep(2 * time.Millisecond) // ensure the deadline has passed
	if err := admin.Redrive(ctx, "run-8"); err == nil {
		t.Error("expected context deadline error")
	}
}
