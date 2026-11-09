package main

import "github.com/spf13/cobra"

func quotaCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "quota", Short: "Inspect managed quota execution"}
	cmd.AddCommand(&cobra.Command{Use: "attempt <attempt-id>", Args: cobra.ExactArgs(1), Short: "Read an owned or administratively visible quota attempt", RunE: func(cmd *cobra.Command, args []string) error {
		value, err := newClient().GetQuotaAttempt(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		if output == "yaml" {
			printYAML(value)
		} else {
			printJSON(value)
		}
		return nil
	}})
	cmd.AddCommand(quotaAdminCommands()...)
	return cmd
}
