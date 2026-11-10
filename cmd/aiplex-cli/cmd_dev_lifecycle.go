package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/vamsiramakrishnan/aiplex/internal/cliconfig"
)

// devDetachedEnv marks the background `aiplex dev` started by --detach.
const devDetachedEnv = "AIPLEX_DEV_DETACHED"

// publicProviderOrigins are always allowed for the local API so a model
// surface for a real provider works without extra flags.
var publicProviderOrigins = []string{"https://api.openai.com", "https://api.anthropic.com", "https://generativelanguage.googleapis.com"}

// devAllowedOrigins normalises --allow-origin values to scheme://host[:port].
func devAllowedOrigins(values []string) ([]string, error) {
	out := append([]string{}, publicProviderOrigins...)
	for _, v := range values {
		u, err := url.Parse(strings.TrimSpace(v))
		if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
			return nil, usageError(fmt.Sprintf("--allow-origin %q is not an origin", v), "Write it as https://host or http://127.0.0.1:port")
		}
		origin := u.Scheme + "://" + u.Host
		dup := false
		for _, o := range out {
			dup = dup || o == origin
		}
		if !dup {
			out = append(out, origin)
		}
	}
	return out, nil
}

// readDevState returns the recorded state of a running `aiplex dev`, or nil.
func readDevState() (*devState, string, error) {
	path, err := devStatePath()
	if err != nil {
		return nil, "", err
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, path, nil
	}
	if err != nil {
		return nil, path, err
	}
	var st devState
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, path, fmt.Errorf("read %s: %w", path, err)
	}
	return &st, path, nil
}

// liveDevState is the state of an `aiplex dev` that is still running; a
// state file left behind by a crashed one is removed.
func liveDevState() (*devState, error) {
	st, path, err := readDevState()
	if err != nil || st == nil {
		return nil, err
	}
	if !processAlive(st.PID) {
		_ = os.Remove(path)
		return nil, nil
	}
	return st, nil
}

func refuseIfDevRunning() error {
	st, err := liveDevState()
	if err != nil || st == nil {
		return nil
	}
	return newCLIError(exitConflict, fmt.Sprintf("aiplex dev is already running (pid %d) at %s", st.PID, st.URL),
		"Use it: eval \"$(aiplex dev env)\"", "Or stop it first: aiplex dev stop")
}

// runDevDetached starts `aiplex dev` again in the background (its own
// session, output to ~/.aiplex/dev.log) and waits until it is ready.
func runDevDetached(cmd *cobra.Command, o devOptions) error {
	dir, err := cliconfig.Dir()
	if err != nil {
		return err
	}
	logPath := filepath.Join(dir, "dev.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer logFile.Close()
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	var args []string
	for _, a := range os.Args[1:] {
		if a == "--detach" || a == "-d" || a == "--detach=true" {
			continue
		}
		args = append(args, a)
	}
	child := exec.Command(exe, args...)
	child.Env = append(os.Environ(), devDetachedEnv+"=1")
	child.Stdout, child.Stderr, child.Stdin = logFile, logFile, nil
	detachProcess(child)
	if err := child.Start(); err != nil {
		return fmt.Errorf("start aiplex dev in the background: %w", err)
	}
	exited := make(chan error, 1)
	go func() { exited <- child.Wait() }()
	deadline := time.After(90 * time.Second)
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case err := <-exited:
			tail, _ := os.ReadFile(logPath)
			return newCLIError(exitError, fmt.Sprintf("aiplex dev exited during startup (%v)", err), strings.TrimSpace(string(tail)), "Full output: "+logPath)
		case <-deadline:
			return newCLIError(exitError, "aiplex dev did not become ready within 90s", "Its output: "+logPath, "Stop it with: aiplex dev stop")
		case <-tick.C:
			st, _, _ := readDevState()
			if st != nil && st.PID == child.Process.Pid && devReachable(st.URL) {
				out, _ := os.ReadFile(logPath)
				fmt.Fprint(cmd.OutOrStdout(), string(out))
				fmt.Fprintf(cmd.OutOrStdout(), "  Background pid %d; its output goes to %s\n", st.PID, logPath)
				return nil
			}
		}
	}
}

func devStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show whether aiplex dev is running, where, and the Console URL",
		Long:  "Prints the running local AIPlex: API URL, pid, uptime, access point, model and Console. Exits 1 when none is running.",
		Example: `  aiplex dev status
  aiplex dev status -o json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := liveDevState()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if st == nil {
				if machineOutput() {
					spStructured(cmd, map[string]any{"running": false})
				}
				return newCLIError(exitError, "aiplex dev is not running", "Start it with: aiplex dev   (or in the background: aiplex dev --detach)")
			}
			healthy := devReachable(st.URL)
			if machineOutput() {
				spStructured(cmd, map[string]any{"running": true, "healthy": healthy, "state": st})
				return nil
			}
			uptime := ""
			if t, err := time.Parse(time.RFC3339, st.StartedAt); err == nil {
				uptime = " (up " + strings.TrimSuffix(relTimeFrom(t, time.Now()), " ago") + ")"
			}
			fmt.Fprintf(out, "aiplex dev is running, pid %d%s\n", st.PID, uptime)
			health := "answering"
			if !healthy {
				health = "NOT answering (see " + st.APILog + ")"
			}
			fmt.Fprintf(out, "  API            %s  %s\n", st.URL, health)
			fmt.Fprintf(out, "  Access point   %s  ->  %s/ap/%s\n", st.AccessPoint, st.URL, st.AccessPoint)
			if len(st.Models) > 1 {
				fmt.Fprintf(out, "  Models         %s\n", strings.Join(st.Models, ", "))
			} else {
				fmt.Fprintf(out, "  Model          %s\n", st.Model)
			}
			fmt.Fprintf(out, "  CLI context    %s\n", st.Context)
			switch {
			case st.Console != "":
				fmt.Fprintf(out, "  Console        %s\n", st.Console)
			case st.ConsoleNote != "":
				fmt.Fprintf(out, "  Console        %s\n", st.ConsoleNote)
			default:
				fmt.Fprintln(out, "  Console        off (--no-console)")
			}
			if st.APILog != "" {
				fmt.Fprintf(out, "  API log        %s\n", st.APILog)
			}
			if !healthy {
				return newCLIError(exitError, "the local API is not answering", "Restart it: aiplex dev stop && aiplex dev --detach")
			}
			return nil
		},
	}
}

func devStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "stop",
		Short:   "Stop the running aiplex dev (and its local API)",
		Example: "  aiplex dev stop",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, path, err := readDevState()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if st == nil {
				fmt.Fprintln(out, "aiplex dev is not running.")
				return nil
			}
			if !processAlive(st.PID) {
				_ = os.Remove(path)
				fmt.Fprintf(out, "aiplex dev is not running (removed the state left by pid %d).\n", st.PID)
				return nil
			}
			if err := terminateProcess(st.PID); err != nil {
				return fmt.Errorf("stop aiplex dev (pid %d): %w", st.PID, err)
			}
			deadline := time.Now().Add(15 * time.Second)
			for processAlive(st.PID) && time.Now().Before(deadline) {
				time.Sleep(100 * time.Millisecond)
			}
			if processAlive(st.PID) {
				_ = killProcess(st.PID)
				time.Sleep(200 * time.Millisecond)
				_ = os.Remove(path)
				fmt.Fprintf(out, "Killed aiplex dev (pid %d) after it did not stop within 15s; its local API exits on its own.\n", st.PID)
				return nil
			}
			fmt.Fprintf(out, "Stopped aiplex dev (pid %d).\n", st.PID)
			return nil
		},
	}
}
