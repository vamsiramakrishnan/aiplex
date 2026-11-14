# Tool connection auth and tool policy

A managed MCP tool connection can declare how AIPlex authenticates to the upstream
tool server (its `auth` block), and which tools a caller may reach through it (its
`tools` block). Eight auth types cover API keys, static bearer and basic credentials,
service OAuth, RFC 8693 token exchange, Google ID tokens, and the existing per-user
OAuth grant. The `tools` block adds allow and deny lists and a read-only mode.

The delegated OAuth flow that `oauth_user` uses is described in
[MCP runtime authorization](mcp-runtime-authorization.md).

## Routes

| Route | Purpose |
| --- | --- |
| `GET /api/v1/mcp/auth-types` | Describes each auth type: its fields, which are required, which are secret references, and the tools-policy fields. Drives CLI and console forms. |
| `GET /api/v1/mcp/connections/{id}` | Read a connection, with its `auth` and `tools` blocks. |
| `PUT /api/v1/mcp/connections/{id}` | Create or replace a connection. Updates send the current revision in `If-Match`. |
| `POST /api/v1/mcp/connections/{id}/registration` | Register the connection's OAuth client (`oauth_user`). Operators only. CLI: `aiplex tool-auth register`. |
| `POST /api/v1/mcp/connections/{id}/invoke` | Invoke one `tools/call` (JSON-RPC body). Subject to the checks below. |

The `auth` and `tools` blocks are optional fields on the connection. A connection with
no `auth` block behaves as before, using its execution credential mode.

## Connect a tool server with the CLI

`aiplex mcp connections` adds, lists, inspects, tests and deletes tool connections without writing a
JSON body. `create` (alias `add`) takes the server URL and the auth type, and sends a `tools/list`
through the gateway to check the connection works (skip with `--no-test`). Credentials are references:
`--secret-ref` names a secret in the tenant namespace, never a value.

```sh
aiplex mcp connections create search --url https://mcp.example.com/mcp --auth bearer --secret-ref search-token --tools search,fetch
aiplex mcp connections create files --url https://files.example.com/mcp --auth oauth-client-credentials \
  --token-endpoint https://idp.example.com/oauth2/token --client-id svc-files --client-secret-ref files-secret \
  --scope files.read --tools list_files
aiplex mcp connections test search
aiplex access-points create team --tool search --allow-groups eng
```

`--tools` lists the tools callers may use. Without it, `create` discovers them by an MCP Streamable HTTP handshake (`initialize`, then `tools/list`, following pages) against the
server from this machine, which works only with `--auth none`. `--allow` and `--deny` set the tools policy,
and `--update` replaces an existing connection. Delete with `aiplex mcp connections delete <id>`, which asks
first on a terminal (see [the CLI page](cli.md#confirmation-prompts-and---yes)).

On `aiplex dev`, the built-in MCP server is the `local-tools` connection. Its details and a real test:

```text
$ aiplex mcp connections list
ID           URL                     AUTH  TOOLS
local-tools  http://127.0.0.1:9001  none  3

$ aiplex mcp connections test local-tools
tools/list through the gateway: 3 tools in 3ms
TOOL           DESCRIPTION
add            Add two numbers.
echo           Echo the given text back.
reset_counter  Reset the server's call counter.
```

A create that adds a connection prints its revision and the same test:

```text
Added tool connection docs (revision 1).

tools/list through the gateway: 2 tools in 1ms
TOOL  DESCRIPTION
add   Add two numbers.
echo  Echo the given text back.

Next: expose it to callers on an access point:
  aiplex access-points create <id> --tool docs --allow-users <email>
```

The URL in the first output is the built-in server's address on the machine, and it changes each time `aiplex dev`
starts. The auth types, their fields and the tools policy are described in the sections below.

### Call one tool

`aiplex mcp connections call <id> <tool>` sends one `tools/call` through the gateway, with the connection's
credential, tools policy and rules applied and the caller's `mcp:tools:{name}` grant checked, so a rule or a
credential can be tried end to end. It is the CLI form of the `invoke` request. The call is real: a tool with side
effects runs.

```sh
aiplex mcp connections call local-tools echo --args '{"text":"hi"}'
aiplex mcp connections call search query --args-file query.yaml     # JSON or YAML; '-' reads stdin
aiplex mcp connections call local-tools echo --args '{"text":"hi"}' -o json   # the raw result
```

Text content prints as text. Other content (an image, say) prints as its type and MIME type, without the payload;
`-o json` prints the whole result. Arguments must be a JSON object. Exit codes follow [the CLI page](cli.md#exit-codes):

| Outcome | Exit |
| --- | --- |
| Result printed | 0 |
| Arguments not an object, unreadable file, `--args` with `--args-file`, bad connection ID | 2, before anything is sent |
| Policy denial (`tool_policy_denied` or `tool_policy_error`) or a missing grant, shown with the rule ID | 3 |
| Unknown connection | 4 |
| The tool server answered a JSON-RPC error, the gateway could not reach it, or the tool ran and reported `isError` (its text is printed first) | 1 |

Nothing secret is printed: the connection's credential never reaches the client, and the CLI prints no token. The
arguments are not echoed in errors. A denial is shown with its rule ID and message, which an operator wrote.

### Register the OAuth client for `oauth_user`

A connection that uses per-user OAuth needs its OAuth client registered with the upstream authorization server before
callers can authorize. Registration was only possible with `POST /api/v1/mcp/connections/{id}/registration`; the CLI
now has it:

```sh
aiplex tool-auth register search      # admin or deployer; the client profile and issuer come from the connection
aiplex tool-auth start search --redirect-uri https://app.example.com/callback
```

The request has no body and the command has no secret flag. A client secret held by the registration is a
credential reference in secure credential storage, never a value on the command line or in the output; the command
prints the registration mode, client ID, issuer and the credential reference. A value the API did not return is shown as
`unknown`. `-o json` prints the stored `oauth-registration` envelope. Exit codes are as above (3 not permitted, 4 no such
connection, 5 `CONNECTION_CHANGED`). The upstream registration and the `start` and `complete` steps were not validated
against a live authorization server in this change; the end-to-end test reaches the route on the dev stack only.

## Auth types

Secret fields take a reference name in the tenant's secret namespace, never a secret
value. Names match `^[A-Za-z0-9][A-Za-z0-9_-]{0,199}$`. Each reference is checked for
existence when the connection is saved.

| Type | Upstream credential | Fields |
| --- | --- | --- |
| `none` | No credential. | None. |
| `api_key` | A secret reference sent in a named header, or in a query parameter. | `in` (required: `header` or `query`), `name` (required: header or query parameter name), `prefix` (optional, header only, for example `Token`), `secret_ref` (required) |
| `bearer` | A secret reference sent as `Authorization: Bearer`. | `secret_ref` (required) |
| `basic` | A username and a secret reference (the password), sent as `Authorization: Basic`. | `username` (required, must not contain `:`), `secret_ref` (required) |
| `oauth_user` | The existing per-user delegated OAuth grant. Issuer and client are configured on the connection. | None in the `auth` block. Requires `execution.credential_mode: delegated-oauth`. |
| `oauth_client_credentials` | A service token from a token endpoint, cached per tenant and connection until shortly before it expires. | `token_endpoint` (required), `client_id` (required), `client_secret_ref` (required), `client_auth_method` (required: `client_secret_basic` or `client_secret_post`), `scopes` (optional), `resource` (optional, RFC 8707) |
| `token_exchange` | RFC 8693 exchange of the caller's verified token for a downstream audience. Cached per caller and never shared across principals. | `token_endpoint` (required), `audience` or `resource` (one required), `scopes`, `subject_token_type` (JWT, access token or ID token URN; default JWT), `requested_token_type` (access token or JWT URN; default access token), `client_id`, `client_secret_ref` and `client_auth_method` (the last two must be given together with `client_id`) |
| `google_id_token` | A workload-issued Google ID token for a fixed audience, from an operator identity profile of type `gcp-adc`. | `audience` (required, fixed URL or identifier), `identity_ref` (required; the profile's `allowed_origins` must include the connection's resource) |

Field rules that are enforced:

- A field that does not apply to the chosen type is rejected. For example, `prefix`
  is refused for query API keys, and `in` is refused for `bearer`.
- `api_key` header names must be header tokens. Hop-by-hop, framing, routing,
  gateway-owned (`x-aiplex-*`, `x-forwarded-*`, `proxy-*`) and MCP transport headers
  are refused.
- `scopes` must be unique tokens, at most 64 entries.
- Token endpoints and resources must be absolute `http` or `https` URLs without
  credentials or fragments. They must also pass the outbound endpoint policy.
- Auth types other than `oauth_user` need managed execution (an `execution` block)
  and `execution.credential_mode: connection-auth`. Setting `oauth_user` requires
  `delegated-oauth`. A connection-auth execution with no `auth` block is rejected.

Setting an auth block through the CLI or SDK also sets `execution.credential_mode`:
`oauth_user` selects `delegated-oauth`, and every other type selects `connection-auth`
with an empty `identity_ref`.

## The `tools` block

```json
{
  "tools": {
    "allow": ["files.*", "search"],
    "deny": ["files.delete"],
    "read_only": true,
    "rules": [{"id": "no-etc", "effect": "deny", "when": "path_under(args.path, '/etc')", "message": "files under /etc are off limits"}]
  }
}
```

| Field | Meaning |
| --- | --- |
| `allow` | Exact tool names, or prefixes ending in one `*`. If empty or omitted, every tool the connection permits is allowed. Up to 1024 entries. |
| `deny` | Same pattern syntax. Deny always wins. |
| `read_only` | Only tools that the MCP server annotated `readOnlyHint: true` are callable. Unknown annotations are denied. |
| `rules` | Up to 64 policy rules (CEL expressions over the tool, its arguments, the caller and the time) evaluated after the lists, the scope and `read_only`. See [policy rules on tool calls](tool-policy-rules.md). |

`*` alone matches every tool name. The `tools` block requires managed execution.

## Check order

For `tools/call` through `POST /api/v1/mcp/connections/{id}/invoke`, the checks run in
this order. A failure at step 1 returns HTTP 400. A denial at steps 2 to 5 is a JSON-RPC
error with code `-32001` that keeps the request ID.

1. The request is a single valid JSON-RPC `tools/call` message. The tool name must be
   listed in `execution.allowed_tools`.
2. The connection's `tools` policy: `deny` first, then `allow`.
3. The caller's grant `mcp:tools:{name}`. The `scope` denial names the missing grant.
4. If `read_only` is set, the tool's `readOnlyHint` must be known and `true`. A hint
   is known only when a `tools/list` response from this gateway, for this connection
   revision, recorded that tool's own annotation within the last 24 hours. Each tool
   expires on its own. A later listing refreshes the allowed tools that it lists with a
   `readOnlyHint`. Other tools keep the time they were last listed, and a tool whose
   annotation is absent from the listing loses its hint. Otherwise the call is denied
   with `read_only_unknown`, and the caller is told to list tools through the gateway.
5. The connection's policy `rules`, then the access point's `rules` for calls that come
   through an access point. The first matching `deny` refuses; with any `allow` rule, a call
   that matches none is refused; a rule that cannot be evaluated refuses. See
   [policy rules on tool calls](tool-policy-rules.md).
6. Request hooks (see [request hooks](request-hooks.md)).
7. Quota admission, when QuotaPlex covers the path.
8. Credential acquisition for the auth type.
9. The upstream call. The credential is applied only to the connection's configured origin.

Every denial in steps 1 to 5 happens before quota is reserved and before any credential
is acquired. Each `tools/call` is an independent request. A call never triggers a
`tools/list` itself.

`tools/list` through the same endpoint is governed as well. The list is authorized
before any credential is acquired, and the response is filtered to tools that are in
`allowed_tools`, permitted by the tools policy, granted to the caller and, under
`read_only`, annotated `readOnlyHint: true`. `tools/list` is not quota-metered.

## Worked example

```sh
# Show the auth types, their fields and the tools policy
aiplex tools auth-types

# API key in a header, read-only, with an allow prefix and one deny
aiplex tools set-auth search --auth-type api_key --auth-in header --auth-name X-Api-Key \
  --secret-ref search-key --allow 'files.*' --deny files.delete --read-only

# Change only the policy; the auth block is kept
aiplex tools set-auth search --deny files.delete --read-only=false

# Service OAuth with client credentials
aiplex tools set-auth search --auth-type oauth_client_credentials \
  --token-endpoint https://idp.example/token --client-id svc --client-secret-ref svc-secret \
  --client-auth-method client_secret_basic --scope read

# Remove the auth block; the connection's credential mode applies again
aiplex tools set-auth search --clear-auth
```

`set-auth` reads the connection, applies the change, and writes it back with the revision
it read, so a concurrent edit is rejected rather than overwritten. The `--secret-ref`,
`--client-secret-ref` and `--identity-ref` flags take names, not values. A non-OAuth
type needs the connection to already have an `execution` block. Create or update the
connection itself with `aiplex mcp connections create` (`--update` to replace one), or with
`PUT /api/v1/mcp/connections/{id}`.

The `--allow` and `--deny` flags replace their lists. Pass `''` to empty one.

## Limits

- Secret values are never stored in connection configuration, returned by any route,
  or written to logs. Only references are stored.
- `read_only` is only as strong as the server's annotation. `readOnlyHint` is a hint
  from the MCP server. AIPlex trusts it and does not verify the tool's behavior.
- The read-only check needs a prior `tools/list` through this gateway for the current
  connection revision. Any change to the connection invalidates the recorded hints.
  Each hint expires 24 hours after its tool was last listed with an annotation. An older
  hint is not used, even when other tools in the same listing are fresh.
- The `allow`, `deny` and `read_only` members match tool names and the read-only annotation only.
  Decisions on arguments, annotations other than `readOnlyHint`, the caller or the time are
  `rules`; they decide allow or deny and never change a call's content (see
  [policy rules on tool calls](tool-policy-rules.md), including its limits).
- The `auth` block applies to managed `tools/call` and `tools/list` through AIPlex.
  A tool server reached any other way is outside this policy.
- Auth types are a closed set. Other upstream schemes (for example mTLS or HMAC
  request signing) are not available.
- `google_id_token` requires a `gcp-adc` identity profile whose origins approve the
  connection's resource. `token_exchange` depends on the token service's behavior for
  the requested audience. AIPlex does not validate either upstream beyond the policy
  checks above.
- Secret existence is checked at save time. A secret deleted later is discovered at
  dispatch.
- The tools policy is connection-level. It does not replace the access point's own
  `expose` list, which is applied on top of it for access-point traffic.

## In the Console

MCPlex, tab Tool connections. Who may do what is decided per route by `cmd/aiplex-api/route_policy.go`:

| Route | Who |
| --- | --- |
| `GET /api/v1/mcp/auth-types` (line 65) | any verified identity |
| `POST /api/v1/mcp/connections/{id}/invoke` (line 115), used by List tools | any verified identity at the route; the handler checks the caller's per-tool grant |
| `GET`, `POST`, `DELETE /api/v1/mcp/connections/{id}/authorization` and `POST .../authorization/complete` (lines 118 and 119) | any verified identity (not used by the Console) |
| `POST /api/v1/mcp/connections/{id}/rules/test` (line 117), the dry run | operators |
| everything else under `/api/v1/mcp/` (the `tenantPrefix` at line 135): list, read, create, replace and delete of connections | operators |

Separately, the Console shows its MCPlex page to operators only (`admin` or `deployer`): its route is wrapped in
`RequireRole need="operator"` (`console/src/App.tsx`, line 63). The screens therefore have no read-only mode for other
users; the server stays the authority, and a refusal is shown with its message and code.

- **Add**: a guided form for `none`, `api_key`, `bearer`, `basic` and `oauth_client_credentials` (fields from the
  auth-type catalog), or a pasted full specification for every other type. There is no field for a secret value:
  credentials are references. One list of checks applies in the guided form and in a pasted specification, before any
  request is made:
  - a reference field must match the catalog's reference pattern and must not have the shape of a known credential (a
    token prefix such as `sk-`, `ghp_`, `xox`, `AKIA`, `AIza`, `ya29.`, a bearer scheme followed by a token, or a JWT). A long or mixed
    name that fits the pattern is accepted: the Console cannot tell it from a value, and only the server checks that
    the reference exists;
  - in a pasted specification every string value (descriptions included) and every member name is checked against the
    same shapes, credential-named members (`password`, `token`, `client_secret`, ...) are refused, and so are members
    the auth type does not define. A name with the shape of a credential is refused without being shown;
  - every URL (the server URL, `auth.token_endpoint`, any value written as an http(s) URL) must be http or https with
    no user name or password and no credential-like query parameter (`client_secret`, `api_key`, `token`, `key`,
    `password`, ...).

  Text that is not JSON is reported as "Not valid JSON", never quoted. The Console cannot discover tools from a
  server, so the allowed tools are typed in. A create refused as a conflict says nothing was saved and keeps the form.
- **Delete**: asks first and sends the revision it read; a conflict is shown and nothing is retried.
- **List tools**: the same `tools/list` through the gateway as `aiplex mcp connections test`. An answer without a tool
  list is an error. An empty list means the server listed no tools: the connection's tools policy may be hiding them, or
  the server may have none; the answer does not say which. This is the only request of these screens that shows the
  gateway's plain-text refusal reason (for example "current tool grant required"), as one line of at most 300
  characters, because the gateway's invoke route answers some refusals in plain text (`pkg/toolexec/gateway.go`,
  `http.Error`, for example line 162). Every other request shows the status text for a non-JSON error body, because
  an error or proxy page may echo the request: that includes the dry run and rule writes, whose bodies carry sample
  arguments and rules.
- **Rules**: list, add (at a position), remove and replace the whole set, each written with the revision read.
  Replacing a rule through Add keeps the members of the stored rule the Console does not model. Expressions are
  compiled and evaluated only by the server, whose error is shown unchanged with its code. The dry run posts a sample
  call and shows which rule decided; a result field the server did not send is shown as "not reported".
- **What is kept on a write**: a rules write sends the stored connection with only the rules changed, so every other
  member survives. The auth editor keeps unknown top-level members, unknown members of `tools` and the rules. It keeps
  unknown members of the `auth` block only while the auth type stays the same; changing the type replaces the auth block
  with the new type's fields, so unknown members of the old block are dropped. `execution.credential_mode` and
  `identity_ref` are re-aligned as the gateway requires.
- A failed refresh of the list keeps the rows on screen marked as possibly out of date, and Add, Edit auth, Delete and
  rule changes are disabled until a reload succeeds, as they are for a row whose revision is unknown.

What the Console checks do not do: they recognise known token shapes (the prefixes `sk-`, `pk-`, `rk-`, `ghp_` and
its siblings, `github_pat_`, `xox`, `AKIA`, `AIza` and `ya29.`, a JWT, and a `Bearer` scheme followed by a token-like
string) and URLs that carry credentials. A secret with no recognisable shape typed into a free-text field, such as a
description of a pasted specification, is not caught, and the server stores what it is sent. A URL with a query
parameter named like a credential (`key`, `sig`, `signature`, `token`, `secret`, `password`, `auth`, `apikey` and the
other names the check lists) is refused even when the parameter is not secret; to proceed, remove it from the URL and
supply the credential through a secret reference.

Limits: the screens were tested against a mocked API only, not a live deployment or `aiplex dev`. One tool call
(`aiplex mcp connections call`), per-user OAuth authorization and registry import are not in the Console. The dry run's
sample arguments are free text that is sent to the server (not stored), so use sample values.
