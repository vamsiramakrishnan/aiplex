package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

// Without a terminal a destructive command refuses (exit 2) unless --yes is given.
func TestConfirmRefusesWithoutATerminal(t *testing.T) {
	cmd := &cobra.Command{Use: "delete"}
	err := confirm(cmd, false, "Delete thing x?")
	var ce *cliError
	if !errors.As(err, &ce) || ce.code != exitUsage || !strings.Contains(err.Error(), "no terminal") {
		t.Fatalf("confirm without a terminal = %v", err)
	}
	if got := strings.Join(ce.hints, " "); !strings.Contains(got, "--yes") {
		t.Fatalf("hint %q does not mention --yes", got)
	}
	if err := confirm(cmd, true, "Delete thing x?"); err != nil {
		t.Fatalf("--yes refused: %v", err)
	}
}

// What a record was for: the tool called, or the connection and what was done.
func TestUsageTargetPerPlane(t *testing.T) {
	for name, tc := range map[string]struct {
		r    usageledger.Record
		want string
	}{
		"model":          {usageledger.Record{ModelID: "mock-gpt", ResourceID: "local-models"}, "mock-gpt"},
		"tool call":      {usageledger.Record{Plane: "mcplex", ToolName: "add", ResourceID: "local-tools", Operation: "tools/call"}, "add"},
		"discovery":      {usageledger.Record{Plane: "mcplex", ResourceID: "local-tools", Operation: "tools/list"}, "local-tools (list)"},
		"initialize":     {usageledger.Record{Plane: "mcplex", ResourceID: "local-tools", Operation: "initialize"}, "local-tools (initialize)"},
		"refused call":   {usageledger.Record{Plane: "mcplex", ResourceID: "local-tools", Operation: "tools/call"}, "local-tools"},
		"nothing at all": {usageledger.Record{}, "—"},
	} {
		if got := usageTarget(tc.r); got != tc.want {
			t.Errorf("%s: %q, want %q", name, got, tc.want)
		}
	}
}

// -o wide shows the readable caller in USER and the canonical ID in its own column.
func TestUsageWideSeparatesUserAndPrincipalID(t *testing.T) {
	old := output
	output = "wide"
	defer func() { output = old }()
	id := "principal-" + strings.Repeat("a", 64)
	table := usageRecordTable([]usageledger.Record{{ID: "ul_1", PrincipalID: id, PrincipalDisplay: "ada@example.com"}})
	var b strings.Builder
	table.renderWidth(&b, 400, true)
	lines := strings.Split(strings.TrimSpace(b.String()), "\n")
	if len(lines) < 2 || !strings.Contains(lines[0], "USER") || !strings.Contains(lines[0], "PRINCIPAL ID") {
		t.Fatalf("header: %q", b.String())
	}
	// Cells are separated by runs of spaces; the readable name comes under USER
	// and the canonical id under PRINCIPAL ID, which follows it.
	header, row := strings.Fields(lines[0]), strings.Fields(lines[1])
	cell := func(fields []string, name string) int {
		for i, f := range fields {
			if f == name {
				return i
			}
		}
		return -1
	}
	if cell(header, "USER") < 0 || cell(row, "ada@example.com") < 0 || cell(row, id) < 1 || row[cell(row, id)-1] != "ada@example.com" {
		t.Fatalf("USER should hold the readable name and PRINCIPAL ID the canonical id:\n%s", b.String())
	}
}

// Help text never points at commands or flags that do not exist.
func TestHelpMentionsNoRetiredCommands(t *testing.T) {
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		text := c.Short + "\n" + c.Long + "\n" + c.Example
		for _, retired := range []string{"aiplex dev up", "--with-tape"} {
			if strings.Contains(text, retired) {
				t.Errorf("%s help mentions %q", c.CommandPath(), retired)
			}
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(newRootCmd())
}
