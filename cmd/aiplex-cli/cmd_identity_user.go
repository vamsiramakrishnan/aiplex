package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

func identityUserCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "identity",
		Short: "Workforce identity: let people sign in with their corporate identity provider",
	}
	providers := &cobra.Command{
		Use:     "user-providers",
		Aliases: []string{"user-provider"},
		Short:   "User identity providers (Okta, Entra ID, Google Workspace, any OIDC issuer)",
		Long: `A user identity provider pins the issuer, audience, subject claim and group
claim of a corporate identity provider. Verified people get canonical
principal IDs and, when the provider verifies groups, memberships usable by
role bindings and quota eligibility.

  aiplex identity user-providers presets
  aiplex identity user-providers apply -f okta.yaml
  aiplex identity user-providers plan okta
  aiplex identity user-providers verify --token-file token.txt`,
	}
	providers.AddCommand(uipPresetsCmd(), uipListCmd(), uipGetCmd(), uipApplyCmd(), uipDeleteCmd(), uipPlanCmd(), uipVerifyCmd())
	cmd.AddCommand(providers)
	return cmd
}

func uipPresetsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "presets",
		Short: "List provider presets and the inputs each one needs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			catalog, err := newClient().ListUserIdentityPresets(cmd.Context())
			if err != nil {
				return fmt.Errorf("list user identity presets: %w", err)
			}
			if spStructured(cmd, catalog) {
				return nil
			}
			rows := make([][]string, 0, len(catalog.Presets))
			for _, p := range catalog.Presets {
				var req, opt []string
				for _, f := range p.Inputs {
					if f.Required && f.Default == "" {
						req = append(req, f.Name)
					} else {
						opt = append(opt, f.Name)
					}
				}
				rows = append(rows, []string{p.ID, spDash(p.DisplayName), spJoin(req), spJoin(opt)})
			}
			spTable(cmd.OutOrStdout(), []string{"PRESET", "NAME", "REQUIRED INPUTS", "OPTIONAL INPUTS"}, rows)
			if !catalog.LiveAuthentication {
				fmt.Fprintln(cmd.OutOrStdout(), "\nNote: live authentication is off on this server. Providers can be created and dry-run with 'verify' but do not yet authenticate requests.")
			}
			return nil
		},
	}
}

func uipListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List user identity providers",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			list, err := newClient().ListUserIdentityProviders(cmd.Context())
			if err != nil {
				return fmt.Errorf("list user identity providers: %w", err)
			}
			if spStructured(cmd, list) {
				return nil
			}
			if len(list) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No user identity providers. Add one with 'aiplex identity user-providers apply -f <file>'.")
				return nil
			}
			rows := make([][]string, 0, len(list))
			for _, p := range list {
				state := "enabled"
				if p.Disabled {
					state = "disabled"
				}
				rows = append(rows, []string{p.ID, p.Preset, state, p.Issuer, strconv.FormatUint(p.Revision, 10)})
			}
			spTable(cmd.OutOrStdout(), []string{"ID", "PRESET", "STATE", "ISSUER", "REV"}, rows)
			return nil
		},
	}
}

func uipGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <id>",
		Short: "Show one user identity provider",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := newClient().GetUserIdentityProvider(cmd.Context(), args[0])
			if err != nil {
				return fmt.Errorf("get user identity provider: %w", err)
			}
			if spStructured(cmd, p) {
				return nil
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "User identity provider %s (revision %d)\n", p.ID, p.Revision)
			fmt.Fprintf(out, "  Preset:    %s\n  Issuer:    %s\n  Audience:  %s\n  Subject:   %s claim\n  Pool:      %s\n", p.Preset, p.Issuer, p.Audience, p.SubjectClaim, p.PrincipalPool)
			if p.Disabled {
				fmt.Fprintln(out, "  State:     disabled")
			}
			if p.Directory != nil {
				fmt.Fprintf(out, "  Directory: %s (%s)\n", p.Directory.Kind, p.DirectoryMode())
			}
			if len(p.Extra) > 0 {
				keys := make([]string, 0, len(p.Extra))
				for k := range p.Extra {
					keys = append(keys, k)
				}
				fmt.Fprintf(out, "  Other fields (use -o json): %s\n", strings.Join(keys, ", "))
			}
			return nil
		},
	}
}

func uipApplyCmd() *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "apply -f <file>",
		Short: "Create or replace a user identity provider from a JSON or YAML file",
		Long: `The file holds the provider id and the request fields:

  id: okta
  preset: okta
  inputs: { domain: acme.okta.com }
  display_name: Acme Okta

Fields this CLI does not know about are sent to the server unchanged; for
example "acknowledge_overlap: true" saves a provider even though an agent
identity binding trusts the same issuer and audience (the server's warning is
printed). The
provider is created, or replaced at its current revision when it exists.
Use '-f -' to read stdin.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := spReadSpec(file, cmd.InOrStdin())
			if err != nil {
				return err
			}
			id, req, err := uipDecode(raw)
			if err != nil {
				return err
			}
			c := newClient()
			var expected uint64
			existing, err := c.GetUserIdentityProvider(cmd.Context(), id)
			switch {
			case err == nil:
				expected = existing.Revision
			case spIsNotFound(err):
			default:
				return fmt.Errorf("read user identity provider: %w", err)
			}
			saved, err := c.PutUserIdentityProvider(cmd.Context(), id, req, expected)
			if err != nil {
				return spExplain("apply user identity provider", err)
			}
			if spStructured(cmd, saved) {
				return nil
			}
			verb := "Created"
			if expected > 0 {
				verb = "Updated"
			}
			for _, w := range saved.Warnings() {
				fmt.Fprintf(cmd.OutOrStdout(), "Warning: %s\n", w)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s user identity provider %s (revision %d)\n  Issuer: %s\n  Audience: %s\nNext: aiplex identity user-providers plan %s\n", verb, saved.ID, saved.Revision, saved.Issuer, saved.Audience, saved.ID)
			return nil
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "JSON or YAML file, or - for stdin")
	return cmd
}

// uipDecode splits the provider id from the request body, keeping every
// unrecognised field.
func uipDecode(raw []byte) (string, aiplex.UserIdentityProviderRequest, error) {
	var req aiplex.UserIdentityProviderRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return "", req, fmt.Errorf("invalid provider file: %w", err)
	}
	var id string
	if rawID, ok := req.Extra["id"]; ok {
		if err := json.Unmarshal(rawID, &id); err != nil {
			return "", req, fmt.Errorf("id must be a string")
		}
		delete(req.Extra, "id")
	}
	if id == "" {
		return "", req, fmt.Errorf("the file needs an id")
	}
	if req.Preset == "" {
		return "", req, fmt.Errorf("the file needs a preset (see 'identity user-providers presets')")
	}
	if len(req.Extra) == 0 {
		req.Extra = nil
	}
	return id, req, nil
}

func uipDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <id>",
		Short: "Remove a user identity provider",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			current, err := c.GetUserIdentityProvider(cmd.Context(), args[0])
			if err != nil {
				return fmt.Errorf("read user identity provider: %w", err)
			}
			if err = c.DeleteUserIdentityProvider(cmd.Context(), args[0], current.Revision); err != nil {
				return spExplain("delete user identity provider", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted user identity provider %s\n", args[0])
			return nil
		},
	}
}

func uipPlanCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "plan <id>",
		Short: "Show the setup steps for a user identity provider",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			plan, err := newClient().PlanUserIdentityProvider(cmd.Context(), args[0])
			if err != nil {
				return fmt.Errorf("plan user identity provider: %w", err)
			}
			if spStructured(cmd, plan) {
				return nil
			}
			uipRenderPlan(cmd.OutOrStdout(), plan)
			return nil
		},
	}
}

func uipRenderPlan(out io.Writer, p *aiplex.UserIdentityPlan) {
	fmt.Fprintf(out, "User identity plan: provider %s (%s)\n", p.Provider.ID, p.Vendor)
	fmt.Fprintf(out, "  Issuer:       %s\n  Audience:     %s\n  Subject:      %s claim\n  Principal pool: %s\n", p.Issuer, p.Audience, p.SubjectClaim, p.PrincipalPool)
	section := func(title string, steps []aiplex.AgentIdentityStep, numbered bool) {
		if len(steps) == 0 {
			return
		}
		fmt.Fprintf(out, "\n%s\n", title)
		for i, s := range steps {
			label := s.Title
			if numbered {
				label = fmt.Sprintf("%d. %s", i+1, s.Title)
			}
			fmt.Fprintf(out, "\n%s\n", label)
			if s.Description != "" {
				fmt.Fprintf(out, "   %s\n", s.Description)
			}
			if s.Content != "" {
				fmt.Fprintln(out)
				for _, line := range strings.Split(strings.TrimRight(s.Content, "\n"), "\n") {
					fmt.Fprintf(out, "     %s\n", line)
				}
			}
		}
	}
	section("In the identity provider", p.Steps, true)
	if p.TokenSnippet.Title != "" {
		section("Getting a token", []aiplex.AgentIdentityStep{p.TokenSnippet}, false)
	}
	section("In AIPlex", p.AIPlex, true)
	if len(p.Notes) > 0 {
		fmt.Fprintln(out, "\nNotes")
		for _, n := range p.Notes {
			fmt.Fprintf(out, "  - %s\n", n)
		}
	}
}

func uipVerifyCmd() *cobra.Command {
	var (
		provider  string
		tokenFile string
	)
	cmd := &cobra.Command{
		Use:   "verify --token-file <path|-> [--provider <id>]",
		Short: "Dry-run a user token against the identity providers",
		Long: `Check what AIPlex would make of a token issued by your identity provider,
without authenticating anything. Every check is listed with its reason; the
exit status is non-zero when the token is not valid.

The token is read from a file or stdin only. It is never accepted as a flag,
never printed, and never logged. (The global --token flag is your own AIPlex
API bearer token, not the user token being tested.)

Examples:
  aiplex identity user-providers verify --token-file token.txt
  cat token.txt | aiplex identity user-providers verify --token-file - --provider okta`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			tok, err := readIdentityToken(tokenFile, cmd.InOrStdin())
			if err != nil {
				return err
			}
			result, err := newClient().VerifyUserIdentityToken(cmd.Context(), aiplex.UserIdentityVerifyRequest{Token: tok, ProviderID: provider})
			tok = ""
			if err != nil {
				return spExplain("verify user identity token", err)
			}
			if !spStructured(cmd, result) {
				uipRenderVerify(cmd.OutOrStdout(), result)
			}
			if !result.Valid {
				return fmt.Errorf("token is not valid")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&provider, "provider", "", "Provider ID to pin the check to")
	cmd.Flags().StringVar(&tokenFile, "token-file", "", "File containing the token, or - for stdin")
	return cmd
}

func uipRenderVerify(out io.Writer, r *aiplex.UserIdentityVerifyResult) {
	rows := make([][]string, 0, len(r.Checks))
	for _, c := range r.Checks {
		label := "SKIP"
		switch c.Status {
		case aiplex.AgentIdentityCheckPass:
			label = "PASS"
		case aiplex.AgentIdentityCheckFail:
			label = "FAIL"
		}
		rows = append(rows, []string{label, c.Name, c.Reason})
	}
	spTable(out, []string{"", "CHECK", "REASON"}, rows)
	fmt.Fprintln(out)
	if !r.Valid {
		fmt.Fprintln(out, "Result: NOT valid")
		return
	}
	fmt.Fprintln(out, "Result: valid")
	if r.Provider != nil {
		fmt.Fprintf(out, "  Provider:  %s\n", r.Provider.ID)
	}
	if r.PrincipalID != "" {
		fmt.Fprintf(out, "  Principal: %s\n", r.PrincipalID)
	}
	switch {
	case r.GroupsAvailable && r.Groups != nil:
		fmt.Fprintf(out, "  Groups:    %s\n", spJoin(*r.Groups))
	default:
		note := r.GroupsNote
		if note == "" {
			note = "not available"
		}
		fmt.Fprintf(out, "  Groups:    unknown (%s)\n", note)
	}
	if !r.LiveAuthentication {
		fmt.Fprintln(out, "  Note: live authentication is off on this server; this token would not yet authenticate requests.")
	}
}
