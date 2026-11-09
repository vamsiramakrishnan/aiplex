package deployment

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestDeploymentHelperProcess(t *testing.T) {
	if os.Getenv("AIPLEX_DEPLOYMENT_HELPER") != "1" {
		return
	}
	if os.Getenv("HELPER_MODE") == "wait" {
		time.Sleep(20 * time.Second)
	}
	fmt.Print(strings.Repeat("x", 10000))
	os.Exit(0)
}
func TestExecRunnerBoundsOutputAndCancellation(t *testing.T) {
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	for _, mode := range []string{"output", "wait"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			result, e := (ExecRunner{}).Run(ctx, Command{Executable: exe, Args: []string{"-test.run=^TestDeploymentHelperProcess$"}, Env: []string{"AIPLEX_DEPLOYMENT_HELPER=1", "HELPER_MODE=" + mode}, Directory: t.TempDir(), OutputLimit: 32})
			if e == nil || !result.Started || !result.Interrupted {
				t.Fatal(e, result)
			}
			if len(result.Stdout)+len(result.Stderr) > 32 {
				t.Fatal("unbounded output")
			}
		})
	}
}
