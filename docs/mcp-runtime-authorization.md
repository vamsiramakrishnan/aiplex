# Owned MCP OAuth execution

MCP client registration and runtime authorization are separate contracts. The
registration service still selects pre-registration, CIMD or DCR. The runtime
service now connects that registration to a verified user's authorization-code
flow and a principal/actor-owned grant consumed by managed tool execution.

## Connection configuration

A saved `tool-connection` can explicitly select delegated OAuth instead of an
operator bearer identity:

```json
{
  "id": "example-tools",
  "resource_url": "https://tools.example.com/mcp",
  "issuer": "https://login.example.com",
  "client": {
    "id": "example-client",
    "name": "Example tool client",
    "application_type": "web",
    "redirect_uris": ["https://console.example.com/tool-callback"],
    "scopes": ["tools.read"],
    "refresh_tokens": true
  },
  "execution": {
    "id": "example-tools",
    "endpoint": "https://tools.example.com/mcp",
    "credential_mode": "delegated-oauth",
    "identity_ref": "",
    "allowed_tools": ["search"],
    "protocol_version": "2025-11-25",
    "mode": "stateless-streamable-http"
  }
}
```

Delegated OAuth is one of eight `auth` types on a tool connection. The other types
(`none`, `api_key`, `bearer`, `basic`, `oauth_client_credentials`, `token_exchange` and
`google_id_token`) use secret references or an operator identity instead of a per-user
grant. The `auth` block and the `tools` allow, deny and read-only policy are described in
[tool connection auth](tool-connection-auth.md). This page covers only `oauth_user`.

The issuer must be explicit and advertised by the resource's validated metadata.
All metadata/token/resource endpoints must pass the operator's endpoint policy.
The resource endpoint must match the saved execution endpoint. Requested provider
scopes and redirect URIs are operator-owned; authorization callers cannot expand
them. `refresh_tokens` opts into refresh grant metadata and storage/use of returned
refresh tokens. Their issuance remains the authorization server's choice.
An existing DCR registration must have persisted `refresh_token` grant capability
before refresh can be enabled. An older authorization-code-only profile requires
a newly registered profile; changing the local flag does not change remote client
metadata.

Production uses the shared revisioned resource store and exact version references
in Secret Manager. PKCE verifiers and access/refresh tokens never enter resource
JSON, API responses or logs. The memory vault is for explicit development/tests.
An opaque vault reference is bound to the tenant, principal, delegated actor,
connection revision, issuer, resource, client profile and authorization grant.

## API lifecycle

All endpoints require the host's normal verified identity plus either
`mcp:authorize:<connection-id>` or a current grant for one configured tool.
Authentication tokens accepted by Aiplex are separate from the access tokens it
obtains for the downstream MCP server. Caller bearer tokens are not passed through.

| Endpoint | Result |
| --- | --- |
| `POST /api/v1/mcp/connections/{id}/authorization` | Accepts an exact registered `redirect_uri`; returns `flow_id`, an authorization URL and expiry |
| `POST /api/v1/mcp/connections/{id}/authorization/complete` | Accepts `flow_id`, `state`, `code` and `iss`; exchanges once and returns safe grant metadata |
| `GET /api/v1/mcp/connections/{id}/authorization` | Returns this verified principal/actor's grant status, scopes and expiry; no tokens |
| `DELETE /api/v1/mcp/connections/{id}/authorization` | Locally revokes the grant and retries destruction of retired secret versions |
| `POST /api/v1/mcp/connections/{id}/invoke` | Uses the owned downstream grant after permission and quota admission checks |

The client opens the authorization URL and completes the authorization server's
own login/consent. Its approved redirect receiver then submits the code and state
to the authenticated completion endpoint as JSON. The API does not publish a
public token callback, accept a caller-selected issuer/token endpoint or put
access tokens into URLs. This contract also supports native clients whose
registered redirect receiver is an exact loopback URI.

The flow stores a random PKCE S256 verifier in the secret vault and a hash of
random state in the durable record. Completion checks the original owner and
connection revision, state, redirect/resource/client binding and exact issuer
before claiming the exchange. Issuer omission is rejected when validated server
metadata advertises issuer support; a present issuer always must match exactly.
The resource indicator is included in both authorization and token requests.

A CAS transition precedes each code exchange or rotating refresh request. Other
replicas cannot reuse the same code or refresh generation. A timeout/lost response
is retained as uncertain and requires a new authorization; it is not replayed.
Missing refresh rotation, broadened scopes, changed resource, invalid token type
or missing/invalid access-token expiry fails closed. Refresh never extends the
original local grant lifetime. Exact session-commit readback handles a lost store
acknowledgement without destroying the committed live token.

New authorization replaces a prior grant only through its saved session revision.
A flow created before revocation cannot silently restore that revoked generation.
Revocation blocks new dispatch immediately after its durable CAS; failed secret
cleanup remains retryable without restoring access. It does not claim RFC 7009
revocation at the remote authorization server or cancellation of already committed
tool work.

## Managed transport and quota

`stateless-json` retains its existing single JSON response contract.
`stateless-streamable-http` accepts either a JSON result or a bounded finite SSE
response for one `tools/call`. SSE may contain progress/log notifications and must
end with exactly the matching terminal JSON-RPC response. Native bytes are
buffered and released after terminal accounting acknowledgement. A truncated
stream, unmatched response, server-initiated RPC, session header or missing final
response retains unknown exposure. There is no automatic GET reconnect or replay.

Both profiles target operator-declared servers that support independent calls;
they do not perform legacy protocol initialization or negotiate stateful sessions.
An OAuth grant is an identity/consent session, not an MCP protocol session. The
adapter does not infer session, sampling, elicitation or async-task support from
registration metadata or a protocol-version string.

Before dispatch, the adapter rereads the owned grant and current Aiplex tool
permission. Quota-protected calls also pin the grant ID into their configuration
fingerprint. Refresh may rotate a token under the same grant; revocation or a new
authorization generation invalidates the pinned dispatch contract. Request and
call counters cover this RPC's lifetime, not opaque internal tool jobs.

## Evidence and remaining qualification

Tests cover real local authorization/token/resource HTTP, PKCE/resource/issuer
binding, owner/actor/tenant isolation, one-shot callbacks, cross-replica refresh
fencing, uncertain responses, revocation, retryable secret cleanup, lost store
acknowledgements, native JSON/SSE preservation and actual quota settlement/abort.
They do not establish production IdP consent, native cloud broker acquisition,
upstream audience validation, live Secret Manager permissions or private routing.

Primary protocol references:

- [MCP authorization, 2026-07-28](https://modelcontextprotocol.io/specification/2026-07-28/basic/authorization)
- [MCP Streamable HTTP, 2025-11-25](https://modelcontextprotocol.io/specification/2025-11-25/basic/transports)
- [OAuth security best current practice, RFC 9700](https://www.rfc-editor.org/rfc/rfc9700.html)
