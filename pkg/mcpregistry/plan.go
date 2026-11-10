package mcpregistry

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Provenance records where an imported connection came from. It is
// informational: nothing in authorization or dispatch reads it.
type Provenance struct {
	// Registry is the registry base URL the server.json was fetched from.
	Registry string `json:"registry"`
	Name     string `json:"name"`
	Version  string `json:"version"`
	// RetrievedAt is when the CLI fetched it (RFC 3339, UTC).
	RetrievedAt string `json:"retrieved_at"`
	// SHA256 is the hex SHA-256 of the server object exactly as the registry
	// served it.
	SHA256 string `json:"sha256"`
}

var sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Validate checks a provenance block saved on a connection.
func (p Provenance) Validate() error {
	if _, err := ValidateBase(p.Registry); err != nil {
		return fmt.Errorf("provenance.registry: %w", err)
	}
	if !serverName.MatchString(p.Name) || len(p.Name) > 200 {
		return fmt.Errorf("provenance.name must be a registry server name")
	}
	if !versionText.MatchString(p.Version) {
		return fmt.Errorf("provenance.version must be a plain version string")
	}
	if _, err := time.Parse(time.RFC3339, p.RetrievedAt); err != nil {
		return fmt.Errorf("provenance.retrieved_at must be an RFC 3339 time")
	}
	if !sha256Hex.MatchString(p.SHA256) {
		return fmt.Errorf("provenance.sha256 must be 64 lower-case hex digits")
	}
	return nil
}

// Provenance builds the provenance block for an entry fetched from registry.
func (e Entry) Provenance(registry string) Provenance {
	sum := sha256.Sum256(e.Raw)
	return Provenance{Registry: strings.TrimSuffix(registry, "/"), Name: e.Server.Name, Version: e.Server.Version, RetrievedAt: e.RetrievedAt.UTC().Format(time.RFC3339), SHA256: hex.EncodeToString(sum[:])}
}

// Auth is a proposed tool connection auth block. It never holds a secret
// value: SecretRef is a reference name the operator creates.
type Auth struct {
	Type      string `json:"type"`
	In        string `json:"in,omitempty"`
	Name      string `json:"name,omitempty"`
	Prefix    string `json:"prefix,omitempty"`
	SecretRef string `json:"secret_ref,omitempty"`
}

// SecretNeed is a secret the operator must create before the connection works.
type SecretNeed struct {
	// Ref is the proposed reference name.
	Ref string `json:"ref"`
	// Purpose says where the value is sent.
	Purpose string `json:"purpose"`
	// Description is the registry's (sanitized) description of the value.
	Description string `json:"description,omitempty"`
}

// Variable is a URL template variable and how it was resolved.
type Variable struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Required    bool     `json:"required"`
	Choices     []string `json:"choices,omitempty"`
	// Source is fixed (set by the registry), flag (--var), default or missing.
	Source string `json:"source"`
	Value  string `json:"value,omitempty"`
}

// Plan is a proposed tool connection derived from one remote. It is a
// proposal: nothing is created, approved or trusted by computing it.
type Plan struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	RemoteIdx int    `json:"remote"`
	// Template is the registry's URL; URL the resolved, pinned endpoint.
	Template  string       `json:"url_template"`
	URL       string       `json:"url"`
	Origin    string       `json:"origin"`
	Variables []Variable   `json:"variables,omitempty"`
	Auth      Auth         `json:"auth"`
	Secrets   []SecretNeed `json:"secrets,omitempty"`
	// Unmapped lists what the entry declares that an AIPlex connection cannot
	// carry, so nothing is silently dropped.
	Unmapped []string `json:"unmapped,omitempty"`
	// Notes are assumptions the operator should review.
	Notes []string `json:"notes,omitempty"`
}

// PlanOptions steer Plan.
type PlanOptions struct {
	// Remote selects a remote by index; -1 selects the first importable one.
	Remote int
	// Vars supplies URL template variables.
	Vars map[string]string
	// ConnectionID seeds proposed secret reference names.
	ConnectionID string
	// NoAuth drops every declared credential (the operator asserts the
	// server needs none).
	NoAuth bool
}

var placeholder = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)\}`)

var dnsHost = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)

// NewPlan maps a remote of the entry to a proposed connection.
func NewPlan(entry Entry, opts PlanOptions) (*Plan, error) {
	classes := entry.Server.Classify()
	if len(classes.Remotes) == 0 {
		return nil, fmt.Errorf("%s@%s is %s: the gateway connects to remote Streamable HTTP servers only", entry.Server.Name, entry.Server.Version, classes.Label())
	}
	idx := opts.Remote
	if idx < 0 {
		idx = classes.Remotes[0]
	}
	if idx >= len(entry.Server.Remotes) || entry.Server.Remotes[idx].Type != "streamable-http" {
		return nil, fmt.Errorf("remote %d is not an importable streamable-http remote (importable: %v)", idx, classes.Remotes)
	}
	remote := entry.Server.Remotes[idx]
	plan := &Plan{Name: entry.Server.Name, Version: entry.Server.Version, RemoteIdx: idx, Template: remote.URL}

	resolved, query, vars, err := resolveURL(remote, opts.Vars)
	plan.Variables = vars
	if err != nil {
		return plan, err
	}
	if plan.Origin, err = checkEndpoint(resolved); err != nil {
		return plan, err
	}
	plan.URL = resolved

	if len(query) > 0 {
		// A secret URL variable that is a whole query value becomes an api_key
		// in the query; the endpoint itself is pinned without it.
		if opts.NoAuth {
			return plan, fmt.Errorf("the URL needs a secret query parameter (%s), so --auth none cannot be used", query[0].param)
		}
		if len(query) > 1 {
			return plan, fmt.Errorf("the URL needs %d secret query parameters; a connection carries one credential", len(query))
		}
		q := query[0]
		plan.Auth = Auth{Type: "api_key", In: "query", Name: q.param, SecretRef: proposeRef(opts.ConnectionID, q.variable)}
		plan.Secrets = append(plan.Secrets, SecretNeed{Ref: plan.Auth.SecretRef, Purpose: "sent as the query parameter " + q.param, Description: Safe(q.description, 200)})
	}
	if u, perr := url.Parse(plan.URL); perr == nil && u.RawQuery != "" {
		return plan, fmt.Errorf("the endpoint keeps a query string after resolving variables; AIPlex pins tool endpoints without one")
	}

	if !opts.NoAuth {
		if err := mapHeaders(plan, remote, opts.ConnectionID); err != nil {
			return plan, err
		}
	} else if len(remote.Headers) > 0 {
		plan.Notes = append(plan.Notes, "--auth none: the "+fmt.Sprint(len(remote.Headers))+" header(s) the registry declares are not sent.")
	}
	if plan.Auth.Type == "" {
		plan.Auth = Auth{Type: "none"}
	}
	return plan, nil
}

type secretQuery struct{ param, variable, description string }

// resolveURL substitutes template variables in a context-aware way. Secret
// variables are allowed only as a whole query value and are returned
// separately; the URL then carries no placeholder for them.
func resolveURL(r Remote, supplied map[string]string) (string, []secretQuery, []Variable, error) {
	tpl := r.URL
	names := map[string]bool{}
	for _, m := range placeholder.FindAllStringSubmatch(tpl, -1) {
		names[m[1]] = true
	}
	var ordered []string
	for n := range names {
		ordered = append(ordered, n)
	}
	sort.Strings(ordered)
	var vars []Variable
	values := map[string]string{}
	var missing, unknown []string
	var secrets []secretQuery
	for _, name := range ordered {
		in, declared := r.Variables[name]
		v := Variable{Name: name, Description: Safe(in.Description, 200), Required: in.IsRequired, Choices: in.Choices}
		if !declared {
			unknown = append(unknown, name)
			v.Source = "undeclared"
			vars = append(vars, v)
			continue
		}
		if in.IsSecret {
			param, ok := queryParamFor(tpl, name)
			if !ok {
				return "", nil, vars, fmt.Errorf("secret variable {%s} is not the whole value of a query parameter; secrets in the host or path are not importable", name)
			}
			secrets = append(secrets, secretQuery{param: param, variable: name, description: in.Description})
			v.Source = "secret"
			vars = append(vars, v)
			continue
		}
		switch {
		case in.Value != "":
			v.Source, v.Value = "fixed", in.Value
		case supplied[name] != "":
			v.Source, v.Value = "flag", supplied[name]
		case in.Default != "":
			v.Source, v.Value = "default", in.Default
		default:
			v.Source = "missing"
			missing = append(missing, name)
		}
		if v.Value != "" {
			if len(in.Choices) > 0 && !contains(in.Choices, v.Value) {
				return "", nil, vars, fmt.Errorf("variable {%s} must be one of %s", name, strings.Join(in.Choices, ", "))
			}
			values[name] = v.Value
		}
		vars = append(vars, v)
	}
	for name := range supplied {
		if !names[name] {
			return "", nil, vars, fmt.Errorf("--var %s: the URL template has no variable {%s}", name, name)
		}
	}
	if len(unknown) > 0 {
		return "", nil, vars, fmt.Errorf("the URL uses undeclared variables: {%s}", strings.Join(unknown, "}, {"))
	}
	if len(missing) > 0 {
		return "", nil, vars, &MissingVariablesError{Names: missing}
	}
	// Secret query parameters are removed from the pinned URL.
	for _, s := range secrets {
		tpl = removeQueryParam(tpl, s.param)
	}
	out, err := substitute(tpl, values)
	return out, secrets, vars, err
}

// MissingVariablesError lists required URL variables the operator must supply.
type MissingVariablesError struct{ Names []string }

func (e *MissingVariablesError) Error() string {
	return "the URL needs values for: " + strings.Join(e.Names, ", ") + " (pass --var name=value)"
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// queryParamFor returns the parameter name when {variable} is exactly the
// value of one query parameter of the template (and appears nowhere else).
func queryParamFor(tpl, variable string) (string, bool) {
	if strings.Count(tpl, "{"+variable+"}") != 1 {
		return "", false
	}
	_, query, found := strings.Cut(tpl, "?")
	if !found {
		return "", false
	}
	query, _, _ = strings.Cut(query, "#")
	for _, pair := range strings.Split(query, "&") {
		name, value, ok := strings.Cut(pair, "=")
		if ok && value == "{"+variable+"}" && name != "" && !strings.ContainsAny(name, "{}%") {
			return name, true
		}
	}
	return "", false
}

func removeQueryParam(tpl, param string) string {
	base, query, found := strings.Cut(tpl, "?")
	if !found {
		return tpl
	}
	query, frag, hasFrag := strings.Cut(query, "#")
	var kept []string
	for _, pair := range strings.Split(query, "&") {
		name, _, _ := strings.Cut(pair, "=")
		if name != param {
			kept = append(kept, pair)
		}
	}
	out := base
	if len(kept) > 0 {
		out += "?" + strings.Join(kept, "&")
	}
	if hasFrag {
		out += "#" + frag
	}
	return out
}

// substitute fills {name} placeholders, validating each value for the part of
// the URL it lands in.
func substitute(tpl string, values map[string]string) (string, error) {
	schemeEnd := strings.Index(tpl, "://")
	if schemeEnd < 0 {
		return "", fmt.Errorf("the URL template has no scheme")
	}
	authStart := schemeEnd + 3
	authEnd := len(tpl)
	if i := strings.IndexAny(tpl[authStart:], "/?#"); i >= 0 {
		authEnd = authStart + i
	}
	pathEnd := len(tpl)
	if i := strings.IndexAny(tpl[authEnd:], "?#"); i >= 0 {
		pathEnd = authEnd + i
	}
	if strings.Contains(tpl[authStart:authEnd], "@") {
		return "", fmt.Errorf("the URL carries credentials")
	}
	var b strings.Builder
	last := 0
	var err error
	for _, loc := range placeholder.FindAllStringSubmatchIndex(tpl, -1) {
		name := tpl[loc[2]:loc[3]]
		value := values[name]
		var rendered string
		switch pos := loc[0]; {
		case pos < authStart:
			return "", fmt.Errorf("variable {%s} is in the URL scheme", name)
		case pos < authEnd:
			if !dnsHost.MatchString(value) || strings.Contains(value, "..") {
				return "", fmt.Errorf("variable {%s} must be a host name part (letters, digits, dots, hyphens)", name)
			}
			rendered = value
		case pos < pathEnd:
			if value == "." || value == ".." || strings.ContainsAny(value, "/\\") || len(value) > 256 {
				return "", fmt.Errorf("variable {%s} must be a single path segment", name)
			}
			rendered = url.PathEscape(value)
		default:
			if len(value) > 512 {
				return "", fmt.Errorf("variable {%s} is too long", name)
			}
			rendered = url.QueryEscape(value)
		}
		if !cleanText(value) {
			return "", fmt.Errorf("variable {%s} contains control characters", name)
		}
		b.WriteString(tpl[last:loc[0]])
		b.WriteString(rendered)
		last = loc[1]
	}
	b.WriteString(tpl[last:])
	return b.String(), err
}

// checkEndpoint validates the resolved URL and returns its origin.
func checkEndpoint(raw string) (string, error) {
	if len(raw) > maxURL || strings.ContainsAny(raw, " \t\r\n\\\x00") {
		return "", fmt.Errorf("the resolved URL contains whitespace or is too long")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Opaque != "" || u.Fragment != "" {
		return "", fmt.Errorf("the resolved URL is not a plain URL")
	}
	if u.User != nil {
		return "", fmt.Errorf("the URL carries credentials; they are never imported")
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !IsLoopbackHost(u.Hostname()) {
			return "", fmt.Errorf("the URL uses plain http; only https (or a loopback host) is importable")
		}
	default:
		return "", fmt.Errorf("the URL scheme %q is not importable", Safe(u.Scheme, 20))
	}
	if strings.Contains(u.Host, "%") || u.Hostname() == "" {
		return "", fmt.Errorf("the URL host is not usable")
	}
	return u.Scheme + "://" + u.Host, nil
}

var refChars = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

func proposeRef(connectionID, what string) string {
	ref := strings.Trim(refChars.ReplaceAllString(connectionID+"-"+what, "-"), "-_")
	if ref == "" {
		ref = "mcp-credential"
	}
	if len(ref) > 100 {
		ref = ref[:100]
	}
	return ref
}

// mapHeaders proposes the single credential an AIPlex connection can carry
// from the declared headers, and lists the rest as unmapped.
func mapHeaders(plan *Plan, r Remote, connectionID string) error {
	type candidate struct {
		h        Header
		prefix   string
		variable string
		secret   bool
		required bool
	}
	var cands []candidate
	for _, h := range r.Headers {
		name := Safe(h.Name, 64)
		matches := placeholder.FindAllStringSubmatch(h.Value, -1)
		secretish := h.IsSecret
		for _, m := range matches {
			if in, ok := h.Variables[m[1]]; ok && in.IsSecret {
				secretish = true
			}
		}
		switch {
		case h.Value == "" && !secretish:
			plan.Unmapped = append(plan.Unmapped, fmt.Sprintf("header %s: a value the user supplies that is not marked secret; AIPlex connections send one credential header only", name))
		case h.Value != "" && len(matches) == 0:
			// A literal value. It is never imported: if it were a secret it
			// would be in registry content; if not, a connection has no place
			// for static headers.
			plan.Unmapped = append(plan.Unmapped, fmt.Sprintf("header %s: fixed value declared by the registry; AIPlex connections cannot send static headers, and the value is not imported", name))
		case len(matches) > 1:
			plan.Unmapped = append(plan.Unmapped, fmt.Sprintf("header %s: its value combines several variables", name))
		case !secretish:
			plan.Unmapped = append(plan.Unmapped, fmt.Sprintf("header %s: a value the user supplies that is not marked secret; AIPlex connections send one credential header only", name))
		case plan.Auth.Type != "":
			plan.Unmapped = append(plan.Unmapped, fmt.Sprintf("header %s: a secret header; the connection already carries the secret query parameter", name))
		default:
			c := candidate{h: h, secret: true, required: h.IsRequired}
			if len(matches) == 1 {
				token := matches[0][0]
				prefix, suffix, _ := strings.Cut(h.Value, token)
				if suffix != "" {
					plan.Unmapped = append(plan.Unmapped, fmt.Sprintf("header %s: its value has text after the secret", name))
					continue
				}
				c.prefix, c.variable = strings.TrimSpace(prefix), matches[0][1]
			}
			cands = append(cands, c)
		}
	}
	if len(cands) == 0 {
		return nil
	}
	pick := 0
	for i, c := range cands {
		if c.required {
			pick = i
			break
		}
	}
	for i, c := range cands {
		if i != pick {
			plan.Unmapped = append(plan.Unmapped, fmt.Sprintf("header %s: a connection carries one credential; %s was chosen", Safe(c.h.Name, 64), Safe(cands[pick].h.Name, 64)))
		}
	}
	c := cands[pick]
	desc := c.h.Description
	if c.variable != "" {
		if in, ok := c.h.Variables[c.variable]; ok && in.Description != "" {
			desc = in.Description
		}
	}
	if c.prefix != "" && !prefixToken.MatchString(c.prefix) {
		return fmt.Errorf("header %s: the scheme %q cannot be represented", Safe(c.h.Name, 64), Safe(c.prefix, 40))
	}
	what := "token"
	switch {
	case strings.EqualFold(c.h.Name, "authorization") && (strings.EqualFold(c.prefix, "bearer") || c.prefix == ""):
		plan.Auth = Auth{Type: "bearer"}
		if c.prefix == "" {
			plan.Notes = append(plan.Notes, "Authorization is marked secret but the registry declares no scheme; Bearer is assumed. Check the server's documentation.")
		}
	default:
		plan.Auth = Auth{Type: "api_key", In: "header", Name: c.h.Name, Prefix: c.prefix}
		what = "key"
	}
	plan.Auth.SecretRef = proposeRef(connectionID, what)
	plan.Secrets = append(plan.Secrets, SecretNeed{Ref: plan.Auth.SecretRef, Purpose: "sent in the " + Safe(c.h.Name, 64) + " header", Description: Safe(desc, 300)})
	return nil
}

var prefixToken = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,32}$`)
