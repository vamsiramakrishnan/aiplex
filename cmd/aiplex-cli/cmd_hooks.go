package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

func hooksCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hooks",
		Short: "Request hooks — inspect, mask or refuse prompts, responses and tool calls",
		Long: `Request hooks run on model and tool traffic before it reaches the upstream
and before the answer reaches the caller. A hook policy binds an ordered list
of hooks (redact, size_limit, http sidecar) to a scope: the tenant, an access
point, a model surface or a tool connection. Narrower scopes add hooks; they
cannot remove a broader one. Hooks fail closed: a hook that cannot decide
refuses the request unless it is explicitly fail_open.

Redaction is pattern matching. It is not a guarantee that personal data or
secrets are removed. See docs/request-hooks.md.`,
		Example: `  aiplex hooks policies set -f examples/hooks/policy.yaml
  aiplex hooks policies test pii-guard --text "mail jane@example.com"
  aiplex usage records --hook-decision deny -o wide`,
	}
	cmd.AddCommand(hookPoliciesCmd())
	return cmd
}

func hookPoliciesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "policies",
		Short: "Manage hook policies (operators only)",
		Long: `Hook policies are revisioned resources: replacing or deleting one needs its
current revision, so concurrent edits are never silently overwritten.`,
		Example: `  aiplex hooks policies list
  aiplex hooks policies set -f examples/hooks/policy.yaml
  aiplex hooks policies delete pii-guard --yes`,
	}
	cmd.AddCommand(hookPoliciesListCmd(), hookPoliciesGetCmd(), hookPoliciesSetCmd(), hookPoliciesDeleteCmd(), hookPoliciesTestCmd())
	return cmd
}

func hookScopeLabel(s aiplex.HookScope) string {
	if s.ID == "" {
		return s.Kind
	}
	return s.Kind + ":" + s.ID
}

func hookList(p aiplex.HookPolicy) string {
	names := make([]string, 0, len(p.Hooks))
	for _, h := range p.Hooks {
		names = append(names, h.ID+"("+h.Type+":"+strings.Join(h.Phases, "+")+")")
	}
	return strings.Join(names, " ")
}

func hookDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func hookPoliciesListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Short:   "List hook policies",
		Example: "  aiplex hooks policies list\n  aiplex hooks policies list -o wide\n  aiplex hooks policies list -o json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			policies, err := newClient().ListHookPolicies(cmd.Context())
			if err != nil {
				return fmt.Errorf("list hook policies: %w", err)
			}
			if policies == nil {
				policies = []aiplex.HookPolicyResource{}
			}
			if usageMachineOutput() {
				usagePrint(policies)
				return nil
			}
			if len(policies) == 0 {
				fmt.Println("No hook policies. Requests and responses pass through uninspected.")
				fmt.Println("Create one with: aiplex hooks policies set -f policy.yaml (see examples/hooks/)")
				return nil
			}
			t := newTable(keyCol("ID"), col("SCOPE"), col("HOOKS"), wideCol("PLANES"), col("STREAMING"), wideCol("NON-TEXT"), wideCol("REVISION"))
			for _, p := range policies {
				planes := strings.Join(p.Policy.Planes, ",")
				t.add(p.ID, hookScopeLabel(p.Policy.Scope), hookList(p.Policy), hookDefault(planes, "all"), hookDefault(p.Policy.Streaming, "deny"), hookDefault(p.Policy.NonText, "allow"), strconv.FormatUint(p.Revision, 10))
			}
			t.render(os.Stdout)
			return nil
		},
	}
}

func hookPoliciesGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "get <id>",
		Short:   "Show one hook policy and its current revision",
		Example: "  aiplex hooks policies get pii\n  aiplex hooks policies get pii -o yaml",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			policy, err := newClient().GetHookPolicy(cmd.Context(), args[0])
			if err != nil {
				return fmt.Errorf("get hook policy: %w", err)
			}
			if usageMachineOutput() {
				usagePrint(policy)
				return nil
			}
			p := policy.Policy
			fmt.Printf("Hook policy %s (revision %d)\n", policy.ID, policy.Revision)
			if p.Description != "" {
				fmt.Printf("  Description: %s\n", p.Description)
			}
			fmt.Printf("  Scope:       %s\n", hookScopeLabel(p.Scope))
			fmt.Printf("  Planes:      %s\n", hookDefault(strings.Join(p.Planes, ","), "all"))
			fmt.Printf("  Streaming:   %s\n", hookDefault(p.Streaming, "deny"))
			fmt.Printf("  Non-text:    %s\n", hookDefault(p.NonText, "allow"))
			for i, h := range p.Hooks {
				fmt.Printf("  %d. %s  type=%s phases=%s timeout=%sms on_uninspectable=%s\n", i+1, h.ID, h.Type, strings.Join(h.Phases, ","), hookDefault(strconv.Itoa(h.TimeoutMS), "2000"), hookDefault(h.OnUninspectable, "deny"))
				switch {
				case h.Redact != nil:
					for _, r := range h.Redact.Rules {
						what := "builtin " + r.Builtin
						if r.Pattern != "" {
							what = "pattern"
						}
						fmt.Printf("       rule %s: %s, %s\n", r.ID, what, r.Action)
					}
				case h.SizeLimit != nil:
					fmt.Printf("       max_chars=%d max_parts=%d\n", h.SizeLimit.MaxChars, h.SizeLimit.MaxParts)
				case h.HTTP != nil:
					fmt.Printf("       endpoint %s format=%s fail_open=%t\n", h.HTTP.Endpoint, hookDefault(h.HTTP.Format, "aiplex"), h.HTTP.FailOpen)
				}
			}
			return nil
		},
	}
}

func hookPoliciesSetCmd() *cobra.Command {
	var file string
	var revision uint64
	cmd := &cobra.Command{
		Use:   "set -f <policy.yaml>",
		Short: "Create or replace a hook policy from a file",
		Long: `Reads a full policy specification (YAML or JSON). Unknown fields and unknown
hook types are refused, every regex is compiled, and sidecar endpoints must be
approved by the deployment's outbound endpoint policy. The current revision is
read for you; --revision pins the revision you expect instead.`,
		Example: `  aiplex hooks policies set -f examples/hooks/policy.yaml
  aiplex hooks policies set -f pii.json --revision 3`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if file == "" {
				return usageError("missing -f <policy.yaml>", "Example: aiplex hooks policies set -f examples/hooks/policy.yaml")
			}
			var policy aiplex.HookPolicy
			if err := readSpecFile(file, &policy); err != nil {
				return usageError("read hook policy: "+err.Error(), "Unknown fields are refused; see docs/request-hooks.md for the fields")
			}
			c := newClient()
			if !cmd.Flags().Changed("revision") {
				if current, err := c.GetHookPolicy(cmd.Context(), policy.ID); err == nil {
					revision = current.Revision
				} else if !spIsNotFound(err) {
					return fmt.Errorf("read hook policy: %w", err)
				}
			}
			saved, err := c.PutHookPolicy(cmd.Context(), policy, revision)
			if err != nil {
				return hookPolicyError("set hook policy", policy.ID, err)
			}
			if usageMachineOutput() {
				usagePrint(saved)
				return nil
			}
			verb := "Created"
			if revision > 0 {
				verb = "Updated"
			}
			fmt.Printf("%s hook policy %s (revision %d): %s on %s\n", verb, saved.ID, saved.Revision, hookList(saved.Policy), hookScopeLabel(saved.Policy.Scope))
			return nil
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "Policy specification (YAML or JSON)")
	cmd.Flags().Uint64Var(&revision, "revision", 0, "Expected current revision (default: read it for you)")
	return cmd
}

func hookPoliciesDeleteCmd() *cobra.Command {
	var revision uint64
	var yes bool
	cmd := &cobra.Command{
		Use:     "delete <id>",
		Short:   "Delete a hook policy (uses the current revision unless --revision is given)",
		Example: "  aiplex hooks policies delete pii --yes",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client := newClient()
			if revision == 0 {
				current, err := client.GetHookPolicy(cmd.Context(), args[0])
				if err != nil {
					return fmt.Errorf("delete hook policy: %w", err)
				}
				revision = current.Revision
			}
			if err := confirm(cmd, yes, "Delete hook policy "+args[0]+"? Traffic it covered is no longer inspected by its hooks."); err != nil {
				return err
			}
			if err := client.DeleteHookPolicy(cmd.Context(), args[0], revision); err != nil {
				return hookPolicyError("delete hook policy", args[0], err)
			}
			fmt.Printf("Deleted hook policy %s (revision %d).\n", args[0], revision)
			return nil
		},
	}
	cmd.Flags().Uint64Var(&revision, "revision", 0, "Expected revision")
	addYesFlag(cmd, &yes)
	return cmd
}

func hookPoliciesTestCmd() *cobra.Command {
	var texts []string
	var phase, plane string
	cmd := &cobra.Command{
		Use:   "test <id> --text <sample> [--text <sample>...]",
		Short: "Dry-run a saved policy on sample text (never stored)",
		Long: `Runs the policy's hooks, whatever its scope, over the sample texts and shows
each hook's decision, the rule identifiers that matched with counts, and the
output after masking. http hooks call their sidecar. Nothing is recorded.`,
		Example: `  aiplex hooks policies test pii --text "mail jane@example.com"
  aiplex hooks policies test pii --phase response --text "model output" -o json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(texts) == 0 {
				return usageError("give at least one --text sample", `Example: aiplex hooks policies test `+args[0]+` --text "mail jane@example.com"`)
			}
			result, err := newClient().TestHookPolicy(cmd.Context(), args[0], aiplex.HookPolicyTest{Phase: phase, Plane: plane, Texts: texts})
			if err != nil {
				return fmt.Errorf("test hook policy: %w", err)
			}
			if usageMachineOutput() {
				usagePrint(result)
				return nil
			}
			fmt.Printf("Decision: %s\n", result.Decision)
			if result.Code != "" {
				fmt.Printf("Refused:  %s — %s\n", strings.ToLower(result.Code), result.Message)
			}
			t := newTable(keyCol("HOOK"), col("TYPE"), col("DECISION"), col("RULES"), col("MS"))
			for _, r := range result.Results {
				var rules []string
				for _, rule := range r.Rules {
					rules = append(rules, fmt.Sprintf("%s×%d", rule.ID, rule.Count))
				}
				decision := r.Decision
				if r.FailOpenUsed {
					decision += " (fail-open)"
				}
				t.add(r.ID, r.Type, decision, usageDash(strings.Join(rules, ",")), strconv.FormatInt(r.DurationMS, 10))
			}
			t.render(os.Stdout)
			for i, text := range result.Texts {
				fmt.Printf("Output %d: %s\n", i+1, text)
			}
			return nil
		},
	}
	cmd.Flags().StringArrayVar(&texts, "text", nil, "Sample text (repeatable)")
	cmd.Flags().StringVar(&phase, "phase", "request", "Phase: request or response")
	cmd.Flags().StringVar(&plane, "plane", "llmplex", "Plane passed to sidecars: llmplex or mcplex")
	return cmd
}

// readSpecFile reads one YAML or JSON document into out, refusing unknown
// fields. YAML is converted to JSON first so the JSON field names apply.
func readSpecFile(path string, out any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(data) > 1<<20 {
		return fmt.Errorf("file exceeds 1 MiB")
	}
	raw := data
	if ext := strings.ToLower(filepath.Ext(path)); ext != ".json" {
		var doc any
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return fmt.Errorf("invalid YAML: %w", err)
		}
		if raw, err = json.Marshal(yamlToJSON(doc)); err != nil {
			return fmt.Errorf("invalid YAML: %w", err)
		}
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return err
	}
	if dec.More() {
		return fmt.Errorf("one document required")
	}
	return nil
}

func hookPolicyError(action, id string, err error) error {
	if aiplex.IsRevisionConflict(err) {
		return &cliError{msg: fmt.Sprintf("%s: hook policy %q was changed by someone else since it was read", action, id), code: exitConflict, err: err,
			hints: []string{"Nothing was changed.", "See the current policy with: aiplex hooks policies get " + id, "Run the command again to apply your change on top of it."}}
	}
	return fmt.Errorf("%s: %w", action, err)
}
