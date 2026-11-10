package main

import (
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/spf13/cobra"

	"github.com/vamsiramakrishnan/aiplex/internal/mcpserve"
)

func mcpServeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Run a read-only MCP server on stdio that lets a coding agent inspect AIPlex",
		Long: `Serves the Model Context Protocol on stdin and stdout. Point an MCP client
(Claude Code, Codex, Gemini CLI) at the command "aiplex mcp serve".

Tools, all read-only and made with your own token and context:
  whoami                who AIPlex sees you as, with roles and scopes
  list_access_points    the access points you can use
  get_connect_config    what "aiplex connect" prints, for an access point and client
  list_models           the models an access point offers
  usage_summary         totals of your own usage
  usage_records         a bounded page of your own usage records

Nothing is changed through this server. Errors carry the API's message; the
token is never included. Diagnostics go to stderr, never to stdout.`,
		Example: "  claude mcp add aiplex -- aiplex mcp serve\n  aiplex --context prod mcp serve",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			t := resolveTarget()
			s := &mcpserve.Server{Client: newClient(), Version: version, Log: os.Stderr}
			if t.Token != "" {
				s.Secrets = []string{t.Token}
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
			defer stop()
			if err := s.Serve(ctx, os.Stdin, os.Stdout); err != nil && ctx.Err() == nil {
				return fmt.Errorf("mcp serve: %w", err)
			}
			return nil
		},
	}
}

func authCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "auth",
		Short:   "Credential helpers for tools that fetch a token by running a command",
		Example: "  aiplex auth print-token",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "print-token",
		Short: "Print the access token this CLI would use, for a client's credential helper",
		Long: `Prints the current access token on stdout and nothing else, so a client can run
it as a credential helper: Claude Code's apiKeyHelper and Codex's
model_providers.<id>.auth.command do. "aiplex connect --write --token-helper"
configures exactly that, so the token never has to be written into a file.

The token comes from --token, AIPLEX_TOKEN or the current context, refreshed
when it is about to expire. Exits 3 when there is none or it has expired.
Treat the output as a secret.`,
		Example: "  aiplex auth print-token\n  aiplex --context prod auth print-token",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			t := resolveTarget()
			if t.Token == "" {
				return newCLIError(exitAuth, "no access token: not signed in", "Sign in with: aiplex login", "or export AIPLEX_TOKEN")
			}
			if !t.ExpiresAt.IsZero() && time.Now().After(t.ExpiresAt) {
				return newCLIError(exitAuth, "the access token has expired", "Sign in again with: aiplex login")
			}
			fmt.Fprintln(cmd.OutOrStdout(), t.Token)
			return nil
		},
	})
	return cmd
}
