package main

import (
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/vamsiramakrishnan/aiplex/internal/cliconfig"
	"github.com/vamsiramakrishnan/aiplex/internal/harness"
)

// connectWriteFlags are the flags of `aiplex connect` that write or undo
// client configuration.
type connectWriteFlags struct {
	write, dryRun, yes, undo, tokenHelper bool
	scope, model, telemetry               string
}

func (f *connectWriteFlags) bind(cmd *cobra.Command) {
	fl := cmd.Flags()
	fl.BoolVar(&f.write, "write", false, "Merge the configuration into the client's own config file (shows a diff, backs up first)")
	fl.BoolVar(&f.dryRun, "dry-run", false, "With --write or --undo: show the diff and write nothing")
	fl.BoolVarP(&f.yes, "yes", "y", false, "Do not ask for confirmation (required without a terminal)")
	fl.BoolVar(&f.undo, "undo", false, "Restore the most recent backup made by --write (for --client, or the latest of any)")
	fl.StringVar(&f.scope, "scope", "user", "With --write: user (home directory files) or project (this directory's files; claude-code and gemini-cli)")
	fl.StringVar(&f.model, "model", "", "With --write: model to select, instead of the first one the access point offers on the client's API")
	fl.BoolVar(&f.tokenHelper, "token-helper", false, "With --write: let the client fetch its token from \"aiplex --context NAME auth print-token\" for the context in effect (needs a stored token; claude-code, codex)")
	fl.StringVar(&f.telemetry, "with-telemetry", "", "With --write: also point the client's own OpenTelemetry export at this OTLP endpoint (prompts stay redacted)")
}

// rejectWriteOnly refuses write-only flags used without --write.
func (f *connectWriteFlags) rejectWriteOnly(cmd *cobra.Command) error {
	for _, name := range []string{"scope", "model", "token-helper", "with-telemetry", "yes"} {
		if cmd.Flags().Changed(name) {
			return usageError("--"+name+" only applies with --write or --undo", "Add --write, or drop the flag")
		}
	}
	return nil
}

func connectWriteHint(ap, client string) string {
	if client != "generic" && !slices.Contains(harness.Writable, client) {
		return ""
	}
	if client == "generic" {
		client = "claude-code"
	}
	return fmt.Sprintf("To write this into the client's own config (diff first, backup, undo with --undo): aiplex connect %s --client %s --write", ap, client)
}

func connectBackupRoot() (string, string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", "", fmt.Errorf("cannot find your home directory: %v", err)
	}
	return home, harness.BackupRoot(home), nil
}

func printChanges(out io.Writer, changes []harness.Change) (changed int) {
	for _, c := range changes {
		d := c.Diff()
		if d == "" {
			fmt.Fprintf(out, "%s: already up to date\n", c.Path)
			continue
		}
		changed++
		state := "update"
		switch {
		case c.Delete:
			state = "remove"
		case !c.Existed:
			state = "create"
		}
		fmt.Fprintf(out, "%s (%s)\n%s\n", c.Path, state, d)
	}
	return changed
}

func runConnectWrite(cmd *cobra.Command, ap, client string, f *connectWriteFlags) error {
	if machineOutput() {
		return usageError("--write prints a diff; -o json and -o yaml are not supported with it")
	}
	if !slices.Contains(harness.Writable, client) {
		why := "choose one of: " + strings.Join(harness.Writable, ", ")
		if client == "antigravity" || client == "antigravity-acp" {
			why = "Antigravity's config file location is not documented, so it is print-only: run `aiplex connect " + ap + " --client " + client + "` and apply the fragment by hand"
		}
		return usageError(fmt.Sprintf("--write does not support --client %q", client), why)
	}
	home, root, err := connectBackupRoot()
	if err != nil {
		return err
	}
	cwd, _ := os.Getwd()
	apiClient := client
	if client == "gemini-cli" {
		apiClient = "generic"
	}
	c, err := newClient().ConnectAccessPoint(cmd.Context(), ap, apiClient)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	opts := harness.Options{Home: home, Cwd: cwd, Scope: f.scope, Model: f.model, TokenHelper: f.tokenHelper, Telemetry: f.telemetry}
	if f.tokenHelper {
		name, err := tokenHelperContext()
		if err != nil {
			return err
		}
		opts.HelperContext = name
	}
	var plan *harness.Plan
	switch client {
	case "claude-code":
		plan, err = harness.PlanClaudeCode(c, opts)
	case "codex":
		plan, err = harness.PlanCodex(c, opts)
	case "gemini-cli":
		plan, err = harness.PlanGeminiCLI(c, opts)
	}
	if err != nil {
		return newCLIError(exitError, "connect --write: "+err.Error())
	}
	out := cmd.OutOrStdout()
	changed := printChanges(out, plan.Changes)
	if changed > 0 {
		if f.dryRun {
			fmt.Fprintln(out, "Dry run: nothing was written.")
		} else {
			q := fmt.Sprintf("Write %d file(s) for %s? The originals are backed up under %s.", changed, client, root)
			if err := confirmWith(cmd, f.yes, q, "this edits your client's configuration files", "cancelled; nothing was written"); err != nil {
				return err
			}
			dir, err := harness.Apply(root, client, ap, plan.Changes, time.Now())
			if err != nil {
				return fmt.Errorf("connect --write: %w", err)
			}
			fmt.Fprintf(out, "Wrote %d file(s). Backup and manifest: %s\nUndo: aiplex connect --undo --client %s\n", changed, dir, client)
		}
	} else {
		fmt.Fprintln(out, "Nothing to change.")
	}
	for _, n := range plan.Notes {
		fmt.Fprintf(out, "Note: %s\n", n)
	}
	if len(plan.Remaining) > 0 {
		fmt.Fprintln(out, "\nStill to do by hand:")
		for _, r := range plan.Remaining {
			fmt.Fprintf(out, "  - %s\n", r)
		}
	}
	return nil
}

// tokenHelperContext names the context the credential helper must use. The
// helper runs later, from the client, so it cannot see this command's flags or
// environment: it is told the context explicitly. That only works when the
// context has a stored token.
func tokenHelperContext() (string, error) {
	t := resolveTarget()
	hint := "Create and sign in to a context first: aiplex config set-context NAME --url URL, then aiplex login --context NAME. Or drop --token-helper and export the token in the shell that starts the client."
	if t.Context == "" {
		return "", usageError("--token-helper needs a stored context, and none is selected; your credentials come only from the environment or flags", hint)
	}
	creds, err := cliconfig.LoadCredentials()
	if err != nil || creds.GetToken(t.Context) == nil || creds.GetToken(t.Context).AccessToken == "" {
		return "", usageError(fmt.Sprintf("--token-helper needs a stored token for context %q, and there is none", t.Context), hint)
	}
	return t.Context, nil
}

func runConnectUndo(cmd *cobra.Command, client string, f *connectWriteFlags) error {
	if f.write {
		return usageError("--undo and --write cannot be combined")
	}
	_, root, err := connectBackupRoot()
	if err != nil {
		return err
	}
	if !cmd.Flags().Changed("client") {
		client = ""
	}
	dir, m, err := harness.LatestBackup(root, client)
	if err != nil {
		return newCLIError(exitNotFound, err.Error())
	}
	changes, err := harness.PlanUndo(dir, m)
	if err != nil {
		return newCLIError(exitError, "connect --undo: "+err.Error())
	}
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Restoring the backup of %s made %s (%s)\n\n", m.Client, m.CreatedAt, dir)
	changed := printChanges(out, changes)
	if changed == 0 {
		fmt.Fprintln(out, "The files already match the backup; marking it undone.")
	}
	if f.dryRun {
		fmt.Fprintln(out, "Dry run: nothing was changed.")
		return nil
	}
	if changed > 0 {
		q := fmt.Sprintf("Restore %d file(s)? Changes made to them since the backup are lost.", changed)
		if err := confirmWith(cmd, f.yes, q, "this overwrites your client's configuration files", "cancelled; nothing was restored"); err != nil {
			return err
		}
	}
	if err := harness.ApplyUndo(dir, m, changes); err != nil {
		return fmt.Errorf("connect --undo: %w", err)
	}
	fmt.Fprintf(out, "Restored %d file(s).\n", changed)
	return nil
}
