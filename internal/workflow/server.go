package workflow

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/vamsiramakrishnan/aiplex/internal/capability"
)

// Server is the HTTP handler that exposes workflow capabilities. The gateway
// resolver maps `/cap/workflow/<name>@<v>/_invoke` to (uri, action="run") and
// forwards here after ext_authz approves; the server then asks the executor
// to run the spec and returns the Run record.
type Server struct {
	Executor *Executor
}

// NewServer wires the executor.
func NewServer(e *Executor) *Server { return &Server{Executor: e} }

// ServeHTTP handles three routes under /cap/workflow/:
//
//	POST /cap/workflow/<name>@<ver>/_invoke   → run (body: {action, input})
//	GET  /cap/workflow/<name>@<ver>/_describe → return registered spec
//	GET  /cap/workflow/runs/<run-id>          → fetch a recorded Run
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/cap/workflow/runs/") {
		s.handleRun(w, r)
		return
	}

	uri, action, err := parseWorkflowPath(r.URL.Path)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_PATH", err.Error())
		return
	}

	switch action {
	case "run":
		s.handleInvoke(w, r, uri)
	case "describe":
		s.handleDescribe(w, uri)
	default:
		writeErr(w, http.StatusBadRequest, "BAD_ACTION", action)
	}
}

func (s *Server) handleInvoke(w http.ResponseWriter, r *http.Request, uri capability.URI) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", r.Method)
		return
	}

	var body struct {
		Input  map[string]any `json:"input"`
		Action string         `json:"action"` // ignored for now; implied by path
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_BODY", err.Error())
		return
	}
	if body.Input == nil {
		body.Input = map[string]any{}
	}

	token := bearerToken(r)
	caller := r.Header.Get("X-AIPlex-Caller") // set by middleware in main; empty in tests is fine.

	run, err := s.Executor.Run(r.Context(), token, uri.String(), caller, body.Input)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "RUN_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (s *Server) handleDescribe(w http.ResponseWriter, uri capability.URI) {
	s.Executor.mu.RLock()
	spec, ok := s.Executor.specs[uri.String()]
	s.Executor.mu.RUnlock()
	if !ok {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "workflow not registered")
		return
	}
	writeJSON(w, http.StatusOK, spec)
}

func (s *Server) handleRun(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/cap/workflow/runs/")
	run := s.Executor.GetRun(id)
	if run == nil {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "run not found")
		return
	}
	writeJSON(w, http.StatusOK, run)
}

// parseWorkflowPath splits "/cap/workflow/<name>@<ver>/_<action>" into
// (URI, action). Returns an error for malformed paths.
func parseWorkflowPath(path string) (capability.URI, string, error) {
	const prefix = "/cap/workflow/"
	if !strings.HasPrefix(path, prefix) {
		return capability.URI{}, "", fmt.Errorf("path must start with %q", prefix)
	}
	rest := strings.TrimPrefix(path, prefix)
	at := strings.Index(rest, "@")
	if at < 0 {
		return capability.URI{}, "", fmt.Errorf("missing @version: %s", path)
	}
	endVer := strings.Index(rest[at:], "/")
	verEnd := len(rest)
	if endVer >= 0 {
		verEnd = at + endVer
	}
	uri, err := capability.ParseURI("cap://workflow/" + rest[:verEnd])
	if err != nil {
		return capability.URI{}, "", err
	}

	action := "run"
	if verEnd < len(rest) {
		tail := rest[verEnd+1:]
		switch tail {
		case "_invoke":
			action = "run"
		case "_describe":
			action = "describe"
		default:
			return capability.URI{}, "", fmt.Errorf("unknown action %q", tail)
		}
	}
	return uri, action, nil
}

func bearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	return ""
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"code": code, "message": message})
}
