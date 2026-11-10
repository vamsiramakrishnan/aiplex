//go:build e2e

package e2e

import (
	"encoding/json"
	"testing"
)

type catalogRecords struct {
	Records []struct {
		ModelID  string `json:"model_id"`
		Provider string `json:"provider"`
		Usage    *struct {
			InputTokens     *int64 `json:"input_tokens"`
			OutputTokens    *int64 `json:"output_tokens"`
			CacheReadTokens *int64 `json:"cache_read_tokens"`
			CacheWrite      *int64 `json:"cache_write_tokens"`
		} `json:"usage"`
		Cost struct {
			Basis    string `json:"basis"`
			Currency string `json:"currency"`
			Amount   string `json:"amount"`
			Scale    int    `json:"scale"`
		} `json:"cost"`
	} `json:"records"`
}

// A catalogue price imported through the CLI prices a real exchange exactly.
// The model ids are real catalogue ids (the mock upstream accepts any model
// name), so the imported price matches the ledger record by provider and model
// id.
func TestCatalogueImportPricesLiveRecordsExactly(t *testing.T) {
	e := start(t, withProviderSecrets)
	origin := e.stack.ModelsURL
	surfaces := map[string]string{
		"oai-cat": `surface:
  id: oai-cat
  protocol: openai-chat
  origin: ` + origin + `
  models: [gpt-4o-mini, gpt-e2e-unlisted]
  operations: [openai.v1.chat-completions]
  provider: {id: openai}
auth: {type: bearer, secret_ref: openai-key}
`,
		"ant-cat": `surface:
  id: ant-cat
  protocol: anthropic-messages
  origin: ` + origin + `
  models: [claude-haiku-4-5]
  operations: [anthropic.messages]
  provider: {id: anthropic, anthropic_version: "2023-06-01"}
auth: {type: api_key, in: header, name: x-api-key, secret_ref: anthropic-key}
`,
	}
	for id, spec := range surfaces {
		e.mustCLI("llm", "surfaces", "apply", "-f", writeFile(t, id+".yaml", spec))
	}
	ap := "id: catalog\ndisplay_name: Catalogue e2e\ncaller_kinds: [user, agent, client]\nmodels:\n" +
		"  - {name: gpt-4o-mini, surface_ref: oai-cat, native_model: gpt-4o-mini}\n" +
		"  - {name: gpt-e2e-unlisted, surface_ref: oai-cat, native_model: gpt-e2e-unlisted}\n" +
		"  - {name: claude-haiku-4-5, surface_ref: ant-cat, native_model: claude-haiku-4-5}\n"
	e.mustCLI("access-points", "apply", "-f", writeFile(t, "ap.yaml", ap))

	send := func(model string) {
		t.Helper()
		path, body, headers := "/ap/catalog/v1/chat/completions", `{"model":"`+model+`","messages":[{"role":"user","content":"one two three four"}]}`, []string(nil)
		if model == "claude-haiku-4-5" {
			path, body, headers = "/ap/catalog/v1/messages", `{"model":"`+model+`","max_tokens":16,"messages":[{"role":"user","content":"one two three four"}]}`, []string{"anthropic-version", "2023-06-01"}
		}
		if resp := e.post(path, body, headers...); resp.Status != 200 {
			t.Fatalf("%s: %d %s", model, resp.Status, resp.Body)
		}
	}
	for _, m := range []string{"claude-haiku-4-5", "gpt-4o-mini", "gpt-e2e-unlisted"} {
		send(m) // before any import: cost is unknown, never zero
	}

	// Search and show write nothing.
	r := e.mustCLI("usage", "prices", "catalog", "search", "claude-haiku-4-5", "--provider", "anthropic")
	contains(t, r.Out, "anthropic/claude-haiku-4-5", "catalog search")
	r = e.mustCLI("usage", "prices", "catalog", "show", "anthropic/claude-haiku-4-5")
	contains(t, r.Out, "USD 1 per 1M tokens", "catalog show")
	r = e.mustCLI("usage", "prices", "list")
	notContains(t, r.Out, "claude-haiku-4-5", "search and show must not import")

	// Dry run writes nothing; without a terminal --yes is required.
	r = e.mustCLI("usage", "prices", "catalog", "import", "anthropic/claude-haiku-4-5", "--dry-run")
	contains(t, r.Out, "USD 5 per 1M tokens", "dry run shows the exact decimal")
	r = e.mustCLI("usage", "prices", "list")
	notContains(t, r.Out, "claude-haiku-4-5", "dry run must not write")
	if r = e.cli("usage", "prices", "catalog", "import", "anthropic/claude-haiku-4-5"); r.Code == 0 {
		t.Fatalf("import without --yes and without a terminal must be refused: %s", r)
	}

	// Import everything the access point uses that the catalogue lists.
	r = e.mustCLI("usage", "prices", "catalog", "import", "--all-for-access-point", "catalog", "--yes")
	contains(t, r.Out, "created   price anthropic-claude-haiku-4-5", "access point import")
	contains(t, r.Out, "created   price openai-gpt-4o-mini", "access point import")
	contains(t, r.Out, "gpt-e2e-unlisted", "models the catalogue lacks are listed")
	r = e.mustCLI("usage", "prices", "catalog", "import", "anthropic/claude-haiku-4-5", "--yes")
	contains(t, r.Out, "skipped: exists, use --replace", "an existing price is never overwritten")

	// The stored price carries its provenance.
	r = e.mustCLI("usage", "prices", "get", "anthropic-claude-haiku-4-5", "-o", "json")
	var got struct {
		Price struct {
			Source struct {
				Catalog     string `json:"catalog"`
				Commit      string `json:"commit"`
				RetrievedAt string `json:"retrieved_at"`
			} `json:"source"`
		} `json:"price"`
	}
	if err := json.Unmarshal([]byte(r.Out), &got); err != nil || got.Price.Source.Catalog != "litellm" || len(got.Price.Source.Commit) != 40 || got.Price.Source.RetrievedAt == "" {
		t.Fatalf("provenance missing (%v): %s", err, r.Out)
	}

	for _, m := range []string{"claude-haiku-4-5", "gpt-4o-mini", "gpt-e2e-unlisted"} {
		send(m)
	}
	r = e.mustCLI("usage", "records", "-o", "json")
	var page catalogRecords
	if err := json.Unmarshal([]byte(r.Out), &page); err != nil {
		t.Fatalf("records: %v\n%s", err, r.Out)
	}
	estimated := map[string]int{}
	unknown := map[string]int{}
	for _, rec := range page.Records {
		switch rec.Cost.Basis {
		case "estimated":
			estimated[rec.ModelID]++
			if rec.ModelID != "claude-haiku-4-5" || rec.Provider != "anthropic" || rec.Usage == nil || rec.Usage.InputTokens == nil || rec.Usage.OutputTokens == nil {
				t.Fatalf("unexpected estimated record: %+v", rec)
			}
			if rec.Usage.CacheReadTokens != nil && *rec.Usage.CacheReadTokens != 0 || rec.Usage.CacheWrite != nil && *rec.Usage.CacheWrite != 0 {
				t.Fatalf("the mock reports cache usage; the expected cost below assumes none: %+v", rec.Usage)
			}
			if *rec.Usage.InputTokens == 0 || *rec.Usage.OutputTokens == 0 {
				t.Fatal("the mock reported no tokens; the check proves nothing")
			}
			// USD 1 per million input tokens and USD 5 per million output tokens
			// are 1 and 5 millionths of a dollar per token.
			want := *rec.Usage.InputTokens*1 + *rec.Usage.OutputTokens*5
			if rec.Cost.Amount != itoa(want) || rec.Cost.Scale != 6 || rec.Cost.Currency != "USD" {
				t.Fatalf("cost %+v, want %d at scale 6 for %d in / %d out tokens", rec.Cost, want, *rec.Usage.InputTokens, *rec.Usage.OutputTokens)
			}
		case "unknown":
			unknown[rec.ModelID]++
		default:
			t.Fatalf("unexpected cost basis: %+v", rec.Cost)
		}
	}
	if estimated["claude-haiku-4-5"] != 1 {
		t.Fatalf("estimated=%v unknown=%v\n%s", estimated, unknown, r.Out)
	}
	// Two records of each other model: the one before the import and the one
	// after. The catalogue prices cache reads of gpt-4o-mini, which the mock
	// upstream does not report, so that cost stays unknown rather than
	// pretending the cache was empty. gpt-e2e-unlisted was never priced.
	if unknown["claude-haiku-4-5"] != 1 || unknown["gpt-4o-mini"] != 2 || unknown["gpt-e2e-unlisted"] != 2 {
		t.Fatalf("estimated=%v unknown=%v", estimated, unknown)
	}

	// Off a terminal nothing is printed on stderr about the catalogue.
	r = e.mustCLI("usage", "records", "--model", "gpt-e2e-unlisted")
	notContains(t, r.Err, "price catalogue", "no hint off a terminal")
}

func itoa(v int64) string {
	b, _ := json.Marshal(v)
	return string(b)
}
