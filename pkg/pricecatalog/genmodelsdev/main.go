// Command genmodelsdev writes pkg/pricecatalog/snapshot_modelsdev.json, a
// trimmed snapshot of the models.dev model database (github.com/sst/models.dev)
// at one pinned commit: text token prices for the providers AIPlex matches.
//
// Usage (from pkg/pricecatalog, or through make update-price-catalog):
//
//	go run ./genmodelsdev -commit <sha>     pin a new upstream commit
//	go run ./genmodelsdev -same             regenerate from the recorded commit
//	                                        (go generate); the output must not change
//
// The commit is fetched with git, or read from -input (a checkout of that
// commit) for an offline run. The LICENSE must equal the
// checked-in LICENSE.modelsdev, which records that the data is under the MIT
// licence. A different licence text stops the run for a human to review.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/pricecatalog"
)

const repo = "sst/models.dev"

var shaRE = regexp.MustCompile(`^[0-9a-f]{40}$`)

// fetchCommit fetches exactly the pinned commit with git into a temporary
// directory (a shallow fetch of one commit by its full hash) and reads it.
func fetchCommit(commit string) (map[string][]byte, error) {
	dir, err := os.MkdirTemp("", "models-dev-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	for _, args := range [][]string{
		{"init", "-q", "."},
		{"fetch", "-q", "--depth", "1", "https://github.com/" + repo, commit},
		{"checkout", "-q", "FETCH_HEAD"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			return nil, fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(string(out)))
		}
	}
	return readCheckout(dir)
}

func readCheckout(dir string) (map[string][]byte, error) {
	files := map[string][]byte{}
	license, err := os.ReadFile(filepath.Join(dir, "LICENSE"))
	if err != nil {
		return nil, err
	}
	files["LICENSE"] = license
	root := filepath.Join(dir, "providers")
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".toml") {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = raw
		return nil
	})
	return files, err
}

func run() error {
	commit := flag.String("commit", "", "40-hex upstream commit to pin")
	same := flag.Bool("same", false, "regenerate from the commit recorded in snapshot_modelsdev.json")
	input := flag.String("input", "", "read a checkout of the commit from this directory instead of fetching it")
	retrieved := flag.String("retrieved", "", "retrieval date YYYY-MM-DD (default: today, or the recorded date with -same)")
	dir := flag.String("dir", ".", "package directory to write into")
	flag.Parse()

	existing := pricecatalog.Meta{}
	if raw, err := os.ReadFile(filepath.Join(*dir, "snapshot_modelsdev.json")); err == nil {
		var s pricecatalog.Snapshot
		if json.Unmarshal(raw, &s) == nil {
			existing = s.Meta
		}
	}
	if *same {
		*commit = existing.Commit
		if *retrieved == "" {
			*retrieved = existing.RetrievedAt
		}
	}
	if !shaRE.MatchString(*commit) {
		return fmt.Errorf("a full 40-hex -commit is required (a branch name is not a pin)")
	}
	if *retrieved == "" {
		*retrieved = time.Now().UTC().Format("2006-01-02")
	}
	if _, err := time.Parse("2006-01-02", *retrieved); err != nil {
		return fmt.Errorf("-retrieved must be YYYY-MM-DD")
	}
	var files map[string][]byte
	var err error
	if *input != "" {
		files, err = readCheckout(*input)
	} else {
		files, err = fetchCommit(*commit)
	}
	if err != nil {
		return fmt.Errorf("upstream data: %w", err)
	}
	pinned, err := os.ReadFile(filepath.Join(*dir, "LICENSE.modelsdev"))
	if err != nil {
		return err
	}
	if !bytes.Equal(bytes.TrimSpace(files["LICENSE"]), bytes.TrimSpace(pinned)) {
		return fmt.Errorf("upstream LICENSE at %s differs from LICENSE.modelsdev: review the licence (MIT) before updating that file", *commit)
	}
	if !bytes.HasPrefix(bytes.TrimSpace(pinned), []byte("MIT License")) {
		return fmt.Errorf("LICENSE.modelsdev does not read as the MIT licence")
	}
	delete(files, "LICENSE")
	meta := pricecatalog.Meta{
		Catalog:        pricecatalog.SourceModelsDev,
		Commit:         *commit,
		RetrievedAt:    *retrieved,
		SourceURL:      "https://github.com/" + repo + "/tree/" + *commit + "/providers",
		License:        "MIT (Copyright (c) 2025 models.dev); see NOTICE.modelsdev",
		UpstreamSHA256: inputDigest(files),
		Generator:      "pkg/pricecatalog/genmodelsdev",
	}
	snap, err := build(files, meta)
	if err != nil {
		return err
	}
	out, err := snap.Marshal()
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(*dir, "snapshot_modelsdev.json"), out, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(*dir, "snapshot_modelsdev.sha256"), []byte(sha256Hex(out)+"\n"), 0o644); err != nil {
		return err
	}
	for _, s := range skipped {
		fmt.Printf("left out (files state two different prices): %s\n", s)
	}
	fmt.Printf("wrote %d entries, %d bytes, commit %s, retrieved %s\n", len(snap.Entries), len(out), *commit, *retrieved)
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "genmodelsdev:", strings.TrimSpace(err.Error()))
		os.Exit(1)
	}
}
