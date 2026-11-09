package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/vamsiramakrishnan/aiplex/internal/cliconfig"
)

func whoamiCmd() *cobra.Command {
	var server bool
	command := &cobra.Command{
		Use:   "whoami",
		Short: "Show current context, account, and auth status",
		Long:  `Displays the active context, GCP project, region, domain, and authentication status.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if server {
				identity, err := newClient().GetIdentity(cmd.Context())
				if err != nil {
					return err
				}
				if output == "yaml" {
					printYAML(identity)
				} else {
					printJSON(identity)
				}
				return nil
			}
			cfg, err := cliconfig.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			if cfg.CurrentContext == "" {
				fmt.Println("No active context. Run: aiplex init")
				return nil
			}

			ctx, err := cfg.Current()
			if err != nil {
				return fmt.Errorf("current context: %w", err)
			}

			fmt.Printf("Context:  %s\n", cfg.CurrentContext)
			fmt.Printf("URL:      %s\n", ctx.URL)
			if ctx.Project != "" {
				fmt.Printf("Project:  %s\n", ctx.Project)
			}
			if ctx.Region != "" {
				fmt.Printf("Region:   %s\n", ctx.Region)
			}
			if ctx.Domain != "" {
				fmt.Printf("Domain:   %s\n", ctx.Domain)
			}

			// Auth status
			creds, err := cliconfig.LoadCredentials()
			if err == nil {
				tok := creds.GetToken(cfg.CurrentContext)
				if tok != nil && tok.AccessToken != "" {
					fmt.Println("Auth:     authenticated")
				} else {
					fmt.Println("Auth:     not authenticated (run: aiplex login)")
				}
			} else {
				fmt.Println("Auth:     unknown")
			}

			return nil
		},
	}
	command.Flags().BoolVar(&server, "server", false, "Resolve verified server identity, canonical principal ID, groups, and scopes")
	return command

}
