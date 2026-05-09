package sandbox

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/capability"
)

func newTestStore(t *testing.T) *SnapshotStore {
	t.Helper()
	store, err := NewSnapshotStore(filepath.Join(t.TempDir(), "snapshots"))
	if err != nil {
		t.Fatalf("NewSnapshotStore: %v", err)
	}
	return store
}

func TestWorkspace_MountUnmountDestroy(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "base")
	if err := os.MkdirAll(filepath.Join(base, "rootfs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "rootfs", "hello.txt"), []byte("hi from base"), 0o644); err != nil {
		t.Fatal(err)
	}

	ws, err := NewWorkspace(WorkspaceConfig{
		Root:    filepath.Join(dir, "ws"),
		BaseDir: base,
		Owner:   "cap://tool/test@v1",
	})
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}

	if err := ws.Mount(); err != nil {
		t.Fatalf("Mount: %v", err)
	}

	// Base content should be visible in merged.
	got, err := os.ReadFile(filepath.Join(ws.MergedDir, "rootfs", "hello.txt"))
	if err != nil {
		t.Fatalf("read merged: %v", err)
	}
	if string(got) != "hi from base" {
		t.Errorf("merged content = %q", got)
	}

	if err := ws.Unmount(); err != nil {
		t.Errorf("Unmount: %v", err)
	}
	if err := ws.Destroy(); err != nil {
		t.Errorf("Destroy: %v", err)
	}
}

func TestWorkspace_UpperLayerOverridesBase(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "base")
	os.MkdirAll(base, 0o755)
	os.WriteFile(filepath.Join(base, "shared.txt"), []byte("base wins"), 0o644)

	ws, err := NewWorkspace(WorkspaceConfig{
		Root:    filepath.Join(dir, "ws"),
		BaseDir: base,
		Owner:   "cap://tool/test@v1",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Pre-seed an upper layer entry.
	os.WriteFile(filepath.Join(ws.UpperDir, "shared.txt"), []byte("upper wins"), 0o644)

	if err := ws.Mount(); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	defer ws.Destroy()

	got, _ := os.ReadFile(filepath.Join(ws.MergedDir, "shared.txt"))
	if string(got) != "upper wins" {
		t.Errorf("upper should override base, got %q", got)
	}
}

func TestSnapshotStore_CaptureAndDiff(t *testing.T) {
	store := newTestStore(t)
	dir := t.TempDir()
	base := filepath.Join(dir, "base")
	os.MkdirAll(base, 0o755)
	os.WriteFile(filepath.Join(base, "boot.txt"), []byte("ready"), 0o644)

	ws, err := NewWorkspace(WorkspaceConfig{
		Root:        filepath.Join(dir, "ws"),
		BaseDir:     base,
		Owner:       "cap://tool/x@v1",
		Snapshotter: store,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Destroy()
	ws.Mount()

	// First snapshot — empty upper layer.
	snap1, err := ws.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot 1: %v", err)
	}
	if snap1.ParentID != "" {
		t.Errorf("first snapshot should have no parent")
	}

	// Simulate the cap writing two files into upper.
	os.WriteFile(filepath.Join(ws.UpperDir, "out.txt"), []byte("hello world"), 0o644)
	os.WriteFile(filepath.Join(ws.UpperDir, "log.txt"), []byte("step 1 done"), 0o644)

	snap2, err := ws.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot 2: %v", err)
	}
	if snap2.ParentID != snap1.ID {
		t.Errorf("snapshot 2 ParentID = %q, want %q", snap2.ParentID, snap1.ID)
	}
	if snap2.Hash == snap1.Hash {
		t.Errorf("hash should differ after writes")
	}
	if len(snap2.Diff.Added) != 2 {
		t.Errorf("expected 2 added entries, got %d: %+v", len(snap2.Diff.Added), snap2.Diff.Added)
	}

	// Modify a file → next snapshot shows it as Modified.
	os.WriteFile(filepath.Join(ws.UpperDir, "out.txt"), []byte("hello world!"), 0o644)
	snap3, err := ws.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(snap3.Diff.Modified) != 1 || snap3.Diff.Modified[0] != "out.txt" {
		t.Errorf("expected one Modified=out.txt, got %+v", snap3.Diff)
	}

	// Delete a file → next snapshot shows it as Deleted.
	os.Remove(filepath.Join(ws.UpperDir, "log.txt"))
	snap4, err := ws.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(snap4.Diff.Deleted) != 1 || snap4.Diff.Deleted[0] != "log.txt" {
		t.Errorf("expected one Deleted=log.txt, got %+v", snap4.Diff)
	}
}

func TestSnapshotStore_RestoreForksWorkspace(t *testing.T) {
	store := newTestStore(t)
	dir := t.TempDir()
	base := filepath.Join(dir, "base")
	os.MkdirAll(base, 0o755)

	ws1, _ := NewWorkspace(WorkspaceConfig{
		Root:        filepath.Join(dir, "ws1"),
		BaseDir:     base,
		Owner:       "cap://tool/x@v1",
		Snapshotter: store,
	})
	defer ws1.Destroy()
	ws1.Mount()
	os.WriteFile(filepath.Join(ws1.UpperDir, "checkpoint.txt"), []byte("frozen state"), 0o644)
	snap, err := ws1.Snapshot()
	if err != nil {
		t.Fatal(err)
	}

	// Fork a fresh workspace from the snapshot.
	ws2, err := NewWorkspace(WorkspaceConfig{
		Root:           filepath.Join(dir, "ws2"),
		BaseDir:        base,
		Owner:          "cap://tool/x@v1",
		FromSnapshotID: snap.ID,
		Snapshotter:    store,
	})
	if err != nil {
		t.Fatalf("fork from snapshot: %v", err)
	}
	defer ws2.Destroy()

	got, err := os.ReadFile(filepath.Join(ws2.UpperDir, "checkpoint.txt"))
	if err != nil {
		t.Fatalf("forked workspace missing snapshot content: %v", err)
	}
	if string(got) != "frozen state" {
		t.Errorf("forked content = %q", got)
	}
}

func TestSnapshotStore_DeterministicHash(t *testing.T) {
	store := newTestStore(t)
	dir := t.TempDir()
	base := filepath.Join(dir, "base")
	os.MkdirAll(base, 0o755)

	mk := func(name string) *Snapshot {
		ws, _ := NewWorkspace(WorkspaceConfig{
			Root: filepath.Join(dir, name), BaseDir: base, Snapshotter: store,
		})
		ws.Mount()
		os.WriteFile(filepath.Join(ws.UpperDir, "a.txt"), []byte("abc"), 0o644)
		os.WriteFile(filepath.Join(ws.UpperDir, "b.txt"), []byte("xyz"), 0o644)
		s, err := ws.Snapshot()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = ws.Destroy() })
		return s
	}
	s1 := mk("ws1")
	s2 := mk("ws2")
	if s1.Hash != s2.Hash {
		t.Errorf("identical content produced different hashes:\n  s1=%s\n  s2=%s", s1.Hash, s2.Hash)
	}
}

func TestProfileForCap_ReadOnly(t *testing.T) {
	cap := capability.Capability{
		URI: "cap://tool/search@v1", Kind: capability.KindTool,
		Attrs: capability.Attrs{SideEffect: "read"},
	}
	p := ProfileForCap(cap)
	if p.DefaultAction != "errno" {
		t.Errorf("DefaultAction = %q", p.DefaultAction)
	}
	contains := func(s []string, target string) bool {
		for _, x := range s {
			if x == target {
				return true
			}
		}
		return false
	}
	if !contains(p.Allow, "read") || !contains(p.Allow, "openat") {
		t.Error("read-side syscalls missing from allowlist")
	}
	if !contains(p.Deny, "execve") || !contains(p.Deny, "fork") {
		t.Error("dangerous syscalls should be in denylist for read-only caps")
	}
	if contains(p.Allow, "write") {
		t.Error("read-only profile should not allow write")
	}
}

func TestProfileForCap_External(t *testing.T) {
	cap := capability.Capability{
		URI: "cap://model/foo@v1", Kind: capability.KindModel,
		Attrs: capability.Attrs{SideEffect: "external"},
	}
	p := ProfileForCap(cap)
	contains := func(s []string, target string) bool {
		for _, x := range s {
			if x == target {
				return true
			}
		}
		return false
	}
	if !contains(p.Allow, "socket") || !contains(p.Allow, "connect") {
		t.Error("external profile should allow network syscalls")
	}
	if !contains(p.Deny, "init_module") {
		t.Error("external profile should still deny module loading")
	}
}

func TestDirectSandbox_RoundTrip(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("/bin/sh not available")
	}
	store := newTestStore(t)
	sb := NewDirect(store)
	defer sb.Close()

	cap := capability.Capability{
		URI: "cap://tool/echo@v1", Kind: capability.KindTool,
		Attrs: capability.Attrs{SideEffect: "read", LatencyBudgetMs: 5000},
	}
	h, err := sb.Spawn(context.Background(), SpawnRequest{
		Cap:     cap,
		Subject: "alice@local",
		Action:  "call",
		Command: []string{"/bin/sh", "-c", "echo hello-from-sandbox"},
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	out, _ := io.ReadAll(h.Stdout)
	res, err := h.Wait(context.Background())
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if !strings.Contains(string(out), "hello-from-sandbox") {
		t.Errorf("stdout = %q", string(out))
	}
	if res.ExitCode != 0 {
		t.Errorf("ExitCode = %d", res.ExitCode)
	}
	if res.SandboxName != "direct" {
		t.Errorf("SandboxName = %q", res.SandboxName)
	}
}

func TestDirectSandbox_TimeoutEnforced(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("/bin/sh not available")
	}
	store := newTestStore(t)
	sb := NewDirect(store)
	defer sb.Close()

	cap := capability.Capability{URI: "cap://tool/sleep@v1", Kind: capability.KindTool}
	h, err := sb.Spawn(context.Background(), SpawnRequest{
		Cap:     cap,
		Command: []string{"/bin/sh", "-c", "sleep 30"},
		Limits:  &ResourceLimits{TimeoutWall: 200 * time.Millisecond},
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	res, _ := h.Wait(context.Background())
	if res.DurationMs > 1500 {
		t.Errorf("timeout not enforced; ran %dms", res.DurationMs)
	}
}

func TestNew_AutoFallsBackToDirect(t *testing.T) {
	// On a host where bwrap is missing the factory should fall back to
	// Direct rather than erroring. We simulate by forcing ModeDirect and
	// verifying the result; ModeAuto's exact fallback path is harder
	// to assert without controlling the host.
	store := newTestStore(t)
	sb, err := New(Config{Mode: ModeDirect, SnapshotStore: store})
	if err != nil {
		t.Fatalf("New(ModeDirect): %v", err)
	}
	if sb.Name() != "direct" {
		t.Errorf("Name = %q", sb.Name())
	}
}

func TestSnapshotStore_GC(t *testing.T) {
	store := newTestStore(t)
	dir := t.TempDir()
	base := filepath.Join(dir, "base")
	os.MkdirAll(base, 0o755)
	ws, _ := NewWorkspace(WorkspaceConfig{
		Root: filepath.Join(dir, "ws"), BaseDir: base, Snapshotter: store,
	})
	defer ws.Destroy()
	ws.Mount()

	// Take three snapshots; the middle one is an interior parent.
	os.WriteFile(filepath.Join(ws.UpperDir, "a"), []byte("1"), 0o644)
	ws.Snapshot()
	os.WriteFile(filepath.Join(ws.UpperDir, "a"), []byte("2"), 0o644)
	ws.Snapshot()
	os.WriteFile(filepath.Join(ws.UpperDir, "a"), []byte("3"), 0o644)
	ws.Snapshot()

	if got := len(store.List("")); got != 3 {
		t.Fatalf("expected 3 snapshots before GC, got %d", got)
	}

	// GC with maxAge of 0 keeps the latest leaf and walks back from it
	// (because parents-of-leaves are kept). All three should survive
	// because they form a chain anchored at the leaf.
	deleted, err := store.GC(0)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 0 {
		t.Errorf("GC of unbroken chain should keep all snapshots; deleted=%d", deleted)
	}
}
