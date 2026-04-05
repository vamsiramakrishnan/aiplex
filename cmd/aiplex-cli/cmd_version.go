package main

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

// Set via ldflags at build time:
//
//	go build -ldflags "-X main.Version=v1.0.0 -X main.Commit=abc1234 -X main.Date=2026-04-05"
var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Show build version information",
		Run: func(cmd *cobra.Command, args []string) {
			if output == "json" || output == "yaml" {
				info := map[string]string{
					"version":  Version,
					"commit":   Commit,
					"date":     Date,
					"go":       runtime.Version(),
					"os":       runtime.GOOS,
					"arch":     runtime.GOARCH,
				}
				if output == "yaml" {
					printYAML(info)
				} else {
					printJSON(info)
				}
				return
			}
			fmt.Printf("aiplex %s\n", Version)
			fmt.Printf("  commit: %s\n", Commit)
			fmt.Printf("  built:  %s\n", Date)
			fmt.Printf("  go:     %s\n", runtime.Version())
			fmt.Printf("  os:     %s/%s\n", runtime.GOOS, runtime.GOARCH)
		},
	}
}
