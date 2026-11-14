package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

func skillsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skills",
		Short: "SkillsPlex — manage skill servers and invocations",
	}
	cmd.AddCommand(skillsServersCmd(), skillsInvokeCmd(), skillsInvocationsCmd())
	return cmd
}

func skillsServersCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "servers",
		Short: "List running SkillsPlex servers",
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			servers, err := c.ListSkillServers(context.Background())
			if err != nil {
				return fmt.Errorf("list skill servers: %w", err)
			}

			if output == "json" {
				printJSON(servers)
				return nil
			}

			headers := []string{"INSTANCE", "NAME", "BUNDLE", "SKILLS", "STATUS"}
			var rows [][]string
			for _, s := range servers {
				skillCount := fmt.Sprintf("%d", len(s.Skills))
				rows = append(rows, []string{s.InstanceID, s.Name, s.SkillBundle, skillCount, s.Status})
			}
			printTable(headers, rows)
			fmt.Printf("\n%d skill server(s)\n", len(servers))
			return nil
		},
	}
}

func skillsInvokeCmd() *cobra.Command {
	var (
		agentID    string
		instanceID string
		skill      string
		userID     string
		traceID    string
	)

	cmd := &cobra.Command{
		Use:   "record-invocation",
		Short: "Record a skill invocation audit event",
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			inv, err := c.RecordSkillInvocation(context.Background(), &aiplex.RecordSkillInvocationRequest{
				AgentID:    agentID,
				InstanceID: instanceID,
				SkillName:  skill,
				UserID:     userID,
				TraceID:    traceID,
			})
			if err != nil {
				return fmt.Errorf("record invocation: %w", err)
			}

			if output == "json" {
				printJSON(inv)
				return nil
			}

			fmt.Printf("Invocation: %s\n", inv.ID)
			fmt.Printf("  %s invoked %s on %s\n", inv.AgentID, inv.SkillName, inv.InstanceID)
			fmt.Printf("  Status: %s\n", inv.Status)
			fmt.Printf("  Trace:  %s\n", inv.TraceID)
			return nil
		},
	}

	cmd.Flags().StringVar(&agentID, "agent", "", "Agent ID (required)")
	cmd.Flags().StringVar(&instanceID, "instance", "", "Skill server instance ID (required)")
	cmd.Flags().StringVarP(&skill, "skill", "s", "", "Skill name (required)")
	cmd.Flags().StringVarP(&userID, "user", "u", "", "User ID")
	cmd.Flags().StringVar(&traceID, "trace", "", "W3C trace id (optional)")
	cmd.MarkFlagRequired("agent")
	cmd.MarkFlagRequired("instance")
	cmd.MarkFlagRequired("skill")

	return cmd
}

func skillsInvocationsCmd() *cobra.Command {
	var (
		agentID string
		skill   string
		limit   int
	)
	cmd := &cobra.Command{
		Use:     "invocations",
		Short:   "List recent skill invocations",
		Long:    "List recent skill invocations. --limit caps how many are returned (1 to 1000, default 100).",
		Example: "  aiplex skills invocations\n  aiplex skills invocations --agent tutor-agent --limit 20\n  aiplex skills invocations -o json",
		RunE: func(cmd *cobra.Command, args []string) error {
			if limit < 1 || limit > 1000 {
				return usageError("--limit must be between 1 and 1000")
			}
			c := newClient()
			invs, err := c.ListSkillInvocations(context.Background(), agentID, skill, aiplex.ListSkillInvocationsOpts{Limit: limit})
			if err != nil {
				return fmt.Errorf("list invocations: %w", err)
			}
			if invs == nil {
				invs = []aiplex.SkillInvocation{}
			}

			if spStructured(cmd, invs) {
				return nil
			}

			headers := []string{"ID", "AGENT", "SKILL", "STATUS", "DURATION", "TRACE"}
			var rows [][]string
			for _, inv := range invs {
				dur := "-"
				if inv.DurationMs > 0 {
					dur = fmt.Sprintf("%dms", inv.DurationMs)
				}
				trace := inv.TraceID
				if len(trace) > 8 {
					trace = trace[:8]
				}
				rows = append(rows, []string{inv.ID, inv.AgentID, inv.SkillName, inv.Status, dur, trace})
			}
			spTable(cmd.OutOrStdout(), headers, rows)
			fmt.Fprintf(cmd.OutOrStdout(), "\n%d invocation(s)\n", len(invs))
			return nil
		},
	}
	cmd.Flags().StringVar(&agentID, "agent", "", "Filter by agent ID")
	cmd.Flags().StringVarP(&skill, "skill", "s", "", "Filter by skill name")
	cmd.Flags().IntVar(&limit, "limit", 100, "Most invocations to return (1 to 1000)")
	return cmd
}
