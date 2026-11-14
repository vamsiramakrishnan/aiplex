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
	var (
		plane    string
		page     int
		pageSize int
	)

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List catalog templates",
		Long: `List catalog templates one page at a time. --page is zero-based and
--page-size is 1 to 100 (default 20). When another page exists the command
prints the exact command for the next one.`,
		Example: "  aiplex catalog list\n  aiplex catalog list --plane llmplex --page-size 50\n  aiplex catalog list --page 2 -o json",
		RunE: func(cmd *cobra.Command, args []string) error {
			if page < 0 {
				return usageError("--page must be 0 or more")
			}
			if pageSize < 1 || pageSize > 100 {
				return usageError("--page-size must be between 1 and 100")
			}
			c := newClient()
			result, err := c.ListCatalog(context.Background(), &aiplex.ListCatalogOpts{Plane: plane, Page: page, PageSize: pageSize})
			if err != nil {
				return fmt.Errorf("list catalog: %w", err)
			}
			if spStructured(cmd, result) {
				return nil
			}

			out := cmd.OutOrStdout()
			headers := []string{"ID", "PLANE", "NAME", "PROVIDER", "VERIFIED"}
			rows := make([][]string, 0, len(result.Templates))
			for _, t := range result.Templates {
				verified := "no"
				if t.Verified {
					verified = "yes"
				}
				rows = append(rows, []string{t.ID, t.Plane, t.Name, orUnknown(t.Provider), verified})
			}
			spTable(out, headers, rows)
			fmt.Fprintf(out, "\n%d template(s) (page %d)\n", result.Total, result.Page)
			// Pages run 0..(total-1)/pageSize. Compare without multiplying so a
			// large --page cannot overflow.
			if result.Total > 0 && page < (result.Total-1)/pageSize {
				fmt.Fprintf(out, "\nMore: %s\n", catalogListNextCommand(plane, page+1, pageSize))
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&plane, "plane", "p", "", "Filter by plane: mcplex, a2aplex, llmplex")
	cmd.Flags().IntVar(&page, "page", 0, "Page to show, zero-based")
	cmd.Flags().IntVar(&pageSize, "page-size", 20, "Templates per page (1 to 100)")
	return cmd
}

// catalogListNextCommand is the command that shows the page after this one. It
// keeps the plane filter so the printed command returns the same listing.
func catalogListNextCommand(plane string, page, pageSize int) string {
	cmd := "aiplex catalog list"
	if plane != "" {
		cmd += " --plane " + runsShellArg(plane)
	}
	return fmt.Sprintf("%s --page %d --page-size %d", cmd, page, pageSize)
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
