package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"
)

func logsCmd() *cobra.Command {
	var (
		follow bool
		tail   int
	)

	cmd := &cobra.Command{
		Use:   "logs <instance-id>",
		Short: "Stream logs from a deployed instance",
		Long: `View logs from a deployed MCP server, A2A agent, or LLM proxy.

Examples:
  aiplex logs knowledge-base-xyz              # last 100 lines
  aiplex logs knowledge-base-xyz -f           # follow/stream
  aiplex logs knowledge-base-xyz --tail 500   # last 500 lines`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			instanceID := args[0]
			c := newClient()

			// Get instance to determine namespace
			inst, err := c.GetInstance(context.Background(), instanceID)
			if err != nil {
				return fmt.Errorf("get instance: %w", err)
			}

			ns := inst.Namespace
			if ns == "" {
				ns = inst.Kind // fallback: kind doubles as namespace label
			}

			fmt.Printf("Logs for %s (namespace: %s)\n", instanceID, ns)
			fmt.Println("────��───────────────────────────────────")

			// Build kubectl logs command
			kubectlArgs := []string{
				"logs",
				"-n", ns,
				"-l", fmt.Sprintf("app=%s", instanceID),
				"--all-containers=true",
				fmt.Sprintf("--tail=%d", tail),
			}
			if follow {
				kubectlArgs = append(kubectlArgs, "-f")
			}

			kubectl := exec.Command("kubectl", kubectlArgs...)
			kubectl.Stdout = os.Stdout
			kubectl.Stderr = os.Stderr

			if err := kubectl.Run(); err != nil {
				// If kubectl fails, give helpful guidance
				fmt.Println()
				fmt.Println("Could not fetch logs. Possible causes:")
				fmt.Printf("  - Instance %s may not be running (status: %s)\n", instanceID, inst.Status)
				fmt.Println("  - kubectl may not be configured — run: aiplex platform apply")
				fmt.Println("  - For local dev, logs are in the terminal running 'make dev'")
				return nil
			}

			return nil
		},
	}

	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Follow log output")
	cmd.Flags().IntVar(&tail, "tail", 100, "Number of recent log lines to show")
	return cmd
}
