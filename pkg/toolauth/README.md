# Tool OAuth registration

`toolauth` registers OAuth clients for HTTP MCP resource connections. It does
not register tools in a catalog, issue access tokens, authenticate users, or
claim to turn Aiplex into an authorization server.

The service uses explicit endpoint-policy configuration for every HTTP request.
Redirects are rejected, response bodies are bounded, resolved addresses are
validated and pinned by `pkg/endpointpolicy`, and no ambient HTTP proxy is used.
Corporate private MCP servers require explicit private CIDRs; loopback HTTP is
an explicit local-development option. Discovery documents cannot add trusted
origins to the policy.

`Prepare` accepts a tenant-scoped `Connection` and `ClientProfile` and performs:

1. RFC9728 protected-resource metadata discovery. An advertised Bearer
   `resource_metadata` URL takes precedence; otherwise the resource-path and
   root well-known locations are attempted.
2. RFC8414/OIDC discovery, including path-bearing issuers. The metadata's issuer
   must exactly match the selected advertised authorization server. Selection
   is explicit when the resource advertises several authorization servers.
3. Verification that PKCE `S256` is advertised.
4. Pre-registered issuer-bound client selection, then advertised CIMD with an
   operator-hosted metadata document, then advertised RFC7591 dynamic client
   registration. DCR requests specify the client application type.

The current DCR/CIMD adapter supports public authorization-code clients using
`token_endpoint_auth_method=none`; confidential-client and `private_key_jwt`
registration need explicit adapters. `RegistrationAuthorizer` can supply an
initial-access bearer token for a protected registration endpoint. Such tokens
are host-owned credentials, not request input.

DCR succeeds only after a `201 Created` response with approved client metadata
and secure credential persistence. Returned `Registration` values contain
references, not client secrets or registration-access tokens. Registrations
are keyed by tenant, profile and exact authorization-server issuer; another
issuer gets a different registration. `MemoryCredentials` is for development;
production hosts should supply a Secret Manager-backed `CredentialStore`.
The credential type intentionally omits secrets during JSON serialization;
a secure-storage adapter must use a private encoding which explicitly includes
them. Existing registrations with changed redirects fail and require deliberate
re-registration.

`MetadataHandler` serves configured self-hosted CIMD documents without trusting
the inbound Host header. `FetchClientMetadata` lets an authorization-server
adapter validate external CIMD and uses bounded HTTP caching. `no-store` and
`no-cache` prevent reuse, exact URL/client-ID equality and approved redirect
shapes are enforced, and stale cached metadata is not used on fetch failure.
CIMD metadata is not user identity or application attestation.

`ProtectedResourceHandler` separately serves resource metadata. Inbound DCR or
CIMD support at an OAuth authorization server must be provided by a configured
server adapter; this package never advertises capabilities it cannot execute.

The result includes the required resource parameter and PKCE method. The host's
authorization-code flow still has to generate a verifier, bind state and redirect
URI, validate the authorization response issuer, exchange the code at the bound
token endpoint, and store tokens securely. MCP inbound tokens must not be
forwarded as upstream OAuth tokens.

Sources:

- https://github.com/modelcontextprotocol/modelcontextprotocol/blob/main/docs/specification/2026-07-28/basic/authorization/client-registration.mdx
- https://github.com/modelcontextprotocol/modelcontextprotocol/blob/main/docs/specification/2026-07-28/basic/authorization/authorization-server-discovery.mdx
- https://github.com/modelcontextprotocol/modelcontextprotocol/blob/main/docs/specification/2026-07-28/basic/authorization/security-considerations.mdx
- https://www.rfc-editor.org/rfc/rfc7591.html
