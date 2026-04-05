package deploy

import (
	"context"
	"os/exec"
)

// execCommandContext wraps exec.CommandContext for testability.
var execCommandContext = func(ctx context.Context, name string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, name, args...)
}
