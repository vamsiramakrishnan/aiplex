package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func whoamiCmd() *cobra.Command {
	var server bool
	command := &cobra.Command{
		Use:   "whoami",
		Short: "Show which API and identity the CLI is using, and your roles",
		Long: `Shows where the CLI sends requests and with which token (and where each came
from: a flag, AIPLEX_URL / AIPLEX_TOKEN, or the context), then asks the API who
that token is: subject, email, roles and grants.

--server (or -o json|yaml) prints only the API's answer, for scripts.`,
		Example: `  aiplex whoami
  aiplex whoami --server -o json
  aiplex --context prod whoami`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if server || machineOutput() {
				identity, err := newClient().GetIdentity(cmd.Context())
				if err != nil {
					return fmt.Errorf("whoami: %w", err)
				}
				if output == "yaml" {
					printYAML(identity)
				} else {
					printJSON(identity)
				}
				return nil
			}
			t := resolveTarget()
			out := cmd.OutOrStdout()
			ctxName := t.Context
			if ctxName == "" {
				ctxName = "(none)"
			}
			fmt.Fprintf(out, "Context:  %s\n", ctxName)
			fmt.Fprintf(out, "API:      %s  (from %s)\n", t.URL, t.URLSource)
			switch {
			case t.Token == "":
				fmt.Fprintln(out, "Token:    none")
			default:
				line := "from " + t.TokenSource
				if exp, ok := jwtExpiry(t.Token); ok {
					if time.Now().After(exp) {
						line += ", expired " + relTimeFrom(exp, time.Now())
					} else {
						line += ", expires " + relTimeFrom(exp, time.Now())
					}
				}
				fmt.Fprintf(out, "Token:    %s\n", line)
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Second)
			defer cancel()
			identity, err := newClient().GetIdentity(ctx)
			if err != nil {
				ce := explain(cmd, nil, err)
				fmt.Fprintf(out, "Identity: unknown (%s)\n", ce.msg)
				for _, h := range ce.hints {
					fmt.Fprintln(out, "  "+h)
				}
				if ce.code == exitAuth && t.Token == "" {
					// Not signed in is an answer, not a failure.
					return nil
				}
				return &cliError{msg: "could not confirm your identity with the API", code: ce.code, err: err}
			}
			id := identity.Identity
			name := id.Email
			if name == "" {
				name = id.Subject
			}
			if name == "" {
				fmt.Fprintln(out, "Identity: anonymous (this API admits unauthenticated requests: development mode)")
			} else {
				fmt.Fprintf(out, "Identity: %s\n", name)
				if id.Subject != "" && id.Subject != name {
					fmt.Fprintf(out, "Subject:  %s\n", id.Subject)
				}
				fmt.Fprintf(out, "ID:       %s\n", id.PrincipalID)
			}
			if len(id.Groups) > 0 {
				fmt.Fprintf(out, "Groups:   %s\n", strings.Join(id.Groups, ", "))
			}
			roles := "none"
			if len(identity.Roles) > 0 {
				roles = strings.Join(identity.Roles, ", ")
			}
			fmt.Fprintf(out, "Roles:    %s\n", roles)
			scopes := append(append([]string{}, identity.Scopes...), identity.GroupScopes...)
			if len(scopes) > 0 {
				fmt.Fprintf(out, "Grants:   %s\n", strings.Join(dedupe(scopes), " "))
			} else {
				fmt.Fprintln(out, "Grants:   none")
			}
			return nil
		},
	}
	command.Flags().BoolVar(&server, "server", false, "Print only the API's identity answer (principal ID, groups, roles, scopes)")
	return command
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}
