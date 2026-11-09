package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime"

	"github.com/spf13/cobra"
	"github.com/vamsiramakrishnan/aiplex/pkg/toolauth"
)

func toolAuthPrint(value any) {
	if output == "yaml" {
		printYAML(value)
	} else {
		printJSON(value)
	}
}
func toolAuthResponseFile(path string) (toolauth.AuthorizationResponse, error) {
	var out toolauth.AuthorizationResponse
	file, e := os.Open(path)
	if e != nil {
		return out, e
	}
	defer file.Close()
	stat, e := file.Stat()
	if e != nil {
		return out, e
	}
	if !stat.Mode().IsRegular() || (runtime.GOOS != "windows" && stat.Mode().Perm()&0077 != 0) {
		return out, fmt.Errorf("authorization response must be a private regular file (mode 0600)")
	}
	raw, e := io.ReadAll(io.LimitReader(file, (16<<10)+1))
	if e != nil || len(raw) > 16<<10 {
		return out, fmt.Errorf("bounded authorization response required")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if e = decoder.Decode(&out); e != nil {
		return out, fmt.Errorf("invalid authorization response JSON")
	}
	if e = decoder.Decode(new(any)); e != io.EOF {
		return out, fmt.Errorf("one authorization response required")
	}
	return out, nil
}
func toolAuthCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "tool-auth", Short: "Authorize downstream tools for the verified principal and actor"}
	var redirect string
	start := &cobra.Command{Use: "start <connection-id>", Args: cobra.ExactArgs(1), Short: "Prepare a registered PKCE authorization URL", RunE: func(cmd *cobra.Command, args []string) error {
		if redirect == "" {
			return fmt.Errorf("--redirect-uri is required")
		}
		value, e := newClient().StartToolAuthorization(cmd.Context(), args[0], redirect)
		if e == nil {
			toolAuthPrint(value)
		}
		return e
	}}
	start.Flags().StringVar(&redirect, "redirect-uri", "", "Exact operator-registered redirect receiver")
	complete := &cobra.Command{Use: "complete <connection-id> <private-response.json>", Args: cobra.ExactArgs(2), Short: "Exchange a callback from a private file, keeping codes out of argv", RunE: func(cmd *cobra.Command, args []string) error {
		input, e := toolAuthResponseFile(args[1])
		if e != nil {
			return e
		}
		value, e := newClient().CompleteToolAuthorization(cmd.Context(), args[0], input)
		if e == nil {
			toolAuthPrint(value)
		}
		return e
	}}
	status := &cobra.Command{Use: "status <connection-id>", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		value, e := newClient().GetToolAuthorization(cmd.Context(), args[0])
		if e == nil {
			toolAuthPrint(value)
		}
		return e
	}}
	revoke := &cobra.Command{Use: "revoke <connection-id>", Args: cobra.ExactArgs(1), Short: "Revoke this owner's downstream tool grant", RunE: func(cmd *cobra.Command, args []string) error {
		return newClient().RevokeToolAuthorization(cmd.Context(), args[0])
	}}
	cmd.AddCommand(start, complete, status, revoke)
	return cmd
}
