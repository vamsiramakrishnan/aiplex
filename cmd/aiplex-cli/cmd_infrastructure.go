package main

import (
	"fmt"
	"github.com/spf13/cobra"
)

func infrastructureCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "infra", Short: "Review, execute and inspect saved infrastructure plans"}
	for _, action := range []string{"approve", "execute", "receipt", "observe", "observation"} {
		var digest, reason, approval, key string
		c := &cobra.Command{Use: action + " <plan-id>", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			client := newClient()
			var value any
			var err error
			switch action {
			case "approve":
				if digest == "" || reason == "" {
					return fmt.Errorf("--digest and --reason required")
				}
				value, err = client.ApproveDeployment(cmd.Context(), args[0], digest, reason)
			case "execute":
				if digest == "" || approval == "" {
					return fmt.Errorf("--digest and --approval required")
				}
				value, err = client.ExecuteDeployment(cmd.Context(), args[0], digest, approval)
			case "receipt":
				value, err = client.GetDeploymentReceipt(cmd.Context(), args[0])
			case "observation":
				if key == "" {
					return fmt.Errorf("--idempotency-key required")
				}
				value, err = client.GetDeploymentObservation(cmd.Context(), args[0], key)
			case "observe":
				if key == "" {
					return fmt.Errorf("--idempotency-key required")
				}
				value, err = client.ObserveDeployment(cmd.Context(), args[0], key)
			}
			if err == nil {
				toolAuthPrint(value)
			}
			return err
		}}
		if action == "approve" || action == "execute" {
			c.Flags().StringVar(&digest, "digest", "", "Exact reviewed plan digest")
		}
		if action == "approve" {
			c.Flags().StringVar(&reason, "reason", "", "Approval reason")
		}
		if action == "execute" {
			c.Flags().StringVar(&approval, "approval", "", "Saved approval record ID")
		}
		if action == "observe" || action == "observation" {
			c.Flags().StringVar(&key, "idempotency-key", "", "Stable observation attempt ID")
		}
		cmd.AddCommand(c)
	}
	return cmd
}
