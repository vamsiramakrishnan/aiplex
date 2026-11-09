package connections

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/vamsiramakrishnan/aiplex/pkg/cloudagents"
	"github.com/vamsiramakrishnan/aiplex/pkg/nativeapi"
)

// GovernanceProvider is operator-owned configuration. Native authority options
// are distinct from the Aiplex tenant namespace. Secrets are credential refs.
type GovernanceProvider struct {
	ID          string          `json:"id"`
	TenantID    string          `json:"tenant_id"`
	Type        string          `json:"type"`
	Endpoint    string          `json:"endpoint"`
	IdentityRef string          `json:"identity_ref"`
	Region      string          `json:"region,omitempty"`
	Options     json.RawMessage `json:"options"`
	AllowApply  bool            `json:"allow_apply,omitempty"`
}

// GovernanceClient pins both the network origin and the credential destination.
// Its fingerprint records reviewed configuration without persisting credentials.
func (r *Runtime) GovernanceClient(spec GovernanceProvider) (*nativeapi.Client, string, error) {
	if spec.ID == "" || spec.TenantID == "" || spec.Type == "" || !json.Valid(spec.Options) {
		return nil, "", fmt.Errorf("governance provider identity and JSON options required")
	}
	if r.Policy == nil || r.HTTP == nil {
		return nil, "", fmt.Errorf("governance provider needs an explicit endpoint policy")
	}
	if err := r.Policy.ValidateURL(spec.Endpoint); err != nil {
		return nil, "", fmt.Errorf("governance endpoint is not allowed")
	}
	if err := r.CheckDestination(spec.TenantID, spec.IdentityRef, spec.Endpoint); err != nil {
		return nil, "", err
	}
	identity, err := r.Identity(spec.TenantID, spec.IdentityRef)
	if err != nil {
		return nil, "", err
	}
	if spec.Type == "aws-agentcore-identity" && identity.Type != "aws-default" {
		return nil, "", fmt.Errorf("AgentCore control-plane governance requires account-attested SigV4 credentials")
	}
	var authenticate nativeapi.Authenticator
	if identity.Type == "aws-default" {
		if spec.Region == "" || spec.Type != "aws-agentcore-identity" {
			return nil, "", fmt.Errorf("AWS governance credentials require AgentCore region")
		}
		source, err := r.governanceAWSCredentials(spec)
		if err != nil {
			return nil, "", err
		}
		authenticate = cloudagents.SigV4Credentials{Source: source, Region: spec.Region, Service: "bedrock-agentcore"}
	} else {
		if identity.Type == "azure-default" && len(identity.Scopes) == 0 {
			return nil, "", fmt.Errorf("native Graph governance requires an explicit credential audience scope")
		}
		var authority struct {
			TenantID string `json:"tenant_id"`
		}
		if spec.Type == "entra-agent-id" && (json.Unmarshal(spec.Options, &authority) != nil || authority.TenantID == "") {
			return nil, "", fmt.Errorf("native Entra authority tenant required")
		}
		var attestMu sync.Mutex
		var attested [sha256.Size]byte
		var attestedOK bool
		authenticate = cloudagents.BearerCredentials{Token: func(ctx context.Context) (string, error) {
			token, err := r.Token(ctx, spec.TenantID, spec.IdentityRef)
			if err != nil {
				return "", err
			}
			if spec.Type == "entra-agent-id" {
				attestMu.Lock()
				defer attestMu.Unlock()
				fingerprint := sha256.Sum256([]byte(token))
				if (!attestedOK || fingerprint != attested) && verifyGraphAuthority(ctx, r.HTTP, spec.Endpoint, token, authority.TenantID) != nil {
					return "", fmt.Errorf("Graph credential native authority mismatch")
				}
				attested, attestedOK = fingerprint, true
			}
			return token, nil
		}}
	}
	client, err := nativeapi.NewClient(spec.Endpoint, r.HTTP, authenticate)
	if err != nil {
		return nil, "", err
	}
	var options any
	if err = json.Unmarshal(spec.Options, &options); err != nil {
		return nil, "", err
	}
	material, err := json.Marshal(struct {
		Provider GovernanceProvider `json:"provider"`
		Identity Identity           `json:"identity"`
		Network  any                `json:"network"`
		Options  any                `json:"canonical_options"`
	}{spec, identity, r.Config.Network, options})
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(material)
	return client, hex.EncodeToString(digest[:]), nil
}

func (r *Runtime) governanceAWSCredentials(spec GovernanceProvider) (aws.CredentialsProvider, error) {
	key := spec.TenantID + "\x00" + spec.IdentityRef + "\x00" + spec.Region
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.governanceAWS == nil {
		r.governanceAWS = map[string]aws.CredentialsProvider{}
	}
	if source := r.governanceAWS[key]; source != nil {
		return source, nil
	}
	cfg, err := awsconfig.LoadDefaultConfig(context.Background(), awsconfig.WithRegion(spec.Region))
	if err != nil {
		return nil, fmt.Errorf("AWS governance credential chain unavailable")
	}
	r.governanceAWS[key] = cfg.Credentials
	return cfg.Credentials, nil
}

// Graph access tokens are opaque to clients. Ask Graph to attest the native
// organization using the same token before a name-based creation can occur.
// This requires Organization.Read.All in addition to native Agent ID grants.
func verifyGraphAuthority(ctx context.Context, httpClient *http.Client, origin, token, tenant string) error {
	client, err := nativeapi.NewClient(origin, httpClient, cloudagents.BearerCredentials{Token: func(context.Context) (string, error) { return token, nil }})
	if err != nil {
		return fmt.Errorf("Graph authority client unavailable")
	}
	result, err := client.Do(ctx, http.MethodGet, "v1.0/organization", url.Values{"$select": []string{"id"}}, nil)
	if err != nil {
		return fmt.Errorf("Graph authority verification failed")
	}
	var response struct {
		Value []struct {
			ID string `json:"id"`
		} `json:"value"`
	}
	if json.Unmarshal(result.Body, &response) != nil || len(response.Value) != 1 || !strings.EqualFold(response.Value[0].ID, tenant) {
		return fmt.Errorf("Graph organization does not match native tenant")
	}
	return nil
}
