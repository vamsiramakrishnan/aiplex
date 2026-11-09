package deployment

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// These exact root record exclusions match the pinned Anvil contract revision.
// Unknown *.report.json files remain content, rather than being silently ignored.
var derived = map[string]bool{"certification.json": true, "publication.json": true, "selftest.report.json": true, "conformance.report.json": true, "conformance.live.report.json": true, "simulation.report.json": true, "review.report.json": true, "observe.report.json": true, "traffic.report.json": true, "benchmark.report.json": true, "evals.report.json": true}

const maxBundleBytes = 64 << 20
const maxBundleFiles = 4096

func digest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

// BundleDigest implements canonical Anvil bundleHash for UTF-8 bundles. Binary
// inputs are refused rather than applying a different Node UTF-8 replacement rule.
func BundleDigest(directory string) (string, error) {
	files, e := readBundle(directory)
	if e != nil {
		return "", e
	}
	return hashFiles(files, true), nil
}
func readBundle(root string) (map[string][]byte, error) {
	info, e := os.Lstat(root)
	if e != nil {
		return nil, e
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("bundle must be a regular directory")
	}
	directory, e := os.OpenRoot(root)
	if e != nil {
		return nil, e
	}
	defer directory.Close()
	files := map[string][]byte{}
	total := 0
	e = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		rel, e := filepath.Rel(root, path)
		if e != nil {
			return e
		}
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(rel, ".aiplex-") {
			return fmt.Errorf("reserved bridge artifact path")
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("bundle symlinks prohibited: %s", rel)
		}
		if entry.IsDir() {
			if entry.Name() == "node_modules" || rel == ".anvil" {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("bundle special files prohibited: %s", rel)
		}
		if len(files) >= maxBundleFiles {
			return fmt.Errorf("bundle file limit exceeded")
		}
		info, e := entry.Info()
		if e != nil {
			return e
		}
		if info.Size() > maxBundleBytes || int64(total)+info.Size() > maxBundleBytes {
			return fmt.Errorf("bundle size bound exceeded")
		}
		file, e := directory.Open(filepath.FromSlash(rel))
		if e != nil {
			return e
		}
		data, e := io.ReadAll(io.LimitReader(file, int64(maxBundleBytes-total)+1))
		_ = file.Close()
		if e != nil {
			return e
		}
		if !utf8.Valid(data) {
			return fmt.Errorf("bundle file is not UTF-8: %s", rel)
		}
		total += len(data)
		if total > maxBundleBytes {
			return fmt.Errorf("bundle size bound exceeded")
		}
		files[rel] = data
		return nil
	})
	if e != nil {
		return nil, e
	}
	if files["air.yaml"] == nil && files["air.json"] == nil {
		return nil, fmt.Errorf("bundle has no canonical AIR")
	}
	return files, nil
}
func hashFiles(files map[string][]byte, excludeDerived bool) string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		return slices.Compare(utf16.Encode([]rune(names[i])), utf16.Encode([]rune(names[j]))) < 0
	})
	h := sha256.New()
	for _, name := range names {
		if excludeDerived && derived[name] {
			continue
		}
		_, _ = fmt.Fprintf(h, "%s\x00%s\x00", name, digest(files[name]))
	}
	return hex.EncodeToString(h.Sum(nil))
}
func snapshot(bundle Bundle) (root, canonical, full string, err error) {
	files, e := readBundle(bundle.Directory)
	if e != nil {
		return "", "", "", e
	}
	canonical = hashFiles(files, true)
	if canonical != bundle.Digest {
		return "", "", "", fmt.Errorf("reviewed Anvil bundle digest changed")
	}
	full = hashFiles(files, false)
	root, e = os.MkdirTemp("", "aiplex-anvil-bundle-")
	if e != nil {
		return "", "", "", e
	}
	privateRoot := root
	defer func() {
		if err != nil {
			_ = os.RemoveAll(privateRoot)
		}
	}()
	for name, data := range files {
		clean := filepath.Clean(filepath.FromSlash(name))
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return "", "", "", fmt.Errorf("bundle path escape")
		}
		dest := filepath.Join(root, clean)
		if e = os.MkdirAll(filepath.Dir(dest), 0700); e != nil {
			return "", "", "", e
		}
		if e = os.WriteFile(dest, data, 0600); e != nil {
			return "", "", "", e
		}
	}
	copied, e := readBundle(root)
	if e != nil || hashFiles(copied, true) != canonical || hashFiles(copied, false) != full {
		return "", "", "", fmt.Errorf("private bundle snapshot verification failed")
	}
	return root, canonical, full, nil
}
