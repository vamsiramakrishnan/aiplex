# Import a tool connection from the MCP Registry

`aiplex mcp registry` searches the official [MCP Registry](https://registry.modelcontextprotocol.io)
(or a private registry that implements its API) and imports a server's **remote** endpoint
as an ordinary [tool connection](tool-connection-auth.md). Import is a convenience for
writing the connection: it proposes a URL and a credential from the registry's `server.json`,
and everything it proposes is checked, bounded and left for you to approve.

```bash
aiplex mcp registry search adside
aiplex mcp registry show ai.adside/adside
aiplex mcp registry import ai.adside/adside --allow-tools list_campaigns --dry-run
aiplex mcp registry import ai.adside/adside --allow-tools list_campaigns --secret-ref adside-token --yes
aiplex mcp connections test adside
```

## Where the registry is fetched from

The CLI fetches the registry, from the machine it runs on. **The AIPlex API never contacts
a registry**, so the registry host must be reachable from the CLI, not from the API. Set
another registry with `--registry URL` or `AIPLEX_MCP_REGISTRY`; the default is
`https://registry.modelcontextprotocol.io`. A registry URL must be `https` (plain `http` only for a
loopback host) and carry no credentials, query or fragment. Environment proxies are used.

Endpoints used (API `v0.1`; the registry serves the same under `v0`):

| Use | Request |
| --- | --- |
| `search` | `GET /v0.1/servers?search=TEXT&version=latest&limit=N[&cursor=C]` |
| `show`, `import` | `GET /v0.1/servers/{name}/versions/{version}` (`latest` when no version is given; the name's `/` is sent as `%2F`) |

The entries are `server.json` documents (schema dates `2025-07-09`, `2025-09-16`, `2025-09-29`,
`2025-10-17` and `2025-12-11` are supported; the registry's current schema is `2025-12-11`).
A schema date this version of `aiplex` does not know is refused rather than guessed at. The
fixtures under `pkg/mcpregistry/testdata` are recorded answers of the official registry,
retrieved on 2026-11-10; the tests never use the network.

## What can be imported

AIPlex managed connections are **remote servers over stateless Streamable HTTP**. From an entry:

| Entry has | Result |
| --- | --- |
| a `remotes[]` entry of type `streamable-http` | Importable. `--remote N` picks one; the default is the first. |
| a `remotes[]` entry of type `sse` | Not importable: the legacy HTTP+SSE transport is not supported by the gateway. |
| `packages[]` (npm, pypi, oci, nuget, mcpb) | Not importable (local package): the server runs on the user's machine, usually over stdio. The gateway does not run local processes, and import does not invent a runner. They are listed by `show`. |

An entry with both a remote and a package imports its remote. `search` marks each entry
`remote` or `not importable (...)`.

## What is proposed, and what is not

| From the entry | In the connection |
| --- | --- |
| The remote's `url` | `resource_url` and `execution.endpoint`. Template variables `{name}` are filled from `--var name=value`, a value the registry fixes, or its default. A value is checked for where it lands: a host variable must look like a host name, a path variable is one path segment, a query value is escaped. A variable the registry marks `choices` must be one of them. A missing required variable stops the import and is listed. After substitution the URL must be `https` (or `http` to a loopback host), without credentials, whitespace or a fragment, and without a query: AIPlex pins tool endpoints without one. |
| A secret header | A **secret reference** the operator creates. `Authorization` with a `Bearer {x}` template, or marked secret with no template (Bearer is assumed and a note says so), becomes `bearer`. Another secret header becomes `api_key` with that header name and, for a `Scheme {x}` template, that scheme as the prefix. A secret that is the whole value of a query parameter becomes `api_key` in the query, and the parameter is removed from the pinned URL. A secret anywhere else in the URL is not importable. |
| Anything else the entry declares | **Not imported, and listed**: fixed header values (never taken from registry content), headers that are not secret (a user-supplied tenant id, for instance), a second credential (a connection sends one), a value with text after the secret. Nothing is dropped silently. |
| (nothing) | The tools. The registry does not list a server's tools. |
| (nothing) | Approval of the server's origin; secrets; exposure on an access point. |

Secret values never pass through the command: it prints the **reference names** to create and
what each is for (`Secrets to create`), the `AIPLEX_TOOL_SECRET_<NAME>` variable to set for
`aiplex dev`, and this API's secret namespace. `--secret-ref NAME` supplies the reference. If a secret is required and the
reference is missing, the command asks for its name on a terminal and **refuses to proceed
non-interactively**. Whether a secret exists is checked by the API when the connection is saved
(in `aiplex dev` it is checked at call time).

## Safety defaults

- **The tools are chosen by you.** Import needs `--allow-tools a,b` (exact names, no
  wildcards) or `--probe`. `--probe` sends `tools/list` from this machine to the server, without a
  credential, and uses what it finds; it refuses a server that declares a credential, a URL that
  is not `https`, a redirect, and hosts that are or resolve to private, loopback or link-local
  addresses (a registry could otherwise aim it at your network). With neither, the command stops
  and says so; `--dry-run` shows the rest.
- **The connection starts read-only.** `tools.read_only: true`: only tools whose MCP annotation
  `readOnlyHint` is `true` can be called, and only after the tools have been listed through the
  gateway once (`aiplex mcp connections test ID`). `--read-write` removes the restriction.
- **The origin is not approved by import.** The API calls only origins its network policy
  approves (`network.allowed_origins` in the connections file, `AIPLEX_CONNECTIONS_FILE`; with
  `aiplex dev`, `--allow-origin`). The dry run and the import print the origin and how to approve it,
  and saving fails with the same instructions until you have. Nothing is created for an origin
  that is not approved.
- **Confirmation.** Import asks before creating, and refuses without `--yes` when there is no
  terminal. An existing id needs `--update`.
- **Nothing is exposed.** The connection is not added to any access point.

## Trust model

Registry content is untrusted input.

- It is validated strictly: `$schema` must be an MCP registry schema URL of a supported date;
  `name` a reverse-DNS `namespace/name`; `version` a plain version (ranges are refused); header and
  variable names plain tokens; sizes and counts bounded (16 remotes, 32 headers, 4096 characters of text,
  1 MiB for one answer, 8 MiB for a listing); control characters in any text refuse the entry (`search`
  counts entries it skipped). The server named in the answer must be the one asked for.
- **Redirects are never followed**, so a registry cannot send the CLI to another host. Answers must be
  `application/json`.
- Text from the registry is printed with control characters removed; it is shown, never executed or
  used as a value. Descriptions are shown next to the secrets you create so you can judge them.
- An entry whose registry status is `deprecated` is imported with a warning; any other non-active status
  is refused.
- The registry vouches for the *name*, not the server: the official registry verifies namespace
  ownership, not behaviour. Read the server's documentation, keep the connection read-only, and check the
  origin you approve.

## Provenance

The connection stores a `provenance` block:

| Field | Meaning |
| --- | --- |
| `registry` | The registry base URL. |
| `name`, `version` | The entry used. |
| `retrieved_at` | When the CLI fetched it (UTC). |
| `sha256` | SHA-256 of the `server` object exactly as the registry served it. |

It is informational. Nothing in authorization or dispatch reads it; it is validated when saved
(`https` registry URL, a registry name and version, an RFC 3339 time, 64 hex digits). Re-fetching the
same version and comparing hashes shows whether the entry changed.

## Related: the access point's own registry-shaped listing

`GET /ap/{id}/v0.1/servers` lists an access point's MCP endpoints. It also serves the registry's response shape
(server.json schema `2025-12-11`): `servers[]` of `{server, _meta}` and `metadata.count`, with names under the
reverse-DNS of the gateway's public host (`example.gateway/ap-team`) and `remotes` of type `streamable-http`.
`_meta` is an empty object: the official registry's `status` and publication times are that registry's to
assert. The AIPlex members `templates`, `total`, `page` and `page_size` remain for existing clients.

## Limits

- Only the official registry's API (v0.1) and registries that implement it were considered; a private
  registry that differs is not supported. The CLI was run against the live official registry for `search`,
  `show` and `import --dry-run`; creating a connection was tested against a local registry only.
- `sse` remotes and `packages` are not importable. Some `sse` servers also answer Streamable HTTP; the
  registry does not say, so import them by hand with `aiplex mcp connections add`.
- Header values that are not a single secret (static headers, several variables) cannot be represented by a
  connection's one credential. The import lists them; the connection may not work until you handle them.
- The tools of a server are not in the registry; `--probe` works only for servers that need no credential.
- A secret in `Authorization` without a declared scheme is assumed to be Bearer.
