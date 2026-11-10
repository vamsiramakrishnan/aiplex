//go:build e2e

package e2e

import (
	"encoding/json"
	"testing"
)

// 3. Usage ledger through the CLI: prices, estimated cost, unknown versus
// zero, records and summaries in table and JSON form.
func TestUsageLedgerPricesSummaryUnknownVersusZero(t *testing.T) {
	e := modelEnv(t)
	price := `{"id":"gpt-e2e","model_id":"gpt-e2e","currency":"USD","scale":6,
  "meters":{"input_tokens":{"numerator":1000,"denominator":1},"output_tokens":{"numerator":2000,"denominator":1},"requests":{"numerator":0,"denominator":1}}}`
	r := e.mustCLI("usage", "prices", "set", "-f", writeFile(t, "price.json", price))
	r = e.mustCLI("usage", "prices", "list")
	contains(t, r.Out, "gpt-e2e", "prices list")
	r = e.mustCLI("usage", "prices", "get", "gpt-e2e", "-o", "json")
	contains(t, r.Out, `"model_id": "gpt-e2e"`, "prices get")

	// Priced, unpriced and no-usage exchanges.
	for _, c := range []struct{ path, body string }{
		{"/ap/models/v1/chat/completions", `{"model":"gpt-e2e","messages":[{"role":"user","content":"one two three"}]}`},
		{"/ap/models/v1/chat/completions", `{"model":"compat-e2e","messages":[{"role":"user","content":"unpriced"}]}`},
		{"/ap/models/v1/messages", `{"model":"claude-e2e","max_tokens":8,"messages":[{"role":"user","content":"zero cache"}]}`},
		{"/ap/models/v1/messages/count_tokens", `{"model":"claude-e2e","messages":[{"role":"user","content":"count"}]}`},
	} {
		if resp := e.post(c.path, c.body, "anthropic-version", "2023-06-01"); resp.Status != 200 {
			t.Fatalf("%s: %d %s", c.path, resp.Status, resp.Body)
		}
	}

	r = e.mustCLI("usage", "records", "-o", "json")
	var page struct {
		Records []struct {
			ModelID   string                     `json:"model_id"`
			Operation string                     `json:"operation"`
			Usage     map[string]json.RawMessage `json:"usage"`
			Cost      struct {
				Basis    string `json:"basis"`
				Currency string `json:"currency"`
				Amount   string `json:"amount"`
				Scale    int    `json:"scale"`
			} `json:"cost"`
		} `json:"records"`
	}
	if err := json.Unmarshal([]byte(r.Out), &page); err != nil {
		t.Fatalf("records json: %v\n%s", err, r.Out)
	}
	var priced, unpriced, anthropic, counted bool
	for _, rec := range page.Records {
		switch {
		case rec.ModelID == "gpt-e2e":
			// 3 input words x 1000 + reply output x 2000 micro-USD.
			if rec.Cost.Basis != "estimated" || rec.Cost.Currency != "USD" || rec.Cost.Amount == "" || rec.Cost.Amount == "0" {
				t.Fatalf("priced record cost: %+v", rec.Cost)
			}
			priced = true
		case rec.ModelID == "compat-e2e":
			if rec.Cost.Basis != "unknown" {
				t.Fatalf("unpriced record has cost %+v", rec.Cost)
			}
			unpriced = true
		case rec.Operation == "anthropic.messages":
			// Anthropic reports cache writes as 0; OpenAI does not report them.
			if string(rec.Usage["cache_write_tokens"]) != "0" {
				t.Fatalf("reported zero lost: cache_write_tokens=%s", rec.Usage["cache_write_tokens"])
			}
			anthropic = true
		case rec.Operation == "anthropic.messages-count-tokens":
			if rec.Usage != nil {
				t.Fatalf("count_tokens is never read for usage, got %v", rec.Usage)
			}
			counted = true
		}
	}
	if !priced || !unpriced || !anthropic || !counted {
		t.Fatalf("missing records: priced=%v unpriced=%v anthropic=%v count=%v\n%s", priced, unpriced, anthropic, counted, r.Out)
	}

	// Table output distinguishes unknown from zero and shows estimated cost.
	r = e.mustCLI("usage", "records", "--model", "gpt-e2e")
	contains(t, r.Out, "(estimated)", "records table cost")
	r = e.mustCLI("usage", "records", "--model", "compat-e2e")
	contains(t, r.Out, "unknown", "records table unknown")

	// Summary grouped by model and status.
	r = e.mustCLI("usage", "summary", "--by", "model,status", "-o", "json")
	var summary map[string]any
	if err := json.Unmarshal([]byte(r.Out), &summary); err != nil {
		t.Fatalf("summary json: %v\n%s", err, r.Out)
	}
	contains(t, r.Out, "gpt-e2e", "summary groups")
	contains(t, r.Out, "compat-e2e", "summary groups")
	r = e.mustCLI("usage", "summary", "--by", "model")
	contains(t, r.Out, "gpt-e2e", "summary table")
	contains(t, r.Out, "USD", "summary table cost")

	// Invalid group-by is a clear error, not a raw JSON dump.
	r = e.cli("usage", "summary", "--by", "colour")
	if r.Code == 0 {
		t.Fatalf("invalid --by accepted: %s", r)
	}
	notContains(t, r.Err, `{"code"`, "raw JSON error")
}
