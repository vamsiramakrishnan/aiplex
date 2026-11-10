package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

// maxIdentityTokenBytes bounds the token read from a file or stdin. The API
// rejects bodies above 80 KiB, so anything near this is not a JWT.
const maxIdentityTokenBytes = 64 << 10

func agentIdentityCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "identity",
		Aliases: []string{"identities"},
		Short:   "Bind external cloud workload identities to an agent",
		Long: `Let an agent running in another cloud (GCP, AWS, Azure, GitHub Actions,
Kubernetes, SPIFFE, any OIDC issuer) authenticate to AIPlex with its native
workload identity instead of a stored secret.

Typical flow:
  aiplex agents identity presets
  aiplex agents identity bind tutor-agent gh-ci --preset github-actions \
    --input repository=acme/tutor --input ref=refs/heads/main
  aiplex agents identity plan tutor-agent gh-ci
  aiplex agents identity verify --agent tutor-agent --binding gh-ci --token-file token.txt`,
	}
	cmd.AddCommand(
		agentIdentityPresetsCmd(),
		agentIdentityListCmd(),
		agentIdentityGetCmd(),
		agentIdentityBindCmd(),
		agentIdentityDeleteCmd(),
		agentIdentityPlanCmd(),
		agentIdentityVerifyCmd(),
	)
	return cmd
}

// printStructured writes JSON or YAML when requested. It reports whether it did.
func printStructured(cmd *cobra.Command, value any) bool {
	switch output {
	case "json":
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		_ = enc.Encode(value)
		return true
	case "yaml":
		printYAML(value)
		return true
	}
	return false
}

func agentIdentityPresetsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "presets",
		Short: "List identity presets and the inputs each one needs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			catalog, err := newClient().ListAgentIdentityPresets(cmd.Context())
			if err != nil {
				return fmt.Errorf("list identity presets: %w", err)
			}
			if printStructured(cmd, catalog) {
				return nil
			}
			out := cmd.OutOrStdout()
			w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "PRESET\tCLOUD\tREQUIRED INPUTS\tOPTIONAL INPUTS")
			for _, p := range catalog.Presets {
				var req, opt []string
				for _, f := range p.Inputs {
					if f.Required && f.Default == "" {
						req = append(req, f.Name)
					} else {
						opt = append(opt, f.Name)
					}
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", p.ID, p.Cloud, dash(strings.Join(req, ",")), dash(strings.Join(opt, ",")))
			}
			w.Flush()
			if !catalog.LiveAuthentication {
				fmt.Fprintln(out, "\nNote: live authentication is off on this server. Bindings can be created and dry-run with 'identity verify' but do not yet authenticate requests.")
			}
			return nil
		},
	}
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func agentIdentityListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list <agent>",
		Short: "List identity bindings of an agent",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			bindings, err := newClient().ListAgentIdentities(cmd.Context(), args[0])
			if err != nil {
				return fmt.Errorf("list identities: %w", err)
			}
			if printStructured(cmd, bindings) {
				return nil
			}
			out := cmd.OutOrStdout()
			if len(bindings) == 0 {
				fmt.Fprintf(out, "No identity bindings for agent %s. Add one with 'aiplex agents identity bind'.\n", args[0])
				return nil
			}
			w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tPRESET\tSTATE\tISSUER\tSUBJECT\tREV")
			for _, b := range bindings {
				state := "enabled"
				if b.Disabled {
					state = "disabled"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%d\n", b.ID, b.Preset, state, b.Issuer, b.Subject, b.Revision)
			}
			return w.Flush()
		},
	}
}

func agentIdentityGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <agent> <id>",
		Short: "Show one identity binding",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			b, err := newClient().GetAgentIdentity(cmd.Context(), args[0], args[1])
			if err != nil {
				return fmt.Errorf("get identity: %w", err)
			}
			if printStructured(cmd, b) {
				return nil
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Binding:     %s (agent %s, revision %d)\n", b.ID, b.ClientID, b.Revision)
			if b.DisplayName != "" {
				fmt.Fprintf(out, "Name:        %s\n", b.DisplayName)
			}
			fmt.Fprintf(out, "Preset:      %s\n", b.Preset)
			fmt.Fprintf(out, "Disabled:    %t\n", b.Disabled)
			fmt.Fprintf(out, "Issuer:      %s\n", b.Issuer)
			fmt.Fprintf(out, "Subject:     %s (%s claim)\n", b.Subject, b.SubjectClaim)
			fmt.Fprintf(out, "Audience:    %s\n", b.Audience)
			fmt.Fprintf(out, "JWKS URL:    %s\n", b.JWKSURL)
			fmt.Fprintf(out, "Principal:   %s\n", b.PrincipalID)
			if b.ActorID != "" {
				fmt.Fprintf(out, "Actor:       %s\n", b.ActorID)
			}
			for _, k := range sortedKeys(b.Inputs) {
				fmt.Fprintf(out, "Input:       %s=%s\n", k, b.Inputs[k])
			}
			for _, k := range sortedKeys(b.ExtraClaims) {
				fmt.Fprintf(out, "Claim:       %s=%s\n", k, b.ExtraClaims[k])
			}
			return nil
		},
	}
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// parseKeyValues turns repeated key=value flags into a map, rejecting
// malformed and duplicate entries.
func parseKeyValues(flag string, pairs []string) (map[string]string, error) {
	out := map[string]string{}
	for _, pair := range pairs {
		key, value, ok := strings.Cut(pair, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return nil, fmt.Errorf("--%s %q must be key=value", flag, pair)
		}
		if _, dup := out[key]; dup {
			return nil, fmt.Errorf("--%s %q given more than once", flag, key)
		}
		out[key] = value
	}
	return out, nil
}

// validateIdentityInputs checks inputs against the preset catalog so a user
// sees every missing or unknown input at once instead of one server error.
func validateIdentityInputs(catalog *aiplex.AgentIdentityPresetCatalog, presetID string, inputs map[string]string) error {
	preset, ok := catalog.Preset(presetID)
	if !ok {
		ids := make([]string, 0, len(catalog.Presets))
		for _, p := range catalog.Presets {
			ids = append(ids, p.ID)
		}
		return fmt.Errorf("unknown preset %q; available presets: %s", presetID, strings.Join(ids, ", "))
	}
	known := map[string]aiplex.AgentIdentityField{}
	for _, f := range preset.Inputs {
		known[f.Name] = f
	}
	var problems []string
	var unknown []string
	for name := range inputs {
		if _, ok := known[name]; !ok {
			unknown = append(unknown, name)
		}
	}
	sort.Strings(unknown)
	if len(unknown) > 0 {
		names := make([]string, 0, len(preset.Inputs))
		for _, f := range preset.Inputs {
			names = append(names, f.Name)
		}
		problems = append(problems, fmt.Sprintf("unknown input(s) for preset %s: %s (valid: %s)", presetID, strings.Join(unknown, ", "), strings.Join(names, ", ")))
	}
	var missing []string
	for _, f := range preset.Inputs {
		if f.Required && f.Default == "" && inputs[f.Name] == "" {
			line := "  --input " + f.Name + "=<" + f.Label + ">"
			if f.Example != "" {
				line = "  --input " + f.Name + "=" + f.Example
			}
			if len(f.Enum) > 0 {
				line += "   (one of: " + strings.Join(f.Enum, ", ") + ")"
			}
			missing = append(missing, line)
		}
	}
	if len(missing) > 0 {
		problems = append(problems, fmt.Sprintf("preset %s is missing required input(s):\n%s", presetID, strings.Join(missing, "\n")))
	}
	for name, value := range inputs {
		f, ok := known[name]
		if !ok || len(f.Enum) == 0 || value == "" {
			continue
		}
		valid := false
		for _, e := range f.Enum {
			valid = valid || e == value
		}
		if !valid {
			problems = append(problems, fmt.Sprintf("input %s must be one of: %s", name, strings.Join(f.Enum, ", ")))
		}
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "\n"))
	}
	return nil
}

func agentIdentityBindCmd() *cobra.Command {
	var (
		preset      string
		inputs      []string
		claims      []string
		displayName string
		disabled    bool
		replace     bool
		ackOverlap  bool
	)
	cmd := &cobra.Command{
		Use:   "bind <agent> <id> --preset <preset> --input key=value ...",
		Short: "Bind an external workload identity to an agent",
		Long: `Create an identity binding. <id> is a lower-case DNS label (for example gh-ci).
Run 'aiplex agents identity presets' to see each preset's inputs. The server
derives the exact issuer, subject and JWKS URL from the inputs.

Use --replace to update an existing binding (the current revision is read
and sent as If-Match, so concurrent edits are rejected, not overwritten).

Examples:
  aiplex agents identity bind tutor-agent gh-ci --preset github-actions \
    --input repository=acme/tutor --input ref=refs/heads/main
  aiplex agents identity bind tutor-agent aws-prod --preset aws-sts-web-identity \
    --input issuer=https://abc123-def456-ghi789-jkl012.tokens.sts.global.api.aws \
    --input role_arn=arn:aws:iam::123456789012:role/aiplex-agent --input region=us-east-1 \
    --input audience=https://aiplex.example.com

The audience input may be omitted when the server has a default audience.
If a user identity provider of this tenant trusts the same issuer and
audience the write is refused; pass --acknowledge-overlap to save it anyway
(the server reports a warning, which is printed).`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if preset == "" {
				return fmt.Errorf("--preset is required; run 'aiplex agents identity presets' to list them")
			}
			in, err := parseKeyValues("input", inputs)
			if err != nil {
				return err
			}
			rc, err := parseKeyValues("claim", claims)
			if err != nil {
				return err
			}
			c := newClient()
			ctx := cmd.Context()
			catalog, err := c.ListAgentIdentityPresets(ctx)
			if err != nil {
				return fmt.Errorf("load identity presets: %w", err)
			}
			if err = validateIdentityInputs(catalog, preset, in); err != nil {
				return err
			}
			if len(rc) > 0 {
				if p, _ := catalog.Preset(preset); !p.AllowsRequiredClaims {
					return fmt.Errorf("preset %s does not accept --claim", preset)
				}
			}
			var expected uint64
			if replace {
				current, err := c.GetAgentIdentity(ctx, args[0], args[1])
				if err != nil {
					return fmt.Errorf("read binding to replace: %w", err)
				}
				expected = current.Revision
			}
			b, err := c.PutAgentIdentity(ctx, args[0], args[1], aiplex.AgentIdentityRequest{
				Preset: preset, Inputs: in, RequiredClaims: rc, DisplayName: displayName, Disabled: disabled, AcknowledgeOverlap: ackOverlap,
			}, expected)
			if err != nil {
				return explainIdentityError("bind identity", err)
			}
			if printStructured(cmd, b) {
				return nil
			}
			out := cmd.OutOrStdout()
			verb := "Created"
			if replace {
				verb = "Updated"
			}
			fmt.Fprintf(out, "%s identity binding %s for agent %s (revision %d)\n", verb, b.ID, b.ClientID, b.Revision)
			fmt.Fprintf(out, "  Issuer:  %s\n  Subject: %s\n  Audience: %s\n", b.Issuer, b.Subject, b.Audience)
			for _, w := range b.Warnings {
				fmt.Fprintf(out, "  Warning: %s\n", w)
			}
			if !catalog.LiveAuthentication {
				fmt.Fprintln(out, "  Note: live authentication is off on this server; use 'identity verify' to dry-run tokens.")
			}
			fmt.Fprintf(out, "Next: aiplex agents identity plan %s %s\n", b.ClientID, b.ID)
			return nil
		},
	}
	cmd.Flags().StringVar(&preset, "preset", "", "Identity preset (see 'identity presets')")
	cmd.Flags().StringArrayVar(&inputs, "input", nil, "Preset input as key=value (repeatable)")
	cmd.Flags().StringArrayVar(&claims, "claim", nil, "Extra exact-match token claim as key=value (repeatable)")
	cmd.Flags().StringVar(&displayName, "display-name", "", "Human-readable name")
	cmd.Flags().BoolVar(&disabled, "disabled", false, "Create the binding disabled")
	cmd.Flags().BoolVar(&replace, "replace", false, "Replace an existing binding with this id")
	cmd.Flags().BoolVar(&ackOverlap, "acknowledge-overlap", false, "Save even though a user identity provider trusts the same issuer and audience (not persisted; repeat on every write)")
	return cmd
}

func explainIdentityError(action string, err error) error {
	var apiErr *aiplex.Error
	if errors.As(err, &apiErr) {
		switch apiErr.Code {
		case "REVISION_CONFLICT":
			return fmt.Errorf("%s: %w\n  The binding already exists or changed. Re-run with --replace to update it, or fetch the latest with 'identity get'.", action, err)
		case "AGENT_NOT_FOUND":
			return fmt.Errorf("%s: %w\n  Register the agent first with 'aiplex agents register'.", action, err)
		}
	}
	return fmt.Errorf("%s: %w", action, err)
}

func agentIdentityDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <agent> <id>",
		Short: "Remove an identity binding",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			current, err := c.GetAgentIdentity(cmd.Context(), args[0], args[1])
			if err != nil {
				return fmt.Errorf("read binding: %w", err)
			}
			if err = c.DeleteAgentIdentity(cmd.Context(), args[0], args[1], current.Revision); err != nil {
				return explainIdentityError("delete identity", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted identity binding %s from agent %s\n", args[1], args[0])
			return nil
		},
	}
}

func agentIdentityPlanCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "plan <agent> <id>",
		Short: "Show the setup steps to run in the other cloud",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			plan, err := newClient().PlanAgentIdentity(cmd.Context(), args[0], args[1])
			if err != nil {
				return fmt.Errorf("plan identity: %w", err)
			}
			if printStructured(cmd, plan) {
				return nil
			}
			renderIdentityPlan(cmd.OutOrStdout(), plan)
			return nil
		},
	}
}

func renderIdentityPlan(out io.Writer, p *aiplex.AgentIdentityPlan) {
	fmt.Fprintf(out, "Identity plan: agent %s, binding %s (%s)\n", p.Binding.ClientID, p.Binding.ID, p.Cloud)
	fmt.Fprintf(out, "  Issuer:   %s\n  Subject:  %s\n  Audience: %s\n", p.Issuer, p.Subject, p.Audience)
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
	section("In the other cloud", p.Steps, true)
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

// readIdentityToken reads a workload token from a file, or stdin when path is
// "-". The token is deliberately not accepted as a flag value (it would land in
// shell history and process listings) and is never printed.
func readIdentityToken(path string, stdin io.Reader) (string, error) {
	if path == "" {
		return "", fmt.Errorf("--token-file is required (use '-' to read the token from stdin); the token is never accepted as a flag value")
	}
	var r io.Reader = stdin
	if path != "-" {
		f, err := os.Open(path)
		if err != nil {
			// os errors carry only the path, never contents.
			return "", fmt.Errorf("open token file: %w", err)
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() {
			return "", fmt.Errorf("token file must be a regular file (use --token-file - for stdin)")
		}
		r = f
	}
	raw, err := io.ReadAll(io.LimitReader(r, maxIdentityTokenBytes+1))
	if err != nil {
		return "", fmt.Errorf("read token: %w", err)
	}
	if len(raw) > maxIdentityTokenBytes {
		return "", fmt.Errorf("token input exceeds %d bytes; expected a single JWT", maxIdentityTokenBytes)
	}
	tok := string(bytes.TrimSpace(raw))
	if tok == "" {
		return "", fmt.Errorf("token input is empty")
	}
	if strings.ContainsAny(tok, " \t\r\n") {
		return "", fmt.Errorf("token input must be a single JWT without whitespace")
	}
	return tok, nil
}

func agentIdentityVerifyCmd() *cobra.Command {
	var (
		agent     string
		binding   string
		tokenFile string
	)
	cmd := &cobra.Command{
		Use:   "verify [--agent <agent> --binding <id>] --token-file <path|->",
		Short: "Dry-run a workload token against identity bindings",
		Long: `Check what AIPlex would make of a token acquired in the other cloud, without
authenticating anything. Every check is listed with its reason; the exit
status is non-zero when the token is not valid.

The token is read from a file or stdin only. It is never accepted as a flag,
never printed, and never logged. Without --agent/--binding the token is
matched against all bindings; with them it is checked against that binding.
(The global --token flag is your own AIPlex API bearer token, not the
workload token being tested.)

Examples:
  aiplex agents identity verify --agent tutor-agent --binding gh-ci --token-file token.txt
  cat token.txt | aiplex agents identity verify --token-file -`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if (agent == "") != (binding == "") {
				return fmt.Errorf("--agent and --binding must be given together")
			}
			tok, err := readIdentityToken(tokenFile, cmd.InOrStdin())
			if err != nil {
				return err
			}
			result, err := newClient().VerifyAgentIdentityToken(cmd.Context(), aiplex.AgentIdentityVerifyRequest{Token: tok, ClientID: agent, BindingID: binding})
			tok = ""
			if err != nil {
				return explainIdentityError("verify identity token", err)
			}
			if !printStructured(cmd, result) {
				renderIdentityVerify(cmd.OutOrStdout(), result)
			}
			if !result.Valid {
				return fmt.Errorf("token is not valid")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&agent, "agent", "", "Agent client ID to pin the check to (requires --binding)")
	cmd.Flags().StringVar(&binding, "binding", "", "Binding ID to pin the check to (requires --agent)")
	cmd.Flags().StringVar(&tokenFile, "token-file", "", "File containing the token, or - for stdin")
	return cmd
}

func renderIdentityVerify(out io.Writer, r *aiplex.AgentIdentityVerifyResult) {
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	for _, c := range r.Checks {
		var label string
		switch c.Status {
		case aiplex.AgentIdentityCheckPass:
			label = "PASS"
		case aiplex.AgentIdentityCheckFail:
			label = "FAIL"
		default:
			label = "SKIP"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", label, c.Name, c.Reason)
	}
	w.Flush()
	fmt.Fprintln(out)
	if r.Valid {
		fmt.Fprintln(out, "Result: valid")
		if r.Binding != nil {
			fmt.Fprintf(out, "  Binding:   %s (agent %s)\n", r.Binding.ID, r.Binding.ClientID)
		}
		if r.PrincipalID != "" {
			fmt.Fprintf(out, "  Principal: %s\n", r.PrincipalID)
		}
		if r.ActorID != "" {
			fmt.Fprintf(out, "  Actor:     %s\n", r.ActorID)
		}
		if !r.LiveAuthentication {
			fmt.Fprintln(out, "  Note: live authentication is off on this server; this token would not yet authenticate requests.")
		}
	} else {
		fmt.Fprintln(out, "Result: NOT valid")
	}
}
