package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

// toolsCmd groups MCP tool connection configuration: which auth types exist
// and how a connection's upstream credential and tool policy are set.
// Secrets are only ever named by reference; no flag accepts a secret value.
func toolsCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "tools", Short: "Configure MCP tool connection auth and tool policy"}
	cmd.AddCommand(toolsAuthTypesCmd(), toolsSetAuthCmd())
	return cmd
}

func toolsAuthTypesCmd() *cobra.Command {
	return &cobra.Command{Use: "auth-types", Args: cobra.NoArgs, Short: "List tool connection auth types, their fields and the tools policy", RunE: func(cmd *cobra.Command, _ []string) error {
		catalog, err := newClient().ListToolAuthTypes(cmd.Context())
		if err != nil {
			return err
		}
		switch output {
		case "json":
			printJSON(catalog)
		case "yaml":
			printYAML(catalog)
		default:
			printToolAuthTypes(catalog)
		}
		return nil
	}}
}

func printToolAuthTypes(catalog *aiplex.ToolAuthCatalog) {
	rows := make([][]string, 0, len(catalog.Types))
	for _, t := range catalog.Types {
		fields := make([]string, 0, len(t.Fields))
		for _, f := range t.Fields {
			label := f.Name
			if f.Required {
				label += "*"
			}
			if f.SecretReference {
				label += " (ref)"
			}
			fields = append(fields, label)
		}
		name := t.Type
		if t.Type == catalog.DefaultType {
			name += " (default)"
		}
		if len(fields) == 0 {
			fields = append(fields, "-")
		}
		rows = append(rows, []string{name, t.Title, t.ExecutionCredentialMode, strings.Join(fields, ", ")})
	}
	printTable([]string{"TYPE", "TITLE", "CREDENTIAL MODE", "FIELDS"}, rows)
	fmt.Println()
	fmt.Println("* required; (ref) takes a secret reference name, never a secret value.")
	if catalog.SecretReferences.Namespace != "" {
		fmt.Println("Secret namespace:", catalog.SecretReferences.Namespace)
	}
	fmt.Println(catalog.SecretReferences.Note)
	fmt.Println()
	fmt.Println("Tools policy (--allow, --deny, --read-only):")
	for _, f := range catalog.ToolsPolicy {
		fmt.Printf("  %-10s %s\n", f.Name, f.Description)
	}
	fmt.Println()
	fmt.Println(catalog.DefaultBehaviour)
}

type toolsSetAuthFlags struct {
	authType, in, name, prefix, secretRef, username                 string
	tokenEndpoint, clientID, clientSecretRef, clientAuthMethod      string
	resource, audience, subjectTokenType, requestedTokenType, ident string
	scopes, allow, deny                                             []string
	readOnly, clearAuth, clearTools                                 bool
}

func toolsSetAuthCmd() *cobra.Command {
	var f toolsSetAuthFlags
	cmd := &cobra.Command{
		Use:   "set-auth <connection-id>",
		Short: "Set a tool connection's upstream auth and tool policy",
		Long: `Read a tool connection, change its auth block and/or tools policy and write it back
with the revision that was read. A concurrent change fails with a revision conflict
instead of being overwritten. Settings you do not pass are kept.

Auth flags build the whole auth block for --auth-type (the previous block is replaced).
Secrets are given only as references with --secret-ref / --client-secret-ref: names in
the tenant secret namespace, never values. Run 'aiplex tools auth-types' for the fields
each type takes.

Tools policy flags change only the part you pass: --allow and --deny replace that list
(pass '' to empty it), --read-only[=false] sets read-only. Deny wins over allow.`,
		Example: `  aiplex tools set-auth search --auth-type api_key --auth-in header --auth-name X-Api-Key \
      --secret-ref search-key --allow 'files.*' --deny files.delete --read-only
  aiplex tools set-auth search --auth-type oauth_client_credentials \
      --token-endpoint https://idp.example/token --client-id svc --client-secret-ref svc-secret \
      --client-auth-method client_secret_basic --scope read
  aiplex tools set-auth search --deny files.delete`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			change, err := f.change(cmd)
			if err != nil {
				return err
			}
			updated, err := newClient().UpdateToolConnectionWith(cmd.Context(), args[0], func(current *aiplex.ToolConnectionRecord) (aiplex.ToolConnectionChange, error) {
				return f.mergeTools(cmd, current, change), nil
			})
			if err != nil {
				return err
			}
			switch output {
			case "json":
				printJSON(updated)
			case "yaml":
				printYAML(updated)
			default:
				printToolConnectionPolicy(updated)
			}
			return nil
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&f.authType, "auth-type", "", "Auth type (see 'aiplex tools auth-types')")
	fl.StringVar(&f.in, "auth-in", "", "api_key: header or query")
	fl.StringVar(&f.name, "auth-name", "", "api_key: header or query parameter name")
	fl.StringVar(&f.prefix, "auth-prefix", "", "api_key: scheme before the key in a header, e.g. Token")
	fl.StringVar(&f.secretRef, "secret-ref", "", "Secret REFERENCE name (api_key, bearer, basic password); never a secret value")
	fl.StringVar(&f.username, "username", "", "basic: username")
	fl.StringVar(&f.tokenEndpoint, "token-endpoint", "", "oauth_client_credentials, token_exchange: token endpoint URL")
	fl.StringVar(&f.clientID, "client-id", "", "OAuth client identifier")
	fl.StringVar(&f.clientSecretRef, "client-secret-ref", "", "Client secret REFERENCE name; never a secret value")
	fl.StringVar(&f.clientAuthMethod, "client-auth-method", "", "client_secret_basic or client_secret_post")
	fl.StringSliceVar(&f.scopes, "scope", nil, "Requested OAuth scope (repeatable)")
	fl.StringVar(&f.resource, "resource", "", "RFC 8707 resource indicator URL")
	fl.StringVar(&f.audience, "audience", "", "token_exchange, google_id_token: audience")
	fl.StringVar(&f.subjectTokenType, "subject-token-type", "", "token_exchange: subject token type URN")
	fl.StringVar(&f.requestedTokenType, "requested-token-type", "", "token_exchange: requested token type URN")
	fl.StringVar(&f.ident, "identity-ref", "", "google_id_token: operator identity profile (type gcp-adc)")
	fl.BoolVar(&f.clearAuth, "clear-auth", false, "Remove the auth block (restores the credential mode's default behaviour)")
	fl.StringSliceVar(&f.allow, "allow", nil, "Tool name or prefix ending in '*' that may be called (repeatable)")
	fl.StringSliceVar(&f.deny, "deny", nil, "Tool name or prefix ending in '*' that may never be called (repeatable)")
	fl.BoolVar(&f.readOnly, "read-only", false, "Only call tools annotated readOnlyHint=true")
	fl.BoolVar(&f.clearTools, "clear-tools", false, "Remove the tools policy")
	return cmd
}

var toolsAuthValueFlags = []string{"auth-in", "auth-name", "auth-prefix", "secret-ref", "username", "token-endpoint", "client-id", "client-secret-ref", "client-auth-method", "scope", "resource", "audience", "subject-token-type", "requested-token-type", "identity-ref"}

// change builds the auth part of the request and validates flag combinations
// that need no server state. The tools policy is merged later against the
// connection as read.
func (f *toolsSetAuthFlags) change(cmd *cobra.Command) (aiplex.ToolConnectionChange, error) {
	var change aiplex.ToolConnectionChange
	changed := cmd.Flags().Changed
	authFlag := false
	for _, name := range toolsAuthValueFlags {
		authFlag = authFlag || changed(name)
	}
	policyFlag := changed("allow") || changed("deny") || changed("read-only")
	switch {
	case f.clearAuth && (changed("auth-type") || authFlag):
		return change, fmt.Errorf("--clear-auth cannot be combined with auth flags")
	case f.clearTools && policyFlag:
		return change, fmt.Errorf("--clear-tools cannot be combined with --allow, --deny or --read-only")
	case authFlag && f.authType == "":
		return change, fmt.Errorf("--auth-type is required with auth flags")
	case !f.clearAuth && !f.clearTools && f.authType == "" && !policyFlag:
		return change, fmt.Errorf("nothing to change: pass --auth-type, --allow, --deny, --read-only, --clear-auth or --clear-tools")
	}
	for flag, ref := range map[string]string{"secret-ref": f.secretRef, "client-secret-ref": f.clientSecretRef} {
		// The rejected input is deliberately not echoed: it may be a secret value.
		if ref != "" && !aiplex.ValidToolSecretRef(ref) {
			return change, fmt.Errorf("--%s must be a secret reference name (letters, digits, '-' or '_'), not a secret value", flag)
		}
	}
	change.ClearAuth, change.ClearTools = f.clearAuth, f.clearTools
	if f.authType != "" {
		change.Auth = &aiplex.ToolAuth{
			Type: f.authType, In: f.in, Name: f.name, Prefix: f.prefix, SecretRef: f.secretRef, Username: f.username,
			TokenEndpoint: f.tokenEndpoint, ClientID: f.clientID, ClientSecretRef: f.clientSecretRef, ClientAuthMethod: f.clientAuthMethod,
			Resource: f.resource, Audience: f.audience, SubjectTokenType: f.subjectTokenType, RequestedTokenType: f.requestedTokenType,
			IdentityRef: f.ident,
		}
		if changed("scope") {
			change.Auth.Scopes = nonEmpty(f.scopes)
		}
	}
	return change, nil
}

// mergeTools overlays the policy flags that were passed on the policy
// already stored on the connection.
func (f *toolsSetAuthFlags) mergeTools(cmd *cobra.Command, current *aiplex.ToolConnectionRecord, change aiplex.ToolConnectionChange) aiplex.ToolConnectionChange {
	changed := cmd.Flags().Changed
	if !(changed("allow") || changed("deny") || changed("read-only")) {
		return change
	}
	policy := aiplex.ToolPolicy{}
	if current.Tools != nil {
		policy = *current.Tools
	}
	if changed("allow") {
		policy.Allow = nonEmpty(f.allow)
	}
	if changed("deny") {
		policy.Deny = nonEmpty(f.deny)
	}
	if changed("read-only") {
		policy.ReadOnly = f.readOnly
	}
	change.Tools = &policy
	return change
}

// nonEmpty drops blank entries so --allow ” empties a list.
func nonEmpty(values []string) []string {
	var out []string
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func printToolConnectionPolicy(c *aiplex.ToolConnectionRecord) {
	authType := "(default)"
	if c.Auth != nil {
		authType = c.Auth.Type
	}
	policy := "-"
	if c.Tools != nil {
		parts := []string{}
		if len(c.Tools.Allow) > 0 {
			parts = append(parts, "allow="+strings.Join(c.Tools.Allow, ","))
		}
		if len(c.Tools.Deny) > 0 {
			parts = append(parts, "deny="+strings.Join(c.Tools.Deny, ","))
		}
		if c.Tools.ReadOnly {
			parts = append(parts, "read-only")
		}
		if len(parts) > 0 {
			policy = strings.Join(parts, " ")
		}
	}
	printTable([]string{"CONNECTION", "REVISION", "AUTH", "TOOLS POLICY"}, [][]string{{c.ID, fmt.Sprint(c.Revision), authType, policy}})
}
