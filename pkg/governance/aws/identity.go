// Package aws governs native AgentCore workload identities and credential
// providers. Runtime invocation identities are deliberately separate resources.
package aws

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/vamsiramakrishnan/aiplex/pkg/nativeapi"
)

// Config is operator supplied. Empty allowlists grant no resource access.
// AWS currently exposes identities within the default directory, not directory
// creation/deletion. This adapter has no synthetic disable operation.
type Config struct {
	AccountID            string   `json:"account_id"`
	Region               string   `json:"region"`
	Partition            string   `json:"partition,omitempty"`
	DirectoryID          string   `json:"directory_id,omitempty"`
	TokenVaultID         string   `json:"token_vault_id,omitempty"`
	AllowedWorkloadNames []string `json:"allowed_workload_names,omitempty"`
	AllowedProviderNames []string `json:"allowed_provider_names,omitempty"`
	AllowedReturnURLs    []string `json:"allowed_return_urls,omitempty"`
	AllowedScopes        []string `json:"allowed_scopes,omitempty"`
	AllowedOAuthFlows    []string `json:"allowed_oauth_flows,omitempty"`
	AllowedAudiences     []string `json:"allowed_audiences,omitempty"`
	AllowedResources     []string `json:"allowed_resources,omitempty"`
	AllowedKMSKeyARNs    []string `json:"allowed_kms_key_arns,omitempty"`
	AllowUserIDExchange  bool     `json:"allow_user_id_exchange,omitempty"`
	// Native update/delete APIs have no etag/CAS. Explicit opt-in and a fresh
	// fingerprint preflight detect drift but cannot close the cloud write race.
	AllowNonAtomicMutations bool `json:"allow_non_atomic_mutations,omitempty"`
}

var workloadName = regexp.MustCompile(`^[A-Za-z0-9_.-]{3,255}$`)
var providerName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
var accountID = regexp.MustCompile(`^[0-9]{12}$`)
var regionName = regexp.MustCompile(`^[a-z]{2}(?:-[a-z]+)+-[0-9]+$`)
var uuid = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func (c *Config) defaults() {
	if c.Partition == "" {
		c.Partition = "aws"
	}
	if c.DirectoryID == "" {
		c.DirectoryID = "default"
	}
	if c.TokenVaultID == "" {
		c.TokenVaultID = "default"
	}
}
func (c Config) Validate() error {
	c.defaults()
	if !accountID.MatchString(c.AccountID) || !regionName.MatchString(c.Region) || !slices.Contains([]string{"aws", "aws-us-gov", "aws-cn"}, c.Partition) || c.DirectoryID != "default" || c.TokenVaultID != "default" {
		return errors.New("invalid AgentCore account, region, partition, directory or vault")
	}
	if (c.Partition == "aws-cn") != strings.HasPrefix(c.Region, "cn-") || (c.Partition == "aws-us-gov") != strings.HasPrefix(c.Region, "us-gov-") {
		return errors.New("AgentCore partition and region disagree")
	}
	for _, n := range c.AllowedWorkloadNames {
		if !workloadName.MatchString(n) {
			return errors.New("invalid allowed workload name")
		}
	}
	for _, n := range c.AllowedProviderNames {
		if !providerName.MatchString(n) {
			return errors.New("invalid allowed provider name")
		}
	}
	for _, u := range c.AllowedReturnURLs {
		if !validReturnURL(u) {
			return errors.New("allowed OAuth return URL requires HTTPS")
		}
	}
	for _, f := range c.AllowedOAuthFlows {
		if !slices.Contains([]string{"M2M", "USER_FEDERATION", "ON_BEHALF_OF_TOKEN_EXCHANGE"}, f) {
			return errors.New("invalid allowed OAuth flow")
		}
	}
	for _, a := range c.AllowedKMSKeyARNs {
		if !c.validKMSKey(a) {
			return errors.New("allowed KMS key is outside configured account/region")
		}
	}
	return nil
}
func (c Config) arn(service, resource string) string {
	return "arn:" + c.Partition + ":" + service + ":" + c.Region + ":" + c.AccountID + ":" + resource
}
func (c Config) DirectoryARN() string {
	c.defaults()
	return c.arn("bedrock-agentcore", "workload-identity-directory/"+c.DirectoryID)
}
func (c Config) WorkloadARN(name string) string {
	return c.DirectoryARN() + "/workload-identity/" + name
}
func (c Config) VaultARN() string {
	c.defaults()
	return c.arn("bedrock-agentcore", "token-vault/"+c.TokenVaultID)
}
func (c Config) validKMSKey(a string) bool {
	p := c.arn("kms", "key/")
	return strings.HasPrefix(a, p) && uuid.MatchString(strings.TrimPrefix(a, p))
}
func validReturnURL(raw string) bool {
	u, e := url.Parse(raw)
	return e == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && u.Fragment == "" && len(raw) <= 2048
}

type Driver struct {
	cfg     Config
	control *nativeapi.Client
	data    *nativeapi.Client
	broker  TokenBroker
	account AccountVerifier
}

func New(cfg Config, client *nativeapi.Client) (*Driver, error) {
	cfg.defaults()
	if e := cfg.Validate(); e != nil {
		return nil, e
	}
	if client == nil {
		return nil, errors.New("AgentCore control-plane client is required")
	}
	cfg.AllowedWorkloadNames = slices.Clone(cfg.AllowedWorkloadNames)
	cfg.AllowedProviderNames = slices.Clone(cfg.AllowedProviderNames)
	cfg.AllowedReturnURLs = slices.Clone(cfg.AllowedReturnURLs)
	cfg.AllowedScopes = slices.Clone(cfg.AllowedScopes)
	cfg.AllowedOAuthFlows = slices.Clone(cfg.AllowedOAuthFlows)
	cfg.AllowedAudiences = slices.Clone(cfg.AllowedAudiences)
	cfg.AllowedResources = slices.Clone(cfg.AllowedResources)
	cfg.AllowedKMSKeyARNs = slices.Clone(cfg.AllowedKMSKeyARNs)
	return &Driver{cfg: cfg, control: client}, nil
}

func (d *Driver) Operations() []nativeapi.OperationInfo {
	var out []nativeapi.OperationInfo
	for _, kind := range []string{"workloadIdentities", "oauth2CredentialProviders", "apiKeyCredentialProviders"} {
		for _, a := range []string{"list", "get", "create", "update", "delete"} {
			out = append(out, nativeapi.OperationInfo{Name: kind + "." + a, Mutating: a == "create" || a == "update" || a == "delete", Credential: kind != "workloadIdentities" && (a == "create" || a == "update"), Description: "Native AgentCore " + kind + " " + a})
		}
	}
	out = append(out, nativeapi.OperationInfo{Name: "tokenVaults.get", Description: "Read native token-vault KMS metadata"}, nativeapi.OperationInfo{Name: "tokenVaults.setCMK", Mutating: true, Description: "Set native token-vault KMS configuration; non-atomic"})
	for _, n := range []string{"workloadTokens.get", "workloadTokens.forJWT", "workloadTokens.forUserID", "resourceCredentials.oauth2", "resourceCredentials.apiKey"} {
		out = append(out, nativeapi.OperationInfo{Name: n, Credential: true, Description: "Library-only native credential exchange requiring a trusted TokenBroker"})
	}
	return out
}

func strict(raw json.RawMessage, dst any) error {
	if len(raw) == 0 {
		return errors.New("AgentCore payload is required")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(dst) != nil || d.Decode(new(any)) != io.EOF {
		return errors.New("invalid AgentCore payload")
	}
	return nil
}
func empty(raw json.RawMessage) error {
	b := bytes.TrimSpace(raw)
	if len(b) > 0 && string(b) != "null" && string(b) != "{}" {
		return errors.New("operation accepts no payload")
	}
	return nil
}
func fingerprint(raw json.RawMessage) (string, error) {
	var v any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if d.Decode(&v) != nil {
		return "", errors.New("invalid AgentCore response")
	}
	b, e := json.Marshal(v)
	if e != nil {
		return "", e
	}
	h := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(h[:]), nil
}
func (d *Driver) name(resource, kind string) (string, error) {
	prefix := d.cfg.DirectoryARN() + "/workload-identity/"
	pattern := workloadName
	allowed := d.cfg.AllowedWorkloadNames
	if kind != "workloadIdentities" {
		p := "oauth2credentialprovider"
		if kind == "apiKeyCredentialProviders" {
			p = "apikeycredentialprovider"
		}
		prefix = d.cfg.arn("bedrock-agentcore", "token-vault/"+d.cfg.TokenVaultID+"/"+p+"/")
		pattern = providerName
		allowed = d.cfg.AllowedProviderNames
	}
	if !strings.HasPrefix(resource, prefix) {
		return "", errors.New("AgentCore resource is outside configured identity scope")
	}
	n := strings.TrimPrefix(resource, prefix)
	if !pattern.MatchString(n) || !slices.Contains(allowed, n) {
		return "", errors.New("AgentCore resource is not allowlisted")
	}
	return n, nil
}

// ProviderMetadata is the complete public provider result. Native provider
// configuration, secret references, callbacks and arbitrary extension fields
// never enter inventory or reconciliation journals.
type ProviderMetadata struct {
	Name                     string      `json:"name"`
	CredentialProviderARN    string      `json:"credentialProviderArn,omitempty"`
	CredentialProviderVendor string      `json:"credentialProviderVendor,omitempty"`
	CreatedTime              json.Number `json:"createdTime,omitempty"`
	LastUpdatedTime          json.Number `json:"lastUpdatedTime,omitempty"`
	Status                   string      `json:"status,omitempty"`
}
type WorkloadMetadata struct {
	Name                string      `json:"name"`
	WorkloadIdentityARN string      `json:"workloadIdentityArn"`
	AllowedReturnURLs   []string    `json:"allowedResourceOauth2ReturnUrls,omitempty"`
	CreatedTime         json.Number `json:"createdTime,omitempty"`
	LastUpdatedTime     json.Number `json:"lastUpdatedTime,omitempty"`
}
type listRequest struct {
	MaxResults int    `json:"maxResults,omitempty"`
	NextToken  string `json:"nextToken,omitempty"`
}
type workloadRequest struct {
	Name              string            `json:"name,omitempty"`
	AllowedReturnURLs []string          `json:"allowedResourceOauth2ReturnUrls"`
	Tags              map[string]string `json:"tags,omitempty"`
}

func (d *Driver) do(ctx context.Context, path string, body any) (nativeapi.Result, error) {
	return d.control.Do(ctx, http.MethodPost, "identities/"+path, nil, body)
}
func (d *Driver) publicResult(r nativeapi.Result, kind, action string) (nativeapi.Result, error) {
	if string(r.Body) == "null" {
		return nativeapi.Result{}, errors.New("native identity response is empty")
	}
	var observedTag string
	if action == "get" {
		var e error
		observedTag, e = fingerprint(r.Body)
		if e != nil {
			return nativeapi.Result{}, e
		}
	}
	if kind == "workloadIdentities" {
		if action == "list" {
			var v struct {
				Items     []WorkloadMetadata `json:"workloadIdentities"`
				NextToken string             `json:"nextToken,omitempty"`
			}
			if json.Unmarshal(r.Body, &v) != nil {
				return nativeapi.Result{}, errors.New("invalid workload inventory")
			}
			v.Items = slices.DeleteFunc(v.Items, func(i WorkloadMetadata) bool {
				return !slices.Contains(d.cfg.AllowedWorkloadNames, i.Name) || i.WorkloadIdentityARN != d.cfg.WorkloadARN(i.Name)
			})
			r.Body, _ = json.Marshal(v)
		} else {
			var v WorkloadMetadata
			if json.Unmarshal(r.Body, &v) != nil || v.Name == "" || !slices.Contains(d.cfg.AllowedWorkloadNames, v.Name) || v.WorkloadIdentityARN != d.cfg.WorkloadARN(v.Name) {
				return nativeapi.Result{}, errors.New("workload response is outside configured scope")
			}
			r.Body, _ = json.Marshal(v)
		}
	} else {
		if action == "list" {
			var v struct {
				Items     []ProviderMetadata `json:"credentialProviders"`
				NextToken string             `json:"nextToken,omitempty"`
			}
			if json.Unmarshal(r.Body, &v) != nil {
				return nativeapi.Result{}, errors.New("invalid provider inventory")
			}
			v.Items = slices.DeleteFunc(v.Items, func(i ProviderMetadata) bool {
				return !slices.Contains(d.cfg.AllowedProviderNames, i.Name) || !d.validProviderARN(i.CredentialProviderARN, i.Name, kind)
			})
			r.Body, _ = json.Marshal(v)
		} else {
			var v ProviderMetadata
			if json.Unmarshal(r.Body, &v) != nil || v.Name == "" || !slices.Contains(d.cfg.AllowedProviderNames, v.Name) || !d.validProviderARN(v.CredentialProviderARN, v.Name, kind) {
				return nativeapi.Result{}, errors.New("provider response is outside configured scope")
			}
			r.Body, _ = json.Marshal(v)
		}
	}
	if action == "get" {
		r.ETag = observedTag
	}
	return r, nil
}

func nativeKind(kind string) string {
	switch kind {
	case "workloadIdentities":
		return "WorkloadIdentity"
	case "oauth2CredentialProviders":
		return "Oauth2CredentialProvider"
	case "apiKeyCredentialProviders":
		return "ApiKeyCredentialProvider"
	}
	return ""
}
func (d *Driver) Execute(ctx context.Context, op nativeapi.Operation) (nativeapi.Result, error) {
	if e := d.ValidateOperation(op); e != nil {
		return nativeapi.Result{}, e
	}
	parts := strings.Split(op.Name, ".")
	if len(parts) != 2 {
		return nativeapi.Result{}, errors.New("unknown AgentCore operation")
	}
	kind, action := parts[0], parts[1]
	if kind == "workloadTokens" || kind == "resourceCredentials" {
		return d.credentials(ctx, op)
	}
	if e := d.verifyAccount(ctx); e != nil {
		return nativeapi.Result{}, e
	}
	if kind == "tokenVaults" {
		return d.vault(ctx, op)
	}
	nk := nativeKind(kind)
	if nk == "" || !slices.Contains([]string{"list", "get", "create", "update", "delete"}, action) {
		return nativeapi.Result{}, errors.New("unknown AgentCore operation")
	}
	if action == "list" {
		parent := d.cfg.DirectoryARN()
		if kind != "workloadIdentities" {
			parent = d.cfg.VaultARN()
		}
		if op.Resource != parent || op.ETag != "" {
			return nativeapi.Result{}, errors.New("inventory requires the configured directory or vault")
		}
		var b listRequest
		if len(op.Payload) > 0 {
			if e := strict(op.Payload, &b); e != nil {
				return nativeapi.Result{}, e
			}
		}
		if b.MaxResults < 0 || b.MaxResults > 20 || len(b.NextToken) > 8192 {
			return nativeapi.Result{}, errors.New("invalid AgentCore pagination")
		}
		plural := nk + "s"
		if kind == "workloadIdentities" {
			plural = "WorkloadIdentities"
		}
		r, e := d.do(ctx, "List"+plural, b)
		if e != nil {
			return r, e
		}
		return d.publicResult(r, kind, action)
	}
	n, e := d.name(op.Resource, kind)
	if e != nil {
		return nativeapi.Result{}, e
	}
	named := map[string]string{"name": n}
	if action == "get" {
		if e = empty(op.Payload); e != nil {
			return nativeapi.Result{}, e
		}
		r, e := d.do(ctx, "Get"+nk, named)
		if e != nil {
			return r, e
		}
		if responseName(r.Body) != n {
			return nativeapi.Result{}, errors.New("native identity response name does not match requested resource")
		}
		return d.publicResult(r, kind, action)
	}
	if action == "create" && op.ETag != "" {
		return nativeapi.Result{}, errors.New("create does not accept an etag")
	}
	var body any = named
	if action == "delete" {
		if e = empty(op.Payload); e != nil {
			return nativeapi.Result{}, e
		}
	} else if kind == "workloadIdentities" {
		var b workloadRequest
		if e = strict(op.Payload, &b); e != nil {
			return nativeapi.Result{}, e
		}
		if b.Name != "" && b.Name != n {
			return nativeapi.Result{}, errors.New("workload body/resource mismatch")
		}
		b.Name = n
		if b.AllowedReturnURLs == nil {
			b.AllowedReturnURLs = []string{}
		}
		for _, u := range b.AllowedReturnURLs {
			if !slices.Contains(d.cfg.AllowedReturnURLs, u) {
				return nativeapi.Result{}, errors.New("workload return URL is not allowlisted")
			}
		}
		if action == "update" && len(b.Tags) > 0 {
			return nativeapi.Result{}, errors.New("UpdateWorkloadIdentity does not accept tags")
		}
		if e = validateTags(b.Tags); e != nil {
			return nativeapi.Result{}, e
		}
		body = b
	} else {
		body, e = d.providerBody(kind, action, n, op.Payload)
		if e != nil {
			return nativeapi.Result{}, e
		}
	}
	if action != "create" {
		if !d.cfg.AllowNonAtomicMutations || !strings.HasPrefix(op.ETag, "sha256:") {
			return nativeapi.Result{}, errors.New("AgentCore mutation requires explicit non-atomic opt-in and observed fingerprint")
		}
		r, e := d.do(ctx, "Get"+nk, named)
		if e != nil {
			return r, e
		}
		if responseName(r.Body) != n {
			return nativeapi.Result{}, errors.New("native identity response name does not match requested resource")
		}
		r, e = d.publicResult(r, kind, "get")
		if e != nil {
			return nativeapi.Result{}, e
		}
		if r.ETag != op.ETag {
			return nativeapi.Result{}, errors.New("AgentCore observation changed; review a new plan")
		}
	}
	verb := map[string]string{"create": "Create", "update": "Update", "delete": "Delete"}[action]
	r, e := d.do(ctx, verb+nk, body)
	if e != nil {
		return r, e
	}
	if action == "delete" {
		r.Body = json.RawMessage("null")
		return r, nil
	}
	if responseName(r.Body) != n {
		return nativeapi.Result{}, errors.New("native identity response name does not match requested resource")
	}
	return d.publicResult(r, kind, action)
}

func validateTags(v map[string]string) error {
	if len(v) > 50 {
		return errors.New("too many AgentCore tags")
	}
	for k, s := range v {
		if len(k) < 1 || len(k) > 128 || len(s) > 256 || strings.ContainsAny(k+s, "\r\n\x00") {
			return errors.New("invalid AgentCore tag")
		}
	}
	return nil
}
