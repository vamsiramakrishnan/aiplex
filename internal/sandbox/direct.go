package sandbox

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/capability"
)

// Direct is the no-isolation Sandbox: it just exec's the cap. Workspace
// + Snapshot still work, so audit semantics are preserved; only the
// runtime isolation is missing. Used as fallback when no stronger
// implementation is available, and in tests where forking real bwrap
// processes is overkill.
//
// The user-visible warning when Direct is in play comes from the AutoDetect
// factory, not from here.
type Direct struct {
	store *SnapshotStore
}

// NewDirect creates a Direct sandbox. snapshotStore is optional — pass
// nil to disable snapshots.
func NewDirect(snapshotStore *SnapshotStore) *Direct {
	return &Direct{store: snapshotStore}
}

func (d *Direct) Name() string { return "direct" }

func (d *Direct) Spawn(ctx context.Context, req SpawnRequest) (*Handle, error) {
	if len(req.Command) == 0 {
		return nil, fmt.Errorf("direct: empty Command")
	}

	// Manage the workspace.
	ws, ephemeral, preSnap, err := prepareWorkspace(req, d.store)
	if err != nil {
		return nil, err
	}

	limits := req.Limits
	if limits == nil {
		limits = limitsForCap(req.Cap, req.Claim)
	}

	procCtx, cancel := context.WithCancel(ctx)
	if limits.TimeoutWall > 0 {
		procCtx, cancel = context.WithTimeout(ctx, limits.TimeoutWall)
	}

	cmd := exec.CommandContext(procCtx, req.Command[0], req.Command[1:]...)
	cmd.Env = append([]string{"PATH=/usr/bin:/bin"}, req.Env...)
	if ws != nil {
		cmd.Dir = ws.MergedDir
	}
	if req.Input != nil {
		cmd.Stdin = req.Input
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}

	started := time.Now()
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("direct start: %w", err)
	}

	done := make(chan error, 1)
	pid := 0
	if cmd.Process != nil {
		pid = cmd.Process.Pid
	}

	res := &Result{
		StartedAt:   started,
		SandboxName: d.Name(),
	}
	if preSnap != nil {
		res.PreSnapshotID = preSnap.ID
	}

	h := &Handle{
		InvocationID: "inv-" + randHex(8),
		Started:      started,
		Workspace:    ws,
		Stdout:       stdout,
		Stderr:       stderr,
		Done:         done,
		PID:          pid,
		Result:       res,
	}

	var once sync.Once
	cleanup := func() error {
		var firstErr error
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		cancel()
		// Take post-snapshot before unmounting if the workspace supports it.
		if ws != nil && d.store != nil {
			_ = ws.Unmount()
			if snap, err := ws.Snapshot(); err == nil && snap != nil {
				res.PostSnapshotID = snap.ID
			} else if err != nil && err != ErrSnapshotsUnsupported {
				firstErr = err
			}
		} else if ws != nil {
			_ = ws.Unmount()
		}
		if ephemeral && ws != nil {
			_ = ws.Destroy()
		}
		return firstErr
	}
	h.cancel = func() error {
		var err error
		once.Do(func() { err = cleanup() })
		return err
	}
	h.snapshot = func() (*Snapshot, error) {
		if ws == nil {
			return nil, ErrSnapshotsUnsupported
		}
		return ws.Snapshot()
	}

	go func() {
		err := cmd.Wait()
		if cmd.ProcessState != nil {
			res.ExitCode = cmd.ProcessState.ExitCode()
		}
		done <- err
		close(done)
		// Always run cleanup once the process exits.
		_ = h.cancel()
	}()

	return h, nil
}

func (d *Direct) Close() error { return nil }

// prepareWorkspace handles the ephemeral / persistent / forked decisions
// uniformly across Sandbox implementations. Returns (workspace, ephemeral,
// pre-snapshot, error). Ephemeral=true means the caller's cleanup should
// Destroy the workspace.
func prepareWorkspace(req SpawnRequest, store *SnapshotStore) (*Workspace, bool, *Snapshot, error) {
	if req.Workspace != nil {
		// Caller provided a workspace; respect their lifecycle.
		if err := req.Workspace.Mount(); err != nil {
			return nil, false, nil, err
		}
		var pre *Snapshot
		if req.Workspace.snapshotter != nil {
			snap, err := req.Workspace.Snapshot()
			if err == nil {
				pre = snap
			}
		}
		return req.Workspace, false, pre, nil
	}

	// No workspace requested. If we have a snapshot store, build an
	// ephemeral one anyway — it gives us pre/post snapshot pairing for
	// audit even if the cap doesn't otherwise need filesystem state.
	if store == nil {
		return nil, false, nil, nil
	}

	base := os.TempDir() // ephemeral caps don't need a meaningful base
	ws, err := NewWorkspace(WorkspaceConfig{
		BaseDir:     base,
		Owner:       req.Cap.URI,
		Persistent:  false,
		Snapshotter: store,
	})
	if err != nil {
		return nil, false, nil, err
	}
	if err := ws.Mount(); err != nil {
		_ = ws.Destroy()
		return nil, false, nil, err
	}
	// Capture an empty pre-snapshot for completeness.
	pre, _ := ws.Snapshot()
	return ws, true, pre, nil
}

// limitsForCap returns the resource limits a Sandbox should enforce
// when neither the operator nor the test overrides them.
func limitsForCap(cap capability.Capability, claim capability.Cap) *ResourceLimits {
	// Defaults are intentionally conservative for `aiplex up`. Production
	// installs would dial these up via cap.Attrs / claim.Constraints.
	out := &ResourceLimits{
		CPUShares:     1024,
		MemoryBytes:   256 * 1024 * 1024, // 256 MiB
		IOWeight:      100,
		PidsMax:       64,
		TimeoutWall:   30 * time.Second,
		OpenFilesMax:  256,
		StackBytesMax: 8 * 1024 * 1024,
	}
	if cap.Attrs.LatencyBudgetMs > 0 {
		out.TimeoutWall = time.Duration(cap.Attrs.LatencyBudgetMs) * time.Millisecond
	}
	if v, ok := claim.Constraints["max_memory_bytes"].(float64); ok && v > 0 {
		out.MemoryBytes = uint64(v)
	}
	if v, ok := claim.Constraints["timeout_seconds"].(float64); ok && v > 0 {
		out.TimeoutWall = time.Duration(v) * time.Second
	}
	return out
}

// drain is a small helper for tests that want the full output.
func drain(r io.Reader) (string, int64) {
	if r == nil {
		return "", 0
	}
	data, _ := io.ReadAll(r)
	return string(data), int64(len(data))
}
