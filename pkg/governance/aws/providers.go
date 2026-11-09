package aws

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/vamsiramakrishnan/aiplex/pkg/nativeapi"
)

// SecretReference uses AWS Secrets Manager rather than storing plaintext in
// desired state. Provider create/update remain confidential operations even
// when only a secret reference is used.
type SecretReference struct {
	JSONKey  string `json:"jsonKey,omitempty"`
	SecretID string `json:"secretId"`
}
type APIKeyProviderInput struct {
	Name               string            `json:"name,omitempty"`
	APIKey             string            `json:"apiKey,omitempty"`
	APIKeySecretConfig *SecretReference  `json:"apiKeySecretConfig,omitempty"`
	APIKeySecretSource string            `json:"apiKeySecretSource,omitempty"`
	Tags               map[string]string `json:"tags,omitempty"`
}
type OAuth2ProviderInput struct {
	Name          string                     `json:"name,omitempty"`
	Vendor        string                     `json:"credentialProviderVendor"`
	Configuration map[string]json.RawMessage `json:"oauth2ProviderConfigInput"`
	Tags          map[string]string          `json:"tags,omitempty"`
}

func (APIKeyProviderInput) String() string   { return "AgentCore API-key provider input (redacted)" }
func (APIKeyProviderInput) GoString() string { return "AgentCore API-key provider input (redacted)" }
func (OAuth2ProviderInput) String() string   { return "AgentCore OAuth provider input (redacted)" }
func (OAuth2ProviderInput) GoString() string { return "AgentCore OAuth provider input (redacted)" }

func (d *Driver) providerBody(kind, action, name string, raw json.RawMessage) (any, error) {
	if kind == "apiKeyCredentialProviders" {
		var v APIKeyProviderInput
		if e := strict(raw, &v); e != nil {
			return nil, e
		}
		if v.Name != "" && v.Name != name {
			return nil, errors.New("provider body/resource mismatch")
		}
		v.Name = name
		if v.APIKeySecretSource == "" {
			v.APIKeySecretSource = "MANAGED"
		}
		if v.APIKeySecretSource == "EXTERNAL" {
			if v.APIKey != "" || v.APIKeySecretConfig == nil || !d.validSecret(v.APIKeySecretConfig.SecretID) || len(v.APIKeySecretConfig.JSONKey) > 128 {
				return nil, errors.New("external API key requires a scoped Secrets Manager reference")
			}
		} else if v.APIKeySecretSource != "MANAGED" || v.APIKey == "" || len(v.APIKey) > 65536 || v.APIKeySecretConfig != nil {
			return nil, errors.New("managed API key requires an API key and no external secret")
		}
		if action == "update" && len(v.Tags) > 0 {
			return nil, errors.New("provider update does not accept tags")
		}
		if e := validateTags(v.Tags); e != nil {
			return nil, e
		}
		return v, nil
	}
	var v OAuth2ProviderInput
	if e := strict(raw, &v); e != nil {
		return nil, e
	}
	if v.Name != "" && v.Name != name {
		return nil, errors.New("provider body/resource mismatch")
	}
	v.Name = name
	vendors := []string{"GoogleOauth2", "GithubOauth2", "SlackOauth2", "SalesforceOauth2", "MicrosoftOauth2", "CustomOauth2", "AtlassianOauth2", "LinkedinOauth2", "XOauth2", "OktaOauth2", "OneLoginOauth2", "PingOneOauth2", "FacebookOauth2", "YandexOauth2", "RedditOauth2", "ZoomOauth2", "TwitchOauth2", "SpotifyOauth2", "DropboxOauth2", "NotionOauth2", "HubspotOauth2", "CyberArkOauth2", "FusionAuthOauth2", "Auth0Oauth2", "CognitoOauth2"}
	if !slices.Contains(vendors, v.Vendor) || len(v.Configuration) != 1 {
		return nil, errors.New("OAuth provider requires one documented native configuration union member")
	}
	member := map[string]string{"GoogleOauth2": "googleOauth2ProviderConfig", "GithubOauth2": "githubOauth2ProviderConfig", "SlackOauth2": "slackOauth2ProviderConfig", "SalesforceOauth2": "salesforceOauth2ProviderConfig", "MicrosoftOauth2": "microsoftOauth2ProviderConfig", "CustomOauth2": "customOauth2ProviderConfig", "AtlassianOauth2": "atlassianOauth2ProviderConfig", "LinkedinOauth2": "linkedinOauth2ProviderConfig"}[v.Vendor]
	if member == "" {
		member = "includedOauth2ProviderConfig"
	}
	value, ok := v.Configuration[member]
	if !ok || len(value) == 0 || string(value) == "null" || len(value) > 131072 {
		return nil, errors.New("OAuth provider vendor/configuration mismatch")
	}
	var nested map[string]any
	if json.Unmarshal(value, &nested) != nil || len(nested) == 0 {
		return nil, errors.New("invalid OAuth provider configuration")
	}
	// Vendor-specific native schemas are intentionally forwarded only in this
	// confidential library operation; the public controller rejects it entirely.
	if e := d.validateSecretReferences(nested); e != nil {
		return nil, e
	}
	if action == "update" && len(v.Tags) > 0 {
		return nil, errors.New("provider update does not accept tags")
	}
	if e := validateTags(v.Tags); e != nil {
		return nil, e
	}
	return v, nil
}
func (d *Driver) validSecret(arn string) bool {
	p := d.cfg.arn("secretsmanager", "secret:")
	return strings.HasPrefix(arn, p) && len(arn) > len(p) && len(arn) < 2048 && !strings.ContainsAny(arn, "\r\n\x00*")
}
func (d *Driver) validateSecretReferences(v any) error {
	switch m := v.(type) {
	case map[string]any:
		for k, v := range m {
			if k == "secretId" {
				s, ok := v.(string)
				if !ok || !d.validSecret(s) {
					return errors.New("OAuth secret reference is outside configured account/region")
				}
			}
			if e := d.validateSecretReferences(v); e != nil {
				return e
			}
		}
	case []any:
		for _, v := range m {
			if e := d.validateSecretReferences(v); e != nil {
				return e
			}
		}
	}
	return nil
}

type KMSConfiguration struct {
	KeyType   string `json:"keyType"`
	KMSKeyARN string `json:"kmsKeyArn,omitempty"`
}
type vaultMetadata struct {
	TokenVaultID     string           `json:"tokenVaultId"`
	KMSConfiguration KMSConfiguration `json:"kmsConfiguration"`
	LastModifiedDate json.Number      `json:"lastModifiedDate,omitempty"`
}
type vaultInput struct {
	TokenVaultID     string           `json:"tokenVaultId,omitempty"`
	KMSConfiguration KMSConfiguration `json:"kmsConfiguration"`
}

func (d *Driver) vault(ctx context.Context, op nativeapi.Operation) (nativeapi.Result, error) {
	if op.Resource != d.cfg.VaultARN() {
		return nativeapi.Result{}, errors.New("vault is outside configured identity scope")
	}
	if op.Name == "tokenVaults.get" {
		if e := empty(op.Payload); e != nil {
			return nativeapi.Result{}, e
		}
		r, e := d.do(ctx, "get-token-vault", map[string]string{"tokenVaultId": d.cfg.TokenVaultID})
		if e != nil {
			return r, e
		}
		return d.vaultResult(r)
	}
	if op.Name != "tokenVaults.setCMK" {
		return nativeapi.Result{}, errors.New("unknown token vault operation")
	}
	b, e := d.validateVaultInput(op)
	if e != nil {
		return nativeapi.Result{}, e
	}
	r, e := d.do(ctx, "get-token-vault", map[string]string{"tokenVaultId": d.cfg.TokenVaultID})
	if e != nil {
		return r, e
	}
	r, e = d.vaultResult(r)
	if e != nil {
		return r, e
	}
	if r.ETag != op.ETag {
		return nativeapi.Result{}, errors.New("token-vault observation changed; review a new plan")
	}
	r, e = d.do(ctx, "set-token-vault-cmk", b)
	if e != nil {
		return r, e
	}
	if string(r.Body) == "null" {
		return r, nil
	}
	return d.vaultResult(r)
}
func (d *Driver) validateVaultInput(op nativeapi.Operation) (vaultInput, error) {
	var b vaultInput
	if e := strict(op.Payload, &b); e != nil {
		return b, e
	}
	if b.TokenVaultID != "" && b.TokenVaultID != d.cfg.TokenVaultID {
		return b, errors.New("token vault body/resource mismatch")
	}
	b.TokenVaultID = d.cfg.TokenVaultID
	k := b.KMSConfiguration
	if k.KeyType == "CustomerManagedKey" {
		if !slices.Contains(d.cfg.AllowedKMSKeyARNs, k.KMSKeyARN) {
			return b, errors.New("vault KMS key is not allowlisted")
		}
	} else if k.KeyType != "ServiceManagedKey" || k.KMSKeyARN != "" {
		return b, errors.New("invalid token vault KMS configuration")
	}
	if !d.cfg.AllowNonAtomicMutations || !validFingerprint(op.ETag) {
		return b, errors.New("token-vault mutation requires non-atomic opt-in and observed fingerprint")
	}
	return b, nil
}
func (d *Driver) vaultResult(r nativeapi.Result) (nativeapi.Result, error) {
	var b vaultMetadata
	if json.Unmarshal(r.Body, &b) != nil || b.TokenVaultID != d.cfg.TokenVaultID {
		return nativeapi.Result{}, errors.New("invalid scoped token vault response")
	}
	r.Body, _ = json.Marshal(b)
	var e error
	r.ETag, e = fingerprint(r.Body)
	return r, e
}
