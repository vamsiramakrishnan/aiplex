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
and the ids of access points, model surfaces, tool connections, prices and
identity providers by asking the current API (one request, 1.5s timeout,
silent when the API is not reachable).

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
