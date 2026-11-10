package toolexec

import (
	"fmt"
	"regexp"
	"strings"
)

// ConnectionAuth selects a credential described by the connection's explicit
// auth block (pkg/toolcredential) instead of an operator identity profile or
// a delegated OAuth grant. It carries no identity_ref.
const ConnectionAuth = "connection-auth"

var toolPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}\*?$|^\*$`)

// ToolPolicy is connection-level tool authorization. Patterns are exact tool
// names or a prefix ending in a single trailing '*'. Deny always wins. When
// Allow is empty every otherwise permitted tool is allowed. ReadOnly permits
// only tools whose gateway-observed MCP annotation readOnlyHint is true.
type ToolPolicy struct {
	Allow    []string `json:"allow,omitempty"`
	Deny     []string `json:"deny,omitempty"`
	ReadOnly bool     `json:"read_only,omitempty"`
}

func (p ToolPolicy) Validate() error {
	for field, patterns := range map[string][]string{"tools.allow": p.Allow, "tools.deny": p.Deny} {
		if patterns != nil && len(patterns) == 0 {
			return fmt.Errorf("%s must be omitted or non-empty", field)
		}
		if len(patterns) > 1024 {
			return fmt.Errorf("%s is bounded to 1024 patterns", field)
		}
		seen := map[string]bool{}
		for _, pattern := range patterns {
			if !toolPattern.MatchString(pattern) || seen[pattern] {
				return fmt.Errorf("%s entries must be unique tool names or prefixes ending in '*'", field)
			}
			seen[pattern] = true
		}
	}
	return nil
}

func matchTool(pattern, name string) bool {
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(name, strings.TrimSuffix(pattern, "*"))
	}
	return pattern == name
}

// Permits applies allow/deny only; read-only and caller scopes are separate.
func (p ToolPolicy) Permits(name string) bool {
	for _, pattern := range p.Deny {
		if matchTool(pattern, name) {
			return false
		}
	}
	if len(p.Allow) == 0 {
		return true
	}
	for _, pattern := range p.Allow {
		if matchTool(pattern, name) {
			return true
		}
	}
	return false
}

// Denial is an authorization refusal reported to the caller as a JSON-RPC
// error that preserves the request ID. Message must be safe to publish.
type Denial struct {
	// Status is the HTTP status of the refusal (default 403).
	Status  int
	Code    int
	Reason  string
	Message string
}

func (d *Denial) Error() string { return d.Message }

const (
	// RPCForbidden is the server-defined JSON-RPC code for authorization denials.
	RPCForbidden = -32001
)
