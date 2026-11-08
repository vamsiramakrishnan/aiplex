//go:build !unix

package deployment

import "os/exec"

func configureProcess(*exec.Cmd) {}
