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

Add to your shell profile:

  # Bash (~/.bashrc or ~/.bash_profile)
  echo 'source <(aiplex completion bash)' >> ~/.bashrc
  source ~/.bashrc

  # Zsh (~/.zshrc)
  echo 'source <(aiplex completion zsh)' >> ~/.zshrc
  source ~/.zshrc

  # Fish (~/.config/fish/config.fish)
  aiplex completion fish | source

  # PowerShell ($PROFILE)
  aiplex completion powershell | Out-String | Invoke-Expression`,
		Args:                  cobra.ExactArgs(1),
		DisableFlagsInUseLine: true,
		ValidArgs:             []string{"bash", "zsh", "fish", "powershell"},
		Example: `  # Generate bash completions and source them
  source <(aiplex completion bash)

  # Generate zsh completions persistently
  aiplex completion zsh > ~/.zsh/completions/_aiplex

  # Generate fish completions persistently
  aiplex completion fish > ~/.config/fish/completions/aiplex.fish`,
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
			default:
				return cmd.Help()
			}
		},
	}
	return cmd
}
