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
	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
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
	register := &cobra.Command{Use: "register <connection-id>", Args: cobra.ExactArgs(1), Short: "Register the connection's OAuth client with its authorization server (operators)",
		Long: `Registers the OAuth client for a connection that uses per-user OAuth
(oauth_user), using the client profile and issuer saved on the connection. The
request carries no body and no secret: a client secret is held as a reference
in secure credential storage, never given on the command line. Needs the admin
or deployer role. Run it before: aiplex tool-auth start`,
		Example: "  aiplex tool-auth register search\n  aiplex tool-auth register search -o json",
		RunE: func(cmd *cobra.Command, args []string) error {
			if e := aiplex.ValidateToolConnectionID(args[0]); e != nil {
				return usageError(fmt.Sprintf("%q is not a valid connection ID", args[0]))
			}
			record, e := newClient().RegisterToolConnection(cmd.Context(), args[0])
			if e != nil {
				return fmt.Errorf("register OAuth client for %s: %w", args[0], e)
			}
			var registration toolauth.Registration
			if machineOutput() {
				toolAuthPrint(record)
				return nil
			}
			if json.Unmarshal(record.Spec, &registration) != nil {
				return fmt.Errorf("register OAuth client for %s: the API returned a registration that cannot be read", args[0])
			}
			unknown := func(v string) string {
				if v == "" {
					return "unknown"
				}
				return v
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Registered the OAuth client for %s (revision %d).\n", args[0], record.Revision)
			fmt.Fprintf(out, "  Mode:        %s\n  Client ID:   %s\n  Issuer:      %s\n", unknown(string(registration.Mode)), unknown(registration.ClientID), unknown(registration.Issuer))
			if registration.CredentialRef != "" {
				fmt.Fprintf(out, "  Credential:  %s (reference)\n", registration.CredentialRef)
			}
			fmt.Fprintf(out, "Next: aiplex tool-auth start %s --redirect-uri <one of the registered redirect URIs>\n", args[0])
			return nil
		}}
	status := &cobra.Command{Use: "status <connection-id>", Args: cobra.ExactArgs(1), Short: "Show this owner's downstream tool grant status", RunE: func(cmd *cobra.Command, args []string) error {
		value, e := newClient().GetToolAuthorization(cmd.Context(), args[0])
		if e == nil {
			toolAuthPrint(value)
		}
		return e
	}}
	revoke := &cobra.Command{Use: "revoke <connection-id>", Args: cobra.ExactArgs(1), Short: "Revoke this owner's downstream tool grant", RunE: func(cmd *cobra.Command, args []string) error {
		return newClient().RevokeToolAuthorization(cmd.Context(), args[0])
	}}
	cmd.AddCommand(register, start, complete, status, revoke)
	return cmd
}
