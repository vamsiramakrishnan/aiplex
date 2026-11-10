package aiplex

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
)

// ToolRulesSubject is the caller a dry run simulates. GroupsKnown defaults to
// true when Groups is set and false otherwise: unknown membership is not
// empty membership.
type ToolRulesSubject struct {
	Kind        string   `json:"kind,omitempty"`
	Principal   string   `json:"principal,omitempty"`
	Groups      []string `json:"groups,omitempty"`
	GroupsKnown *bool    `json:"groups_known,omitempty"`
	Agent       string   `json:"agent,omitempty"`
	Client      string   `json:"client,omitempty"`
}

// ToolRulesAnnotations simulates observed MCP tool annotations; nil is unknown.
type ToolRulesAnnotations struct {
	ReadOnly    *bool `json:"read_only,omitempty"`
	Destructive *bool `json:"destructive,omitempty"`
	Idempotent  *bool `json:"idempotent,omitempty"`
	OpenWorld   *bool `json:"open_world,omitempty"`
}

// ToolRulesTest is a dry-run sample. The server never stores it.
type ToolRulesTest struct {
	Tool        string                `json:"tool"`
	Args        json.RawMessage       `json:"args,omitempty"`
	Subject     ToolRulesSubject      `json:"subject"`
	AccessPoint string                `json:"access_point,omitempty"`
	Annotations *ToolRulesAnnotations `json:"annotations,omitempty"`
	// Rules, when set, replace the connection's saved rules for this test only.
	Rules []ToolRule `json:"rules,omitempty"`
	Time  string     `json:"time,omitempty"`
}

// ToolRulesTrace is one rule's outcome: matched, not_matched, error or
// not_evaluated (an earlier rule decided).
type ToolRulesTrace struct {
	Set    string `json:"set"`
	ID     string `json:"id"`
	Effect string `json:"effect"`
	Result string `json:"result"`
	Error  string `json:"error,omitempty"`
}

// ToolRulesTestResult is what the rules decided on the sample.
type ToolRulesTestResult struct {
	Connection string           `json:"connection"`
	Tool       string           `json:"tool"`
	Decision   string           `json:"decision"` // allow, deny or error
	ErrorClass string           `json:"error_class,omitempty"`
	Reason     string           `json:"reason,omitempty"`
	Rule       string           `json:"rule,omitempty"`
	Message    string           `json:"message,omitempty"`
	Trace      []ToolRulesTrace `json:"trace"`
	ListHidden bool             `json:"list_hidden"`
	Note       string           `json:"note"`
}

// TestToolRules evaluates a connection's policy rules against a sample call.
// Operators only. Only rules are evaluated, and nothing is stored.
func (c *Client) TestToolRules(ctx context.Context, connectionID string, sample ToolRulesTest) (*ToolRulesTestResult, error) {
	if err := validateResourceID(connectionID); err != nil {
		return nil, err
	}
	var out ToolRulesTestResult
	if err := c.do(ctx, http.MethodPost, "/api/v1/mcp/connections/"+url.PathEscape(connectionID)+"/rules/test", sample, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
