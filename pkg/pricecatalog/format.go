// Package pricecatalog holds a pinned, trimmed snapshot of a third-party model
// price list and converts its entries to AIPlex model-price resources.
//
// The snapshot is DATA: list prices as a third party published them on a date.
// It can be stale or wrong, and it does not reflect discounts or commitments.
// Nothing here prices anything on its own: an operator chooses a model and
// imports it as an ordinary price resource. A model the catalogue does not list
// stays unknown.
package pricecatalog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strings"
)

// Meta describes where a snapshot came from.
type Meta struct {
	Catalog        string `json:"catalog"`
	Commit         string `json:"commit"`
	RetrievedAt    string `json:"retrieved_at"`
	SourceURL      string `json:"source_url"`
	License        string `json:"license"`
	UpstreamSHA256 string `json:"upstream_sha256"`
	Generator      string `json:"generator"`
}

// Rates are list prices in currency units per million tokens, as exact
// decimal strings. An empty string is a meter the catalogue does not state:
// unknown, never zero. "0" is a meter the source states to be free.
//
// The modality and one-hour rates are per million tokens of that kind: input
// audio, image and video tokens, output audio and image tokens, and cache
// writes with the one-hour TTL. They price counts the ledger holds
// (usageledger.UnitUsage). Tiers override the rates for requests whose input
// is long (see Tier); they exist only where the provider's rule is confirmed.
type Rates struct {
	Input        string `json:"input,omitempty"`
	Output       string `json:"output,omitempty"`
	CacheRead    string `json:"cache_read,omitempty"`
	CacheWrite   string `json:"cache_write,omitempty"`
	CacheWrite1h string `json:"cache_write_1h,omitempty"`
	InputAudio   string `json:"input_audio,omitempty"`
	InputImage   string `json:"input_image,omitempty"`
	InputVideo   string `json:"input_video,omitempty"`
	OutputAudio  string `json:"output_audio,omitempty"`
	OutputImage  string `json:"output_image,omitempty"`
	// Tiers are ascending long-context overrides; a Tier's Rates carries no
	// tiers of its own.
	Tiers []Tier `json:"tiers,omitempty"`
}

// Tier is the rates that replace the base rates for a whole request whose
// input_tokens (the ledger's total input) is greater than Above.
type Tier struct {
	Above int64 `json:"above_input_tokens"`
	Rates Rates `json:"rates"`
}

// Empty reports whether no rate is stated.
func (r Rates) Empty() bool {
	for _, v := range r.values() {
		if v != "" {
			return false
		}
	}
	return len(r.Tiers) == 0
}

// Entry is one catalogue model. Provider is the catalogue provider name and
// Model the model id as the provider spells it natively.
type Entry struct {
	Provider        string `json:"provider"`
	Model           string `json:"model"`
	Mode            string `json:"mode"`
	ContextWindow   int64  `json:"context_window,omitempty"`
	MaxOutputTokens int64  `json:"max_output_tokens,omitempty"`
	Source          string `json:"source,omitempty"`
	Upstream        string `json:"upstream"`
	Rates           Rates  `json:"per_mtok"`
	Batch           *Rates `json:"batch_per_mtok,omitempty"`
	// Priority and Flex are the rates of those service tiers where the
	// provider reports the tier a request was served on (OpenAI service_tier,
	// Gemini usageMetadata.serviceTier); they import as prices that select on
	// the record's service_tier.
	Priority *Rates `json:"priority_per_mtok,omitempty"`
	Flex     *Rates `json:"flex_per_mtok,omitempty"`
	// ServerTools are exact per-call prices (currency units per call) of
	// provider-side tools, by tool kind, where the provider reports the call
	// count (currently Anthropic web search).
	ServerTools map[string]string `json:"server_tools_per_call,omitempty"`
	// Blocking names upstream fields that change what a request costs and that
	// an AIPlex price cannot express (context-length tiers, cache TTL rates,
	// modality token rates). Importing the base rates alone can misstate cost,
	// so an import needs an explicit acceptance.
	Blocking []string `json:"blocking,omitempty"`
	// LeftOut names upstream fields that are not imported and do not change the
	// standard-tier text token cost (per-unit media, surcharges, other service
	// tiers, reasoning rates).
	LeftOut []string `json:"left_out,omitempty"`
}

// ID is the catalogue identity, "<provider>/<model>".
func (e Entry) ID() string { return e.Provider + "/" + e.Model }

// Snapshot is the whole catalogue file.
type Snapshot struct {
	Meta    Meta    `json:"meta"`
	Entries []Entry `json:"entries"`
}

// Marshal writes the snapshot deterministically: entries sorted, one per line.
func (s Snapshot) Marshal() ([]byte, error) {
	entries := append([]Entry(nil), s.Entries...)
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Provider != entries[j].Provider {
			return entries[i].Provider < entries[j].Provider
		}
		if entries[i].Model != entries[j].Model {
			return entries[i].Model < entries[j].Model
		}
		return entries[i].Upstream < entries[j].Upstream
	})
	var b bytes.Buffer
	meta, err := json.Marshal(s.Meta)
	if err != nil {
		return nil, err
	}
	b.WriteString("{\"meta\":")
	b.Write(meta)
	b.WriteString(",\n\"entries\":[\n")
	for i, e := range entries {
		raw, err := json.Marshal(e)
		if err != nil {
			return nil, err
		}
		b.WriteString(" ")
		b.Write(raw)
		if i < len(entries)-1 {
			b.WriteString(",")
		}
		b.WriteString("\n")
	}
	b.WriteString("]}\n")
	return b.Bytes(), nil
}

// MtokDecimal converts a decimal number written as text per single token
// (for example "1.5e-07", as the source file spells it) to the exact decimal
// price per million tokens ("0.15"). It never uses floating point.
func MtokDecimal(perToken string) (string, error) {
	perToken = strings.TrimSpace(perToken)
	r, ok := new(big.Rat).SetString(perToken)
	if !ok || perToken == "" || strings.Trim(perToken, "0123456789.eE+-") != "" {
		return "", fmt.Errorf("%q is not a decimal number", perToken)
	}
	if r.Sign() < 0 {
		return "", fmt.Errorf("%q is negative", perToken)
	}
	r.Mul(r, big.NewRat(1_000_000, 1))
	return decimalString(r)
}

// decimalString renders r exactly, with the fewest fractional digits.
func decimalString(r *big.Rat) (string, error) {
	for digits := 0; digits <= 40; digits++ {
		s := r.FloatString(digits)
		if back, ok := new(big.Rat).SetString(s); ok && back.Cmp(r) == 0 {
			return s, nil
		}
	}
	return "", fmt.Errorf("%s has no short exact decimal form", r.String())
}

// Signature names which scalar rates are stated, so two rate sets can be
// checked to price the same meters.
func (r Rates) Signature() string {
	sig := ""
	for i, v := range r.values() {
		if v != "" {
			sig += string(rune('a' + i))
		}
	}
	return sig
}

// values lists the scalar rates in a fixed order.
func (r Rates) values() []string {
	return []string{r.Input, r.Output, r.CacheRead, r.CacheWrite, r.CacheWrite1h, r.InputAudio, r.InputImage, r.InputVideo, r.OutputAudio, r.OutputImage}
}
