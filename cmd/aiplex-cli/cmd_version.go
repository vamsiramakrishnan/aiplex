package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"time"

	"github.com/spf13/cobra"
)

// Set via ldflags at build time: -ldflags "-X main.version=v0.1.0 -X main.commit=abc123"
var (
	version = "dev"
	commit  = "unknown"
)

func versionCmd() *cobra.Command {
	var checkUpgrade bool

	cmd := &cobra.Command{
		Use:   "version",
		Short: "Show CLI version and check for updates",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("aiplex version %s\n", version)
			fmt.Printf("  commit:   %s\n", commit)
			fmt.Printf("  go:       %s\n", runtime.Version())
			fmt.Printf("  os/arch:  %s/%s\n", runtime.GOOS, runtime.GOARCH)

			if checkUpgrade {
				fmt.Println()
				checkForUpgrade()
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&checkUpgrade, "check", false, "Check for newer version")
	return cmd
}

func checkForUpgrade() {
	if version == "dev" {
		fmt.Println("  Running dev build — version check skipped.")
		return
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("https://api.github.com/repos/vamsiramakrishnan/aiplex/releases/latest")
	if err != nil {
		fmt.Println("  Could not check for updates.")
		return
	}
	defer resp.Body.Close()

	var release struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return
	}

	if release.TagName != "" && release.TagName != version {
		fmt.Printf("  Update available: %s → %s\n", version, release.TagName)
		fmt.Printf("  Download: %s\n", release.HTMLURL)
	} else {
		fmt.Println("  You are up to date.")
	}
}
