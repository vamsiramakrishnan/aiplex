package aws

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/vamsiramakrishnan/aiplex/pkg/nativeapi"
)

// ValidateOperation performs no authentication or cloud calls. Hosts must run
// this validation before saving a desired operation. Credential=true operations
// must never be accepted by a public controller or persisted in its journal.
func (d *Driver) ValidateOperation(op nativeapi.Operation) error {
	if d == nil {
		return errors.New("AgentCore driver is not configured")
	}
	parts := strings.Split(op.Name, ".")
	if len(parts) != 2 {
		return errors.New("unknown AgentCore operation")
	}
	kind, action := parts[0], parts[1]
	if kind == "workloadTokens" || kind == "resourceCredentials" {
		_, _, e := d.tokenRequest(op)
		return e
	}
	if kind == "tokenVaults" {
		if op.Resource != d.cfg.VaultARN() {
			return errors.New("vault is outside configured identity scope")
		}
		if action == "get" {
			if op.ETag != "" {
				return errors.New("get accepts no etag")
			}
			return empty(op.Payload)
		}
		if action != "setCMK" {
			return errors.New("unknown token vault operation")
		}
		_, e := d.validateVaultInput(op)
		return e
	}
	if nativeKind(kind) == "" || !slices.Contains([]string{"list", "get", "create", "update", "delete"}, action) {
		return errors.New("unknown AgentCore operation")
	}
	if action == "list" {
		parent := d.cfg.DirectoryARN()
		if kind != "workloadIdentities" {
			parent = d.cfg.VaultARN()
		}
		if op.Resource != parent || op.ETag != "" {
			return errors.New("inventory requires configured directory or vault")
		}
		var b listRequest
		if len(op.Payload) > 0 {
			if e := strict(op.Payload, &b); e != nil {
				return e
			}
		}
		if b.MaxResults < 0 || b.MaxResults > 20 || len(b.NextToken) > 8192 {
			return errors.New("invalid AgentCore pagination")
		}
		return nil
	}
	n, e := d.name(op.Resource, kind)
	if e != nil {
		return e
	}
	if action == "get" {
		if op.ETag != "" {
			return errors.New("get accepts no etag")
		}
		return empty(op.Payload)
	}
	if action == "create" {
		if op.ETag != "" {
			return errors.New("create accepts no etag")
		}
	} else if !d.cfg.AllowNonAtomicMutations || !validFingerprint(op.ETag) {
		return errors.New("AgentCore mutation requires non-atomic opt-in and observed fingerprint")
	}
	if action == "delete" {
		return empty(op.Payload)
	}
	if kind != "workloadIdentities" {
		_, e := d.providerBody(kind, action, n, op.Payload)
		return e
	}
	var b workloadRequest
	if e := strict(op.Payload, &b); e != nil {
		return e
	}
	if b.Name != "" && b.Name != n {
		return errors.New("workload body/resource mismatch")
	}
	if action == "update" && b.AllowedReturnURLs == nil {
		return errors.New("workload update requires an explicit return-URL list")
	}
	if len(b.AllowedReturnURLs) > 100 {
		return errors.New("too many workload return URLs")
	}
	for _, u := range b.AllowedReturnURLs {
		if !slices.Contains(d.cfg.AllowedReturnURLs, u) {
			return errors.New("workload return URL is not allowlisted")
		}
	}
	if action == "update" && len(b.Tags) > 0 {
		return errors.New("UpdateWorkloadIdentity does not accept tags")
	}
	return validateTags(b.Tags)
}
func validFingerprint(v string) bool {
	if !strings.HasPrefix(v, "sha256:") || len(v) != 71 {
		return false
	}
	for _, r := range strings.TrimPrefix(v, "sha256:") {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

func (d *Driver) validProviderARN(arn, name, kind string) bool {
	collection := "oauth2credentialprovider"
	if kind == "apiKeyCredentialProviders" {
		collection = "apikeycredentialprovider"
	}
	resource := "token-vault/" + d.cfg.TokenVaultID + "/" + collection + "/" + name
	// AWS APIReference still documents the legacy acps service component for
	// provider ARN responses. Both exact scoped forms are accepted as metadata;
	// operation references always use the canonical bedrock-agentcore form.
	return arn == d.cfg.arn("bedrock-agentcore", resource) || arn == d.cfg.arn("acps", resource)
}
func responseName(raw json.RawMessage) string {
	var v struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(raw, &v)
	return v.Name
}
