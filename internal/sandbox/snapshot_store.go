package sandbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// SnapshotStore persists snapshots and their content. Layout:
//
//	<root>/snapshots/<id>/
//	    manifest.json   serialised Snapshot record
//	    tree/           the captured file tree (mirror of upper at capture time)
//
// Content under tree/ is the literal copy of the workspace upper layer at
// snapshot time. Restore copies it back into a new workspace's upper dir.
//
// The store is safe for concurrent use within one process. Cross-process
// safety relies on filesystem rename atomicity.
type SnapshotStore struct {
	root string

	mu    sync.RWMutex
	index map[string]*Snapshot // id → snapshot (loaded lazily)
}

// NewSnapshotStore opens or creates a store rooted at root. The directory
// is created with mode 0700 if absent.
func NewSnapshotStore(root string) (*SnapshotStore, error) {
	if root == "" {
		home, _ := os.UserHomeDir()
		root = filepath.Join(home, ".aiplex", "snapshots")
	}
	if err := os.MkdirAll(filepath.Join(root, "snapshots"), 0o700); err != nil {
		return nil, err
	}
	s := &SnapshotStore{
		root:  root,
		index: make(map[string]*Snapshot),
	}
	if err := s.loadIndex(); err != nil {
		return nil, err
	}
	return s, nil
}

// Root returns the on-disk directory backing the store.
func (s *SnapshotStore) Root() string { return s.root }

// loadIndex scans the snapshots directory and populates the in-memory
// index. Called on Open and after Save (idempotent).
func (s *SnapshotStore) loadIndex() error {
	dir := filepath.Join(s.root, "snapshots")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		manifest := filepath.Join(dir, e.Name(), "manifest.json")
		data, err := os.ReadFile(manifest)
		if err != nil {
			continue
		}
		var snap Snapshot
		if err := json.Unmarshal(data, &snap); err != nil {
			continue
		}
		snap.StoragePath = filepath.Join(dir, e.Name())
		s.index[snap.ID] = &snap
	}
	return nil
}

// Save persists snap and copies the contents of upperDir into the
// store's tree directory for later Restore. The Snapshot's StoragePath
// is updated to reflect where it lives on disk.
func (s *SnapshotStore) Save(snap *Snapshot, upperDir string) error {
	dir := filepath.Join(s.root, "snapshots", snap.ID)
	tree := filepath.Join(dir, "tree")
	if err := os.MkdirAll(tree, 0o700); err != nil {
		return err
	}

	// Copy the upper layer's contents into the snapshot tree.
	if pathExistsCopy(upperDir) {
		if err := copyTree(upperDir, tree); err != nil {
			return fmt.Errorf("save snapshot tree: %w", err)
		}
	}

	snap.StoragePath = dir
	manifest, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, "manifest.json.tmp")
	if err := os.WriteFile(tmp, manifest, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(dir, "manifest.json")); err != nil {
		return err
	}

	s.mu.Lock()
	s.index[snap.ID] = snap
	s.mu.Unlock()
	return nil
}

// Get returns the snapshot record for id, or ErrSnapshotNotFound.
func (s *SnapshotStore) Get(id string) (*Snapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snap, ok := s.index[id]
	if !ok {
		return nil, ErrSnapshotNotFound
	}
	return snap, nil
}

// List returns all snapshots, optionally filtered by workspaceID. Sorted
// most-recent-first.
func (s *SnapshotStore) List(workspaceID string) []*Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Snapshot, 0, len(s.index))
	for _, snap := range s.index {
		if workspaceID != "" && snap.WorkspaceID != workspaceID {
			continue
		}
		out = append(out, snap)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].TakenAt.After(out[j].TakenAt)
	})
	return out
}

// Restore copies the snapshot's tree into dst. Used by Workspace
// FromSnapshotID to fork from a captured state.
func (s *SnapshotStore) Restore(id, dst string) error {
	snap, err := s.Get(id)
	if err != nil {
		return err
	}
	if snap.StoragePath == "" {
		return fmt.Errorf("snapshot %s has no storage path", id)
	}
	tree := filepath.Join(snap.StoragePath, "tree")
	if !pathExistsCopy(tree) {
		return fmt.Errorf("snapshot %s tree missing on disk", id)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	return copyTree(tree, dst)
}

// Delete removes a snapshot. ParentID/child references are not pruned —
// the caller is responsible for ensuring nothing depends on this
// snapshot. Used for explicit GC.
func (s *SnapshotStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap, ok := s.index[id]
	if !ok {
		return ErrSnapshotNotFound
	}
	delete(s.index, id)
	return os.RemoveAll(snap.StoragePath)
}

// Diff returns the diff between two snapshots regardless of their
// position in the chain. Useful for "what changed between yesterday's
// state and now."
func (s *SnapshotStore) Diff(fromID, toID string) (*DiffSummary, error) {
	from, err := s.Get(fromID)
	if err != nil {
		return nil, err
	}
	to, err := s.Get(toID)
	if err != nil {
		return nil, err
	}
	return diffSnapshots(from, to), nil
}

// GC removes snapshots older than maxAge that aren't referenced by any
// kept snapshot's ParentID. Conservative: keeps every leaf, walks
// backwards. Caller should be holding the world steady (no concurrent
// Captures) when running GC.
func (s *SnapshotStore) GC(maxAge time.Duration) (int, error) {
	cutoff := time.Now().Add(-maxAge)
	s.mu.Lock()
	defer s.mu.Unlock()

	keep := make(map[string]bool)
	// Mark all leaves (snapshots not referenced as a parent).
	parents := make(map[string]bool)
	for _, snap := range s.index {
		if snap.ParentID != "" {
			parents[snap.ParentID] = true
		}
	}
	for id, snap := range s.index {
		if !parents[id] || snap.TakenAt.After(cutoff) {
			// Walk back through ParentID, marking all ancestors.
			for cur := snap; cur != nil; {
				keep[cur.ID] = true
				if cur.ParentID == "" {
					break
				}
				cur = s.index[cur.ParentID]
			}
		}
	}

	deleted := 0
	for id, snap := range s.index {
		if keep[id] {
			continue
		}
		if err := os.RemoveAll(snap.StoragePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			continue
		}
		delete(s.index, id)
		deleted++
	}
	return deleted, nil
}
