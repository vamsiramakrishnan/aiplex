package sandbox

import (
	"errors"
	"fmt"
	"os"
)

// Mode selects which Sandbox implementation to use. ModeAuto is the
// recommended default: it picks the strongest available on this host
// and falls back gracefully.
type Mode string

const (
	ModeAuto   Mode = "auto"
	ModeBwrap  Mode = "bwrap"
	ModeDirect Mode = "direct"
)

// Config drives the AutoDetect factory.
type Config struct {
	// Mode picks the implementation. ModeAuto by default.
	Mode Mode

	// SnapshotStore is the backing store for Workspace + Snapshot. nil
	// means snapshots are disabled (workspace still works as scratch).
	SnapshotStore *SnapshotStore

	// LogWarning is called when the factory falls back to a weaker
	// implementation than requested. Receives a human-readable reason.
	// Default behaviour: write to os.Stderr.
	LogWarning func(reason string)
}

// New constructs a Sandbox according to cfg. Always returns a working
// Sandbox unless cfg.Mode pins an implementation that's not available
// here — in which case the error explains which probe failed.
//
// In ModeAuto, the factory tries Bwrap first, falls through to Direct
// with a one-line warning if Bwrap can't be constructed. This is what
// `aiplex up` uses: stronger isolation when the host can do it,
// graceful degradation otherwise.
func New(cfg Config) (Sandbox, error) {
	if cfg.Mode == "" {
		cfg.Mode = ModeAuto
	}
	if cfg.LogWarning == nil {
		cfg.LogWarning = func(reason string) {
			fmt.Fprintln(os.Stderr, "sandbox: "+reason)
		}
	}

	tryBwrap := func() (Sandbox, error) {
		b, err := NewBwrap(cfg.SnapshotStore)
		if err != nil {
			return nil, err
		}
		return b, nil
	}

	switch cfg.Mode {
	case ModeBwrap:
		return tryBwrap()
	case ModeDirect:
		return NewDirect(cfg.SnapshotStore), nil
	case ModeAuto:
		if s, err := tryBwrap(); err == nil {
			return s, nil
		} else if !errors.Is(err, ErrSandboxUnavailable) {
			cfg.LogWarning(fmt.Sprintf("bwrap unavailable (%v); falling back to direct (no isolation)", err))
		}
		return NewDirect(cfg.SnapshotStore), nil
	default:
		return nil, fmt.Errorf("unknown sandbox mode %q", cfg.Mode)
	}
}

// Capabilities reports what the chosen Sandbox actually provides at
// runtime. Useful in `aiplex up` banner output and for the Console to
// say "your local node is using bwrap" / "your local node has no
// runtime isolation."
type Capabilities struct {
	Name             string `json:"name"`
	Isolated         bool   `json:"isolated"`
	HasNamespaces    bool   `json:"has_namespaces"`
	HasSeccomp       bool   `json:"has_seccomp"`
	HasNetworkScope  bool   `json:"has_network_scope"`
	SupportsSnapshot bool   `json:"supports_snapshot"`
	Notes            string `json:"notes,omitempty"`
}

// Describe a Sandbox's runtime properties.
func Describe(s Sandbox, store *SnapshotStore) Capabilities {
	c := Capabilities{Name: s.Name()}
	switch s.Name() {
	case "bwrap":
		c.Isolated = true
		c.HasNamespaces = true
		c.HasSeccomp = false // not yet wired (see bwrap_linux.go TODO)
		c.HasNetworkScope = true
		c.Notes = "Linux user-namespace isolation; seccomp profile generated but not applied yet."
	case "direct":
		c.Isolated = false
		c.HasNamespaces = false
		c.HasSeccomp = false
		c.HasNetworkScope = false
		c.Notes = "No runtime isolation — caps share the host process namespace. Use only on a host you control."
	}
	c.SupportsSnapshot = store != nil
	return c
}
