package main

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/vamsiramakrishnan/aiplex/internal/cliconfig"
	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

// Shell completion of resource ids from the server. Completion runs on every
// TAB, so it is bounded: one request, a short timeout, at most
// completionMaxIDs results, and silence (no file names) on any error.
const (
	completionTimeout = 1500 * time.Millisecond
	completionMaxIDs  = 200
)

// idVerbs are the subcommands whose first argument is an id of their group.
var idVerbs = map[string]bool{"get": true, "delete": true, "test": true, "quota-plan": true, "plan": true}

func completeWith(l lister) cobra.CompletionFunc {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		ctx, cancel := context.WithTimeout(context.Background(), completionTimeout)
		defer cancel()
		c := newClient()
		c.HTTPClient.Timeout = completionTimeout
		ids, err := l(ctx, c)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return filterIDs(ids, toComplete), cobra.ShellCompDirectiveNoFileComp
	}
}

func filterIDs(ids []string, prefix string) []string {
	out := []string{}
	for _, id := range ids {
		if strings.HasPrefix(id, prefix) {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	if len(out) > completionMaxIDs {
		out = out[:completionMaxIDs]
	}
	return out
}

func completeContexts(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	cfg, err := cliconfig.Load()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return filterIDs(contextNames(cfg), toComplete), cobra.ShellCompDirectiveNoFileComp
}

var accessPointLister lister = func(ctx context.Context, c *aiplex.Client) ([]string, error) {
	list, err := c.ListAccessPoints(ctx)
	ids := make([]string, 0, len(list))
	for _, r := range list {
		ids = append(ids, r.ID)
	}
	return ids, err
}

var priceLister lister = func(ctx context.Context, c *aiplex.Client) ([]string, error) {
	list, err := c.ListModelPrices(ctx)
	ids := make([]string, 0, len(list))
	for _, r := range list {
		ids = append(ids, r.ID)
	}
	return ids, err
}

// registerCompletions wires id completion into the command tree.
func registerCompletions(root *cobra.Command) {
	registerLister("usage prices", priceLister)
	registerLister("identity user-providers", func(ctx context.Context, c *aiplex.Client) ([]string, error) {
		list, err := c.ListUserIdentityProviders(ctx)
		ids := make([]string, 0, len(list))
		for _, p := range list {
			ids = append(ids, p.ID)
		}
		return ids, err
	})
	byPath := map[string]*cobra.Command{}
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		byPath[strings.TrimPrefix(c.CommandPath(), "aiplex ")] = c
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)
	set := func(path string, f cobra.CompletionFunc) {
		if c, ok := byPath[path]; ok && c.ValidArgsFunction == nil && len(c.ValidArgs) == 0 {
			c.ValidArgsFunction = f
		}
	}
	for parent, l := range listers {
		for verb := range idVerbs {
			set(parent+" "+verb, completeWith(l))
		}
	}
	set("connect", completeWith(accessPointLister))
	set("tools set-auth", completeWith(listers["mcp connections"]))
	for _, p := range []string{"tool-auth start", "tool-auth status", "tool-auth revoke"} {
		set(p, completeWith(listers["mcp connections"]))
	}
	for _, p := range []string{"ctx", "config use-context", "config delete-context"} {
		set(p, completeContexts)
	}
	// Flags that name a resource.
	for path, c := range byPath {
		if f := c.Flags().Lookup("access-point"); f != nil && path != "" {
			_ = c.RegisterFlagCompletionFunc("access-point", completeWith(accessPointLister))
		}
		if f := c.Flags().Lookup("tool"); f != nil && strings.HasPrefix(path, "access-points") {
			_ = c.RegisterFlagCompletionFunc("tool", completeWith(listers["mcp connections"]))
		}
	}
	_ = root.RegisterFlagCompletionFunc("context", completeContexts)
	_ = root.RegisterFlagCompletionFunc("output", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return []string{"table", "wide", "json", "yaml"}, cobra.ShellCompDirectiveNoFileComp
	})
}
