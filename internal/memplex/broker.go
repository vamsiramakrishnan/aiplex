package memplex

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/capability"
)

// Broker serves cap://memory/* invocations over HTTP. The Envoy capability
// resolver matches a request path of the form
//
//	/cap/memory/<name>@<version>/<key>
//
// to a (uri, action) pair and forwards the call to this broker. Tenant
// scoping (e.g. cap://memory/{tenant}/{user}/profile@v1) is achieved by
// templating sub-paths in the URI; the constraint filter validates that
// JWT claims match the substituted variables.
type Broker struct {
	mu       sync.RWMutex
	defaults MemoryBackend
	routes   map[string]nsRoute // exact URI → namespace + backend
}

type nsRoute struct {
	ns      Namespace
	backend MemoryBackend
}

// NewBroker creates a broker with a default backend used by namespaces that
// don't pin a specific one.
func NewBroker(defaults MemoryBackend) *Broker {
	return &Broker{
		defaults: defaults,
		routes:   make(map[string]nsRoute),
	}
}

// Register adds (or replaces) a namespace's backend mapping. Called by the
// deploy engine when a kind=memory capability is provisioned.
func (b *Broker) Register(ns Namespace, backend MemoryBackend) {
	if backend == nil {
		backend = b.defaults
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.routes[ns.URI.String()] = nsRoute{ns: ns, backend: backend}
}

// Unregister removes a namespace.
func (b *Broker) Unregister(uri string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.routes, uri)
}

// resolve maps a parsed URI to its namespace + backend. Falls back to a
// default-backed namespace for ad-hoc lookups (handy in tests).
func (b *Broker) resolve(u capability.URI) (Namespace, MemoryBackend) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if r, ok := b.routes[u.String()]; ok {
		return r.ns, r.backend
	}
	return Namespace{URI: u}, b.defaults
}

// ServeHTTP implements http.Handler. Routes:
//
//	GET    /cap/memory/<name>@<ver>/<key>            → Read
//	PUT    /cap/memory/<name>@<ver>/<key>            → Write (body: Value)
//	DELETE /cap/memory/<name>@<ver>/<key>            → Delete
//	GET    /cap/memory/<name>@<ver>/?prefix=...      → List
//	POST   /cap/memory/<name>@<ver>/_search          → Search (body: Query)
//	GET    /cap/memory/<name>@<ver>/_subscribe?prefix=... (SSE)
//
// The `<key>` segment may contain slashes and is taken verbatim as the
// remainder of the path after the capability segment.
func (b *Broker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	u, action, key, err := parsePath(r.URL.Path)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_PATH", err.Error())
		return
	}
	if u.Kind != capability.KindMemory {
		writeErr(w, http.StatusBadRequest, "WRONG_KIND",
			fmt.Sprintf("broker only serves kind=memory; got %s", u.Kind))
		return
	}

	ns, backend := b.resolve(u)
	if backend == nil {
		writeErr(w, http.StatusServiceUnavailable, "NO_BACKEND",
			"no backend registered for this namespace and no default configured")
		return
	}

	switch {
	case action == "invoke":
		b.handleInvoke(w, r, ns, backend)
	case action == "search":
		b.handleSearch(w, r, ns, backend)
	case action == "subscribe":
		b.handleSubscribe(w, r, ns, backend)
	case action == "list" && key == "":
		b.handleList(w, r, ns, backend)
	case key != "":
		switch r.Method {
		case http.MethodGet:
			b.handleRead(w, r, ns, backend, key)
		case http.MethodPut:
			b.handleWrite(w, r, ns, backend, key)
		case http.MethodDelete:
			b.handleDelete(w, r, ns, backend, key)
		default:
			writeErr(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", r.Method)
		}
	default:
		writeErr(w, http.StatusBadRequest, "BAD_ACTION", "missing key or special action")
	}
}

// handleInvoke is the universal cap-call endpoint. Every kind exposes
// POST /cap/<kind>/<name>@<v>/_invoke with body `{"action": "...", "input": {...}}`.
// For memory, the action field selects between read/write/delete/search/list
// and the input carries the operation-specific arguments. This is what the
// workflow executor (and any uniform CapInvoker) calls — kind-specific REST
// verbs are still available for direct human/SDK use.
func (b *Broker) handleInvoke(w http.ResponseWriter, r *http.Request, ns Namespace, backend MemoryBackend) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", r.Method)
		return
	}
	var body struct {
		Action string         `json:"action"`
		Input  map[string]any `json:"input"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_BODY", err.Error())
		return
	}
	if body.Input == nil {
		body.Input = map[string]any{}
	}

	switch body.Action {
	case "read":
		key, _ := body.Input["key"].(string)
		if key == "" {
			writeErr(w, http.StatusBadRequest, "BAD_INPUT", "input.key required for read")
			return
		}
		v, err := backend.Read(r.Context(), ns, key)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				writeErr(w, http.StatusNotFound, "NOT_FOUND", "key not found")
				return
			}
			writeErr(w, http.StatusInternalServerError, "BACKEND_ERROR", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, v)

	case "write", "":
		key, _ := body.Input["key"].(string)
		if key == "" {
			writeErr(w, http.StatusBadRequest, "BAD_INPUT", "input.key required for write")
			return
		}
		data, _ := body.Input["data"].(map[string]any)
		if data == nil {
			data = map[string]any{}
		}
		cleaned, err := applyPII(data, ns.PII)
		if errors.Is(err, ErrPIIRejected) {
			writeErr(w, http.StatusForbidden, "PII_REJECTED", err.Error())
			return
		}
		val := Value{Data: cleaned}
		if err := backend.Write(r.Context(), ns, key, val, WriteOpts{}); err != nil {
			writeErr(w, http.StatusInternalServerError, "BACKEND_ERROR", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"key": key})

	case "delete":
		key, _ := body.Input["key"].(string)
		if err := backend.Delete(r.Context(), ns, key); err != nil {
			if errors.Is(err, ErrNotFound) {
				writeErr(w, http.StatusNotFound, "NOT_FOUND", "key not found")
				return
			}
			writeErr(w, http.StatusInternalServerError, "BACKEND_ERROR", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"deleted": key})

	case "search":
		q := Query{}
		if emb, ok := body.Input["embedding"].([]any); ok {
			vec := make([]float32, len(emb))
			for i, e := range emb {
				if f, ok := e.(float64); ok {
					vec[i] = float32(f)
				}
			}
			q.Embedding = vec
		}
		if t, ok := body.Input["text"].(string); ok {
			q.Text = t
		}
		if n, ok := body.Input["top_k"].(float64); ok {
			q.TopK = int(n)
		}
		hits, err := backend.Search(r.Context(), ns, q)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "BACKEND_ERROR", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"hits": hits})

	case "list":
		prefix, _ := body.Input["prefix"].(string)
		out, err := backend.List(r.Context(), ns, prefix, Page{})
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "BACKEND_ERROR", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, out)

	default:
		writeErr(w, http.StatusBadRequest, "BAD_ACTION",
			fmt.Sprintf("unknown action %q for kind=memory", body.Action))
	}
}

func (b *Broker) handleRead(w http.ResponseWriter, r *http.Request, ns Namespace, backend MemoryBackend, key string) {
	v, err := backend.Read(r.Context(), ns, key)
	if errors.Is(err, ErrNotFound) {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "key not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "BACKEND_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (b *Broker) handleWrite(w http.ResponseWriter, r *http.Request, ns Namespace, backend MemoryBackend, key string) {
	var val Value
	if err := json.NewDecoder(r.Body).Decode(&val); err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_BODY", err.Error())
		return
	}
	if val.Data == nil {
		val.Data = map[string]any{}
	}

	// PII redaction — applied by the broker, not the backend, so every
	// backend gets the same guarantee.
	cleaned, err := applyPII(val.Data, ns.PII)
	if errors.Is(err, ErrPIIRejected) {
		writeErr(w, http.StatusForbidden, "PII_REJECTED", "value rejected by namespace PII policy")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "PII_ERROR", err.Error())
		return
	}
	val.Data = cleaned

	opts := WriteOpts{}
	if r.Header.Get("If-None-Match") == "*" {
		opts.IfNoneMatch = true
	}
	if ttlStr := r.Header.Get("X-AIPlex-TTL-Seconds"); ttlStr != "" {
		if secs, err := strconv.Atoi(ttlStr); err == nil && secs > 0 {
			opts.TTL = time.Duration(secs) * time.Second
		}
	}

	if err := backend.Write(r.Context(), ns, key, val, opts); err != nil {
		if errors.Is(err, ErrConflict) {
			writeErr(w, http.StatusConflict, "CONFLICT", "key already exists")
			return
		}
		writeErr(w, http.StatusInternalServerError, "BACKEND_ERROR", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (b *Broker) handleDelete(w http.ResponseWriter, r *http.Request, ns Namespace, backend MemoryBackend, key string) {
	if err := backend.Delete(r.Context(), ns, key); err != nil {
		if errors.Is(err, ErrNotFound) {
			writeErr(w, http.StatusNotFound, "NOT_FOUND", "key not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, "BACKEND_ERROR", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (b *Broker) handleSearch(w http.ResponseWriter, r *http.Request, ns Namespace, backend MemoryBackend) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", r.Method)
		return
	}
	var q Query
	if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_BODY", err.Error())
		return
	}
	hits, err := backend.Search(r.Context(), ns, q)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "BACKEND_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"hits": hits})
}

func (b *Broker) handleList(w http.ResponseWriter, r *http.Request, ns Namespace, backend MemoryBackend) {
	prefix := r.URL.Query().Get("prefix")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	cursor := r.URL.Query().Get("cursor")
	out, err := backend.List(r.Context(), ns, prefix, Page{Cursor: cursor, Limit: limit})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "BACKEND_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (b *Broker) handleSubscribe(w http.ResponseWriter, r *http.Request, ns Namespace, backend MemoryBackend) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "SSE_UNSUPPORTED", "streaming not supported")
		return
	}
	prefix := r.URL.Query().Get("prefix")
	ch, err := backend.Subscribe(r.Context(), ns, prefix)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "BACKEND_ERROR", err.Error())
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	for {
		select {
		case <-r.Context().Done():
			return
		case change, ok := <-ch:
			if !ok {
				return
			}
			data, _ := json.Marshal(change)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", change.Op, data)
			flusher.Flush()
		}
	}
}

// parsePath splits "/cap/memory/<name>@<ver>/<rest...>" into the URI, the
// implied action, and the trailing key (or "" for collection-level operations).
//
// Special suffixes:
//
//	/_search    → action="search"
//	/_subscribe → action="subscribe"
//	(trailing /) → action="list", key=""
//
// Otherwise action is determined by HTTP method and key is the everything
// after the capability segment.
func parsePath(path string) (capability.URI, string, string, error) {
	const prefix = "/cap/memory/"
	if !strings.HasPrefix(path, prefix) {
		return capability.URI{}, "", "", fmt.Errorf("path must start with %q", prefix)
	}
	rest := strings.TrimPrefix(path, prefix)
	at := strings.Index(rest, "@")
	if at < 0 {
		return capability.URI{}, "", "", fmt.Errorf("missing @version in path: %s", path)
	}
	// Find end of version segment (next '/' after '@').
	endVer := strings.Index(rest[at:], "/")
	verEnd := len(rest)
	if endVer >= 0 {
		verEnd = at + endVer
	}
	uriStr := "cap://memory/" + rest[:verEnd]
	u, err := capability.ParseURI(uriStr)
	if err != nil {
		return capability.URI{}, "", "", err
	}

	tail := ""
	if verEnd < len(rest) {
		tail = rest[verEnd+1:] // skip the '/'
	}

	switch {
	case tail == "_invoke":
		return u, "invoke", "", nil
	case tail == "_search":
		return u, "search", "", nil
	case tail == "_subscribe":
		return u, "subscribe", "", nil
	case tail == "":
		return u, "list", "", nil
	default:
		return u, "", tail, nil
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"code": code, "message": message})
}
