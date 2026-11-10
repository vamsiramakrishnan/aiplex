package api

import (
	"net/netip"
	"net/url"
	"regexp"
	"strings"
)

// The access point's catalog is also served in the shape of the MCP Registry
// API (GET /v0.1/servers, server.json schema 2025-12-11): servers[] entries
// of {server, _meta} and metadata.count. The AIPlex catalog members
// (templates, total, page, page_size) stay for existing clients.
//
// The _meta object is empty: the registry-managed metadata of the official
// registry (status, publication times) is the registry's to assert, and this
// listing is not that registry.

const registryServerSchema = "https://static.modelcontextprotocol.io/schemas/2025-12-11/server.schema.json"

var registryServerName = regexp.MustCompile(`^[a-zA-Z0-9.-]+/[a-zA-Z0-9._-]+$`)

type registryServerEntry struct {
	Server registryServerJSON `json:"server"`
	Meta   map[string]any     `json:"_meta"`
}

type registryServerJSON struct {
	Schema      string              `json:"$schema"`
	Name        string              `json:"name"`
	Title       string              `json:"title,omitempty"`
	Description string              `json:"description"`
	Version     string              `json:"version"`
	Remotes     []accessPointRemote `json:"remotes"`
}

// registryNamespace is the reverse-DNS namespace for names this deployment
// serves: its public host reversed, or "localhost" when there is no DNS name.
func registryNamespace(publicURL string) string {
	u, err := url.Parse(publicURL)
	if err != nil || u.Hostname() == "" {
		return "localhost"
	}
	host := strings.ToLower(u.Hostname())
	if _, err := netip.ParseAddr(host); err == nil || host == "localhost" {
		return "localhost"
	}
	labels := strings.Split(strings.TrimSuffix(host, "."), ".")
	for i, j := 0, len(labels)-1; i < j; i, j = i+1, j-1 {
		labels[i], labels[j] = labels[j], labels[i]
	}
	ns := strings.Join(labels, ".")
	if !regexp.MustCompile(`^[a-z0-9.-]+$`).MatchString(ns) {
		return "localhost"
	}
	return ns
}

func clipText(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// registryEntries converts the access point's catalog templates to registry
// entries. An entry whose name would not satisfy the schema is left out of
// servers (it stays in templates).
func (h *ConnectedHandler) registryEntries(servers []accessPointServer) []registryServerEntry {
	ns := registryNamespace(h.PublicURL)
	out := []registryServerEntry{}
	for _, s := range servers {
		name := ns + "/" + s.ID
		if len(name) > 200 || len(name) < 3 || !registryServerName.MatchString(name) {
			continue
		}
		title := clipText(s.Name, 100)
		out = append(out, registryServerEntry{
			Server: registryServerJSON{Schema: registryServerSchema, Name: name, Title: title, Description: clipText(s.Description, 100), Version: s.Version, Remotes: s.Remotes},
			Meta:   map[string]any{},
		})
	}
	return out
}
