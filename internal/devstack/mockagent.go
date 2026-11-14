package devstack

import (
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
)

// Names of the seeded agent connection and what it points at.
const (
	SeedAgentConnection = "local-agent"
	// MockAgentResource is the Google Agent Runtime resource name the seeded
	// connection names. The mock speaks that provider's query wire
	// (POST /v1/<resource>:query) and nothing else.
	MockAgentResource = "projects/aiplex-local/locations/local/reasoningEngines/mock-agent"
	// AgentIdentityRef is the identity profile of the seeded connection: a
	// bearer token read from the API process's environment variable named
	// AgentTokenEnv. The connections file holds the variable's name, not a value.
	AgentIdentityRef = "dev-agent-identity"
	AgentTokenEnv    = "AIPLEX_DEV_AGENT_TOKEN"
)

// MockAgent is a tiny deterministic agent runtime on the Google Agent Runtime
// query wire. It answers POST /v1/<MockAgentResource>:query with a reply that
// is a pure function of the request, and refuses (401) a request without its
// bearer token. It holds no real credential and calls nothing.
type MockAgent struct {
	// Token is the bearer token it requires; empty disables the check.
	Token string

	mu     sync.Mutex
	called int
	bodies []string // the last maxMockAgentBodies, for tests
}

// maxMockAgentBodies bounds what a long-running aiplex dev keeps of requests.
const maxMockAgentBodies = 8

// RotateToken changes the bearer token the mock requires, so the token the API
// holds is refused (for trying an agent runtime that rejects the gateway).
func (m *MockAgent) RotateToken(token string) {
	m.mu.Lock()
	m.Token = token
	m.mu.Unlock()
}

func (m *MockAgent) token() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.Token
}

// Calls returns how many authorized query requests it has served.
func (m *MockAgent) Calls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.called
}

// Bodies returns the raw body of the last few authorized queries, in order.
func (m *MockAgent) Bodies() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.bodies...)
}

func (m *MockAgent) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if want := m.token(); want != "" {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if r.Header.Get("Authorization") == got || subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": map[string]any{"code": 401, "message": "missing or invalid bearer token"}})
			return
		}
	}
	if r.Method != http.MethodPost || r.URL.Path != "/v1/"+MockAgentResource+":query" {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": map[string]any{"code": 404, "message": "the mock agent serves POST /v1/" + MockAgentResource + ":query only"}})
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"code": 400, "message": "read error"}})
		return
	}
	var in struct {
		Input struct {
			Message string `json:"message"`
		} `json:"input"`
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		body = []byte("{}")
	}
	if err := json.Unmarshal(body, &in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"code": 400, "message": "body must be JSON like {\"input\":{\"message\":\"hi\"}}"}})
		return
	}
	m.mu.Lock()
	m.called++
	m.bodies = append(m.bodies, string(body))
	if len(m.bodies) > maxMockAgentBodies {
		m.bodies = m.bodies[len(m.bodies)-maxMockAgentBodies:]
	}
	m.mu.Unlock()
	reply := "mock-agent: hello"
	if in.Input.Message != "" {
		reply = "mock-agent: you said " + in.Input.Message
	}
	writeJSON(w, http.StatusOK, map[string]any{"output": map[string]any{"reply": reply, "agent": "mock-agent"}})
}
