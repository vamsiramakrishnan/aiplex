package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	rdebug "runtime/debug"
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
		Short: "Show the CLI version, commit and whether the Console is built in",
		Example: `  aiplex version
  aiplex version --check
  aiplex version -o json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			v, c := buildVersion()
			console := "embedded"
			if !consoleBuilt() {
				console = "not embedded (source build without Node; make console && make build to include it)"
			}
			apiPath, apiErr := findAPIBinaryQuiet()
			api := apiPath
			if apiErr != nil {
				api = "not found (aiplex dev needs it next to this binary or on PATH)"
			}
			if machineOutput() {
				spStructured(cmd, map[string]any{"version": v, "commit": c, "go": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH, "console_embedded": consoleBuilt(), "api_binary": apiPath})
				return nil
			}
			fmt.Printf("aiplex version %s\n", v)
			fmt.Printf("  commit:   %s\n", c)
			fmt.Printf("  console:  %s\n", console)
			fmt.Printf("  api:      %s\n", api)
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

// buildVersion prefers the release ldflags and falls back to the module
// version and VCS revision Go records (go install ...@version).
func buildVersion() (string, string) {
	v, c := version, commit
	if info, ok := rdebug.ReadBuildInfo(); ok {
		if v == "dev" && info.Main.Version != "" && info.Main.Version != "(devel)" {
			v = info.Main.Version
		}
		if c == "unknown" {
			for _, s := range info.Settings {
				if s.Key == "vcs.revision" && len(s.Value) >= 7 {
					c = s.Value[:7]
				}
			}
		}
	}
	return v, c
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
