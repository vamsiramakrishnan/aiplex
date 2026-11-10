package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

var resourceKinds = []aiplex.ResourceKind{aiplex.ModelSurfaces, aiplex.ToolConnections, aiplex.AgentConnections, aiplex.AgentBindings, aiplex.NetworkProfiles, aiplex.ClientDestinations, aiplex.DeploymentIntents}

// guidedFor names the guided command for a kind, when one exists.
var guidedFor = map[aiplex.ResourceKind]string{
	aiplex.ModelSurfaces:   "aiplex llm surfaces",
	aiplex.ToolConnections: "aiplex mcp connections",
}

func resourceKind(arg string) (aiplex.ResourceKind, error) {
	k := aiplex.ResourceKind(strings.TrimSuffix(strings.TrimSpace(arg), "s"))
	for _, known := range resourceKinds {
		if known == k {
			return k, nil
		}
	}
	names := make([]string, len(resourceKinds))
	for i, known := range resourceKinds {
		names[i] = string(known)
	}
	hints := []string{"Kinds: " + strings.Join(names, ", ")}
	if near := nearest(arg, names); len(near) > 0 {
		hints = append([]string{"Did you mean: " + near[0]}, hints...)
	}
	return "", newCLIError(exitUsage, fmt.Sprintf("%q is not a resource kind", arg), hints...)
}

// resourcesCmd is a typed resource client, not a shell/plugin execution path.
func resourcesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "resources",
		Short: "Low-level access to any resource kind (prefer the guided commands)",
		Long: `Read and write any connected module resource by kind and id. Kinds:
model-surface, tool-connection, agent-connection, agent-binding,
network-profile, client-destination, deployment-intent.

Prefer the guided commands where they exist: aiplex llm surfaces,
aiplex mcp connections, aiplex access-points. Revisions are handled for you:
apply reads the current revision and writes on top of it, so a concurrent
change fails with a conflict instead of being overwritten.`,
		Example: `  aiplex resources list tool-connection
  aiplex resources apply tool-connection docs -f docs.json`,
	}
	show := func(cmd *cobra.Command, value any) {
		if output == "yaml" {
			printYAML(value)
		} else {
			printJSON(value)
		}
	}
	cmd.AddCommand(&cobra.Command{
		Use: "list <kind>", Short: "List resources of a kind (JSON or YAML)", Args: cobra.ExactArgs(1),
		Example: "  aiplex resources list tool-connection",
		RunE: func(cmd *cobra.Command, args []string) error {
			kind, err := resourceKind(args[0])
			if err != nil {
				return err
			}
			out, e := newClient().ListResources(cmd.Context(), kind)
			if e != nil {
				return fmt.Errorf("list %s: %w", kind, e)
			}
			show(cmd, out)
			return nil
		}})
	cmd.AddCommand(&cobra.Command{
		Use: "get <kind> <id>", Short: "Show one resource and its revision", Args: cobra.ExactArgs(2),
		Example: "  aiplex resources get tool-connection local-tools -o yaml",
		RunE: func(cmd *cobra.Command, args []string) error {
			kind, err := resourceKind(args[0])
			if err != nil {
				return err
			}
			out, e := newClient().GetResource(cmd.Context(), kind, args[1])
			if e != nil {
				return resourceNotFound(cmd.Context(), kind, args[1], e)
			}
			show(cmd, out)
			return nil
		}})
	var file string
	var revision uint64
	apply := &cobra.Command{
		Use:     "apply <kind> <id> -f <file>",
		Aliases: []string{"put"},
		Short:   "Create or replace a resource from a JSON or YAML file",
		Long: `Creates the resource, or replaces it when it exists: the current revision is
read for you. --revision pins the revision you expect (0 = must not exist).

The legacy form "resources put <kind> <id> <file> <revision>" still works.`,
		Example: `  aiplex resources apply tool-connection docs -f docs.json
  aiplex resources apply model-surface openai -f openai.yaml --revision 3`,
		Args: cobra.RangeArgs(2, 4),
		RunE: func(cmd *cobra.Command, args []string) error {
			kind, err := resourceKind(args[0])
			if err != nil {
				return err
			}
			id := args[1]
			path := file
			pinned := cmd.Flags().Changed("revision")
			if len(args) >= 3 {
				if file != "" {
					return usageError("give the file either as -f or as the third argument, not both")
				}
				path = args[2]
			}
			if len(args) == 4 {
				r, e := strconv.ParseUint(args[3], 10, 63)
				if e != nil {
					return usageError(fmt.Sprintf("revision %q is not a number", args[3]))
				}
				revision, pinned = r, true
			}
			payload, err := spReadSpec(path, cmd.InOrStdin())
			if err != nil {
				return err
			}
			c := newClient()
			if !pinned {
				current, e := c.GetResource(cmd.Context(), kind, id)
				switch {
				case e == nil:
					revision = current.Revision
				case spIsNotFound(e):
					revision = 0
				default:
					return fmt.Errorf("read %s %s: %w", kind, id, e)
				}
			}
			out, e := c.PutResource(cmd.Context(), kind, id, json.RawMessage(payload), revision)
			if e != nil {
				return fmt.Errorf("apply %s %s: %w", kind, id, e)
			}
			if machineOutput() {
				show(cmd, out)
				return nil
			}
			verb := "Created"
			if revision > 0 {
				verb = "Updated"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s %s %s (revision %d).\n", verb, kind, id, out.Revision)
			return nil
		}}
	apply.Flags().StringVarP(&file, "file", "f", "", "JSON or YAML specification, or - for stdin")
	apply.Flags().Uint64Var(&revision, "revision", 0, "Expected current revision (default: read it; 0 = must not exist)")
	cmd.AddCommand(apply)
	var yes bool
	del := &cobra.Command{
		Use: "delete <kind> <id>", Short: "Delete a resource (its current revision is read for you)", Args: cobra.RangeArgs(2, 3),
		Example: "  aiplex resources delete tool-connection docs --yes",
		RunE: func(cmd *cobra.Command, args []string) error {
			kind, err := resourceKind(args[0])
			if err != nil {
				return err
			}
			c := newClient()
			var rev uint64
			if len(args) == 3 {
				r, e := strconv.ParseUint(args[2], 10, 63)
				if e != nil || r == 0 {
					return usageError("the optional revision must be a positive number")
				}
				rev = r
			} else {
				current, e := c.GetResource(cmd.Context(), kind, args[1])
				if e != nil {
					return resourceNotFound(cmd.Context(), kind, args[1], e)
				}
				rev = current.Revision
			}
			if err := confirm(cmd, yes, fmt.Sprintf("Delete %s %s?", kind, args[1])); err != nil {
				return err
			}
			if e := c.DeleteResource(cmd.Context(), kind, args[1], rev); e != nil {
				return fmt.Errorf("delete %s %s: %w", kind, args[1], e)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted %s %s.\n", kind, args[1])
			return nil
		}}
	del.Flags().BoolVarP(&yes, "yes", "y", false, "Do not ask for confirmation")
	cmd.AddCommand(del)
	var idempotency string
	plan := &cobra.Command{
		Use: "plan <kind> <id>", Short: "Show the deployment plan for a resource", Args: cobra.ExactArgs(2),
		Example: "  aiplex resources plan deployment-intent web --idempotency-key web-v1",
		RunE: func(cmd *cobra.Command, args []string) error {
			kind, err := resourceKind(args[0])
			if err != nil {
				return err
			}
			out, e := newClient().ResourcePlan(cmd.Context(), kind, args[1], idempotency)
			if e != nil {
				return fmt.Errorf("plan %s %s: %w", kind, args[1], e)
			}
			show(cmd, out)
			return nil
		}}
	plan.Flags().StringVar(&idempotency, "idempotency-key", "", "Stable deployment planning key (required for deployment-intent)")
	cmd.AddCommand(plan)
	return cmd
}

// resourceNotFound names the kind and id on a 404 and suggests near ids.
func resourceNotFound(ctx context.Context, kind aiplex.ResourceKind, id string, err error) error {
	if !spIsNotFound(err) {
		return fmt.Errorf("get %s %s: %w", kind, id, err)
	}
	hints := []string{}
	if ids, e := resourceIDs(ctx, newClient(), kind); e == nil {
		if near := nearest(id, ids); len(near) > 0 {
			hints = append(hints, "Did you mean: "+strings.Join(near, ", "))
		}
	}
	list := "aiplex resources list " + string(kind)
	if g, ok := guidedFor[kind]; ok {
		list = g + " list"
	}
	hints = append(hints, "List them with: "+list)
	return &cliError{msg: fmt.Sprintf("%s %q not found", kind, id), hints: hints, code: exitNotFound, err: err}
}
