package main

import (
	"context"
	"fmt"
	"strings"

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
	var plane string

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List catalog templates",
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			opts := &aiplex.ListCatalogOpts{}
			if plane != "" {
				opts.Plane = plane
			}
			page, err := c.ListCatalog(context.Background(), opts)
			if err != nil {
				return fmt.Errorf("list catalog: %w", err)
			}

			if output == "json" {
				printJSON(page)
				return nil
			}

			headers := []string{"ID", "PLANE", "NAME", "PROVIDER", "VERIFIED"}
			var rows [][]string
			for _, t := range page.Templates {
				verified := ""
				if t.Verified {
					verified = "yes"
				}
				rows = append(rows, []string{
					t.ID, t.Plane, t.Name, t.Provider, verified,
				})
			}
			printTable(headers, rows)
			fmt.Printf("\n%d template(s) (page %d)\n", page.Total, page.Page)
			return nil
		},
	}

	cmd.Flags().StringVarP(&plane, "plane", "p", "", "Filter by plane: mcplex, a2aplex, llmplex")
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
			fmt.Printf("  Plane:       %s\n", t.Plane)
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
				fmt.Printf("  Capabilities: %s\n", strings.Join(t.Capabilities, ", "))
			}
			if len(t.Tools) > 0 {
				fmt.Println("  Tools:")
				for _, tool := range t.Tools {
					fmt.Printf("    - %s: %s\n", tool.Name, tool.Description)
				}
			}
			if len(t.TaskTypes) > 0 {
				fmt.Printf("  Task Types: %s\n", strings.Join(t.TaskTypes, ", "))
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
