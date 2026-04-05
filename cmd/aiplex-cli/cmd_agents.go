package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

func agentsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agents",
		Short: "Manage registered agents",
	}

	cmd.AddCommand(registerAgentCmd(), deleteAgentCmd(), grantScopesCmd(), userScopesCmd())
	return cmd
}

func registerAgentCmd() *cobra.Command {
	var (
		displayName string
		description string
		authMethod  string
		grantTypes  []string
		scopes      []string
	)

	cmd := &cobra.Command{
		Use:   "register <client-id>",
		Short: "Register a new agent",
		Long: `Register an AI agent as an OAuth client.

Examples:
  aiplex agents register tutor-agent --name "Tutor Agent" --auth client_credentials \
    --scopes mcp:tools:search,a2a:task:research,llm:model:gemini-2.5-flash`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()

			agent, err := c.RegisterAgent(context.Background(), &aiplex.RegisterAgentRequest{
				ClientID:      args[0],
				DisplayName:   displayName,
				Description:   description,
				AuthMethod:    authMethod,
				GrantTypes:    grantTypes,
				AllowedScopes: scopes,
			})
			if err != nil {
				return fmt.Errorf("register agent: %w", err)
			}

			if output == "json" {
				printJSON(agent)
				return nil
			}

			fmt.Printf("Registered agent: %s\n", agent.ClientID)
			fmt.Printf("  Name:   %s\n", agent.DisplayName)
			fmt.Printf("  Auth:   %s\n", agent.AuthMethod)
			fmt.Printf("  Status: %s\n", agent.Status)
			if len(agent.AllowedScopes) > 0 {
				fmt.Printf("  Scopes: %s\n", strings.Join(agent.AllowedScopes, ", "))
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&displayName, "name", "n", "", "Display name")
	cmd.Flags().StringVar(&description, "description", "", "Description")
	cmd.Flags().StringVar(&authMethod, "auth", "client_credentials", "Auth method: client_credentials, authorization_code, device_code")
	cmd.Flags().StringSliceVar(&grantTypes, "grants", []string{"client_credentials"}, "Grant types")
	cmd.Flags().StringSliceVar(&scopes, "scopes", nil, "Allowed scopes (Dimension A)")
	return cmd
}

func deleteAgentCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <client-id>",
		Short: "Delete a registered agent",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			if err := c.DeleteAgent(context.Background(), args[0]); err != nil {
				return fmt.Errorf("delete agent: %w", err)
			}
			fmt.Printf("Deleted agent: %s\n", args[0])
			return nil
		},
	}
}

func grantScopesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "permissions <client-id>",
		Short: "Show agent permissions across all planes",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			perms, err := c.GetAgentPermissions(context.Background(), args[0])
			if err != nil {
				return fmt.Errorf("get permissions: %w", err)
			}

			if output == "json" {
				printJSON(perms)
				return nil
			}

			fmt.Printf("Agent: %s\n", perms.AgentID)
			fmt.Println()
			for plane, scopes := range perms.Ceiling {
				fmt.Printf("  %s:\n", plane)
				for _, s := range scopes {
					desc := ""
					if s.Description != "" {
						desc = " — " + s.Description
					}
					fmt.Printf("    %s%s\n", s.Scope, desc)
				}
			}
			return nil
		},
	}
}

func userScopesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "user-scopes",
		Short: "Manage user scopes (Dimension B)",
	}

	getCmd := &cobra.Command{
		Use:   "get <user-id>",
		Short: "Get user scopes",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			us, err := c.GetUserScopes(context.Background(), args[0])
			if err != nil {
				return fmt.Errorf("get user scopes: %w", err)
			}

			if output == "json" {
				printJSON(us)
				return nil
			}

			fmt.Printf("User: %s\n", us.UserID)
			for plane, scopes := range us.Scopes {
				fmt.Printf("  %s:\n", plane)
				for _, s := range scopes {
					fmt.Printf("    - %s\n", s)
				}
			}
			return nil
		},
	}

	var scopes []string
	setCmd := &cobra.Command{
		Use:   "set <user-id>",
		Short: "Set user scopes",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			us, err := c.SetUserScopes(context.Background(), args[0], scopes)
			if err != nil {
				return fmt.Errorf("set user scopes: %w", err)
			}

			if output == "json" {
				printJSON(us)
				return nil
			}

			fmt.Printf("Updated scopes for %s\n", us.UserID)
			return nil
		},
	}
	setCmd.Flags().StringSliceVar(&scopes, "scopes", nil, "Scopes to set")
	setCmd.MarkFlagRequired("scopes")

	cmd.AddCommand(getCmd, setCmd)
	return cmd
}
