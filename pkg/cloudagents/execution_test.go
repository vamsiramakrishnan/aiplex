package cloudagents

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/vamsiramakrishnan/aiplex/pkg/execution"
)

type agentHookLease struct {
	events               *[]string
	claimErr, observeErr error
	claims, aborts       int
	observations         []execution.Observation
	results              []execution.Result
	resourceID           string
}

func (l *agentHookLease) AttemptID() string { return "attempt-agent" }
func (l *agentHookLease) BeforeDispatch(context.Context) error {
	l.claims++
	if l.events != nil {
		*l.events = append(*l.events, "claim")
	}
	return l.claimErr
}
func (l *agentHookLease) Observe(_ context.Context, o execution.Observation) error {
	l.observations = append(l.observations, o)
	if l.events != nil {
		*l.events = append(*l.events, "observe")
	}
	return l.observeErr
}
func (l *agentHookLease) Resource(_ context.Context, r execution.Resource) error {
	l.resourceID = r.ID
	return nil
}
func (l *agentHookLease) Finish(_ context.Context, r execution.Result) error {
	l.results = append(l.results, r)
	if l.events != nil {
		*l.events = append(*l.events, "finish")
	}
	return nil
}
func (l *agentHookLease) Abort(context.Context) error { l.aborts++; return nil }
func controlledAgent(t *testing.T, server *httptest.Server, lease *agentHookLease, credentials Authenticator, prepareErr error) *Invoker {
	t.Helper()
	c := baseConnection(GoogleRuntime, server.URL)
	c.ResourceID = "projects/p/locations/us-central1/reasoningEngines/agent"
	v, e := NewInvoker(c, AuthorizeFunc(allow), server.Client(), credentials, WithExecutionPreparer(ExecutionPreparerFunc(func(_ context.Context, in ExecutionRequest) (execution.Lease, error) {
		if lease.events != nil {
			*lease.events = append(*lease.events, "prepare")
		}
		if in.Method != "POST" || !strings.HasSuffix(in.Path, ":streamQuery") || string(in.Body) != `{"hello":"native"}` || in.Headers.Get("Authorization") != "" {
			t.Error("invalid private execution snapshot", in)
		}
		return lease, prepareErr
	})))
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func TestControlledAgentAdmissionBeforeCredentialsAndOneShotDispatch(t *testing.T) {
	var calls atomic.Int32
	events := []string{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		events = append(events, "dispatch")
		io.WriteString(w, "native-agent-result")
	}))
	defer server.Close()
	for _, denied := range []bool{true, false} {
		events = nil
		lease := &agentHookLease{events: &events}
		credentials := 0
		var rejected error
		if denied {
			rejected = &execution.StatusError{Status: 429, Code: "QUOTA_DENIED", Message: "quota exhausted"}
		}
		v := controlledAgent(t, server, lease, BearerCredentials{Token: func(context.Context) (string, error) {
			credentials++
			events = append(events, "credentials")
			return "token", nil
		}}, rejected)
		response, e := v.Invoke(context.Background(), requestContext(), Invocation{Operation: "stream-query", Payload: []byte(`{"hello":"native"}`)})
		if denied {
			if e == nil || credentials != 0 || calls.Load() != 0 {
				t.Fatal("admission denial acquired credentials/dispatched", e, credentials, calls.Load())
			}
			continue
		}
		if e != nil {
			t.Fatal(e)
		}
		body, e := io.ReadAll(response.Body)
		response.Body.Close()
		if e != nil || string(body) != "native-agent-result" || response.Header.Get(execution.AttemptHeader) != "attempt-agent" {
			t.Fatal(string(body), e)
		}
		if !reflect.DeepEqual(events, []string{"prepare", "credentials", "claim", "dispatch", "observe", "finish"}) {
			t.Fatal("wrong lifecycle order", events)
		}
		if len(lease.results) != 1 || !lease.results[0].ResponseComplete || lease.observations[0].Counters["requests"] != 1 {
			t.Fatal("HTTP-RPC completion not acknowledged", lease.results, lease.observations)
		}
	}
}
func TestControlledAgentBinaryStreamRetainsTailUntilDurableEOF(t *testing.T) {
	for _, badAck := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "unacknowledged"}[badAck], func(t *testing.T) {
			release := make(chan struct{})
			var once sync.Once
			closeRelease := func() { once.Do(func() { close(release) }) }
			defer closeRelease()
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
				w.Write([]byte{0, 1, 2, 3})
				w.(http.Flusher).Flush()
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
				w.Write([]byte{4, 5})
			}))
			defer server.Close()
			lease := &agentHookLease{}
			if badAck {
				lease.observeErr = errors.New("durability unavailable")
			}
			v := controlledAgent(t, server, lease, BearerCredentials{Token: func(context.Context) (string, error) { return "token", nil }}, nil)
			response, e := v.Invoke(context.Background(), requestContext(), Invocation{Operation: "stream-query", Payload: []byte(`{"hello":"native"}`)})
			if e != nil {
				t.Fatal(e)
			}
			defer response.Body.Close()
			first := make([]byte, 3)
			if _, e = io.ReadFull(response.Body, first); e != nil || !bytes.Equal(first, []byte{0, 1, 2}) {
				t.Fatal("early native stream buffered/changed", first, e)
			}
			if len(lease.observations) != 0 {
				t.Fatal("partial HTTP stream settled")
			}
			closeRelease()
			tail, e := io.ReadAll(response.Body)
			if badAck {
				if e == nil || bytes.Contains(tail, []byte{5}) {
					t.Fatal("unacknowledged final byte released", tail, e)
				}
			} else if e != nil || !bytes.Equal(append(first, tail...), []byte{0, 1, 2, 3, 4, 5}) {
				t.Fatal("binary native bytes changed", tail, e)
			}
		})
	}
}
func TestControlledAgentCloseOrCancelledReadRetainsUnknownExposure(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "partial")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	lease := &agentHookLease{}
	v := controlledAgent(t, server, lease, BearerCredentials{Token: func(context.Context) (string, error) { return "token", nil }}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	response, e := v.Invoke(ctx, requestContext(), Invocation{Operation: "stream-query", Payload: []byte(`{"hello":"native"}`)})
	if e != nil {
		cancel()
		t.Fatal(e)
	}
	cancel()
	response.Body.Close()
	if lease.aborts != 0 || lease.claims != 1 || len(lease.observations) != 0 || len(lease.results) != 1 || lease.results[0].ResponseComplete {
		t.Fatal("uncertain dispatch was refunded or settled", lease)
	}
}
func TestControlledAgentPreclaimFailureAbortsOnlyUndispatchedLease(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer server.Close()
	lease := &agentHookLease{claimErr: errors.New("scope changed")}
	v := controlledAgent(t, server, lease, BearerCredentials{Token: func(context.Context) (string, error) { return "token", nil }}, nil)
	if _, e := v.Invoke(context.Background(), requestContext(), Invocation{Operation: "stream-query", Payload: []byte(`{"hello":"native"}`)}); e == nil || calls.Load() != 0 || lease.aborts != 1 || len(lease.results) != 0 {
		t.Fatal("preclaim failure created provider work", e, calls.Load(), lease)
	}
}
func TestControlledCopilotConversationSeparatesDelegatedActors(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{"conversationId":"conversation1"}`) }))
	defer server.Close()
	sessions := NewMemorySessionStore()
	lease := &agentHookLease{}
	v, e := NewInvoker(baseConnection(CopilotStudio, server.URL), AuthorizeFunc(allow), server.Client(), BearerCredentials{Token: func(context.Context) (string, error) { return "token", nil }}, WithSessionStore(sessions), WithExecutionPreparer(ExecutionPreparerFunc(func(context.Context, ExecutionRequest) (execution.Lease, error) { return lease, nil })))
	if e != nil {
		t.Fatal(e)
	}
	caller := requestContext()
	caller.ActorID = "actor-a"
	response, e := v.Invoke(context.Background(), caller, Invocation{Operation: "start-conversation"})
	if e != nil {
		t.Fatal(e)
	}
	io.ReadAll(response.Body)
	response.Body.Close()
	if lease.resourceID != "conversation1" {
		t.Fatal("native session was not pinned")
	}
	caller.ActorID = "actor-b"
	if _, e = v.Invoke(context.Background(), caller, Invocation{Operation: "send-activity", SessionID: "conversation1"}); e == nil {
		t.Fatal("another delegated actor adopted conversation")
	}
}
