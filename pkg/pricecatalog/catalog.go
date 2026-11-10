package pricecatalog

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
)

//go:generate go run ./gen -same

//go:embed snapshot.json
var snapshotJSON []byte

//go:embed snapshot.sha256
var snapshotSum string

// Providers maps each catalogue provider name to the provider id AIPlex writes
// on a usage record (and so on a price). The catalogue names are the ids used
// in "catalog show <provider>/<model>". Vertex AI Gemini records carry the
// Google publisher; Claude on Vertex carries the anthropic publisher; every
// OpenAI-compatible vendor is recorded as openai-compatible, whatever endpoint
// the operator pointed that surface at.
var Providers = map[string]string{
	"openai":           "openai",
	"anthropic":        "anthropic",
	"azure-openai":     "azure-openai",
	"bedrock":          "bedrock",
	"gemini-developer": "gemini-developer",
	"google":           "google",
	"vertex-anthropic": "anthropic",
	"deepseek":         "openai-compatible",
	"groq":             "openai-compatible",
	"mistral":          "openai-compatible",
	"xai":              "openai-compatible",
}

// LedgerProvider returns the provider id a usage record carries for entries of
// the catalogue provider, or "" for an unknown catalogue provider.
func LedgerProvider(catalogProvider string) string { return Providers[catalogProvider] }

// ProviderNames lists the catalogue provider names, sorted.
func ProviderNames() []string {
	names := make([]string, 0, len(Providers))
	for name := range Providers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Catalog is a loaded snapshot with lookup by identity.
type Catalog struct {
	Snapshot
	byID map[string]int
}

// Parse decodes a snapshot and indexes it. Duplicate identities are refused.
func Parse(raw []byte) (*Catalog, error) {
	var s Snapshot
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("price catalogue: %w", err)
	}
	c := &Catalog{Snapshot: s, byID: make(map[string]int, len(s.Entries))}
	for i, e := range s.Entries {
		if e.Provider == "" || e.Model == "" || Providers[e.Provider] == "" {
			return nil, fmt.Errorf("price catalogue: entry %d has no known provider/model", i)
		}
		if _, dup := c.byID[e.ID()]; dup {
			return nil, fmt.Errorf("price catalogue: duplicate entry %s", e.ID())
		}
		c.byID[e.ID()] = i
	}
	return c, nil
}

var (
	loadOnce sync.Once
	loaded   *Catalog
	loadErr  error
)

// Embedded returns the snapshot compiled into the binary, after checking it
// against its recorded checksum.
func Embedded() (*Catalog, error) {
	loadOnce.Do(func() {
		if err := VerifyEmbedded(); err != nil {
			loadErr = err
			return
		}
		loaded, loadErr = Parse(snapshotJSON)
	})
	return loaded, loadErr
}

// EmbeddedChecksum returns the SHA-256 of the embedded snapshot file.
func EmbeddedChecksum() string {
	sum := sha256.Sum256(snapshotJSON)
	return hex.EncodeToString(sum[:])
}

// VerifyEmbedded checks the embedded snapshot against snapshot.sha256, the
// checksum recorded when the snapshot was generated.
func VerifyEmbedded() error {
	want := strings.TrimSpace(snapshotSum)
	if got := EmbeddedChecksum(); got != want {
		return fmt.Errorf("price catalogue snapshot does not match its recorded checksum (have %s, recorded %s); regenerate it with make update-price-catalog", got, want)
	}
	return nil
}

// Get returns the entry for "<provider>/<model>".
func (c *Catalog) Get(id string) (Entry, bool) {
	i, ok := c.byID[id]
	if !ok {
		return Entry{}, false
	}
	return c.Entries[i], true
}

// Lookup returns the entry for a catalogue provider and model id.
func (c *Catalog) Lookup(provider, model string) (Entry, bool) {
	return c.Get(provider + "/" + model)
}

// Search returns entries whose "<provider>/<model>" contains every word of
// text (case-insensitive), optionally restricted to one catalogue provider.
// It is substring matching only: no fuzzy matching or aliasing.
func (c *Catalog) Search(text, provider string) []Entry {
	words := strings.Fields(strings.ToLower(text))
	out := []Entry{}
	for _, e := range c.Entries {
		if provider != "" && e.Provider != provider {
			continue
		}
		id := strings.ToLower(e.ID())
		match := true
		for _, w := range words {
			if !strings.Contains(id, w) {
				match = false
				break
			}
		}
		if match {
			out = append(out, e)
		}
	}
	return out
}

// SplitID splits "<provider>/<model>" at the first slash. The model may itself
// contain slashes (some providers name models "vendor/model").
func SplitID(id string) (provider, model string, err error) {
	provider, model, ok := strings.Cut(id, "/")
	if !ok || provider == "" || model == "" {
		return "", "", fmt.Errorf("%q is not <provider>/<model>", id)
	}
	if Providers[provider] == "" {
		return "", "", fmt.Errorf("unknown catalogue provider %q (known: %s)", provider, strings.Join(ProviderNames(), ", "))
	}
	return provider, model, nil
}
