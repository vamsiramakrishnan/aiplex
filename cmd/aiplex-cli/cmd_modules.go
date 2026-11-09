package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func modulesCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "modules", Short: "Discover enabled Aiplex modules"}
	cmd.AddCommand(&cobra.Command{
		Use: "list", Short: "List enabled modules and their capabilities", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			all, err := newClient().ListModules(cmd.Context())
			if err != nil {
				return err
			}
			switch output {
			case "json":
				printJSON(all)
			case "yaml":
				printYAML(all)
			default:
				var rows [][]string
				for _, d := range all {
					rows = append(rows, []string{d.Name, d.Version, d.ContractVersion, strings.Join(d.Capabilities, ", ")})
				}
				printTable([]string{"MODULE", "VERSION", "CONTRACT", "CAPABILITIES"}, rows)
			}
			return nil
		},
	}, &cobra.Command{
		Use: "inspect <name>", Short: "Inspect a module's published contract", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			d, err := newClient().GetModule(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if output == "yaml" {
				printYAML(d)
			} else {
				printJSON(d)
			}
			return nil
		},
	})
	return cmd
}

type cliModule struct {
	name     string
	commands []func() *cobra.Command
}

// Trusted command factories are registered locally. Server metadata supplies
// availability, never executable commands. Discovery runs lazily, so --help
// and shell completion work without network access.
func registerModuleCommands(root *cobra.Command) {
	for _, registration := range []cliModule{
		{name: "llmplex", commands: []func() *cobra.Command{llmCmd}},
		{name: "a2aplex", commands: []func() *cobra.Command{a2aCmd}},
		{name: "skillsplex", commands: []func() *cobra.Command{skillsCmd}},
		{name: "clientplex", commands: []func() *cobra.Command{clientsCmd}},
	} {
		for _, factory := range registration.commands {
			command := factory()
			guardModuleCommand(command, registration.name)
			root.AddCommand(command)
		}
	}
}

func guardModuleCommand(command *cobra.Command, name string) {
	if command.Annotations == nil {
		command.Annotations = map[string]string{}
	}
	command.Annotations["aiplex.module"] = name
	previous := command.PreRunE
	command.PreRunE = func(cmd *cobra.Command, args []string) error {
		d, err := newClient().GetModule(cmd.Context(), name)
		if err != nil {
			return fmt.Errorf("module %s unavailable: %w", name, err)
		}
		if d.ContractVersion != "v1" {
			return fmt.Errorf("module %s requires unsupported contract %q", name, d.ContractVersion)
		}
		if previous != nil {
			return previous(cmd, args)
		}
		return nil
	}
	for _, child := range command.Commands() {
		guardModuleCommand(child, name)
	}
}
