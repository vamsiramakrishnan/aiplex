package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

var (
	apiURL string
	token  string
	output string
)

func newClient() *aiplex.Client {
	url := apiURL
	if url == "" {
		url = os.Getenv("AIPLEX_URL")
	}
	if url == "" {
		url = "http://localhost:8080"
	}
	c := aiplex.NewClient(url)

	t := token
	if t == "" {
		t = os.Getenv("AIPLEX_TOKEN")
	}
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
  LLMPlex — Agent ↔ Model  (LLM providers)`,
		SilenceUsage: true,
	}

	root.PersistentFlags().StringVar(&apiURL, "url", "", "AIPlex API URL (env: AIPLEX_URL)")
	root.PersistentFlags().StringVar(&token, "token", "", "Bearer token (env: AIPLEX_TOKEN)")
	root.PersistentFlags().StringVarP(&output, "output", "o", "table", "Output format: table, json")

	root.AddCommand(
		statusCmd(),
		deployCmd(),
		undeployCmd(),
		listCmd(),
		getCmd(),
		agentsCmd(),
		llmCmd(),
		a2aCmd(),
		catalogCmd(),
	)

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func exitErr(msg string, err error) {
	fmt.Fprintf(os.Stderr, "Error: %s: %v\n", msg, err)
	os.Exit(1)
}
