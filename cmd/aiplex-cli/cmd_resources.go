package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/cobra"
	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

// resourcesCmd is a typed resource client, not a shell/plugin execution path.
func resourcesCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "resources", Short: "Manage connected module resources (model-surface, agent-connection, tool-connection, network-profile, client-destination, deployment-intent, agent-binding)"}
	show := func(value any) {
		if output == "yaml" {
			printYAML(value)
		} else {
			printJSON(value)
		}
	}
	cmd.AddCommand(&cobra.Command{Use: "list <kind>", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		out, e := newClient().ListResources(cmd.Context(), aiplex.ResourceKind(args[0]))
		if e == nil {
			show(out)
		}
		return e
	}})
	cmd.AddCommand(&cobra.Command{Use: "get <kind> <id>", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		out, e := newClient().GetResource(cmd.Context(), aiplex.ResourceKind(args[0]), args[1])
		if e == nil {
			show(out)
		}
		return e
	}})
	cmd.AddCommand(&cobra.Command{Use: "put <kind> <id> <json-file> <expected-revision>", Args: cobra.ExactArgs(4), RunE: func(cmd *cobra.Command, args []string) error {
		revision, e := strconv.ParseUint(args[3], 10, 63)
		if e != nil {
			return e
		}
		payload, e := os.ReadFile(args[2])
		if e != nil {
			return e
		}
		if !json.Valid(payload) {
			return fmt.Errorf("specification must be JSON")
		}
		out, e := newClient().PutResource(cmd.Context(), aiplex.ResourceKind(args[0]), args[1], json.RawMessage(payload), revision)
		if e == nil {
			show(out)
		}
		return e
	}})
	cmd.AddCommand(&cobra.Command{Use: "delete <kind> <id> <expected-revision>", Args: cobra.ExactArgs(3), RunE: func(cmd *cobra.Command, args []string) error {
		revision, e := strconv.ParseUint(args[2], 10, 63)
		if e != nil || revision == 0 {
			return fmt.Errorf("positive expected revision required")
		}
		return newClient().DeleteResource(cmd.Context(), aiplex.ResourceKind(args[0]), args[1], revision)
	}})
	var idempotency string
	plan := &cobra.Command{Use: "plan <kind> <id>", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		out, e := newClient().ResourcePlan(cmd.Context(), aiplex.ResourceKind(args[0]), args[1], idempotency)
		if e == nil {
			show(out)
		}
		return e
	}}
	plan.Flags().StringVar(&idempotency, "idempotency-key", "", "Stable deployment planning key (required for deployment-intent)")
	cmd.AddCommand(plan)
	return cmd
}
