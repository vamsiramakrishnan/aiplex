package capability

import "testing"

func TestParseURI(t *testing.T) {
	cases := []struct {
		in       string
		wantKind Kind
		wantName string
		wantVer  string
		wantErr  bool
	}{
		{"cap://tool/search_curriculum@v1", KindTool, "search_curriculum", "v1", false},
		{"cap://task/research@v1.2", KindTask, "research", "v1.2", false},
		{"cap://model/gemini-2.5-flash@v1", KindModel, "gemini-2.5-flash", "v1", false},
		{"cap://memory/students/alice/profile@v1", KindMemory, "students/alice/profile", "v1", false},
		{"cap://meta/deploy@latest", KindMeta, "deploy", "latest", false},
		{"cap://skill/code-review@v2.1.0", KindSkill, "code-review", "v2.1.0", false},

		{"cap://bogus/x@v1", "", "", "", true},
		{"http://tool/x@v1", "", "", "", true},
		{"cap://tool/x", "", "", "", true},
		{"cap://tool/@v1", "", "", "", true},
		{"cap://tool/bad name@v1", "", "", "", true},
		{"cap://tool/x@bad", "", "", "", true},
	}
	for _, c := range cases {
		got, err := ParseURI(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("ParseURI(%q): want error, got %+v", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseURI(%q): unexpected error: %v", c.in, err)
			continue
		}
		if got.Kind != c.wantKind || got.Name != c.wantName || got.Version != c.wantVer {
			t.Errorf("ParseURI(%q): got {%s,%s,%s}, want {%s,%s,%s}",
				c.in, got.Kind, got.Name, got.Version, c.wantKind, c.wantName, c.wantVer)
		}
		if got.String() != c.in {
			t.Errorf("ParseURI(%q).String() = %q, want round-trip", c.in, got.String())
		}
	}
}

func TestNewURI(t *testing.T) {
	u := New(KindTool, "search", "")
	if u.String() != "cap://tool/search@v1" {
		t.Errorf("default version: got %q", u.String())
	}
}

func TestCapAllows(t *testing.T) {
	c := Cap{URI: "cap://tool/search@v1", Actions: []string{"call"}}
	if !c.Allows("cap://tool/search@v1", "call") {
		t.Error("expected allow")
	}
	if c.Allows("cap://tool/search@v1", "nope") {
		t.Error("unexpected allow on unknown action")
	}
	if c.Allows("cap://tool/other@v1", "call") {
		t.Error("unexpected allow on different URI")
	}

	// Empty actions means all kind actions
	any := Cap{URI: "cap://memory/x@v1"}
	if !any.Allows("cap://memory/x@v1", "read") {
		t.Error("empty actions should grant kind defaults")
	}
	if any.Allows("cap://memory/x@v1", "bogus") {
		t.Error("empty actions should still reject non-kind actions")
	}
}

func TestCapSetIntersect(t *testing.T) {
	a := CapSet{
		{URI: "cap://tool/a@v1", Actions: []string{"call"}},
		{URI: "cap://tool/b@v1", Actions: []string{"call"}},
	}
	b := CapSet{
		{URI: "cap://tool/a@v1", Actions: []string{"call"}},
		{URI: "cap://tool/c@v1", Actions: []string{"call"}},
	}
	got := a.Intersect(b)
	if len(got) != 1 || got[0].URI != "cap://tool/a@v1" {
		t.Errorf("intersect: got %+v", got)
	}
}

func TestCapSetUnionDedup(t *testing.T) {
	a := CapSet{{URI: "cap://tool/a@v1"}}
	b := CapSet{{URI: "cap://tool/a@v1"}, {URI: "cap://tool/b@v1"}}
	got := a.Union(b)
	if len(got) != 2 {
		t.Errorf("union dedup: got %+v", got)
	}
}

func TestKindNamespace(t *testing.T) {
	cases := map[Kind]string{
		KindTool:     "mcplex",
		KindTask:     "a2aplex",
		KindModel:    "aiplex-system",
		KindSkill:    "skillsplex",
		KindMemory:   "memplex",
		KindAgent:    "agentplex",
		KindWorkflow: "aiplex-system",
		KindMeta:     "aiplex-system",
	}
	for k, ns := range cases {
		if got := k.Namespace(); got != ns {
			t.Errorf("Kind(%s).Namespace() = %q, want %q", k, got, ns)
		}
	}
}
