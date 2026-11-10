package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/vamsiramakrishnan/aiplex/internal/models"
	"github.com/vamsiramakrishnan/aiplex/pkg/clientplex"
	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
)

// overviewTokenEnv is the placeholder variable every snippet reads the bearer
// token from. No snippet ever contains a token or secret value.
const overviewTokenEnv = "AIPLEX_TOKEN"

// OverviewSnippet is one ready-to-paste block.
type OverviewSnippet struct {
	Title    string `json:"title"`
	Language string `json:"language"`
	Content  string `json:"content"`
}

// OverviewConnectAuth says how a client obtains and presents a token.
type OverviewConnectAuth struct {
	Scheme               string   `json:"scheme"`
	Header               string   `json:"header"`
	TokenEnv             string   `json:"token_env"`
	CallerKinds          []string `json:"caller_kinds"`
	AuthorizationServers []string `json:"authorization_servers,omitempty"`
	ResourceMetadataURL  string   `json:"resource_metadata_url,omitempty"`
	Steps                []string `json:"steps"`
}

// OverviewConnectModel is a model and the protocols that reach it.
type OverviewConnectModel struct {
	Name      string   `json:"name"`
	Protocols []string `json:"protocols"`
}

// OverviewEndpoint is a model endpoint that does not fit a single base URL:
// Bedrock Runtime (the model is a path segment) and WebSocket sessions.
// Transport is http, http-stream or websocket. A "{model}" in URL is replaced
// by a model name; Query lists the query parameters a connection requires.
// WebSocket URLs use wss (or ws when the public URL is http).
type OverviewEndpoint struct {
	Protocol  string   `json:"protocol"`
	Transport string   `json:"transport"`
	URL       string   `json:"url"`
	Label     string   `json:"label,omitempty"`
	Query     []string `json:"query,omitempty"`
	Models    []string `json:"models"`
}

// OverviewConnect is the response of GET /overview/access-points/{id}/connect.
type OverviewConnect struct {
	AccessPointID string                 `json:"access_point_id"`
	Client        string                 `json:"client"`
	BaseURLs      map[string]string      `json:"base_urls"`
	Endpoints     []OverviewEndpoint     `json:"endpoints"`
	Models        []OverviewConnectModel `json:"models"`
	MCPURL        string                 `json:"mcp_url,omitempty"`
	MCPCatalogURL string                 `json:"mcp_catalog_url,omitempty"`
	SkillsURL     string                 `json:"skills_url,omitempty"`
	Auth          OverviewConnectAuth    `json:"auth"`
	Snippets      []OverviewSnippet      `json:"snippets"`
	Notes         []string               `json:"notes"`
}

// overviewWireFamily maps a wire protocol to the base-url family it uses.
func overviewWireFamily(wire string) string {
	switch wire {
	case WireOpenAIChat, WireOpenAIResponses, WireOpenAIEmbeddings, WireOpenAIModerations, WireOpenAIImages, WireOpenAIAudio:
		return "openai"
	case WireAnthropic:
		return "anthropic"
	case WireGenAI:
		return "genai"
	}
	return ""
}

// overviewProfileUsesAccessPoint reports whether a client profile can speak to
// an access point: it has at least one model wire protocol (not only the
// Claude apps gateway protocol, which needs its own gateway server).
func overviewProfileUsesAccessPoint(p clientplex.Profile) bool {
	for _, protocol := range p.Protocols {
		switch protocol {
		case clientplex.OpenAIChat, clientplex.Responses, clientplex.Messages, clientplex.GenAI:
			return true
		}
	}
	return false
}

// ConnectForCaller serves GET /overview/access-points/{id}/connect to any
// verified caller. Admins and deployers (and development mode) get the full
// response. Every other caller gets it only for an access point that admits it
// (the same check as the filtered access-point view), with the operator-only
// detail removed (see connectRestricted); an access point that does not admit
// the caller answers exactly like a missing one.
func (h *OverviewHandler) ConnectForCaller(w http.ResponseWriter, r *http.Request) {
	development := h.Connected != nil && h.Connected.Development
	h.connect(w, r, !privilegedOrDevelopment(r, development, models.RoleAdmin, models.RoleDeployer))
}

// Connect handles GET /overview/access-points/{id}/connect?client=… for
// operators.
//
// Every URL is built from the configured public URL, never from the request
// Host header.
func (h *OverviewHandler) Connect(w http.ResponseWriter, r *http.Request) { h.connect(w, r, false) }

// connect builds the connect response. restricted is set for callers without
// an operator role. Those callers are admitted by the access point or told it
// does not exist, and the response omits, field by field:
//
//   - auth.caller_kinds: reduced to the caller's own kind (the access point's
//     full list is configuration);
//   - auth.steps: only the step for the caller's own kind, so no operator CLI
//     hints for kinds it is not;
//   - notes naming a model's surface reference (internal resource ids) and the
//     list of registered client ids the access point admits (other subjects).
//
// Kept: base URLs, endpoints, model names and protocols, MCP and skills URLs,
// authorization servers and resource-metadata URL (all published to callers
// anyway), and the snippets, which are built only from those.
func (h *OverviewHandler) connect(w http.ResponseWriter, r *http.Request, restricted bool) {
	tenant, ok := h.tenant(w, r)
	if !ok {
		return
	}
	if err := overviewParams(r, "client"); err != nil {
		overviewQueryError(w, r, err.Error())
		return
	}
	client := r.URL.Query().Get("client")
	if client == "" {
		client = "generic"
	}
	var profile clientplex.Profile
	if client != "generic" {
		p, err := clientplex.FindProfile(client)
		if err != nil {
			ids := []string{"generic"}
			for _, known := range clientplex.Profiles() {
				if overviewProfileUsesAccessPoint(known) {
					ids = append(ids, known.ID)
				}
			}
			Error(w, r, http.StatusNotFound, "UNKNOWN_CLIENT", "client must be one of: "+strings.Join(ids, ", "))
			return
		}
		profile = p
		if !overviewProfileUsesAccessPoint(p) {
			Error(w, r, http.StatusUnprocessableEntity, "CLIENT_NOT_APPLICABLE", fmt.Sprintf("client %s speaks %s, which signs in through a Claude apps gateway server rather than an access point; use claude-code (Anthropic Messages) for an access point", p.ID, strings.Join(p.Protocols, ", ")))
			return
		}
	}
	base := h.Connected.publicBase()
	if base == "" {
		Error(w, r, http.StatusConflict, "PUBLIC_URL_REQUIRED", "connect output is built from the configured public URL, which is not set")
		return
	}
	id := chi.URLParam(r, "id")
	_, ap, err := h.Connected.loadAccessPointRecord(r.Context(), tenant, id)
	callerKind := ""
	if restricted {
		identity := GetWIFIdentity(r.Context())
		if identity == nil || identity.PrincipalID == "" {
			Error(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "verified identity required")
			return
		}
		caller, cerr := h.Connected.resolveAccessPointCaller(r.Context(), tenant, identity)
		if cerr != nil {
			Error(w, r, http.StatusServiceUnavailable, "CALLER_UNAVAILABLE", "caller resolution is unavailable")
			return
		}
		callerKind = caller.Kind
		if err == nil && !ap.admits(identity, caller) {
			err = resources.ErrNotFound
		}
		if errors.Is(err, errAccessPointRecordInvalid) {
			err = resources.ErrNotFound // do not reveal that a record exists
		}
	}
	if err != nil {
		accessPointLoadError(w, r, err, errorsIsNotFound)
		return
	}
	ap = overviewNormalizeAccessPoint(ap)
	out := OverviewConnect{AccessPointID: id, Client: client, BaseURLs: map[string]string{}, Endpoints: []OverviewEndpoint{}, Models: []OverviewConnectModel{}, Snippets: []OverviewSnippet{}, Notes: []string{}}
	apBase := base + "/ap/" + id

	// Which wire protocols reach at least one model, and an example model each.
	wires := map[string]string{}
	surfaceProtocols := map[string]string{}
	for _, m := range ap.Models {
		conn, err := h.Connected.accessPointSurface(r.Context(), tenant, m.SurfaceRef)
		if err != nil {
			if !restricted {
				out.Notes = append(out.Notes, fmt.Sprintf("model %q is omitted: its surface %q does not resolve", m.Name, m.SurfaceRef))
			}
			continue
		}
		reach := accessPointServedWires(conn.Surface, m.NativeModel)
		out.Models = append(out.Models, OverviewConnectModel{Name: m.Name, Protocols: reach})
		for _, wire := range reach {
			if _, seen := wires[wire]; !seen {
				wires[wire] = m.Name
			}
		}
		if _, seen := surfaceProtocols[conn.Surface.Protocol]; !seen {
			surfaceProtocols[conn.Surface.Protocol] = m.Name
		}
	}
	for wire := range wires {
		switch overviewWireFamily(wire) {
		case "openai":
			out.BaseURLs["openai"] = apBase + "/v1"
		case "anthropic":
			out.BaseURLs["anthropic"] = apBase
		case "genai":
			out.BaseURLs["genai"] = apBase
		}
	}
	out.Endpoints = overviewEndpoints(base, apBase, wires, out.Models)
	if len(ap.Tools) > 0 && h.Connected.moduleEnabled("mcplex") {
		out.MCPURL = base + accessPointResourcePath(id, "")
		out.MCPCatalogURL = apBase + "/v0.1/servers"
	}
	if len(ap.Skills) > 0 && h.Connected.moduleEnabled("skillsplex") {
		out.SkillsURL = apBase + "/skills"
	}
	out.Auth = h.connectAuth(base, id, ap)
	if restricted {
		out.Auth = restrictConnectAuth(out.Auth, callerKind)
	}

	if client == "generic" {
		out.Snippets = append(out.Snippets, overviewGenericSnippets(out, wires)...)
	} else {
		snippets, notes, status, message := h.profileSnippets(r.Context(), tenant, profile, ap, apBase, wires, surfaceProtocols)
		if status != 0 {
			Error(w, r, status, "CLIENT_NOT_APPLICABLE", message)
			return
		}
		out.Snippets = append(out.Snippets, snippets...)
		out.Notes = append(out.Notes, notes...)
		if len(snippets) == 0 {
			out.Snippets = append(out.Snippets, overviewGenericSnippets(out, wires)...)
		} else {
			out.Snippets = append(out.Snippets, overviewSharedSnippets(out)...)
		}
	}
	if len(ap.Subjects.ClientRegistrations) > 0 && !restricted {
		out.Notes = append(out.Notes, "This access point admits only registered clients: the token's authorized party (azp) must equal one of "+strings.Join(ap.Subjects.ClientRegistrations, ", ")+".")
	}
	out.Notes = append(out.Notes, "Snippets read the token from $"+overviewTokenEnv+"; no token or secret is included in this output.")
	if len(out.BaseURLs) == 0 {
		out.Notes = append(out.Notes, "No model is reachable through this access point, so no model base URL is offered.")
	}
	JSON(w, http.StatusOK, out)
}

// restrictConnectAuth reduces the auth block to the caller's own kind.
func restrictConnectAuth(auth OverviewConnectAuth, kind string) OverviewConnectAuth {
	prefix := map[string]string{CallerUser: "Users:", CallerAgent: "Agents:", CallerClient: "Clients:"}[kind]
	steps := []string{}
	for _, step := range auth.Steps {
		generic := !strings.HasPrefix(step, "Users:") && !strings.HasPrefix(step, "Agents:") && !strings.HasPrefix(step, "Clients:")
		if generic || (prefix != "" && strings.HasPrefix(step, prefix)) {
			steps = append(steps, step)
		}
	}
	auth.Steps = steps
	auth.CallerKinds = []string{kind}
	return auth
}

func errorsIsNotFound(err error) bool { return err == resources.ErrNotFound || isNotFound(err) }

func (h *OverviewHandler) connectAuth(base, id string, ap AccessPoint) OverviewConnectAuth {
	auth := OverviewConnectAuth{Scheme: "bearer", Header: "Authorization: Bearer $" + overviewTokenEnv, TokenEnv: overviewTokenEnv, CallerKinds: ap.CallerKinds, AuthorizationServers: h.Connected.AccessPoint.AuthorizationServers, Steps: []string{}}
	if len(ap.Tools) > 0 {
		auth.ResourceMetadataURL = h.Connected.protectedResourceMetadataURL(id, "")
	}
	for _, kind := range ap.CallerKinds {
		switch kind {
		case CallerUser:
			auth.Steps = append(auth.Steps, "Users: sign in through a workforce identity provider configured for this tenant (list them with `aiplex identity user-providers list`; check a token with `aiplex identity user-providers verify --token-file <path>`), then export the access token as $"+overviewTokenEnv+".")
		case CallerAgent:
			auth.Steps = append(auth.Steps, "Agents: authenticate with a bound workload identity (see `aiplex agents identity presets`), obtain the identity provider's token for the AIPlex audience, and export it as $"+overviewTokenEnv+".")
		case CallerClient:
			auth.Steps = append(auth.Steps, "Clients: use the token of the signed-in user, issued to a registered client (`aiplex clients`); export it as $"+overviewTokenEnv+" or use the client's own sign-in.")
		}
	}
	auth.Steps = append(auth.Steps, "Send it on every request as `Authorization: Bearer $"+overviewTokenEnv+"`. Tokens are short-lived; refresh before expiry.")
	return auth
}

// overviewEndpoints lists the Bedrock, WebSocket, embeddings, moderation,
// image and audio endpoints of the wires an
// access point serves, in the fixed wire order.
func overviewEndpoints(base, apBase string, wires map[string]string, models []OverviewConnectModel) []OverviewEndpoint {
	out := []OverviewEndpoint{}
	wsBase := apBase
	switch {
	case strings.HasPrefix(base, "https://"):
		wsBase = "wss://" + strings.TrimPrefix(apBase, "https://")
	case strings.HasPrefix(base, "http://"):
		wsBase = "ws://" + strings.TrimPrefix(apBase, "http://")
	}
	reached := func(wire string) []string {
		names := []string{}
		for _, m := range models {
			for _, p := range m.Protocols {
				if p == wire {
					names = append(names, m.Name)
					break
				}
			}
		}
		return names
	}
	add := func(wire, transport, url, label string, query []string) {
		out = append(out, OverviewEndpoint{Protocol: wire, Transport: transport, URL: url, Label: label, Query: query, Models: reached(wire)})
	}
	for _, wire := range accessPointWires {
		if _, ok := wires[wire.ID]; !ok {
			continue
		}
		switch wire.ID {
		case WireBedrockConverse:
			add(wire.ID, "http", apBase+"/model/{model}/converse", "Converse", nil)
			add(wire.ID, "http-stream", apBase+"/model/{model}/converse-stream", "ConverseStream", nil)
		case WireBedrockInvoke:
			add(wire.ID, "http", apBase+"/model/{model}/invoke", "InvokeModel", nil)
			add(wire.ID, "http-stream", apBase+"/model/{model}/invoke-with-response-stream", "InvokeModelWithResponseStream", nil)
		case WireOpenAIEmbeddings:
			add(wire.ID, "http", apBase+"/v1/embeddings", "Embeddings", nil)
		case WireOpenAIModerations:
			add(wire.ID, "http", apBase+"/v1/moderations", "Moderations", nil)
		case WireOpenAIImages:
			add(wire.ID, "http", apBase+"/v1/images/generations", "Image generation", nil)
			add(wire.ID, "http", apBase+"/v1/images/edits", "Image edits", nil)
		case WireOpenAIAudio:
			add(wire.ID, "http", apBase+"/v1/audio/transcriptions", "Audio transcription", nil)
			add(wire.ID, "http", apBase+"/v1/audio/translations", "Audio translation", nil)
			add(wire.ID, "http", apBase+"/v1/audio/speech", "Speech synthesis", nil)
		case WireOpenAIRealtime:
			add(wire.ID, "websocket", wsBase+"/v1/realtime", "OpenAI Realtime", []string{"model"})
		case WireGeminiLive:
			add(wire.ID, "websocket", wsBase+geminiLivePath, "Gemini Live", []string{"model"})
		}
	}
	return out
}

func overviewGenericSnippets(c OverviewConnect, wires map[string]string) []OverviewSnippet {
	out := []OverviewSnippet{}
	var env []string
	if u, ok := c.BaseURLs["openai"]; ok {
		env = append(env, "# OpenAI-compatible clients (chat completions / responses)", `export OPENAI_BASE_URL="`+u+`"`, `export OPENAI_API_KEY="$`+overviewTokenEnv+`"  # an AIPlex token, not an OpenAI key`)
	}
	if u, ok := c.BaseURLs["anthropic"]; ok {
		env = append(env, "# Anthropic Messages clients", `export ANTHROPIC_BASE_URL="`+u+`"`, `export ANTHROPIC_AUTH_TOKEN="$`+overviewTokenEnv+`"`, `# or, for SDKs that take an API key: export ANTHROPIC_API_KEY="$`+overviewTokenEnv+`"`)
	}
	if u, ok := c.BaseURLs["genai"]; ok {
		env = append(env, "# Google GenAI clients", `export GOOGLE_GEMINI_BASE_URL="`+u+`"`, `export GEMINI_API_KEY="$`+overviewTokenEnv+`"  # an AIPlex token, sent as x-goog-api-key`)
	}
	if len(env) > 0 {
		out = append(out, OverviewSnippet{Title: "Environment variables", Language: "bash", Content: strings.Join(append([]string{`# Set AIPLEX_TOKEN to your AIPlex access token first.`}, env...), "\n") + "\n"})
	}
	apiBase := func(family string) string { return c.BaseURLs[family] }
	if m, ok := wires[WireOpenAIChat]; ok {
		out = append(out, OverviewSnippet{Title: "curl: OpenAI chat completions", Language: "bash", Content: fmt.Sprintf("curl -sS %s/chat/completions \\\n  -H \"Authorization: Bearer $%s\" \\\n  -H \"Content-Type: application/json\" \\\n  -d '{\"model\":%q,\"messages\":[{\"role\":\"user\",\"content\":\"Say hello.\"}]}'\n", apiBase("openai"), overviewTokenEnv, m)})
	}
	if m, ok := wires[WireOpenAIResponses]; ok {
		out = append(out, OverviewSnippet{Title: "curl: OpenAI responses", Language: "bash", Content: fmt.Sprintf("curl -sS %s/responses \\\n  -H \"Authorization: Bearer $%s\" \\\n  -H \"Content-Type: application/json\" \\\n  -d '{\"model\":%q,\"input\":\"Say hello.\"}'\n", apiBase("openai"), overviewTokenEnv, m)})
	}
	if m, ok := wires[WireAnthropic]; ok {
		out = append(out, OverviewSnippet{Title: "curl: Anthropic messages", Language: "bash", Content: fmt.Sprintf("curl -sS %s/v1/messages \\\n  -H \"Authorization: Bearer $%s\" \\\n  -H \"anthropic-version: 2023-06-01\" \\\n  -H \"Content-Type: application/json\" \\\n  -d '{\"model\":%q,\"max_tokens\":256,\"messages\":[{\"role\":\"user\",\"content\":\"Say hello.\"}]}'\n", apiBase("anthropic"), overviewTokenEnv, m)})
	}
	if m, ok := wires[WireGenAI]; ok {
		out = append(out, OverviewSnippet{Title: "curl: Google GenAI generateContent", Language: "bash", Content: fmt.Sprintf("curl -sS \"%s/v1beta/models/%s:generateContent\" \\\n  -H \"Authorization: Bearer $%s\" \\\n  -H \"Content-Type: application/json\" \\\n  -d '{\"contents\":[{\"role\":\"user\",\"parts\":[{\"text\":\"Say hello.\"}]}]}'\n", apiBase("genai"), m, overviewTokenEnv)})
	}
	if m, ok := wires[WireBedrockConverse]; ok {
		for _, e := range c.Endpoints {
			if e.Protocol == WireBedrockConverse && e.Transport == "http" {
				out = append(out, OverviewSnippet{Title: "curl: Amazon Bedrock Converse", Language: "bash", Content: fmt.Sprintf("curl -sS \"%s\" \\\n  -H \"Authorization: Bearer $%s\" \\\n  -H \"Content-Type: application/json\" \\\n  -d '{\"messages\":[{\"role\":\"user\",\"content\":[{\"text\":\"Say hello.\"}]}]}'\n", strings.Replace(e.URL, "{model}", url.PathEscape(m), 1), overviewTokenEnv)})
			}
		}
	}
	return append(out, overviewSharedSnippets(c)...)
}

// overviewSharedSnippets are the tool and skill snippets, identical for every
// client.
func overviewSharedSnippets(c OverviewConnect) []OverviewSnippet {
	out := []OverviewSnippet{}
	if c.MCPURL != "" {
		out = append(out,
			OverviewSnippet{Title: "MCP client configuration (streamable HTTP)", Language: "json", Content: fmt.Sprintf("{\n  \"mcpServers\": {\n    \"aiplex-%s\": {\n      \"type\": \"http\",\n      \"url\": %q,\n      \"headers\": { \"Authorization\": \"Bearer ${%s}\" }\n    }\n  }\n}\n", c.AccessPointID, c.MCPURL, overviewTokenEnv)},
			OverviewSnippet{Title: "curl: list the MCP server catalog", Language: "bash", Content: fmt.Sprintf("curl -sS %s \\\n  -H \"Authorization: Bearer $%s\"\n", c.MCPCatalogURL, overviewTokenEnv)})
	}
	if c.SkillsURL != "" {
		out = append(out, OverviewSnippet{Title: "curl: list skills", Language: "bash", Content: fmt.Sprintf("curl -sS %s \\\n  -H \"Authorization: Bearer $%s\"\n", c.SkillsURL, overviewTokenEnv)})
	}
	return out
}

// profileSnippets renders a known client profile with ClientPlex's renderer.
// A non-zero status means the profile cannot use this access point.
func (h *OverviewHandler) profileSnippets(ctx context.Context, tenant string, profile clientplex.Profile, ap AccessPoint, apBase string, wires, surfaceProtocols map[string]string) ([]OverviewSnippet, []string, int, string) {
	protocol, model := "", ""
	for _, p := range profile.Protocols {
		if m, ok := surfaceProtocols[p]; ok {
			protocol, model = p, m
			break
		}
	}
	if protocol == "" {
		offered := overviewSortedKeys(surfaceProtocols)
		return nil, nil, http.StatusUnprocessableEntity, fmt.Sprintf("client %s speaks %s, but this access point offers %s", profile.ID, strings.Join(profile.Protocols, ", "), orNone(offered))
	}
	endpoint := apBase
	if protocol == clientplex.OpenAIChat || protocol == clientplex.Responses {
		endpoint += "/v1"
	}
	var allowed []string
	for _, m := range ap.Models {
		if conn, err := h.Connected.accessPointSurface(ctx, tenant, m.SurfaceRef); err == nil && conn.Surface.Protocol == protocol {
			allowed = append(allowed, m.Name)
		}
	}
	sort.Strings(allowed)
	envName := overviewTokenEnv
	registration := clientplex.Registration{
		ID: "access-point-connect", ProfileID: profile.ID, Mode: "gateway", Protocol: protocol,
		DestinationRef: "access-point", PrincipalBinding: "access-point:" + ap.ID,
		ModelID: model, AllowedModels: allowed, AuthMode: "gateway-key", CredentialEnv: envName, CredentialRef: "aiplex-access-token",
	}
	resolver := clientplex.ResolverFunc(func(context.Context, string, string) (clientplex.Destination, error) {
		return clientplex.Destination{ID: "access-point", Provider: "aiplex", Endpoint: endpoint, Protocols: []string{protocol}, Availability: "configured"}, nil
	})
	plan, err := clientplex.BuildPlan(ctx, tenant, registration, resolver)
	if err != nil {
		return []OverviewSnippet{}, []string{fmt.Sprintf("ClientPlex could not render a %s configuration for this access point (%v); use the generic snippets with base URL %s.", profile.Name, err, endpoint)}, 0, ""
	}
	var snippets []OverviewSnippet
	for _, a := range plan.Artifacts {
		snippets = append(snippets, OverviewSnippet{Title: fmt.Sprintf("%s: %s (%s)", profile.Name, a.Target, a.MergeStrategy), Language: a.Format, Content: a.Content})
	}
	for _, c := range plan.Commands {
		snippets = append(snippets, OverviewSnippet{Title: profile.Name + ": command", Language: "bash", Content: strings.Join(c, " ") + "\n"})
	}
	notes := []string{fmt.Sprintf("Rendered by ClientPlex for profile %s using protocol %s against %s.", profile.ID, protocol, endpoint)}
	for _, b := range plan.CredentialBindings {
		notes = append(notes, fmt.Sprintf("Provide the AIPlex access token to the client as environment variable %s (credential reference %q is a placeholder).", b.Environment, b.Reference))
	}
	notes = append(notes, plan.Warnings...)
	return snippets, notes, 0, ""
}

func orNone(list []string) string {
	if len(list) == 0 {
		return "no model protocol"
	}
	return strings.Join(list, ", ")
}
