package api

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	tape "github.com/vamsiramakrishnan/durable-agents/tape/sdk/go"
	pb "github.com/vamsiramakrishnan/durable-agents/tape/sdk/go/tapepb"
)

// GRPCTapeAdmin is the real implementation of TapeAdmin: it dials a
// running tape-server via gRPC and translates operator actions to the
// matching Tape RPCs.
//
// Mapping (verified against tape/proto/tape.proto):
//
//	Redrive    → ResumeRun           (re-acquires lease, marks RUNNING,
//	                                  reactors pick up on next tick)
//	Reconcile  → ResumeRun           (nudge — reconciler loop processes
//	                                  UNKNOWN effects within ~1 tick)
//	Cancel     → EndRun(CANCELLED)   (cooperative; agent checks at next
//	                                  model/tool boundary)
//	Signal     → SendSignal          (resumes a parked run on the named gate)
//	Compensate → ResumeRun           (nudge — obligation drainer processes
//	                                  PENDING obligations within ~1 tick)
//
// Each call uses a short per-RPC timeout. The dialed connection is
// reused for the lifetime of the handler. On any RPC failure we surface
// the gRPC status verbatim so callers see Tape's own error semantics.
type GRPCTapeAdmin struct {
	client *tape.Client
	mu     sync.Mutex
	dialed bool
	url    string
}

// NewGRPCTapeAdmin reads TAPE_URL and dials lazily on first use. When
// the env var is empty the constructor returns (nil, false) and
// main.go falls back to NoopTapeAdmin — local dev paths keep working
// without Tape.
func NewGRPCTapeAdmin() (*GRPCTapeAdmin, bool) {
	url := os.Getenv("TAPE_URL")
	if url == "" {
		return nil, false
	}
	return &GRPCTapeAdmin{url: url}, true
}

// Close shuts down the underlying gRPC connection.
func (g *GRPCTapeAdmin) Close() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.client != nil {
		return g.client.Close()
	}
	return nil
}

func (g *GRPCTapeAdmin) ensure() (*tape.Client, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.dialed && g.client != nil {
		return g.client, nil
	}
	// Auth=false because AIPlex talks to tape-server in-cluster, mTLS
	// terminated by the mesh. For Cloud Run / IAM-protected endpoints
	// the caller can override TAPE_URL to a `tapes://` URL and the
	// Tape SDK picks up Google ID tokens automatically.
	c, err := tape.Dial(g.url, tape.Options{Auth: false, DialOpts: []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}})
	if err != nil {
		return nil, fmt.Errorf("dial tape-server at %s: %w", g.url, err)
	}
	g.client = c
	g.dialed = true
	return c, nil
}

// operatorLeaseTTLMs — long enough that a reactor tick can pick up
// the kicked run before the lease lapses, short enough that an
// operator-triggered redrive of a dead pod gets reclaimed.
const operatorLeaseTTLMs = 60_000

const operatorLeaseOwner = "aiplex-operator"

func (g *GRPCTapeAdmin) Redrive(ctx context.Context, runID string) error {
	c, err := g.ensure()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err = c.PB().ResumeRun(ctx, &pb.ResumeRunRequest{
		RunId:      runID,
		LeaseOwner: operatorLeaseOwner,
		LeaseTtlMs: operatorLeaseTTLMs,
	})
	return err
}

// Reconcile uses the same ResumeRun nudge as Redrive — the reconciler
// reactor scans UNKNOWN effects per-run on its tick interval (~250 ms
// default), so a fresh ResumeRun is enough to schedule the work for
// immediate attention. AIPlex doesn't reimplement reactor logic.
func (g *GRPCTapeAdmin) Reconcile(ctx context.Context, runID string) error {
	return g.Redrive(ctx, runID)
}

func (g *GRPCTapeAdmin) Cancel(ctx context.Context, runID, reason string) error {
	c, err := g.ensure()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	detail := fmt.Sprintf(`{"reason":%q,"actor":"aiplex-operator"}`, reason)
	_, err = c.PB().EndRun(ctx, &pb.EndRunRequest{
		RunId:       runID,
		Status:      pb.RunStatus_RUN_STATUS_CANCELLED,
		DetailJson:  detail,
	})
	return err
}

func (g *GRPCTapeAdmin) Signal(ctx context.Context, runID, gateName, resolutionJSON string) error {
	c, err := g.ensure()
	if err != nil {
		return err
	}
	if gateName == "" {
		return errors.New("signal: gate_name required")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err = c.PB().SendSignal(ctx, &pb.SendSignalRequest{
		RunId:          runID,
		GateName:       gateName,
		ResolutionJson: resolutionJSON,
	})
	return err
}

// Compensate is also a ResumeRun nudge — the compensation reactor
// processes PENDING obligations per-run on each tick.
func (g *GRPCTapeAdmin) Compensate(ctx context.Context, runID string) error {
	return g.Redrive(ctx, runID)
}
