package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

func mcpConnRulesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rules",
		Short: "Policy rules on a connection's tool calls",
		Long: `Rules decide allow or deny on a tool call from structured facts: the tool and
its annotations, the call's arguments, the caller and the request time. They
are CEL boolean expressions, type-checked when saved.

Order: the connection's tools allow and deny lists, the caller's mcp:tools
scope and read_only are checked first. Then the rules run in order. The first
matching deny refuses. If the connection has any allow rule, a call that
matches none of them is refused. A rule that cannot be evaluated refuses the
call (tool_policy_error). Rules never change a call's content.

Variables: tool.name, tool.connection, tool.annotations (read_only,
destructive, idempotent, open_world; absent when unknown), args, caller.kind,
caller.principal, caller.groups (absent unless caller.groups_known),
caller.agent, caller.client, access_point.id, access_point.labels,
request.time. Helpers: path_under, url_host, any_in, lower, matches (literal
RE2 pattern), startsWith, endsWith.`,
		Example: `  aiplex mcp connections rules list search
  aiplex mcp connections rules add search --id no-etc --effect deny --when "path_under(args.path, '/etc')" --message "files under /etc are off limits"
  aiplex mcp connections rules test search --tool read_file --args '{"path":"/etc/hosts"}' --group eng`,
	}
	cmd.AddCommand(mcpRulesListCmd(), mcpRulesAddCmd(), mcpRulesRemoveCmd(), mcpRulesSetCmd(), mcpRulesTestCmd())
	return cmd
}

func decodeRulesFrom(v toolConnView) []aiplex.ToolRule {
	if v.Tools == nil {
		return nil
	}
	return v.Tools.Rules
}

func truncateRule(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		return s[:n-1] + "…"
	}
	return s
}

func mcpRulesListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list <connection>",
		Aliases: []string{"ls"},
		Short:   "List a connection's policy rules in evaluation order",
		Example: "  aiplex mcp connections rules list search\n  aiplex mcp connections rules list search -o yaml",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := newClient().GetResource(cmd.Context(), aiplex.ToolConnections, args[0])
			if err != nil {
				return fmt.Errorf("get tool connection: %w", err)
			}
			rules := decodeRulesFrom(decodeToolConnView(*r))
			if rules == nil {
				rules = []aiplex.ToolRule{}
			}
			if spStructured(cmd, rules) {
				return nil
			}
			if len(rules) == 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "No rules on %s. Add one with: aiplex mcp connections rules add %s --id <id> --effect deny --when <expression>\n", args[0], args[0])
				return nil
			}
			t := newTable(keyCol("ID"), col("EFFECT"), col("WHEN"), wideCol("MESSAGE"))
			for _, rule := range rules {
				t.add(rule.ID, rule.Effect, truncateRule(rule.When, 70), spDash(truncateRule(rule.Message, 50)))
			}
			t.render(cmd.OutOrStdout())
			return nil
		},
	}
}

// updateRules reads the connection, applies change to its rules and writes it
// back with the revision it read, so a concurrent edit is a conflict.
func updateRules(cmd *cobra.Command, id string, change func([]aiplex.ToolRule) ([]aiplex.ToolRule, error)) (*aiplex.ToolConnectionRecord, error) {
	return newClient().UpdateToolConnectionWith(cmd.Context(), id, func(current *aiplex.ToolConnectionRecord) (aiplex.ToolConnectionChange, error) {
		policy := aiplex.ToolPolicy{}
		if current.Tools != nil {
			policy = *current.Tools
		}
		rules, err := change(append([]aiplex.ToolRule(nil), policy.Rules...))
		if err != nil {
			return aiplex.ToolConnectionChange{}, err
		}
		policy.Rules = rules
		if len(policy.Allow) == 0 && len(policy.Deny) == 0 && !policy.ReadOnly && len(policy.Rules) == 0 {
			return aiplex.ToolConnectionChange{ClearTools: true}, nil
		}
		return aiplex.ToolConnectionChange{Tools: &policy}, nil
	})
}

func mcpRulesAddCmd() *cobra.Command {
	var rule aiplex.ToolRule
	var replace bool
	var before string
	cmd := &cobra.Command{
		Use:   "add <connection> --id <rule-id> --effect deny|allow --when <expression>",
		Short: "Add a policy rule (appended; use --before to place it earlier)",
		Long: `Adds a rule to the connection's tools policy. The expression is compiled and
type-checked by the gateway when the connection is saved; an expression that
does not check is rejected and nothing changes. Test candidates first with:

  aiplex mcp connections rules test search --tool read_file --args '{"path":"/tmp/a"}'`,
		Example: `  aiplex mcp connections rules add search --id no-etc --effect deny --when "path_under(args.path, '/etc')" --message "files under /etc are off limits"
  aiplex mcp connections rules add search --id eng-only --effect allow --when "caller.groups_known && 'eng' in caller.groups"
  aiplex mcp connections rules add search --id no-force --effect deny --when "has(args.force) && args.force == true" --before eng-only`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			saved, err := updateRules(cmd, args[0], func(rules []aiplex.ToolRule) ([]aiplex.ToolRule, error) {
				for i, existing := range rules {
					if existing.ID == rule.ID {
						if !replace {
							return nil, newCLIError(exitConflict, fmt.Sprintf("rule %q already exists on %s", rule.ID, args[0]), "Replace it with --replace, or remove it with: aiplex mcp connections rules remove "+args[0]+" "+rule.ID)
						}
						rules = append(rules[:i], rules[i+1:]...)
						break
					}
				}
				if before != "" {
					for i, existing := range rules {
						if existing.ID == before {
							return append(rules[:i], append([]aiplex.ToolRule{rule}, rules[i:]...)...), nil
						}
					}
					return nil, usageError(fmt.Sprintf("--before %q is not a rule of %s", before, args[0]))
				}
				return append(rules, rule), nil
			})
			if err != nil {
				return fmt.Errorf("add rule: %w", err)
			}
			if spStructured(cmd, saved) {
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Saved rule %s on %s (revision %d).\nNext: aiplex mcp connections rules test %s --tool <name> --args '{...}'\n", rule.ID, args[0], saved.Revision, args[0])
			return nil
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&rule.ID, "id", "", "Rule id: lower-case letters, digits, '-' or '_'")
	fl.StringVar(&rule.Effect, "effect", "", "deny or allow")
	fl.StringVar(&rule.When, "when", "", "CEL boolean expression")
	fl.StringVar(&rule.Message, "message", "", "Message shown to the caller when the rule refuses a call (safe to publish)")
	fl.StringVar(&rule.Description, "description", "", "What the rule is for (operators only)")
	fl.StringVar(&before, "before", "", "Insert before this rule id instead of appending")
	fl.BoolVar(&replace, "replace", false, "Replace a rule with the same id")
	_ = cmd.MarkFlagRequired("id")
	_ = cmd.MarkFlagRequired("effect")
	_ = cmd.MarkFlagRequired("when")
	return cmd
}

func mcpRulesRemoveCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "remove <connection> <rule-id>",
		Aliases: []string{"rm", "delete"},
		Short:   "Remove a policy rule",
		Example: "  aiplex mcp connections rules remove search no-etc --yes",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := confirm(cmd, yes, fmt.Sprintf("Remove rule %s from tool connection %s? Calls it refused are no longer refused.", args[1], args[0])); err != nil {
				return err
			}
			saved, err := updateRules(cmd, args[0], func(rules []aiplex.ToolRule) ([]aiplex.ToolRule, error) {
				for i, existing := range rules {
					if existing.ID == args[1] {
						return append(rules[:i], rules[i+1:]...), nil
					}
				}
				return nil, newCLIError(exitNotFound, fmt.Sprintf("rule %q is not on %s", args[1], args[0]), "List rules with: aiplex mcp connections rules list "+args[0])
			})
			if err != nil {
				return fmt.Errorf("remove rule: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Removed rule %s from %s (revision %d).\n", args[1], args[0], saved.Revision)
			return nil
		},
	}
	addYesFlag(cmd, &yes)
	return cmd
}

func mcpRulesSetCmd() *cobra.Command {
	var file string
	var yes bool
	cmd := &cobra.Command{
		Use:   "set <connection> -f <file>",
		Short: "Replace all of a connection's rules from a JSON or YAML file",
		Long: `Replaces the whole rules list from a file: a list of rules, or an object with a
"rules" member. Replacing removes rules that are not in the file, so it asks
for confirmation (--yes without a terminal). An empty list removes every rule.`,
		Example: "  aiplex mcp connections rules set search -f rules.yaml --yes\n  aiplex mcp connections rules list search -o yaml > rules.yaml",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := spReadSpec(file, cmd.InOrStdin())
			if err != nil {
				return err
			}
			var rules []aiplex.ToolRule
			var wrapped struct {
				Rules []aiplex.ToolRule `json:"rules"`
			}
			if json.Unmarshal(raw, &rules) != nil {
				if json.Unmarshal(raw, &wrapped) != nil {
					return usageError("the file must be a list of rules or an object with a \"rules\" list")
				}
				rules = wrapped.Rules
			}
			if err := confirm(cmd, yes, fmt.Sprintf("Replace all rules of tool connection %s with the %d in %s?", args[0], len(rules), file)); err != nil {
				return err
			}
			saved, err := updateRules(cmd, args[0], func([]aiplex.ToolRule) ([]aiplex.ToolRule, error) { return rules, nil })
			if err != nil {
				return fmt.Errorf("set rules: %w", err)
			}
			if spStructured(cmd, saved) {
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Set %d rules on %s (revision %d).\n", len(rules), args[0], saved.Revision)
			return nil
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "Rules as JSON or YAML ('-' for stdin)")
	addYesFlag(cmd, &yes)
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

func mcpRulesTestCmd() *cobra.Command {
	var (
		tool, argsJSON, argsFile, kind, principal, agent, client, accessPoint, at string
		groups                                                                    []string
		groupsUnknown                                                             bool
		annotations                                                               []string
		rulesFile                                                                 string
	)
	cmd := &cobra.Command{
		Use:   "test <connection> --tool <name> [--args JSON] [--group g ...]",
		Short: "Dry-run the rules against a sample call (nothing is stored)",
		Long: `Evaluates the connection's rules against a sample call and prints each rule's
result (matched, not_matched, error, not_evaluated) and the decision. Only
rules are evaluated: the tool name lists, the caller's scope, read_only, quota
and request hooks are not part of this test. The sample is not stored or
recorded, and arguments are not logged. Operators only.

--rules-file tries candidate rules in place of the saved ones, without saving.
Without --group the subject's group membership is unknown (caller.groups_known
is false); pass --group for each group, or --groups-known=false to be explicit.`,
		Example: `  aiplex mcp connections rules test search --tool read_file --args '{"path":"/etc/hosts"}' --group eng
  aiplex mcp connections rules test search --tool delete --annotation destructive=true --kind agent
  aiplex mcp connections rules test search --tool read_file --args-file call.json --rules-file candidate.yaml`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sample := aiplex.ToolRulesTest{Tool: tool, AccessPoint: accessPoint, Time: at,
				Subject: aiplex.ToolRulesSubject{Kind: kind, Principal: principal, Groups: groups, Agent: agent, Client: client}}
			if cmd.Flags().Changed("groups-unknown") && groupsUnknown {
				known := false
				sample.Subject.GroupsKnown = &known
			}
			if argsFile != "" {
				if argsJSON != "" {
					return usageError("--args and --args-file cannot be combined")
				}
				raw, err := spReadSpec(argsFile, cmd.InOrStdin())
				if err != nil {
					return err
				}
				argsJSON = string(raw)
			}
			if argsJSON != "" {
				if !json.Valid([]byte(argsJSON)) {
					return usageError("--args must be a JSON object")
				}
				sample.Args = json.RawMessage(argsJSON)
			}
			if len(annotations) > 0 {
				sample.Annotations = &aiplex.ToolRulesAnnotations{}
				for _, a := range annotations {
					name, value, ok := strings.Cut(a, "=")
					if !ok || (value != "true" && value != "false") {
						return usageError(fmt.Sprintf("--annotation %q: use name=true or name=false", a))
					}
					b := value == "true"
					switch name {
					case "read_only":
						sample.Annotations.ReadOnly = &b
					case "destructive":
						sample.Annotations.Destructive = &b
					case "idempotent":
						sample.Annotations.Idempotent = &b
					case "open_world":
						sample.Annotations.OpenWorld = &b
					default:
						return usageError(fmt.Sprintf("--annotation %q: name is read_only, destructive, idempotent or open_world", a))
					}
				}
			}
			if rulesFile != "" {
				raw, err := spReadSpec(rulesFile, cmd.InOrStdin())
				if err != nil {
					return err
				}
				var rules []aiplex.ToolRule
				var wrapped struct {
					Rules []aiplex.ToolRule `json:"rules"`
				}
				if json.Unmarshal(raw, &rules) != nil {
					if json.Unmarshal(raw, &wrapped) != nil {
						return usageError("--rules-file must be a list of rules or an object with a \"rules\" list")
					}
					rules = wrapped.Rules
				}
				if rules == nil {
					rules = []aiplex.ToolRule{}
				}
				sample.Rules = rules
			}
			result, err := newClient().TestToolRules(cmd.Context(), args[0], sample)
			if err != nil {
				return fmt.Errorf("test rules: %w", err)
			}
			if spStructured(cmd, result) {
				return nil
			}
			out := cmd.OutOrStdout()
			if len(result.Trace) == 0 {
				fmt.Fprintln(out, "No rules to evaluate.")
			} else {
				t := newTable(col("SET"), keyCol("RULE"), col("EFFECT"), col("RESULT"), wideCol("ERROR"))
				for _, tr := range result.Trace {
					t.add(tr.Set, tr.ID, tr.Effect, tr.Result, spDash(truncateRule(tr.Error, 80)))
				}
				t.render(out)
			}
			fmt.Fprintf(out, "Decision: %s", result.Decision)
			if result.ErrorClass != "" {
				fmt.Fprintf(out, " (%s", result.ErrorClass)
				if result.Rule != "" {
					fmt.Fprintf(out, ", rule %s", result.Rule)
				}
				fmt.Fprintf(out, ", %s)", result.Reason)
			}
			fmt.Fprintln(out)
			if result.Message != "" {
				fmt.Fprintf(out, "Caller sees: %s\n", result.Message)
			}
			if result.ListHidden {
				fmt.Fprintln(out, "tools/list hides this tool for this subject.")
			}
			fmt.Fprintln(out, result.Note)
			return nil
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&tool, "tool", "", "Tool name of the sample call")
	fl.StringVar(&argsJSON, "args", "", "Sample arguments, a JSON object")
	fl.StringVar(&argsFile, "args-file", "", "Sample arguments from a JSON or YAML file ('-' for stdin)")
	fl.StringVar(&kind, "kind", "", "Caller kind: user, agent or client")
	fl.StringVar(&principal, "principal", "", "Caller principal id")
	fl.StringSliceVar(&groups, "group", nil, "Caller group (repeatable); sets caller.groups_known")
	fl.BoolVar(&groupsUnknown, "groups-unknown", false, "The caller's group membership is unknown")
	fl.StringVar(&agent, "agent", "", "Caller agent id")
	fl.StringVar(&client, "client", "", "Caller client registration id")
	fl.StringVar(&accessPoint, "access-point", "", "Also apply this access point's rules for the connection")
	fl.StringSliceVar(&annotations, "annotation", nil, "Simulated tool annotation, name=true|false (read_only, destructive, idempotent, open_world; repeatable)")
	fl.StringVar(&rulesFile, "rules-file", "", "Candidate rules (JSON or YAML) used instead of the saved ones")
	fl.StringVar(&at, "time", "", "request.time as RFC 3339 (default: now)")
	_ = cmd.MarkFlagRequired("tool")
	return cmd
}
