package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/time/rate"

	"github.com/vamsiramakrishnan/aiplex/internal/models"
)

// Policy denials are recorded from two places: the API itself (skill and tool
// admission) and the authz sidecar at the gateway, which posts to
// POST /internal/policy-denials. That route carries the bearer secret in
// AIPLEX_DENIAL_TOKEN (BearerToken answers 503 while it is unset) and a
// per-source rate limit; this handler adds a global one, a body limit and a
// closed schema. The sidecar posts only denials of tokens it verified.
//
// Denials are deployment-wide operator data. The payload cannot carry a
// tenant: the shared secret authenticates the sidecar as a component, not as a
// tenant, and the sidecar's tokens carry no tenant claim, so the record is
// stored without one and read through the admin-only dashboard route.
const (
	// DenialTokenEnv names the environment variable holding the shared secret.
	DenialTokenEnv = "AIPLEX_DENIAL_TOKEN"

	maxDenialBody    = 4 << 10
	maxDenialID      = 256
	maxDenialAction  = 512
	maxDenialScope   = 512
	maxDenialRequest = 128
	// DenialRetention is how long a denial is kept: stores expire it (the
	// in-memory store drops the oldest beyond its cap; Firestore uses a TTL on
	// the expires_at field).
	DenialRetention = 30 * 24 * time.Hour

	denialsPerSecond = 50
	denialBurst      = 200
)

var (
	denialPlanes  = map[string]bool{"": true, "mcplex": true, "a2aplex": true, "llmplex": true, "skillsplex": true}
	denialReasons = map[string]bool{"scope_missing": true, "budget_exceeded": true, "rate_limited": true}
)

// denialInput is the closed wire schema. id and timestamp are accepted for
// callers of the earlier admin route and ignored: the server assigns both.
type denialInput struct {
	ID        json.RawMessage `json:"id"`
	Timestamp json.RawMessage `json:"timestamp"`
	Plane     string          `json:"plane"`
	AgentID   string          `json:"agent_id"`
	UserID    string          `json:"user_id"`
	Action    string          `json:"action"`
	Scope     string          `json:"scope"`
	Reason    string          `json:"reason"`
	RequestID string          `json:"request_id"`
	TraceID   string          `json:"trace_id"`
}

func plainText(s string, max int) bool {
	if utf8.RuneCountInString(s) > max || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func validTraceID(s string) bool {
	if s == "" {
		return true
	}
	if len(s) != 32 {
		return false
	}
	zero := true
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
		if c != '0' {
			zero = false
		}
	}
	return !zero
}

// validate returns the name of the first invalid field, or "".
func (d *denialInput) validate() string {
	switch {
	case !denialPlanes[d.Plane]:
		return "plane"
	case !denialReasons[d.Reason]:
		return "reason"
	case !plainText(d.AgentID, maxDenialID):
		return "agent_id"
	case !plainText(d.UserID, maxDenialID):
		return "user_id"
	case d.AgentID == "" && d.UserID == "":
		return "agent_id"
	case d.Action == "" || !plainText(d.Action, maxDenialAction):
		return "action"
	case !plainText(d.Scope, maxDenialScope):
		return "scope"
	case !plainText(d.RequestID, maxDenialRequest):
		return "request_id"
	case !validTraceID(d.TraceID):
		return "trace_id"
	}
	return ""
}

func newDenialID() (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "den_" + hex.EncodeToString(b[:]), nil
}

// allowDenial applies the handler-wide limit on recorded denials.
func (h *DashboardHandler) allowDenial() bool {
	h.denialMu.Lock()
	if h.denialLimit == nil {
		h.denialLimit = rate.NewLimiter(denialsPerSecond, denialBurst)
	}
	l := h.denialLimit
	h.denialMu.Unlock()
	return l.Allow()
}

// RecordPolicyDenial records a policy denial.
// POST /internal/policy-denials (authz sidecar, shared bearer secret)
// POST /api/v1/dashboard/denials (admin)
func (h *DashboardHandler) RecordPolicyDenial(w http.ResponseWriter, r *http.Request) {
	if !h.allowDenial() {
		w.Header().Set("Retry-After", strconv.Itoa(1))
		Error(w, r, http.StatusTooManyRequests, "RATE_LIMITED", "too many denial reports")
		return
	}
	body := http.MaxBytesReader(w, r.Body, maxDenialBody)
	dec := json.NewDecoder(body)
	dec.DisallowUnknownFields()
	var in denialInput
	if err := dec.Decode(&in); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			Error(w, r, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "denial report exceeds 4096 bytes")
			return
		}
		Error(w, r, http.StatusBadRequest, "BAD_REQUEST", "invalid request body")
		return
	}
	if _, err := dec.Token(); err != io.EOF {
		Error(w, r, http.StatusBadRequest, "BAD_REQUEST", "invalid request body")
		return
	}
	if field := in.validate(); field != "" {
		Error(w, r, http.StatusBadRequest, "INVALID_FIELD", "invalid "+field)
		return
	}
	id, err := newDenialID()
	if err != nil {
		Error(w, r, http.StatusInternalServerError, "INTERNAL", "could not allocate an identifier")
		return
	}
	now := time.Now()
	if h.Now != nil {
		now = h.Now()
	}
	d := models.PolicyDenial{
		ID:        id,
		Timestamp: now.UTC(),
		Plane:     in.Plane,
		AgentID:   in.AgentID,
		UserID:    in.UserID,
		Action:    in.Action,
		Scope:     in.Scope,
		Reason:    in.Reason,
		RequestID: in.RequestID,
		TraceID:   in.TraceID,
		ExpiresAt: now.UTC().Add(DenialRetention),
	}
	if err := h.store.AppendPolicyDenial(r.Context(), &d); err != nil {
		Error(w, r, http.StatusInternalServerError, "STORE_ERROR", "denial store unavailable")
		return
	}
	JSON(w, http.StatusCreated, d)
}
