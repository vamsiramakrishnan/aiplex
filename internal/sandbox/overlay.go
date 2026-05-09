package sandbox

// overlayDriver assembles MergedDir = Base ∪ Upper. Implementations:
//   - linux: try overlayfs; fall back to copy-based on permission errors
//   - other OS: always copy-based
//
// The driver doesn't know about the cap or the sandbox — it's just a
// filesystem primitive Workspaces use.
type overlayDriver interface {
	Mount(w *Workspace) error
	Unmount(w *Workspace) error
}
