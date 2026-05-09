package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/vamsiramakrishnan/aiplex/internal/sandbox"
)

// snapshotCmd surfaces the on-disk snapshot store. Snapshots are
// content-addressed records of what a cap wrote to its workspace; they
// are how AIPlex makes audit verifiable rather than "trust the logs."
//
// See design/26-sandbox-and-snapshots.md.
func snapshotCmd() *cobra.Command {
	var dataDir string
	cmd := &cobra.Command{
		Use:     "snapshot",
		Short:   "Inspect filesystem snapshots produced by sandboxed cap invocations",
		Aliases: []string{"snap"},
	}
	cmd.PersistentFlags().StringVar(&dataDir, "data-dir", "", "Where snapshots live (default ~/.aiplex)")

	cmd.AddCommand(
		snapshotListCmd(&dataDir),
		snapshotShowCmd(&dataDir),
		snapshotDiffCmd(&dataDir),
		snapshotGCCmd(&dataDir),
	)
	return cmd
}

func snapshotStoreFromFlags(dataDir string) (*sandbox.SnapshotStore, error) {
	if dataDir == "" {
		home, _ := os.UserHomeDir()
		dataDir = filepath.Join(home, ".aiplex")
	}
	return sandbox.NewSnapshotStore(filepath.Join(dataDir, "snapshots"))
}

func snapshotListCmd(dataDir *string) *cobra.Command {
	var workspace string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List captured snapshots",
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := snapshotStoreFromFlags(*dataDir)
			if err != nil {
				return err
			}
			snaps := store.List(workspace)
			if output == "json" {
				printJSON(snaps)
				return nil
			}
			headers := []string{"ID", "WORKSPACE", "PARENT", "TAKEN", "CHANGES"}
			var rows [][]string
			for _, s := range snaps {
				changes := "—"
				if s.Diff != nil {
					changes = fmt.Sprintf("+%d ~%d -%d",
						len(s.Diff.Added), len(s.Diff.Modified), len(s.Diff.Deleted))
				}
				parent := s.ParentID
				if parent == "" {
					parent = "—"
				}
				rows = append(rows, []string{
					s.ID, s.WorkspaceID, parent, s.TakenAt.Format("2006-01-02 15:04:05"), changes,
				})
			}
			printTable(headers, rows)
			fmt.Printf("\n%d snapshot(s)\n", len(snaps))
			return nil
		},
	}
	cmd.Flags().StringVar(&workspace, "workspace", "", "Filter to one workspace ID")
	return cmd
}

func snapshotShowCmd(dataDir *string) *cobra.Command {
	return &cobra.Command{
		Use:   "show <snapshot-id>",
		Short: "Show a snapshot's full manifest",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := snapshotStoreFromFlags(*dataDir)
			if err != nil {
				return err
			}
			snap, err := store.Get(args[0])
			if err != nil {
				return err
			}
			if output == "json" {
				printJSON(snap)
				return nil
			}
			fmt.Printf("Snapshot: %s\n", snap.ID)
			fmt.Printf("  Workspace:    %s\n", snap.WorkspaceID)
			fmt.Printf("  Parent:       %s\n", snap.ParentID)
			fmt.Printf("  Hash:         %s\n", snap.Hash)
			fmt.Printf("  Taken at:     %s\n", snap.TakenAt.Format("2006-01-02 15:04:05"))
			fmt.Printf("  Storage path: %s\n", snap.StoragePath)
			fmt.Printf("  Files:        %d\n", len(snap.Files))
			if snap.Diff != nil {
				fmt.Println()
				fmt.Println("  Diff vs parent:")
				fmt.Print(indent(snap.Diff.Format(), "    "))
			}
			return nil
		},
	}
}

func snapshotDiffCmd(dataDir *string) *cobra.Command {
	return &cobra.Command{
		Use:   "diff <from-id> <to-id>",
		Short: "Show the file-level diff between two snapshots",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := snapshotStoreFromFlags(*dataDir)
			if err != nil {
				return err
			}
			diff, err := store.Diff(args[0], args[1])
			if err != nil {
				return err
			}
			if output == "json" {
				printJSON(diff)
				return nil
			}
			fmt.Printf("Diff %s → %s\n\n", args[0], args[1])
			fmt.Print(diff.Format())
			return nil
		},
	}
}

func snapshotGCCmd(dataDir *string) *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "gc",
		Short: "Garbage-collect orphaned snapshots",
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := snapshotStoreFromFlags(*dataDir)
			if err != nil {
				return err
			}
			if dryRun {
				before := len(store.List(""))
				fmt.Printf("Would consider %d snapshot(s) for GC.\n", before)
				return nil
			}
			n, err := store.GC(0)
			if err != nil {
				return err
			}
			fmt.Printf("Reclaimed %d orphaned snapshot(s).\n", n)
			return nil
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show how many snapshots are eligible without removing")
	return cmd
}

// indent prefixes every line in s with prefix.
func indent(s, prefix string) string {
	out := ""
	for _, line := range splitLines(s) {
		if line == "" {
			out += "\n"
			continue
		}
		out += prefix + line + "\n"
	}
	return out
}

func splitLines(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == '\n' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}
