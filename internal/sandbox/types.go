// Package sandbox isolates capability invocations.
//
// Every cap call (a `kind=tool` invocation, a `kind=skill` exec, a
// `kind=workflow` step that spawns a process) runs inside a Sandbox whose
// identity, syscall surface, filesystem, network, and resource budget are
// derived from the cap's attrs and the cap claim's constraints. This is
// what gives AIPlex its "blast radius = one cap" guarantee:
//
//   - process identity: per-cap UID, fresh PID/IPC namespaces, dropped capabilities
//   - syscall surface: kind-specific seccomp-bpf profile (no exec for read-only,
//     no fs writes for external, etc.)
//   - filesystem: layered Workspace (read-only base + per-invocation upper
//     layer); writes never escape; snapshottable for verifiable audit
//   - network: scoped to declared egress whitelist; default-deny otherwise
//   - resources: cgroups v2 caps from cap.attrs.cost_tier + claim constraints
//   - lifetime: setrlimit + ctx deadline derived from latency_budget_ms
//
// See design/26-sandbox-and-snapshots.md.
//
// The Sandbox interface is platform-neutral. Concrete implementations:
//
//   - Direct (any OS): no isolation, used as fallback. Workspace + Snapshot
//     still apply, so audit semantics are uniform; isolation is what's missing.
//   - Bwrap (Linux): bubblewrap-based namespacing + seccomp + cgroups. The
//     production-grade local-mode implementation.
//   - K8s (planned): per-pod PodSecurityContext + NetworkPolicy + Cilium L7.
//     Production cluster mode.
//
// AutoDetect picks the strongest implementation available on the host, so
// `aiplex up` on a Linux laptop gets bwrap automatically; on macOS or in a
// container without bwrap it falls back to Direct with a warning.
package sandbox

import (
	"context"
	"io"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/capability"
)

// Sandbox isolates and runs capability invocations. One Sandbox instance
// can spawn many concurrent invocations; each Spawn call returns its own
// Handle with an independent workspace and lifecycle.
type Sandbox interface {
	// Name returns the implementation identifier ("bwrap", "direct", …).
	Name() string

	// Spawn launches one cap invocation. The Handle's Done channel resolves
	// when the process exits; cancellation propagates via context.
	Spawn(ctx context.Context, req SpawnRequest) (*Handle, error)

	// Close releases any global resources (cached overlays, daemon connections).
	Close() error
}

// SpawnRequest describes one invocation. The Sandbox derives its security
// posture from req.Cap.Attrs (long-lived metadata) and req.Claim.Constraints
// (per-grant runtime knobs).
type SpawnRequest struct {
	// Cap is the capability being invoked.
	Cap capability.Capability

	// Claim is the cap claim from the caller's JWT — carries
	// per-(user,agent,cap) constraints like rate limits and budgets.
	Claim capability.Cap

	// Subject is the human delegating (sub claim).
	Subject string

	// CallerSpiffe is the SPIFFE ID of the agent invoking, for audit.
	CallerSpiffe string

	// Action is the cap action being invoked ("call", "complete", "write", …).
	Action string

	// Command is the argv to exec inside the sandbox. argv[0] must be an
	// absolute path that exists in the prepared rootfs.
	Command []string

	// Env is the environment for the sandboxed process. By default the
	// Sandbox starts with an empty environment plus PATH=/usr/bin:/bin.
	Env []string

	// Input is fed to the process's stdin. nil means /dev/null.
	Input io.Reader

	// Workspace gives the cap a filesystem to operate on. nil means create
	// a fresh ephemeral workspace that's destroyed on Handle.Close.
	Workspace *Workspace

	// Mounts adds extra read-only or read-write bind mounts beyond the
	// kind defaults. Use sparingly — every mount widens blast radius.
	Mounts []MountSpec

	// AllowedEgress is the list of host:port pairs (or CIDR for raw) the
	// sandboxed process may reach over the network. Empty means no network
	// at all (default-deny).
	AllowedEgress []string

	// Limits override the values derived from cap.attrs / claim.constraints.
	// Mostly for tests; production code should let the Sandbox derive these.
	Limits *ResourceLimits
}

// MountSpec describes one filesystem entry available to the sandbox.
type MountSpec struct {
	HostPath  string
	GuestPath string
	ReadOnly  bool
}

// ResourceLimits caps the sandboxed process's footprint. Implementations
// translate to cgroups v2 (Linux) or rlimits (everywhere). Zero values
// mean "use the kind's default."
type ResourceLimits struct {
	CPUShares      uint64        // 1024 = baseline; 512 = half; 2048 = double
	MemoryBytes    uint64        // hard cap; OOM-killed beyond
	IOWeight       uint64        // 1..1000
	PidsMax        int           // process count cap
	TimeoutWall    time.Duration // wall-clock deadline (also enforced via ctx)
	OpenFilesMax   int           // RLIMIT_NOFILE
	StackBytesMax  int64         // RLIMIT_STACK
}

// Handle controls one running invocation.
type Handle struct {
	// InvocationID is "inv-<hex>" — referenced from receipts so the audit
	// chain can fetch the workspace + snapshot for verification.
	InvocationID string

	// Started is when Spawn returned (post-fork, pre-output).
	Started time.Time

	// Workspace is the filesystem the process sees. The same Workspace
	// can be reused across invocations (e.g. successive workflow steps
	// or a long-lived agent's persistent state).
	Workspace *Workspace

	// Stdout/Stderr stream output. Closed when the process exits.
	Stdout io.ReadCloser
	Stderr io.ReadCloser

	// Done resolves with the exit error (nil on clean exit).
	Done <-chan error

	// PID is the host PID of the sandboxed process. 0 if unavailable.
	PID int

	// cancel is called by Wait + Close to terminate cleanly.
	cancel func() error

	// snapshot captures the workspace at the current moment.
	snapshot func() (*Snapshot, error)

	// Result is populated by the implementation after Done fires.
	Result *Result
}

// Result is what came back from a completed invocation. Receipts cite this.
type Result struct {
	ExitCode       int
	StartedAt      time.Time
	FinishedAt     time.Time
	DurationMs     int64
	StdoutBytes    int64
	StderrBytes    int64
	PreSnapshotID  string // workspace state before the invocation started
	PostSnapshotID string // workspace state when the invocation finished
	Reason         string // OK | timeout | killed | sandbox-violation | …
	SandboxName    string
}

// Wait blocks until the invocation finishes (or ctx fires) and returns
// the Result. Callers should always Wait before Close to ensure the
// post-snapshot is captured.
func (h *Handle) Wait(ctx context.Context) (*Result, error) {
	select {
	case err := <-h.Done:
		if h.Result == nil {
			h.Result = &Result{}
		}
		h.Result.FinishedAt = time.Now()
		h.Result.DurationMs = h.Result.FinishedAt.Sub(h.Started).Milliseconds()
		if err != nil && h.Result.Reason == "" {
			h.Result.Reason = err.Error()
		}
		return h.Result, err
	case <-ctx.Done():
		_ = h.cancel()
		return h.Result, ctx.Err()
	}
}

// Snapshot captures the workspace state at the current point. Returns the
// snapshot record (containing the content hash + diff vs. the previous
// snapshot). Used by the workflow executor to capture between-step state
// and by the receipt emitter for audit.
func (h *Handle) Snapshot() (*Snapshot, error) {
	if h.snapshot == nil {
		return nil, ErrSnapshotsUnsupported
	}
	return h.snapshot()
}

// Close terminates the process if still running and releases the workspace
// (unless it was a persistent workspace owned by another caller). Always
// safe to call multiple times.
func (h *Handle) Close() error {
	if h.cancel == nil {
		return nil
	}
	return h.cancel()
}
