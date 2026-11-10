package toolpolicy

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func facts(args string) Facts {
	parsed, err := ParseArgs(json.RawMessage(args))
	if err != nil {
		panic(err)
	}
	return Facts{
		ToolName: "write_file", ToolConnection: "files", Args: parsed, HasArgs: true,
		Caller:        Caller{Kind: "user", Principal: "p1", Groups: []string{"eng", "ops"}, GroupsKnown: true},
		AccessPointID: "team", Time: time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC),
	}
}

func mustCompile(t *testing.T, rules ...Rule) *Set {
	t.Helper()
	set, err := Compile(rules)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return set
}

func deny(id, when string) Rule  { return Rule{ID: id, When: when, Effect: Deny, Message: "no " + id} }
func allow(id, when string) Rule { return Rule{ID: id, When: when, Effect: Allow} }

func TestCompileRejects(t *testing.T) {
	long := "args.a == '" + strings.Repeat("x", MaxExpressionBytes) + "'"
	nodes := "true" + strings.Repeat(" && true", 200)
	cases := map[string]Rule{
		"syntax":           deny("r", "args.a =="),
		"undeclared":       deny("r", "secret.value == 1"),
		"unknown field":    deny("r", "tool.nme == 'x'"),
		"unknown caller":   deny("r", "caller.email == 'x'"),
		"unknown hint":     deny("r", "tool.annotations.nope == true"),
		"not boolean":      deny("r", "tool.name"),
		"type error":       deny("r", "size(5) > 1"),
		"too long":         deny("r", long),
		"too many nodes":   deny("r", nodes),
		"bad id":           deny("Bad Id", "true"),
		"bad effect":       {ID: "r", When: "true", Effect: "log"},
		"empty":            deny("r", "  "),
		"control message":  {ID: "r", When: "true", Effect: Deny, Message: "a\nb"},
		"long message":     {ID: "r", When: "true", Effect: Deny, Message: strings.Repeat("m", MaxMessageBytes+1)},
		"unknown function": deny("r", "now() > 1"),
		"duration clock":   deny("r", "timestamp('2026-01-01T00:00:00Z') < request.nope"),
		"dynamic pattern":  deny("r", "args.s.matches(args.p)"),
		"dynamic pattern2": deny("r", "matches(args.s, args.p)"),
		"long pattern":     deny("r", "args.s.matches('"+strings.Repeat("a", MaxPatternBytes+1)+"')"),
	}
	for name, rule := range cases {
		if _, err := Compile([]Rule{rule}); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := Compile([]Rule{deny("a", "true"), deny("a", "false")}); err == nil {
		t.Error("duplicate ids accepted")
	}
	many := make([]Rule, MaxRules+1)
	for i := range many {
		many[i] = deny("r"+strings.Repeat("a", i%5)+string(rune('a'+i%26))+string(rune('a'+i/26)), "true")
	}
	if _, err := Compile(many); err == nil {
		t.Error("too many rules accepted")
	}
}

func TestDenyByArgument(t *testing.T) {
	set := mustCompile(t, deny("no-etc", "path_under(args.path, '/etc')"))
	for path, wantAllowed := range map[string]bool{"/etc/passwd": false, "/tmp/../etc/shadow": false, "/etc": false, "/etcetera/x": true, "/home/a": true} {
		d := set.Evaluate(context.Background(), facts(`{"path":`+quote(path)+`}`))
		if d.Allowed != wantAllowed {
			t.Errorf("%s: allowed=%v", path, d.Allowed)
		}
		if !d.Allowed && (d.Class != ClassDenied || d.RuleID != "no-etc" || d.Message != "no no-etc") {
			t.Errorf("%s: %+v", path, d)
		}
	}
}

func quote(s string) string { b, _ := json.Marshal(s); return string(b) }

func TestFirstDenyWinsAndEvaluationOrder(t *testing.T) {
	set := mustCompile(t, allow("a", "true"), deny("d1", "true"), deny("d2", "true"))
	d := set.Evaluate(context.Background(), facts(`{}`))
	if d.Allowed || d.RuleID != "d1" || d.Trace[2].Result != "not_evaluated" {
		t.Fatalf("%+v", d)
	}
}

func TestDefaultDenyWithAllowRules(t *testing.T) {
	set := mustCompile(t, allow("eng-only", "'eng' in caller.groups"), allow("admin", "caller.principal == 'root'"))
	if d := set.Evaluate(context.Background(), facts(`{}`)); !d.Allowed {
		t.Fatalf("group member refused: %+v", d)
	}
	f := facts(`{}`)
	f.Caller.Groups, f.Caller.Principal = []string{"sales"}, "p2"
	d := set.Evaluate(context.Background(), f)
	if d.Allowed || d.Class != ClassDenied || d.Reason != ReasonNoAllowRule || d.RuleID != "" {
		t.Fatalf("%+v", d)
	}
	// Deny-only sets allow what no rule matches.
	only := mustCompile(t, deny("d", "false"))
	if d := only.Evaluate(context.Background(), f); !d.Allowed {
		t.Fatalf("%+v", d)
	}
	// An allow rule does not override a matching deny.
	both := mustCompile(t, allow("a", "true"), deny("d", "args.danger == true"))
	if d := both.Evaluate(context.Background(), facts(`{"danger":true}`)); d.Allowed || d.RuleID != "d" {
		t.Fatalf("%+v", d)
	}
}

func TestUnknownGroupsAreNotEmpty(t *testing.T) {
	f := facts(`{}`)
	f.Caller.Groups, f.Caller.GroupsKnown = nil, false
	// Unguarded use is an evaluation error, which denies.
	d := mustCompile(t, deny("blocked", "'contractor' in caller.groups")).Evaluate(context.Background(), f)
	if d.Allowed || d.Class != ClassError || d.RuleID != "blocked" {
		t.Fatalf("%+v", d)
	}
	// An explicit guard decides on purpose.
	guarded := mustCompile(t, deny("blocked", "!caller.groups_known || 'contractor' in caller.groups"))
	if d := guarded.Evaluate(context.Background(), f); d.Allowed || d.Class != ClassDenied {
		t.Fatalf("%+v", d)
	}
	if d := guarded.Evaluate(context.Background(), facts(`{}`)); !d.Allowed {
		t.Fatalf("%+v", d)
	}
	// An allow rule on a group never matches unknown membership.
	allowGroup := mustCompile(t, allow("eng", "caller.groups_known && 'eng' in caller.groups"))
	if d := allowGroup.Evaluate(context.Background(), f); d.Allowed {
		t.Fatalf("%+v", d)
	}
}

func TestFailClosedCases(t *testing.T) {
	cases := map[string]string{
		"missing key":           `args.path == 'x'`,
		"type mismatch":         `args.count + 1 > 2`,
		"unknown annotation":    `tool.annotations.destructive == true`,
		"cost limit":            `args.items.all(a, args.items.all(b, args.items.all(c, a == b || c == a || b == c)))`,
		"url without host":      `url_host(args.url) == 'x'`,
		"relative path":         `path_under(args.rel, '/srv')`,
		"division by zero":      `1 / args.zero == 1`,
		"index out of range":    `args.items[99] == 1`,
		"cross-type compare ok": `args.s == 1`,
	}
	args := `{"count":"many","items":[` + strings.TrimSuffix(strings.Repeat("1,", 60), ",") + `],"url":"not a url","rel":"a/b","big":"` + strings.Repeat("a", MaxHelperString+1) + `","s":"abc","zero":0}`
	for name, when := range cases {
		set, err := Compile([]Rule{deny("r", when)})
		if err != nil {
			t.Errorf("%s: compile: %v", name, err)
			continue
		}
		d := set.Evaluate(context.Background(), facts(args))
		if name == "cross-type compare ok" {
			if !d.Allowed {
				t.Errorf("%s: %+v", name, d)
			}
			continue
		}
		if d.Allowed || d.Class != ClassError || d.RuleID != "r" {
			t.Errorf("%s: %+v", name, d)
		}
	}
}

func TestOversizeArguments(t *testing.T) {
	big := `{"x":"` + strings.Repeat("a", MaxArgsBytes) + `"}`
	if _, err := ParseArgs(json.RawMessage(big)); err != ErrArgsTooLarge {
		t.Fatalf("%v", err)
	}
	if _, err := ParseArgs(json.RawMessage(`[1]`)); err == nil {
		t.Fatal("array arguments accepted")
	}
	f := facts(`{}`)
	f.Args = map[string]any{"x": strings.Repeat("a", MaxArgsBytes)}
	d := mustCompile(t, deny("r", "false")).Evaluate(context.Background(), f)
	if d.Allowed || d.Class != ClassError || d.Reason != ReasonArgsTooLarge {
		t.Fatalf("%+v", d)
	}
}

func TestAnnotationsAbsentWhenUnknown(t *testing.T) {
	yes := true
	set := mustCompile(t, deny("destructive", "has(tool.annotations.destructive) && tool.annotations.destructive"))
	f := facts(`{}`)
	if d := set.Evaluate(context.Background(), f); !d.Allowed {
		t.Fatalf("unknown annotation guarded: %+v", d)
	}
	f.Annotations.Destructive = &yes
	if d := set.Evaluate(context.Background(), f); d.Allowed || d.RuleID != "destructive" {
		t.Fatalf("%+v", d)
	}
}

func TestHelpers(t *testing.T) {
	cases := []struct {
		when string
		args string
		want bool
	}{
		{`args.name.startsWith('tmp-') && args.name.endsWith('.txt')`, `{"name":"tmp-a.txt"}`, true},
		{`args.name.matches('^[a-z]+$')`, `{"name":"abc"}`, true},
		{`args.name.matches('^[a-z]+$')`, `{"name":"ab1"}`, false},
		{`matches(args.name, 'b')`, `{"name":"abc"}`, true},
		{`url_host(args.url) == 'example.com'`, `{"url":"https://Example.com./a?b=1"}`, true},
		{`url_host(args.url) in ['example.com', 'api.example.com']`, `{"url":"http://api.example.com:8080/x"}`, true},
		{`url_host(args.url) == 'example.com'`, `{"url":"https://example.com.evil.io/"}`, false},
		{`args.limit > 100`, `{"limit":250}`, true},
		{`args.limit >= 2.5`, `{"limit":3}`, true},
		{`any_in(caller.groups, ['ops', 'sec'])`, `{}`, true},
		{`any_in(caller.groups, ['sec'])`, `{}`, false},
		{`lower(args.m) == 'delete'`, `{"m":"DeLeTe"}`, true},
		{`has(args.opt) ? args.opt == true : false`, `{}`, false},
		{`request.time.getFullYear() == 2026`, `{}`, true},
		{`tool.connection == 'files' && access_point.id == 'team' && caller.kind == 'user'`, `{}`, true},
		{`size(access_point.labels) == 0`, `{}`, true},
	}
	for _, c := range cases {
		set, err := Compile([]Rule{deny("r", c.when)})
		if err != nil {
			t.Errorf("%s: %v", c.when, err)
			continue
		}
		d := set.Evaluate(context.Background(), facts(c.args))
		if d.Class == ClassError {
			t.Errorf("%s: error %v", c.when, d.Trace)
			continue
		}
		if matched := !d.Allowed; matched != c.want {
			t.Errorf("%s %s: matched=%v want %v", c.when, c.args, matched, c.want)
		}
	}
}

func TestPathUnderNormalizes(t *testing.T) {
	for _, c := range []struct {
		p, root string
		want    bool
	}{
		{"/srv/app/../../etc/passwd", "/srv", false},
		{"/srv//app/./x", "/srv", true},
		{"/srv", "/srv/", true},
		{"/srvx", "/srv", false},
		{"/anything", "/", true},
	} {
		got, err := pathUnder(c.p, c.root)
		if err != nil || got != c.want {
			t.Errorf("%s under %s: %v %v", c.p, c.root, got, err)
		}
	}
	for _, bad := range [][2]string{{"rel", "/srv"}, {"/srv\\x", "/srv"}, {"/a\x00", "/a"}, {"/a", "relroot"}} {
		if _, err := pathUnder(bad[0], bad[1]); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestURLHostStrict(t *testing.T) {
	for _, bad := range []string{"", "ftp://x.com", "https://user:pw@x.com/", "https://x.com\\@evil.com", "https://xn--é.com", "//x.com", "https://%65xample.com/", "https:///path", "https://a b.com", "mailto:a@b.c"} {
		if _, err := urlHost(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestHiddenWithoutArguments(t *testing.T) {
	ctx := context.Background()
	f := facts(`{}`)
	cases := []struct {
		name  string
		rules []Rule
		want  bool
	}{
		{"no rules", nil, false},
		{"caller-only deny matches", []Rule{deny("d", "caller.kind == 'user'")}, true},
		{"caller-only deny misses", []Rule{deny("d", "caller.kind == 'agent'")}, false},
		{"args deny cannot hide", []Rule{deny("d", "args.x == 1")}, false},
		{"time deny cannot hide", []Rule{deny("d", "request.time.getHours() > 3")}, false},
		{"decidable allow misses", []Rule{allow("a", "caller.kind == 'agent'")}, true},
		{"decidable allow matches", []Rule{allow("a", "caller.kind == 'user'")}, false},
		{"allow on args might match", []Rule{allow("a", "args.x == 1")}, false},
		{"mixed allows, one undecidable", []Rule{allow("a", "caller.kind == 'agent'"), allow("b", "args.x == 1")}, false},
		{"erroring decidable rule hides", []Rule{deny("d", "tool.annotations.read_only == true")}, true},
		{"unknown groups hide a group allow", []Rule{allow("a", "caller.groups_known && 'eng' in caller.groups")}, false},
	}
	for _, c := range cases {
		set := mustCompile(t, c.rules...)
		if got := set.Hidden(ctx, f); got != c.want {
			t.Errorf("%s: hidden=%v want %v", c.name, got, c.want)
		}
	}
	unknown := f
	unknown.Caller.GroupsKnown, unknown.Caller.Groups = false, nil
	if !mustCompile(t, allow("a", "caller.groups_known && 'eng' in caller.groups")).Hidden(ctx, unknown) {
		t.Error("allow requiring known groups should hide for an unknown-membership caller")
	}
}

func TestErrorMessageNeverContainsArguments(t *testing.T) {
	marker := "SECRET-MARKER-9f3a"
	set := mustCompile(t, deny("r", "args.n + 1 > 0"), deny("s", "args.s == 'x'"))
	d := set.Evaluate(context.Background(), facts(`{"n":"`+marker+`"}`))
	raw, _ := json.Marshal(d)
	if d.Allowed || strings.Contains(d.Message, marker) {
		t.Fatalf("%+v", d)
	}
	_ = raw
	if d.Class != ClassError {
		t.Fatalf("%+v", d)
	}
}
