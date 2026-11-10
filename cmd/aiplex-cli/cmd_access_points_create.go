package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/vamsiramakrishnan/aiplex/internal/auth"
	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

type apCreateFlags struct {
	name                   string
	models, tools, callers []string
	users, groups, agents  []string
	clients                []string
	update                 bool
}

func spAPCreateCmd() *cobra.Command {
	var f apCreateFlags
	cmd := &cobra.Command{
		Use:     "create <id> --model <name=surface[:native]> --tool <connection> [--allow-users ...]",
		Aliases: []string{"add"},
		Short:   "Create an access point from flags (or apply -f for a full spec)",
		Long: `Creates an access point: one URL (/ap/<id>/...) that serves the models and
tools you list to the callers you allow.

--model name=surface[:native] exposes a model of a model surface under a
client-facing name (native defaults to name). --tool exposes every permitted
tool of a tool connection (named <connection>__<tool> to callers).

Who may call: with no --allow-* flag every verified caller of the allowed
kinds (--callers, default user,agent,client) is admitted. Otherwise a caller
must match one of:
  --allow-users   a subject at your identity provider (as in the token's sub),
                  a known user's email (from the usage ledger), or a
                  principal-... ID (see: aiplex who --kind user)
  --allow-groups  a group of your identity provider (e.g. eng)
  --allow-agents  an agent ID (see: aiplex agents identity list <agent>)
--allow-clients additionally requires a registered client ID.

The access point is created new; --update replaces an existing one (its
revision is read for you). For QoS classes and expose lists use apply -f.`,
		Example: `  aiplex access-points create team --model mock-gpt=local-models --tool local-tools
  aiplex access-points create ide --model gpt-4o-mini=openai --model claude=claude:claude-sonnet-4-5 --allow-groups eng
  aiplex access-points create ci --model gpt-4o-mini=openai --callers agent --allow-agents ci-bot
  aiplex access-points create bob-only --model mock-gpt=local-models --allow-users bob --update`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAPCreate(cmd, args[0], f)
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&f.name, "name", "", "Display name")
	fl.StringArrayVar(&f.models, "model", nil, "Model to expose: name=surface[:native-model] (repeatable)")
	fl.StringArrayVar(&f.tools, "tool", nil, "Tool connection to expose (repeatable)")
	fl.StringSliceVar(&f.callers, "callers", []string{"user", "agent", "client"}, "Caller kinds admitted: user, agent, client")
	fl.StringSliceVar(&f.users, "allow-users", nil, "Users admitted (subject, email or principal ID; comma separated)")
	fl.StringSliceVar(&f.groups, "allow-groups", nil, "Groups admitted (comma separated)")
	fl.StringSliceVar(&f.agents, "allow-agents", nil, "Agent IDs admitted (comma separated)")
	fl.StringSliceVar(&f.clients, "allow-clients", nil, "Registered client IDs required (comma separated)")
	fl.BoolVar(&f.update, "update", false, "Replace the access point if it exists")
	return cmd
}

func runAPCreate(cmd *cobra.Command, id string, f apCreateFlags) error {
	spec := aiplex.AccessPoint{ID: id, DisplayName: f.name, CallerKinds: nonEmpty(f.callers)}
	for _, k := range spec.CallerKinds {
		if k != "user" && k != "agent" && k != "client" {
			return usageError(fmt.Sprintf("--callers: %q is not a caller kind", k), "Use user, agent and/or client")
		}
	}
	for _, m := range f.models {
		name, rest, ok := strings.Cut(m, "=")
		if !ok || name == "" || rest == "" {
			return usageError(fmt.Sprintf("--model %q: write name=surface or name=surface:native-model", m), "Example: --model gpt-4o-mini=openai")
		}
		surface, native, _ := strings.Cut(rest, ":")
		if native == "" {
			native = name
		}
		spec.Models = append(spec.Models, aiplex.AccessPointModel{Name: name, SurfaceRef: surface, NativeModel: native})
	}
	for _, t := range f.tools {
		if t = strings.TrimSpace(t); t != "" {
			spec.Tools = append(spec.Tools, aiplex.AccessPointTool{ConnectionRef: t})
		}
	}
	if len(spec.Models)+len(spec.Tools) == 0 {
		return usageError("an access point needs at least one --model or --tool",
			"List surfaces with: aiplex llm surfaces list", "List tool connections with: aiplex mcp connections list")
	}
	c := newClient()
	ctx := cmd.Context()
	if len(f.users)+len(f.groups) > 0 {
		lctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		me, err := c.GetIdentity(lctx)
		cancel()
		if err != nil {
			return fmt.Errorf("resolve your identity provider for --allow-users/--allow-groups: %w", err)
		}
		provider, pool := me.Identity.Provider, me.Identity.PoolID
		for _, u := range nonEmpty(f.users) {
			p, err := resolveUserPrincipal(ctx, c, u, provider, pool)
			if err != nil {
				return err
			}
			spec.Subjects.Principals = append(spec.Subjects.Principals, p)
		}
		if len(f.groups) > 0 {
			if provider == "" {
				return usageError("--allow-groups needs a signed-in identity to know which identity provider the groups belong to", "Sign in (aiplex login, or eval \"$(aiplex dev env)\") or use apply -f with subjects.groups")
			}
			source := auth.MembershipSource(provider, pool)
			for _, g := range nonEmpty(f.groups) {
				spec.Subjects.Groups = append(spec.Subjects.Groups, quotaplex.GroupRef{Source: source, ID: g})
			}
		}
	}
	spec.Subjects.Agents = nonEmpty(f.agents)
	spec.Subjects.ClientRegistrations = nonEmpty(f.clients)

	var expected uint64
	existing, err := c.GetAccessPoint(ctx, id)
	switch {
	case err == nil:
		if !f.update {
			return newCLIError(exitConflict, fmt.Sprintf("access point %q already exists (revision %d)", id, existing.Revision),
				"Replace it with the same command and --update", "Or edit it: aiplex access-points get "+id+" -o yaml > ap.yaml; aiplex access-points apply -f ap.yaml")
		}
		expected = existing.Revision
	case spIsNotFound(err):
	default:
		return fmt.Errorf("read access point: %w", err)
	}
	saved, err := c.PutAccessPoint(ctx, spec, expected)
	if err != nil {
		return fmt.Errorf("create access point %s: %w", id, err)
	}
	if spStructured(cmd, saved) {
		return nil
	}
	out := cmd.OutOrStdout()
	verb := "Created"
	if expected > 0 {
		verb = "Updated"
	}
	fmt.Fprintf(out, "%s access point %s (revision %d): %s\n", verb, saved.ID, saved.Revision, resolveAPIURL()+"/ap/"+saved.ID)
	fmt.Fprintf(out, "\nNext:\n  aiplex connect %s                 # client configuration\n  aiplex access-points get %s\n", saved.ID, saved.ID)
	return nil
}

// resolveUserPrincipal turns a subject, a known email or a principal ID into
// the canonical principal ID the access point matches on.
func resolveUserPrincipal(ctx context.Context, c *aiplex.Client, value, provider, pool string) (string, error) {
	if strings.HasPrefix(value, "principal-") {
		return value, nil
	}
	if strings.Contains(value, "@") {
		page, err := c.ListOverviewSubjects(ctx, aiplex.OverviewSubjectsQuery{Kind: "user", Since: "30d"})
		if err == nil {
			for _, s := range page.Subjects {
				if strings.EqualFold(s.DisplayName, value) {
					return s.ID, nil
				}
			}
		}
		return "", usageError(fmt.Sprintf("--allow-users %q: no known user has this email", value),
			"Users become known after their first request; give their subject (the token's sub) or principal ID instead",
			"See known users with: aiplex who --kind user")
	}
	if provider == "" {
		return "", usageError(fmt.Sprintf("--allow-users %q: sign in first so the subject can be resolved at your identity provider, or pass a principal-... ID", value))
	}
	return auth.CanonicalPrincipalID(provider, pool, value), nil
}
