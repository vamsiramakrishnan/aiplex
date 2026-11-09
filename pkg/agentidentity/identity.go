// Package agentidentity verifies native Google Cloud Agent Identity assertions.
// The trust domain, pool issuer, subject allowlist and binding mode are explicit.
package agentidentity

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"strings"
)

type ClaimsVerifier interface {
	Verify(context.Context, string) (map[string]any, error)
}
type BindingMode string

const (
	RequiredBinding BindingMode = "required"
	UnboundAllowed  BindingMode = "unbound-allowed"
)

type Config struct {
	Issuer          string      `json:"issuer"`
	Audience        string      `json:"audience"`
	TrustDomain     string      `json:"trust_domain"`
	AllowedSubjects []string    `json:"allowed_subjects"`
	BindingMode     BindingMode `json:"binding_mode"`
}
type Verifier struct {
	config   Config
	verifier ClaimsVerifier
	subjects map[string]bool
}

var poolIssuer = regexp.MustCompile(`^https://sts\.googleapis\.com/v1/(organizations|projects)/([0-9]+)/locations/global/workloadIdentityPools/([a-z0-9.-]+)$`)
var trustDomain = regexp.MustCompile(`^agents\.global\.(org|proj)-[0-9]+\.system\.id\.goog$`)

func (c Config) Validate() error {
	match := poolIssuer.FindStringSubmatch(c.Issuer)
	if match == nil || match[3] != c.TrustDomain || !trustDomain.MatchString(c.TrustDomain) {
		return fmt.Errorf("agent identity requires an exact Google STS agent trust-domain issuer")
	}
	expectedDomain := "agents.global.org-" + match[2] + ".system.id.goog"
	if match[1] == "projects" {
		expectedDomain = "agents.global.proj-" + match[2] + ".system.id.goog"
	}
	if c.TrustDomain != expectedDomain {
		return fmt.Errorf("agent identity trust domain owner mismatch")
	}
	if strings.TrimSpace(c.Audience) == "" || len(c.AllowedSubjects) == 0 {
		return fmt.Errorf("audience and allowed agent subjects required")
	}
	if c.BindingMode != RequiredBinding && c.BindingMode != UnboundAllowed {
		return fmt.Errorf("explicit agent token binding mode required")
	}
	seen := map[string]bool{}
	for _, s := range c.AllowedSubjects {
		if !validSPIFFE(s, c.TrustDomain) || seen[s] {
			return fmt.Errorf("invalid or duplicate allowed SPIFFE subject %q", s)
		}
		seen[s] = true
	}
	return nil
}
func validSPIFFE(raw, domain string) bool {
	u, e := url.Parse(raw)
	return e == nil && u.Scheme == "spiffe" && u.Host == domain && u.User == nil && u.RawQuery == "" && u.Fragment == "" && strings.HasPrefix(u.Path, "/resources/") && !strings.Contains(u.Path, "/../") && !strings.Contains(u.Path, "//") && !strings.ContainsAny(raw, "*%\\\r\n")
}
func (c Config) JWKSURL() (string, error) {
	if e := c.Validate(); e != nil {
		return "", e
	}
	return c.Issuer + "/openid/jwks", nil
}
func New(c Config, verifier ClaimsVerifier) (*Verifier, error) {
	if e := c.Validate(); e != nil {
		return nil, e
	}
	if verifier == nil || (reflect.ValueOf(verifier).Kind() == reflect.Pointer && reflect.ValueOf(verifier).IsNil()) {
		return nil, fmt.Errorf("cryptographic token verifier required")
	}
	c.AllowedSubjects = append([]string(nil), c.AllowedSubjects...)
	v := &Verifier{config: c, verifier: verifier, subjects: map[string]bool{}}
	for _, s := range c.AllowedSubjects {
		v.subjects[s] = true
	}
	return v, nil
}
func (v *Verifier) Verify(ctx context.Context, token string) (map[string]any, error) {
	return v.verify(ctx, token, nil)
}
func (v *Verifier) VerifyRequest(r *http.Request, token string) (map[string]any, error) {
	if r == nil {
		return nil, fmt.Errorf("request required")
	}
	return v.verify(r.Context(), token, r)
}
func (v *Verifier) verify(ctx context.Context, token string, r *http.Request) (map[string]any, error) {
	claims, e := v.verifier.Verify(ctx, token)
	if e != nil {
		return nil, e
	}
	parts := strings.Split(token, ".")
	var header struct {
		Type string `json:"typ"`
	}
	if len(parts) != 3 {
		return nil, fmt.Errorf("agent identity requires a JWT assertion")
	}
	rawHeader, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || json.Unmarshal(rawHeader, &header) != nil || header.Type != "JWT" {
		return nil, fmt.Errorf("agent identity assertion type must be JWT")
	}

	sub, _ := claims["sub"].(string)
	iss, _ := claims["iss"].(string)
	if iss != v.config.Issuer || !audience(claims["aud"], v.config.Audience) || !v.subjects[sub] {
		return nil, fmt.Errorf("agent identity issuer, audience or SPIFFE subject rejected")
	}
	binding := ""
	if cnf, ok := claims["cnf"].(map[string]any); ok {
		binding, _ = cnf["x5t#S256"].(string)
	}
	if v.config.BindingMode == RequiredBinding || claims["cnf"] != nil {
		if binding == "" || r == nil || r.TLS == nil || len(r.TLS.PeerCertificates) == 0 || len(r.TLS.VerifiedChains) == 0 {
			return nil, fmt.Errorf("agent certificate binding requires verified mutual TLS")
		}
		leaf := r.TLS.PeerCertificates[0]
		if len(r.TLS.VerifiedChains[0]) == 0 || !leaf.Equal(r.TLS.VerifiedChains[0][0]) {
			return nil, fmt.Errorf("unverified agent client certificate")
		}
		digest := sha256.Sum256(leaf.Raw)
		expected := base64.RawURLEncoding.EncodeToString(digest[:])
		if subtle.ConstantTimeCompare([]byte(binding), []byte(expected)) != 1 {
			return nil, fmt.Errorf("agent token certificate fingerprint mismatch")
		}
		subjectMatch := false
		for _, u := range leaf.URIs {
			if u.String() == sub {
				subjectMatch = true
			}
		}
		if !subjectMatch {
			return nil, fmt.Errorf("agent SPIFFE token and certificate identities differ")
		}
	}
	return claims, nil
}
func audience(raw any, want string) bool {
	switch a := raw.(type) {
	case string:
		return a == want
	case []any:
		for _, x := range a {
			if x == want {
				return true
			}
		}
	case []string:
		for _, x := range a {
			if x == want {
				return true
			}
		}
	}
	return false
}
