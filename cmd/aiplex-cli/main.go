package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/vamsiramakrishnan/aiplex/internal/cliconfig"
	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

var (
	apiURL string
	token  string
	output string
)

// newClient builds an SDK client, resolving config in order:
// flags → env vars → ~/.aiplex/config.json + credentials.json
func newClient() *aiplex.Client {
	url := apiURL
	if url == "" {
		url = os.Getenv("AIPLEX_URL")
	}

	t := token
	if t == "" {
		t = os.Getenv("AIPLEX_TOKEN")
	}

	// Fall back to persistent config
	if url == "" || t == "" {
		cfg, err := cliconfig.Load()
		if err == nil {
			if ctx, err := cfg.Current(); err == nil {
				if url == "" && ctx.URL != "" {
					url = ctx.URL
				}
				if t == "" {
					creds, err := cliconfig.LoadCredentials()
					if err == nil {
						if entry := creds.GetToken(ctx.Name); entry != nil {
							t = entry.AccessToken
						}
					}
				}
			}
		}
	}

	if url == "" {
		url = "http://localhost:8080"
	}

	c := aiplex.NewClient(url)
	if t != "" {
		c.SetToken(t)
	}
	return c
}

func main() {
	root := &cobra.Command{
		Use:   "aiplex",
		Short: "AIPlex CLI — unified control plane for AI agent interactions",
		Long: `AIPlex CLI manages three planes through a single gateway:
  MCPlex  — Agent ↔ Tool   (MCP servers)
  A2APlex — Agent ↔ Agent  (A2A delegation)
  LLMPlex — Agent ↔ Model  (LLM providers)

Get started:
  aiplex init             Set up AIPlex on a GCP project
  aiplex login            Authenticate with your AIPlex instance
  aiplex config show      View current configuration
  aiplex health           Check connectivity to all components`,
		SilenceUsage: true,
	}

	root.PersistentFlags().StringVar(&apiURL, "url", "", "AIPlex API URL (env: AIPLEX_URL)")
	root.PersistentFlags().StringVar(&token, "token", "", "Bearer token (env: AIPLEX_TOKEN)")
	root.PersistentFlags().StringVarP(&output, "output", "o", "table", "Output format: table, json, yaml")
	root.PersistentFlags().BoolVar(&debug, "debug", false, "Enable debug output")

	root.AddCommand(
		// Onboarding
		initCmd(),
		loginCmd(),
		logoutCmd(),
		configCmd(),
		healthCmd(),
		doctorCmd(),
		platformCmd(),

		// Operations
		statusCmd(),
		applyCmd(),
		deployCmd(),
		undeployCmd(),
		logsCmd(),
		listCmd(),
		getCmd(),
		agentsCmd(),
		llmCmd(),
		a2aCmd(),
		catalogCmd(),

		// Utilities
		completionCmd(),
		versionCmd(),
		validateCmd(),
	)

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func exitErr(msg string, err error) {
	fmt.Fprintf(os.Stderr, "Error: %s: %v\n", msg, err)
	os.Exit(1)
}
