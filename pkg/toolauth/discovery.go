package toolauth

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

func (s *Service) DiscoverResource(ctx context.Context, resource, challenge string) (ProtectedResourceMetadata, error) {
	if e := s.allowedURL(resource); e != nil {
		return ProtectedResourceMetadata{}, e
	}
	resourceURL, _ := url.Parse(resource)
	if resourceURL.RawQuery != "" {
		return ProtectedResourceMetadata{}, fmt.Errorf("MCP resource identifiers must not contain a query")
	}
	metadataURL, e := ResourceMetadataURL(challenge)
	if e != nil {
		return ProtectedResourceMetadata{}, e
	}
	candidates := []string{}
	if metadataURL != "" {
		candidates = append(candidates, metadataURL)
	} else {
		origin := originOf(resourceURL)
		if resourceURL.EscapedPath() != "" && resourceURL.EscapedPath() != "/" {
			candidates = append(candidates, origin+"/.well-known/oauth-protected-resource"+resourceURL.EscapedPath())
		}
		candidates = append(candidates, origin+"/.well-known/oauth-protected-resource")
	}
	for _, candidate := range candidates {
		var document ProtectedResourceMetadata
		response, err := s.fetchJSON(ctx, candidate, &document)
		if err != nil {
			if metadataURL == "" && response != nil && (response.StatusCode == 404 || response.StatusCode == 405) {
				continue
			}
			return ProtectedResourceMetadata{}, err
		}
		if document.Resource != resource {
			return ProtectedResourceMetadata{}, fmt.Errorf("protected resource metadata resource mismatch")
		}
		if len(document.AuthorizationServers) == 0 {
			return ProtectedResourceMetadata{}, fmt.Errorf("resource advertises no authorization server")
		}
		seen := map[string]bool{}
		for _, issuer := range document.AuthorizationServers {
			if e = s.allowedURL(issuer); e != nil {
				return ProtectedResourceMetadata{}, e
			}
			u, _ := url.Parse(issuer)
			if u.RawQuery != "" || seen[issuer] {
				return ProtectedResourceMetadata{}, fmt.Errorf("invalid or duplicate authorization server issuer")
			}
			seen[issuer] = true
		}
		return document, nil
	}
	return ProtectedResourceMetadata{}, fmt.Errorf("protected resource metadata not found")
}

func (s *Service) DiscoverAuthorizationServer(ctx context.Context, issuer string) (AuthorizationServerMetadata, error) {
	if e := s.allowedURL(issuer); e != nil {
		return AuthorizationServerMetadata{}, e
	}
	u, _ := url.Parse(issuer)
	if u.RawQuery != "" {
		return AuthorizationServerMetadata{}, fmt.Errorf("issuer must not contain query")
	}
	path := strings.TrimSuffix(u.EscapedPath(), "/")
	origin := originOf(u)
	candidates := []string{origin + "/.well-known/oauth-authorization-server" + path, origin + "/.well-known/openid-configuration" + path}
	if path != "" {
		candidates = append(candidates, origin+path+"/.well-known/openid-configuration")
	}
	for _, candidate := range candidates {
		var metadata AuthorizationServerMetadata
		response, e := s.fetchJSON(ctx, candidate, &metadata)
		if e != nil {
			if response != nil && (response.StatusCode == 404 || response.StatusCode == 405) {
				continue
			}
			return AuthorizationServerMetadata{}, e
		}
		if metadata.Issuer != issuer {
			return AuthorizationServerMetadata{}, fmt.Errorf("authorization server metadata issuer mismatch")
		}
		for _, endpoint := range []string{metadata.AuthorizationEndpoint, metadata.TokenEndpoint} {
			if e = s.allowedURL(endpoint); e != nil {
				return AuthorizationServerMetadata{}, e
			}
		}
		if metadata.RegistrationEndpoint != "" {
			if e = s.allowedURL(metadata.RegistrationEndpoint); e != nil {
				return AuthorizationServerMetadata{}, e
			}
		}
		return metadata, nil
	}
	return AuthorizationServerMetadata{}, fmt.Errorf("authorization server metadata not found")
}

// ResourceMetadataURL reads only a resource_metadata parameter belonging to a
// Bearer challenge. Quoted commas and escaped quotes are not treated as fields.
func ResourceMetadataURL(challenge string) (string, error) {
	if strings.TrimSpace(challenge) == "" {
		return "", nil
	}
	if len(challenge) > 16*1024 {
		return "", fmt.Errorf("authorization challenge too large")
	}
	segments := []string{}
	quoted, escaped := false, false
	start := 0
	for i, c := range challenge {
		if escaped {
			escaped = false
			continue
		}
		if quoted && c == '\\' {
			escaped = true
			continue
		}
		if c == '"' {
			quoted = !quoted
		}
		if c == ',' && !quoted {
			segments = append(segments, challenge[start:i])
			start = i + 1
		}
	}
	if quoted || escaped {
		return "", fmt.Errorf("malformed authorization challenge")
	}
	segments = append(segments, challenge[start:])
	scheme := ""
	found := ""
	for _, segment := range segments {
		field := strings.TrimSpace(segment)
		if field == "" {
			continue
		}
		// A scheme token followed by whitespace starts a new challenge. Parameter
		// names have '=' before whitespace, or immediately after an optional space.
		equal := strings.IndexByte(field, '=')
		space := strings.IndexAny(field, " \t")
		if space >= 0 && (equal < 0 || space < equal) {
			first := field[:space]
			rest := strings.TrimSpace(field[space:])
			if !strings.HasPrefix(rest, "=") {
				scheme = strings.ToLower(first)
				field = rest
			}
		} else if equal < 0 {
			scheme = strings.ToLower(field)
			continue
		}
		if scheme != "bearer" {
			continue
		}
		equal = strings.IndexByte(field, '=')
		if equal < 1 {
			continue
		}
		key := strings.TrimSpace(field[:equal])
		value := strings.TrimSpace(field[equal+1:])
		if !strings.EqualFold(key, "resource_metadata") {
			continue
		}
		if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
			value = value[1 : len(value)-1]
			var unquoted strings.Builder
			esc := false
			for _, c := range value {
				if esc {
					unquoted.WriteRune(c)
					esc = false
				} else if c == '\\' {
					esc = true
				} else {
					unquoted.WriteRune(c)
				}
			}
			if esc {
				return "", fmt.Errorf("malformed resource metadata parameter")
			}
			value = unquoted.String()
		} else if strings.ContainsAny(value, " \t\r\n\"") {
			return "", fmt.Errorf("malformed resource metadata parameter")
		}
		if value == "" || found != "" {
			return "", fmt.Errorf("empty or duplicate resource metadata parameter")
		}
		found = value
	}
	return found, nil
}
