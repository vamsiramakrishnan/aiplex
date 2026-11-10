package api

import (
	"strings"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/pkg/usageledger"
)

// An admitted caller asking for a model the access point does not offer gets a
// protocol-native 404 and a ledger record (status error, error_class
// model_not_found, bounded model_id, no usage, no cost). A caller the access
// point does not admit leaves nothing, so strangers cannot fill the ledger.
func TestUnknownModelThroughAccessPointIsRecordedForAdmittedCallersOnly(t *testing.T) {
	f := newAPFixture(t)
	records := attachLedger(t, f.h)

	long := strings.Repeat("x", 400)
	for _, name := range []string{"nope", long, "bad\x01name\nwith\x7fcontrols"} {
		w := f.do("POST", "/ap/ide/v1/chat/completions", `{"model":`+jsonString(name)+`}`)
		if w.Code != 404 {
			t.Fatalf("%q: %d %s", name, w.Code, w.Body.String())
		}
	}
	got := records()
	if len(got) != 3 {
		t.Fatalf("expected a record per refused request, got %d: %+v", len(got), got)
	}
	byModel := map[string]usageledger.Record{}
	for _, r := range got {
		byModel[r.ModelID] = r
		if r.Status != usageledger.StatusError || r.ErrorClass != "model_not_found" || r.HTTPStatus != 404 || r.Usage != nil || r.Cost.Basis != usageledger.BasisUnknown || r.AccessPointID != "ide" || r.CallerKind == "" || r.Operation != "unknown-model" || r.Provider != "" {
			t.Fatalf("record %+v", r)
		}
		if len(r.ModelID) > 128 || strings.ContainsAny(r.ModelID, "\x01\n\x7f") {
			t.Fatalf("model_id not bounded and sanitized: %q", r.ModelID)
		}
		if err := r.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := byModel["nope"]; !ok || len(byModel[strings.Repeat("x", 128)].ModelID) != 128 || byModel["badnamewithcontrols"].ID == "" {
		t.Fatalf("model ids: %v", byModel)
	}

	// Not admitted by the access point (a caller kind it does not allow, or no
	// identity at all): refused, and nothing is recorded.
	f.as(nil)
	if w := f.do("POST", "/ap/ide/v1/chat/completions", `{"model":"nope"}`); w.Code != 401 {
		t.Fatalf("anonymous: %d", w.Code)
	}
	if after := records(); len(after) != 3 {
		t.Fatalf("a refused stranger wrote a record: %+v", after)
	}
}

func TestBoundedModelName(t *testing.T) {
	for in, want := range map[string]string{
		"gpt-4o":                 "gpt-4o",
		"  spaced  ":             "spaced",
		"a\u0000b\tc\r\nd":       "abcd",
		"bad\xffutf8":            "badutf8",
		"":                       "",
		strings.Repeat("é", 300): strings.Repeat("é", 128),
	} {
		if got := boundedModelName(in); got != want {
			t.Errorf("boundedModelName(%q) = %q, want %q", in, got, want)
		}
	}
}

func jsonString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, c := range s {
		switch {
		case c == '"' || c == '\\':
			b.WriteByte('\\')
			b.WriteRune(c)
		case c < 0x20 || c == 0x7f:
			b.WriteString(`\u00`)
			b.WriteString(string("0123456789abcdef"[c>>4]))
			b.WriteString(string("0123456789abcdef"[c&0xf]))
		default:
			b.WriteRune(c)
		}
	}
	b.WriteByte('"')
	return b.String()
}
