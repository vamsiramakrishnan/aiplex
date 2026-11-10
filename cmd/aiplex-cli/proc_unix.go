//go:build !windows

package main

import (
	"errors"
	"os/exec"
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
	return err == nil || errors.Is(err, syscall.EPERM)
}

func terminateProcess(pid int) error { return syscall.Kill(pid, syscall.SIGTERM) }
func killProcess(pid int) error      { return syscall.Kill(pid, syscall.SIGKILL) }
