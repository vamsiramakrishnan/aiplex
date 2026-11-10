package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/vamsiramakrishnan/aiplex/internal/cliconfig"
	"github.com/vamsiramakrishnan/aiplex/internal/devstack"
)

func devTokenCmd() *cobra.Command {
	var user, role, groups string
	cmd := &cobra.Command{
		Use:   "token --user <name> [--role none|deployer|admin] [--groups a,b]",
		Short: "Mint a local token for another caller, to see role-based behaviour",
		Long: `Prints a token for a second local caller, signed by the same local issuer as your
own, so you can see what a caller with no role, a deployer or another admin gets.

The token is printed on stdout (nothing else), so it can be captured; what it
is for goes to stderr. It is valid for an hour, only for the local API that
issued it, and is minted by the local token issuer (loopback only), which
accepts the request only with your own developer token.

  --role none      (default) no role: the caller sees only the access points
                   that admit it. Admission is not a grant: to call a model
                   it needs llm:model:<model> and llm:surface:<surface>:*,
                   set with "aiplex agents user-scopes set" and the
                   principal ID this command prints
  --role deployer  configuration screens and commands, no admin-only actions
  --role admin     the same role as you
  --groups         extra identity-provider groups in the token (for access
                   points that admit a group)

The caller's email is <name>@localhost, and its subject is <name>, so
"aiplex access-points create team --allow-users <name>@localhost ..." admits it.`,
		Example: `  aiplex dev token --user bob
  aiplex dev token --user dana --role deployer
  aiplex dev token --user carol --groups eng,research`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(user) == "" {
				return usageError("--user is required", "For example: aiplex dev token --user bob")
			}
			st, _, err := readDevState()
			if err != nil {
				return err
			}
			if st == nil {
				return fmt.Errorf("no local AIPlex is running; start one with: aiplex dev")
			}
			if st.IssuerURL == "" {
				return fmt.Errorf("the running aiplex dev does not report its token issuer; restart it with: aiplex dev stop && aiplex dev --detach")
			}
			creds, err := cliconfig.LoadCredentials()
			if err != nil {
				return err
			}
			entry := creds.GetToken(st.Context)
			if entry == nil || entry.AccessToken == "" {
				return fmt.Errorf("no developer token stored for context %q; restart: aiplex dev", st.Context)
			}
			body := devstack.DevTokenRequest{User: user, Role: role}
			for _, g := range strings.Split(groups, ",") {
				if g = strings.TrimSpace(g); g != "" {
					body.Groups = append(body.Groups, g)
				}
			}
			raw, _ := json.Marshal(body)
			req, err := http.NewRequestWithContext(cmd.Context(), http.MethodPost, strings.TrimRight(st.IssuerURL, "/")+devstack.DevTokenPath, bytes.NewReader(raw))
			if err != nil {
				return err
			}
			req.Header.Set("Authorization", "Bearer "+entry.AccessToken)
			req.Header.Set("Content-Type", "application/json")
			resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
			if err != nil {
				return fmt.Errorf("the local token issuer at %s is not answering; is aiplex dev still running? (%w)", st.IssuerURL, err)
			}
			defer resp.Body.Close()
			data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			if resp.StatusCode != http.StatusOK {
				var e struct {
					Error string `json:"error"`
				}
				_ = json.Unmarshal(data, &e)
				if e.Error == "" {
					e.Error = strings.TrimSpace(string(data))
				}
				return usageError("aiplex dev token: " + e.Error)
			}
			var out devstack.DevTokenResponse
			if err := json.Unmarshal(data, &out); err != nil {
				return fmt.Errorf("unexpected answer from the token issuer: %w", err)
			}
			if spStructured(cmd, out) {
				return nil
			}
			stderr := cmd.ErrOrStderr()
			fmt.Fprintf(stderr, "Token for %s (role %s, valid %s, this local API only)\n", out.Email, out.Role, time.Duration(out.ExpiresIn)*time.Second)
			fmt.Fprintf(stderr, "  principal  %s\n", out.PrincipalID)
			if len(out.Groups) > 0 {
				fmt.Fprintf(stderr, "  groups     %s\n", strings.Join(out.Groups, ", "))
			}
			fmt.Fprintf(stderr, "Use it:  AIPLEX_TOKEN=$(aiplex dev token --user %s) aiplex whoami\n", out.Subject)
			if out.Role == "none" {
				fmt.Fprintf(stderr, "Without a role this caller sees only access points that admit it, and needs model grants:\n  aiplex agents user-scopes set %s --scopes llm:model:mock-gpt,llm:surface:local-models:*\n", out.PrincipalID)
			}
			fmt.Fprintln(cmd.OutOrStdout(), out.Token)
			return nil
		},
	}
	cmd.Flags().StringVar(&user, "user", "", "Name of the caller to mint a token for (its email is <name>@localhost)")
	cmd.Flags().StringVar(&role, "role", "none", "Role: none, deployer or admin")
	cmd.Flags().StringVar(&groups, "groups", "", "Extra groups for the token, comma separated")
	return cmd
}
