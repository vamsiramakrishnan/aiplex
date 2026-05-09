//go:build !linux

package sandbox

// On non-Linux hosts, Bwrap is unavailable. NewBwrap returns
// ErrSandboxUnavailable so the AutoDetect factory can fall through to
// Direct without surfacing build errors.

// Bwrap is the placeholder type for non-Linux builds. Its only purpose
// is to satisfy go-doc/cross-platform builds; calls return errors.
type Bwrap struct{}

// NewBwrap returns ErrSandboxUnavailable on non-Linux platforms.
func NewBwrap(_ *SnapshotStore) (*Bwrap, error) { return nil, ErrSandboxUnavailable }
