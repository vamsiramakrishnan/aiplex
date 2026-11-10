package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
	"github.com/vamsiramakrishnan/aiplex/pkg/toolpolicy"
)

var dryRunToolName = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)

// ToolRulesTestRequest is the body of POST /api/v1/mcp/connections/{id}/rules/test.
// Nothing in it is stored, logged or recorded in the ledger.
type ToolRulesTestRequest struct {
	Tool string          `json:"tool"`
	Args json.RawMessage `json:"args,omitempty"`
	// Subject describes the caller to simulate.
	Subject ToolRulesTestSubject `json:"subject"`
	// AccessPoint, when set, adds that access point's rules for this
	// connection and sets access_point.id.
	AccessPoint string `json:"access_point,omitempty"`
	// Annotations simulates observed MCP annotations. When omitted the
	// gateway's stored observations are used (absent when unknown or stale).
	Annotations *ToolRulesTestAnnotations `json:"annotations,omitempty"`
	// Rules, when present, replaces the connection's saved rules for this test
	// only, so candidate rules can be tried before saving them.
	Rules []toolpolicy.Rule `json:"rules,omitempty"`
	// Time overrides request.time (RFC 3339).
	Time string `json:"time,omitempty"`
}

// ToolRulesTestSubject is the simulated caller. GroupsKnown defaults to true
// when groups are given and to false when they are not: unknown membership is
// not empty membership.
type ToolRulesTestSubject struct {
	Kind        string   `json:"kind,omitempty"`
	Principal   string   `json:"principal,omitempty"`
	Groups      []string `json:"groups,omitempty"`
	GroupsKnown *bool    `json:"groups_known,omitempty"`
	Agent       string   `json:"agent,omitempty"`
	Client      string   `json:"client,omitempty"`
}

type ToolRulesTestAnnotations struct {
	ReadOnly    *bool `json:"read_only,omitempty"`
	Destructive *bool `json:"destructive,omitempty"`
	Idempotent  *bool `json:"idempotent,omitempty"`
	OpenWorld   *bool `json:"open_world,omitempty"`
}

type ToolRulesTestTrace struct {
	Set string `json:"set"`
	toolpolicy.RuleTrace
}

type ToolRulesTestResult struct {
	Connection string               `json:"connection"`
	Tool       string               `json:"tool"`
	Decision   string               `json:"decision"`
	Class      string               `json:"error_class,omitempty"`
	Reason     string               `json:"reason,omitempty"`
	Rule       string               `json:"rule,omitempty"`
	Message    string               `json:"message,omitempty"`
	Trace      []ToolRulesTestTrace `json:"trace"`
	// ListHidden is true when tools/list would hide this tool for this
	// subject (decided without arguments).
	ListHidden bool `json:"list_hidden"`
	// Note states what the dry run does not check.
	Note string `json:"note"`
}

// TestToolRules evaluates a connection's rules (and optionally an access
// point's) against a sample call and returns the per-rule trace. Operators
// only. It checks rules, not the name lists, scopes, read_only, quota or hooks.
func (h *ConnectedHandler) TestToolRules(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.tenant(w, r)
	if !ok {
		return
	}
	id := idParam(r)
	record, err := h.Store.Get(r.Context(), tenant, "tool-connection", id)
	if err != nil {
		resourceError(w, r, err)
		return
	}
	var connection ToolConnection
	if json.Unmarshal(record.Spec, &connection) != nil || connection.ID != id {
		Error(w, r, 409, "INVALID_STORED_CONNECTION", "the stored tool connection is invalid")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, (256<<10)+1))
	if err != nil || len(body) > 256<<10 {
		Error(w, r, http.StatusRequestEntityTooLarge, "REQUEST_TOO_LARGE", "the sample exceeds 256 KiB")
		return
	}
	var in ToolRulesTestRequest
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&in) != nil || !dryRunToolName.MatchString(in.Tool) {
		Error(w, r, http.StatusBadRequest, "INVALID_REQUEST", `body must be {"tool": "name", "args": {...}, "subject": {"kind": "user", "principal": "...", "groups": [...]}}`)
		return
	}
	now := time.Now().UTC()
	if in.Time != "" {
		if now, err = time.Parse(time.RFC3339, in.Time); err != nil {
			Error(w, r, http.StatusBadRequest, "INVALID_REQUEST", "time must be RFC 3339")
			return
		}
	}
	var connRules, apRules *toolpolicy.Set
	if in.Rules != nil {
		connRules, err = toolpolicy.Compile(in.Rules)
	} else if connection.Tools != nil {
		connRules, err = toolpolicy.CompileCached(connection.Tools.Rules)
	}
	if err != nil {
		Error(w, r, http.StatusBadRequest, "INVALID_RULES", err.Error())
		return
	}
	var apLabels map[string]string
	if in.AccessPoint != "" {
		_, ap, loadErr := h.loadAccessPointRecord(r.Context(), tenant, in.AccessPoint)
		if loadErr != nil {
			resourceError(w, r, resources.ErrNotFound)
			return
		}
		apLabels = ap.Labels
		if tool := ap.tool(id); tool != nil {
			if apRules, err = toolpolicy.CompileCached(tool.Rules); err != nil {
				Error(w, r, http.StatusConflict, "INVALID_STORED_RULES", "the access point's rules no longer compile")
				return
			}
		}
	}
	known := in.Subject.Groups != nil
	if in.Subject.GroupsKnown != nil {
		known = *in.Subject.GroupsKnown
	}
	facts := toolpolicy.Facts{ToolName: in.Tool, ToolConnection: id, HasArgs: true, AccessPointID: in.AccessPoint, AccessPointTags: apLabels, Time: now,
		Caller: toolpolicy.Caller{Kind: in.Subject.Kind, Principal: in.Subject.Principal, Groups: in.Subject.Groups, GroupsKnown: known, Agent: in.Subject.Agent, Client: in.Subject.Client}}
	if in.Annotations != nil {
		facts.Annotations = toolpolicy.Annotations{ReadOnly: in.Annotations.ReadOnly, Destructive: in.Annotations.Destructive, Idempotent: in.Annotations.Idempotent, OpenWorld: in.Annotations.OpenWorld}
	} else {
		facts.Annotations = h.policyAnnotationsFor(r.Context(), tenant, record, in.Tool)
	}
	result := ToolRulesTestResult{Connection: id, Tool: in.Tool, Decision: "allow", Trace: []ToolRulesTestTrace{},
		Note: "Only policy rules were evaluated. The tool name lists, the caller's mcp:tools scope, read_only, quota and request hooks are not part of this test. Nothing was stored."}
	w.Header().Set("Cache-Control", "no-store")
	args, err := toolpolicy.ParseArgs(in.Args)
	if err != nil {
		if !errors.Is(err, toolpolicy.ErrArgsTooLarge) {
			Error(w, r, http.StatusBadRequest, "INVALID_REQUEST", "args must be a JSON object")
			return
		}
		result.Decision, result.Class, result.Reason = "error", toolpolicy.ClassError, toolpolicy.ReasonArgsTooLarge
		JSON(w, http.StatusOK, result)
		return
	}
	facts.Args = args
	decided := false
	for _, named := range []struct {
		name string
		set  *toolpolicy.Set
	}{{"connection", connRules}, {"access_point", apRules}} {
		if named.set.Len() == 0 {
			continue
		}
		d := named.set.Evaluate(r.Context(), facts)
		for _, t := range d.Trace {
			result.Trace = append(result.Trace, ToolRulesTestTrace{Set: named.name, RuleTrace: t})
		}
		if !d.Allowed && !decided {
			decided = true
			result.Decision, result.Class, result.Reason, result.Rule, result.Message = decisionLabel(d), d.Class, d.Reason, d.RuleID, d.Message
		}
	}
	result.ListHidden = connRules.Hidden(r.Context(), facts) || apRules.Hidden(r.Context(), facts)
	JSON(w, http.StatusOK, result)
}
