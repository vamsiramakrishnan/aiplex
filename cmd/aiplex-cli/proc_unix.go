//go:build !windows

package main

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// detachProcess puts cmd in its own session so it survives the terminal.
func detachProcess(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }

// processAlive reports whether pid exists (signal 0).
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	if err != nil && !errors.Is(err, syscall.EPERM) {
		return false
	}
	// A zombie (exited, not yet reaped) is not running. /proc exists on Linux;
	// elsewhere an orphaned zombie is reaped by launchd/init promptly.
	if stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat"); err == nil {
		if i := strings.LastIndexByte(string(stat), ')'); i >= 0 && i+2 < len(stat) && stat[i+2] == 'Z' {
			return false
		}
	}
	return true
}

func terminateProcess(pid int) error { return syscall.Kill(pid, syscall.SIGTERM) }
func killProcess(pid int) error      { return syscall.Kill(pid, syscall.SIGKILL) }
