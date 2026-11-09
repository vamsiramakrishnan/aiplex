package cloudagents

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/vamsiramakrishnan/aiplex/pkg/contracts"
)

func requestContext() contracts.RequestContext {
	return contracts.RequestContext{Version: "v1", TenantID: "t", PrincipalID: "person", RequestID: "r", ResourceID: "connection"}
}
func baseConnection(provider Provider, endpoint string) Connection {
	return Connection{ID: "connection", TenantID: "t", Provider: provider, Endpoint: endpoint, IdentityRef: "identity", ResourceID: "external-resource", GovernanceMode: "invocation"}
}
func allow(context.Context, Action) error { return nil }
func TestNativeProviderRequestsAndStreaming(t *testing.T) {
	tests := []struct {
		provider        Provider
		operation, path string
		configure       func(*Connection, *Invocation)
	}{
		{GoogleRuntime, "stream-query", "/v1/projects/p/locations/us-central1/reasoningEngines/123:streamQuery", func(c *Connection, in *Invocation) {
			c.ResourceID = "projects/p/locations/us-central1/reasoningEngines/123"
		}},
		{BedrockAgent, "invoke", "/agents/agent1/agentAliases/alias1/sessions/session1/text", func(c *Connection, in *Invocation) {
			c.AgentID = "agent1"
			c.AliasID = "alias1"
			c.Region = "us-east-1"
			in.SessionID = "session1"
		}},
		{AgentCoreRuntime, "invoke", "/runtimes/arn:aws:bedrock-agentcore:us-east-1:123456789012:runtime/agent1/invocations", func(c *Connection, in *Invocation) {
			c.ResourceID = "arn:aws:bedrock-agentcore:us-east-1:123456789012:runtime/agent1"
			c.Region = "us-east-1"
			in.SessionID = strings.Repeat("s", 33)
		}},
		{FoundryAgent, "agent-responses", "/api/projects/project1/agents/agent1/endpoint/protocols/openai/responses", func(c *Connection, in *Invocation) {
			c.Project = "project1"
			c.AgentID = "agent1"
			c.APIVersion = "2025-05-01"
		}},
		{CopilotStudio, "send-activity", "/v3/directline/conversations/conversation1/activities", func(c *Connection, in *Invocation) { in.SessionID = "conversation1" }},
	}
	for _, test := range tests {
		t.Run(string(test.provider), func(t *testing.T) {
			arrived := make(chan struct{})
			release := make(chan struct{})
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				expectedPath := test.path
				if test.provider == BedrockAgent {
					expectedPath = strings.ReplaceAll(expectedPath, "session1", scopedSession("t", "person", "connection", "session1"))
				}
				if r.URL.Path != expectedPath {
					t.Errorf("path %s", r.URL.Path)
				}
				if r.Header.Get("Authorization") != "Bearer token" && !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ") {
					t.Error("missing credential")
				}
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(200)
				_, _ = w.Write([]byte("first\n"))
				w.(http.Flusher).Flush()
				close(arrived)
				<-release
				_, _ = w.Write([]byte("last\n"))
			}))
			defer server.Close()
			c := baseConnection(test.provider, server.URL)
			in := Invocation{Operation: test.operation, Payload: []byte(`{"native":true}`)}
			test.configure(&c, &in)
			sessions := NewMemorySessionStore()
			_ = sessions.Bind(context.Background(), "t", "person", "connection", "conversation1")
			var credentials Authenticator = BearerCredentials{Token: func(context.Context) (string, error) { return "token", nil }}
			if c.Provider == BedrockAgent {
				credentials = SigV4Credentials{Source: aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
					return aws.Credentials{AccessKeyID: "test", SecretAccessKey: "test"}, nil
				}), Region: c.Region, Service: "bedrock"}
			}
			invoker, e := NewInvoker(c, AuthorizeFunc(allow), server.Client(), credentials, WithSessionStore(sessions))
			if e != nil {
				t.Fatal(e)
			}
			response, e := invoker.Invoke(context.Background(), requestContext(), in)
			if e != nil {
				close(release)
				t.Fatal(e)
			}
			defer response.Body.Close()
			<-arrived
			buf := make([]byte, 6)
			if _, e = io.ReadFull(response.Body, buf); e != nil || string(buf) != "first\n" {
				t.Fatalf("stream buffered/changed: %s %v", buf, e)
			}
			close(release)
		})
	}
}
func TestAuthorizationPrecedesCredentialAndNetwork(t *testing.T) {
	calls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer server.Close()
	credentials := 0
	v, e := NewInvoker(baseConnection(CopilotStudio, server.URL), AuthorizeFunc(func(context.Context, Action) error { return errors.New("denied") }), server.Client(), BearerCredentials{Token: func(context.Context) (string, error) { credentials++; return "token", nil }}, WithSessionStore(NewMemorySessionStore()))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = v.Invoke(context.Background(), requestContext(), Invocation{Operation: "start-conversation"}); e == nil || credentials != 0 || calls != 0 {
		t.Fatalf("authorization bypass %v %d %d", e, credentials, calls)
	}
	request := requestContext()
	request.TenantID = "other"
	if _, e = v.Invoke(context.Background(), request, Invocation{Operation: "start-conversation"}); e == nil {
		t.Fatal("cross tenant accepted")
	}
}
func TestSigV4UsesOfficialSignerAndSessionCredentials(t *testing.T) {
	req, _ := http.NewRequest("POST", "https://bedrock-agentcore.us-east-1.amazonaws.com/runtimes/arn%3Aaws%3Abedrock-agentcore%3Aus-east-1%3A123456789012%3Aruntime%2Fagent/invocations", strings.NewReader(`{}`))
	credentials := SigV4Credentials{Source: aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
		return aws.Credentials{AccessKeyID: "TESTACCESS", SecretAccessKey: "test-secret", SessionToken: "test-session"}, nil
	}), Region: "us-east-1", Service: "bedrock-agentcore", Now: func() time.Time { return time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC) }}
	if e := credentials.Authenticate(context.Background(), req); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(req.Header.Get("Authorization"), "20261109/us-east-1/bedrock-agentcore/aws4_request") || req.Header.Get("X-Amz-Security-Token") != "test-session" {
		t.Fatal(req.Header)
	}
}
func TestMalformedNativeResourceRejected(t *testing.T) {
	c := baseConnection(AgentCoreRuntime, "https://service.example")
	c.Region = "us-east-1"
	c.ResourceID = "arn:aws:bedrock-agentcore:us-west-2:123456789012:runtime/a"
	if c.Validate() == nil {
		t.Fatal("cross-region runtime accepted")
	}
	c = baseConnection(CopilotStudio, "https://service.example/path")
	if c.Validate() == nil {
		t.Fatal("endpoint path accepted")
	}
}

func TestDirectLineConversationOwnershipAndAWSSessionIsolation(t *testing.T) {
	sessions := NewMemorySessionStore()
	if e := sessions.Bind(context.Background(), "t", "alice", "c", "conversation"); e != nil {
		t.Fatal(e)
	}
	if e := sessions.Check(context.Background(), "t", "bob", "c", "conversation"); e == nil {
		t.Fatal("other principal resumed conversation")
	}
	if e := sessions.Bind(context.Background(), "t", "bob", "c", "conversation"); e == nil {
		t.Fatal("conversation adopted by another principal")
	}
	if scopedSession("t", "alice", "c", "same") == scopedSession("t", "bob", "c", "same") {
		t.Fatal("AWS sessions cross principal")
	}
	if scopedSession("t", "alice", "c", "same") != scopedSession("t", "alice", "c", "same") {
		t.Fatal("AWS session loses continuity")
	}
}
func TestDirectLineStartBindsReturnedConversation(t *testing.T) {
	sessions := NewMemorySessionStore()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"conversationId":"newconversation","token":"native-token"}`))
	}))
	defer server.Close()
	v, e := NewInvoker(baseConnection(CopilotStudio, server.URL), AuthorizeFunc(allow), server.Client(), BearerCredentials{Token: func(context.Context) (string, error) { return "token", nil }}, WithSessionStore(sessions))
	if e != nil {
		t.Fatal(e)
	}
	response, e := v.Invoke(context.Background(), requestContext(), Invocation{Operation: "start-conversation"})
	if e != nil {
		t.Fatal(e)
	}
	data, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if !strings.Contains(string(data), "native-token") {
		t.Fatal("native response changed")
	}
	if e = sessions.Check(context.Background(), "t", "person", "connection", "newconversation"); e != nil {
		t.Fatal(e)
	}
}
func TestFoundryBoundAgentCannotSelectAnotherAgentOrModel(t *testing.T) {
	c := baseConnection(FoundryAgent, "https://foundry.example")
	c.Project = "p"
	c.APIVersion = "2025-05-01"
	c.AgentID = "approved"
	v, e := NewInvoker(c, AuthorizeFunc(allow), http.DefaultClient, BearerCredentials{Token: func(context.Context) (string, error) {
		t.Fatal("credential fetched for denied selector")
		return "", nil
	}})
	if e != nil {
		t.Fatal(e)
	}
	for _, in := range []Invocation{{Operation: "responses"}, {Operation: "agent-responses", Payload: []byte(`{"agent":"other"}`)}, {Operation: "agent-responses", Payload: []byte(`{"model":"other"}`)}} {
		if _, e = v.Invoke(context.Background(), requestContext(), in); e == nil {
			t.Fatal("Foundry selector override accepted")
		}
	}
}
func TestSigV4ConnectionBinding(t *testing.T) {
	c := baseConnection(AgentCoreRuntime, "https://bedrock-agentcore.us-east-1.amazonaws.com")
	c.Region = "us-east-1"
	c.ResourceID = "arn:aws:bedrock-agentcore:us-east-1:123456789012:runtime/agent"
	if _, e := NewInvoker(c, AuthorizeFunc(allow), http.DefaultClient, SigV4Credentials{Region: "us-west-2", Service: "bedrock-agentcore"}); e == nil {
		t.Fatal("cross-region signer accepted")
	}
}

func TestCopilotTokenEndpointAcquisition(t *testing.T) {
	calls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" || r.URL.Path != "/published/token" {
			t.Error(r.URL)
		}
		_, _ = w.Write([]byte(`{"token":"short-lived","expires_in":1800}`))
	}))
	defer server.Close()
	credentials, e := NewCopilotTokenCredentials(server.URL+"/published/token", server.Client(), nil)
	if e != nil {
		t.Fatal(e)
	}
	req, _ := http.NewRequest("POST", "https://directline.botframework.com/v3/directline/conversations", nil)
	if e = credentials.Authenticate(context.Background(), req); e != nil {
		t.Fatal(e)
	}
	if calls != 1 || req.Header.Get("Authorization") != "Bearer short-lived" {
		t.Fatal("token acquisition failed")
	}
}

func TestCopilotTokenEndpointCannotMintFreshTokenForContinuation(t *testing.T) {
	calls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{"token":"new-conversation-token"}`))
	}))
	defer server.Close()
	credentials, e := NewCopilotTokenCredentials(server.URL+"/token", server.Client(), nil)
	if e != nil {
		t.Fatal(e)
	}
	for _, method := range []string{"GET", "POST"} {
		req, _ := http.NewRequest(method, "https://directline.botframework.com/v3/directline/conversations/existing/activities", nil)
		if e = credentials.Authenticate(context.Background(), req); e == nil {
			t.Fatal("continuation accepted a newly generated token")
		}
	}
	if calls != 0 {
		t.Fatal("credential endpoint contacted for continuation")
	}
}
