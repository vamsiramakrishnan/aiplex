# Mount identity and connection configuration

The API requires pinned identity configuration before startup. `aiplex platform apply`
checks Helm values before changing infrastructure. Configure either the
`JWT_ISSUER`, `JWT_AUDIENCE`, and HTTPS `JWT_JWKS_URL` environment variables, or
`AIPLEX_IDENTITY_PROVIDERS_FILE` pointing to a mounted provider configuration.

Operator configuration files are mounted from existing Kubernetes ConfigMaps or
Secrets. The chart accepts `api.extraVolumes` and `api.extraVolumeMounts`, limits
sources to ConfigMaps and Secrets, and makes every mount read-only. Credentials
belong in their managed credential source; do not place token values in Helm values.

Example `operator-values.yaml`:

```yaml
api:
  env:
    AIPLEX_IDENTITY_PROVIDERS_FILE: /etc/aiplex/identity/providers.json
    AIPLEX_CONNECTIONS_FILE: /etc/aiplex/connections/providers.json
    AIPLEX_TENANT_ID: example-tenant
  extraVolumes:
    - name: identity-config
      configMap:
        name: aiplex-identity-providers
    - name: connection-config
      secret:
        secretName: aiplex-connection-providers
  extraVolumeMounts:
    - name: identity-config
      mountPath: /etc/aiplex/identity
      readOnly: true
    - name: connection-config
      mountPath: /etc/aiplex/connections
      readOnly: true
```

Provision the ConfigMap and Secret in the API namespace (`aiplex-system` by
default), each with a `providers.json` key containing its documented configuration
format. Values contain resource names and mounted paths, never file contents.
The caller identity used by the CLI is separate from these server identity and
provider configurations.

```bash
aiplex platform apply --values operator-values.yaml
```

The preflight checks static source/mount/path consistency. It does not check that
Kubernetes resources exist, retrieve their contents, exchange credentials, or
establish upstream readiness. Those conditions are checked by Kubernetes and the
configured providers during deployment and use.

A single file can instead be mounted with `subPath`; then `mountPath` must equal
the corresponding environment-variable file path. Directory mounts receive
Kubernetes configuration updates; a `subPath` mount requires a pod restart to
receive updated Secret/ConfigMap content. Restart the API after changing identity
or provider configuration, since these are loaded at startup.

## Server environment variables

These variables are read by the API at startup from `api.env`. The names are exact. The API
reads `HOST` and `PORT`, not `AIPLEX_HOST` or `AIPLEX_PORT`.

| Variable | Default | Meaning |
| --- | --- | --- |
| `HOST` | `0.0.0.0` | Address the API listens on. With `AIPLEX_DEV_MODE=true` and `HOST` unset, the default is `127.0.0.1`. Development mode accepts only a loopback `HOST` (`127.0.0.1`, `::1` or `localhost`). Any other value stops startup. |
| `PORT` | `8080` | Port the API listens on. |
| `CONSOLE_ORIGINS` | empty | Comma-separated list of full browser origins (scheme, host and optional port) allowed to read API responses cross-origin. A `*` entry allows any origin, without credentials. When empty and development mode is off, cross-origin browser access is disabled. When empty and development mode is on, every origin is allowed. Set it in production. |
| `AIPLEX_TENANT_ID` | empty | The tenant this deployment serves. It is the single-tenant binding for deployment-wide state and for live federation. Live federation requires it unless development mode is on. With development mode on and this empty, the tenant is `development`. |
| `AIPLEX_DEV_MODE` | `false` | Local development mode. See the warning below. |
| `TRUSTED_PROXY_HOPS` | `0` | How many reverse proxies sit in front of the API and append to `X-Forwarded-For`. `0` ignores `X-Forwarded-For`, `Forwarded` and `X-Real-IP` and uses the connection's peer address. `N` (1 to 16) takes the client address `N` entries from the right of `X-Forwarded-For`; the entries on the left are written by the client and are never used. A malformed or too-short header falls back to the peer address. Set it to exactly the number of trusted proxies in the path, and only then. It affects the Tape ingest rate limit; the general rate limit always keys on the verified principal or the peer address. See [rate limits](authorization.md#rate-limits). |

Development mode (`AIPLEX_DEV_MODE=true`) is for one developer on one machine. It changes these
behaviours:

- The API binds to loopback only. A non-loopback `HOST` stops startup.
- Requests without a verified identity are admitted to the routes that otherwise require one,
  including the route policy. Handlers still apply their own checks where they need a verified
  identity or tenant.
- An anonymous request that carries `X-Forwarded-For`, `Forwarded` or `X-Real-IP` is refused with
  `403 PROXIED_DEVELOPMENT_REQUEST`, because it came through a proxy. See [authorization](authorization.md).
- Browser origins are not restricted when `CONSOLE_ORIGINS` is empty.
- The per-caller rate limit is 1200 requests per minute, not 200.
- Connection configuration may allow loopback upstreams (`network.allow_loopback`).

Startup logs record the `CONSOLE_ORIGINS` choice. Do not set `AIPLEX_DEV_MODE` in a Helm values
file for a shared or networked deployment.

## Federation environment variables

These variables are read by the API at startup. Set them under `api.env` with the other
environment variables. Each federation switch accepts `enabled` or `disabled`. An unset
value means disabled. Any other value stops startup. The audience and cache TTL variables
have the defaults shown.

| Variable | Default | Meaning |
| --- | --- | --- |
| `AIPLEX_AGENT_FEDERATION` | off | `enabled` turns on live authentication with agent identity bindings. Requires `AIPLEX_TENANT_ID` unless development mode is on. See [agent identity federation](agent-identity-federation.md). |
| `AIPLEX_USER_FEDERATION` | off | `enabled` turns on live authentication with user identity providers. Requires `AIPLEX_TENANT_ID` unless development mode is on. See [user identity federation](user-identity-federation.md). |
| `AIPLEX_AGENT_FEDERATION_AUDIENCE` | `PUBLIC_URL` | Default `audience` that agent identity presets suggest for new bindings. Stored bindings keep their own audience. See [agent identity federation](agent-identity-federation.md). |
| `AIPLEX_USER_DIRECTORY_CACHE_TTL` | `1m` | Lifetime of a resolved directory group view. At most `5m`. |
