package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// addYesFlag registers the --yes/-y flag shared by the commands that ask for
// confirmation before a destructive or bulk change.
func addYesFlag(cmd *cobra.Command, yes *bool) {
	cmd.Flags().BoolVarP(yes, "yes", "y", false, "Do not ask for confirmation (required when there is no terminal)")
}

// confirm guards a destructive change. On a terminal (stdin, and stderr where
// the question is written) it asks; --yes skips the question. With no
// terminal there is nobody to ask, so the command refuses (exit 2) unless
// --yes was given: a script that deletes something must say so.
func confirm(cmd *cobra.Command, yes bool, question string) error {
	return confirmWith(cmd, yes, question, "this is destructive", "cancelled; nothing was deleted")
}

// confirmWith is confirm with its own wording: what is risky about the
// action (for the no-terminal refusal) and what to say when declined.
func confirmWith(cmd *cobra.Command, yes bool, question, risk, cancelled string) error {
	if yes {
		return nil
	}
	if !stdinIsTerminal() || !stderrIsTerminal() {
		return usageError("refusing to proceed: "+risk+" and there is no terminal to ask for confirmation. "+question,
			"Add --yes (-y) to run it non-interactively")
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "%s [y/N] ", question)
	answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		return nil
	}
	return newCLIError(exitError, cancelled)
}
