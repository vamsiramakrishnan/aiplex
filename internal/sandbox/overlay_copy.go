package sandbox

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// overlayCopy is the universal fallback driver. On Mount it copies the
// base into the merged dir, then layers the upper on top. On Unmount it
// rsyncs writes from merged back to upper (so subsequent reads see them).
//
// Slower than overlayfs but works on any OS, in any container, without
// privilege. Used as fallback when overlayfs/fuse-overlayfs aren't
// available.
//
// The cost: the per-spawn copy takes O(base size). For a typical tool
// cap (a small Python or Node binary plus its libs, ~50-200 MB) that's
// 50-200 ms on local SSD. Worth the universality for a `aiplex up`
// experience that "just works."
type overlayCopy struct{}

func (o *overlayCopy) Mount(w *Workspace) error {
	// Start from a clean merged dir.
	if err := os.RemoveAll(w.MergedDir); err != nil {
		return fmt.Errorf("clear merged: %w", err)
	}
	if err := os.MkdirAll(w.MergedDir, 0o755); err != nil {
		return err
	}

	// Copy base → merged.
	if err := copyTree(w.BaseDir, w.MergedDir); err != nil {
		return fmt.Errorf("copy base into merged: %w", err)
	}

	// Layer upper → merged. (Copy semantics: upper wins on conflict.)
	if pathExistsCopy(w.UpperDir) {
		if err := copyTree(w.UpperDir, w.MergedDir); err != nil {
			return fmt.Errorf("layer upper onto merged: %w", err)
		}
	}
	return nil
}

func (o *overlayCopy) Unmount(w *Workspace) error {
	// Sync writes from merged back into upper so subsequent invocations
	// see them. We copy only entries that aren't already in upper or
	// that have changed — but for simplicity in this iteration we
	// rsync the whole merged tree into upper. Snapshot capture happens
	// off the upper dir, so as long as upper is a faithful representation
	// of post-invocation state, audit is correct.
	if !pathExistsCopy(w.MergedDir) {
		return nil
	}
	if err := copyTree(w.MergedDir, w.UpperDir); err != nil {
		return fmt.Errorf("sync merged → upper: %w", err)
	}
	// Free the merged tree; ephemeral workspaces will Destroy() right after.
	return os.RemoveAll(w.MergedDir)
}

// copyTree mirrors src → dst preserving file mode. Symlinks are
// preserved as symlinks (we don't dereference). Empty src is a no-op.
func copyTree(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("copyTree: source %s is not a directory", src)
	}

	return filepath.Walk(src, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(dst, rel)

		switch {
		case fi.IsDir():
			return os.MkdirAll(target, fi.Mode())
		case fi.Mode()&os.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			_ = os.Remove(target)
			return os.Symlink(link, target)
		default:
			return copyFile(path, target, fi.Mode())
		}
	})
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

// pathExistsCopy mirrors pathExists for use in this file (kept separate
// so non-linux builds don't take a dependency on overlay_linux.go).
func pathExistsCopy(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
