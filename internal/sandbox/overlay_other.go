//go:build !linux

package sandbox

// On non-Linux hosts (macOS dev laptops, Windows) overlayfs isn't an
// option. The copy-based driver provides identical semantics at the
// cost of a one-time per-spawn copy. Snapshots, diffs, and the receipt
// chain all work the same.
func newOverlayDriver() overlayDriver { return &overlayCopy{} }
