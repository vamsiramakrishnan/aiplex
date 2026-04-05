package main

import (
	"os"

	"github.com/spf13/cobra"
)

func completionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "completion [bash|zsh|fish|powershell]",
		Short: "Generate shell completion scripts",
		Long: `Generate shell completion scripts for aiplex.

To load completions:

Bash:
  $ source <(aiplex completion bash)
  # To load completions for each session, execute once:
  $ aiplex completion bash > /etc/bash_completion.d/aiplex

Zsh:
  $ source <(aiplex completion zsh)
  # To load completions for each session, execute once:
  $ aiplex completion zsh > "${fpath[1]}/_aiplex"

Fish:
  $ aiplex completion fish | source
  # To load completions for each session, execute once:
  $ aiplex completion fish > ~/.config/fish/completions/aiplex.fish

PowerShell:
  PS> aiplex completion powershell | Out-String | Invoke-Expression
`,
		DisableFlagsInUseLine: true,
		ValidArgs:             []string{"bash", "zsh", "fish", "powershell"},
		Args:                  cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch args[0] {
			case "bash":
				return cmd.Root().GenBashCompletion(os.Stdout)
			case "zsh":
				return cmd.Root().GenZshCompletion(os.Stdout)
			case "fish":
				return cmd.Root().GenFishCompletion(os.Stdout, true)
			case "powershell":
				return cmd.Root().GenPowerShellCompletionWithDesc(os.Stdout)
			}
			return nil
		},
	}
	return cmd
}
