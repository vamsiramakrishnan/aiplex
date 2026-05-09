package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

func catalogCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "catalog",
		Short: "Browse the template catalog",
	}

	cmd.AddCommand(catalogListCmd(), catalogGetCmd())
	return cmd
}

func catalogListCmd() *cobra.Command {
	var kind string

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List catalog templates",
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			opts := &aiplex.ListCatalogOpts{}
			if kind != "" {
				opts.Kind = kind
			}
			page, err := c.ListCatalog(context.Background(), opts)
			if err != nil {
				return fmt.Errorf("list catalog: %w", err)
			}

			if output == "json" {
				printJSON(page)
				return nil
			}

			headers := []string{"ID", "KIND", "NAME", "PROVIDER", "VERIFIED"}
			var rows [][]string
			for _, t := range page.Templates {
				verified := ""
				if t.Verified {
					verified = "yes"
				}
				rows = append(rows, []string{
					t.ID, t.Kind, t.Name, t.Provider, verified,
				})
			}
			printTable(headers, rows)
			fmt.Printf("\n%d template(s) (page %d)\n", page.Total, page.Page)
			return nil
		},
	}

	cmd.Flags().StringVarP(&kind, "kind", "k", "", "Filter by capability kind: tool, task, model, skill, memory")
	return cmd
}

func catalogGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <template-id>",
		Short: "Get template details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			t, err := c.GetTemplate(context.Background(), args[0])
			if err != nil {
				return fmt.Errorf("get template: %w", err)
			}

			if output == "json" {
				printJSON(t)
				return nil
			}

			fmt.Printf("Template: %s\n", t.ID)
			fmt.Printf("  Name:        %s\n", t.Name)
			fmt.Printf("  Kind:        %s\n", t.Kind)
			fmt.Printf("  Description: %s\n", t.Description)
			if t.Provider != "" {
				fmt.Printf("  Provider:    %s\n", t.Provider)
			}
			if t.ModelID != "" {
				fmt.Printf("  Model:       %s\n", t.ModelID)
			}
			if t.Image != "" {
				fmt.Printf("  Image:       %s\n", t.Image)
			}
			if t.Version != "" {
				fmt.Printf("  Version:     %s\n", t.Version)
			}
			if len(t.Capabilities) > 0 {
				fmt.Println("  Capabilities:")
				for _, c := range t.Capabilities {
					fmt.Printf("    - %s: %s\n", c.URI, c.Description)
				}
			}
			if t.Pricing != nil {
				fmt.Printf("  Pricing:     $%.2f/M input, $%.2f/M output\n", t.Pricing.Input, t.Pricing.Output)
			}
			if t.Verified {
				fmt.Println("  Verified:    yes")
			}
			return nil
		},
	}
}
