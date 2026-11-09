// Package modelapi preserves provider-native model APIs behind explicit,
// capability-aware operation profiles. It does not normalize native protocols
// into chat completions or implement a quota allocator.
package modelapi

import (
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/vamsiramakrishnan/aiplex/pkg/toolcredential"
)

type Transport string

const (
	HTTP      Transport = "http"
	SSE       Transport = "sse"
	WebSocket Transport = "websocket"
)

type Action string

const (
	Create Action = "create"
	Read   Action = "read"
	List   Action = "list"
	Update Action = "update"
	Delete Action = "delete"
	Cancel Action = "cancel"
	Invoke Action = "invoke"
)

type Operation struct {
	ID           string    `json:"id"`
	Method       string    `json:"method"`
	Path         string    `json:"path"`
	Transport    Transport `json:"transport"`
	Action       Action    `json:"action"`
	ResourceKind string    `json:"resource_kind,omitempty"`
	Stateful     bool      `json:"stateful,omitempty"`
	SourceURL    string    `json:"source_url"`
}
type Surface struct {
	ID           string      `json:"id"`
	Protocol     string      `json:"protocol"`
	Origin       string      `json:"origin"`
	APIVersion   string      `json:"api_version"`
	Project      string      `json:"project"`
	Location     string      `json:"location"`
	Publisher    string      `json:"publisher,omitempty"`
	DataPrefixes []string    `json:"data_prefixes,omitempty"`
	Models       []string    `json:"models"`
	Agents       []string    `json:"agents,omitempty"`
	Endpoint     string      `json:"endpoint,omitempty"`
	Operations   []Operation `json:"operations"`
	// Provider selects a non-Google native provider. Nil keeps the Google
	// Cloud surface contract (project/location/version/publisher) unchanged.
	Provider *Provider `json:"provider,omitempty"`
}

var segmentPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._@-]*$`)

func validSegment(s string) bool { return segmentPattern.MatchString(s) && s != "." && s != ".." }
func (s Surface) Validate() error {
	if s.Provider != nil {
		return s.validateProvider()
	}
	if !validSegment(s.ID) || s.Protocol == "" || !validSegment(s.Project) || !validSegment(s.Location) {
		return fmt.Errorf("surface requires valid id, protocol, project and location")
	}
	if s.APIVersion != "v1" && s.APIVersion != "v1beta1" {
		return fmt.Errorf("unsupported model API version %q", s.APIVersion)
	}
	origin, e := url.Parse(s.Origin)
	if e != nil || origin.Host == "" || origin.User != nil || origin.RawQuery != "" || origin.Fragment != "" || (origin.Path != "" && origin.Path != "/") || (origin.Scheme != "https" && origin.Scheme != "http") {
		return fmt.Errorf("surface origin must be a configured HTTP(S) origin")
	}
	if s.Publisher != "" && !validSegment(s.Publisher) {
		return fmt.Errorf("invalid publisher")
	}
	if s.Endpoint != "" && !validSegment(s.Endpoint) {
		return fmt.Errorf("invalid endpoint")
	}
	for _, prefix := range s.DataPrefixes {
		if _, e := normalizeDataURI(prefix); e != nil {
			return fmt.Errorf("invalid Cloud Storage data prefix: %w", e)
		}
	}
	seen := map[string]bool{}
	for _, m := range append(append([]string{}, s.Models...), s.Agents...) {
		if m == "" || strings.ContainsAny(m, "?#%\\\r\n") || strings.Contains(m, "..") || strings.HasPrefix(m, "/") {
			return fmt.Errorf("invalid model/agent identifier %q", m)
		}
	}
	if len(s.Operations) == 0 {
		return fmt.Errorf("surface has no allowed operations")
	}
	for _, o := range s.Operations {
		if !validSegment(o.ID) || seen[o.ID] {
			return fmt.Errorf("invalid or duplicate operation %q", o.ID)
		}
		seen[o.ID] = true
		if o.Method != "GET" && o.Method != "POST" && o.Method != "PATCH" && o.Method != "DELETE" {
			return fmt.Errorf("invalid operation method")
		}
		if o.Transport != HTTP && o.Transport != SSE && o.Transport != WebSocket {
			return fmt.Errorf("unsupported transport")
		}
		if o.Stateful && o.ResourceKind == "" {
			return fmt.Errorf("stateful operation requires resource kind")
		}
		if !strings.HasPrefix(o.Path, "/") || strings.ContainsAny(o.Path, "?#%\\\r\n ") || strings.Contains(o.Path, "..") || strings.Contains(o.Path, "//") {
			return fmt.Errorf("invalid operation path")
		}
		for _, part := range placeholderPattern.FindAllString(o.Path, -1) {
			switch part {
			case "{version}", "{project}", "{location}", "{publisher}", "{model}", "{endpoint}", "{id}":
			default:
				return fmt.Errorf("unknown operation placeholder %s", part)
			}
		}
		if strings.Contains(o.Path, "{publisher}") && s.Publisher == "" {
			return fmt.Errorf("operation requires publisher")
		}
		if strings.Contains(o.Path, "{endpoint}") && s.Endpoint == "" {
			return fmt.Errorf("operation requires endpoint")
		}
		if strings.Contains(o.Path, "{model}") && len(s.Models) == 0 {
			return fmt.Errorf("operation requires allowed models")
		}
		if o.ResourceKind == "interaction" {
			if s.Location != "global" && s.Location != "us" && s.Location != "eu" {
				return fmt.Errorf("interactions requires global/us/eu location")
			}
			expected := "aiplatform.googleapis.com"
			if s.Location == "us" {
				expected = "aiplatform.us.rep.googleapis.com"
			}
			if s.Location == "eu" {
				expected = "aiplatform.eu.rep.googleapis.com"
			}
			if origin.Scheme == "https" && origin.Hostname() != expected {
				return fmt.Errorf("interactions origin does not match location")
			}
		}
	}
	return nil
}

var placeholderPattern = regexp.MustCompile(`\{[^{}]+\}`)

// GoogleOperations returns native operation contracts; a Surface selects an
// explicit subset. Availability remains model/region/account-specific.
func GoogleOperations() []Operation {
	ref := "https://docs.cloud.google.com/gemini-enterprise-agent-platform/reference/rest"
	base := "/{version}/projects/{project}/locations/{location}"
	model := base + "/publishers/{publisher}/models/{model}"
	ops := []Operation{}
	add := func(id, method, path string, transport Transport, action Action, kind string, stateful bool) {
		ops = append(ops, Operation{id, method, path, transport, action, kind, stateful, ref})
	}
	for _, method := range []string{"generateContent", "streamGenerateContent", "embedContent", "countTokens", "computeTokens", "predict", "rawPredict", "streamRawPredict", "predictLongRunning", "fetchPredictOperation"} {
		t := HTTP
		if method == "streamGenerateContent" || method == "streamRawPredict" {
			t = SSE
		}
		add("models."+method, "POST", model+":"+method, t, Invoke, "", false)
	}
	for i := range ops {
		if ops[i].ID == "models.predictLongRunning" {
			ops[i].Stateful = true
			ops[i].ResourceKind = "prediction-operation"
			ops[i].Action = Create
		}
		if ops[i].ID == "models.fetchPredictOperation" {
			ops[i].Stateful = true
			ops[i].ResourceKind = "prediction-operation"
			ops[i].Action = Read
		}
	}
	add("openai.chat-completions", "POST", base+"/endpoints/{endpoint}/chat/completions", SSE, Invoke, "", false)
	for _, method := range []string{"generateContent", "streamGenerateContent", "countTokens", "computeTokens", "predict", "rawPredict", "streamRawPredict"} {
		transport := HTTP
		if strings.HasPrefix(method, "stream") {
			transport = SSE
		}
		add("endpoints."+method, "POST", base+"/endpoints/{endpoint}:"+method, transport, Invoke, "", false)
	}
	for _, response := range []struct {
		id, method, path string
		action           Action
	}{{"create", "POST", "", Create}, {"get", "GET", "/{id}", Read}, {"delete", "DELETE", "/{id}", Delete}} {
		add("response."+response.id, response.method, base+"/endpoints/{endpoint}/responses"+response.path, HTTP, response.action, "response", true)
		ops[len(ops)-1].SourceURL = "https://docs.cloud.google.com/gemini-enterprise-agent-platform/models/partner-models/grok/responses"
	}

	for _, resource := range []struct {
		kind, path            string
		patch, delete, cancel bool
	}{{"interaction", "interactions", false, true, true}, {"cache", "cachedContents", true, true, false}, {"batch", "batchPredictionJobs", false, true, true}, {"tuning", "tuningJobs", false, false, true}, {"operation", "operations", false, true, true}} {
		prefix := base + "/" + resource.path
		if resource.kind != "operation" {
			add(resource.kind+".create", "POST", prefix, HTTP, Create, resource.kind, true)
			add(resource.kind+".list", "GET", prefix, HTTP, List, resource.kind, true)
		}
		add(resource.kind+".get", "GET", prefix+"/{id}", HTTP, Read, resource.kind, true)
		if resource.patch {
			add(resource.kind+".update", "PATCH", prefix+"/{id}", HTTP, Update, resource.kind, true)
		}
		if resource.delete {
			add(resource.kind+".delete", "DELETE", prefix+"/{id}", HTTP, Delete, resource.kind, true)
		}
		if resource.cancel {
			suffix := ":cancel"
			if resource.kind == "interaction" {
				suffix = "/cancel"
			}
			add(resource.kind+".cancel", "POST", prefix+"/{id}"+suffix, HTTP, Cancel, resource.kind, true)
		}
	}
	add("live.connect", "GET", "/ws/google.cloud.aiplatform.v1.LlmBidiService/BidiGenerateContent", WebSocket, Invoke, "live-session", true)
	return ops
}

func SelectOperations(ids ...string) ([]Operation, error) {
	all := map[string]Operation{}
	for _, o := range Operations() {
		all[o.ID] = o
	}
	out := make([]Operation, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		o, ok := all[id]
		if !ok || seen[id] {
			return nil, fmt.Errorf("unknown or duplicate operation %q", id)
		}
		seen[id] = true
		out = append(out, o)
	}
	return out, nil
}

// Registry snapshots surface configuration so callers cannot change an active
// allowlist by mutating slices after registration.
type Registry struct{ surfaces map[string]Surface }

func NewRegistry(surfaces ...Surface) (*Registry, error) {
	r := &Registry{surfaces: map[string]Surface{}}
	for _, s := range surfaces {
		if e := s.Validate(); e != nil {
			return nil, e
		}
		if _, ok := r.surfaces[s.ID]; ok {
			return nil, fmt.Errorf("duplicate surface")
		}
		r.surfaces[s.ID] = cloneSurface(s)
	}
	return r, nil
}
func (r *Registry) Get(id string) (Surface, bool) {
	s, ok := r.surfaces[id]
	return cloneSurface(s), ok
}
func (r *Registry) List() []Surface {
	out := []Surface{}
	for _, s := range r.surfaces {
		out = append(out, cloneSurface(s))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
func cloneSurface(s Surface) Surface {
	s.DataPrefixes = append([]string(nil), s.DataPrefixes...)
	s.Models = append([]string(nil), s.Models...)
	s.Agents = append([]string(nil), s.Agents...)
	s.Operations = append([]Operation(nil), s.Operations...)
	s.Provider = cloneProvider(s.Provider)
	return s
}

// ModelID gives policy engines a profile-normalized model ID while retaining
// the exact native spelling in request metadata and the unchanged payload.
func (s Surface) ModelID(native string) (string, error) {
	g := Gateway{surface: s}
	if !g.allowedModel(native) {
		return "", fmt.Errorf("model is not allowed by surface")
	}
	return g.normalizeModel(native), nil
}

// Connection binds a provider-native surface to host-owned credentials and
// optional provisioning intent. Secrets are always referenced, never inline.
type Connection struct {
	Surface           Surface `json:"surface"`
	IdentityRef       string  `json:"identity_ref"`
	NetworkProfileRef string  `json:"network_profile_ref,omitempty"`
	BillingProject    string  `json:"billing_project,omitempty"`
	// Auth selects a tenant-scoped secret-reference credential for provider
	// surfaces, using the same reference scheme as tool connections.
	Auth *toolcredential.Auth `json:"auth,omitempty"`
}
