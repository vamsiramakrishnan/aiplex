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

## Federation environment variables

These variables are read by the API at startup. Set them under `api.env` with the other
environment variables. Each federation switch accepts `enabled` or `disabled`. An unset
value means disabled. Any other value stops startup. The audience and cache TTL variables
have the defaults shown.

| Variable | Default | Meaning |
| --- | --- | --- |
| `AIPLEX_AGENT_FEDERATION` | off | `enabled` turns on live authentication with agent identity bindings. Requires `AIPLEX_TENANT_ID`. See [agent identity federation](agent-identity-federation.md). |
| `AIPLEX_USER_FEDERATION` | off | `enabled` turns on live authentication with user identity providers. Requires `AIPLEX_TENANT_ID`. See [user identity federation](user-identity-federation.md). |
| `AIPLEX_AGENT_FEDERATION_AUDIENCE` | `PUBLIC_URL` | Default `audience` that agent identity presets suggest for new bindings. Stored bindings keep their own audience. See [agent identity federation](agent-identity-federation.md). |
| `AIPLEX_USER_DIRECTORY_CACHE_TTL` | `1m` | Lifetime of a resolved directory group view. At most `5m`. |
