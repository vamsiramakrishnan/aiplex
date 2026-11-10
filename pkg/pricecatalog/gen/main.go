// Command gen writes pkg/pricecatalog/snapshot.json, a trimmed snapshot of
// LiteLLM's model_prices_and_context_window.json at one pinned commit.
//
// Usage (from pkg/pricecatalog, or through make update-price-catalog):
//
//	go run ./gen -commit <sha>     pin a new upstream commit
//	go run ./gen -same             regenerate from the commit already recorded
//	                               (go generate); the output must not change
//
// The upstream file and licence are fetched from raw.githubusercontent.com at
// the commit, or read from -input and -license for an offline run. The licence
// text must equal the checked-in LICENSE.litellm, which records that the file
// is under the MIT licence and outside enterprise/. A different licence text
// stops the run for a human to review.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/pricecatalog"
)

const (
	repo     = "BerriAI/litellm"
	dataFile = "model_prices_and_context_window.json"
)

var shaRE = regexp.MustCompile(`^[0-9a-f]{40}$`)

func fetch(url string) ([]byte, error) {
	client := &http.Client{Timeout: 2 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 64<<20))
}

func run() error {
	commit := flag.String("commit", "", "40-hex upstream commit to pin")
	same := flag.Bool("same", false, "regenerate from the commit recorded in snapshot.json")
	input := flag.String("input", "", "read the upstream file from this path instead of fetching it")
	licensePath := flag.String("license", "", "read the upstream LICENSE from this path instead of fetching it")
	retrieved := flag.String("retrieved", "", "retrieval date YYYY-MM-DD (default: today, or the recorded date with -same)")
	dir := flag.String("dir", ".", "package directory to write into")
	flag.Parse()

	existing := pricecatalog.Meta{}
	if raw, err := os.ReadFile(filepath.Join(*dir, "snapshot.json")); err == nil {
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
	base := "https://raw.githubusercontent.com/" + repo + "/" + *commit + "/"

	license, err := read(*licensePath, base+"LICENSE")
	if err != nil {
		return fmt.Errorf("licence: %w", err)
	}
	pinned, err := os.ReadFile(filepath.Join(*dir, "LICENSE.litellm"))
	if err != nil {
		return err
	}
	if !bytes.Equal(bytes.TrimSpace(license), bytes.TrimSpace(pinned)) {
		return fmt.Errorf("upstream LICENSE at %s differs from LICENSE.litellm: review the licence (MIT, nothing under enterprise/) before updating that file", *commit)
	}
	if !bytes.Contains(license, []byte("MIT License")) || !bytes.Contains(license, []byte(`"enterprise/" directory`)) {
		return fmt.Errorf("LICENSE.litellm does not read as MIT with an enterprise/ exclusion")
	}
	upstream, err := read(*input, base+dataFile)
	if err != nil {
		return fmt.Errorf("upstream data: %w", err)
	}
	meta := pricecatalog.Meta{
		Catalog:        "litellm",
		Commit:         *commit,
		RetrievedAt:    *retrieved,
		SourceURL:      base + dataFile,
		License:        "MIT (Copyright (c) 2023 Berri AI); see NOTICE",
		UpstreamSHA256: sha256Hex(upstream),
		Generator:      "pkg/pricecatalog/gen",
	}
	snap, err := build(upstream, meta)
	if err != nil {
		return err
	}
	out, err := snap.Marshal()
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(*dir, "snapshot.json"), out, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(*dir, "snapshot.sha256"), []byte(sha256Hex(out)+"\n"), 0o644); err != nil {
		return err
	}
	for _, s := range skipped {
		fmt.Printf("left out (upstream states two different prices): %s\n", s)
	}
	fmt.Printf("wrote %d entries, %d bytes, commit %s, retrieved %s\n", len(snap.Entries), len(out), *commit, *retrieved)
	return nil
}

func read(path, url string) ([]byte, error) {
	if path != "" {
		return os.ReadFile(path)
	}
	return fetch(url)
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "gen:", strings.TrimSpace(err.Error()))
		os.Exit(1)
	}
}
