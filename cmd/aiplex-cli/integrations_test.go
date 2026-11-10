package main

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// These tests keep integrations/ and AGENTS.md honest: manifests parse, and
// every `aiplex ...` command line in the instructions names a real command
// and real flags, so the text cannot drift from the CLI.

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestIntegrationManifestsParse(t *testing.T) {
	root := repoRoot(t)
	var plugin struct {
		Name        string `json:"name"`
		Version     string `json:"version"`
		Description string `json:"description"`
	}
	readJSON(t, filepath.Join(root, "integrations/claude-code/.claude-plugin/plugin.json"), &plugin)
	if plugin.Name != "aiplex" || plugin.Version == "" || plugin.Description == "" {
		t.Errorf("plugin.json incomplete: %+v", plugin)
	}
	if strings.HasPrefix(plugin.Name, "claude-") {
		t.Error("plugin names starting with claude- are reserved")
	}

	type servers struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	var mcp, gem servers
	readJSON(t, filepath.Join(root, "integrations/claude-code/.mcp.json"), &mcp)
	var ext struct {
		servers
		Name            string `json:"name"`
		Version         string `json:"version"`
		ContextFileName string `json:"contextFileName"`
	}
	readJSON(t, filepath.Join(root, "integrations/gemini-cli/gemini-extension.json"), &ext)
	gem = ext.servers
	if ext.Name == "" || ext.Version == "" {
		t.Errorf("gemini-extension.json incomplete: %+v", ext)
	}
	if _, err := os.Stat(filepath.Join(root, "integrations/gemini-cli", ext.ContextFileName)); err != nil {
		t.Errorf("contextFileName %q does not exist: %v", ext.ContextFileName, err)
	}

	cli := newRootCmd()
	for name, set := range map[string]servers{"claude-code/.mcp.json": mcp, "gemini-cli/gemini-extension.json": gem} {
		if len(set.MCPServers) == 0 {
			t.Errorf("%s declares no MCP server", name)
		}
		for id, s := range set.MCPServers {
			if s.Command != "aiplex" {
				t.Errorf("%s: server %s runs %q, want aiplex", name, id, s.Command)
				continue
			}
			if err := checkCommandLine(cli, append([]string{"aiplex"}, s.Args...)); err != nil {
				t.Errorf("%s: server %s: %v", name, id, err)
			}
		}
	}

	// Each skill's frontmatter name matches its directory.
	skills, _ := filepath.Glob(filepath.Join(root, "integrations/claude-code/skills/*/SKILL.md"))
	if len(skills) < 3 {
		t.Fatalf("expected the three skills, found %d", len(skills))
	}
	for _, p := range skills {
		b, _ := os.ReadFile(p)
		m := regexp.MustCompile(`(?s)\A---\nname: ([a-z0-9-]+)\ndescription: .+?\n---\n`).FindSubmatch(b)
		if m == nil {
			t.Errorf("%s: missing name/description frontmatter", p)
			continue
		}
		if string(m[1]) != filepath.Base(filepath.Dir(p)) {
			t.Errorf("%s: name %q does not match its directory", p, m[1])
		}
	}
}

func readJSON(t *testing.T, path string, into any) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, into); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

func TestInstructionCommandsExist(t *testing.T) {
	root := repoRoot(t)
	files := []string{filepath.Join(root, "AGENTS.md")}
	_ = filepath.WalkDir(filepath.Join(root, "integrations"), func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".md") {
			files = append(files, p)
		}
		return nil
	})
	cli := newRootCmd()
	checked := 0
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, cl := range commandLines(string(b)) {
			checked++
			if err := checkCommandLine(cli, cl.tokens); err != nil {
				t.Errorf("%s: %q: %v", strings.TrimPrefix(f, root+"/"), cl.text, err)
			}
		}
	}
	if checked < 20 {
		t.Errorf("only %d command lines were found; the extractor is probably broken", checked)
	}
}

type commandLine struct {
	text   string
	tokens []string
}

var (
	inlineCode = regexp.MustCompile("`([^`\n]+)`")
	aiplexAt   = regexp.MustCompile(`(^|[\s("'$])aiplex\s`)
)

// commandLines finds `aiplex ...` invocations in fenced code blocks and
// inline code spans. Prose is not scanned, so the product name is not a command.
func commandLines(md string) []commandLine {
	var out []commandLine
	inFence := false
	for _, line := range strings.Split(md, "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "```") {
			inFence = !inFence
			continue
		}
		var segments []string
		if inFence {
			segments = []string{line}
		} else {
			for _, m := range inlineCode.FindAllStringSubmatch(line, -1) {
				segments = append(segments, m[1])
			}
		}
		for _, seg := range segments {
			if strings.HasPrefix(strings.TrimSpace(seg), "#") {
				continue
			}
			loc := aiplexAt.FindStringIndex(seg)
			for loc != nil {
				start := loc[0]
				if seg[start] != 'a' {
					start++
				}
				rest := seg[start:]
				if i := strings.IndexAny(rest, "|;&)`#"); i >= 0 {
					rest = rest[:i]
				}
				if toks := shellFields(rest); len(toks) > 1 {
					out = append(out, commandLine{text: strings.TrimSpace(rest), tokens: toks})
				}
				next := aiplexAt.FindStringIndex(seg[start+6:])
				if next == nil {
					break
				}
				next[0] += start + 6
				next[1] += start + 6
				loc = next
			}
		}
	}
	return out
}

// shellFields splits on whitespace, keeping quoted strings together.
func shellFields(s string) []string {
	var out []string
	var cur strings.Builder
	quote := rune(0)
	has := false
	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote, has = r, true
		case r == ' ' || r == '\t':
			if has || cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
				has = false
			}
		default:
			cur.WriteRune(r)
		}
	}
	if has || cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// checkCommandLine walks the cobra tree: each word must be a subcommand while
// the current command has subcommands, and each flag must exist.
func checkCommandLine(root *cobra.Command, tokens []string) error {
	cmd := root
	args := tokens[1:]
	atCommand := true
	for i := 0; i < len(args); i++ {
		tok := args[i]
		if strings.HasPrefix(tok, "-") && tok != "-" {
			name := strings.TrimLeft(tok, "-")
			if eq := strings.Index(name, "="); eq >= 0 {
				name = name[:eq]
			}
			if tok == "--help" || tok == "-h" { // added by cobra when the command runs
				continue
			}
			var f *pflag.Flag
			if strings.HasPrefix(tok, "--") {
				f = lookupFlag(cmd, name, false)
			} else if len(name) >= 1 {
				f = lookupFlag(cmd, name[:1], true)
			}
			if f == nil {
				return &flagError{cmd.CommandPath(), tok}
			}
			if f.Value.Type() != "bool" && !strings.Contains(tok, "=") && f.NoOptDefVal == "" && i+1 < len(args) {
				i++ // the flag's value
			}
			continue
		}
		if !atCommand {
			continue
		}
		if strings.HasPrefix(tok, "<") || strings.HasPrefix(tok, "$") {
			atCommand = false
			continue
		}
		var next *cobra.Command
		for _, c := range cmd.Commands() {
			if c.Name() == tok || c.HasAlias(tok) {
				next = c
				break
			}
		}
		if next != nil {
			cmd = next
			continue
		}
		if cmd.HasSubCommands() {
			return &flagError{cmd.CommandPath(), tok}
		}
		atCommand = false // a positional argument
	}
	return nil
}

type flagError struct{ cmd, tok string }

func (e *flagError) Error() string {
	return "`" + e.tok + "` is not a command or flag of `" + e.cmd + "`"
}

func TestCommandLineCheckerCatchesRot(t *testing.T) {
	cli := newRootCmd()
	good := []string{
		"aiplex connect local --client claude-code --write --yes",
		"aiplex usage summary --by model --since 7d",
		"aiplex mcp serve",
		"aiplex connect --undo --client codex",
	}
	bad := []string{
		"aiplex connect local --nope",
		"aiplex usage sumary",
		"aiplex platform frobnicate",
		"aiplex mcp serve --port 3",
	}
	for _, l := range good {
		if err := checkCommandLine(cli, shellFields(l)); err != nil {
			t.Errorf("%q should pass: %v", l, err)
		}
	}
	for _, l := range bad {
		if err := checkCommandLine(cli, shellFields(l)); err == nil {
			t.Errorf("%q should fail", l)
		}
	}
}

func lookupFlag(cmd *cobra.Command, name string, short bool) *pflag.Flag {
	for _, set := range []*pflag.FlagSet{cmd.Flags(), cmd.InheritedFlags(), cmd.PersistentFlags()} {
		if short {
			if f := set.ShorthandLookup(name); f != nil {
				return f
			}
		} else if f := set.Lookup(name); f != nil {
			return f
		}
	}
	return nil
}
