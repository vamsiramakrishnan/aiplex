package main

import (
	"slices"

	"github.com/spf13/cobra"
)

// verbAliases is the one place that decides the short names of the common
// verbs. Every command with one of these names, at any depth of the tree,
// answers to its aliases too:
//
//	aiplex mcp connections ls     == aiplex mcp connections list
//	aiplex clients show local     == aiplex clients get local
//	aiplex budgets rm team-a      == aiplex budgets delete team-a
//
// A command never declares these aliases itself, so a command added later gets
// them without anyone remembering to. `remove` is the same verb as `delete`
// for commands that detach an entry from a list (a policy rule), so it answers
// to the same names.
var verbAliases = map[string][]string{
	"list":   {"ls"},
	"get":    {"show", "describe"},
	"delete": {"rm", "remove"},
	"remove": {"rm", "delete"},
}

// aliasSkip records an alias that applyVerbAliases left off a command because
// a sibling already answers to that name.
type aliasSkip struct {
	Command string // command path, for example "aiplex widgets get"
	Alias   string
}

// aliasGrant is one alias that should be on one command.
type aliasGrant struct {
	Command *cobra.Command
	Alias   string
}

// planVerbAliases walks the whole command tree (hidden commands included) and
// returns the aliases a verb command still lacks, and the ones it must not get
// because a sibling in the same group is named, or already aliased, that way.
// It changes nothing.
func planVerbAliases(root *cobra.Command) (missing []aliasGrant, skipped []aliasSkip) {
	// claimed holds the names handed out while planning, per group, so that two
	// verb commands of one group (delete and remove) never get the same alias.
	claimed := map[*cobra.Command][]string{}
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		if aliases, ok := verbAliases[c.Name()]; ok && c.HasParent() {
			for _, alias := range aliases {
				switch {
				case slices.Contains(c.Aliases, alias):
				case siblingUses(c, alias) || slices.Contains(claimed[c.Parent()], alias):
					skipped = append(skipped, aliasSkip{c.CommandPath(), alias})
				default:
					claimed[c.Parent()] = append(claimed[c.Parent()], alias)
					missing = append(missing, aliasGrant{c, alias})
				}
			}
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)
	return missing, skipped
}

// applyVerbAliases gives every verb command of the tree its aliases. It never
// shadows a sibling: when another command in the same group is named, or
// already aliased, as an alias it would add, that alias is skipped for this
// group and reported.
func applyVerbAliases(root *cobra.Command) []aliasSkip {
	missing, skipped := planVerbAliases(root)
	for _, g := range missing {
		g.Command.Aliases = append(g.Command.Aliases, g.Alias)
	}
	return skipped
}

// siblingUses reports whether another command of c's group is named, or is
// already aliased, name.
func siblingUses(c *cobra.Command, name string) bool {
	for _, s := range c.Parent().Commands() {
		if s != c && (s.Name() == name || slices.Contains(s.Aliases, name)) {
			return true
		}
	}
	return false
}
