package main

import "github.com/spf13/cobra"

// shortDescriptions are the one-line descriptions `aiplex --help` shows for
// top-level commands. Each says the outcome, not the mechanism.
var shortDescriptions = map[string]string{
	"dev":        "Run AIPlex on this machine with a mock model, mock tools and the Console",
	"login":      "Sign in to an AIPlex API and store the token for the current context",
	"logout":     "Forget the stored token for the current context",
	"whoami":     "Show which API and identity the CLI is using, and your roles",
	"config":     "Manage named contexts (API URL and credentials per environment)",
	"ctx":        "Switch to another context, or show the current one",
	"doctor":     "Diagnose your setup and say what to fix",
	"health":     "Check that the API and its components answer",
	"init":       "Set up AIPlex on a Google Cloud project (guided)",
	"quickstart": "Create a cloud deployment end to end in one command",
	"version":    "Show the CLI version, commit and whether the Console is built in",

	"access-points": "Create and manage access points: one URL with identity, access and quota",
	"connect":       "Print ready-to-paste client configuration for an access point",
	"clients":       "Register developer clients (IDEs, CLIs) and review their connection plans",
	"who":           "See every caller: how it signs in, what it can reach and its usage",

	"llm": "Connect model providers (surfaces), and inspect routes, batches and capacity",

	"mcp":       "Connect MCP tool servers and test them through the gateway",
	"tools":     "Set a tool connection's credential type and which tools it allows",
	"tool-auth": "Authorize a downstream tool for yourself (OAuth sign-in to the tool)",
	"skills":    "Inspect skill servers and skill invocations",

	"agents":   "Register agents, bind their cloud identities and set their permissions",
	"a2a":      "Discover agents and delegate tasks between them (A2A)",
	"identity": "Let people sign in with your corporate identity provider",

	"usage": "See what each request cost: records, totals and model prices",
	"quota": "Inspect quota policy, balances, loans and admission decisions",

	"platform":  "Deploy, inspect or destroy the AIPlex platform on Google Cloud",
	"upgrade":   "Upgrade a deployed AIPlex platform",
	"infra":     "Review, execute and inspect saved infrastructure plans",
	"native":    "Read and apply native cloud governance plans",
	"resources": "Low-level access to any resource kind (prefer the guided commands)",
	"modules":   "List the modules this API has enabled",
	"apply":     "Apply a declarative AIPlex configuration file",
	"validate":  "Check an AIPlex configuration file without applying it",
	"deploy":    "Deploy an instance from a catalog template",
	"undeploy":  "Remove a deployed instance",
	"list":      "List instances, agents, delegations or policy denials",
	"get":       "Show one instance, agent or delegation",
	"catalog":   "Browse templates you can deploy",
	"logs":      "Stream logs from a deployed instance",
	"runs":      "Inspect and operate durable agent runs",
	"status":    "Show a dashboard summary of the platform",
	"tui":       "Open an interactive terminal dashboard",
	"console":   "Serve the Console locally against the current context",
}

func applyShortDescriptions(root *cobra.Command) {
	for _, c := range root.Commands() {
		if s, ok := shortDescriptions[c.Name()]; ok {
			c.Short = s
		}
	}
}
