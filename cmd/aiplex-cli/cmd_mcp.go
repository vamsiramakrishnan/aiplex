package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/vamsiramakrishnan/aiplex/pkg/resources"
	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

// mcpCmd is the guided path for MCP tool connections: add one with flags,
// list, inspect, test it through the gateway and delete it.
func mcpCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Connect MCP tool servers and test them through the gateway",
		Long: `An MCP tool connection pins one MCP server URL, the credential the gateway
uses to call it (a secret reference, never a value) and which of its tools
callers may use. Expose it to callers by adding it to an access point:

  aiplex mcp connections add search --url https://mcp.example.com/mcp --auth bearer --secret-ref search-token
  aiplex mcp connections test search
  aiplex access-points create team --tool search --allow-groups eng`,
		Example: `  aiplex mcp connections list
  aiplex mcp connections add docs --url http://127.0.0.1:3000/mcp`,
	}
	cmd.AddCommand(mcpConnectionsCmd(), mcpServeCmd())
	return cmd
}

func mcpConnectionsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:         "connections",
		Aliases:     []string{"connection", "conn"},
		Short:       "Add, list, inspect, test and delete MCP tool connections",
		Annotations: map[string]string{"kind": "tool connection"},
		Example: `  aiplex mcp connections list
  aiplex mcp connections test local-tools`,
	}
	cmd.AddCommand(mcpConnListCmd(), mcpConnGetCmd(), mcpConnAddCmd(), mcpConnDeleteCmd(), mcpConnTestCmd())
	registerLister("mcp connections", func(ctx context.Context, c *aiplex.Client) ([]string, error) {
		return resourceIDs(ctx, c, aiplex.ToolConnections)
	})
	return cmd
}

func resourceIDs(ctx context.Context, c *aiplex.Client, kind aiplex.ResourceKind) ([]string, error) {
	list, err := c.ListResources(ctx, kind)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(list))
	for _, r := range list {
		ids = append(ids, r.ID)
	}
	return ids, nil
}

// toolConnView is the part of a tool-connection spec the guided commands show.
type toolConnView struct {
	ID          string `json:"id"`
	ResourceURL string `json:"resource_url"`
	Execution   struct {
		Endpoint       string   `json:"endpoint"`
		CredentialMode string   `json:"credential_mode"`
		AllowedTools   []string `json:"allowed_tools"`
		Mode           string   `json:"mode"`
	} `json:"execution"`
	Auth  *aiplex.ToolAuth   `json:"auth"`
	Tools *aiplex.ToolPolicy `json:"tools"`
}

func decodeToolConnView(r resources.Record) toolConnView {
	var v toolConnView
	_ = json.Unmarshal(r.Spec, &v)
	if v.ID == "" {
		v.ID = r.ID
	}
	return v
}

func (v toolConnView) url() string {
	if v.Execution.Endpoint != "" {
		return v.Execution.Endpoint
	}
	return v.ResourceURL
}

func (v toolConnView) authLabel() string {
	if v.Auth == nil || v.Auth.Type == "" {
		if v.Execution.CredentialMode == aiplex.ToolCredentialDelegatedOAuth {
			return "oauth_user"
		}
		return "none"
	}
	if v.Auth.SecretRef != "" {
		return v.Auth.Type + " (" + v.Auth.SecretRef + ")"
	}
	if v.Auth.ClientSecretRef != "" {
		return v.Auth.Type + " (" + v.Auth.ClientSecretRef + ")"
	}
	return v.Auth.Type
}

func mcpConnListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List MCP tool connections",
		Example: "  aiplex mcp connections list\n  aiplex mcp connections list -o json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			list, err := newClient().ListResources(cmd.Context(), aiplex.ToolConnections)
			if err != nil {
				return fmt.Errorf("list tool connections: %w", err)
			}
			if list == nil {
				list = []resources.Record{}
			}
			if spStructured(cmd, list) {
				return nil
			}
			if len(list) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No tool connections. Add one with: aiplex mcp connections add <id> --url <mcp-url>")
				return nil
			}
			t := newTable(keyCol("ID"), idCol("URL"), col("AUTH"), col("TOOLS"), wideCol("MODE"), wideCol("REVISION"))
			for _, r := range list {
				v := decodeToolConnView(r)
				t.add(r.ID, v.url(), v.authLabel(), toolCountCell(v), v.Execution.Mode, fmt.Sprint(r.Revision))
			}
			t.render(cmd.OutOrStdout())
			return nil
		},
	}
}

func toolCountCell(v toolConnView) string {
	n := len(v.Execution.AllowedTools)
	s := fmt.Sprintf("%d", n)
	if v.Tools != nil && (len(v.Tools.Deny) > 0 || len(v.Tools.Allow) > 0 || v.Tools.ReadOnly) {
		s += " (policy)"
	}
	return s
}

func mcpConnGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "get <id>",
		Aliases: []string{"show", "describe"},
		Short:   "Show one MCP tool connection",
		Example: "  aiplex mcp connections get local-tools\n  aiplex mcp connections get local-tools -o yaml",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := newClient().GetResource(cmd.Context(), aiplex.ToolConnections, args[0])
			if err != nil {
				return fmt.Errorf("get tool connection: %w", err)
			}
			if spStructured(cmd, r) {
				return nil
			}
			v := decodeToolConnView(*r)
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Tool connection %s (revision %d)\n", r.ID, r.Revision)
			fmt.Fprintf(out, "  URL:      %s\n", v.url())
			fmt.Fprintf(out, "  Auth:     %s\n", v.authLabel())
			fmt.Fprintf(out, "  Tools:    %s\n", spJoin(v.Execution.AllowedTools))
			if v.Tools != nil {
				if len(v.Tools.Allow) > 0 {
					fmt.Fprintf(out, "  Allow:    %s\n", strings.Join(v.Tools.Allow, ","))
				}
				if len(v.Tools.Deny) > 0 {
					fmt.Fprintf(out, "  Deny:     %s\n", strings.Join(v.Tools.Deny, ","))
				}
				if v.Tools.ReadOnly {
					fmt.Fprintln(out, "  Read-only tools only")
				}
			}
			fmt.Fprintf(out, "Next: aiplex mcp connections test %s\n", r.ID)
			return nil
		},
	}
}

type mcpAddFlags struct {
	file, url, auth, secretRef, headerName, authIn, prefix, username string
	tokenEndpoint, clientID, clientSecretRef, clientAuthMethod       string
	scopes, tools, allow, deny                                       []string
	readOnly, noTest, update                                         bool
	mode, protocolVersion                                            string
}

func mcpConnAddCmd() *cobra.Command {
	var f mcpAddFlags
	cmd := &cobra.Command{
		Use:     "create <id> --url <mcp-url> [--auth none|api-key|bearer|basic|oauth-client-credentials]",
		Aliases: []string{"add"},
		Short:   "Add an MCP tool connection from flags (or -f for a full spec)",
		Long: `Adds a tool connection that the gateway uses to call one MCP server.

Credentials are secret REFERENCES (--secret-ref, --client-secret-ref): names in
the tenant secret namespace, never secret values. With aiplex dev, a reference
NAME resolves from the environment variable AIPLEX_TOOL_SECRET_<NAME> of the
local API process.

--tools lists the tools callers may use. Without it, the tools are discovered
by calling tools/list on the server from this machine (only for --auth none;
pass --tools for a server that needs a credential).

After the connection is written, a tools/list is sent through the gateway to
check it works (skip with --no-test). With --update an existing connection is
replaced (the revision is read for you); without it the id must be new.`,
		Example: `  aiplex mcp connections add docs --url http://127.0.0.1:3000/mcp
  aiplex mcp connections add search --url https://mcp.example.com/mcp --auth bearer --secret-ref search-token --tools search,fetch
  aiplex mcp connections add crm --url https://crm.example.com/mcp --auth api-key --header-name X-Api-Key --secret-ref crm-key --tools lookup
  aiplex mcp connections add files --url https://files.example.com/mcp --auth oauth-client-credentials --token-endpoint https://idp.example.com/oauth2/token --client-id svc-files --client-secret-ref files-secret --scope files.read --tools list_files
  aiplex mcp connections add search -f search.yaml`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMCPAdd(cmd, args[0], f)
		},
	}
	fl := cmd.Flags()
	fl.StringVarP(&f.file, "file", "f", "", "Full tool-connection specification (JSON or YAML; '-' for stdin) instead of flags")
	fl.StringVar(&f.url, "url", "", "MCP server URL (streamable HTTP endpoint, e.g. https://host/mcp)")
	fl.StringVar(&f.auth, "auth", "none", "How the gateway authenticates to the server: none, api-key, bearer, basic or oauth-client-credentials")
	fl.StringVar(&f.secretRef, "secret-ref", "", "Secret REFERENCE name holding the API key, bearer token or password (never the value)")
	fl.StringVar(&f.headerName, "header-name", "X-Api-Key", "api-key: header (or query parameter) that carries the key")
	fl.StringVar(&f.authIn, "auth-in", "header", "api-key: header or query")
	fl.StringVar(&f.prefix, "auth-prefix", "", "api-key: scheme written before the key, e.g. Token")
	fl.StringVar(&f.username, "username", "", "basic: username")
	fl.StringVar(&f.tokenEndpoint, "token-endpoint", "", "oauth-client-credentials: token endpoint URL")
	fl.StringVar(&f.clientID, "client-id", "", "oauth-client-credentials: client ID")
	fl.StringVar(&f.clientSecretRef, "client-secret-ref", "", "oauth-client-credentials: client secret REFERENCE name")
	fl.StringVar(&f.clientAuthMethod, "client-auth-method", "client_secret_basic", "oauth-client-credentials: client_secret_basic or client_secret_post")
	fl.StringSliceVar(&f.scopes, "scope", nil, "oauth-client-credentials: scope to request (repeatable)")
	fl.StringSliceVar(&f.tools, "tools", nil, "Tools callers may use (comma separated); default: discovered from the server")
	fl.StringSliceVar(&f.allow, "allow", nil, "Tools policy: only these tools or prefixes ending in '*'")
	fl.StringSliceVar(&f.deny, "deny", nil, "Tools policy: never these tools or prefixes ending in '*'")
	fl.BoolVar(&f.readOnly, "read-only", false, "Tools policy: only tools annotated readOnlyHint=true")
	fl.StringVar(&f.mode, "mode", "streamable-http", "Server transport: streamable-http or json")
	fl.StringVar(&f.protocolVersion, "protocol-version", "2025-06-18", "MCP protocol version to send")
	fl.BoolVar(&f.noTest, "no-test", false, "Do not run tools/list through the gateway after writing")
	fl.BoolVar(&f.update, "update", false, "Replace the connection if it exists")
	return cmd
}

func normalizeAuthType(v string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(v)), "-", "_")
}

func runMCPAdd(cmd *cobra.Command, id string, f mcpAddFlags) error {
	out := cmd.OutOrStdout()
	var spec json.RawMessage
	if f.file != "" {
		for _, name := range []string{"url", "auth", "secret-ref", "tools", "token-endpoint", "client-id", "client-secret-ref"} {
			if cmd.Flags().Changed(name) {
				return usageError("--" + name + " cannot be combined with -f; put it in the file")
			}
		}
		raw, err := spReadSpec(f.file, cmd.InOrStdin())
		if err != nil {
			return err
		}
		spec = raw
	} else {
		built, err := buildToolConnectionSpec(cmd.Context(), id, f, out)
		if err != nil {
			return err
		}
		spec = built
	}
	c := newClient()
	var expected uint64
	existing, err := c.GetResource(cmd.Context(), aiplex.ToolConnections, id)
	switch {
	case err == nil:
		if !f.update {
			return newCLIError(exitConflict, fmt.Sprintf("tool connection %q already exists (revision %d)", id, existing.Revision),
				"Replace it with: aiplex mcp connections add "+id+" ... --update",
				"Or change only its credential or tools policy with: aiplex tools set-auth "+id)
		}
		expected = existing.Revision
	case spIsNotFound(err):
	default:
		return fmt.Errorf("read tool connection: %w", err)
	}
	saved, err := c.PutResource(cmd.Context(), aiplex.ToolConnections, id, spec, expected)
	if err != nil {
		return fmt.Errorf("write tool connection %s: %w", id, err)
	}
	if spStructured(cmd, saved) {
		return nil
	}
	verb := "Added"
	if expected > 0 {
		verb = "Updated"
	}
	fmt.Fprintf(out, "%s tool connection %s (revision %d).\n", verb, id, saved.Revision)
	if !f.noTest {
		fmt.Fprintln(out)
		if err := printConnectionTools(cmd.Context(), out, c, id); err != nil {
			fmt.Fprintln(out, "  The connection was saved, but the check through the gateway failed.")
			return err
		}
	}
	fmt.Fprintf(out, "\nNext: expose it to callers on an access point:\n  aiplex access-points create <id> --tool %s --allow-users <email>\n", id)
	return nil
}

func buildToolConnectionSpec(ctx context.Context, id string, f mcpAddFlags, out io.Writer) (json.RawMessage, error) {
	if f.url == "" {
		return nil, usageError("--url is required (the MCP server's streamable HTTP endpoint)", "Example: aiplex mcp connections add "+id+" --url https://mcp.example.com/mcp")
	}
	u, err := url.Parse(f.url)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, usageError(fmt.Sprintf("--url %q is not an http(s) URL", f.url))
	}
	mode := map[string]string{"streamable-http": "stateless-streamable-http", "json": "stateless-json"}[f.mode]
	if mode == "" {
		return nil, usageError(fmt.Sprintf("--mode %q is not a transport", f.mode), "Use streamable-http (default) or json")
	}
	auth := map[string]any{}
	switch normalizeAuthType(f.auth) {
	case "none", "":
		auth["type"] = aiplex.ToolAuthNone
	case "api_key", "apikey":
		if f.secretRef == "" {
			return nil, usageError("--auth api-key needs --secret-ref NAME (the secret reference holding the key)")
		}
		auth = map[string]any{"type": aiplex.ToolAuthAPIKey, "in": f.authIn, "name": f.headerName, "secret_ref": f.secretRef}
		if f.prefix != "" {
			auth["prefix"] = f.prefix
		}
	case "bearer":
		if f.secretRef == "" {
			return nil, usageError("--auth bearer needs --secret-ref NAME (the secret reference holding the token)")
		}
		auth = map[string]any{"type": aiplex.ToolAuthBearer, "secret_ref": f.secretRef}
	case "basic":
		if f.secretRef == "" || f.username == "" {
			return nil, usageError("--auth basic needs --username and --secret-ref NAME (the secret reference holding the password)")
		}
		auth = map[string]any{"type": aiplex.ToolAuthBasic, "username": f.username, "secret_ref": f.secretRef}
	case "oauth_client_credentials", "client_credentials":
		if f.tokenEndpoint == "" || f.clientID == "" || f.clientSecretRef == "" {
			return nil, usageError("--auth oauth-client-credentials needs --token-endpoint, --client-id and --client-secret-ref")
		}
		auth = map[string]any{"type": aiplex.ToolAuthOAuthClientCredentials, "token_endpoint": f.tokenEndpoint, "client_id": f.clientID, "client_secret_ref": f.clientSecretRef, "client_auth_method": f.clientAuthMethod}
		if len(f.scopes) > 0 {
			auth["scopes"] = f.scopes
		}
	default:
		return nil, usageError(fmt.Sprintf("--auth %q is not supported here", f.auth),
			"Use none, api-key, bearer, basic or oauth-client-credentials",
			"For other types (oauth_user, token_exchange, google_id_token) write the spec and use -f; see: aiplex tools auth-types")
	}
	for _, ref := range []string{f.secretRef, f.clientSecretRef} {
		if ref != "" && !aiplex.ValidToolSecretRef(ref) {
			return nil, usageError("--secret-ref and --client-secret-ref take a secret reference NAME (letters, digits, '-' or '_'), never a secret value")
		}
	}
	tools := nonEmpty(f.tools)
	if len(tools) == 0 {
		if auth["type"] != aiplex.ToolAuthNone {
			return nil, usageError("--tools is required for a server that needs a credential (the CLI does not hold the secret, so it cannot list the tools itself)",
				"Example: --tools search,fetch")
		}
		dctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		discovered, err := discoverMCPTools(dctx, f.url, f.protocolVersion)
		cancel()
		if err != nil {
			return nil, newCLIError(exitError, "could not list the tools of "+f.url+": "+err.Error(),
				"Check the URL answers MCP (POST initialize, then tools/list), or pass --tools explicitly.")
		}
		if len(discovered) == 0 {
			return nil, newCLIError(exitError, f.url+" lists no tools", "Pass --tools explicitly if the server registers tools later.")
		}
		tools = discovered
		fmt.Fprintf(out, "Discovered %d tools at %s: %s\n", len(tools), f.url, strings.Join(tools, ", "))
	}
	spec := map[string]any{
		"id": id, "resource_url": f.url,
		"execution": map[string]any{"id": id, "endpoint": f.url, "credential_mode": aiplex.ToolCredentialConnectionAuth, "allowed_tools": tools, "protocol_version": f.protocolVersion, "mode": mode},
		"auth":      auth,
	}
	if len(f.allow) > 0 || len(f.deny) > 0 || f.readOnly {
		policy := map[string]any{}
		if a := nonEmpty(f.allow); len(a) > 0 {
			policy["allow"] = a
		}
		if d := nonEmpty(f.deny); len(d) > 0 {
			policy["deny"] = d
		}
		if f.readOnly {
			policy["read_only"] = true
		}
		spec["tools"] = policy
	}
	raw, err := json.Marshal(spec)
	return raw, err
}

// discoverMCPTools calls initialize and tools/list on an MCP server directly
// (no credential), accepting JSON or SSE responses.
func discoverMCPTools(ctx context.Context, endpoint, version string) ([]string, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	session := ""
	call := func(id int, method string, params any) (json.RawMessage, error) {
		msg := map[string]any{"jsonrpc": "2.0", "method": method}
		if id > 0 {
			msg["id"] = id
		}
		if params != nil {
			msg["params"] = params
		}
		body, _ := json.Marshal(msg)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("MCP-Protocol-Version", version)
		if session != "" {
			req.Header.Set("Mcp-Session-Id", session)
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if s := resp.Header.Get("Mcp-Session-Id"); s != "" {
			session = s
		}
		if id == 0 {
			return nil, nil
		}
		if resp.StatusCode == 401 || resp.StatusCode == 403 {
			return nil, fmt.Errorf("the server requires a credential (HTTP %d); pass --tools and --auth", resp.StatusCode)
		}
		if resp.StatusCode/100 != 2 {
			return nil, fmt.Errorf("%s answered HTTP %d", method, resp.StatusCode)
		}
		raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		if err != nil {
			return nil, err
		}
		if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
			raw = lastSSEData(raw)
		}
		var envelope struct {
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			return nil, fmt.Errorf("%s: response is not JSON-RPC", method)
		}
		if envelope.Error != nil {
			return nil, fmt.Errorf("%s: %s", method, envelope.Error.Message)
		}
		return envelope.Result, nil
	}
	if _, err := call(1, "initialize", map[string]any{"protocolVersion": version, "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "aiplex-cli", "version": version}}); err != nil {
		return nil, err
	}
	_, _ = call(0, "notifications/initialized", nil)
	var names []string
	cursor := ""
	for page := 0; page < 20; page++ {
		var params any
		if cursor != "" {
			params = map[string]any{"cursor": cursor}
		}
		result, err := call(page+2, "tools/list", params)
		if err != nil {
			return nil, err
		}
		var listed struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
			NextCursor string `json:"nextCursor"`
		}
		if err := json.Unmarshal(result, &listed); err != nil {
			return nil, fmt.Errorf("tools/list: unexpected result")
		}
		for _, t := range listed.Tools {
			names = append(names, t.Name)
		}
		if listed.NextCursor == "" {
			break
		}
		cursor = listed.NextCursor
	}
	return names, nil
}

func lastSSEData(raw []byte) []byte {
	var last []byte
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "data:") {
			last = []byte(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	return last
}

func mcpConnTestCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "test <id>",
		Short: "Send a real tools/list through the gateway and print the tools",
		Long: `Calls tools/list on the connection's server through the gateway, with the
connection's credential and tools policy applied, and prints the tools callers
can see. A failure here is what a client would hit.`,
		Example: "  aiplex mcp connections test local-tools\n  aiplex mcp connections test local-tools -o json",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			if machineOutput() {
				tools, err := c.ListConnectionTools(cmd.Context(), args[0])
				if err != nil {
					return explainToolTest(args[0], err)
				}
				spStructured(cmd, tools)
				return nil
			}
			return printConnectionTools(cmd.Context(), cmd.OutOrStdout(), c, args[0])
		},
	}
}

func printConnectionTools(ctx context.Context, out io.Writer, c *aiplex.Client, id string) error {
	start := time.Now()
	tools, err := c.ListConnectionTools(ctx, id)
	if err != nil {
		return explainToolTest(id, err)
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
	fmt.Fprintf(out, "tools/list through the gateway: %d tools in %s\n", len(tools), humanDuration(time.Since(start).Milliseconds()))
	if len(tools) == 0 {
		fmt.Fprintln(out, "  The server answered, but the connection's tools policy hides every tool.")
		return nil
	}
	t := newTable(keyCol("TOOL"), col("DESCRIPTION"), wideCol("READ-ONLY"))
	for _, tool := range tools {
		ro := "unknown"
		if tool.Annotations != nil && tool.Annotations.ReadOnlyHint != nil {
			ro = fmt.Sprint(*tool.Annotations.ReadOnlyHint)
		}
		desc := tool.Description
		if desc == "" {
			desc = tool.Title
		}
		t.add(tool.Name, spDash(desc), ro)
	}
	t.render(out)
	return nil
}

func explainToolTest(id string, err error) error {
	var rpc *aiplex.ToolListError
	if errors.As(err, &rpc) {
		return newCLIError(exitError, "the tool server refused tools/list through the gateway: "+rpc.Message,
			"Check the connection's credential with: aiplex mcp connections get "+id,
			"Change it with: aiplex tools set-auth "+id+" --auth-type ...")
	}
	var apiErr *aiplex.Error
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusBadGateway {
		hints := []string{"The gateway could not get a valid answer from the server at the connection's URL."}
		if isLoopbackURL(resolveAPIURL()) {
			hints = append(hints, "With aiplex dev, the local API only calls loopback URLs and origins given with: aiplex dev --allow-origin https://host")
		}
		return newCLIError(exitError, "tools/list through the gateway failed: "+apiErr.Message, hints...)
	}
	return fmt.Errorf("test tool connection %s: %w", id, err)
}

func mcpConnDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "delete <id>",
		Aliases: []string{"remove", "rm"},
		Short:   "Delete an MCP tool connection",
		Example: "  aiplex mcp connections delete docs --yes",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			current, err := c.GetResource(cmd.Context(), aiplex.ToolConnections, args[0])
			if err != nil {
				return fmt.Errorf("read tool connection: %w", err)
			}
			if err := confirm(cmd, yes, fmt.Sprintf("Delete tool connection %s? Access points that use it stop offering its tools.", args[0])); err != nil {
				return err
			}
			if err := c.DeleteResource(cmd.Context(), aiplex.ToolConnections, args[0], current.Revision); err != nil {
				return fmt.Errorf("delete tool connection: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted tool connection %s.\n", args[0])
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Do not ask for confirmation (required when there is no terminal)")
	return cmd
}

// confirm guards a destructive change. On a terminal (stdin, and stderr where
// the question is written) it asks; --yes skips the question. With no
// terminal there is nobody to ask, so the command refuses (exit 2) unless
// --yes was given: a script that deletes something must say so.
func confirm(cmd *cobra.Command, yes bool, question string) error {
	return confirmWith(cmd, yes, question, "this is destructive", "cancelled; nothing was deleted")
}

// confirmWith is confirm with its own wording: what is risky about the
// action (for the no-terminal refusal) and what to say when declined.
func confirmWith(cmd *cobra.Command, yes bool, question, risk, cancelled string) error {
	if yes {
		return nil
	}
	if !stdinIsTerminal() || !stderrIsTerminal() {
		return usageError("refusing to proceed: "+risk+" and there is no terminal to ask for confirmation. "+question,
			"Add --yes (-y) to run it non-interactively")
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "%s [y/N] ", question)
	answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		return nil
	}
	return newCLIError(exitError, cancelled)
}

// confirmDestructive gives every delete/undeploy command without its own
// confirmation a --yes flag and a prompt on a terminal.
func confirmDestructive(c *cobra.Command) {
	for _, sub := range c.Commands() {
		confirmDestructive(sub)
	}
	if (c.Name() != "delete" && c.Name() != "undeploy" && c.Name() != "delete-context") || c.RunE == nil || c.Flags().Lookup("yes") != nil {
		return
	}
	var yes bool
	c.Flags().BoolVarP(&yes, "yes", "y", false, "Do not ask for confirmation (required when there is no terminal)")
	run := c.RunE
	c.RunE = func(cmd *cobra.Command, args []string) error {
		what := strings.TrimPrefix(strings.TrimPrefix(cmd.Parent().CommandPath(), "aiplex"), " ")
		if err := confirm(cmd, yes, strings.Join(strings.Fields(cmd.Name()+" "+what+" "+strings.Join(args, " ")), " ")+"?"); err != nil {
			return err
		}
		return run(cmd, args)
	}
}
