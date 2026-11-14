package harness

import (
	"fmt"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

// Branchyard (a tool that runs several coding agents, each on its own git
// branch, behind a model gateway) takes its backends from the [models] tables
// of branchyard.toml. An AIPlex access point becomes a backend there: one per
// wire, marked control_plane = "aiplex", reached with a token Branchyard reads
// from the variable named in [secrets]. The tables written here are the ones
// in docs/branchyard.md.

// BranchyardFile is the default file name, read from the working directory.
const BranchyardFile = "branchyard.toml"

const (
	byWireAnthropic = "anthropic"
	byWireOpenAI    = "openai"
	bySecretName    = "aiplex"
	byControlPlane  = "aiplex"
)

// byProtocols maps the wire protocols an access point reports to the API
// Branchyard's gateway speaks. Anything else is not written.
var byProtocols = map[string]string{
	"anthropic-messages":      byWireAnthropic,
	"openai-chat-completions": byWireOpenAI,
	"openai-responses":        byWireOpenAI,
}

type byBackend struct{ name, api, url string }

type byRoute struct{ model, backend, api string }

// byTables is what the access point asks for: backends and routes in a fixed
// order, and what was left out.
type byTables struct {
	backends    []byBackend
	routes      []byRoute
	notWritten  []string
	accessPoint string
}

func (b byBackend) fields() map[string]any {
	return map[string]any{"api": b.api, "url": b.url, "key": bySecretName, "control_plane": byControlPlane}
}

func (r byRoute) fields() map[string]any {
	return map[string]any{"model": r.model, "backends": []any{r.backend}}
}

func (t *byTables) backend(name string) (byBackend, bool) {
	for _, b := range t.backends {
		if b.name == name {
			return b, true
		}
	}
	return byBackend{}, false
}

func branchyardTables(c *aiplex.AccessPointConnect, o Options) (*byTables, error) {
	if o.Model != "" {
		return nil, fmt.Errorf("--model does not apply to branchyard: its routes name every model the access point offers on each wire")
	}
	if o.TokenHelper {
		return nil, fmt.Errorf("--token-helper does not apply to branchyard: its [secrets] entry names a variable (or \"@path\" for a file); see the remaining steps")
	}
	if o.Telemetry != "" {
		return nil, fmt.Errorf("--with-telemetry does not apply to branchyard: it is not a client that exports telemetry to AIPlex")
	}
	base := c.BaseURLs["anthropic"]
	if base == "" {
		if u := c.BaseURLs["openai"]; u != "" {
			base = strings.TrimSuffix(strings.TrimRight(u, "/"), "/v1")
		}
	}
	base = strings.TrimRight(base, "/")
	if !strings.HasSuffix(base, "/ap/"+c.AccessPointID) {
		return nil, fmt.Errorf("access point %s reports no base URL ending in /ap/%s; see `aiplex connect %s`", c.AccessPointID, c.AccessPointID, c.AccessPointID)
	}
	t := &byTables{accessPoint: c.AccessPointID}
	have := map[string]bool{}
	skipped := map[string][]string{}
	for _, m := range c.Models {
		seen := map[string]bool{}
		for _, p := range m.Protocols {
			api, ok := byProtocols[p]
			if !ok {
				skipped[p] = append(skipped[p], m.Name)
				continue
			}
			if seen[api] {
				continue
			}
			seen[api] = true
			if strings.ContainsAny(m.Name, "*?") {
				return nil, fmt.Errorf("model %q contains a glob character, which a Branchyard route would read as a pattern; rename it in the access point", m.Name)
			}
			have[api] = true
			t.routes = append(t.routes, byRoute{model: m.Name, api: api, backend: "aiplex-" + c.AccessPointID + "-" + api})
		}
	}
	for _, api := range []string{byWireAnthropic, byWireOpenAI} {
		if have[api] {
			t.backends = append(t.backends, byBackend{name: "aiplex-" + c.AccessPointID + "-" + api, api: api, url: base})
		}
	}
	protocols := make([]string, 0, len(skipped))
	for p := range skipped {
		protocols = append(protocols, p)
	}
	sort.Strings(protocols)
	for _, p := range protocols {
		t.notWritten = append(t.notWritten, fmt.Sprintf("not written: %s (models: %s): Branchyard's gateway speaks Anthropic's and OpenAI's APIs", p, strings.Join(skipped[p], ", ")))
	}
	if len(t.backends) == 0 {
		return nil, fmt.Errorf("access point %s offers no model on a wire Branchyard's gateway speaks (Anthropic Messages, OpenAI Chat Completions, OpenAI Responses); nothing to write. %s", c.AccessPointID, strings.Join(t.notWritten, "; "))
	}
	return t, nil
}

func (b byBackend) text() string {
	return "[models.backends." + tomlKey(b.name) + "]\napi = " + tomlString(b.api) + "\nurl = " + tomlString(b.url) +
		"\nkey = " + tomlString(bySecretName) + "\ncontrol_plane = " + tomlString(byControlPlane) + "\n"
}

func (r byRoute) text() string {
	return "[[models.routes]]\nmodel = " + tomlString(r.model) + "\nbackends = [" + tomlString(r.backend) + "]\n"
}

const bySecretsText = "[secrets]\n" + bySecretName + " = \"" + TokenEnv + "\"\n"

// text is the whole fragment, as printed.
func (t *byTables) text() string {
	parts := []string{bySecretsText}
	for _, b := range t.backends {
		parts = append(parts, b.text())
	}
	for _, r := range t.routes {
		parts = append(parts, r.text())
	}
	return strings.Join(parts, "\n")
}

const branchyardGatewayStep = "put branches on the model gateway: `by run --model-gateway ...`, or set `[models] allow = [\"*\"]` in branchyard.toml (not written: it decides which branches use it)"

var (
	varName   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	upperName = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,63}$`)
)

// validSecretRef reports whether a [secrets] value is a reference as Branchyard
// reads it: a variable name, or "@" and a path.
func validSecretRef(v string) bool {
	if strings.HasPrefix(v, "@") {
		return len(v) > 1 && !strings.ContainsAny(v, "\x00\r\n")
	}
	return varName.MatchString(v)
}

// safeToPrintRef: a path or the conventional upper-case name. A name in any
// other form could be a token pasted by mistake, so it is never printed.
func safeToPrintRef(v string) bool {
	return strings.HasPrefix(v, "@") || upperName.MatchString(v)
}

// branchyardRemaining is what is left to do by hand. ref is what [secrets]
// aiplex holds: the variable name, or "@path" for a file.
func branchyardRemaining(ref string) []string {
	if ref == "" { // an existing reference that is not printed
		return []string{
			"put the token in the variable or file that [secrets] aiplex names (its value is not shown here), in the shell that runs `by`: the output of `aiplex auth print-token`. The token expires: write it again to refresh",
			branchyardGatewayStep,
		}
	}
	token := "export " + ref + "=\"$(aiplex auth print-token)\"   # in the shell that runs `by`; Branchyard reads the variable named in [secrets]. The token expires: run it again to refresh (Branchyard reads the key when each turn's gateway starts)"
	if strings.HasPrefix(ref, "@") {
		token = "put the token in that file: " + strings.TrimPrefix(ref, "@") + "   # [secrets] aiplex names it. Write the output of `aiplex auth print-token` there; the token expires, so write it again to refresh"
	}
	return []string{
		token,
		branchyardGatewayStep,
	}
}

func branchyardNotes(t *byTables) []string {
	notes := append([]string{}, t.notWritten...)
	notes = append(notes,
		"`control_plane` is read only by a Branchyard that knows the AIPlex integration; an older one refuses this file when it reads it. Check with `by config validate`",
		"every model the access point lists gets a route here. A call for any other model is offered to the plain backends of its API when your file has any, and to AIPlex backends only when it has none (Branchyard's rule for calls no route matches)")
	return notes
}

// BranchyardFragment returns the tables to add to branchyard.toml, with the
// notes and remaining steps, without touching any file.
func BranchyardFragment(c *aiplex.AccessPointConnect, o Options) (*Plan, error) {
	t, err := branchyardTables(c, o)
	if err != nil {
		return nil, err
	}
	return &Plan{Client: "branchyard", Print: t.text(), Notes: branchyardNotes(t), Remaining: branchyardRemaining(TokenEnv)}, nil
}

// PlanBranchyard plans the merge into branchyard.toml: ./branchyard.toml,
// which must exist, or Options.File, which is created when missing.
func PlanBranchyard(c *aiplex.AccessPointConnect, o Options) (*Plan, error) {
	if o.Home == "" {
		return nil, fmt.Errorf("cannot find your home directory")
	}
	if o.scope() != "project" {
		return nil, fmt.Errorf("branchyard has no user-scope file here: the default is %s in this directory; name another file with --file", BranchyardFile)
	}
	t, err := branchyardTables(c, o)
	if err != nil {
		return nil, err
	}
	p := &Plan{Client: "branchyard", Notes: branchyardNotes(t), Remaining: branchyardRemaining(TokenEnv)}
	path := o.File
	if path == "" {
		if o.Cwd == "" {
			return nil, fmt.Errorf("cannot find the working directory")
		}
		path = filepath.Join(o.Cwd, BranchyardFile)
	} else if !filepath.IsAbs(path) && o.Cwd != "" {
		path = filepath.Join(o.Cwd, path)
	}
	data, existed, mode, err := readFile(path)
	if err != nil {
		return nil, err
	}
	if !existed && o.File == "" {
		p.Print = t.text()
		p.Notes = append([]string{"no " + BranchyardFile + " in " + o.Cwd + ", so nothing was written (this may not be a Branchyard project): the tables above are what to add to your configuration, or pass --file PATH to create one"}, p.Notes...)
		return p, nil
	}
	m, err := mergeBranchyard(string(data), t)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if !existed {
		mode = 0o644
	}
	p.Notes = append(p.Notes, m.notes...)
	p.Preamble = m.preamble
	p.Remaining = branchyardRemaining(m.shownRef)
	p.Changes = append(p.Changes, Change{Path: path, Before: data, Existed: existed, After: []byte(m.text), Mode: mode})
	p.Notes = append(p.Notes, "branchyard.toml is edited as text: tables AIPlex does not write keep their comments and order; entries are added or replaced in place and never removed, so remove a stale route by hand")
	return p, nil
}

// ---- merge --------------------------------------------------------------

type lineEdit struct {
	start, end int // replace lines[start:end]
	text       []string
}

// headerPath parses a table header line into its dotted path.
func headerPath(line string) (path []string, array, ok bool) {
	s := strings.TrimSpace(line)
	if !strings.HasPrefix(s, "[") {
		return nil, false, false
	}
	s = s[1:]
	if strings.HasPrefix(s, "[") {
		array, s = true, s[1:]
	}
	for {
		s = strings.TrimLeft(s, " \t")
		var part string
		switch {
		case s == "":
			return nil, false, false
		case s[0] == '"' || s[0] == '\'':
			q := s[0]
			i := 1
			for i < len(s) && s[i] != q {
				if q == '"' && s[i] == '\\' {
					i++
				}
				i++
			}
			if i >= len(s) {
				return nil, false, false
			}
			part, s = s[1:i], s[i+1:]
		default:
			i := 0
			for i < len(s) && (s[i] == '-' || s[i] == '_' || s[i] >= '0' && s[i] <= '9' || s[i] >= 'a' && s[i] <= 'z' || s[i] >= 'A' && s[i] <= 'Z') {
				i++
			}
			if i == 0 {
				return nil, false, false
			}
			part, s = s[:i], s[i:]
		}
		path = append(path, part)
		s = strings.TrimLeft(s, " \t")
		switch {
		case strings.HasPrefix(s, "."):
			s = s[1:]
		case array && strings.HasPrefix(s, "]]"):
			return path, true, rest(s[2:])
		case !array && strings.HasPrefix(s, "]"):
			return path, false, rest(s[1:])
		default:
			return nil, false, false
		}
	}
}

func rest(s string) bool {
	s = strings.TrimSpace(s)
	return s == "" || strings.HasPrefix(s, "#")
}

type header struct {
	line  int
	path  []string
	array bool
}

func headers(lines []string) []header {
	var out []header
	for i, l := range lines {
		if !tableHeader.MatchString(l) {
			continue
		}
		if p, arr, ok := headerPath(l); ok {
			out = append(out, header{i, p, arr})
		}
	}
	return out
}

// span is the lines of the table whose header is hs[k], without the blank and
// comment lines that precede the next header (they belong to it).
func span(lines []string, hs []header, k int) (start, end int) {
	start = hs[k].line
	end = len(lines)
	if k+1 < len(hs) {
		end = hs[k+1].line
	}
	for end > start+1 {
		t := strings.TrimSpace(lines[end-1])
		if t != "" && !strings.HasPrefix(t, "#") {
			break
		}
		end--
	}
	return start, end
}

func samePath(a, b []string) bool { return reflect.DeepEqual(a, b) }

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func strs(v any) []string {
	var out []string
	l, _ := v.([]any)
	for _, e := range l {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// globMatch mirrors glob_match in Branchyard's
// crates/branchyard-provision/src/connectors.rs, which route models are
// matched with: `*` matches any run of characters (dots included), `?` any one
// character, and everything else matches itself.
func globMatch(glob, text string) bool {
	g, t := glob, text
	gi, ti := 0, 0
	starG, starT := -1, 0
	for ti < len(t) {
		switch {
		case gi < len(g) && g[gi] == '*':
			starG, starT = gi, ti
			gi++
		case gi < len(g) && (g[gi] == t[ti] || g[gi] == '?'):
			gi++
			ti++
		case starG >= 0:
			gi = starG + 1
			starT++
			ti = starT
		default:
			return false
		}
	}
	return strings.Trim(g[gi:], "*") == ""
}

// merged is the result of a merge.
type merged struct {
	text      string
	notes     []string // after the write
	preamble  []string // before the diff: what the person should know to confirm it
	secretRef string   // what [secrets] aiplex holds: the variable name or "@path"; never printed as such
	shownRef  string   // secretRef when it may be printed, else ""
}

func quoteList(l []string) string {
	q := make([]string, len(l))
	for i, s := range l {
		q[i] = tomlString(s)
	}
	return "[" + strings.Join(q, ", ") + "]"
}

// mergeBranchyard edits the file's text. What it can and cannot edit:
//   - tables in header form ([secrets], [models.backends.NAME],
//     [[models.routes]]) are found, replaced in place or inserted into;
//   - new tables are appended;
//   - dotted keys (models.backends.x.api = ...) are left alone and merged
//     around, as long as none of them defines an entry this command writes;
//   - an entry this command writes that exists only as dotted keys or an
//     inline table, or an inline `routes = [...]` array, cannot be extended:
//     the result would not parse and the command refuses.
//
// The result is parsed and compared with the original plus the intended
// entries, so no other change can get through.
func mergeBranchyard(before string, t *byTables) (*merged, error) {
	orig, err := parseTOML(before)
	if err != nil {
		return nil, fmt.Errorf("the existing file is not valid TOML (%v); fix it first, nothing was changed", err)
	}
	models := asMap(orig["models"])
	if orig["models"] != nil && models == nil {
		return nil, fmt.Errorf("`models` is not a table; fix it first, nothing was changed")
	}
	userBackends := asMap(models["backends"])
	userRoutes, _ := models["routes"].([]any)
	eol := "\n"
	if i := strings.Index(before, "\n"); i > 0 && before[i-1] == '\r' {
		eol = "\r\n"
	}
	lines := strings.Split(before, "\n")
	hs := headers(lines)
	// text lines to insert, in the file's line ending
	ins := func(text string) []string {
		l := strings.Split(strings.TrimRight(text, "\n"), "\n")
		if eol == "\r\n" {
			for i := range l {
				l[i] += "\r"
			}
		}
		return l
	}

	var routeHeaders []int // index into hs of each [[models.routes]], in order
	for k, h := range hs {
		if h.array && samePath(h.path, []string{"models", "routes"}) {
			routeHeaders = append(routeHeaders, k)
		}
	}

	isAIPlex := func(name string) bool {
		if _, ours := t.backend(name); ours {
			return true
		}
		return asMap(userBackends[name])["control_plane"] == byControlPlane
	}
	apiOf := func(name string) string {
		if b, ours := t.backend(name); ours {
			return b.api
		}
		s, _ := asMap(userBackends[name])["api"].(string)
		return s
	}
	routeName := func(i int) string {
		what := fmt.Sprintf("models.routes[%d]", i)
		if i < len(routeHeaders) {
			what += fmt.Sprintf(" at line %d", hs[routeHeaders[i]].line+1)
		}
		return what
	}
	namesOf := func(r map[string]any) []string {
		return append(strs(r["backends"]), strs(r["fallbacks"])...)
	}

	var edits []lineEdit
	res := &merged{secretRef: TokenEnv, shownRef: TokenEnv}
	replaced := map[int]byRoute{} // user route index -> ours

	for i, ur := range userRoutes {
		r := asMap(ur)
		model, _ := r["model"].(string)
		names := namesOf(r)
		var air, plain []string
		for _, n := range names {
			if isAIPlex(n) {
				air = append(air, n)
			} else {
				plain = append(plain, n)
			}
		}
		if len(air) > 0 && len(plain) > 0 {
			return nil, fmt.Errorf("%s (model %q) names an AIPlex backend (%s) and a backend that is not one (%s). Branchyard refuses such a route, because an outage or a refusal of AIPlex would be bypassed by the direct provider. Remove or rename that route (or give the plain backend a route of its own), then run this again. Nothing was changed",
				routeName(i), model, strings.Join(air, ", "), strings.Join(plain, ", "))
		}
		if len(plain) > 0 {
			for _, ours := range t.routes {
				if globMatch(model, ours.model) {
					return nil, fmt.Errorf("%s (model %q, backends = %s) names a backend that is not an AIPlex backend and matches %q, which this would route to AIPlex. A route that names an AIPlex backend may not be mixed with a plain one, and Branchyard takes the first route that matches, so this route would send %q straight to its provider; the new routes are added after it. Two ways forward: move that route below the new ones by hand, or run `aiplex connect %s --client branchyard` without --write and paste the printed tables above it. Nothing was changed",
						routeName(i), model, quoteList(strs(r["backends"])), ours.model, ours.model, t.accessPoint)
				}
			}
		}
	}
	satisfied := map[int]bool{} // index into t.routes
	for j, ours := range t.routes {
		for i, ur := range userRoutes {
			r := asMap(ur)
			if r["model"] != ours.model {
				continue
			}
			names := namesOf(r)
			includesOurs, same := false, len(names) > 0
			for _, n := range names {
				includesOurs = includesOurs || n == ours.backend
				same = same && isAIPlex(n) && apiOf(n) == ours.api
			}
			// A route that names our backend is right whatever else it
			// carries (other AIPlex backends as failover, weights, a rate
			// limit, comments). A mix with a plain backend was refused above.
			if includesOurs {
				satisfied[j] = true
				break
			}
			if !same {
				continue
			}
			satisfied[j] = true
			{
				if _, done := replaced[i]; !done && i < len(routeHeaders) {
					replaced[i] = ours
					res.notes = append(res.notes, fmt.Sprintf("replaced your %s (model %q) with the AIPlex route; see the diff", routeName(i), ours.model))
				}
			}
			break
		}
	}
	// A new route that an earlier AIPlex route already takes the model for is
	// never used. That cannot bypass AIPlex, so it is a note, not a refusal.
	for j, ours := range t.routes {
		if satisfied[j] {
			continue
		}
		for i, ur := range userRoutes {
			r := asMap(ur)
			if _, gone := replaced[i]; gone {
				continue
			}
			if m, _ := r["model"].(string); !globMatch(m, ours.model) {
				continue
			}
			applies := r["backend_model"] != nil
			for _, n := range namesOf(r) {
				applies = applies || apiOf(n) == ours.api
			}
			if applies {
				res.notes = append(res.notes, fmt.Sprintf("the new route for %q (to %s) will not take effect: your %s (model %q) comes first and matches it; move the new route above it", ours.model, ours.backend, routeName(i), r["model"]))
				break
			}
		}
	}

	// [secrets]
	secrets := asMap(orig["secrets"])
	if orig["secrets"] != nil && secrets == nil {
		return nil, fmt.Errorf("`secrets` is not a table; fix it first, nothing was changed")
	}
	var appended []string
	secretsHeader := -1
	for k, h := range hs {
		if !h.array && samePath(h.path, []string{"secrets"}) {
			secretsHeader = k
		}
	}
	cur, has := secrets[bySecretName]
	switch {
	case has:
		ref, _ := cur.(string)
		if !validSecretRef(ref) {
			// The value is never printed: it may be a token pasted by mistake.
			return nil, fmt.Errorf("[secrets] %s holds neither a variable name nor an @path, which is all Branchyard reads there. If it is a token, remove it from the file now (it is stored in clear) and replace it with the name of a variable or an @path to a file that holds it. Nothing was changed", bySecretName)
		}
		// The person's own reference stays exactly as it is.
		res.secretRef = ref
		if safeToPrintRef(ref) {
			res.shownRef = ref
			if ref != TokenEnv {
				res.preamble = append(res.preamble, fmt.Sprintf("your [secrets] already has %s = %s; it is kept as it is and the AIPlex backends use it, so it must name where your AIPlex token is", bySecretName, tomlString(ref)))
			}
		} else {
			res.preamble = append(res.preamble, "your [secrets] already has an aiplex entry; it is kept as it is and the AIPlex backends use it (its value is not shown, because a value shaped like a variable name could be a token pasted by mistake)")
		}
	case secretsHeader >= 0:
		_, end := span(lines, hs, secretsHeader)
		_ = end
		edits = append(edits, lineEdit{hs[secretsHeader].line + 1, hs[secretsHeader].line + 1, ins(bySecretName + " = " + tomlString(TokenEnv))})
	case orig["secrets"] != nil:
		return nil, fmt.Errorf("`secrets` is defined in a form this command cannot extend (an inline table or dotted keys); write it as a [secrets] table, nothing was changed")
	default:
		appended = append(appended, bySecretsText)
	}

	// backends
	writtenBackends := map[string]bool{}
	for _, b := range t.backends {
		cur, has := userBackends[b.name]
		if m := asMap(cur); has && m != nil {
			right := true
			for k, v := range b.fields() {
				right = right && m[k] == v
			}
			if right { // already as it would be written: keep it, comments and extra keys too
				continue
			}
		}
		writtenBackends[b.name] = true
		hk := -1
		for k, h := range hs {
			if !h.array && samePath(h.path, []string{"models", "backends", b.name}) {
				hk = k
			}
		}
		switch {
		case has && hk >= 0:
			start, end := span(lines, hs, hk)
			edits = append(edits, lineEdit{start, end, ins(b.text())})
			res.notes = append(res.notes, fmt.Sprintf("replaced your [models.backends.%s] with the AIPlex values; see the diff", b.name))
		case has:
			return nil, fmt.Errorf("backend %q is defined in a form this command cannot edit (an inline table or dotted keys); write it as a [models.backends.%s] table or remove it, nothing was changed", b.name, b.name)
		default:
			appended = append(appended, b.text())
		}
	}

	// routes
	for i, ours := range replaced {
		start, end := span(lines, hs, routeHeaders[i])
		edits = append(edits, lineEdit{start, end, ins(ours.text())})
	}
	for j, r := range t.routes {
		if !satisfied[j] {
			appended = append(appended, r.text())
		}
	}

	// Apply from the bottom up. At the same line a replacement goes before an
	// insertion, so the inserted line ends up above the replaced table.
	sort.SliceStable(edits, func(a, b int) bool {
		if edits[a].start != edits[b].start {
			return edits[a].start > edits[b].start
		}
		return edits[a].end > edits[a].start && edits[b].end == edits[b].start
	})
	for _, e := range edits {
		if e.end == len(lines) && len(e.text) > 0 && eol == "\r\n" {
			// The file has no final newline: do not give the last line a lone CR.
			e.text = append([]string{}, e.text...)
			e.text[len(e.text)-1] = strings.TrimSuffix(e.text[len(e.text)-1], "\r")
		}
		lines = append(lines[:e.start:e.start], append(append([]string{}, e.text...), lines[e.end:]...)...)
	}
	after := strings.Join(lines, "\n")
	if len(appended) > 0 {
		if after != "" && !strings.HasSuffix(after, "\n") {
			after += eol
		}
		if strings.TrimSpace(after) != "" {
			after += eol
		}
		after += strings.ReplaceAll(strings.Join(appended, "\n"), "\n", eol)
	}

	got, err := parseTOML(after)
	if err != nil {
		return nil, fmt.Errorf("the merged file would not be valid TOML (%v). It probably defines [models], its backends or its routes in a form this command cannot extend (an inline table, dotted keys that define an entry written here, or an inline `routes = [...]` array); write them as tables or add the tables by hand (`aiplex connect <access-point> --client branchyard` prints them). Nothing was changed", err)
	}
	want, _ := parseTOML(before)
	ws := asMap(want["secrets"])
	if ws == nil {
		ws = map[string]any{}
		want["secrets"] = ws
	}
	ws[bySecretName] = res.secretRef
	wm := asMap(want["models"])
	if wm == nil {
		wm = map[string]any{}
		want["models"] = wm
	}
	wb := asMap(wm["backends"])
	if wb == nil {
		wb = map[string]any{}
		wm["backends"] = wb
	}
	for _, b := range t.backends {
		if writtenBackends[b.name] {
			wb[b.name] = b.fields()
		}
	}
	wr, _ := wm["routes"].([]any)
	for i, ours := range replaced {
		wr[i] = ours.fields()
	}
	for j, r := range t.routes {
		if !satisfied[j] {
			wr = append(wr, r.fields())
		}
	}
	if wr != nil {
		wm["routes"] = wr
	}
	if !reflect.DeepEqual(want, got) {
		return nil, fmt.Errorf("refusing to write: the merge would change settings other than the AIPlex tables (the file probably has a layout this command cannot edit safely); add the tables by hand. Nothing was changed")
	}
	sort.Strings(res.notes)
	res.text = after
	return res, nil
}
