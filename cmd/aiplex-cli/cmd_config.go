package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/vamsiramakrishnan/aiplex/internal/cliconfig"
)

func configCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage CLI configuration and contexts",
		Long: `Manage named contexts for different AIPlex environments.

Each context stores a URL, GCP project, region, and domain.
Credentials are stored separately in ~/.aiplex/credentials.json.

Examples:
  aiplex config set-context dev --url http://localhost:8080
  aiplex config set-context prod --url https://aiplex.example.com --project my-gcp-project
  aiplex config use-context prod
  aiplex config show`,
	}

	cmd.AddCommand(
		configShowCmd(),
		configSetContextCmd(),
		configUseContextCmd(),
		configDeleteContextCmd(),
		configCurrentCmd(),
	)
	return cmd
}

func configShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Show current configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := cliconfig.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			if output == "json" {
				printJSON(cfg)
				return nil
			}

			if len(cfg.Contexts) == 0 {
				fmt.Println("No contexts configured.")
				fmt.Println()
				fmt.Println("Get started:")
				fmt.Println("  aiplex init                                    # guided setup")
				fmt.Println("  aiplex config set-context dev --url http://localhost:8080  # manual")
				return nil
			}

			dir, _ := cliconfig.Dir()
			fmt.Printf("Config: %s/config.json\n", dir)
			fmt.Printf("Active: %s\n", cfg.CurrentContext)
			fmt.Println()

			headers := []string{"CONTEXT", "URL", "PROJECT", "REGION", "ACTIVE"}
			var rows [][]string
			for name, ctx := range cfg.Contexts {
				active := ""
				if name == cfg.CurrentContext {
					active = "*"
				}
				rows = append(rows, []string{
					name, ctx.URL, ctx.Project, ctx.Region, active,
				})
			}
			printTable(headers, rows)

			creds, _ := cliconfig.LoadCredentials()
			if creds != nil {
				fmt.Println()
				for name := range cfg.Contexts {
					if t := creds.GetToken(name); t != nil {
						fmt.Printf("  %s: authenticated\n", name)
					} else {
						fmt.Printf("  %s: no credentials\n", name)
					}
				}
			}
			return nil
		},
	}
}

func configSetContextCmd() *cobra.Command {
	var (
		url     string
		project string
		region  string
		domain  string
	)

	cmd := &cobra.Command{
		Use:   "set-context <name>",
		Short: "Create or update a named context",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			cfg, err := cliconfig.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			// Merge with existing if updating
			if existing, ok := cfg.Contexts[name]; ok {
				if url == "" {
					url = existing.URL
				}
				if project == "" {
					project = existing.Project
				}
				if region == "" {
					region = existing.Region
				}
				if domain == "" {
					domain = existing.Domain
				}
			}

			cfg.SetContext(name, url, project, region, domain)
			if err := cfg.Save(); err != nil {
				return fmt.Errorf("save config: %w", err)
			}

			fmt.Printf("Context %q set and activated.\n", name)
			fmt.Printf("  URL:     %s\n", url)
			if project != "" {
				fmt.Printf("  Project: %s\n", project)
			}
			if region != "" {
				fmt.Printf("  Region:  %s\n", region)
			}
			if domain != "" {
				fmt.Printf("  Domain:  %s\n", domain)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&url, "url", "", "API URL")
	cmd.Flags().StringVar(&project, "project", "", "GCP project ID")
	cmd.Flags().StringVar(&region, "region", "", "GCP region")
	cmd.Flags().StringVar(&domain, "domain", "", "Custom domain")
	return cmd
}

func configUseContextCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "use-context <name>",
		Short: "Switch to a different context",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			cfg, err := cliconfig.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			if _, ok := cfg.Contexts[name]; !ok {
				return fmt.Errorf("context %q not found — available: %v", name, contextNames(cfg))
			}
			cfg.CurrentContext = name
			if err := cfg.Save(); err != nil {
				return fmt.Errorf("save config: %w", err)
			}
			fmt.Printf("Switched to context %q.\n", name)
			return nil
		},
	}
}

func configDeleteContextCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete-context <name>",
		Short: "Delete a named context",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			cfg, err := cliconfig.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			if _, ok := cfg.Contexts[name]; !ok {
				return fmt.Errorf("context %q not found", name)
			}
			delete(cfg.Contexts, name)
			if cfg.CurrentContext == name {
				cfg.CurrentContext = ""
			}
			if err := cfg.Save(); err != nil {
				return fmt.Errorf("save config: %w", err)
			}

			// Also remove credentials
			creds, _ := cliconfig.LoadCredentials()
			if creds != nil {
				delete(creds.Tokens, name)
				creds.Save()
			}

			fmt.Printf("Deleted context %q.\n", name)
			return nil
		},
	}
}

func configCurrentCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "current-context",
		Short: "Show the active context name",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := cliconfig.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			if cfg.CurrentContext == "" {
				fmt.Println("No active context. Run: aiplex config set-context <name>")
				return nil
			}
			fmt.Println(cfg.CurrentContext)
			return nil
		},
	}
}

func contextNames(cfg *cliconfig.Config) []string {
	names := make([]string, 0, len(cfg.Contexts))
	for n := range cfg.Contexts {
		names = append(names, n)
	}
	return names
}
