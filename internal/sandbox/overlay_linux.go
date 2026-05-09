//go:build linux

package sandbox

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// newOverlayDriver chooses the strongest overlay implementation that
// actually works on this kernel + permissions context. Order:
//
//  1. Native overlayfs via syscall.Mount — fastest, but needs
//     CAP_SYS_ADMIN or a user-namespace setup that allows it.
//  2. fuse-overlayfs binary — works rootless on most distros.
//  3. Copy-based fallback — works always; slowest.
//
// The choice is per-process; the first Mount that succeeds dictates
// the path forward. If all three fail, Mount returns the original
// error so callers can surface it with context.
func newOverlayDriver() overlayDriver {
	return &linuxOverlay{}
}

type linuxOverlay struct {
	chosen overlayDriver // pinned after first successful Mount
}

func (l *linuxOverlay) Mount(w *Workspace) error {
	if l.chosen != nil {
		return l.chosen.Mount(w)
	}
	// Native overlayfs first.
	native := &overlayfsNative{}
	if err := native.Mount(w); err == nil {
		l.chosen = native
		return nil
	} else if !isPermissionLikeError(err) {
		// A non-permission error (e.g. corrupted dirs) means we shouldn't
		// silently retry with another driver — surface the real cause.
		return err
	}

	// fuse-overlayfs (rootless).
	fuse := &overlayfsFUSE{}
	if err := fuse.Mount(w); err == nil {
		l.chosen = fuse
		return nil
	}

	// Copy-based.
	cp := &overlayCopy{}
	if err := cp.Mount(w); err != nil {
		return fmt.Errorf("overlayfs/fuse-overlayfs/copy all failed: %w", err)
	}
	l.chosen = cp
	return nil
}

func (l *linuxOverlay) Unmount(w *Workspace) error {
	if l.chosen == nil {
		return nil
	}
	return l.chosen.Unmount(w)
}

// overlayfsNative uses the kernel overlayfs driver via mount(2).
// Requires CAP_SYS_ADMIN.
type overlayfsNative struct{}

func (n *overlayfsNative) Mount(w *Workspace) error {
	opts := fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s",
		w.BaseDir, w.UpperDir, w.WorkDir)
	if err := syscall.Mount("overlay", w.MergedDir, "overlay", 0, opts); err != nil {
		return fmt.Errorf("mount overlayfs: %w", err)
	}
	return nil
}

func (n *overlayfsNative) Unmount(w *Workspace) error {
	if err := syscall.Unmount(w.MergedDir, 0); err != nil && !errors.Is(err, syscall.EINVAL) {
		return fmt.Errorf("unmount overlay: %w", err)
	}
	return nil
}

// overlayfsFUSE uses the fuse-overlayfs binary (rootless overlay).
type overlayfsFUSE struct{}

func (f *overlayfsFUSE) Mount(w *Workspace) error {
	bin, err := exec.LookPath("fuse-overlayfs")
	if err != nil {
		return fmt.Errorf("fuse-overlayfs binary not found: %w", err)
	}
	opts := fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s",
		w.BaseDir, w.UpperDir, w.WorkDir)
	cmd := exec.Command(bin, "-o", opts, w.MergedDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("fuse-overlayfs: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (f *overlayfsFUSE) Unmount(w *Workspace) error {
	if bin, err := exec.LookPath("fusermount3"); err == nil {
		_ = exec.Command(bin, "-u", w.MergedDir).Run()
		return nil
	}
	if bin, err := exec.LookPath("fusermount"); err == nil {
		_ = exec.Command(bin, "-u", w.MergedDir).Run()
	}
	return nil
}

// isPermissionLikeError matches the broad set of "this isn't going to
// work without privilege" errors that should trigger fallback to a
// userspace driver.
func isPermissionLikeError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EOPNOTSUPP) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "operation not permitted") ||
		strings.Contains(msg, "permission denied") ||
		strings.Contains(msg, "operation not supported")
}

// ensureWritable is a tiny helper used by drivers when the upper layer's
// permission bits aren't quite right for the cap process's UID.
func ensureWritable(path string) error {
	return os.Chmod(path, 0o755)
}

// pathExists reports whether path exists. Used by the copy fallback to
// decide between fresh-init and resume.
func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

var _ = filepath.Join // keep import used
