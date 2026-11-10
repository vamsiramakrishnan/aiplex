// Package mcpregistry reads the official MCP Registry (and registries that
// implement its API) and turns a server.json "remote" entry into a proposed
// AIPlex tool connection.
//
// Registry content is untrusted input. Everything here validates strictly,
// bounds sizes, never follows a redirect to another host and never turns a
// registry-supplied string into a secret value: the only credential the
// package produces is a reference the operator must supply.
//
// The fetch happens in the caller (the CLI); the AIPlex API never contacts a
// registry.
package mcpregistry

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	// DefaultRegistry is the official registry.
	DefaultRegistry = "https://registry.modelcontextprotocol.io"
	// APIPrefix is the API version this package speaks. The registry also
	// serves the same endpoints under /v0.
	APIPrefix = "/v0.1"

	officialMeta = "io.modelcontextprotocol.registry/official"
)

// SupportedSchemas are the server.json schema dates this package understands.
// The remote and header members it reads are the same in all of them; an
// unknown date is refused rather than guessed at.
var SupportedSchemas = []string{"2025-07-09", "2025-09-16", "2025-09-29", "2025-10-17", "2025-12-11"}

var schemaURL = regexp.MustCompile(`^https://static\.modelcontextprotocol\.io/(?:mcp/registry/)?schemas/(\d{4}-\d{2}-\d{2})/server\.schema\.json$`)

// Input is a registry "Input": a header, variable or argument value.
type Input struct {
	Description string   `json:"description,omitempty"`
	Value       string   `json:"value,omitempty"`
	Default     string   `json:"default,omitempty"`
	Placeholder string   `json:"placeholder,omitempty"`
	Format      string   `json:"format,omitempty"`
	Choices     []string `json:"choices,omitempty"`
	IsRequired  bool     `json:"isRequired,omitempty"`
	IsSecret    bool     `json:"isSecret,omitempty"`
}

// Header is a remote's KeyValueInput.
type Header struct {
	Input
	Name      string           `json:"name"`
	Variables map[string]Input `json:"variables,omitempty"`
}

// Remote is a RemoteTransport: a server the registry says is reachable over
// HTTP.
type Remote struct {
	Type      string           `json:"type"`
	URL       string           `json:"url"`
	Headers   []Header         `json:"headers,omitempty"`
	Variables map[string]Input `json:"variables,omitempty"`
}

// Package is a locally installed server (npm, pypi, oci, nuget, mcpb). The
// gateway cannot run one; it is listed and never imported.
type Package struct {
	RegistryType string `json:"registryType"`
	Identifier   string `json:"identifier"`
	Version      string `json:"version,omitempty"`
	Transport    struct {
		Type string `json:"type"`
	} `json:"transport"`
}

// Repository is the server's source repository.
type Repository struct {
	URL    string `json:"url,omitempty"`
	Source string `json:"source,omitempty"`
}

// Server is the server.json document (ServerDetail).
type Server struct {
	Schema      string      `json:"$schema"`
	Name        string      `json:"name"`
	Title       string      `json:"title,omitempty"`
	Description string      `json:"description"`
	Version     string      `json:"version"`
	WebsiteURL  string      `json:"websiteUrl,omitempty"`
	Repository  *Repository `json:"repository,omitempty"`
	Remotes     []Remote    `json:"remotes,omitempty"`
	Packages    []Package   `json:"packages,omitempty"`
}

// Meta is the registry-managed metadata of an entry.
type Meta struct {
	Status          string `json:"status,omitempty"`
	StatusMessage   string `json:"statusMessage,omitempty"`
	StatusChangedAt string `json:"statusChangedAt,omitempty"`
	PublishedAt     string `json:"publishedAt,omitempty"`
	UpdatedAt       string `json:"updatedAt,omitempty"`
	IsLatest        bool   `json:"isLatest,omitempty"`
}

// Entry is one registry answer: the server.json plus its registry metadata.
// Raw holds the server object exactly as served, so its hash identifies the
// document that was used.
type Entry struct {
	Server Server
	Meta   Meta
	Raw    json.RawMessage
	// RetrievedAt is when this process fetched it.
	RetrievedAt time.Time
}

// Page is one page of search results.
type Page struct {
	Entries    []Entry
	NextCursor string
}

var (
	serverName  = regexp.MustCompile(`^[a-zA-Z0-9.-]+/[a-zA-Z0-9._-]+$`)
	versionText = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,254}$`)
	headerName  = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]{1,128}$")
	variableKey = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)
)

// Limits on what is accepted from a registry.
const (
	maxRemotes   = 16
	maxPackages  = 64
	maxHeaders   = 32
	maxVariables = 32
	maxText      = 4096
	maxURL       = 2048
)

// SchemaDate returns the date in a server.json $schema, or "".
func SchemaDate(schema string) string {
	if m := schemaURL.FindStringSubmatch(schema); m != nil {
		return m[1]
	}
	return ""
}

func supportedSchema(date string) bool {
	for _, d := range SupportedSchemas {
		if d == date {
			return true
		}
	}
	return false
}

// Validate checks a server.json for the members this package reads. It is
// strict about identity, schema version, sizes and character sets and does
// not try to be a complete JSON Schema validator.
func (s Server) Validate() error {
	if !serverName.MatchString(s.Name) || len(s.Name) < 3 || len(s.Name) > 200 {
		return fmt.Errorf("server name %q is not a reverse-DNS name (namespace/name)", Safe(s.Name, 60))
	}
	if !versionText.MatchString(s.Version) {
		return fmt.Errorf("server version %q is not a plain version string", Safe(s.Version, 60))
	}
	date := SchemaDate(s.Schema)
	if date == "" {
		return fmt.Errorf("server.json $schema %q is not an MCP registry schema URL", Safe(s.Schema, 120))
	}
	if !supportedSchema(date) {
		return fmt.Errorf("server.json schema %s is not supported by this version of aiplex (supported: %s); update aiplex or import by hand", date, strings.Join(SupportedSchemas, ", "))
	}
	for field, value := range map[string]string{"description": s.Description, "title": s.Title, "websiteUrl": s.WebsiteURL} {
		if len(value) > maxText || !cleanText(value) {
			return fmt.Errorf("server %s is too long or contains control characters", field)
		}
	}
	if len(s.Remotes) > maxRemotes || len(s.Packages) > maxPackages {
		return fmt.Errorf("server.json lists too many remotes or packages")
	}
	for i, r := range s.Remotes {
		if err := r.validate(); err != nil {
			return fmt.Errorf("remotes[%d]: %w", i, err)
		}
	}
	for i, p := range s.Packages {
		if len(p.Identifier) > 512 || len(p.RegistryType) > 64 || len(p.Version) > 255 || !cleanText(p.Identifier+p.RegistryType+p.Version+p.Transport.Type) {
			return fmt.Errorf("packages[%d]: invalid package entry", i)
		}
	}
	return nil
}

func (r Remote) validate() error {
	if r.Type == "" || len(r.Type) > 64 || !cleanText(r.Type) {
		return fmt.Errorf("transport type is missing or invalid")
	}
	if r.URL == "" || len(r.URL) > maxURL || strings.ContainsAny(r.URL, " \t\r\n\\\x00") || !cleanText(r.URL) {
		return fmt.Errorf("url is missing, too long or contains whitespace")
	}
	if len(r.Headers) > maxHeaders || len(r.Variables) > maxVariables {
		return fmt.Errorf("too many headers or variables")
	}
	for name, in := range r.Variables {
		if !variableKey.MatchString(name) {
			return fmt.Errorf("variable name %q is not a plain identifier", Safe(name, 40))
		}
		if err := in.validate(); err != nil {
			return fmt.Errorf("variable %s: %w", name, err)
		}
	}
	for i, h := range r.Headers {
		if !headerName.MatchString(h.Name) {
			return fmt.Errorf("headers[%d]: %q is not a header name", i, Safe(h.Name, 40))
		}
		if err := h.Input.validate(); err != nil {
			return fmt.Errorf("headers[%d]: %w", i, err)
		}
		if len(h.Variables) > maxVariables {
			return fmt.Errorf("headers[%d]: too many variables", i)
		}
		for name, in := range h.Variables {
			if !variableKey.MatchString(name) {
				return fmt.Errorf("headers[%d]: variable name %q is not a plain identifier", i, Safe(name, 40))
			}
			if err := in.validate(); err != nil {
				return fmt.Errorf("headers[%d] variable %s: %w", i, name, err)
			}
		}
	}
	return nil
}

func (in Input) validate() error {
	if len(in.Description) > maxText || len(in.Value) > maxText || len(in.Default) > maxText || len(in.Placeholder) > maxText || len(in.Choices) > 256 {
		return fmt.Errorf("input is too large")
	}
	if !cleanText(in.Description) || !cleanText(in.Value) || !cleanText(in.Default) || !cleanText(in.Placeholder) {
		return fmt.Errorf("input contains control characters")
	}
	for _, c := range in.Choices {
		if len(c) > 512 || !cleanText(c) {
			return fmt.Errorf("choice is too large or contains control characters")
		}
	}
	return nil
}

func cleanText(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for _, c := range s {
		if unicode.IsControl(c) && c != '\n' && c != '\t' {
			return false
		}
	}
	return true
}

// Safe makes registry text safe to print on a terminal: control characters
// (including escape sequences) become '?', and the text is cut to max runes.
func Safe(s string, max int) string {
	var b strings.Builder
	n := 0
	for _, c := range s {
		if n >= max {
			b.WriteString("…")
			break
		}
		switch {
		case c == utf8.RuneError || unicode.IsControl(c) || unicode.Is(unicode.Cf, c):
			b.WriteRune('?')
		default:
			b.WriteRune(c)
		}
		n++
	}
	return b.String()
}

// Importable classifies a server.json by what AIPlex can do with it.
type Importable struct {
	// Remotes are the indexes of remotes of type streamable-http.
	Remotes []int
	// SSEOnly counts remotes of the legacy SSE transport (not importable).
	SSE int
	// Packages counts local packages (not importable: the gateway runs none).
	Packages int
}

// Classify returns what is importable and what is not.
func (s Server) Classify() Importable {
	var out Importable
	for i, r := range s.Remotes {
		switch r.Type {
		case "streamable-http":
			out.Remotes = append(out.Remotes, i)
		case "sse":
			out.SSE++
		}
	}
	out.Packages = len(s.Packages)
	return out
}

// Label is a short description of the importable state for listings.
func (i Importable) Label() string {
	switch {
	case len(i.Remotes) > 0:
		return "remote"
	case i.SSE > 0:
		return "not importable (legacy SSE transport)"
	case i.Packages > 0:
		return "not importable (local package)"
	}
	return "not importable (no remote)"
}
