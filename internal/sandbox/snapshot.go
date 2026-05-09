package sandbox

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// A Snapshot captures the writable state of a workspace at a point in
// time. It is content-addressed: identical workspace state produces
// identical snapshot ID. Two properties matter:
//
//   1. Cryptographically verifiable. The Hash field is sha256 over the
//      canonicalised file tree (path, mode, content-hash) ordered
//      deterministically. Receipts cite this hash; an auditor can fetch
//      the snapshot and verify it matches.
//
//   2. Diffable. DiffSummary tells the auditor what changed since the
//      previous snapshot — the cap's externally-visible side effects
//      on the filesystem.
//
// Together: every cap invocation that touches the filesystem leaves a
// signed receipt referencing pre/post snapshot hashes. The auditor reads
// the diff and either verifies "the cap did exactly what its claim said"
// or surfaces the deviation.
type Snapshot struct {
	ID          string       `json:"id"`           // snap-<8-hex of Hash>
	WorkspaceID string       `json:"workspace_id"`
	ParentID    string       `json:"parent_id,omitempty"` // previous snapshot in the chain
	TakenAt     time.Time    `json:"taken_at"`
	Hash        string       `json:"hash"` // sha256 of canonical tree
	StoragePath string       `json:"-"`    // where the snapshot's files live (server-only)
	Files       []FileEntry  `json:"files"`
	Diff        *DiffSummary `json:"diff,omitempty"`
}

// FileEntry is one entry in the snapshot's manifest. Mode lets the
// auditor reason about executable bits and symlink targets.
type FileEntry struct {
	Path        string `json:"path"`         // workspace-relative
	Mode        uint32 `json:"mode"`         // os.FileMode bits
	Size        int64  `json:"size"`
	ContentHash string `json:"content_hash"` // sha256 of contents (empty for dirs/symlinks)
	LinkTarget  string `json:"link_target,omitempty"`
}

// DiffSummary describes the change between this snapshot and its parent.
type DiffSummary struct {
	Added      []string `json:"added,omitempty"`
	Modified   []string `json:"modified,omitempty"`
	Deleted    []string `json:"deleted,omitempty"`
	BytesAdded int64    `json:"bytes_added"`
	BytesRemoved int64  `json:"bytes_removed"`
}

// snapshotter binds a Workspace to its persistent SnapshotStore. It
// owns the chain of snapshots (Latest, Parent, etc).
type snapshotter struct {
	mu        sync.Mutex
	workspace *Workspace
	store     *SnapshotStore
	latest    *Snapshot
}

func newSnapshotter(w *Workspace, s *SnapshotStore) *snapshotter {
	return &snapshotter{workspace: w, store: s}
}

// Capture takes a snapshot of the workspace's upper layer. The new
// snapshot is parented to the previously-captured one (if any), so the
// resulting chain represents the cap's lifetime of writes.
func (s *snapshotter) Capture() (*Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	files, err := walkTree(s.workspace.UpperDir)
	if err != nil {
		return nil, fmt.Errorf("walk upper: %w", err)
	}

	hash := manifestHash(files)
	snap := &Snapshot{
		ID:          "snap-" + hash[:16],
		WorkspaceID: s.workspace.ID,
		TakenAt:     time.Now(),
		Hash:        hash,
		Files:       files,
	}
	if s.latest != nil {
		snap.ParentID = s.latest.ID
		snap.Diff = diffSnapshots(s.latest, snap)
	} else {
		// First snapshot: everything is "added" relative to nothing.
		snap.Diff = &DiffSummary{}
		for _, f := range files {
			snap.Diff.Added = append(snap.Diff.Added, f.Path)
			snap.Diff.BytesAdded += f.Size
		}
	}

	if err := s.store.Save(snap, s.workspace.UpperDir); err != nil {
		return nil, err
	}
	s.latest = snap
	return snap, nil
}

// Latest returns the most recent captured snapshot or nil.
func (s *snapshotter) Latest() *Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.latest
}

// walkTree produces a sorted manifest of every entry under root with a
// content hash. Sorted output is what makes the manifest hash
// deterministic.
func walkTree(root string) ([]FileEntry, error) {
	if !pathExistsCopy(root) {
		return nil, nil
	}
	var out []FileEntry
	err := filepath.Walk(root, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		entry := FileEntry{
			Path: filepath.ToSlash(rel),
			Mode: uint32(fi.Mode()),
		}
		switch {
		case fi.Mode()&os.ModeSymlink != 0:
			tgt, _ := os.Readlink(path)
			entry.LinkTarget = tgt
		case fi.IsDir():
			// directories: no content hash, no size
		default:
			entry.Size = fi.Size()
			h, err := hashFile(path)
			if err != nil {
				return err
			}
			entry.ContentHash = h
		}
		out = append(out, entry)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// manifestHash hashes the canonical, sorted manifest of FileEntries.
// Deterministic — identical trees produce identical hashes regardless
// of filesystem traversal order.
func manifestHash(files []FileEntry) string {
	h := sha256.New()
	for _, f := range files {
		fmt.Fprintf(h, "%s\x00%o\x00%d\x00%s\x00%s\n",
			f.Path, f.Mode, f.Size, f.ContentHash, f.LinkTarget)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// diffSnapshots compares two snapshots and returns the delta. Used by
// receipts to record "what the cap changed."
func diffSnapshots(prev, curr *Snapshot) *DiffSummary {
	prevMap := make(map[string]FileEntry, len(prev.Files))
	for _, f := range prev.Files {
		prevMap[f.Path] = f
	}
	currMap := make(map[string]FileEntry, len(curr.Files))
	for _, f := range curr.Files {
		currMap[f.Path] = f
	}

	out := &DiffSummary{}
	for path, c := range currMap {
		if p, ok := prevMap[path]; ok {
			if p.ContentHash != c.ContentHash || p.Mode != c.Mode || p.LinkTarget != c.LinkTarget {
				out.Modified = append(out.Modified, path)
				if c.Size > p.Size {
					out.BytesAdded += c.Size - p.Size
				} else {
					out.BytesRemoved += p.Size - c.Size
				}
			}
		} else {
			out.Added = append(out.Added, path)
			out.BytesAdded += c.Size
		}
	}
	for path, p := range prevMap {
		if _, ok := currMap[path]; !ok {
			out.Deleted = append(out.Deleted, path)
			out.BytesRemoved += p.Size
		}
	}
	sort.Strings(out.Added)
	sort.Strings(out.Modified)
	sort.Strings(out.Deleted)
	return out
}

// Format renders a DiffSummary as a human-readable string. Used by
// `aiplex snapshot diff`.
func (d *DiffSummary) Format() string {
	if d == nil {
		return "(no diff)"
	}
	var b strings.Builder
	if len(d.Added) > 0 {
		fmt.Fprintf(&b, "+ %d added\n", len(d.Added))
		for _, p := range d.Added {
			fmt.Fprintf(&b, "    + %s\n", p)
		}
	}
	if len(d.Modified) > 0 {
		fmt.Fprintf(&b, "~ %d modified\n", len(d.Modified))
		for _, p := range d.Modified {
			fmt.Fprintf(&b, "    ~ %s\n", p)
		}
	}
	if len(d.Deleted) > 0 {
		fmt.Fprintf(&b, "- %d deleted\n", len(d.Deleted))
		for _, p := range d.Deleted {
			fmt.Fprintf(&b, "    - %s\n", p)
		}
	}
	if b.Len() == 0 {
		return "(no changes)\n"
	}
	fmt.Fprintf(&b, "  %d bytes added, %d bytes removed\n", d.BytesAdded, d.BytesRemoved)
	return b.String()
}
