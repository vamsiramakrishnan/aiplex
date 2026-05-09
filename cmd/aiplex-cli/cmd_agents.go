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

	cmd.AddCommand(registerAgentCmd(), deleteAgentCmd(), agentPermissionsCmd(), userCapsCmd())
	return cmd
}

// parseCapsFlag converts strings like "cap://tool/foo@v1:call,read" into Cap entries.
// "cap://tool/foo@v1" alone defaults actions to nil (kind defaults).
func parseCapsFlag(specs []string) []aiplex.Cap {
	out := make([]aiplex.Cap, 0, len(specs))
	for _, s := range specs {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		uri, actions, _ := splitCapSpec(s)
		out = append(out, aiplex.Cap{URI: uri, Actions: actions})
	}
	return out
}

func splitCapSpec(s string) (uri string, actions []string, ok bool) {
	// "cap://tool/foo@v1" or "cap://tool/foo@v1:call,read"
	at := strings.LastIndex(s, "@")
	if at < 0 {
		return s, nil, false
	}
	colon := strings.IndexByte(s[at:], ':')
	if colon < 0 {
		return s, nil, true
	}
	uri = s[:at+colon]
	rest := s[at+colon+1:]
	for _, a := range strings.Split(rest, ",") {
		if a = strings.TrimSpace(a); a != "" {
			actions = append(actions, a)
		}
	}
	return uri, actions, true
}

func registerAgentCmd() *cobra.Command {
	var (
		displayName string
		description string
		authMethod  string
		grantTypes  []string
		caps        []string
	)

	cmd := &cobra.Command{
		Use:   "register <client-id>",
		Short: "Register a new agent",
		Long: `Register an AI agent as an OAuth client.

Cap entries use the form  cap://kind/name@version[:action1,action2]

Examples:
  aiplex agents register tutor-agent --name "Tutor Agent" --auth client_credentials \
    --caps cap://tool/search@v1:call,cap://task/research@v1:invoke,cap://model/gemini-2.5-flash@v1:complete`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()

			agent, err := c.RegisterAgent(context.Background(), &aiplex.RegisterAgentRequest{
				ClientID:    args[0],
				DisplayName: displayName,
				Description: description,
				AuthMethod:  authMethod,
				GrantTypes:  grantTypes,
				AllowedCaps: parseCapsFlag(caps),
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
			if len(agent.AllowedCaps) > 0 {
				fmt.Println("  Caps:")
				for _, c := range agent.AllowedCaps {
					fmt.Printf("    - %s %v\n", c.URI, c.Actions)
				}
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&displayName, "name", "n", "", "Display name")
	cmd.Flags().StringVar(&description, "description", "", "Description")
	cmd.Flags().StringVar(&authMethod, "auth", "client_credentials", "Auth method: client_credentials, authorization_code, device_code")
	cmd.Flags().StringSliceVar(&grantTypes, "grants", []string{"client_credentials"}, "Grant types")
	cmd.Flags().StringSliceVar(&caps, "caps", nil, "Allowed capabilities (Dimension A) — cap://kind/name@v[:actions,...]")
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

func agentPermissionsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "permissions <client-id>",
		Short: "Show agent capabilities across all kinds",
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

			fmt.Printf("Agent: %s\n\n", perms.AgentID)
			for kind, caps := range perms.Ceiling {
				fmt.Printf("  %s:\n", kind)
				for _, c := range caps {
					desc := ""
					if c.Description != "" {
						desc = " — " + c.Description
					}
					fmt.Printf("    %s %v%s\n", c.URI, c.Actions, desc)
				}
			}
			return nil
		},
	}
}

func userCapsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "user-caps",
		Short: "Manage user capabilities (Dimension B)",
	}

	getCmd := &cobra.Command{
		Use:   "get <user-id>",
		Short: "Get user capabilities",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			uc, err := c.GetUserCaps(context.Background(), args[0])
			if err != nil {
				return fmt.Errorf("get user caps: %w", err)
			}

			if output == "json" {
				printJSON(uc)
				return nil
			}

			fmt.Printf("User: %s\n", uc.UserID)
			for kind, caps := range uc.ByKind {
				fmt.Printf("  %s:\n", kind)
				for _, c := range caps {
					fmt.Printf("    - %s %v\n", c.URI, c.Actions)
				}
			}
			return nil
		},
	}

	var caps []string
	setCmd := &cobra.Command{
		Use:   "set <user-id>",
		Short: "Set user capabilities",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			if err := c.SetUserCaps(context.Background(), args[0], parseCapsFlag(caps)); err != nil {
				return fmt.Errorf("set user caps: %w", err)
			}
			fmt.Printf("Updated caps for %s\n", args[0])
			return nil
		},
	}
	setCmd.Flags().StringSliceVar(&caps, "caps", nil, "Capabilities to set — cap://kind/name@v[:actions,...]")
	setCmd.MarkFlagRequired("caps")

	cmd.AddCommand(getCmd, setCmd)
	return cmd
}
