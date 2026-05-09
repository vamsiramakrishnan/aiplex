package sandbox

import (
	"github.com/vamsiramakrishnan/aiplex/internal/capability"
)

// SeccompProfile is a syscall allowlist (or denylist with a default
// allow). Implementations turn this into platform-specific filters:
//
//   - Linux bwrap: written to a file and passed via --seccomp <fd>
//     (after compilation by libseccomp).
//   - K8s: rendered as a SeccompProfile CR or runtime/default annotation.
//   - Direct: ignored (no isolation).
//
// The profile shape is intentionally portable. ProfileForCap derives one
// from a capability's attrs — side_effect is the dominant input:
//
//   side_effect=read     → no fs writes, no exec, no fork, no raw network
//   side_effect=write    → fs writes allowed in workspace; no exec; no
//                          raw network beyond declared egress
//   side_effect=external → fs writes confined to workspace; network
//                          allowed but only to declared egress; exec
//                          allowed (e.g. python interpreter spawning workers)
//
// The DataClass attribute tightens further: pii forbids writes outside
// the workspace's encrypted partition; regulated forbids any non-
// snapshotted state.
type SeccompProfile struct {
	// DefaultAction taken when no rule matches. "errno", "kill", or
	// "log".
	DefaultAction string `json:"default"`

	// Allow is the set of syscalls explicitly permitted. If non-empty
	// and DefaultAction is "kill" or "errno", calls outside this set
	// fail.
	Allow []string `json:"allow,omitempty"`

	// Deny is the set of syscalls explicitly forbidden. If non-empty
	// and DefaultAction is "log" or "allow", these still fail.
	Deny []string `json:"deny,omitempty"`

	// ArchMatches are the architectures the profile applies to.
	// Defaults to native. Used by Linux only.
	ArchMatches []string `json:"arch_matches,omitempty"`
}

// ProfileForCap returns a sensible default seccomp profile for the
// capability's attrs. Operators can override per-instance; the default
// is what a production deployment gets if nothing is customised.
//
// The profiles below are intentionally narrow — Linux has ~400 syscalls;
// most caps need maybe 60. The "common safe" set covers vDSO + epoll +
// the core stdio/fs reads that any non-trivial program needs.
func ProfileForCap(cap capability.Capability) SeccompProfile {
	common := commonSafeSyscalls()

	switch cap.Attrs.SideEffect {
	case "read":
		return SeccompProfile{
			DefaultAction: "errno",
			Allow:         common,
			// Even within "common", deny exec/fork hard for read-only caps.
			Deny: []string{"execve", "execveat", "fork", "vfork", "clone", "ptrace"},
		}
	case "write":
		out := SeccompProfile{
			DefaultAction: "errno",
			Allow:         append(common, fsWriteSyscalls()...),
			Deny:          []string{"execve", "execveat", "fork", "vfork", "ptrace"},
		}
		if cap.Attrs.DataClass == "regulated" {
			// regulated: also deny opening files outside the workspace.
			// Enforced by the bwrap rootfs, but we belt-and-suspender.
			out.Deny = append(out.Deny, "openat2")
		}
		return out
	case "external":
		// External caps (LLM proxy, code interpreter) need network +
		// possibly subprocess spawn. We keep them tightly scoped via
		// the bwrap mounts and allowed_egress; the seccomp profile is
		// permissive within the kind's expected surface.
		return SeccompProfile{
			DefaultAction: "errno",
			Allow:         append(append(common, fsWriteSyscalls()...), networkSyscalls()...),
			// Even external caps shouldn't load kernel modules or
			// reboot the host.
			Deny: []string{
				"init_module", "finit_module", "delete_module",
				"reboot", "kexec_load",
				"mount", "umount2",
				"swapon", "swapoff",
			},
		}
	default:
		// Unknown side_effect: assume read-only.
		return SeccompProfile{
			DefaultAction: "errno",
			Allow:         common,
			Deny:          []string{"execve", "execveat", "fork", "vfork", "clone", "ptrace"},
		}
	}
}

// commonSafeSyscalls is the baseline every cap is allowed to make: file
// stdio, time, signal handling, memory mapping. Anything beyond this is
// kind-specific.
func commonSafeSyscalls() []string {
	return []string{
		// Process / thread metadata
		"getpid", "getppid", "gettid", "getuid", "geteuid", "getgid", "getegid",
		"getpgid", "getpgrp", "getsid", "getgroups", "getrlimit", "prlimit64",
		// Memory
		"brk", "mmap", "munmap", "mremap", "mprotect", "madvise", "mlock", "munlock",
		// File metadata (read-only ops)
		"stat", "fstat", "lstat", "newfstatat", "fstatfs", "statfs", "statx",
		"access", "faccessat", "faccessat2",
		"readlink", "readlinkat",
		"getcwd", "getdents64",
		// File I/O — read side
		"open", "openat", "openat2", "close", "read", "pread64", "readv", "preadv",
		"lseek",
		// Signals
		"rt_sigaction", "rt_sigprocmask", "rt_sigreturn", "rt_sigtimedwait",
		"rt_sigsuspend", "rt_sigpending", "kill", "tgkill",
		"signalfd", "signalfd4",
		// Time
		"clock_gettime", "clock_getres", "clock_nanosleep",
		"gettimeofday", "nanosleep",
		// Polling / fd events
		"select", "pselect6", "poll", "ppoll",
		"epoll_create", "epoll_create1", "epoll_ctl", "epoll_wait", "epoll_pwait",
		"eventfd", "eventfd2",
		// Pipes / FDs
		"pipe", "pipe2", "dup", "dup2", "dup3", "fcntl",
		// Misc
		"getrandom", "uname", "arch_prctl", "set_tid_address",
		"set_robust_list", "exit", "exit_group", "futex", "futex_waitv",
		"sched_yield", "sched_getaffinity",
	}
}

// fsWriteSyscalls covers writes within the workspace.
func fsWriteSyscalls() []string {
	return []string{
		"write", "pwrite64", "writev", "pwritev",
		"creat", "ftruncate", "truncate",
		"unlink", "unlinkat",
		"rename", "renameat", "renameat2",
		"mkdir", "mkdirat",
		"rmdir",
		"link", "linkat", "symlink", "symlinkat",
		"chmod", "fchmod", "fchmodat",
		"chown", "fchown", "lchown", "fchownat",
		"utime", "utimes", "futimesat", "utimensat",
		"fsync", "fdatasync", "sync_file_range",
	}
}

// networkSyscalls covers TCP/UDP/Unix sockets. Egress is further
// constrained at the namespace level (see SpawnRequest.AllowedEgress).
func networkSyscalls() []string {
	return []string{
		"socket", "socketpair", "connect", "accept", "accept4",
		"sendto", "sendmsg", "sendmmsg", "send",
		"recvfrom", "recvmsg", "recvmmsg", "recv",
		"shutdown", "bind", "listen",
		"getsockname", "getpeername", "getsockopt", "setsockopt",
	}
}
