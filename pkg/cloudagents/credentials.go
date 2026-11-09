package cloudagents

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/signer/v4"
)

// BearerCredentials uses an injected refresh-capable token source; credentials
// are never persisted in connection records or selected from request payloads.
type BearerCredentials struct {
	Token func(context.Context) (string, error)
}

func (b BearerCredentials) Authenticate(ctx context.Context, req *http.Request) error {
	if b.Token == nil {
		return fmt.Errorf("bearer token source unavailable")
	}
	token, e := b.Token(ctx)
	if e != nil {
		return e
	}
	if token == "" || strings.ContainsAny(token, "\r\n") {
		return fmt.Errorf("invalid bearer credential")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	return nil
}

// SigV4Credentials signs native AWS requests using the official AWS SDK signer.
// Source should use IAM roles/web identity/STS rather than long-lived keys.
type SigV4Credentials struct {
	Source          aws.CredentialsProvider
	Region, Service string
	Now             func() time.Time
}

func (s SigV4Credentials) Authenticate(ctx context.Context, req *http.Request) error {
	if s.Source == nil || s.Region == "" || (s.Service != "bedrock" && s.Service != "bedrock-agentcore") {
		return fmt.Errorf("explicit AWS role credentials, region and signing service required")
	}
	creds, e := s.Source.Retrieve(ctx)
	if e != nil {
		return e
	}
	if creds.AccessKeyID == "" || creds.SecretAccessKey == "" || (creds.CanExpire && !creds.Expires.After(time.Now())) {
		return fmt.Errorf("AWS credentials missing or expired")
	}
	h := sha256.New()
	if req.Body != nil {
		if req.GetBody == nil {
			return fmt.Errorf("AWS signing requires replayable body")
		}
		body, e := req.GetBody()
		if e != nil {
			return e
		}
		defer body.Close()
		if _, e = io.Copy(h, body); e != nil {
			return e
		}
	}
	now := time.Now()
	if s.Now != nil {
		now = s.Now()
	}
	return v4.NewSigner().SignHTTP(ctx, creds, req, hex.EncodeToString(h.Sum(nil)), s.Service, s.Region, now)
}

func (s SigV4Credentials) ValidateConnection(c Connection) error {
	service := ""
	switch c.Provider {
	case BedrockAgent:
		service = "bedrock"
	case AgentCoreRuntime:
		service = "bedrock-agentcore"
	default:
		return fmt.Errorf("SigV4 is not supported for this agent provider")
	}
	if s.Region != c.Region || s.Service != service {
		return fmt.Errorf("AWS credential signer does not match connection region/service")
	}
	return nil
}

// CopilotTokenCredentials obtains the short-lived DirectLine credential from
// the exact token endpoint published by Copilot Studio (not tokens/generate).
// It supports starting conversations only; continuations require a host-owned
// conversation credential resolver or an approved DirectLine server secret.
type CopilotTokenCredentials struct {
	endpoint string
	client   *http.Client
	acquire  Authenticator
}

func NewCopilotTokenCredentials(endpoint string, client *http.Client, acquire Authenticator) (*CopilotTokenCredentials, error) {
	u, e := url.Parse(endpoint)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" || strings.ContainsAny(endpoint, "\r\n\\") {
		return nil, fmt.Errorf("Copilot token endpoint must be pinned HTTPS")
	}
	if client == nil {
		return nil, fmt.Errorf("policy-constrained token HTTP client required")
	}
	copy := *client
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &CopilotTokenCredentials{endpoint, &copy, acquire}, nil
}
func (c *CopilotTokenCredentials) Authenticate(ctx context.Context, request *http.Request) error {
	// A newly acquired DirectLine token belongs to its newly allocated
	// conversation. Generating another token cannot authorize a continuation of
	// an existing conversation. The host must resolve a stored conversation token
	// (or use an approved DirectLine server secret) for continuation.
	if request.Method != http.MethodPost || request.URL.Path != "/v3/directline/conversations" {
		return fmt.Errorf("Copilot token-endpoint credentials support start-conversation only; continuation requires a conversation-bound credential resolver or DirectLine server secret")
	}
	fetch, e := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint, nil)
	if e != nil {
		return e
	}
	if c.acquire != nil {
		if e = c.acquire.Authenticate(ctx, fetch); e != nil {
			return e
		}
	}
	response, e := c.client.Do(fetch)
	if e != nil {
		return e
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Copilot token endpoint returned HTTP %d", response.StatusCode)
	}
	data, e := io.ReadAll(io.LimitReader(response.Body, 65537))
	if e != nil {
		return e
	}
	if len(data) > 65536 {
		return fmt.Errorf("Copilot token response exceeds limit")
	}
	var credential struct {
		Token     string `json:"token"`
		ExpiresIn int64  `json:"expires_in"`
	}
	if json.Unmarshal(data, &credential) != nil || credential.Token == "" || strings.ContainsAny(credential.Token, "\r\n") {
		return fmt.Errorf("invalid Copilot token response")
	}
	request.Header.Set("Authorization", "Bearer "+credential.Token)
	return nil
}
func (c *CopilotTokenCredentials) ValidateConnection(connection Connection) error {
	if connection.Provider != CopilotStudio {
		return fmt.Errorf("Copilot credential source requires a Copilot connection")
	}
	return nil
}
