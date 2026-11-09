package toolcredential

import (
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// Credential is one resolved upstream credential bound to a single origin.
// Its value is unexported, has no JSON form and formats as redacted.
type Credential struct {
	origin string
	header string
	query  string
	value  string
	none   bool
}

func (Credential) String() string               { return "ToolCredential(redacted)" }
func (Credential) GoString() string             { return "ToolCredential(redacted)" }
func (Credential) MarshalJSON() ([]byte, error) { return []byte(`"redacted"`), nil }

// Origin returns the only origin this credential may be sent to.
func (c Credential) Origin() string { return c.origin }

// Apply adds the credential to req only when req targets the bound origin.
func (c Credential) Apply(req *http.Request) error {
	if req == nil || req.URL == nil || c.origin == "" {
		return fmt.Errorf("tool credential has no bound origin")
	}
	got, err := Origin(req.URL.String())
	if err != nil || got != c.origin || (req.Host != "" && !strings.EqualFold(req.Host, req.URL.Host)) {
		return fmt.Errorf("tool credential is bound to a different origin")
	}
	if c.none {
		return nil
	}
	if !headerValue(c.value) {
		return fmt.Errorf("tool credential value is not a valid header value")
	}
	switch {
	case c.header != "":
		req.Header.Set(c.header, c.value)
	case c.query != "":
		q := req.URL.Query()
		q.Set(c.query, c.value)
		req.URL.RawQuery = q.Encode()
	default:
		return fmt.Errorf("tool credential placement unavailable")
	}
	return nil
}

func headerValue(v string) bool {
	if v == "" || len(v) > 16384 {
		return false
	}
	for _, r := range v {
		if r < 0x20 || r > 0x7e {
			return false
		}
	}
	return true
}

// Origin normalizes a URL to scheme://host:port.
func Origin(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return "", fmt.Errorf("absolute HTTP(S) URL required")
	}
	port := u.Port()
	if port == "" {
		port = "443"
		if u.Scheme == "http" {
			port = "80"
		}
	}
	return u.Scheme + "://" + net.JoinHostPort(strings.ToLower(u.Hostname()), port), nil
}

func headerCredential(origin, name, value string) Credential {
	return Credential{origin: origin, header: name, value: value}
}

func bearerCredential(origin, token string) Credential {
	return headerCredential(origin, "Authorization", "Bearer "+token)
}

func basicCredential(origin, username, password string) Credential {
	return headerCredential(origin, "Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(username+":"+password)))
}
