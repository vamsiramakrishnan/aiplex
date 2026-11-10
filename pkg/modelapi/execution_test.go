package modelapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/vamsiramakrishnan/aiplex/pkg/execution"
)

type modelLease struct {
	mu                                   sync.Mutex
	calls                                []string
	observations                         []execution.Observation
	results                              []execution.Result
	aborts                               int
	beforeErr, errorObserve, errorFinish error
	settled                              bool
}

func (l *modelLease) add(call string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, call)
}
func (l *modelLease) BeforeDispatch(context.Context) error { l.add("claim"); return l.beforeErr }
func (l *modelLease) Observe(_ context.Context, obs execution.Observation) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, "observe")
	l.observations = append(l.observations, obs)
	return l.errorObserve
}
func (l *modelLease) Resource(_ context.Context, r execution.Resource) error {
	l.add("resource:" + r.ID)
	return nil
}
func (l *modelLease) Finish(_ context.Context, result execution.Result) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, "finish")
	l.results = append(l.results, result)
	l.settled = result.Terminal && l.errorFinish == nil
	return l.errorFinish
}
func (l *modelLease) Abort(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, "abort")
	l.aborts++
	return ctx.Err()
}
func (*modelLease) AttemptID() string { return "attempt-1" }
func (l *modelLease) isSettled() bool { l.mu.Lock(); defer l.mu.Unlock(); return l.settled }

func (l *modelLease) terminalObservations() []execution.Observation {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []execution.Observation
	for _, value := range l.observations {
		if value.Terminal {
			out = append(out, value)
		}
	}
	return out
}

type modelRoundTrip func(*http.Request) (*http.Response, error)

func (f modelRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type failingModelReader struct{}

func (failingModelReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

type modelAckWriter struct {
	*httptest.ResponseRecorder
	onWrite func([]byte)
}

func (w modelAckWriter) Write(data []byte) (int, error) {
	if w.onWrite != nil {
		w.onWrite(data)
	}
	return w.ResponseRecorder.Write(data)
}

const controlledBody = `{"contents":[{"role":"user","parts":[{"text":"hello"}]}],"generationConfig":{"maxOutputTokens":8}}`
const controlledResponse = `{"responseId":"native-response","modelVersion":"model-v1","candidates":[{"index":0,"finishReason":"STOP","content":{"parts":[{"text":"hello"}]}}],"usageMetadata":{"promptTokenCount":7,"cachedContentTokenCount":2,"candidatesTokenCount":3,"thoughtsTokenCount":2,"totalTokenCount":12}}`
const controlledPath = "/v1/projects/test-project/locations/global/publishers/google/models/model-a:generateContent"
const controlledStreamPath = "/v1/projects/test-project/locations/global/publishers/google/models/model-a:streamGenerateContent"

func TestControlledHTTPAcknowledgesBeforeDeliveryAndIsolatesRequest(t *testing.T) {
	lease := &modelLease{}
	var gotBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lease.add("provider")
		data, _ := io.ReadAll(r.Body)
		gotBody = string(data)
		if r.URL.Query().Get("key") != "" {
			t.Error("client credential reached provider")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, controlledResponse)
	}))
	defer upstream.Close()
	opts := options(t, upstream.URL, nil)
	opts.TokenSource = TokenSourceFunc(func(context.Context) (string, error) { lease.add("token"); return "upstream-token", nil })
	opts.Execution = ExecutionPreparerFunc(func(_ context.Context, request ExecutionRequest) (execution.Lease, error) {
		lease.add("prepare")
		if request.Context.PrincipalID != "alice" || request.Metadata.Model != "model-a" || request.Path != controlledPath || request.RawQuery != "alt=json" {
			t.Fatalf("wrong resolved native facts %+v", request)
		}
		if request.Headers.Get("Authorization") != "" || request.Headers.Get("Cookie") != "" || request.Headers.Get("Idempotency-Key") != "stable" {
			t.Fatal("unsafe request header snapshot", request.Headers)
		}
		request.Body[0] = '!'
		request.Headers.Set("Idempotency-Key", "changed")
		return lease, nil
	})
	g, err := NewGateway(surface(t, upstream.URL, "models.generateContent"), opts)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", controlledPath+"?key=secret&alt=json", strings.NewReader(controlledBody))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Idempotency-Key", "stable")
	r.Header.Set("Authorization", "Bearer caller")
	r.Header.Set("Cookie", "secret=1")
	w := modelAckWriter{ResponseRecorder: httptest.NewRecorder(), onWrite: func(data []byte) {
		if !lease.isSettled() {
			t.Error("native body delivered before durable terminal acknowledgement")
		}
	}}
	g.ServeHTTP(w, r)
	if w.Code != 200 || w.Body.String() != controlledResponse || gotBody != controlledBody || r.Header.Get("Idempotency-Key") != "stable" {
		t.Fatal(w.Code, w.Body.String(), gotBody)
	}
	if w.Header().Get(execution.AttemptHeader) != "attempt-1" {
		t.Fatal("missing receipt")
	}
	terminal := lease.terminalObservations()
	if len(terminal) != 1 || !terminal[0].Cumulative || terminal[0].ModelVersion != "model-v1" {
		t.Fatalf("bad terminal evidence %+v", lease.observations)
	}
	usage := terminal[0].Counters
	if usage["uncached_input_tokens"] != 5 || usage["cached_tokens"] != 2 || usage["output_tokens"] != 3 || usage["thought_tokens"] != 2 || usage["total_tokens"] != 12 {
		t.Fatal(usage)
	}
	if strings.Join(lease.calls, ",") != "prepare,token,claim,provider,resource:native-response,observe,finish" || lease.aborts != 0 {
		t.Fatal(lease.calls, lease.aborts)
	}
}

func TestControlledFailureBoundariesAndQuotaStatus(t *testing.T) {
	for _, stage := range []string{"prepare", "credentials", "claim", "transport"} {
		t.Run(stage, func(t *testing.T) {
			lease := &modelLease{}
			var credentials, provider atomic.Int32
			opts := options(t, "https://models.example", nil)
			opts.HTTPClient = &http.Client{Transport: modelRoundTrip(func(*http.Request) (*http.Response, error) {
				provider.Add(1)
				return nil, errors.New("transport uncertain")
			})}
			opts.TokenSource = TokenSourceFunc(func(context.Context) (string, error) {
				credentials.Add(1)
				if stage == "credentials" {
					return "", errors.New("credential unavailable")
				}
				return "token", nil
			})
			opts.Execution = ExecutionPreparerFunc(func(context.Context, ExecutionRequest) (execution.Lease, error) {
				if stage == "prepare" {
					return nil, &execution.StatusError{Status: 429, Code: "QUOTA_DENIED", Message: "capacity unavailable", RetryAfter: time.Second + 1}
				}
				return lease, nil
			})
			if stage == "claim" {
				lease.beforeErr = &execution.StatusError{Status: 403, Code: "REVOKED", Message: "execution revoked"}
			}
			g, err := NewGateway(surface(t, "https://models.example", "models.generateContent"), opts)
			if err != nil {
				t.Fatal(err)
			}
			w := serve(t, g, "POST", controlledPath, controlledBody, "")
			switch stage {
			case "prepare":
				if w.Code != 429 || w.Header().Get("Retry-After") != "2" || credentials.Load() != 0 || provider.Load() != 0 || lease.aborts != 0 {
					t.Fatal(w.Code, w.Header(), credentials.Load(), provider.Load(), lease.aborts)
				}
			case "credentials":
				if w.Code != 503 || provider.Load() != 0 || lease.aborts != 1 || len(lease.results) != 0 {
					t.Fatal(w.Code, provider.Load(), lease.aborts, lease.results)
				}
			case "claim":
				if w.Code != 403 || provider.Load() != 0 || lease.aborts != 1 {
					t.Fatal(w.Code, provider.Load(), lease.aborts)
				}
			case "transport":
				if w.Code != 502 || provider.Load() != 1 || lease.aborts != 0 || len(lease.results) != 1 || lease.results[0].Terminal || lease.results[0].Err == nil {
					t.Fatal(w.Code, provider.Load(), lease.aborts, lease.results)
				}
			}
		})
	}
}

func TestControlledSSEForwardsEarlyFramesAndAcknowledgesFinalTail(t *testing.T) {
	lease := &modelLease{}
	first := ": heartbeat\r\n\r\ndata: {\"modelVersion\":\"model-v1\",\"candidates\":[{\"index\":0,\"content\":{\"parts\":[{\"text\":\"hello\"}]}}]}\n\n"
	final := "id: final\ndata: " + controlledResponse + "\n\n"
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, first)
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		_, _ = io.WriteString(w, final)
	}))
	defer upstream.Close()
	opts := options(t, upstream.URL, nil)
	opts.Execution = ExecutionPreparerFunc(func(context.Context, ExecutionRequest) (execution.Lease, error) { return lease, nil })
	g, err := NewGateway(surface(t, upstream.URL, "models.streamGenerateContent"), opts)
	if err != nil {
		t.Fatal(err)
	}
	early := make(chan struct{}, 1)
	w := modelAckWriter{ResponseRecorder: httptest.NewRecorder(), onWrite: func(data []byte) {
		if strings.Contains(string(data), "finishReason") {
			if !lease.isSettled() {
				t.Error("native final frame delivered before acknowledgement")
			}
		} else if strings.Contains(string(data), "hello") {
			select {
			case early <- struct{}{}:
			default:
			}
		}
	}}
	done := make(chan struct{})
	go func() {
		g.ServeHTTP(w, httptest.NewRequest("POST", controlledStreamPath, strings.NewReader(controlledBody)))
		close(done)
	}()
	select {
	case <-early:
	case <-time.After(2 * time.Second):
		close(release)
		t.Fatal("ordinary frame buffered behind complete stream")
	}
	close(release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("stream did not finish")
	}
	terminal := lease.terminalObservations()
	if w.Body.String() != first+final || !lease.isSettled() || len(terminal) != 1 || terminal[0].EventID != "final" {
		t.Fatal(w.Body.String(), lease.results, lease.observations)
	}
}

func TestControlledStreamUncertaintyNeverPublishesFinalTail(t *testing.T) {
	for _, kind := range []string{"truncated", "incomplete", "missing-marker", "missing-usage", "ack-failed", "inconsistent-thoughts", "null-counter"} {
		t.Run(kind, func(t *testing.T) {
			lease := &modelLease{}
			payload := controlledResponse
			if kind == "missing-marker" {
				payload = strings.ReplaceAll(payload, `"finishReason":"STOP",`, "")
			}
			if kind == "missing-usage" {
				payload = `{"candidates":[{"finishReason":"STOP"}]}`
				lease.errorFinish = errors.New("strict terminal usage is required")
			}
			if kind == "inconsistent-thoughts" {
				payload = strings.ReplaceAll(payload, `"thoughtsTokenCount":2`, `"thoughtsTokenCount":1`)
			}
			if kind == "null-counter" {
				payload = strings.ReplaceAll(payload, `"promptTokenCount":7`, `"promptTokenCount":null`)
			}
			frame := "data: " + payload + "\n\n"
			if kind == "incomplete" {
				frame = strings.TrimSuffix(frame, "\n")
			}
			if kind == "ack-failed" {
				lease.errorObserve = errors.New("durable usage unavailable")
			}
			var reader io.Reader = strings.NewReader(frame)
			if kind == "truncated" {
				reader = io.MultiReader(reader, failingModelReader{})
			}
			opts := options(t, "https://models.example", nil)
			opts.Execution = ExecutionPreparerFunc(func(context.Context, ExecutionRequest) (execution.Lease, error) { return lease, nil })
			opts.HTTPClient = &http.Client{Transport: modelRoundTrip(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(reader)}, nil
			})}
			g, err := NewGateway(surface(t, "https://models.example", "models.streamGenerateContent"), opts)
			if err != nil {
				t.Fatal(err)
			}
			w := serve(t, g, "POST", controlledStreamPath, controlledBody, "")
			if lease.isSettled() || lease.aborts != 0 || len(lease.results) != 1 || lease.results[0].Terminal || (kind != "missing-usage" && lease.results[0].Err == nil) {
				t.Fatal(kind, lease.results, lease.aborts)
			}
			if kind != "missing-marker" && w.Body.Len() != 0 {
				t.Fatal("uncertain terminal tail was delivered", w.Body.String())
			}
		})
	}
}

// A token-metered lease cannot govern a Live session: it is aborted after the
// bounded setup and before any credential or upstream dial. A request-count
// lease admits the session (see the usage-format conformance harness).
func TestControlledLiveRejectedBeforeCredentialsOrUpgrade(t *testing.T) {
	lease := &modelLease{}
	var credentials atomic.Int32
	opts := options(t, "https://models.example", newOwnership())
	opts.Execution = ExecutionPreparerFunc(func(context.Context, ExecutionRequest) (execution.Lease, error) { return lease, nil })
	opts.TokenSource = TokenSourceFunc(func(context.Context) (string, error) { credentials.Add(1); return "token", nil })
	g, err := NewGateway(surface(t, "https://models.example", "live.connect"), opts)
	if err != nil {
		t.Fatal(err)
	}
	gateway := httptest.NewServer(g)
	defer gateway.Close()
	ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(gateway.URL, "http")+"/ws/google.cloud.aiplatform.v1.LlmBidiService/BidiGenerateContent", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	_ = ws.WriteMessage(websocket.TextMessage, []byte(`{"setup":{"model":"model-a"}}`))
	_ = ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, err = ws.ReadMessage()
	var closeErr *websocket.CloseError
	if !errors.As(err, &closeErr) || closeErr.Code != websocket.ClosePolicyViolation || closeErr.Text != "UNSUPPORTED_EXECUTION_TRANSPORT" {
		t.Fatal("token-metered Live session not refused", err)
	}
	if credentials.Load() != 0 || lease.aborts != 1 || len(lease.results) != 0 {
		t.Fatal(credentials.Load(), lease.aborts, lease.results)
	}
}

func TestGeminiCanonicalUsageUsesExactNonoverlappingComposition(t *testing.T) {
	value := UsageObservation{Counters: map[string]int64{"input_tokens": 7, "output_tokens": 3, "total_tokens": 12}}
	if err := normalizeGeminiFinal(&value); err != nil {
		t.Fatal(err)
	}
	if value.Counters["thought_tokens"] != 2 || value.Counters["cached_tokens"] != 0 || value.Counters["uncached_input_tokens"] != 7 {
		t.Fatal("missingthoughtwasfabricatedzero", value.Counters)
	}
	for _, test := range []struct {
		name  string
		usage map[string]int64
	}{
		{"missinginput", map[string]int64{"output_tokens": 3, "total_tokens": 12}},
		{"missingoutput", map[string]int64{"input_tokens": 7, "total_tokens": 12}},
		{"cachedexceedsinput", map[string]int64{"input_tokens": 7, "cached_tokens": 8, "output_tokens": 3, "total_tokens": 12}},
		{"overlapoverflow", map[string]int64{"input_tokens": 1<<63 - 1, "output_tokens": 1<<63 - 1, "total_tokens": 1<<63 - 1}},
		{"undeclaredtoolwork", map[string]int64{"input_tokens": 7, "output_tokens": 3, "total_tokens": 12, "tool_use_tokens": 1}},
	} {
		t.Run(test.name, func(t *testing.T) {
			value := UsageObservation{Counters: test.usage}
			if err := normalizeGeminiFinal(&value); err == nil {
				t.Fatal("unsupportedbillingcompositionaccepted", value.Counters)
			}
		})
	}
}

func TestControlledSSEVersionOnlyMetadataPrecedesDriftRejection(t *testing.T) {
	lease := &modelLease{}
	first := "data: {\"modelVersion\":\"model-v1\"}\n\n"
	changed := "data: {\"modelVersion\":\"model-v2\"}\n\n"
	opts := options(t, "https://models.example", nil)
	opts.Execution = ExecutionPreparerFunc(func(context.Context, ExecutionRequest) (execution.Lease, error) { return lease, nil })
	opts.HTTPClient = &http.Client{Transport: modelRoundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(first + changed))}, nil
	})}
	g, err := NewGateway(surface(t, "https://models.example", "models.streamGenerateContent"), opts)
	if err != nil {
		t.Fatal(err)
	}
	w := serve(t, g, "POST", controlledStreamPath, controlledBody, "")
	if w.Body.String() != first || lease.aborts != 0 || len(lease.observations) != 2 || len(lease.results) != 1 || lease.results[0].Terminal || lease.results[0].Err == nil {
		t.Fatal(w.Body.String(), lease.observations, lease.results)
	}
	for i, version := range []string{"model-v1", "model-v2"} {
		value := lease.observations[i]
		if value.ModelVersion != version || value.Terminal || len(value.Counters) != 0 || !value.Cumulative {
			t.Fatal("version evidence lost or fabricated usage", value)
		}
	}
}

func TestCountOnlyLeaseCanAcknowledgeSSEWithoutTokenUsage(t *testing.T) {
	lease := &modelLease{}
	frames := "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"},\"finish_reason\":null}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" + "data: [DONE]\n\n"
	opts := options(t, "https://models.example", nil)
	opts.Execution = ExecutionPreparerFunc(func(context.Context, ExecutionRequest) (execution.Lease, error) { return lease, nil })
	opts.HTTPClient = &http.Client{Transport: modelRoundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(frames))}, nil
	})}
	g, err := NewGateway(surface(t, "https://models.example", "openai.chat-completions"), opts)
	if err != nil {
		t.Fatal(err)
	}
	w := serve(t, g, "POST", "/v1/projects/test-project/locations/global/endpoints/openapi/chat/completions", `{"model":"model-a","stream":true}`, "")
	if w.Code != 200 || w.Body.String() != frames || len(lease.observations) != 0 || len(lease.results) != 1 || !lease.results[0].ResponseComplete || lease.results[0].Terminal || lease.results[0].Err != nil {
		t.Fatal(w.Code, w.Body.String(), lease.observations, lease.results)
	}
}

func TestFinalOnlySSEVersionRejectionKeepsCompleteOrUnknownEvidence(t *testing.T) {
	for _, truncated := range []bool{false, true} {
		t.Run(map[bool]string{false: "clean", true: "truncated"}[truncated], func(t *testing.T) {
			lease := &modelLease{errorObserve: &execution.StatusError{Status: 502, Code: "WRONG_NATIVE_VERSION", Message: "native model version changed"}}
			payload := strings.ReplaceAll(controlledResponse, `"modelVersion":"model-v1"`, `"modelVersion":"unexpected-version"`)
			var reader io.Reader = strings.NewReader("data: " + payload + "\n\n")
			if truncated {
				reader = io.MultiReader(reader, failingModelReader{})
			}
			opts := options(t, "https://models.example", nil)
			opts.Execution = ExecutionPreparerFunc(func(context.Context, ExecutionRequest) (execution.Lease, error) { return lease, nil })
			opts.HTTPClient = &http.Client{Transport: modelRoundTrip(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(reader)}, nil
			})}
			g, err := NewGateway(surface(t, "https://models.example", "models.streamGenerateContent"), opts)
			if err != nil {
				t.Fatal(err)
			}
			w := serve(t, g, "POST", controlledStreamPath, controlledBody, "")
			if w.Body.Len() != 0 || lease.aborts != 0 || len(lease.observations) != 1 || len(lease.results) != 1 || lease.results[0].Terminal || lease.results[0].Err == nil {
				t.Fatal(w.Body.String(), lease.observations, lease.results)
			}
			value := lease.observations[0]
			if value.ModelVersion != "unexpected-version" || value.Terminal == truncated {
				t.Fatal("wrong completion evidence", value)
			}
			if truncated {
				if len(value.Counters) != 0 {
					t.Fatal("truncated stream fabricated terminal usage", value)
				}
			} else if value.Counters["input_tokens"] != 7 || value.Counters["uncached_input_tokens"] != 5 || value.Counters["output_tokens"] != 3 || value.Counters["thought_tokens"] != 2 {
				t.Fatal("complete final counters discarded before version rejection", value)
			}
		})
	}
}
