package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
	"github.com/vamsiramakrishnan/aiplex/internal/auth"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/pkg/clientplex"
	"github.com/vamsiramakrishnan/aiplex/pkg/modelapi"
	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
	"github.com/vamsiramakrishnan/aiplex/pkg/toolexec"
)

// upstreamCall is one request observed by the fake provider/tool server.
type upstreamCall struct {
	Path, Query, Body, Authorization string
	ContentType                      string
	ContentLength                    int64 // -1 when the request was chunked
}

type apFixture struct {
	t        *testing.T
	h        *ConnectedHandler
	store    resources.Store
	router   http.Handler
	upstream *httptest.Server

	mu       sync.Mutex
	calls    []upstreamCall
	tenant   string
	identity *models.WIFIdentity
	access   *models.ResolvedAccess
}

const apIssuer = "https://idp.example"

func apUser(subject string, groups ...string) *models.WIFIdentity {
	return &models.WIFIdentity{PrincipalID: auth.CanonicalPrincipalID(apIssuer, "people", subject), Subject: subject, Provider: apIssuer, PoolID: "people", IsWorkforce: true, Groups: groups}
}

func (f *apFixture) as(identity *models.WIFIdentity, scopes ...string) {
	f.identity = identity
	if identity == nil {
		f.access = nil
		return
	}
	f.access = &models.ResolvedAccess{Identity: *identity, Scopes: scopes, Roles: []models.IAMRole{models.RoleAdmin}}
}

func (f *apFixture) upstreamCalls() []upstreamCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]upstreamCall(nil), f.calls...)
}

func (f *apFixture) put(kind, id string, spec any) {
	f.t.Helper()
	raw, err := json.Marshal(spec)
	if err != nil {
		f.t.Fatal(err)
	}
	if _, err = f.store.Put(context.Background(), resources.Record{TenantID: "tenant", Kind: kind, ID: id, Spec: raw}, 0); err != nil {
		f.t.Fatal(err)
	}
}

func (f *apFixture) do(method, path, body string, headers ...string) *httptest.ResponseRecorder {
	f.t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	for i := 0; i+1 < len(headers); i += 2 {
		r.Header.Set(headers[i], headers[i+1])
	}
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, r)
	return w
}

const apToolsList = `{"jsonrpc":"2.0","id":%s,"result":{"tools":[%s]}}`

func toolEntries(names ...string) string {
	parts := make([]string, len(names))
	for i, n := range names {
		parts[i] = `{"name":"` + n + `","description":"upstream description","inputSchema":{"type":"object"}}`
	}
	return strings.Join(parts, ",")
}

func newAPFixture(t *testing.T) *apFixture {
	t.Helper()
	f := &apFixture{t: t, tenant: "tenant"}
	f.upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.calls = append(f.calls, upstreamCall{Path: r.URL.Path, Query: r.URL.RawQuery, Body: string(body), Authorization: r.Header.Get("Authorization"), ContentType: r.Header.Get("Content-Type"), ContentLength: r.ContentLength})
		f.mu.Unlock()
		switch {
		case r.URL.Path == "/v1/realtime":
			conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close()
			for _, event := range []string{`{"type":"response.created","response":{"id":"resp_ap1"}}`, `{"type":"response.done","response":{"id":"resp_ap1","status":"completed","usage":{"input_tokens":9,"input_token_details":{"cached_tokens":4},"output_tokens":3,"total_tokens":12}}}`} {
				_ = conn.WriteMessage(websocket.TextMessage, []byte(event))
			}
			_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
			_ = conn.SetReadDeadline(time.Now().Add(time.Second))
			for {
				if _, _, err := conn.ReadMessage(); err != nil {
					return
				}
			}
		case strings.HasPrefix(r.URL.Path, "/mcp/"):
			connection := strings.TrimPrefix(r.URL.Path, "/mcp/")
			if connection == "broken" {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":"x","error":{"code":-32000,"message":"upstream secret stack trace"}}`)
				return
			}
			var message struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
				Params struct {
					Name string `json:"name"`
				} `json:"params"`
			}
			_ = json.Unmarshal(body, &message)
			w.Header().Set("Content-Type", "application/json")
			if message.Method == "tools/list" {
				names := map[string][]string{"github": {"search", "delete"}, "search": {"search", "searchx", "other"}}[connection]
				_, _ = io.WriteString(w, strings.Replace(strings.Replace(apToolsList, "%s", string(message.ID), 1), "%s", toolEntries(names...), 1))
				return
			}
			_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":`+string(message.ID)+`,"result":{"content":[{"type":"text","text":"`+connection+`:`+message.Params.Name+`"}]}}`)
		case strings.HasSuffix(r.URL.Path, ":streamGenerateContent"):
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"a\"}]}}]}\r\n\r\n")
			w.(http.Flusher).Flush()
			_, _ = io.WriteString(w, "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"b\"}]}}]}\r\n\r\n")
		case strings.Contains(r.URL.Path, "provider-error"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(400)
			_, _ = io.WriteString(w, `{"error":{"code":400,"message":"provider says no","status":"INVALID_ARGUMENT"}}`)
		default:
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"native":"response","path":"`+r.URL.Path+`"}`)
		}
	}))
	t.Cleanup(f.upstream.Close)
	t.Setenv("COHESION_HOST_CREDENTIAL", "operator-upstream-token")
	f.store = resources.NewMemoryStore()
	f.h = cohesionHandler(t, f.store, f.upstream.URL)
	f.h.Tenant = func(*http.Request) (string, error) { return f.tenant, nil }
	f.h.AccessPoint.AuthorizationServers = []string{"https://auth.example"}

	genaiOps, _ := modelapi.SelectOperations("models.generateContent", "models.streamGenerateContent")
	chatOps, _ := modelapi.SelectOperations("openai.chat-completions")
	claudeOps, _ := modelapi.SelectOperations("models.rawPredict", "models.streamRawPredict")
	base := modelapi.Surface{Origin: f.upstream.URL, APIVersion: "v1", Project: "project", Location: "global"}
	genai, chat, claude := base, base, base
	genai.ID, genai.Protocol, genai.Publisher, genai.Models, genai.Operations = "genai", clientplex.GenAI, "google", []string{"gemini-2.5-pro", "provider-error"}, genaiOps
	chat.ID, chat.Protocol, chat.Publisher, chat.Endpoint, chat.Models, chat.Operations = "chat", clientplex.OpenAIChat, "google", "openapi", []string{"chat-model"}, chatOps
	claude.ID, claude.Protocol, claude.Publisher, claude.Models, claude.Operations = "claude", clientplex.Messages, "anthropic", []string{"claude-sonnet-4-5"}, claudeOps
	for _, s := range []modelapi.Surface{genai, chat, claude} {
		f.put("model-surface", s.ID, ModelConnection{Surface: s, IdentityRef: "host"})
	}
	for id, tools := range map[string][]string{"github": {"search", "delete"}, "search": {"search", "searchx", "other"}, "broken": {"search"}} {
		endpoint := f.upstream.URL + "/mcp/" + id
		f.put("tool-connection", id, ToolConnection{ID: id, ResourceURL: endpoint, Execution: &toolexec.Connection{ID: id, Endpoint: endpoint, IdentityRef: "host", AllowedTools: tools, ProtocolVersion: "2025-06-18", Mode: toolexec.StatelessJSON}})
	}
	f.put(clientplex.Kind, "ide", clientplex.Registration{ID: "ide", ProfileID: "codex", Mode: "gateway", Protocol: clientplex.Responses, PrincipalBinding: "principal:any", AuthMode: "oidc"})
	f.put(AccessPointKind, "ide", apSpec())

	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), keyRequestID, "request-1")
			if f.identity != nil {
				ctx = context.WithValue(ctx, keyWIFIdentity, f.identity)
				ctx = context.WithValue(ctx, keyWIFAccess, f.access)
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	})
	f.h.MountAccessPointEndpoints(router)
	router.Route("/api/v1", func(r chi.Router) { f.h.MountAccessPoints(r) })
	f.router = router
	f.as(apUser("alice", "platform-eng"), apAllScopes...)
	return f
}

var apAllScopes = []string{"llm:surface:genai:models.generateContent", "llm:surface:genai:models.streamGenerateContent", "llm:surface:chat:openai.chat-completions", "llm:surface:claude:models.rawPredict", "llm:surface:claude:models.streamRawPredict", "llm:model:gemini-2.5-pro", "llm:model:provider-error", "llm:model:chat-model", "llm:model:claude-sonnet-4-5", "mcp:tools:search", "mcp:tools:searchx", "mcp:tools:delete", "mcp:tools:other"}

func apSpec() AccessPoint {
	return AccessPoint{ID: "ide", DisplayName: "IDE", CallerKinds: []string{CallerUser, CallerClient},
		Models: []AccessPointModel{{Name: "gemini-pro", SurfaceRef: "genai", NativeModel: "gemini-2.5-pro"}, {Name: "provider-error", SurfaceRef: "genai", NativeModel: "provider-error"}, {Name: "chat-model", SurfaceRef: "chat", NativeModel: "chat-model"}, {Name: "claude", SurfaceRef: "claude", NativeModel: "claude-sonnet-4-5"}},
		Tools:  []AccessPointTool{{ConnectionRef: "github"}, {ConnectionRef: "search", Expose: []string{"search*"}}, {ConnectionRef: "broken"}}}
}

func decodeJSON(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("response is not JSON (%d): %q", w.Code, w.Body.String())
	}
	return out
}

func TestAccessPointValidation(t *testing.T) {
	f := newAPFixture(t)
	other := apSpec()
	f.store.Put(context.Background(), resources.Record{TenantID: "other", Kind: clientplex.Kind, ID: "foreign", Spec: json.RawMessage(`{"id":"foreign"}`)}, 0)
	f.put("tool-connection", "bad__name", ToolConnection{ID: "bad__name"})
	mutate := func(fn func(*AccessPoint)) []byte {
		ap := other
		ap.Models = append([]AccessPointModel(nil), other.Models...)
		ap.Tools = append([]AccessPointTool(nil), other.Tools...)
		fn(&ap)
		raw, _ := json.Marshal(ap)
		return raw
	}
	cases := []struct {
		name string
		raw  []byte
		ok   bool
	}{
		{"valid", mutate(func(*AccessPoint) {}), true},
		{"valid subjects", mutate(func(a *AccessPoint) {
			a.Subjects = AccessPointSubjects{Groups: []quotaplex.GroupRef{{Source: "s", ID: "g"}}, ClientRegistrations: []string{"ide"}}
		}), true},
		{"unknown field", []byte(`{"id":"ide","caller_kinds":["user"],"models":[{"name":"chat-model","surface_ref":"chat","native_model":"chat-model"}],"extra":1}`), false},
		{"unknown nested field", []byte(`{"id":"ide","caller_kinds":["user"],"models":[{"name":"chat-model","surface_ref":"chat","native_model":"chat-model","weight":1}]}`), false},
		{"id mismatch", mutate(func(a *AccessPoint) { a.ID = "other" }), false},
		{"no caller kinds", mutate(func(a *AccessPoint) { a.CallerKinds = nil }), false},
		{"bad caller kind", mutate(func(a *AccessPoint) { a.CallerKinds = []string{"robot"} }), false},
		{"duplicate caller kind", mutate(func(a *AccessPoint) { a.CallerKinds = []string{"user", "user"} }), false},
		{"no resources", mutate(func(a *AccessPoint) { a.Models, a.Tools = nil, nil }), false},
		{"duplicate model name", mutate(func(a *AccessPoint) { a.Models = append(a.Models, a.Models[0]) }), false},
		{"unknown surface", mutate(func(a *AccessPoint) { a.Models[0].SurfaceRef = "missing" }), false},
		{"native model not on surface", mutate(func(a *AccessPoint) { a.Models[0].NativeModel = "unlisted" }), false},
		{"alias on OpenAI chat, rewritten in place", mutate(func(a *AccessPoint) { a.Models[2].Name = "alias" }), true},
		{"unknown connection", mutate(func(a *AccessPoint) { a.Tools[0].ConnectionRef = "missing" }), false},
		{"ambiguous connection id", mutate(func(a *AccessPoint) { a.Tools[0].ConnectionRef = "bad__name" }), false},
		{"trailing underscore connection", mutate(func(a *AccessPoint) { a.Tools[0].ConnectionRef = "github_" }), false},
		{"duplicate connection", mutate(func(a *AccessPoint) { a.Tools = append(a.Tools, a.Tools[0]) }), false},
		{"bad expose", mutate(func(a *AccessPoint) { a.Tools[1].Expose = []string{"se*rch"} }), false},
		{"empty expose", []byte(`{"id":"ide","caller_kinds":["user"],"tools":[{"connection_ref":"search","expose":[]}]}`), false},
		{"unknown client registration", mutate(func(a *AccessPoint) { a.Subjects.ClientRegistrations = []string{"missing"} }), false},
		{"cross-tenant client registration", mutate(func(a *AccessPoint) { a.Subjects.ClientRegistrations = []string{"foreign"} }), false},
		{"bad group", mutate(func(a *AccessPoint) { a.Subjects.Groups = []quotaplex.GroupRef{{ID: "g"}} }), false},
		{"skills without registry", mutate(func(a *AccessPoint) { a.Skills = []AccessPointSkill{{InstanceRef: "runbooks"}} }), false},
		{"budget ref without quota", mutate(func(a *AccessPoint) { a.QoS = &AccessPointQoS{Class: "interactive", BudgetRefs: []string{"b"}} }), false},
		{"bad qos class", mutate(func(a *AccessPoint) { a.QoS = &AccessPointQoS{Class: "Interactive"} }), false},
		{"empty qos limits", mutate(func(a *AccessPoint) { a.QoS = &AccessPointQoS{Class: "batch", Limits: &AccessPointLimit{}} }), false},
		{"negative qos limit", mutate(func(a *AccessPoint) {
			a.QoS = &AccessPointQoS{Class: "batch", Limits: &AccessPointLimit{Concurrency: -1}}
		}), false},
		{"valid qos", mutate(func(a *AccessPoint) {
			a.QoS = &AccessPointQoS{Class: "interactive", Weight: 4, Limits: &AccessPointLimit{RequestsPerMinute: 120, Concurrency: 8}}
		}), true},
	}
	for _, c := range cases {
		err := f.h.validate(context.Background(), "tenant", AccessPointKind, "ide", c.raw)
		if (err == nil) != c.ok {
			t.Errorf("%s: ok=%v err=%v", c.name, c.ok, err)
		}
	}
	// References resolve in the caller's tenant only.
	if err := f.h.validate(context.Background(), "other", AccessPointKind, "ide", mutate(func(*AccessPoint) {})); err == nil {
		t.Error("access point resolved another tenant's surfaces")
	}
}

func TestAccessPointCRUDIsAdminOnlyWithRevisionsAndTenantIsolation(t *testing.T) {
	f := newAPFixture(t)
	spec, _ := json.Marshal(apSpec())
	f.access.Roles = []models.IAMRole{models.RoleDeployer}
	if w := f.do("PUT", "/api/v1/access-points/ide", string(spec)); w.Code != 403 {
		t.Fatalf("non-admin write: %d %s", w.Code, w.Body.String())
	}
	f.access.Roles = []models.IAMRole{models.RoleAdmin}
	if w := f.do("PUT", "/api/v1/access-points/ide", string(spec)); w.Code != 409 {
		t.Fatalf("overwrite without If-Match must conflict: %d", w.Code)
	}
	w := f.do("PUT", "/api/v1/access-points/ide", string(spec), "If-Match", `"1"`)
	if w.Code != 200 || w.Header().Get("ETag") != `"2"` {
		t.Fatalf("admin update: %d %s", w.Code, w.Body.String())
	}
	if w = f.do("PUT", "/api/v1/access-points/bad", `{"id":"bad","caller_kinds":["user"]}`); w.Code != 400 {
		t.Fatalf("invalid spec accepted: %d", w.Code)
	}
	if w = f.do("GET", "/api/v1/access-points/ide", ""); w.Code != 200 {
		t.Fatalf("read: %d", w.Code)
	}
	if w = f.do("DELETE", "/api/v1/access-points/ide", ""); w.Code != 428 {
		t.Fatalf("delete without revision: %d", w.Code)
	}
	f.tenant = "other"
	if w = f.do("GET", "/api/v1/access-points/ide", ""); w.Code != 404 {
		t.Fatalf("cross-tenant read: %d", w.Code)
	}
	if w = f.do("POST", "/ap/ide/v1/chat/completions", `{"model":"chat-model"}`); w.Code != 404 {
		t.Fatalf("cross-tenant access point use: %d", w.Code)
	}
	f.tenant = "tenant"
	f.access.Roles = nil
	if w = f.do("DELETE", "/api/v1/access-points/ide", "", "If-Match", `"2"`); w.Code != 403 {
		t.Fatalf("non-admin delete: %d", w.Code)
	}
}

func TestAccessPointCallerKindDerivation(t *testing.T) {
	f := newAPFixture(t)
	user := apUser("alice")
	ideUser := apUser("bob")
	ideUser.AuthorizedParty = "ide"
	unknownClient := apUser("carol")
	unknownClient.AuthorizedParty = "not-registered"
	delegated := apUser("dave")
	delegated.ActorID, delegated.AuthorizedParty = "principal-agent", "ide"
	workload := &models.WIFIdentity{PrincipalID: "principal-workload", Subject: "wl", Provider: apIssuer, PoolID: "agents"}
	cases := []struct {
		identity     *models.WIFIdentity
		kind, client string
	}{
		{user, CallerUser, ""},
		{ideUser, CallerClient, "ide"},
		{unknownClient, CallerUser, ""},
		{delegated, CallerAgent, "ide"},
		{workload, CallerAgent, ""},
	}
	for _, c := range cases {
		got, err := f.h.resolveAccessPointCaller(context.Background(), "tenant", c.identity)
		if err != nil || got.Kind != c.kind || got.ClientID != c.client {
			t.Errorf("%s: got %+v %v want %s/%s", c.identity.Subject, got, err, c.kind, c.client)
		}
	}
	// A registration in another tenant never makes a caller a client.
	if got, _ := f.h.resolveAccessPointCaller(context.Background(), "other", ideUser); got.Kind != CallerUser || got.ClientID != "" {
		t.Errorf("cross-tenant registration matched: %+v", got)
	}

	source := auth.MembershipSource(apIssuer, "people")
	platform := apUser("erin", "platform-eng")
	platformIDE := apUser("frank", "platform-eng")
	platformIDE.AuthorizedParty = "ide"
	staleGroups := apUser("gina", "platform-eng")
	staleGroups.VerifiedEvidence = &models.IdentityEvidence{GroupsPresent: false}
	subjects := AccessPointSubjects{Groups: []quotaplex.GroupRef{{Source: source, ID: "platform-eng"}}, ClientRegistrations: []string{"ide"}}
	ap := AccessPoint{CallerKinds: []string{CallerUser, CallerClient, CallerAgent}, Subjects: subjects}
	admit := func(id *models.WIFIdentity) bool {
		caller, _ := f.h.resolveAccessPointCaller(context.Background(), "tenant", id)
		return ap.admits(id, caller)
	}
	if admit(platform) {
		t.Error("group alone satisfied an AND client constraint")
	}
	if !admit(platformIDE) {
		t.Error("group member through the registered client was refused")
	}
	if admit(ideUser) {
		t.Error("registered client alone satisfied an AND group constraint")
	}
	if admit(staleGroups) {
		t.Error("missing group evidence satisfied a group constraint")
	}
	ap.Subjects = AccessPointSubjects{Principals: []string{user.PrincipalID}, Agents: []string{"principal-workload"}}
	if !admit(user) || !admit(workload) || admit(platform) {
		t.Error("principal/agent constraints misapplied")
	}
	ap.Subjects, ap.CallerKinds = AccessPointSubjects{}, []string{CallerAgent}
	if admit(user) || !admit(delegated) {
		t.Error("caller_kinds misapplied")
	}
	// The gate checks the caller before any reference is used.
	f.put(AccessPointKind, "agents-only", AccessPoint{ID: "agents-only", CallerKinds: []string{CallerAgent}, Models: []AccessPointModel{{Name: "x", SurfaceRef: "missing", NativeModel: "x"}}})
	w := f.do("POST", "/ap/agents-only/v1/chat/completions", `{"model":"x"}`)
	if w.Code != 403 || !strings.Contains(w.Body.String(), "permission_error") {
		t.Fatalf("caller check did not run first: %d %s", w.Code, w.Body.String())
	}
}

func TestAccessPointModelProtocolsRouteToNativeSurfaces(t *testing.T) {
	f := newAPFixture(t)
	body := `{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"native_extension":{"keep":true}}`
	w := f.do("POST", "/ap/ide/v1beta/models/gemini-pro:generateContent", body, "Authorization", "Bearer caller-token")
	calls := f.upstreamCalls()
	if w.Code != 200 || len(calls) != 1 || calls[0].Path != "/v1/projects/project/locations/global/publishers/google/models/gemini-2.5-pro:generateContent" || calls[0].Body != body || calls[0].Authorization != "Bearer operator-upstream-token" {
		t.Fatalf("GenAI routing: %d %s %+v", w.Code, w.Body.String(), calls)
	}

	w = f.do("POST", "/ap/ide/v1beta/models/gemini-pro:streamGenerateContent?alt=sse", body)
	calls = f.upstreamCalls()
	want := "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"a\"}]}}]}\r\n\r\ndata: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"b\"}]}}]}\r\n\r\n"
	if w.Code != 200 || w.Body.String() != want || calls[1].Query != "alt=sse" || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("stream not passed through: %d %q %+v", w.Code, w.Body.String(), calls[1])
	}

	chat := `{"model":"chat-model","messages":[{"role":"user","content":"hi"}],"stream":false,"vendor":{"x":1}}`
	w = f.do("POST", "/ap/ide/v1/chat/completions", chat)
	calls = f.upstreamCalls()
	if w.Code != 200 || calls[2].Path != "/v1/projects/project/locations/global/endpoints/openapi/chat/completions" || calls[2].Body != chat {
		t.Fatalf("chat routing: %d %s %+v", w.Code, w.Body.String(), calls[2])
	}

	w = f.do("POST", "/ap/ide/v1/messages", `{"model":"claude","max_tokens":5,"messages":[{"role":"user","content":"hi"}]}`)
	calls = f.upstreamCalls()
	if w.Code != 200 || calls[3].Path != "/v1/projects/project/locations/global/publishers/anthropic/models/claude-sonnet-4-5:rawPredict" || strings.Contains(calls[3].Body, `"model"`) || !strings.Contains(calls[3].Body, "vertex-2023-10-16") {
		t.Fatalf("messages routing: %d %s %+v", w.Code, w.Body.String(), calls[3])
	}

	// Provider errors are relayed unchanged.
	w = f.do("POST", "/ap/ide/v1beta/models/provider-error:generateContent", `{"contents":[]}`)
	if w.Code != 400 || w.Body.String() != `{"error":{"code":400,"message":"provider says no","status":"INVALID_ARGUMENT"}}` {
		t.Fatalf("provider error rewritten: %d %s", w.Code, w.Body.String())
	}
	if len(f.upstreamCalls()) != 5 {
		t.Fatal("unexpected upstream calls", f.upstreamCalls())
	}
}

func TestAccessPointModelErrorsAreProtocolNative(t *testing.T) {
	f := newAPFixture(t)
	type check func(map[string]any) bool
	openai := func(kind string) check {
		return func(m map[string]any) bool {
			e, _ := m["error"].(map[string]any)
			return e != nil && e["type"] == kind && e["message"] != ""
		}
	}
	anthropic := func(kind string) check {
		return func(m map[string]any) bool {
			e, _ := m["error"].(map[string]any)
			return m["type"] == "error" && e != nil && e["type"] == kind
		}
	}
	google := func(status string, code float64) check {
		return func(m map[string]any) bool {
			e, _ := m["error"].(map[string]any)
			return e != nil && e["status"] == status && e["code"] == code
		}
	}
	cases := []struct {
		name, path, body string
		status           int
		ok               check
		mention          string
	}{
		{"openai unknown model", "/ap/ide/v1/chat/completions", `{"model":"nope"}`, 404, openai("not_found_error"), "nope"},
		{"openai wrong protocol", "/ap/ide/v1/chat/completions", `{"model":"claude"}`, 400, openai("invalid_request_error"), "anthropic-messages (/v1/messages)"},
		{"responses wrong protocol", "/ap/ide/v1/responses", `{"model":"gemini-pro"}`, 400, openai("invalid_request_error"), "google-genai"},
		{"anthropic wrong protocol", "/ap/ide/v1/messages", `{"model":"chat-model"}`, 400, anthropic("invalid_request_error"), "openai-chat-completions"},
		{"anthropic no model", "/ap/ide/v1/messages", `{"messages":[]}`, 400, anthropic("invalid_request_error"), "model"},
		{"google unknown model", "/ap/ide/v1beta/models/nope:generateContent", `{}`, 404, google("NOT_FOUND", 404), "nope"},
		{"google wrong protocol", "/ap/ide/v1beta/models/claude:generateContent", `{}`, 400, google("INVALID_ARGUMENT", 400), "anthropic-messages"},
		{"duplicate model key", "/ap/ide/v1/chat/completions", `{"model":"chat-model","model":"claude"}`, 400, openai("invalid_request_error"), ""},
	}
	for _, c := range cases {
		w := f.do("POST", c.path, c.body)
		if w.Code != c.status || !c.ok(decodeJSON(t, w)) || !strings.Contains(w.Body.String(), c.mention) {
			t.Errorf("%s: %d %s", c.name, w.Code, w.Body.String())
		}
	}
	// Managed-path refusals (here a missing model grant) use the same shape.
	f.as(apUser("alice"), "llm:surface:chat:openai.chat-completions")
	w := f.do("POST", "/ap/ide/v1/chat/completions", `{"model":"chat-model"}`)
	if w.Code != 403 || !openai("permission_error")(decodeJSON(t, w)) {
		t.Fatalf("managed refusal not native: %d %s", w.Code, w.Body.String())
	}
	w = f.do("POST", "/ap/ide/v1beta/models/gemini-pro:generateContent", `{}`)
	if w.Code != 403 || !google("PERMISSION_DENIED", 403)(decodeJSON(t, w)) {
		t.Fatalf("managed refusal not native: %d %s", w.Code, w.Body.String())
	}
	// Caller not admitted, and unauthenticated.
	f.put(AccessPointKind, "agents", AccessPoint{ID: "agents", CallerKinds: []string{CallerAgent}, Models: apSpec().Models})
	w = f.do("POST", "/ap/agents/v1/messages", `{"model":"claude"}`)
	if w.Code != 403 || !anthropic("permission_error")(decodeJSON(t, w)) {
		t.Fatalf("caller denial not native: %d %s", w.Code, w.Body.String())
	}
	f.as(nil)
	w = f.do("POST", "/ap/ide/v1/chat/completions", `{"model":"chat-model"}`)
	if w.Code != 401 || !openai("authentication_error")(decodeJSON(t, w)) || w.Header().Get("WWW-Authenticate") == "" {
		t.Fatalf("unauthenticated not native: %d %s", w.Code, w.Body.String())
	}
	if len(f.upstreamCalls()) != 0 {
		t.Fatal("refused requests reached the provider", f.upstreamCalls())
	}
}

func TestAccessPointModelListingIsFilteredToCallerGrants(t *testing.T) {
	f := newAPFixture(t)
	f.as(apUser("alice"), "llm:model:gemini-2.5-pro", "llm:model:claude-sonnet-4-5")
	w := f.do("GET", "/ap/ide/v1/models", "")
	got := decodeJSON(t, w)
	data, _ := got["data"].([]any)
	if w.Code != 200 || got["object"] != "list" || len(data) != 2 {
		t.Fatalf("openai listing: %d %s", w.Code, w.Body.String())
	}
	w = f.do("GET", "/ap/ide/v1/models", "", "anthropic-version", "2023-06-01")
	got = decodeJSON(t, w)
	if data, _ = got["data"].([]any); len(data) != 2 || got["has_more"] != false || data[0].(map[string]any)["type"] != "model" {
		t.Fatalf("anthropic listing: %s", w.Body.String())
	}
	w = f.do("GET", "/ap/ide/v1beta/models", "")
	got = decodeJSON(t, w)
	if models, _ := got["models"].([]any); len(models) != 1 || models[0].(map[string]any)["name"] != "models/gemini-pro" {
		t.Fatalf("genai listing: %s", w.Body.String())
	}
}

func mcpPost(f *apFixture, path, body string, headers ...string) *httptest.ResponseRecorder {
	return f.do("POST", path, body, append([]string{"Content-Type", "application/json", "Accept", "application/json, text/event-stream", "MCP-Protocol-Version", "2025-06-18"}, headers...)...)
}

func TestAccessPointMCPLifecycleAndAggregation(t *testing.T) {
	f := newAPFixture(t)
	w := mcpPost(f, "/ap/ide/mcp", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`, "MCP-Protocol-Version", "")
	got := decodeJSON(t, w)
	result, _ := got["result"].(map[string]any)
	if w.Code != 200 || got["id"] != float64(1) || result["protocolVersion"] != "2025-06-18" || w.Header().Get("Mcp-Session-Id") != "" {
		t.Fatalf("initialize: %d %s", w.Code, w.Body.String())
	}
	w = mcpPost(f, "/ap/ide/mcp", `{"jsonrpc":"2.0","id":2,"method":"initialize","params":{"protocolVersion":"2099-01-01"}}`)
	if result = decodeJSON(t, w)["result"].(map[string]any); result["protocolVersion"] != "2025-11-25" {
		t.Fatalf("version negotiation: %s", w.Body.String())
	}
	if w = mcpPost(f, "/ap/ide/mcp", `{"jsonrpc":"2.0","method":"notifications/initialized"}`); w.Code != 202 || w.Body.Len() != 0 {
		t.Fatalf("notification: %d %q", w.Code, w.Body.String())
	}
	if w = mcpPost(f, "/ap/ide/mcp", `{"jsonrpc":"2.0","id":"p","method":"ping"}`); w.Code != 200 || !strings.Contains(w.Body.String(), `"result":{}`) {
		t.Fatalf("ping: %d %s", w.Code, w.Body.String())
	}
	if w = f.do("GET", "/ap/ide/mcp", ""); w.Code != 405 || w.Header().Get("Allow") != "POST" {
		t.Fatalf("GET: %d", w.Code)
	}
	if w = f.do("DELETE", "/ap/ide/mcp/github", ""); w.Code != 405 {
		t.Fatalf("DELETE: %d", w.Code)
	}

	w = mcpPost(f, "/ap/ide/mcp", `{"jsonrpc":"2.0","id":3,"method":"tools/list"}`)
	result, _ = decodeJSON(t, w)["result"].(map[string]any)
	tools, _ := result["tools"].([]any)
	var names []string
	for _, tool := range tools {
		names = append(names, tool.(map[string]any)["name"].(string))
	}
	if w.Code != 200 || strings.Join(names, ",") != "github__search,github__delete,search__search,search__searchx" || strings.Contains(w.Body.String(), "secret") {
		t.Fatalf("aggregate list: %d %s", w.Code, w.Body.String())
	}

	w = mcpPost(f, "/ap/ide/mcp/search", `{"jsonrpc":"2.0","id":"s","method":"tools/list"}`)
	if !strings.Contains(w.Body.String(), `"name":"searchx"`) || strings.Contains(w.Body.String(), `"other"`) || strings.Contains(w.Body.String(), "__") || !strings.Contains(w.Body.String(), `"id":"s"`) {
		t.Fatalf("single list: %s", w.Body.String())
	}

	before := len(f.upstreamCalls())
	w = mcpPost(f, "/ap/ide/mcp", `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"github__search","arguments":{"q":"x"},"_meta":{"progressToken":1}}}`)
	calls := f.upstreamCalls()
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"text":"github:search"`) || !strings.Contains(w.Body.String(), `"id":7`) || len(calls) != before+1 || !strings.Contains(calls[before].Body, `"name":"search"`) || calls[before].Authorization != "Bearer operator-upstream-token" {
		t.Fatalf("aggregate call: %d %s %+v", w.Code, w.Body.String(), calls)
	}
	w = mcpPost(f, "/ap/ide/mcp/search", `{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"searchx","arguments":{}}}`)
	if !strings.Contains(w.Body.String(), `"text":"search:searchx"`) {
		t.Fatalf("single call: %s", w.Body.String())
	}

	before = len(f.upstreamCalls())
	for name, body := range map[string]string{
		"unexposed":        `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"search__other"}}`,
		"unknown conn":     `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"gitlab__search"}}`,
		"no namespace":     `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"search"}}`,
		"unknown params":   `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"github__search","task":{}}}`,
		"unknown method":   `{"jsonrpc":"2.0","id":9,"method":"resources/list"}`,
		"aggregate cursor": `{"jsonrpc":"2.0","id":9,"method":"tools/list","params":{"cursor":"c"}}`,
	} {
		w = mcpPost(f, "/ap/ide/mcp", body)
		e, _ := decodeJSON(t, w)["error"].(map[string]any)
		if e == nil || !strings.Contains(w.Body.String(), `"id":9`) {
			t.Errorf("%s: %d %s", name, w.Code, w.Body.String())
		}
		if name == "unknown method" && e["code"] != float64(rpcMethodNotFound) {
			t.Errorf("unknown method code: %v", e["code"])
		}
	}
	if len(f.upstreamCalls()) != before {
		t.Fatal("rejected calls reached a tool server")
	}
	if w = mcpPost(f, "/ap/ide/mcp/other", `{"jsonrpc":"2.0","id":1,"method":"ping"}`); w.Code != 404 {
		t.Fatalf("unexposed connection endpoint: %d", w.Code)
	}
}

func TestAccessPointMCPTransportValidationAndDenials(t *testing.T) {
	f := newAPFixture(t)
	w := mcpPost(f, "/ap/ide/mcp", `{"jsonrpc":"2.0","id":"v","method":"ping"}`, "MCP-Protocol-Version", "2026-07-28")
	e, _ := decodeJSON(t, w)["error"].(map[string]any)
	if w.Code != 400 || e == nil || e["code"] != float64(rpcInvalidRequest) || !strings.Contains(w.Body.String(), `"id":"v"`) || !strings.Contains(w.Body.String(), "2025-11-25") {
		t.Fatalf("unsupported version: %d %s", w.Code, w.Body.String())
	}
	if w = mcpPost(f, "/ap/ide/mcp", `[{"jsonrpc":"2.0","id":1,"method":"ping"}]`); w.Code != 400 {
		t.Fatalf("batch: %d", w.Code)
	}
	if w = f.do("POST", "/ap/ide/mcp", `{"jsonrpc":"2.0","id":1,"method":"ping"}`, "Content-Type", "text/plain"); w.Code != 415 {
		t.Fatalf("content type: %d", w.Code)
	}
	if w = mcpPost(f, "/ap/ide/mcp", `{"jsonrpc":"2.0","id":1,"method":"ping"}`, "Origin", "https://evil.example"); w.Code != 403 {
		t.Fatalf("origin: %d", w.Code)
	}
	if w = mcpPost(f, "/ap/ide/mcp", `{"jsonrpc":"2.0","id":1,"method":"ping"}`, "Origin", "https://gateway.example"); w.Code != 200 {
		t.Fatalf("public origin refused: %d", w.Code)
	}
	if w = mcpPost(f, "/ap/ide/mcp", `{"jsonrpc":"2.0","id":{"x":1},"method":"ping"}`); w.Code != 400 {
		t.Fatalf("object id accepted: %d", w.Code)
	}

	// Caller scope is enforced by the managed tool path, per tool.
	f.as(apUser("alice"), "mcp:tools:search")
	w = mcpPost(f, "/ap/ide/mcp", `{"jsonrpc":"2.0","id":"d","method":"tools/call","params":{"name":"github__delete"}}`)
	e, _ = decodeJSON(t, w)["error"].(map[string]any)
	if e == nil || e["code"] != float64(toolexec.RPCForbidden) || !strings.Contains(w.Body.String(), `"id":"d"`) {
		t.Fatalf("scope denial: %d %s", w.Code, w.Body.String())
	}
	w = mcpPost(f, "/ap/ide/mcp", `{"jsonrpc":"2.0","id":3,"method":"tools/list"}`)
	if strings.Contains(w.Body.String(), "delete") || strings.Contains(w.Body.String(), "searchx") || !strings.Contains(w.Body.String(), "github__search") {
		t.Fatalf("list not filtered by scope: %s", w.Body.String())
	}
	// Connection policy is enforced by the managed tool path too.
	f.as(apUser("alice"), apAllScopes...)
	connection := ToolConnection{ID: "github", ResourceURL: f.upstream.URL + "/mcp/github", Execution: &toolexec.Connection{ID: "github", Endpoint: f.upstream.URL + "/mcp/github", IdentityRef: "host", AllowedTools: []string{"search", "delete"}, ProtocolVersion: "2025-06-18", Mode: toolexec.StatelessJSON}, Tools: &toolexec.ToolPolicy{Deny: []string{"delete"}}}
	record, _ := f.store.Get(context.Background(), "tenant", "tool-connection", "github")
	raw, _ := json.Marshal(connection)
	f.store.Put(context.Background(), resources.Record{TenantID: "tenant", Kind: "tool-connection", ID: "github", Spec: raw}, record.Revision)
	before := len(f.upstreamCalls())
	w = mcpPost(f, "/ap/ide/mcp", `{"jsonrpc":"2.0","id":"p","method":"tools/call","params":{"name":"github__delete"}}`)
	if !strings.Contains(w.Body.String(), "connection_policy") || len(f.upstreamCalls()) != before {
		t.Fatalf("connection policy bypassed: %s", w.Body.String())
	}

	// Caller-kind denial preserves the id; unauthenticated gets an RFC 9728 challenge.
	f.put(AccessPointKind, "agents", AccessPoint{ID: "agents", CallerKinds: []string{CallerAgent}, Tools: []AccessPointTool{{ConnectionRef: "github"}}})
	w = mcpPost(f, "/ap/agents/mcp", `{"jsonrpc":"2.0","id":"k","method":"tools/list"}`)
	if w.Code != 403 || !strings.Contains(w.Body.String(), `"id":"k"`) {
		t.Fatalf("caller denial: %d %s", w.Code, w.Body.String())
	}
	f.as(nil)
	w = mcpPost(f, "/ap/ide/mcp/github", `{"jsonrpc":"2.0","id":1,"method":"initialize"}`)
	if w.Code != 401 || w.Header().Get("WWW-Authenticate") != `Bearer resource_metadata="https://gateway.example/.well-known/oauth-protected-resource/ap/ide/mcp/github"` {
		t.Fatalf("challenge: %d %q", w.Code, w.Header().Get("WWW-Authenticate"))
	}
}

func TestAccessPointProtectedResourceMetadata(t *testing.T) {
	f := newAPFixture(t)
	f.as(nil)
	for path, resource := range map[string]string{
		"/.well-known/oauth-protected-resource/ap/ide/mcp":        "https://gateway.example/ap/ide/mcp",
		"/.well-known/oauth-protected-resource/ap/ide/mcp/github": "https://gateway.example/ap/ide/mcp/github",
		"/ap/ide/.well-known/oauth-protected-resource":            "https://gateway.example/ap/ide/mcp",
		"/ap/ide/.well-known/oauth-protected-resource/mcp/search": "https://gateway.example/ap/ide/mcp/search",
	} {
		w := f.do("GET", path, "")
		got := decodeJSON(t, w)
		servers, _ := got["authorization_servers"].([]any)
		if w.Code != 200 || got["resource"] != resource || len(servers) != 1 || servers[0] != "https://auth.example" {
			t.Errorf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
	if w := f.do("GET", "/.well-known/oauth-protected-resource/ap/bad%20id/mcp", ""); w.Code != 404 {
		t.Errorf("invalid resource accepted: %d", w.Code)
	}
}

func TestAccessPointCatalogListing(t *testing.T) {
	f := newAPFixture(t)
	w := f.do("GET", "/ap/ide/v0.1/servers", "")
	got := decodeJSON(t, w)
	templates, _ := got["templates"].([]any)
	if w.Code != 200 || len(templates) != 4 || got["total"] != float64(4) {
		t.Fatalf("catalog: %d %s", w.Code, w.Body.String())
	}
	first := templates[0].(map[string]any)
	remotes := first["remotes"].([]any)
	if first["plane"] != "mcplex" || remotes[0].(map[string]any)["url"] != "https://gateway.example/ap/ide/mcp" || remotes[0].(map[string]any)["type"] != "streamable-http" {
		t.Fatalf("catalog entry: %v", first)
	}
	f.as(nil)
	if w = f.do("GET", "/ap/ide/v0.1/servers", ""); w.Code != 401 {
		t.Fatalf("anonymous catalog: %d", w.Code)
	}
}

// The listing also has the shape of the MCP Registry API (server.json schema
// 2025-12-11): servers[] of {server, _meta}, metadata.count, names that match
// the schema's pattern, descriptions within its limit and remotes of a known
// transport type with an http(s) URL.
func TestAccessPointCatalogIsRegistryShaped(t *testing.T) {
	f := newAPFixture(t)
	w := f.do("GET", "/ap/ide/v0.1/servers", "")
	var got struct {
		Servers []struct {
			Server struct {
				Schema      string `json:"$schema"`
				Name        string `json:"name"`
				Title       string `json:"title"`
				Description string `json:"description"`
				Version     string `json:"version"`
				Remotes     []struct {
					Type string `json:"type"`
					URL  string `json:"url"`
				} `json:"remotes"`
			} `json:"server"`
			Meta map[string]any `json:"_meta"`
		} `json:"servers"`
		Metadata struct {
			Count int `json:"count"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != 200 || len(got.Servers) != 4 || got.Metadata.Count != 4 {
		t.Fatalf("registry listing: %d %s", w.Code, w.Body.String())
	}
	seen := map[string]bool{}
	for _, e := range got.Servers {
		s := e.Server
		if s.Schema != "https://static.modelcontextprotocol.io/schemas/2025-12-11/server.schema.json" || !registryServerName.MatchString(s.Name) || seen[s.Name] || strings.Count(s.Name, "/") != 1 {
			t.Errorf("name or schema: %+v", s)
		}
		seen[s.Name] = true
		if s.Description == "" || len([]rune(s.Description)) > 100 || len([]rune(s.Title)) > 100 || s.Version == "" || len(s.Remotes) != 1 || s.Remotes[0].Type != "streamable-http" || !strings.HasPrefix(s.Remotes[0].URL, "https://gateway.example/ap/ide/mcp") {
			t.Errorf("entry: %+v", s)
		}
		if e.Meta == nil || len(e.Meta) != 0 {
			t.Errorf("_meta must be an empty object (this is not the official registry): %v", e.Meta)
		}
	}
	if !seen["example.gateway/ap-ide"] {
		t.Errorf("names are not reverse-DNS of the public host: %v", seen)
	}
}
