package aws

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/vamsiramakrishnan/aiplex/pkg/nativeapi"
)

// CredentialResult is the preferred library result for credential exchanges.
// Formatting/JSON never discloses credentials. Explicit accessors are for the
// consuming agent's outbound transport, not public API responses or audit logs.
type CredentialResult struct{ workloadToken, accessToken, apiKey, authorizationURL, sessionURI, sessionStatus string }

func (CredentialResult) String() string               { return "AgentCore credential result (redacted)" }
func (CredentialResult) GoString() string             { return "AgentCore credential result (redacted)" }
func (CredentialResult) MarshalJSON() ([]byte, error) { return []byte(`{"redacted":true}`), nil }
func (r *CredentialResult) WorkloadAccessToken() string {
	if r == nil {
		return ""
	}
	return r.workloadToken
}
func (r *CredentialResult) AccessToken() string {
	if r == nil {
		return ""
	}
	return r.accessToken
}
func (r *CredentialResult) APIKey() string {
	if r == nil {
		return ""
	}
	return r.apiKey
}
func (r *CredentialResult) AuthorizationURL() string {
	if r == nil {
		return ""
	}
	return r.authorizationURL
}
func (r *CredentialResult) SessionURI() string {
	if r == nil {
		return ""
	}
	return r.sessionURI
}
func (r *CredentialResult) SessionStatus() string {
	if r == nil {
		return ""
	}
	return r.sessionStatus
}

// Exchange performs a library-only native token operation with redacted result
// semantics. Driver.Execute also implements the shared raw native API contract;
// hosts must categorically refuse Credential operations on public controllers.
func (d *Driver) Exchange(ctx context.Context, op nativeapi.Operation) (*CredentialResult, error) {
	if !strings.HasPrefix(op.Name, "workloadTokens.") && !strings.HasPrefix(op.Name, "resourceCredentials.") {
		return nil, errors.New("operation is not a credential exchange")
	}
	r, e := d.Execute(ctx, op)
	if e != nil {
		return nil, e
	}
	var v struct {
		WorkloadToken    string `json:"workloadAccessToken"`
		AccessToken      string `json:"accessToken"`
		APIKey           string `json:"apiKey"`
		AuthorizationURL string `json:"authorizationUrl"`
		SessionURI       string `json:"sessionUri"`
		SessionStatus    string `json:"sessionStatus"`
	}
	if json.Unmarshal(r.Body, &v) != nil {
		return nil, errors.New("invalid native credential response")
	}
	if strings.HasPrefix(op.Name, "workloadTokens.") {
		if v.WorkloadToken == "" {
			return nil, errors.New("native workload token is missing")
		}
		return &CredentialResult{workloadToken: v.WorkloadToken}, nil
	}
	if op.Name == "resourceCredentials.apiKey" {
		if v.APIKey == "" {
			return nil, errors.New("native API key is missing")
		}
		return &CredentialResult{apiKey: v.APIKey}, nil
	}
	if v.AccessToken == "" && v.AuthorizationURL == "" && v.SessionStatus == "" {
		return nil, errors.New("native OAuth result is missing")
	}
	return &CredentialResult{accessToken: v.AccessToken, authorizationURL: v.AuthorizationURL, sessionURI: v.SessionURI, sessionStatus: v.SessionStatus}, nil
}
