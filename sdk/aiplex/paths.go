package aiplex

import (
	"fmt"
	"net/url"
	"strings"
)

// validateAPIPath prevents encoded resource IDs from changing router boundaries.
// Query parameters are parsed separately and never participate in path checks.
func validateAPIPath(path string) error {
	parsed, err := url.ParseRequestURI(path)
	if err != nil || !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || parsed.IsAbs() || parsed.Host != "" || parsed.Fragment != "" {
		return fmt.Errorf("aiplex: invalid API path")
	}
	current := parsed.EscapedPath()
	for i := 0; i < 8; i++ {
		if strings.ContainsAny(current, "\\\x00\r\n") {
			return fmt.Errorf("aiplex: invalid API path")
		}
		for _, part := range strings.Split(current, "/") {
			if part == "." || part == ".." {
				return fmt.Errorf("aiplex: resource ID contains path traversal")
			}
		}
		decoded, e := url.PathUnescape(current)
		if e != nil {
			return fmt.Errorf("aiplex: invalid API path encoding")
		}
		if decoded == current {
			return nil
		}
		if strings.Count(decoded, "/") != strings.Count(current, "/") || strings.ContainsAny(decoded, "\\\x00\r\n") {
			return fmt.Errorf("aiplex: encoded resource separators are forbidden")
		}
		current = decoded
	}
	return fmt.Errorf("aiplex: excessive resource path encoding")
}

// validateResourceID refuses an ID the server could not route. The error is an
// *InputError so callers can tell "nothing was sent" from a failed request.
func validateResourceID(id string) error {
	if id == "" || strings.TrimSpace(id) != id || len(id) > 512 {
		return &InputError{Message: "resource ID is required"}
	}
	if err := validateAPIPath("/resource/" + url.PathEscape(id)); err != nil {
		return &InputError{Message: strings.TrimPrefix(err.Error(), "aiplex: ")}
	}
	return nil
}
