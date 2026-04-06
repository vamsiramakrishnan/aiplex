package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/vamsiramakrishnan/aiplex/internal/cliconfig"
)

func quickstartCmd() *cobra.Command {
	var (
		project     string
		region      string
		autoApprove bool
	)

	cmd := &cobra.Command{
		Use:   "quickstart",
		Short: "Zero to running platform in one command",
		Long: `Runs the full setup pipeline end-to-end:

  1. aiplex init      — configure project and install tools
  2. platform apply   — deploy infrastructure + application
  3. deploy example   — deploy quickstart workload
  4. open console     — open the AIPlex console in browser

This is the fastest way to go from nothing to a running AIPlex platform.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println()
			fmt.Println("  ╔══════════════════════════════════════╗")
			fmt.Println("  ║       AIPlex Quickstart               ║")
			fmt.Println("  ╚══════════════════════════════════════╝")
			fmt.Println()

			// Step 1: Init (if no context exists)
			cfg, _ := cliconfig.Load()
			if cfg.CurrentContext == "" || project != "" {
				fmt.Println("━━━ Step 1/4: Initialize ━━━━━━━━━━━━━━━")
				fmt.Println()
				initArgs := []string{"init"}
				if project != "" {
					initArgs = append(initArgs, "--project", project)
				}
				if region != "" {
					initArgs = append(initArgs, "--region", region)
				}
				self, _ := os.Executable()
				initCmd := exec.Command(self, initArgs...)
				initCmd.Stdout = os.Stdout
				initCmd.Stderr = os.Stderr
				initCmd.Stdin = os.Stdin
				if err := initCmd.Run(); err != nil {
					return fmt.Errorf("init failed: %w", err)
				}
				fmt.Println()
			} else {
				fmt.Printf("━━━ Step 1/4: Initialize (using context %q) ━━━\n", cfg.CurrentContext)
				fmt.Println()
			}

			// Step 2: Platform apply
			fmt.Println("━━━ Step 2/4: Deploy Platform ━━━━━━━━━━")
			fmt.Println()
			self, _ := os.Executable()
			applyArgs := []string{"platform", "apply"}
			if autoApprove {
				applyArgs = append(applyArgs, "--yes")
			}
			applyCmd := exec.Command(self, applyArgs...)
			applyCmd.Stdout = os.Stdout
			applyCmd.Stderr = os.Stderr
			applyCmd.Stdin = os.Stdin
			if err := applyCmd.Run(); err != nil {
				return fmt.Errorf("platform apply failed: %w", err)
			}
			fmt.Println()

			// Step 3: Deploy quickstart example
			fmt.Println("━━━ Step 3/4: Deploy Example Workload ━━")
			fmt.Println()
			exampleFile := findQuickstartFile()
			if exampleFile != "" {
				applyExCmd := exec.Command(self, "apply", "-f", exampleFile)
				applyExCmd.Stdout = os.Stdout
				applyExCmd.Stderr = os.Stderr
				if err := applyExCmd.Run(); err != nil {
					fmt.Printf("  [WARN] Example deploy failed: %v\n", err)
					fmt.Println("  You can deploy manually later: aiplex apply -f examples/quickstart.yaml")
				} else {
					fmt.Println("  [pass] Example workload deployed")
				}
			} else {
				fmt.Println("  [WARN] No quickstart example found. Deploy manually later.")
			}
			fmt.Println()

			// Step 4: Open console
			fmt.Println("━━━ Step 4/4: Open Console ━━━━━━━━━━━━━")
			fmt.Println()
			cfg, _ = cliconfig.Load()
			if ctx, err := cfg.Current(); err == nil && ctx.URL != "" {
				consoleURL := ctx.URL
				fmt.Printf("  Console: %s\n", consoleURL)
				if err := openBrowser(consoleURL); err == nil {
					fmt.Println("  Browser opened.")
				} else {
					fmt.Println("  Open the URL above in your browser.")
				}
			}

			fmt.Println()
			fmt.Println("  ╔══════════════════════════════════════╗")
			fmt.Println("  ║     Quickstart Complete!              ║")
			fmt.Println("  ╚══════════════════════════════════════╝")
			fmt.Println()
			fmt.Println("  Next steps:")
			fmt.Println("    aiplex login           # authenticate")
			fmt.Println("    aiplex status           # see what's running")
			fmt.Println("    aiplex catalog list     # browse tools/agents/models")
			fmt.Println()

			return nil
		},
	}

	cmd.Flags().StringVar(&project, "project", "", "GCP project ID (skips interactive prompt)")
	cmd.Flags().StringVar(&region, "region", "", "GCP region")
	cmd.Flags().BoolVarP(&autoApprove, "yes", "y", false, "Auto-approve Terraform changes")
	return cmd
}

func findQuickstartFile() string {
	candidates := []string{
		"examples/quickstart.yaml",
		"examples/quickstart.json",
		"../examples/quickstart.yaml",
	}
	for _, c := range candidates {
		abs, _ := filepath.Abs(c)
		if _, err := os.Stat(abs); err == nil {
			return abs
		}
	}
	return ""
}
