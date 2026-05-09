package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

// demoCmd deploys the Ember tutor scenario end-to-end against a running
// `aiplex up` node. After it returns the local node has:
//
//   - A model cap (Gemini Flash) deployed
//   - A memory namespace for student profiles deployed
//   - A workflow cap chaining the two (grade-quiz)
//
// The user can then invoke the workflow from the SDK or curl to see a
// receipt-traced multi-cap delegation in action — the visceral demo of
// "the capability primitive is real and yours."
//
// Connects to the URL/token resolved by newClient(); pair with `aiplex up`.
func demoCmd() *cobra.Command {
	var dataDir string
	cmd := &cobra.Command{
		Use:   "demo",
		Short: "Deploy the Ember tutor scenario against a local AIPlex node",
		Long: `Deploys a working multi-cap example: a Gemini model proxy, a
student-profile memory namespace, and a 'grade-quiz' workflow that chains
them. Use this immediately after 'aiplex up' to see the Capability primitive
in action.

After it completes, try:
  curl -H "Authorization: Bearer $(cat ~/.aiplex/token)" \
       -H "Content-Type: application/json" \
       -d '{"input":{"quiz_id":"q-1","student":"alice"}}' \
       http://localhost:8080/cap/workflow/grade-quiz@v1/_invoke`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if dataDir == "" {
				home, _ := os.UserHomeDir()
				dataDir = filepath.Join(home, ".aiplex")
			}

			// Use the locally-persisted token if no explicit one was passed.
			if token == "" && os.Getenv("AIPLEX_TOKEN") == "" {
				if data, err := os.ReadFile(filepath.Join(dataDir, "token")); err == nil {
					token = string(data)
				}
			}

			c := newClient()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			steps := []struct {
				label string
				kind  string
				tmpl  string
			}{
				{"Gemini Flash model proxy", "model", "gemini-2.5-flash"},
				{"Student profile memory namespace", "memory", "user-profile"},
				{"Grade-quiz workflow", "workflow", "grade-quiz"},
			}

			fmt.Println("Deploying the Ember tutor scenario:")
			for i, s := range steps {
				fmt.Printf("  [%d/%d] %s … ", i+1, len(steps), s.label)
				inst, err := c.Deploy(ctx, &aiplex.DeployRequest{
					Kind:        s.kind,
					TemplateID:  s.tmpl,
					DisplayName: s.label,
				})
				if err != nil {
					fmt.Printf("FAILED: %v\n", err)
					return err
				}
				fmt.Printf("ok (%s)\n", inst.ID)
			}

			fmt.Println()
			fmt.Println("All three capabilities are deployed and bound to your local user.")
			fmt.Println("Try invoking the workflow:")
			fmt.Println()
			fmt.Println("  curl -H \"Authorization: Bearer $(cat ~/.aiplex/token)\" \\")
			fmt.Println("       -H \"Content-Type: application/json\" \\")
			fmt.Println("       -d '{\"input\":{\"quiz_id\":\"q-1\",\"student\":\"alice\"}}' \\")
			fmt.Println("       http://localhost:8080/cap/workflow/grade-quiz@v1/_invoke")
			fmt.Println()
			fmt.Println("Or browse caps in the Console:  http://localhost:8080/")
			return nil
		},
	}
	cmd.Flags().StringVar(&dataDir, "data-dir", "", "Where to find the local token (default ~/.aiplex)")
	return cmd
}
