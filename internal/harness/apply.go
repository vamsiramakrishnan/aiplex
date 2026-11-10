// Package harness writes AIPlex connection settings into the configuration
// files of coding-agent harnesses (Claude Code, Codex, Gemini CLI) on request.
//
// Every write is a merge into the user's existing file, shown as a diff
// first, backed up under ~/.aiplex/connect-backups with a manifest, and
// reversible. A token value is never written anywhere: files reference the
// AIPLEX_TOKEN environment variable or a credential helper command.
package harness

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Change is one file edit.
type Change struct {
	Path    string // absolute
	Before  []byte
	Existed bool
	After   []byte // ignored when Delete is set
	Delete  bool   // the file is removed (undo of a file that did not exist)
	Mode    os.FileMode
}

// Changed reports whether applying the change would alter the file system.
func (c Change) Changed() bool {
	if c.Delete {
		return c.Existed
	}
	return !c.Existed || string(c.Before) != string(c.After)
}

// readFile reads a config file. A missing file is not an error.
func readFile(path string) (data []byte, existed bool, mode os.FileMode, err error) {
	real := resolveLink(path)
	st, err := os.Stat(real)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, 0o600, nil
	}
	if err != nil {
		return nil, false, 0, err
	}
	if st.IsDir() {
		return nil, false, 0, fmt.Errorf("%s is a directory", path)
	}
	data, err = os.ReadFile(real)
	if err != nil {
		return nil, false, 0, err
	}
	return data, true, st.Mode().Perm(), nil
}

// resolveLink follows a symlink (dotfile managers link config files) so the
// target is edited in place and the link survives.
func resolveLink(path string) string {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return real
	}
	return path
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	real := resolveLink(path)
	dir := filepath.Dir(real)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".aiplex-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	cleanup := func() { _ = os.Remove(name) }
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(name, real); err != nil {
		cleanup()
		return err
	}
	return nil
}

// Manifest records one backup set. It holds paths and hashes only.
type Manifest struct {
	Version     int            `json:"version"`
	Client      string         `json:"client"`
	AccessPoint string         `json:"access_point,omitempty"`
	CreatedAt   string         `json:"created_at"`
	Undone      bool           `json:"undone,omitempty"`
	Files       []ManifestFile `json:"files"`
}

// ManifestFile is one file in a backup set.
type ManifestFile struct {
	Path         string `json:"path"`
	Existed      bool   `json:"existed"`
	Backup       string `json:"backup,omitempty"` // relative to the backup directory
	Mode         uint32 `json:"mode"`
	SHA256Before string `json:"sha256_before,omitempty"`
	SHA256After  string `json:"sha256_after,omitempty"`
}

// BackupRoot is where backup sets live for a given home directory.
func BackupRoot(home string) string {
	return filepath.Join(home, ".aiplex", "connect-backups")
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// Apply backs up and then writes every changed file. It returns the backup
// directory, or "" when nothing changed.
func Apply(root, client, accessPoint string, changes []Change, now time.Time) (string, error) {
	var todo []Change
	for _, c := range changes {
		if c.Changed() {
			todo = append(todo, c)
		}
	}
	if len(todo) == 0 {
		return "", nil
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	dir, err := newBackupDir(root, client, now)
	if err != nil {
		return "", err
	}
	m := Manifest{Version: 1, Client: client, AccessPoint: accessPoint, CreatedAt: now.UTC().Format(time.RFC3339)}
	for i, c := range todo {
		f := ManifestFile{Path: c.Path, Existed: c.Existed, Mode: uint32(c.Mode), SHA256After: sum(c.After)}
		if c.Existed {
			f.SHA256Before = sum(c.Before)
			f.Backup = fmt.Sprintf("files/%d.bak", i)
			if err := writeAtomic(filepath.Join(dir, f.Backup), c.Before, 0o600); err != nil {
				return dir, fmt.Errorf("back up %s: %w", c.Path, err)
			}
		}
		m.Files = append(m.Files, f)
	}
	if err := writeManifest(dir, &m); err != nil {
		return dir, err
	}
	for _, c := range todo {
		if err := writeAtomic(c.Path, c.After, c.Mode); err != nil {
			return dir, fmt.Errorf("write %s: %w (originals are in %s)", c.Path, err, dir)
		}
	}
	return dir, nil
}

func newBackupDir(root, client string, now time.Time) (string, error) {
	base := now.UTC().Format("20060102T150405.000000Z") + "-" + client
	for n := 0; n < 100; n++ {
		name := base
		if n > 0 {
			name = fmt.Sprintf("%s-%d", base, n)
		}
		dir := filepath.Join(root, name)
		if err := os.Mkdir(dir, 0o700); err == nil {
			if err := os.Mkdir(filepath.Join(dir, "files"), 0o700); err != nil {
				return "", err
			}
			return dir, nil
		} else if !errors.Is(err, os.ErrExist) {
			return "", err
		}
	}
	return "", fmt.Errorf("could not allocate a backup directory under %s", root)
}

func writeManifest(dir string, m *Manifest) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(dir, "manifest.json"), append(b, '\n'), 0o600)
}

// LatestBackup finds the newest backup set that has not been undone, for one
// client (or any client when client is empty).
func LatestBackup(root, client string) (string, *Manifest, error) {
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil, fmt.Errorf("no connect backups found under %s", root)
	}
	if err != nil {
		return "", nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	for _, n := range names {
		dir := filepath.Join(root, n)
		b, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
		if err != nil {
			continue
		}
		var m Manifest
		if json.Unmarshal(b, &m) != nil || m.Undone {
			continue
		}
		if client == "" || m.Client == client {
			return dir, &m, nil
		}
	}
	if client != "" {
		return "", nil, fmt.Errorf("no connect backup to undo for client %q under %s", client, root)
	}
	return "", nil, fmt.Errorf("no connect backup to undo under %s", root)
}

// PlanUndo builds the changes that restore a backup set.
func PlanUndo(dir string, m *Manifest) ([]Change, error) {
	var out []Change
	for _, f := range m.Files {
		cur, existed, mode, err := readFile(f.Path)
		if err != nil {
			return nil, err
		}
		if !existed {
			mode = os.FileMode(f.Mode)
		}
		c := Change{Path: f.Path, Before: cur, Existed: existed, Mode: mode}
		if f.Existed {
			if filepath.IsAbs(f.Backup) || strings.Contains(f.Backup, "..") {
				return nil, fmt.Errorf("manifest %s names a backup outside its directory", dir)
			}
			orig, err := os.ReadFile(filepath.Join(dir, f.Backup))
			if err != nil {
				return nil, fmt.Errorf("read backup of %s: %w", f.Path, err)
			}
			c.After = orig
			c.Mode = os.FileMode(f.Mode)
		} else {
			c.Delete = true
		}
		out = append(out, c)
	}
	return out, nil
}

// ApplyUndo writes the restoring changes and marks the backup set undone.
func ApplyUndo(dir string, m *Manifest, changes []Change) error {
	for _, c := range changes {
		if !c.Changed() {
			continue
		}
		if c.Delete {
			if err := os.Remove(resolveLink(c.Path)); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			continue
		}
		if err := writeAtomic(c.Path, c.After, c.Mode); err != nil {
			return fmt.Errorf("restore %s: %w", c.Path, err)
		}
	}
	m.Undone = true
	return writeManifest(dir, m)
}
