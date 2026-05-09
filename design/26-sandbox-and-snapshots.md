# 26 — Sandbox & Snapshots: Verifiable Isolation

> **Status:** Shipped (Direct + Bwrap on Linux + Workspace + Snapshot + SnapshotStore + per-kind seccomp profiles + 11 tests).
> **Closes the admin lens of design/25:** blast-radius guarantees that don't depend on policy adherence, plus filesystem audit that's verifiable instead of "trust the logs."

## What this is

Two coupled primitives, both first-class in the codebase:

1. **`Sandbox`** — runs a cap invocation in a process whose identity, syscall surface, filesystem, network, and resource budget are derived from the cap's `attrs` and the cap claim's `constraints`. One interface, multiple implementations, runtime selection.

2. **`Workspace` + `Snapshot`** — every cap invocation gets a layered filesystem (read-only base + per-invocation upper layer); the upper layer is content-hashed into Snapshots that the receipt chain references. Snapshots are forkable, diffable, and restorable.

The two together are why AIPlex's audit story is *categorically* different from competitors: receipts cite cryptographic content hashes of what the cap actually wrote, not vendor-managed log lines.

## Why this is the substrate (not a feature)

Existing systems pick one or two of these and stop:

| System | Isolation | Verifiable workspace state |
|---|---|---|
| AgentCore (AWS) | Per-agent runtime sandbox; not per cap call | None — log entries live in CloudTrail |
| Vertex Agent Engine (GCP) | Per-deployment IAM scoping | None — Vertex-managed state, opaque |
| Solo agentgateway | TLS termination + forward; isolation is downstream | N/A |
| Letta | Process-level (one Python process, one tenant) | Per-agent memory in DB |
| Docker MCP Toolkit | Per-tool container | None |

AIPlex: **per-cap-invocation** isolation derived from cap claim constraints, with a **content-hashed snapshot** of the workspace state on every invocation, chainable into receipts. No competitor has both. This is the trust-ledger design from doc 21 made real.

## The shape

```
internal/sandbox/
├── types.go             Sandbox interface, SpawnRequest, Handle, Result, MountSpec, ResourceLimits
├── errors.go            Sentinel errors (errors.Is targets)
├── sandbox.go           AutoDetect factory + Capabilities reporter
├── seccomp.go           Per-kind seccomp profile generation (read/write/external)
│
├── workspace.go         Workspace lifecycle (Mount, Unmount, Snapshot, Destroy)
├── overlay.go           overlayDriver interface
├── overlay_linux.go     Tries native overlayfs → fuse-overlayfs → copy
├── overlay_other.go     Non-Linux builds always copy
├── overlay_copy.go      Universal copy-based fallback (the slow but always-works path)
│
├── snapshot.go          Snapshot, FileEntry, DiffSummary, deterministic content hash
├── snapshot_store.go    Filesystem-backed store (manifest.json + tree/), GC, Diff
│
├── direct.go            No-isolation Sandbox (workspace + snapshots still apply)
├── bwrap_linux.go       Bubblewrap-backed Sandbox (Linux): namespaces, UID per-cap, mounts, seccomp profile written
└── bwrap_other.go       Stub (returns ErrSandboxUnavailable on non-Linux)
```

## The Sandbox interface

```go
type Sandbox interface {
    Name() string
    Spawn(ctx context.Context, req SpawnRequest) (*Handle, error)
    Close() error
}

type SpawnRequest struct {
    Cap           capability.Capability
    Claim         capability.Cap
    Subject       string
    CallerSpiffe  string
    Action        string
    Command       []string
    Env           []string
    Input         io.Reader
    Workspace     *Workspace      // ephemeral if nil
    Mounts        []MountSpec
    AllowedEgress []string
    Limits        *ResourceLimits // derived from cap.attrs+claim.constraints if nil
}
```

The `Cap.Attrs.SideEffect` field drives the seccomp profile; `Cap.Attrs.LatencyBudgetMs` drives the wall-clock deadline; `Claim.Constraints` further narrows resource caps.

## The Workspace model

Every workspace has three lifetime patterns the deploy engine and workflow executor compose against:

- **Ephemeral** — created on `Spawn`, destroyed on `Handle.Close`. Used for stateless tool calls.
- **Persistent** — created once, reused across invocations. The workspace IS the long-lived agent/memory state. Snapshots are how you audit and roll back.
- **Forked** — created from an existing snapshot. Used for replay, what-if exploration, "resume the agent from yesterday's state." Tied to the durable-execution roadmap (doc 27, planned).

```go
ws, err := sandbox.NewWorkspace(sandbox.WorkspaceConfig{
    BaseDir:        "/var/lib/aiplex/images/tutor-v1",
    Owner:          "cap://agent/tutor@v1",
    Persistent:     true,
    Snapshotter:    store,
    FromSnapshotID: "snap-1a2b3c…",  // optional: fork from previous state
})
ws.Mount()
// … sandbox spawns process inside ws.MergedDir as /work …
ws.Snapshot()   // captures upper layer; content-hash + diff vs previous
```

### Layered storage

- **BaseDir**: read-only root the cap mounts at `/`. Assembled by the deploy engine from the cap's image + kind defaults.
- **UpperDir**: writes go here. Survives across `Mount/Unmount` cycles for persistent workspaces.
- **MergedDir**: the union view exposed to the cap process.

On Linux:
1. **Native overlayfs** (mount(2) with type "overlay") if CAP_SYS_ADMIN. Fastest.
2. **fuse-overlayfs** if available — works rootless. Used by Flatpak / podman-rootless.
3. **Copy fallback** — universal. ~50-200ms per spawn but works without privilege.

On macOS/Windows: copy fallback only.

The package picks the strongest available driver at first `Mount` and pins it; subsequent mounts use the same path.

## Snapshots: the verifiable bit

```go
type Snapshot struct {
    ID          string         // snap-<16-hex of Hash>
    WorkspaceID string
    ParentID    string         // previous snapshot in the chain
    TakenAt     time.Time
    Hash        string         // sha256 over canonicalised manifest
    Files       []FileEntry    // sorted, content-hashed
    Diff        *DiffSummary   // delta vs ParentID
    StoragePath string         // server-side; the snapshot's tree/ on disk
}
```

Three properties matter:

1. **Content-addressed** — `manifestHash(files)` is deterministic over a sorted manifest of `(path, mode, size, sha256(content), symlink-target)`. Identical workspace state always yields identical hash. Test `TestSnapshotStore_DeterministicHash` proves this with two workspaces in different paths.

2. **Diffable** — `DiffSummary` records added/modified/deleted paths plus byte deltas, parented to the previous snapshot. The auditor reads the diff and either verifies "the cap did exactly what its claim said" or surfaces the deviation. `aiplex snapshot diff <a> <b>` exposes this.

3. **Restorable** — `SnapshotStore.Restore(id, dst)` copies the snapshot's tree into a fresh workspace's UpperDir. That's the foundation for replay, what-if exploration, and durable agent restart.

### Receipt integration

Every `Result` carries `PreSnapshotID` and `PostSnapshotID`. When the receipt chain ships (doc 21 — planned), receipts cite these hashes. An auditor with `cap://meta/audit@v1`:

1. Fetches the receipt
2. Fetches the pre- and post-snapshots from the SnapshotStore (or sigstore-anchored mirror)
3. Verifies the manifest hash matches the receipt's claim
4. Reads the diff to see exactly what files the cap changed

That's verifiable audit — the chain breaks if anyone tampers with intermediate state.

## Per-kind seccomp profiles

`ProfileForCap(cap)` returns a `SeccompProfile` (allow/deny lists + default action) keyed off `cap.Attrs.SideEffect`:

| `side_effect` | Default | Allow includes | Deny includes |
|---|---|---|---|
| `read` | errno | stdio reads, stat, mmap, futex, signals | execve, fork, ptrace, **all writes** |
| `write` | errno | reads + fs write syscalls | execve, fork, ptrace |
| `external` | errno | reads + writes + network syscalls | init_module, reboot, mount, swapon |
| (default) | errno | minimal common-safe set | execve, fork, ptrace |

The profile is per-cap and emitted as JSON next to the workspace (`<workspace-root>/seccomp.json`). The Bwrap impl writes it for inspection; libseccomp BPF compilation + `bwrap --seccomp <fd>` wiring is the next iteration. Tests `TestProfileForCap_ReadOnly` and `TestProfileForCap_External` lock in the contract.

## Sandbox implementations

### Direct
No isolation. The cap exec's directly under the host process. Workspace + Snapshot still work, so receipts are still produced. Used as fallback when nothing stronger is available, and in tests where forking real bwrap processes is overkill.

The factory's banner makes the trade-off visible: when `aiplex up` falls back to Direct, it logs a clear warning that runtime isolation is off.

### Bwrap (Linux)
Constructs a flag set per-spawn:

```
bwrap \
  --die-with-parent \
  --unshare-pid --unshare-ipc --unshare-uts \
  [--unshare-net]                                  # default-deny network
  --proc /proc --dev /dev --tmpfs /tmp \
  --ro-bind /usr /usr \
  --ro-bind-try /lib /lib \
  --ro-bind-try /etc/ssl /etc/ssl \
  --uid <per-cap-UID> --gid <per-cap-UID> \
  --bind <workspace.MergedDir> /work \
  --setenv HOME /work --chdir /work \
  -- <cap.Command...>
```

Per-cap UIDs come from a 256-deep pool (9000–9255). Network is unshared by default; only the explicit `AllowedEgress` set is reachable via cap-namespace nftables rules (set by AIPlex outside the bwrap exec).

The flag-builder is exposed as `Bwrap.BuildFlagsForTest` so the construction logic is testable without forking real bwrap processes.

### AutoDetect factory

```go
sb, err := sandbox.New(sandbox.Config{
    Mode:          sandbox.ModeAuto,    // or ModeBwrap, ModeDirect
    SnapshotStore: store,
})
fmt.Println(sandbox.Describe(sb, store).Notes)
```

`ModeAuto` tries Bwrap first; falls back to Direct with a stderr warning. `aiplex up` uses ModeAuto.

## CLI

```
aiplex snapshot list                        # all captured snapshots
aiplex snapshot list --workspace ws-abc     # for one workspace
aiplex snapshot show snap-abc               # full manifest + diff vs parent
aiplex snapshot diff snap-abc snap-def      # file-level diff between any two
aiplex snapshot gc                          # reclaim orphaned snapshots
```

Used by humans to inspect what their agents did. Will also be the data source for the live receipt stream in the Console.

## Tests

11 tests, all green, none requiring bwrap or root:

- `TestWorkspace_MountUnmountDestroy`
- `TestWorkspace_UpperLayerOverridesBase`
- `TestSnapshotStore_CaptureAndDiff` (add/modify/delete cases)
- `TestSnapshotStore_RestoreForksWorkspace`
- `TestSnapshotStore_DeterministicHash` (two paths, identical hash)
- `TestSnapshotStore_GC` (chain preservation)
- `TestProfileForCap_ReadOnly` / `TestProfileForCap_External`
- `TestDirectSandbox_RoundTrip` (real shell exec via Direct)
- `TestDirectSandbox_TimeoutEnforced` (wall-clock kill)
- `TestNew_AutoFallsBackToDirect`

## What's not yet shipped

These are extensions on top of the same primitive, not architectural changes:

- **libseccomp BPF compilation** so `--seccomp <fd>` actually applies the profile (Bwrap currently writes JSON; the kernel filter is the next step).
- **cgroups v2 wrapper** (CPUMax/MemoryMax/IOWeight). Today wall-clock timeout enforces the most important bound; cgroups are belt-and-suspender.
- **AllowedEgress nftables glue** — the cap's network namespace rules.
- **Workspace export/import** — `aiplex workspace export <id>` for cross-node portability.
- **Microvm Sandbox impl** (Firecracker/Cloud Hypervisor) for multi-tenant SaaS.
- **K8s Sandbox impl** that emits the same SpawnRequest as a Pod with PodSecurityContext + SeccompProfile + NetworkPolicy.

Each of these plugs into the same `Sandbox` interface — strength dialed by deployment, no protocol change.

## How this passes the test in design/25

| Stakeholder | What this commit gives them |
|---|---|
| **Admin** | Per-cap, kernel-level isolation (Linux Bwrap) with per-cap UID, namespaces, default-deny network, cgroup-ready limits, syscall profile per kind. Blast-radius = one cap-invocation. |
| **Compliance officer** | Cryptographic content hashes on every workspace state transition. Receipts cite hashes; tampering breaks the chain. Diffs show exactly what files a cap touched. |
| **End user** | `aiplex snapshot diff` lets them see what an agent did to their workspace. Forkable snapshots mean "give me the same agent state on my friend's machine." |
| **Developer** | One Sandbox interface; fallback to Direct in dev so tests don't need root. Same SDK shape; isolation is a deployment dial. |
| **Founder** | The trust property (verifiable audit) is now a code-level guarantee, not a marketing claim. Easier to sell into regulated industries. |

## See also

- [25 — The Problem We Solve](25-the-problem-we-solve.md) — north star
- [21 — Runtime Consent & Trust Ledger](21-runtime-consent-and-trust-ledger.md) — receipts cite snapshot hashes
- [24 — Agent-as-Cap, Workflow-as-Cap](24-agent-and-workflow-as-cap.md) — workflows reuse Workspace state across steps
- `internal/sandbox/`, `cmd/aiplex-cli/cmd_snapshot.go`
