package sandbox

import "errors"

// Sentinel errors. Implementations use these so callers can errors.Is.
var (
	// ErrSandboxUnavailable is returned when no isolation backend can be
	// constructed on this host (e.g. asking for bwrap on macOS).
	ErrSandboxUnavailable = errors.New("sandbox: no implementation available")

	// ErrSnapshotsUnsupported is returned by Handle.Snapshot when the
	// underlying workspace doesn't support snapshotting.
	ErrSnapshotsUnsupported = errors.New("sandbox: snapshots unsupported on this workspace")

	// ErrWorkspaceNotFound is returned by SnapshotStore when a referenced
	// workspace ID has no on-disk state.
	ErrWorkspaceNotFound = errors.New("sandbox: workspace not found")

	// ErrSnapshotNotFound is returned by SnapshotStore when an ID is
	// unknown or has been GC'd.
	ErrSnapshotNotFound = errors.New("sandbox: snapshot not found")

	// ErrLimitExceeded is returned post-hoc when a process was killed for
	// exceeding cpu/memory/wall-time bounds.
	ErrLimitExceeded = errors.New("sandbox: resource limit exceeded")

	// ErrSeccompViolation is returned when seccomp killed the process.
	ErrSeccompViolation = errors.New("sandbox: seccomp violation (disallowed syscall)")
)
