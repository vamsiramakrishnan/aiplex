package main

import (
	"os"

	"github.com/spf13/cobra"
)

func completionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "completion <bash|zsh|fish|powershell>",
		Short: "Print a shell completion script (commands, flags and resource ids)",
		Long: `Print a shell completion script for aiplex. It completes commands and flags,
and the ids a command takes (access points, surfaces, connections, agents,
clients, runs, instances and more) by asking the current API: one GET, 1.5s
timeout, nothing offered when the API cannot be reached, refuses the request
or your stored login has expired.

Add to your shell profile:

  # Bash (~/.bashrc or ~/.bash_profile)
  echo 'source <(aiplex completion bash)' >> ~/.bashrc
  source ~/.bashrc

  # Zsh (~/.zshrc)
  echo 'source <(aiplex completion zsh)' >> ~/.zshrc
  source ~/.zshrc

  # Fish
  aiplex completion fish > ~/.config/fish/completions/aiplex.fish

  # PowerShell ($PROFILE)
  aiplex completion powershell | Out-String | Invoke-Expression`,
		Args:                  cobra.ExactArgs(1),
		DisableFlagsInUseLine: true,
		ValidArgs:             []string{"bash", "zsh", "fish", "powershell"},
		Example: `  source <(aiplex completion bash)
  aiplex completion zsh > "${fpath[1]}/_aiplex"
  aiplex completion fish > ~/.config/fish/completions/aiplex.fish`,
		RunE: func(cmd *cobra.Command, args []string) error {
			switch args[0] {
			case "bash":
				return cmd.Root().GenBashCompletionV2(os.Stdout, true)
			case "zsh":
				return cmd.Root().GenZshCompletion(os.Stdout)
			case "fish":
				return cmd.Root().GenFishCompletion(os.Stdout, true)
			case "powershell":
				return cmd.Root().GenPowerShellCompletionWithDesc(os.Stdout)
			default:
				return cmd.Help()
			}
		},
	}
	return cmd
}
