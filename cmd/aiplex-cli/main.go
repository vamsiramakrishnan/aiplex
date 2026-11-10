package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/vamsiramakrishnan/aiplex/internal/cliconfig"
	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

func init() {
	// Add mise shims to PATH so exec.Command finds mise-managed tools
	// (terraform, helm, kubectl, etc.) without requiring `eval "$(mise activate bash)"`
	home, _ := os.UserHomeDir()
	if home != "" {
		shims := filepath.Join(home, ".local", "share", "mise", "shims")
		if _, err := os.Stat(shims); err == nil {
			os.Setenv("PATH", shims+":"+os.Getenv("PATH"))
		}
	}
}

var (
	apiURL string
	token  string
	output string
)

// newClient builds an SDK client for the resolved target (see target.go).
func newClient() *aiplex.Client {
	t := resolveTarget()
	debugf("API %s (from %s), token from %s", t.URL, t.URLSource, orNone(t.TokenSource))
	c := aiplex.NewClient(t.URL)
	if t.Token != "" {
		c.SetToken(t.Token)
	}
	if isLoopbackURL(t.URL) && c.HTTPClient != nil {
		base := c.HTTPClient.Transport
		if base == nil {
			base = http.DefaultTransport
		}
		c.HTTPClient.Transport = &writeRecorder{base: base, url: t.URL}
	}
	return c
}

func orNone(s string) string {
	if s == "" {
		return "nowhere"
	}
	return s
}

// Command groups for `aiplex --help`, by what a person is trying to do.
var commandGroups = []*cobra.Group{
	{ID: "start", Title: "Get started:"},
	{ID: "connect", Title: "Connect clients:"},
	{ID: "models", Title: "Models:"},
	{ID: "tools", Title: "Tools:"},
	{ID: "agents", Title: "Agents and identity:"},
	{ID: "usage", Title: "Usage and quota:"},
	{ID: "operate", Title: "Operate:"},
}

// groupOf assigns each top-level command to a help group.
var groupOf = map[string]string{
	"dev": "start", "login": "start", "auth": "start", "logout": "start", "whoami": "start", "config": "start", "ctx": "start",
	"doctor": "start", "health": "start", "init": "start", "quickstart": "start", "version": "start",
	"access-points": "connect", "connect": "connect", "clients": "connect", "who": "connect",
	"llm": "models",
	"mcp": "tools", "tools": "tools", "tool-auth": "tools", "skills": "tools",
	"agents": "agents", "a2a": "agents", "identity": "agents",
	"usage": "usage", "quota": "usage",
	"platform": "operate", "upgrade": "operate", "infra": "operate", "native": "operate", "resources": "operate",
	"modules": "operate", "apply": "operate", "validate": "operate", "deploy": "operate", "undeploy": "operate",
	"list": "operate", "get": "operate", "catalog": "operate", "logs": "operate", "runs": "operate",
	"status": "operate", "tui": "operate", "console": "operate",
}

// newRootCmd builds the whole command tree. Building it never contacts the
// server, so --help and completion of command names work offline.
func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "aiplex",
		Short: "AIPlex CLI — one gateway, identity and audit trail for models, tools and agents",
		Long: `AIPlex puts models, MCP tools and agents behind one gateway with one identity,
one access policy and one usage ledger.

Start here (no Docker, no cloud account):
  aiplex dev                 run AIPlex locally with a mock model and mock tools
  eval "$(aiplex dev env)"   in another terminal: point SDKs and the CLI at it
  aiplex connect local       print client configuration for the local access point`,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			switch output {
			case "table", "wide", "json", "yaml":
			case "":
				output = "table"
			default:
				return usageError(fmt.Sprintf("--output %q is not a format", output), "Use one of: table, wide, json, yaml")
			}
			applyColorPolicy()
			name := contextFlag
			if name == "" && cmd.Flags().Lookup("context") == cmd.Root().PersistentFlags().Lookup("context") {
				name = os.Getenv("AIPLEX_CONTEXT")
			}
			if name != "" && cmd.Flags().Lookup("context") == cmd.Root().PersistentFlags().Lookup("context") {
				cfg, err := cliconfig.Load()
				if err != nil {
					return err
				}
				if _, ok := cfg.Contexts[name]; !ok {
					names := make([]string, 0, len(cfg.Contexts))
					for n := range cfg.Contexts {
						names = append(names, n)
					}
					hints := []string{"List contexts with: aiplex config show"}
					if near := nearest(name, names); len(near) > 0 {
						hints = append([]string{"Did you mean: " + strings.Join(near, ", ")}, hints...)
					}
					return newCLIError(exitUsage, fmt.Sprintf("context %q does not exist", name), hints...)
				}
				cliconfig.SetContextOverride(name)
			}
			return nil
		},
	}
	root.SetHelpTemplate(root.HelpTemplate() + "\n" + exitCodesHelp + "\n")
	root.CompletionOptions.HiddenDefaultCmd = true

	pf := root.PersistentFlags()
	pf.StringVar(&apiURL, "url", "", "AIPlex API URL (env: AIPLEX_URL)")
	pf.StringVar(&token, "token", "", "Bearer token (env: AIPLEX_TOKEN)")
	pf.StringVar(&contextFlag, "context", "", "Use this CLI context for one command (env: AIPLEX_CONTEXT)")
	pf.StringVarP(&output, "output", "o", "table", "Output format: table, wide (every column, untruncated), json or yaml")
	pf.BoolVar(&noColorFlag, "no-color", false, "Disable colour (also: NO_COLOR=1)")
	pf.BoolVar(&debug, "debug", false, "Show debug output, including raw API errors")
	root.SetFlagErrorFunc(func(c *cobra.Command, err error) error {
		return &cliError{msg: err.Error(), hints: usageHints(c), code: exitUsage, err: err}
	})

	root.AddGroup(commandGroups...)
	// Listed in this order within each help group (sorting is off).
	cobra.EnableCommandSorting = false
	root.AddCommand(
		devCmd(), loginCmd(), logoutCmd(), authCmd(), whoamiCmd(), configCmd(), ctxCmd(), doctorCmd(), healthCmd(),
		versionCmd(), initCmd(), quickstartCmd(),
		accessPointsCmd(), connectCmd(), whoCmd(),
		mcpCmd(), toolsCmd(), toolAuthCmd(),
		agentsCmd(), identityUserCmd(),
		usageCmd(), quotaCmd(),
		statusCmd(), resourcesCmd(), applyCmd(), validateCmd(), modulesCmd(), platformCmd(), upgradeCmd(),
		infrastructureCmd(), nativeCmd(), catalogCmd(), deployCmd(), undeployCmd(), listCmd(), getCmd(),
		logsCmd(), runsCmd(), consoleCmd(), tuiCmd(),
		completionCmd(),
	)
	registerModuleCommands(root)
	for _, c := range root.Commands() {
		if g, ok := groupOf[c.Name()]; ok {
			c.GroupID = g
		}
	}
	applyShortDescriptions(root)
	applyExamples(root)
	registerCompletions(root)
	confirmDestructive(root)
	rejectUnknownSubcommands(root)
	return root
}

func main() {
	root := newRootCmd()
	cmd, err := root.ExecuteC()
	if err != nil {
		var args []string
		if cmd != nil {
			args = cmd.Flags().Args()
		}
		os.Exit(reportError(os.Stderr, cmd, args, err))
	}
}

func exitErr(msg string, err error) {
	os.Exit(reportError(os.Stderr, nil, nil, fmt.Errorf("%s: %w", msg, err)))
}
