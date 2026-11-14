package main

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// walkCommands visits every non-hidden command below root.
func walkCommands(c *cobra.Command, visit func(*cobra.Command)) {
	for _, sub := range c.Commands() {
		if sub.Hidden || sub.Name() == "help" {
			continue
		}
		visit(sub)
		walkCommands(sub, visit)
	}
}

// shellWords splits a command line the way a POSIX shell would for the
// purposes of these examples: single and double quotes, backslash escapes
// and line continuations. Command substitutions are not expanded.
func shellWords(line string) []string {
	var words []string
	var cur strings.Builder
	inWord := false
	var quote rune
	runes := []rune(line)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else if r == '\\' && quote == '"' && i+1 < len(runes) {
				i++
				cur.WriteRune(runes[i])
			} else {
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote, inWord = r, true
		case r == '\\' && i+1 < len(runes):
			i++
			if runes[i] != '\n' {
				cur.WriteRune(runes[i])
				inWord = true
			}
		case r == ' ' || r == '\t' || r == '\n':
			if inWord {
				words = append(words, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	if inWord {
		words = append(words, cur.String())
	}
	return words
}

// exampleInvocations extracts every `aiplex ...` invocation from a help
// example block: continuation lines are joined, trailing comments dropped,
// and pipelines, && chains and $(...) split into separate invocations.
func exampleInvocations(example string) []string { return invocations(example, false) }

// invocations extracts invocations; in prose (Long) only indented lines count
// and an aligned description after three spaces is dropped.
func invocations(example string, prose bool) []string {
	joined := strings.ReplaceAll(example, "\\\n", " ")
	var out []string
	for _, line := range strings.Split(joined, "\n") {
		if prose {
			if !strings.HasPrefix(line, " ") {
				continue
			}
			line = strings.TrimSpace(line)
			if i := strings.Index(line, "   "); i >= 0 {
				line = line[:i]
			}
		}
		line = strings.TrimSpace(line)
		if i := strings.Index(line, "  #"); i >= 0 {
			line = line[:i]
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.NewReplacer("$(", " ; ", ")\"", " ; ", "&&", " ; ", "||", " ; ", "|", " ; ", ">", " ; ").Replace(line)
		for _, seg := range strings.Split(line, ";") {
			seg = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(seg), "eval \""))
			if strings.HasPrefix(seg, "aiplex ") || seg == "aiplex" {
				out = append(out, seg)
			}
		}
	}
	return out
}

// checkInvocation parses one example against a fresh command tree: the
// command exists, its flags exist and parse, required flags and positional
// arguments are present.
func checkInvocation(t *testing.T, owner, line string) {
	t.Helper()
	words := shellWords(line)[1:]
	root := newRootCmd()
	cmd, rest, err := root.Find(words)
	if err != nil {
		t.Errorf("%s: example %q: %v", owner, line, err)
		return
	}
	if cmd.Runnable() && cmd.HasSubCommands() && len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		// A group command given an unknown subcommand.
		for _, a := range rest {
			if !strings.HasPrefix(a, "-") {
				if c, _, _ := cmd.Find([]string{a}); c == cmd {
					t.Errorf("%s: example %q: unknown subcommand %q of %q", owner, line, a, cmd.CommandPath())
				}
				break
			}
		}
	}
	if err := cmd.ParseFlags(rest); err != nil {
		t.Errorf("%s: example %q: %v", owner, line, err)
		return
	}
	if help, _ := cmd.Flags().GetBool("help"); help {
		return
	}
	if err := cmd.ValidateRequiredFlags(); err != nil {
		t.Errorf("%s: example %q: %v", owner, line, err)
	}
	if err := cmd.ValidateFlagGroups(); err != nil {
		t.Errorf("%s: example %q: %v", owner, line, err)
	}
	if cmd.Args != nil {
		if err := cmd.Args(cmd, cmd.Flags().Args()); err != nil {
			t.Errorf("%s: example %q: %v", owner, line, err)
		}
	}
}

// Every command has at least one example, and every `aiplex ...` line in any
// command's help (Example and Long) parses against the real command tree.
func TestEveryHelpExampleParses(t *testing.T) {
	root := newRootCmd()
	checked := 0
	walkCommands(root, func(c *cobra.Command) {
		if strings.TrimSpace(c.Example) == "" {
			t.Errorf("%s has no example in --help", c.CommandPath())
		}
		for _, inv := range append(exampleInvocations(c.Example), invocations(c.Long, true)...) {
			checkInvocation(t, c.CommandPath(), inv)
			checked++
		}
	})
	for _, inv := range invocations(root.Long, true) {
		checkInvocation(t, "aiplex", inv)
		checked++
	}
	if checked < 150 {
		t.Fatalf("only %d example invocations checked", checked)
	}
}

func TestExampleExtraction(t *testing.T) {
	got := exampleInvocations("  eval \"$(aiplex dev env)\"    # in another terminal\n  aiplex a --b c \\\n      --d 'e f' | jq .\n  aiplex x && aiplex y")
	want := []string{"aiplex dev env", "aiplex a --b c --d 'e f'", "aiplex x", "aiplex y"}
	for i := range got {
		got[i] = strings.Join(strings.Fields(got[i]), " ")
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q", got)
	}
	if w := shellWords(`aiplex a --x 'q r' "s\"t"`); len(w) != 5 || w[4] != `s"t` || w[3] != "q r" {
		t.Fatalf("shellWords: %q", w)
	}
}

// jargonInDescriptions are words from the code's internals that a user reading
// --help should not meet: docs/naming.md says "module" for what the code
// sometimes calls a plane, and the run backend's own names stay out of Short.
var jargonInDescriptions = []string{"plane", "tape", "dimension", " rpc", "kick the", "projected", "run backend", "bulky"}

// Every command, at any depth, has a one-line description of what it does
// for the user, in the vocabulary of docs/naming.md.
func TestEveryCommandHasAShortDescription(t *testing.T) {
	root := newRootCmd()
	if strings.TrimSpace(root.Short) == "" {
		t.Error("aiplex has no description")
	}
	walkCommands(root, func(c *cobra.Command) {
		short := strings.TrimSpace(c.Short)
		if short == "" {
			t.Errorf("%s has no description (Short)", c.CommandPath())
			return
		}
		if strings.Contains(short, "\n") {
			t.Errorf("%s: description is not one line: %q", c.CommandPath(), short)
		}
		for _, word := range jargonInDescriptions {
			if strings.Contains(strings.ToLower(short), word) {
				t.Errorf("%s: description uses internal jargon %q: %q", c.CommandPath(), strings.TrimSpace(word), short)
			}
		}
	})
}
