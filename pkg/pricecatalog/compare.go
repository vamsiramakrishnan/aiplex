package pricecatalog

import (
	"math/big"
	"sort"
)

// Disagreement is one meter of one model that both sources state and that
// differs. Above is 0 for the base rate, or the long-context threshold of a
// tier. Values are decimal USD per million tokens as each source states them.
type Disagreement struct {
	ID    string `json:"id"`
	Meter string `json:"meter"`
	Above int64  `json:"above_input_tokens,omitempty"`
	A     string `json:"a"`
	B     string `json:"b"`
}

var rateNames = []string{"input", "output", "cache_read", "cache_write", "cache_write_1h", "input_audio", "input_image", "input_video", "output_audio", "output_image"}

func sameDecimal(a, b string) bool {
	x, okA := new(big.Rat).SetString(a)
	y, okB := new(big.Rat).SetString(b)
	return okA && okB && x.Cmp(y) == 0
}

func compareRates(id string, above int64, a, b Rates) []Disagreement {
	var out []Disagreement
	av, bv := a.values(), b.values()
	for i, name := range rateNames {
		if av[i] != "" && bv[i] != "" && !sameDecimal(av[i], bv[i]) {
			out = append(out, Disagreement{ID: id, Meter: name, Above: above, A: av[i], B: bv[i]})
		}
	}
	return out
}

// Disagreements compares two sources' entries for the same provider and model.
// A meter that only one source states is not a disagreement (the other is
// silent, which is unknown); only two stated values that differ numerically
// are. Base rates and the rates of tiers at the same threshold are compared;
// batch, priority and flex rates exist in only one source today.
func Disagreements(a, b Entry) []Disagreement {
	out := compareRates(a.ID(), 0, a.Rates, b.Rates)
	for _, ta := range a.Rates.Tiers {
		for _, tb := range b.Rates.Tiers {
			if ta.Above == tb.Above {
				out = append(out, compareRates(a.ID(), ta.Above, ta.Rates, tb.Rates)...)
			}
		}
	}
	return out
}

// CompareCatalogs lists every disagreement between two catalogues over the
// models both list, ordered by id and meter.
func CompareCatalogs(a, b *Catalog) []Disagreement {
	var out []Disagreement
	for _, ea := range a.Entries {
		if eb, ok := b.Get(ea.ID()); ok {
			out = append(out, Disagreements(ea, eb)...)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].ID != out[j].ID {
			return out[i].ID < out[j].ID
		}
		if out[i].Meter != out[j].Meter {
			return out[i].Meter < out[j].Meter
		}
		return out[i].Above < out[j].Above
	})
	return out
}
