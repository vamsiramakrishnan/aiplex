package mcpregistry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	maxListBytes   = 8 << 20
	maxDetailBytes = 1 << 20
	maxPageSize    = 100
)

// Client reads a registry. The zero value is not usable; use NewClient.
type Client struct {
	base *url.URL
	http *http.Client
	now  func() time.Time
}

// BaseURL returns the validated registry base URL without a trailing slash.
func (c *Client) BaseURL() string { return strings.TrimSuffix(c.base.String(), "/") }

// ValidateBase checks a registry base URL: https, or http to a loopback host;
// no credentials, query or fragment.
func ValidateBase(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || strings.ContainsAny(raw, " \\\x00\r\n") {
		return nil, fmt.Errorf("registry %q is not a plain URL without credentials, query or fragment", Safe(raw, 80))
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !IsLoopbackHost(u.Hostname()) {
			return nil, fmt.Errorf("registry %q must use https (plain http is accepted only for a loopback host)", Safe(raw, 80))
		}
	default:
		return nil, fmt.Errorf("registry %q must use https", Safe(raw, 80))
	}
	u.Path = strings.TrimSuffix(u.Path, "/")
	return u, nil
}

// IsLoopbackHost reports whether host is localhost or a loopback address.
func IsLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.IsLoopback()
}

// NewClient returns a client for the registry at base. httpClient may be nil;
// its redirect policy is replaced: redirects are never followed.
func NewClient(base string, httpClient *http.Client) (*Client, error) {
	u, err := ValidateBase(base)
	if err != nil {
		return nil, err
	}
	var hc http.Client
	if httpClient != nil {
		hc = *httpClient
	}
	if hc.Timeout == 0 {
		hc.Timeout = 20 * time.Second
	}
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	hc.Jar = nil
	return &Client{base: u, http: &hc, now: time.Now}, nil
}

func (c *Client) get(ctx context.Context, path string, query url.Values, limit int64) ([]byte, error) {
	target := c.BaseURL() + APIPrefix + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "aiplex-cli")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("registry %s unreachable: %w", c.base.Host, err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		return nil, fmt.Errorf("registry answered a redirect; redirects are not followed (the registry URL must be the final one)")
	case resp.StatusCode == http.StatusNotFound:
		return nil, ErrNotFound
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("registry answered HTTP %d", resp.StatusCode)
	}
	if media, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type")); err != nil || media != "application/json" {
		return nil, fmt.Errorf("registry did not answer application/json")
	}
	if resp.ContentLength > limit {
		return nil, fmt.Errorf("registry response is larger than %d bytes", limit)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read registry response: %w", err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("registry response is larger than %d bytes", limit)
	}
	return data, nil
}

// ErrNotFound is returned when the registry has no such server or version.
var ErrNotFound = fmt.Errorf("not found in the registry")

type wireEntry struct {
	Server json.RawMessage            `json:"server"`
	Meta   map[string]json.RawMessage `json:"_meta"`
}

func (c *Client) decodeEntry(w wireEntry) (Entry, error) {
	if len(w.Server) == 0 {
		return Entry{}, fmt.Errorf("registry entry has no server object")
	}
	var server Server
	dec := json.NewDecoder(bytes.NewReader(w.Server))
	if err := dec.Decode(&server); err != nil {
		return Entry{}, fmt.Errorf("registry entry is not a server.json: %v", err)
	}
	if err := server.Validate(); err != nil {
		return Entry{}, err
	}
	var meta Meta
	if raw, ok := w.Meta[officialMeta]; ok {
		if err := json.Unmarshal(raw, &meta); err != nil {
			return Entry{}, fmt.Errorf("registry metadata is invalid")
		}
		for _, v := range []string{meta.Status, meta.StatusMessage} {
			if len(v) > maxText || !cleanText(v) {
				return Entry{}, fmt.Errorf("registry metadata is invalid")
			}
		}
	}
	return Entry{Server: server, Meta: meta, Raw: append(json.RawMessage(nil), w.Server...), RetrievedAt: c.now().UTC()}, nil
}

// Search lists latest versions whose name contains text (the registry's
// search). Entries that fail validation are returned in skipped, not dropped
// silently and never trusted.
func (c *Client) Search(ctx context.Context, text string, limit int, cursor string) (page Page, skipped int, err error) {
	if limit <= 0 || limit > maxPageSize {
		limit = 30
	}
	q := url.Values{"limit": {strconv.Itoa(limit)}, "version": {"latest"}}
	if text != "" {
		q.Set("search", text)
	}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	data, err := c.get(ctx, "/servers", q, maxListBytes)
	if err != nil {
		return Page{}, 0, err
	}
	var wire struct {
		Servers  []wireEntry `json:"servers"`
		Metadata struct {
			NextCursor string `json:"nextCursor"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return Page{}, 0, fmt.Errorf("registry answer is not a server list")
	}
	if len(wire.Servers) > maxPageSize*2 || len(wire.Metadata.NextCursor) > 512 || !cleanText(wire.Metadata.NextCursor) {
		return Page{}, 0, fmt.Errorf("registry answer is larger than expected")
	}
	for _, w := range wire.Servers {
		entry, err := c.decodeEntry(w)
		if err != nil {
			skipped++
			continue
		}
		page.Entries = append(page.Entries, entry)
	}
	page.NextCursor = wire.Metadata.NextCursor
	return page, skipped, nil
}

// Get fetches one server version. An empty version or "latest" selects the
// latest. The returned entry is validated, and its name and version must be
// the ones asked for.
func (c *Client) Get(ctx context.Context, name, version string) (Entry, error) {
	if !serverName.MatchString(name) || len(name) > 200 {
		return Entry{}, fmt.Errorf("%q is not a registry server name (namespace/name)", Safe(name, 60))
	}
	asked := version
	if version == "" {
		version = "latest"
	}
	if version != "latest" && !versionText.MatchString(version) {
		return Entry{}, fmt.Errorf("%q is not a version", Safe(version, 60))
	}
	data, err := c.get(ctx, "/servers/"+url.PathEscape(name)+"/versions/"+url.PathEscape(version), nil, maxDetailBytes)
	if err != nil {
		if err == ErrNotFound {
			return Entry{}, fmt.Errorf("%s@%s: %w", name, version, ErrNotFound)
		}
		return Entry{}, err
	}
	var wire wireEntry
	if err := json.Unmarshal(data, &wire); err != nil {
		return Entry{}, fmt.Errorf("registry answer is not a server entry")
	}
	entry, err := c.decodeEntry(wire)
	if err != nil {
		return Entry{}, err
	}
	if entry.Server.Name != name {
		return Entry{}, fmt.Errorf("registry answered %q for %q", Safe(entry.Server.Name, 60), name)
	}
	if asked != "" && asked != "latest" && entry.Server.Version != asked {
		return Entry{}, fmt.Errorf("registry answered version %q for %q", Safe(entry.Server.Version, 40), asked)
	}
	return entry, nil
}
