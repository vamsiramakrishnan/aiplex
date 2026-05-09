package sandbox

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// A Workspace is the filesystem a sandboxed cap invocation sees. It is
// always layered: a read-only Base provides the cap's binary + libs (and
// any namespace-specific data), and a writable Upper captures everything
// the cap writes during its run. The Merged view exposes the union of
// both.
//
// On Linux we use overlayfs (or fuse-overlayfs in user namespaces) for
// the upper layer. Elsewhere we fall back to copy-on-spawn — slower but
// portable. The Snapshot API is uniform across both.
//
// Workspaces have three lifetime patterns:
//
//   - Ephemeral (default): created on Spawn, destroyed on Handle.Close.
//     Scratch state lives only for the invocation. Used for stateless
//     tool calls.
//
//   - Persistent: created once, reused across invocations. Used for
//     stateful agents and memory namespaces — the workspace IS the
//     long-lived state. Snapshots are how you audit and roll back.
//
//   - Forked: created from an existing snapshot. Used for replay,
//     what-if exploration, and "resume the agent from yesterday's
//     state."
type Workspace struct {
	ID         string    // workspace-<hex>
	BaseDir    string    // read-only root the cap mounts at /
	UpperDir   string    // CoW upper layer (writes land here)
	WorkDir    string    // overlayfs scratch (Linux only)
	MergedDir  string    // the path the cap process actually sees as /
	Owner      string    // cap URI that owns this workspace (for GC + ACL)
	Persistent bool      // true = survive Handle.Close
	CreatedAt  time.Time
	UpdatedAt  time.Time

	mu          sync.Mutex
	overlay     overlayDriver // drives mount/unmount per platform
	root        string        // top-level dir for this workspace's storage
	snapshotter *snapshotter
}

// WorkspaceConfig is what NewWorkspace consumes. Defaults are sensible for
// ephemeral tool invocations.
type WorkspaceConfig struct {
	// Root is the directory that holds all workspaces. Defaults to
	// $XDG_DATA_HOME/aiplex/workspaces or ~/.aiplex/workspaces.
	Root string

	// BaseDir is the read-only filesystem the cap sees. Required —
	// callers (e.g. the deploy engine) are expected to assemble this
	// from the cap's image / kind defaults.
	BaseDir string

	// Owner identifies which cap this workspace belongs to.
	Owner string

	// Persistent keeps the workspace alive after the spawning Handle
	// is closed. Used for stateful agents.
	Persistent bool

	// FromSnapshotID, if non-empty, forks the new workspace from a
	// previously captured snapshot. The forked workspace's upper layer
	// starts as a copy of the snapshot's contents.
	FromSnapshotID string

	// Snapshotter (optional) — provides snapshot capture/restore. nil
	// means snapshots are unsupported on this workspace.
	Snapshotter *SnapshotStore
}

// NewWorkspace creates and prepares a workspace. The returned Workspace's
// MergedDir is bind-mountable but not yet mounted; call Mount before
// passing to a sandbox.
func NewWorkspace(cfg WorkspaceConfig) (*Workspace, error) {
	if cfg.BaseDir == "" {
		return nil, fmt.Errorf("workspace: BaseDir required")
	}
	root := cfg.Root
	if root == "" {
		home, _ := os.UserHomeDir()
		root = filepath.Join(home, ".aiplex", "workspaces")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("workspace: create root: %w", err)
	}

	id := "workspace-" + randHex(8)
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}

	ws := &Workspace{
		ID:         id,
		BaseDir:    cfg.BaseDir,
		UpperDir:   filepath.Join(dir, "upper"),
		WorkDir:    filepath.Join(dir, "work"),
		MergedDir:  filepath.Join(dir, "merged"),
		Owner:      cfg.Owner,
		Persistent: cfg.Persistent,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
		root:       dir,
		overlay:    newOverlayDriver(),
	}

	for _, d := range []string{ws.UpperDir, ws.WorkDir, ws.MergedDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}

	// Fork from a snapshot if requested. The snapshot's content is
	// copied into the new upper layer; the cap inherits that state.
	if cfg.FromSnapshotID != "" {
		if cfg.Snapshotter == nil {
			return nil, fmt.Errorf("workspace: FromSnapshotID requires Snapshotter")
		}
		if err := cfg.Snapshotter.Restore(cfg.FromSnapshotID, ws.UpperDir); err != nil {
			return nil, fmt.Errorf("workspace: restore from %s: %w", cfg.FromSnapshotID, err)
		}
	}

	if cfg.Snapshotter != nil {
		ws.snapshotter = newSnapshotter(ws, cfg.Snapshotter)
	}

	return ws, nil
}

// Mount makes MergedDir reflect Base ∪ Upper. On Linux this is overlayfs;
// elsewhere it copies Base into MergedDir then re-applies Upper on top.
// Idempotent.
func (w *Workspace) Mount() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.overlay.Mount(w); err != nil {
		return fmt.Errorf("workspace mount: %w", err)
	}
	w.UpdatedAt = time.Now()
	return nil
}

// Unmount tears down the merged view. Upper data persists if Persistent.
func (w *Workspace) Unmount() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.overlay.Unmount(w)
}

// Destroy removes the workspace from disk. Non-persistent workspaces
// destroy themselves on Handle.Close; this is for explicit cleanup.
func (w *Workspace) Destroy() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.overlay.Unmount(w); err != nil {
		// best-effort; carry on
		_ = err
	}
	return os.RemoveAll(w.root)
}

// Snapshot captures the current upper layer (the cap's writes) as a
// content-addressed Snapshot. Atomic with respect to the cap process —
// you usually call this after Wait, but mid-run snapshots are valid if
// the cap isn't writing concurrently.
func (w *Workspace) Snapshot() (*Snapshot, error) {
	if w.snapshotter == nil {
		return nil, ErrSnapshotsUnsupported
	}
	return w.snapshotter.Capture()
}

// LatestSnapshot returns the most recently captured snapshot for this
// workspace, or nil if none have been taken. Useful for the deploy
// engine to record pre/post snapshot pairs in receipts.
func (w *Workspace) LatestSnapshot() *Snapshot {
	if w.snapshotter == nil {
		return nil
	}
	return w.snapshotter.Latest()
}

// SetEphemeral marks the workspace for cleanup when its owning Handle
// closes. Used internally by Sandbox implementations.
func (w *Workspace) setEphemeral() { w.Persistent = false }

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "00000000"
	}
	return hex.EncodeToString(b)
}
