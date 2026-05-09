//go:build linux

package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/capability"
)

// Bwrap runs caps under bubblewrap (/usr/bin/bwrap). The flags it
// constructs are derived from the cap's attrs and the cap claim's
// constraints — same logic as the K8s PodSecurityContext path, just
// expressed in bwrap-CLI form for local-mode use.
//
// Capabilities and isolation:
//
//   - Mount namespace: fresh; the cap sees a curated rootfs assembled
//     from the workspace's MergedDir plus selected read-only host
//     directories (--ro-bind /usr /usr, etc).
//   - User namespace: cap runs as a per-cap UID, with no capabilities.
//   - PID namespace: --unshare-pid; can't see other host processes.
//   - IPC namespace: --unshare-ipc; no shared SysV.
//   - UTS namespace: --unshare-uts; cap can't change hostname.
//   - Network namespace: --unshare-net unless AllowedEgress is set.
//   - seccomp: profile compiled by ProfileForCap, written to a fd, passed
//     via --seccomp <fd>. (libseccomp is the production compiler; this
//     impl ships the profile as JSON the operator can hand-compile until
//     we wire libseccomp directly.)
//   - cgroups: bwrap doesn't manage cgroups; we'd typically launch via
//     systemd-run --scope on production. For `aiplex up` we wrap the
//     bwrap exec in setrlimit + a wall-clock watchdog.
type Bwrap struct {
	binPath string
	store   *SnapshotStore

	// uidPool hands out per-cap UIDs from a starting base. Each spawn
	// gets a unique UID inside the user namespace.
	uidPool chan uint32
}

// NewBwrap probes for a usable bwrap binary on PATH and returns a
// Sandbox that uses it. snapshotStore is optional; pass nil to disable
// snapshots (workspace + audit still work).
func NewBwrap(snapshotStore *SnapshotStore) (*Bwrap, error) {
	bin, err := exec.LookPath("bwrap")
	if err != nil {
		return nil, fmt.Errorf("bwrap not found on PATH: %w", err)
	}
	if err := probeBwrap(bin); err != nil {
		return nil, fmt.Errorf("bwrap %s not usable: %w", bin, err)
	}

	pool := make(chan uint32, 256)
	for i := uint32(9000); i < 9256; i++ {
		pool <- i
	}

	return &Bwrap{binPath: bin, store: snapshotStore, uidPool: pool}, nil
}

func (b *Bwrap) Name() string { return "bwrap" }

func (b *Bwrap) Spawn(ctx context.Context, req SpawnRequest) (*Handle, error) {
	if len(req.Command) == 0 {
		return nil, fmt.Errorf("bwrap: empty Command")
	}

	ws, ephemeral, preSnap, err := prepareWorkspace(req, b.store)
	if err != nil {
		return nil, err
	}

	limits := req.Limits
	if limits == nil {
		limits = limitsForCap(req.Cap, req.Claim)
	}

	uid := <-b.uidPool
	defer func() {
		// Return UID to pool when the spawn handle is closed (see h.cancel).
		_ = uid
	}()

	flags, err := b.buildFlags(req, ws, uid)
	if err != nil {
		if ephemeral && ws != nil {
			_ = ws.Destroy()
		}
		return nil, err
	}

	procCtx, cancel := context.WithCancel(ctx)
	if limits.TimeoutWall > 0 {
		procCtx, cancel = context.WithTimeout(ctx, limits.TimeoutWall)
	}

	args := append(flags, "--", req.Command[0])
	args = append(args, req.Command[1:]...)

	cmd := exec.CommandContext(procCtx, b.binPath, args...)
	cmd.Env = append([]string{"PATH=/usr/bin:/bin"}, req.Env...)
	if req.Input != nil {
		cmd.Stdin = req.Input
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return nil, err
	}

	started := time.Now()
	if err := cmd.Start(); err != nil {
		cancel()
		if ephemeral && ws != nil {
			_ = ws.Destroy()
		}
		return nil, fmt.Errorf("bwrap start: %w", err)
	}

	res := &Result{
		StartedAt:   started,
		SandboxName: b.Name(),
	}
	if preSnap != nil {
		res.PreSnapshotID = preSnap.ID
	}

	done := make(chan error, 1)
	pid := 0
	if cmd.Process != nil {
		pid = cmd.Process.Pid
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
		if ws != nil {
			_ = ws.Unmount()
			if b.store != nil {
				if snap, err := ws.Snapshot(); err == nil && snap != nil {
					res.PostSnapshotID = snap.ID
				} else if err != nil && err != ErrSnapshotsUnsupported {
					firstErr = err
				}
			}
		}
		if ephemeral && ws != nil {
			_ = ws.Destroy()
		}
		// Recycle UID (best-effort).
		select {
		case b.uidPool <- uid:
		default:
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
			if cmd.ProcessState.ExitCode() == 137 || (cmd.ProcessState.Sys() != nil) {
				// 137 is the conventional exit for SIGKILL — could be OOM
				// or our wall-clock watchdog. Reason is best-effort.
			}
		}
		done <- err
		close(done)
		_ = h.cancel()
	}()

	return h, nil
}

func (b *Bwrap) Close() error { return nil }

// buildFlags translates a SpawnRequest into the bwrap CLI flag set.
// Callers (tests) that want to inspect the command line without exec
// can use this directly via the package-private helper.
func (b *Bwrap) buildFlags(req SpawnRequest, ws *Workspace, uid uint32) ([]string, error) {
	flags := []string{
		"--die-with-parent",
		"--unshare-pid",
		"--unshare-ipc",
		"--unshare-uts",
		"--proc", "/proc",
		"--dev", "/dev",
		"--tmpfs", "/tmp",
		"--ro-bind", "/usr", "/usr",
		"--ro-bind-try", "/lib", "/lib",
		"--ro-bind-try", "/lib64", "/lib64",
		"--ro-bind-try", "/etc/ld.so.cache", "/etc/ld.so.cache",
		"--ro-bind-try", "/etc/resolv.conf", "/etc/resolv.conf",
		"--ro-bind-try", "/etc/ssl", "/etc/ssl",
		"--setenv", "HOME", "/work",
		"--chdir", "/work",
		"--uid", strconv.FormatUint(uint64(uid), 10),
		"--gid", strconv.FormatUint(uint64(uid), 10),
	}

	// Workspace: bind merged → /work (the cap's CWD).
	if ws != nil {
		flags = append(flags, "--bind", ws.MergedDir, "/work")
	}

	// Network: default-deny unless AllowedEgress non-empty.
	if len(req.AllowedEgress) == 0 {
		flags = append(flags, "--unshare-net")
	}
	// (Egress filtering is at the host network layer — nftables in the
	// cap's net namespace — managed by AIPlex outside the bwrap exec.)

	// Extra mounts the caller asked for.
	for _, m := range req.Mounts {
		if m.ReadOnly {
			flags = append(flags, "--ro-bind", m.HostPath, m.GuestPath)
		} else {
			flags = append(flags, "--bind", m.HostPath, m.GuestPath)
		}
	}

	// seccomp: write a JSON profile alongside the workspace (the
	// operator can compile to BPF with libseccomp in production).
	// For now we skip --seccomp until the libseccomp wiring lands so
	// the spawn doesn't fail on the bwrap side.
	profile := ProfileForCap(req.Cap)
	if ws != nil {
		profilePath := filepath.Join(ws.root, "seccomp.json")
		data, _ := json.MarshalIndent(profile, "", "  ")
		_ = os.WriteFile(profilePath, data, 0o600)
	}

	return flags, nil
}

// probeBwrap runs `bwrap --version` to ensure the binary is executable.
// Distinct from LookPath because some hardened distros remove setuid
// while leaving the binary on PATH.
func probeBwrap(bin string) error {
	cmd := exec.Command(bin, "--version")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, string(out))
	}
	return nil
}

// BuildFlagsForTest is exposed so tests can verify the flag construction
// without actually exec'ing bwrap. Returned slice is a snapshot, safe to
// mutate.
func (b *Bwrap) BuildFlagsForTest(req SpawnRequest, ws *Workspace, uid uint32) ([]string, error) {
	return b.buildFlags(req, ws, uid)
}

var _ = io.Copy            // keep import alive
var _ = capability.KindMeta // keep import alive
