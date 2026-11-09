package toolauth

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// MetadataHandler serves only operator-configured Client ID Metadata Documents.
// publicOrigin must be the canonical HTTPS origin, not an untrusted Host header.
func (s *Service) MetadataHandler(publicOrigin string) (http.Handler, error) {
	if e := validateURL(publicOrigin, false); e != nil {
		return nil, e
	}
	origin, _ := url.Parse(publicOrigin)
	if origin.Path != "" && origin.Path != "/" || origin.RawQuery != "" {
		return nil, fmt.Errorf("public CIMD origin must be an HTTPS origin")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", 405)
			return
		}
		// Request Host cannot select an arbitrary metadata document or alter client_id.
		key := originOf(origin) + r.URL.EscapedPath()
		if r.URL.RawQuery != "" {
			http.NotFound(w, r)
			return
		}
		document, ok := s.documents[key]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "public, max-age=300")
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(cloneMetadata(document))
		}
	}), nil
}

// ProtectedResourceHandler publishes MCP resource metadata. It deliberately
// advertises no DCR/CIMD authorization-server capability: the external AS owns it.
func ProtectedResourceHandler(metadata ProtectedResourceMetadata) (http.Handler, error) {
	if e := validateURL(metadata.Resource, false); e != nil {
		return nil, e
	}
	if len(metadata.AuthorizationServers) == 0 {
		return nil, fmt.Errorf("authorization servers required")
	}
	for _, issuer := range metadata.AuthorizationServers {
		if e := validateURL(issuer, false); e != nil {
			return nil, e
		}
	}
	metadata.AuthorizationServers = append([]string(nil), metadata.AuthorizationServers...)
	metadata.ScopesSupported = append([]string(nil), metadata.ScopesSupported...)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", 405)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(metadata)
		}
	}), nil
}
