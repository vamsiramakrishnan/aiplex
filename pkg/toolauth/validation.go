package toolauth

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// ValidateRedirectURI validates an exact URI; prefix checks incorrectly accept
// names such as localhost.attacker.example. HTTP is limited to loopback hosts.
func ValidateRedirectURI(raw string) error {
	u, e := url.Parse(raw)
	if e != nil || u.Host == "" || u.User != nil || u.Fragment != "" || u.Opaque != "" {
		return fmt.Errorf("invalid redirect URI")
	}
	if u.Scheme == "https" {
		return nil
	}
	ip := net.ParseIP(u.Hostname())
	if u.Scheme == "http" && (u.Hostname() == "localhost" || (ip != nil && ip.IsLoopback())) {
		return nil
	}
	return fmt.Errorf("redirect URI requires HTTPS or exact loopback HTTP host")
}
func validateURL(raw string, allowLoopback bool) error {
	u, e := url.Parse(raw)
	if e != nil || u.Host == "" || u.User != nil || u.Fragment != "" || u.Opaque != "" {
		return fmt.Errorf("invalid OAuth endpoint")
	}
	if u.Scheme == "https" {
		return nil
	}
	ip := net.ParseIP(u.Hostname())
	if allowLoopback && u.Scheme == "http" && (u.Hostname() == "localhost" || (ip != nil && ip.IsLoopback())) {
		return nil
	}
	return fmt.Errorf("OAuth endpoint requires HTTPS")
}
func validateProfile(p ClientProfile) error {
	if _, err := validScopes(p.Scopes); err != nil {
		return err
	}
	if strings.TrimSpace(p.ID) == "" || strings.TrimSpace(p.Name) == "" || len(p.RedirectURIs) == 0 {
		return fmt.Errorf("client profile requires id, name and redirects")
	}
	if p.ApplicationType != "native" && p.ApplicationType != "web" {
		return fmt.Errorf("application_type must be native or web")
	}
	seen := map[string]bool{}
	for _, r := range p.RedirectURIs {
		if e := ValidateRedirectURI(r); e != nil {
			return e
		}
		if seen[r] {
			return fmt.Errorf("duplicate redirect URI")
		}
		seen[r] = true
	}
	return nil
}
func validateMetadata(m ClientMetadata) error {
	if e := validateClientIDURL(m.ClientID); e != nil {
		return e
	}
	if strings.TrimSpace(m.ClientName) == "" || len(m.RedirectURIs) == 0 {
		return fmt.Errorf("CIMD requires client_name and redirect_uris")
	}
	seen := map[string]bool{}
	for _, r := range m.RedirectURIs {
		if e := ValidateRedirectURI(r); e != nil {
			return e
		}
		if seen[r] {
			return fmt.Errorf("duplicate metadata redirect URI")
		}
		seen[r] = true
	}
	if len(m.GrantTypes) > 0 && !sameStrings(m.GrantTypes, []string{"authorization_code"}) && !sameStrings(m.GrantTypes, []string{"authorization_code", "refresh_token"}) {
		return fmt.Errorf("unsupported CIMD grant types")
	}
	if len(m.ResponseTypes) > 0 && !sameStrings(m.ResponseTypes, []string{"code"}) {
		return fmt.Errorf("unsupported CIMD response types")
	}

	if m.TokenEndpointAuthMethod != "" && m.TokenEndpointAuthMethod != "none" {
		return fmt.Errorf("this CIMD adapter supports public clients using token_endpoint_auth_method none")
	}
	return nil
}
func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	set := map[string]int{}
	for _, x := range a {
		set[x]++
	}
	for _, x := range b {
		set[x]--
	}
	for _, v := range set {
		if v != 0 {
			return false
		}
	}
	return true
}
func cloneMetadata(m ClientMetadata) ClientMetadata {
	m.RedirectURIs = append([]string(nil), m.RedirectURIs...)
	m.GrantTypes = append([]string(nil), m.GrantTypes...)
	m.ResponseTypes = append([]string(nil), m.ResponseTypes...)
	return m
}

func ValidateClientProfile(profile ClientProfile) error { return validateProfile(profile) }
func ValidateConnection(connection Connection) error {
	if connection.TenantID == "" || connection.ID == "" {
		return fmt.Errorf("tool connection requires tenant and id")
	}
	if e := validateURL(connection.ResourceURL, true); e != nil {
		return e
	}
	u, _ := url.Parse(connection.ResourceURL)
	if u.RawQuery != "" {
		return fmt.Errorf("resource must not contain query")
	}
	if connection.Issuer != "" {
		if e := validateURL(connection.Issuer, true); e != nil {
			return e
		}
	}
	return nil
}

func validateClientIDURL(raw string) error {
	if e := validateURL(raw, false); e != nil {
		return e
	}
	u, _ := url.Parse(raw)
	if u.Path == "" || u.Path == "/" || u.RawQuery != "" {
		return fmt.Errorf("CIMD client_id requires a path and no query")
	}
	return nil
}
func ValidateClientMetadata(metadata ClientMetadata) error { return validateMetadata(metadata) }
