package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/vamsiramakrishnan/aiplex/internal/cliconfig"
	"github.com/vamsiramakrishnan/aiplex/internal/devstack"
	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

func devTokenCmd() *cobra.Command {
	var user, role, groups, issuer, tenant string
	cmd := &cobra.Command{
		Use:   "token --user <name> [--role none|deployer|admin] [--groups a,b] | --issuer branchyard --tenant <name>",
		Short: "Mint a local token for another caller, to see role-based behaviour",
		Long: `Prints a token for a second local caller, so you can see what a caller with no
role, a deployer or another admin gets, and try identity federation.

The token is printed on stdout (nothing else), so it can be captured; what it
is for goes to stderr. It is valid for an hour, only for the local API that
issued it, and is minted by the local token issuer (loopback only), which
accepts the request only with your own developer token.

  --issuer local         (default) signed by the same local issuer as your own
                         token: email <name>@localhost, subject <name>
  --issuer idp           a stand-in workforce identity provider: email
                         <name>@idp.localhost. The API refuses it until you
                         apply a user identity provider for it (the command is
                         printed on stderr); then it is a user with no role
  --issuer workload      a stand-in workload identity provider, for agents:
                         subject <name>, no email. Bind an agent to it with
                         "aiplex agents identity bind" (printed on stderr)
  --issuer unconfigured  an issuer nobody configured; its tokens are refused
                         (401), which is its use
  --issuer branchyard    a stand-in Branchyard server (Branchyard is a separate
                         product that runs coding agents on git branches; its
                         server signs a short-lived token for each turn): an
                         EdDSA (Ed25519) turn token for the Branchyard tenant
                         --tenant <name> (subject branchyard:<name>,
                         by_purpose aiplex). Takes --tenant instead of
                         --user. The API refuses it until an agent is bound
                         with the branchyard preset (printed on stderr)

  --role none      (default) no role: the caller sees only the access points
                   that admit it. Admission is not a grant: to call a model
                   it needs llm:model:<model> and llm:surface:<surface>:*,
                   set with "aiplex agents user-scopes set" and the
                   principal ID this command prints
  --role deployer  configuration screens and commands, no admin-only actions
  --role admin     the same role as you
  --groups         extra identity-provider groups in the token (for access
                   points that admit a group)

--role applies to the local issuer only: a token from idp, workload or
unconfigured never carries a role (roles come from role bindings), and --role
is refused with them.

With the local issuer the caller's email is <name>@localhost, and its subject is
<name>, so "aiplex access-points create team --allow-users <name>@localhost ..."
admits it.`,
		Example: `  aiplex dev token --user bob
  aiplex dev token --user dana --role deployer
  aiplex dev token --user carol --groups eng,research
  aiplex dev token --user alice --issuer idp      # needs a user identity provider for the local idp
  aiplex dev token --user ci-1 --issuer workload  # needs an agent identity binding
  aiplex dev token --issuer branchyard --tenant acme  # needs a branchyard binding`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if issuer == devstack.IssuerBranchyard {
				if strings.TrimSpace(tenant) == "" {
					return usageError("--tenant is required with --issuer branchyard", "For example: aiplex dev token --issuer branchyard --tenant acme")
				}
			} else if strings.TrimSpace(user) == "" {
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
			body := devstack.DevTokenRequest{User: user, Role: role, Issuer: issuer, Tenant: tenant}
			for _, g := range strings.Split(groups, ",") {
				if g = strings.TrimSpace(g); g != "" {
					body.Groups = append(body.Groups, g)
				}
			}
			out, err := mintDevToken(cmd.Context(), st.IssuerURL, entry.AccessToken, body)
			if err != nil {
				return err
			}
			if spStructured(cmd, out) {
				return nil
			}
			stderr := cmd.ErrOrStderr()
			if out.Issuer != "" && out.Issuer != devstack.IssuerLocal {
				printFederatedTokenHelp(stderr, out)
				fmt.Fprintln(cmd.OutOrStdout(), out.Token)
				return nil
			}
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
	cmd.Flags().StringVar(&user, "user", "", "Name of the caller to mint a token for (its email is <name>@localhost; <name>@idp.localhost with --issuer idp)")
	cmd.Flags().StringVar(&role, "role", "none", "Role: none, deployer or admin")
	cmd.Flags().StringVar(&issuer, "issuer", devstack.IssuerLocal, "Who signs the token: "+strings.Join(devstack.IssuerNames, ", "))
	cmd.Flags().StringVar(&groups, "groups", "", "Extra groups for the token, comma separated")
	cmd.Flags().StringVar(&tenant, "tenant", "", "Branchyard tenant of a --issuer branchyard token (its subject is branchyard:<tenant>)")
	return cmd
}

// mintDevToken asks the local issuer for a token and refuses an answer that is
// not for the issuer requested: a stack started by an older aiplex dev ignores
// the issuer field and would answer with a local token.
func mintDevToken(ctx context.Context, issuerURL, bearer string, body devstack.DevTokenRequest) (devstack.DevTokenResponse, error) {
	var out devstack.DevTokenResponse
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(issuerURL, "/")+devstack.DevTokenPath, bytes.NewReader(raw))
	if err != nil {
		return out, err
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Content-Type", "application/json")
	resp, err := authHTTPClient(10 * time.Second).Do(req)
	if err != nil {
		return out, fmt.Errorf("the local token issuer at %s is not answering; is aiplex dev still running? (%w)", issuerURL, err)
	}
	defer resp.Body.Close()
	if err := aiplex.RefusedRedirect(req.URL, resp); err != nil {
		return out, err
	}
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &e)
		if e.Error == "" {
			e.Error = strings.TrimSpace(string(data))
		}
		return out, usageError("aiplex dev token: " + e.Error)
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return out, fmt.Errorf("unexpected answer from the token issuer: %w", err)
	}
	want := body.Issuer
	if want == "" {
		want = devstack.IssuerLocal
	}
	got := out.Issuer
	if got == "" {
		got = devstack.IssuerLocal // an older stack names no issuer: its tokens are local
	}
	if got != want {
		return devstack.DevTokenResponse{}, fmt.Errorf("the running aiplex dev answered with a %s token, not the %s token you asked for; it is older than this aiplex and has no federation issuers. Restart it: aiplex dev stop && aiplex dev --detach", got, want)
	}
	return out, nil
}

// printFederatedTokenHelp says, on stderr, what the API needs before it trusts
// a token from a federation issuer. Everything printed is public configuration
// (issuer name, JWKS address, audience), never the token.
func printFederatedTokenHelp(w io.Writer, out devstack.DevTokenResponse) {
	who := out.Subject
	if out.Email != "" {
		who = out.Email
	}
	fmt.Fprintf(w, "Token for %s from the %s issuer (valid %s, this local API only)\n", who, out.Issuer, time.Duration(out.ExpiresIn)*time.Second)
	fmt.Fprintf(w, "  issuer     %s\n  jwks       %s\n  audience   %s\n", out.IssuerURL, out.JWKSURL, out.Audience)
	if out.PrincipalID != "" {
		fmt.Fprintf(w, "  principal  %s\n", out.PrincipalID)
	}
	if len(out.Groups) > 0 {
		fmt.Fprintf(w, "  groups     %s\n", strings.Join(out.Groups, ", "))
	}
	switch out.Issuer {
	case devstack.IssuerIDP:
		fmt.Fprintf(w, "The API refuses this issuer until a user identity provider names it (once):\n")
		fmt.Fprintf(w, "  aiplex identity user-providers apply -f - <<'EOF'\nid: local-idp\npreset: oidc\ndisplay_name: Local IdP\ninputs:\n  issuer: %s\n  jwks_url: %s\n  audience: %s\n  groups_from: claim\n  group_claim: groups\nEOF\n", out.IssuerURL, out.JWKSURL, out.Audience)
		fmt.Fprintf(w, "Use it:  AIPLEX_TOKEN=$(aiplex dev token --user %s --issuer idp) aiplex whoami\n", out.Subject)
		fmt.Fprintf(w, "It has no role: roles come from role bindings. To call a model it needs a grant:\n  aiplex agents user-scopes set %s --scopes 'llm:model:mock-gpt,llm:surface:local-models:*'\n", out.PrincipalID)
	case devstack.IssuerWorkload:
		fmt.Fprintf(w, "The API refuses this issuer until an agent is bound to it (once):\n")
		fmt.Fprintf(w, "  aiplex agents register tutor --name Tutor --scopes 'llm:model:mock-gpt,llm:surface:local-models:*'\n")
		fmt.Fprintf(w, "  aiplex agents identity bind tutor ci --preset oidc --input issuer=%s --input jwks_url=%s --input audience=%s --input subject=%s\n", out.IssuerURL, out.JWKSURL, out.Audience, out.Subject)
		fmt.Fprintf(w, "Use it:  AIPLEX_TOKEN=$(aiplex dev token --user %s --issuer workload) aiplex whoami\n", out.Subject)
		fmt.Fprintf(w, "The binding grants nothing by itself. To call a model the agent needs a grant:\n  aiplex agents user-scopes set %s --scopes 'llm:model:mock-gpt,llm:surface:local-models:*'\n", out.PrincipalID)
	case devstack.IssuerBranchyard:
		fmt.Fprintf(w, "The API refuses this issuer until an agent is bound to it (once):\n")
		fmt.Fprintf(w, "  aiplex agents register branchyard-agent --name 'Branchyard %s' --scopes 'llm:model:mock-gpt,llm:surface:local-models:*'\n", out.Tenant)
		fmt.Fprintf(w, "  aiplex agents identity bind branchyard-agent branchyard --preset branchyard --input server=%s --input tenant=%s --input audience=%s\n", out.IssuerURL, out.Tenant, out.Audience)
		fmt.Fprintf(w, "Use it:  AIPLEX_TOKEN=$(aiplex dev token --issuer branchyard --tenant %s) aiplex whoami\n", out.Tenant)
		fmt.Fprintf(w, "The binding grants nothing by itself. To call a model the agent needs a grant:\n  aiplex agents user-scopes set %s --scopes 'llm:model:mock-gpt,llm:surface:local-models:*'\n", out.PrincipalID)
	default:
		fmt.Fprintln(w, "Nothing configures this issuer, so the API refuses the token (401). That is its use.")
	}
}
