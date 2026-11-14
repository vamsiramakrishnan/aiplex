package main

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// expectedAliasSkips lists the verb aliases applyVerbAliases leaves off a
// command because a sibling in the same group already answers to the name.
// Keyed by command path, with the alias that is skipped. None exist today; an
// entry here is a deliberate decision that the sibling keeps its name.
var expectedAliasSkips = map[string][]string{}

// Every list, get and delete command anywhere in the tree answers to the
// shared aliases, and no command shadows a sibling.
func TestVerbAliasesCoverTheWholeTree(t *testing.T) {
	root := newRootCmd()
	missing, skipped := planVerbAliases(root) // changes nothing
	for _, g := range missing {
		t.Errorf("%s lacks the alias %q (is applyVerbAliases called after the last command is added?)", g.Command.CommandPath(), g.Alias)
	}
	got := map[string][]string{}
	for _, s := range skipped {
		got[s.Command] = append(got[s.Command], s.Alias)
	}
	if len(got) != len(expectedAliasSkips) {
		t.Errorf("alias collisions: got %v, want %v", got, expectedAliasSkips)
	}
	for path, aliases := range expectedAliasSkips {
		if !slices.Equal(got[path], aliases) {
			t.Errorf("alias collisions for %s: got %v, want %v", path, got[path], aliases)
		}
	}
	checked := 0
	walkCommands(root, func(c *cobra.Command) {
		for _, alias := range verbAliases[c.Name()] {
			if slices.Contains(expectedAliasSkips[c.CommandPath()], alias) {
				if slices.Contains(c.Aliases, alias) {
					t.Errorf("%s: alias %q is expected to be skipped but is set", c.CommandPath(), alias)
				}
				continue
			}
			if !slices.Contains(c.Aliases, alias) {
				t.Errorf("%s lacks the alias %q", c.CommandPath(), alias)
			}
			checked++
		}
	})
	if checked < 60 {
		t.Fatalf("only %d verb aliases checked; is the tree walk broken?", checked)
	}
}

// Within one group no two commands share a name or an alias, hidden commands
// included.
func TestNoCommandShadowsASibling(t *testing.T) {
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		seen := map[string]string{}
		for _, sub := range c.Commands() {
			for _, name := range append([]string{sub.Name()}, sub.Aliases...) {
				if other, dup := seen[name]; dup {
					t.Errorf("%s: %q names both %q and %q", c.CommandPath(), name, other, sub.Name())
				}
				seen[name] = sub.Name()
			}
			walk(sub)
		}
	}
	walk(newRootCmd())
}

// A real subcommand with the name of an alias is never shadowed; the walker
// skips that alias for the group, keeps the others, and says so.
func TestVerbAliasesDoNotShadowASibling(t *testing.T) {
	root := &cobra.Command{Use: "aiplex"}
	group := &cobra.Command{Use: "widgets"}
	get := &cobra.Command{Use: "get <id>"}
	show := &cobra.Command{Use: "show"}
	other := &cobra.Command{Use: "list", Aliases: []string{"rm"}}
	del := &cobra.Command{Use: "delete <id>"}
	group.AddCommand(get, show, other, del)
	root.AddCommand(group)
	skipped := applyVerbAliases(root)
	if !slices.Equal(get.Aliases, []string{"describe"}) {
		t.Errorf("get aliases %v", get.Aliases)
	}
	if !slices.Equal(del.Aliases, []string{"remove"}) {
		t.Errorf("delete aliases %v (rm belongs to a sibling)", del.Aliases)
	}
	if !slices.Equal(other.Aliases, []string{"rm", "ls"}) {
		t.Errorf("list aliases %v", other.Aliases)
	}
	if len(show.Aliases) != 0 {
		t.Errorf("show aliases %v", show.Aliases)
	}
	want := []aliasSkip{{"aiplex widgets get", "show"}, {"aiplex widgets delete", "rm"}}
	if len(skipped) != 2 || !slices.Contains(skipped, want[0]) || !slices.Contains(skipped, want[1]) {
		t.Errorf("skipped %v, want %v", skipped, want)
	}
	// Running it again changes nothing.
	again := slices.Clone(get.Aliases)
	applyVerbAliases(root)
	if !slices.Equal(get.Aliases, again) {
		t.Errorf("not idempotent: %v then %v", again, get.Aliases)
	}
}

// The verb aliases are declared in aliases.go only. A per-command alias line
// for one of them would be redundant at best, and hides a collision at worst.
func TestVerbAliasesAreDeclaredOnlyInOnePlace(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	verbs := regexp.MustCompile(`"(ls|show|describe|rm|remove)"`)
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || f == "aliases.go" {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			if strings.Contains(line, "Aliases:") && verbs.MatchString(line) {
				t.Errorf("%s:%d declares a verb alias on the command: %s", f, i+1, strings.TrimSpace(line))
			}
		}
	}
}

// A group with both a delete and a remove command: neither takes the other's
// name, and rm goes to one of them only.
func TestVerbAliasesAreNotHandedOutTwiceInOneGroup(t *testing.T) {
	root := &cobra.Command{Use: "aiplex"}
	group := &cobra.Command{Use: "widgets"}
	del := &cobra.Command{Use: "delete <id>"}
	rem := &cobra.Command{Use: "remove <id>"}
	group.AddCommand(del, rem)
	root.AddCommand(group)
	applyVerbAliases(root)
	owners := 0
	for _, c := range []*cobra.Command{del, rem} {
		if slices.Contains(c.Aliases, "rm") {
			owners++
		}
		if slices.Contains(c.Aliases, "delete") || slices.Contains(c.Aliases, "remove") {
			t.Errorf("%s took the name of its sibling: %v", c.Name(), c.Aliases)
		}
	}
	if owners != 1 {
		t.Errorf("rm is on %d commands: delete %v, remove %v", owners, del.Aliases, rem.Aliases)
	}
}
