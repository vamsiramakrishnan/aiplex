package api

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/vamsiramakrishnan/aiplex/internal/connections"
	"github.com/vamsiramakrishnan/aiplex/pkg/cloudagents"
	"github.com/vamsiramakrishnan/aiplex/pkg/endpointpolicy"
)

type accountRoundTripper func(*http.Request) (*http.Response, error)

func (f accountRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestLinkedBedrockAttestationRequiresBothDestinationGrantsAndMatchingAccount(t *testing.T) {
	const agentOrigin = "https://bedrock-agent-runtime.us-east-1.amazonaws.com"
	const stsOrigin = "https://sts.us-east-1.amazonaws.com"
	connection := cloudagents.Connection{Provider: cloudagents.BedrockAgent, Region: "us-east-1", AgentID: "ABCDEFGHIJ", AliasID: "KLMNOPQRST", IdentityRef: "aws", GovernanceBindingRef: "binding", ResourceID: "arn:aws:bedrock:us-east-1:123456789012:agent-alias/ABCDEFGHIJ/KLMNOPQRST"}
	for _, test := range []struct {
		name               string
		global, credential bool
		account            string
		wantSuccess        bool
		wantCalls          int
	}{
		{"global policy missing", false, true, "123456789012", false, 0},
		{"credential grant missing", true, false, "123456789012", false, 0},
		{"wrong signing account", true, true, "999999999999", false, 1},
		{"same signing account", true, true, "123456789012", true, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			global, allowed := []string{agentOrigin}, []string{agentOrigin}
			if test.global {
				global = append(global, stsOrigin)
			}
			if test.credential {
				allowed = append(allowed, stsOrigin)
			}
			runtime, err := connections.New(connections.Config{Network: endpointpolicy.Config{AllowedOrigins: global}, Identities: []connections.Identity{{ID: "aws", TenantID: "tenant", Type: "aws-default", AllowedOrigins: allowed}}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			retrievals, calls := 0, 0
			credentials := sdkaws.CredentialsProviderFunc(func(context.Context) (sdkaws.Credentials, error) {
				retrievals++
				return sdkaws.Credentials{AccessKeyID: "TESTACCESS", SecretAccessKey: "test-secret"}, nil
			})
			runtime.HTTP = &http.Client{Transport: accountRoundTripper(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.String() != stsOrigin || !strings.Contains(r.Header.Get("Authorization"), "Credential=TESTACCESS/") {
					t.Fatal("attestation did not use pinned authority and supplied signing credentials")
				}
				body := "<GetCallerIdentityResponse><GetCallerIdentityResult><Account>" + test.account + "</Account><Arn>arn:aws:sts::" + test.account + ":assumed-role/test/session</Arn></GetCallerIdentityResult></GetCallerIdentityResponse>"
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
			})}
			h := &ConnectedHandler{Runtime: runtime}
			err = h.verifyLinkedBedrockAccount(context.Background(), "tenant", connection, credentials)
			if (err == nil) != test.wantSuccess || calls != test.wantCalls || retrievals != test.wantCalls {
				t.Fatalf("success=%v calls=%d retrievals=%d error=%v", err == nil, calls, retrievals, err)
			}
		})
	}
}
