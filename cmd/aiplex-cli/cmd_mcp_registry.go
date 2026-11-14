package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/vamsiramakrishnan/aiplex/pkg/mcpregistry"
	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

const registryEnv = "AIPLEX_MCP_REGISTRY"

func mcpRegistryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "registry",
		Short: "Search the MCP Registry and import a remote server as a tool connection",
		Long: `Reads the official MCP Registry (https://registry.modelcontextprotocol.io) or
a private registry that implements its API, and imports a server's remote
endpoint as an ordinary tool connection.

The registry is fetched by this command, from this machine. The AIPlex API
never contacts a registry, so the registry host must be reachable from here,
not from the API. Set another registry with --registry or ` + registryEnv + `.

Registry content is untrusted input. It is validated strictly, redirects are
not followed, and nothing it says is approved for you: the server's origin
must still be approved by the API's network policy, secrets are references you
create, and the imported connection starts read-only.

Only remote servers using Streamable HTTP can be imported. Servers that are
local packages (npm, pypi, oci, nuget, mcpb) run on the user's machine; the
gateway cannot run them, so they are listed as not importable.`,
		Example: `  aiplex mcp registry search github
  aiplex mcp registry show ai.adside/adside
  aiplex mcp registry import ai.adside/adside --allow-tools list_campaigns --dry-run`,
	}
	cmd.PersistentFlags().String("registry", "", "Registry base URL (default "+mcpregistry.DefaultRegistry+"; or "+registryEnv+")")
	cmd.AddCommand(mcpRegistrySearchCmd(), mcpRegistryShowCmd(), mcpRegistryImportCmd())
	return cmd
}

func registryClient(cmd *cobra.Command) (*mcpregistry.Client, error) {
	base, _ := cmd.Flags().GetString("registry")
	if base == "" {
		base = os.Getenv(registryEnv)
	}
	if base == "" {
		base = mcpregistry.DefaultRegistry
	}
	c, err := mcpregistry.NewClient(base, nil)
	if err != nil {
		return nil, usageError(err.Error(), "Use an https registry URL (plain http only for a loopback host)")
	}
	return c, nil
}

func registryError(c *mcpregistry.Client, err error) error {
	if errors.Is(err, mcpregistry.ErrNotFound) {
		return newCLIError(exitNotFound, err.Error(), "Search with: aiplex mcp registry search <text>")
	}
	return newCLIError(exitError, err.Error(), "The registry is fetched by this command, from this machine: "+c.BaseURL())
}

type registrySearchRow struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description"`
	Status      string `json:"status,omitempty"`
	Importable  string `json:"importable"`
}

func mcpRegistrySearchCmd() *cobra.Command {
	var limit int
	var cursor string
	cmd := &cobra.Command{
		Use:   "search <text>",
		Short: "Search the registry by server name",
		Long: `Lists the latest version of registry servers whose name contains the text.
IMPORT says what AIPlex can do with the entry: "remote" can be imported;
local packages and legacy SSE servers cannot.`,
		Example: "  aiplex mcp registry search github --limit 10\n  aiplex mcp registry search '' --limit 5 -o json",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := registryClient(cmd)
			if err != nil {
				return err
			}
			page, skipped, err := c.Search(cmd.Context(), args[0], limit, cursor)
			if err != nil {
				return registryError(c, err)
			}
			rows := make([]registrySearchRow, 0, len(page.Entries))
			for _, e := range page.Entries {
				rows = append(rows, registrySearchRow{Name: e.Server.Name, Version: e.Server.Version, Title: mcpregistry.Safe(e.Server.Title, 100), Description: mcpregistry.Safe(e.Server.Description, 200), Status: e.Meta.Status, Importable: e.Server.Classify().Label()})
			}
			if spStructured(cmd, map[string]any{"registry": c.BaseURL(), "servers": rows, "next_cursor": page.NextCursor, "skipped_invalid": skipped}) {
				return nil
			}
			out := cmd.OutOrStdout()
			if len(rows) == 0 {
				fmt.Fprintf(out, "No servers match %q in %s.\n", mcpregistry.Safe(args[0], 60), c.BaseURL())
				return nil
			}
			t := newTable(keyCol("NAME"), col("VERSION"), col("IMPORT"), wideCol("DESCRIPTION"))
			for _, r := range rows {
				t.add(mcpregistry.Safe(r.Name, 80), mcpregistry.Safe(r.Version, 30), r.Importable, r.Description)
			}
			t.render(out)
			if skipped > 0 {
				fmt.Fprintf(out, "%d entries failed validation and are not shown.\n", skipped)
			}
			if page.NextCursor != "" {
				fmt.Fprintf(out, "More: add --cursor %s\n", mcpregistry.Safe(page.NextCursor, 200))
			}
			fmt.Fprintln(out, "Next: aiplex mcp registry show <name>")
			return nil
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 20, "Number of results (1 to 100)")
	cmd.Flags().StringVar(&cursor, "cursor", "", "Continue from a cursor printed by an earlier search")
	return cmd
}

func splitNameVersion(arg string) (string, string) {
	name, version, _ := strings.Cut(arg, "@")
	return name, version
}

func mcpRegistryShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <name>[@version]",
		Short: "Show a registry server: remotes, packages and what is importable",
		Long: `Prints a server.json: its remotes with the headers and variables they declare,
and its packages. Remotes using Streamable HTTP are importable; everything
else is marked "not importable" with the reason. The registry's text is
printed with control characters removed.`,
		Example: "  aiplex mcp registry show ai.adside/adside\n  aiplex mcp registry show ai.adside/adside@1.0.0 -o json",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := registryClient(cmd)
			if err != nil {
				return err
			}
			name, version := splitNameVersion(args[0])
			entry, err := c.Get(cmd.Context(), name, version)
			if err != nil {
				return registryError(c, err)
			}
			prov := entry.Provenance(c.BaseURL())
			if spStructured(cmd, map[string]any{"server": json.RawMessage(entry.Raw), "registry_meta": entry.Meta, "provenance": prov, "importable": entry.Server.Classify().Label()}) {
				return nil
			}
			printEntry(cmd.OutOrStdout(), entry, prov)
			s := entry.Server
			if len(entry.Server.Classify().Remotes) > 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "Next: aiplex mcp registry import %s --allow-tools <tools> --dry-run\n", s.Name)
			}
			return nil
		},
	}
}

func printEntry(out io.Writer, e mcpregistry.Entry, prov mcpregistry.Provenance) {
	s := e.Server
	status := e.Meta.Status
	if status == "" {
		status = "unknown"
	}
	fmt.Fprintf(out, "%s %s (%s)\n", mcpregistry.Safe(s.Name, 100), mcpregistry.Safe(s.Version, 40), mcpregistry.Safe(status, 20))
	if s.Title != "" {
		fmt.Fprintf(out, "  Title:       %s\n", mcpregistry.Safe(s.Title, 120))
	}
	fmt.Fprintf(out, "  Description: %s\n", mcpregistry.Safe(s.Description, 300))
	if s.WebsiteURL != "" {
		fmt.Fprintf(out, "  Website:     %s\n", mcpregistry.Safe(s.WebsiteURL, 200))
	}
	if s.Repository != nil && s.Repository.URL != "" {
		fmt.Fprintf(out, "  Repository:  %s\n", mcpregistry.Safe(s.Repository.URL, 200))
	}
	if e.Meta.StatusMessage != "" {
		fmt.Fprintf(out, "  Status note: %s\n", mcpregistry.Safe(e.Meta.StatusMessage, 300))
	}
	fmt.Fprintf(out, "  Fetched:     %s from %s\n  server.json sha256: %s (schema %s)\n", prov.RetrievedAt, prov.Registry, prov.SHA256, mcpregistry.SchemaDate(s.Schema))
	classes := s.Classify()
	for i, r := range s.Remotes {
		state := "importable"
		if r.Type != "streamable-http" {
			state = "not importable (the " + mcpregistry.Safe(r.Type, 20) + " transport is not supported; the gateway speaks stateless Streamable HTTP)"
		}
		fmt.Fprintf(out, "  Remote %d: %s %s  [%s]\n", i, mcpregistry.Safe(r.Type, 20), mcpregistry.Safe(r.URL, 200), state)
		names := make([]string, 0, len(r.Variables))
		for n := range r.Variables {
			names = append(names, n)
		}
		sortStrings(names)
		for _, n := range names {
			in := r.Variables[n]
			fmt.Fprintf(out, "      variable {%s}%s%s: %s\n", n, flagIf(in.IsRequired, " required"), flagIf(in.IsSecret, " secret"), mcpregistry.Safe(in.Description, 120))
		}
		for _, h := range r.Headers {
			fmt.Fprintf(out, "      header %s%s%s: %s\n", mcpregistry.Safe(h.Name, 64), flagIf(h.IsRequired, " required"), flagIf(h.IsSecret, " secret"), mcpregistry.Safe(h.Description, 120))
		}
	}
	for _, p := range s.Packages {
		fmt.Fprintf(out, "  Package: %s %s %s  [not importable (local package): it runs on the user's machine over %s; the gateway cannot run it]\n",
			mcpregistry.Safe(p.RegistryType, 20), mcpregistry.Safe(p.Identifier, 120), mcpregistry.Safe(p.Version, 40), mcpregistry.Safe(p.Transport.Type, 20))
	}
	if len(classes.Remotes) == 0 {
		fmt.Fprintf(out, "  Import:      %s\n", classes.Label())
	}
}

func flagIf(on bool, text string) string {
	if on {
		return text
	}
	return ""
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

type importFlags struct {
	id, auth, secretRef string
	remote              int
	vars, allowTools    []string
	probe, readWrite    bool
	dryRun, yes, update bool
}

var toolNameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)

func mcpRegistryImportCmd() *cobra.Command {
	var f importFlags
	cmd := &cobra.Command{
		Use:   "import <name>[@version] [--id <connection>] --allow-tools <tools> | --probe",
		Short: "Create a tool connection from a registry server's remote endpoint",
		Long: `Creates an ordinary tool connection through the API from a server's remote
endpoint (Streamable HTTP only; local packages cannot be imported).

What is proposed from the registry entry, and what is not:
  URL        the remote's URL, with its variables filled from --var, fixed values
             or defaults (values are checked for the part of the URL they land in)
  Auth       a proposal mapped from the declared headers: a secret Authorization
             header becomes bearer, another secret header becomes api_key. Secrets
             are REFERENCES you create; the command prints which. Fixed header
             values and headers that are not secret are not imported and are listed.
             --auth none drops the declared credential
  Provenance registry, name, version, retrieval time and the SHA-256 of the
             server.json, stored on the connection

What it does not do: it does not approve the server's origin (the API's network
policy decides which origins it calls; the command prints the origin and how to
approve it), does not create secrets, and does not expose the connection on any
access point.

The registry does not list a server's tools. Choose them with --allow-tools, or
with --probe, which sends tools/list to the server from this machine without a
credential (only for servers that need none) and shows what it found. The
connection is saved read-only: only tools annotated readOnlyHint=true can be
called, after the tools are listed through the gateway once. --read-write
removes that restriction.

Use --dry-run to see everything above without creating anything.`,
		Example: `  aiplex mcp registry import ai.adside/adside --allow-tools list_campaigns,get_report --dry-run
  aiplex mcp registry import ai.adside/adside --id adside --allow-tools list_campaigns --secret-ref adside-token --yes
  aiplex mcp registry import ai.biel/biel-ai --var project_slug=my-docs --probe`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runRegistryImport(cmd, args[0], f) },
	}
	fl := cmd.Flags()
	fl.StringVar(&f.id, "id", "", "Connection id (default: derived from the server name)")
	fl.StringVar(&f.auth, "auth", "auto", "auto (map the declared credential) or none (send no credential)")
	fl.StringVar(&f.secretRef, "secret-ref", "", "Secret REFERENCE name for the credential (never the value); default: proposed")
	fl.IntVar(&f.remote, "remote", -1, "Which remote of the entry to import (index from 'registry show'; default: the first importable)")
	fl.StringArrayVar(&f.vars, "var", nil, "URL template variable, name=value (repeatable)")
	fl.StringSliceVar(&f.allowTools, "allow-tools", nil, "Tools the connection may call (comma separated)")
	fl.BoolVar(&f.probe, "probe", false, "Discover the tools by sending tools/list to the server from this machine (no credential is sent)")
	fl.BoolVar(&f.readWrite, "read-write", false, "Do not restrict the connection to tools annotated readOnlyHint=true")
	fl.BoolVar(&f.dryRun, "dry-run", false, "Show the proposed connection, origin and secrets; create nothing")
	addYesFlag(cmd, &f.yes)
	fl.BoolVar(&f.update, "update", false, "Replace the connection if it exists")
	return cmd
}

func deriveConnectionID(name string) string {
	_, short, _ := strings.Cut(name, "/")
	id := regexp.MustCompile(`[^a-z0-9_.-]+`).ReplaceAllString(strings.ToLower(short), "-")
	id = strings.Trim(id, "-_.")
	if len(id) > 63 {
		id = strings.Trim(id[:63], "-_.")
	}
	return id
}

var connectionIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,62}$`)

func runRegistryImport(cmd *cobra.Command, arg string, f importFlags) error {
	out := cmd.OutOrStdout()
	rc, err := registryClient(cmd)
	if err != nil {
		return err
	}
	if f.auth != "auto" && f.auth != "none" {
		return usageError(fmt.Sprintf("--auth %q is not supported here", f.auth), "Use auto or none; change the credential afterwards with: aiplex tools set-auth <id> ...")
	}
	vars := map[string]string{}
	for _, kv := range f.vars {
		name, value, ok := strings.Cut(kv, "=")
		if !ok || name == "" {
			return usageError(fmt.Sprintf("--var %q: use name=value", kv))
		}
		vars[name] = value
	}
	for _, t := range f.allowTools {
		if !toolNameRe.MatchString(t) {
			return usageError(fmt.Sprintf("--allow-tools %q is not a tool name (exact names only; no wildcards)", t))
		}
	}
	if f.probe && len(f.allowTools) > 0 {
		return usageError("--probe and --allow-tools cannot be combined", "--probe discovers the tools; --allow-tools names them")
	}
	name, version := splitNameVersion(arg)
	entry, err := rc.Get(cmd.Context(), name, version)
	if err != nil {
		return registryError(rc, err)
	}
	switch entry.Meta.Status {
	case "", "active":
	case "deprecated":
		fmt.Fprintf(cmd.ErrOrStderr(), "Warning: %s %s is deprecated in the registry. %s\n", mcpregistry.Safe(name, 100), mcpregistry.Safe(entry.Server.Version, 40), mcpregistry.Safe(entry.Meta.StatusMessage, 200))
	default:
		return newCLIError(exitError, fmt.Sprintf("%s %s has registry status %q and is not imported", mcpregistry.Safe(name, 100), mcpregistry.Safe(entry.Server.Version, 40), mcpregistry.Safe(entry.Meta.Status, 20)))
	}
	id := f.id
	if id == "" {
		id = deriveConnectionID(entry.Server.Name)
	}
	if !connectionIDRe.MatchString(id) {
		return usageError(fmt.Sprintf("connection id %q is not valid", id), "Choose one with --id (letters, digits, '.', '_' and '-')")
	}
	plan, err := mcpregistry.NewPlan(entry, mcpregistry.PlanOptions{Remote: f.remote, Vars: vars, ConnectionID: id, NoAuth: f.auth == "none"})
	if err != nil {
		var missing *mcpregistry.MissingVariablesError
		if errors.As(err, &missing) {
			return usageError(err.Error(), "Describe them with: aiplex mcp registry show "+name)
		}
		return newCLIError(exitError, "not importable: "+err.Error(), "See what the entry declares with: aiplex mcp registry show "+name)
	}
	prov := entry.Provenance(rc.BaseURL())
	c := newClient()
	namespace := ""
	if catalog, cerr := c.ListToolAuthTypes(cmd.Context()); cerr == nil {
		namespace = catalog.SecretReferences.Namespace
	}

	if f.secretRef != "" {
		if !aiplex.ValidToolSecretRef(f.secretRef) {
			return usageError("--secret-ref takes a secret reference NAME (letters, digits, '-' or '_'), never a secret value")
		}
		if len(plan.Secrets) == 0 {
			return usageError("--secret-ref given, but the proposed connection needs no secret")
		}
		plan.Auth.SecretRef, plan.Secrets[0].Ref = f.secretRef, f.secretRef
	}

	var tools []string
	toolSource := ""
	switch {
	case len(f.allowTools) > 0:
		tools, toolSource = f.allowTools, "--allow-tools"
	case f.probe:
		if len(plan.Secrets) > 0 {
			return usageError("--probe cannot be used: the server declares a credential and this command holds none to send", "Name the tools with --allow-tools instead")
		}
		if err := probeAllowed(plan.URL, rc.BaseURL()); err != nil {
			return usageError(err.Error(), "Name the tools with --allow-tools instead")
		}
		if f.dryRun {
			toolSource = "--probe (not run in a dry run)"
		} else {
			fmt.Fprintf(cmd.ErrOrStderr(), "Probing %s with tools/list from this machine (no credential is sent)...\n", plan.URL)
			pctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Second)
			discovered, derr := discoverMCPToolsWith(pctx, plan.URL, "2025-06-18", true)
			cancel()
			if derr != nil {
				return newCLIError(exitError, "the probe failed: "+derr.Error(), "Name the tools with --allow-tools instead")
			}
			for _, t := range discovered {
				if !toolNameRe.MatchString(t) {
					return newCLIError(exitError, fmt.Sprintf("the server listed a tool name that is not importable (%q); name the tools with --allow-tools", mcpregistry.Safe(t, 60)))
				}
			}
			if len(discovered) == 0 {
				return newCLIError(exitError, "the server lists no tools", "Name the tools with --allow-tools instead")
			}
			tools, toolSource = discovered, "--probe"
		}
	case !f.dryRun:
		return usageError("the registry does not list a server's tools, so say which the connection may call",
			"Name them: --allow-tools search,fetch",
			"Or discover them from a server that needs no credential: --probe",
			"See the proposal first with: --dry-run")
	}

	existing, getErr := c.GetResource(cmd.Context(), aiplex.ToolConnections, id)
	var expected uint64
	switch {
	case getErr == nil:
		expected = existing.Revision
	case spIsNotFound(getErr):
	default:
		if !f.dryRun {
			return fmt.Errorf("read tool connection: %w", getErr)
		}
		fmt.Fprintln(cmd.ErrOrStderr(), "Note: the API was not reachable, so whether the connection exists is unknown.")
	}

	printRegistryPlan(out, entry, prov, plan, id, tools, toolSource, f.readWrite, namespace, getErr == nil)
	if f.dryRun {
		fmt.Fprintln(out, "\nDry run: nothing was created.")
		return nil
	}
	if getErr == nil && !f.update {
		return newCLIError(exitConflict, fmt.Sprintf("tool connection %q already exists (revision %d)", id, existing.Revision),
			"Replace it with --update, or choose another name with --id")
	}
	if len(plan.Secrets) > 0 && f.secretRef == "" {
		if !stdinIsTerminal() || !stderrIsTerminal() {
			return usageError("refusing to proceed: the connection needs a secret reference and there is no terminal to ask for its name",
				"Create the secret (see above), then pass its name: --secret-ref "+plan.Secrets[0].Ref,
				"The command never reads the secret's value")
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "Secret reference name [%s]: ", plan.Secrets[0].Ref)
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if line = strings.TrimSpace(line); line != "" {
			if !aiplex.ValidToolSecretRef(line) {
				return usageError("a secret reference NAME is letters, digits, '-' or '_'; never type the secret itself here")
			}
			plan.Auth.SecretRef, plan.Secrets[0].Ref = line, line
		}
	}
	if err := confirmWith(cmd, f.yes, fmt.Sprintf("Create tool connection %s for %s? Its origin %s is not approved by this command.", id, plan.URL, plan.Origin), "it creates a connection to a server named by registry content", "cancelled; nothing was created"); err != nil {
		return err
	}
	spec := importSpec(id, plan, tools, f.readWrite, prov)
	saved, err := c.PutResource(cmd.Context(), aiplex.ToolConnections, id, spec, expected)
	if err != nil {
		return explainImportError(id, plan, err)
	}
	if spStructured(cmd, saved) {
		return nil
	}
	verb := "Created"
	if expected > 0 {
		verb = "Updated"
	}
	fmt.Fprintf(out, "\n%s tool connection %s (revision %d) from %s %s.\n", verb, id, saved.Revision, mcpregistry.Safe(plan.Name, 100), mcpregistry.Safe(plan.Version, 40))
	fmt.Fprintf(out, "Next:\n  aiplex mcp connections test %s          # lists the tools through the gateway and records their annotations\n  aiplex access-points create <id> --tool %s ...   # expose it to callers\n", id, id)
	return nil
}

func importSpec(id string, plan *mcpregistry.Plan, tools []string, readWrite bool, prov mcpregistry.Provenance) map[string]any {
	auth := map[string]any{"type": plan.Auth.Type}
	if plan.Auth.In != "" {
		auth["in"] = plan.Auth.In
	}
	if plan.Auth.Name != "" {
		auth["name"] = plan.Auth.Name
	}
	if plan.Auth.Prefix != "" {
		auth["prefix"] = plan.Auth.Prefix
	}
	if plan.Auth.SecretRef != "" {
		auth["secret_ref"] = plan.Auth.SecretRef
	}
	spec := map[string]any{
		"id": id, "resource_url": plan.URL,
		"execution":  map[string]any{"id": id, "endpoint": plan.URL, "credential_mode": aiplex.ToolCredentialConnectionAuth, "allowed_tools": tools, "protocol_version": "2025-06-18", "mode": "stateless-streamable-http"},
		"auth":       auth,
		"provenance": prov,
	}
	if !readWrite {
		spec["tools"] = map[string]any{"read_only": true}
	}
	return spec
}

func printRegistryPlan(out io.Writer, entry mcpregistry.Entry, prov mcpregistry.Provenance, plan *mcpregistry.Plan, id string, tools []string, toolSource string, readWrite bool, namespace string, exists bool) {
	fmt.Fprintf(out, "Registry entry: %s %s", mcpregistry.Safe(plan.Name, 100), mcpregistry.Safe(plan.Version, 40))
	if entry.Meta.Status != "" {
		fmt.Fprintf(out, " (%s)", mcpregistry.Safe(entry.Meta.Status, 20))
	}
	fmt.Fprintf(out, "\n  %s\n  fetched %s from %s; server.json sha256 %s\n", mcpregistry.Safe(entry.Server.Description, 200), prov.RetrievedAt, prov.Registry, prov.SHA256)
	fmt.Fprintf(out, "\nConnection %s%s\n", id, flagIf(exists, " (already exists; --update replaces it)"))
	fmt.Fprintf(out, "  URL:       %s\n  Transport: Streamable HTTP (stateless), remote %d of the entry\n", plan.URL, plan.RemoteIdx)
	for _, v := range plan.Variables {
		if v.Source != "secret" {
			fmt.Fprintf(out, "  Variable:  {%s} = %s (%s)\n", v.Name, mcpregistry.Safe(v.Value, 80), v.Source)
		}
	}
	fmt.Fprintf(out, "  Origin:    %s\n", plan.Origin)
	fmt.Fprintf(out, "             Not approved by this command. The API calls only origins its network policy approves:\n")
	fmt.Fprintf(out, "               aiplex dev:  aiplex dev --allow-origin %s\n", plan.Origin)
	fmt.Fprintf(out, "               otherwise:   add it to network.allowed_origins in the connections file (AIPLEX_CONNECTIONS_FILE)\n")
	fmt.Fprintf(out, "             Saving fails until it is approved.\n")
	switch plan.Auth.Type {
	case "none":
		fmt.Fprintln(out, "  Auth:      none")
	case "bearer":
		fmt.Fprintf(out, "  Auth:      bearer, secret reference %s\n", plan.Auth.SecretRef)
	default:
		where := plan.Auth.In + " " + plan.Auth.Name
		if plan.Auth.Prefix != "" {
			where += ", scheme " + plan.Auth.Prefix
		}
		fmt.Fprintf(out, "  Auth:      api_key (%s), secret reference %s\n", where, plan.Auth.SecretRef)
	}
	switch {
	case len(tools) > 0:
		fmt.Fprintf(out, "  Tools:     %s (from %s)\n", strings.Join(tools, ", "), toolSource)
	case toolSource != "":
		fmt.Fprintf(out, "  Tools:     %s\n", toolSource)
	default:
		fmt.Fprintln(out, "  Tools:     not chosen; pass --allow-tools a,b or --probe (the registry does not list tools)")
	}
	if readWrite {
		fmt.Fprintln(out, "  Policy:    read-write (no read_only restriction)")
	} else {
		fmt.Fprintln(out, "  Policy:    read_only: only tools annotated readOnlyHint=true can be called; list the tools through the gateway once (aiplex mcp connections test) before calls")
	}
	if len(plan.Secrets) > 0 {
		fmt.Fprintln(out, "\nSecrets to create (this command never reads or writes their values):")
		for _, s := range plan.Secrets {
			fmt.Fprintf(out, "  %s: %s\n", s.Ref, s.Purpose)
			if s.Description != "" {
				fmt.Fprintf(out, "    registry says: %s\n", s.Description)
			}
			fmt.Fprintf(out, "    aiplex dev:  export AIPLEX_TOOL_SECRET_%s=<value> before starting aiplex dev\n", strings.ToUpper(strings.ReplaceAll(s.Ref, "-", "_")))
		}
		if namespace != "" {
			fmt.Fprintf(out, "    this API's secret namespace: %s\n", mcpregistry.Safe(namespace, 200))
		}
	}
	if len(plan.Unmapped) > 0 {
		fmt.Fprintln(out, "\nDeclared by the registry but not imported:")
		for _, u := range plan.Unmapped {
			fmt.Fprintf(out, "  - %s\n", u)
		}
	}
	for _, n := range plan.Notes {
		fmt.Fprintf(out, "\nNote: %s\n", n)
	}
}

func explainImportError(id string, plan *mcpregistry.Plan, err error) error {
	var apiErr *aiplex.Error
	if errors.As(err, &apiErr) {
		switch {
		case strings.Contains(apiErr.Message, "origin not approved"):
			return newCLIError(exitError, fmt.Sprintf("the API does not approve the origin %s, so nothing was created", plan.Origin),
				"With aiplex dev, restart it with: aiplex dev --allow-origin "+plan.Origin,
				"Otherwise add the origin to network.allowed_origins in the connections file (AIPLEX_CONNECTIONS_FILE), then run the same command again.",
				"Approving an origin is your decision; import does not do it for you.")
		case strings.Contains(apiErr.Message, "secret reference"):
			return newCLIError(exitError, apiErr.Message, "Create the secret named "+plan.Auth.SecretRef+" in this API's secret namespace, then run the same command again.")
		}
	}
	return fmt.Errorf("create tool connection %s: %w", id, err)
}

// probeAllowed refuses to send a probe to a destination that a registry could
// use to reach the operator's internal network: non-https URLs (other than
// loopback when the registry itself is loopback) and hosts that are or
// resolve to private, link-local or loopback addresses.
func probeAllowed(raw, registry string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("--probe refused: the URL is not usable")
	}
	registryLoopback := false
	if r, err := url.Parse(registry); err == nil {
		registryLoopback = mcpregistry.IsLoopbackHost(r.Hostname())
	}
	host := u.Hostname()
	if registryLoopback {
		return nil // a local registry and local servers: development.
	}
	if u.Scheme != "https" {
		return fmt.Errorf("--probe refused: it only contacts https servers")
	}
	addrs := []netip.Addr{}
	if ip, err := netip.ParseAddr(host); err == nil {
		addrs = append(addrs, ip)
	} else {
		ips, lerr := net.LookupIP(host)
		if lerr != nil {
			return fmt.Errorf("--probe refused: %s does not resolve", host)
		}
		for _, ip := range ips {
			if a, ok := netip.AddrFromSlice(ip); ok {
				addrs = append(addrs, a.Unmap())
			}
		}
	}
	for _, a := range addrs {
		if a.IsPrivate() || a.IsLoopback() || a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() || a.IsUnspecified() {
			return fmt.Errorf("--probe refused: %s is or resolves to a private, loopback or link-local address", host)
		}
	}
	return nil
}
