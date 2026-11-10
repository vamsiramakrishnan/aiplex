---
title: aiplex tools
sidebar_label: tools
description: "Configure MCP tool connection auth and tool policy"
---

This page is generated from the output of `aiplex --help` for the CLI built from `cmd/aiplex-cli`. Regenerate it with `npm run gen:cli` in `docs-site/`.

```text
Configure MCP tool connection auth and tool policy

Usage:
  aiplex tools [command]

Available Commands:
  auth-types  List tool connection auth types, their fields and the tools policy
  set-auth    Set a tool connection's upstream auth and tool policy

Flags:
  -h, --help   help for tools

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)

Use "aiplex tools [command] --help" for more information about a command.
```

### `aiplex tools auth-types`

```text
List tool connection auth types, their fields and the tools policy

Usage:
  aiplex tools auth-types [flags]

Flags:
  -h, --help   help for auth-types

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

### `aiplex tools set-auth`

```text
Read a tool connection, change its auth block and/or tools policy and write it back
with the revision that was read. A concurrent change fails with a revision conflict
instead of being overwritten. Settings you do not pass are kept.

Auth flags build the whole auth block for --auth-type (the previous block is replaced).
Secrets are given only as references with --secret-ref / --client-secret-ref: names in
the tenant secret namespace, never values. Run 'aiplex tools auth-types' for the fields
each type takes.

Tools policy flags change only the part you pass: --allow and --deny replace that list
(pass '' to empty it), --read-only[=false] sets read-only. Deny wins over allow.

Usage:
  aiplex tools set-auth <connection-id> [flags]

Examples:
  aiplex tools set-auth search --auth-type api_key --auth-in header --auth-name X-Api-Key \
      --secret-ref search-key --allow 'files.*' --deny files.delete --read-only
  aiplex tools set-auth search --auth-type oauth_client_credentials \
      --token-endpoint https://idp.example/token --client-id svc --client-secret-ref svc-secret \
      --client-auth-method client_secret_basic --scope read
  aiplex tools set-auth search --deny files.delete

Flags:
      --allow strings                 Tool name or prefix ending in '*' that may be called (repeatable)
      --audience string               token_exchange, google_id_token: audience
      --auth-in string                api_key: header or query
      --auth-name string              api_key: header or query parameter name
      --auth-prefix string            api_key: scheme before the key in a header, e.g. Token
      --auth-type string              Auth type (see 'aiplex tools auth-types')
      --clear-auth                    Remove the auth block (restores the credential mode's default behaviour)
      --clear-tools                   Remove the tools policy
      --client-auth-method string     client_secret_basic or client_secret_post
      --client-id string              OAuth client identifier
      --client-secret-ref string      Client secret REFERENCE name; never a secret value
      --deny strings                  Tool name or prefix ending in '*' that may never be called (repeatable)
  -h, --help                          help for set-auth
      --identity-ref string           google_id_token: operator identity profile (type gcp-adc)
      --read-only                     Only call tools annotated readOnlyHint=true
      --requested-token-type string   token_exchange: requested token type URN
      --resource string               RFC 8707 resource indicator URL
      --scope strings                 Requested OAuth scope (repeatable)
      --secret-ref string             Secret REFERENCE name (api_key, bearer, basic password); never a secret value
      --subject-token-type string     token_exchange: subject token type URN
      --token-endpoint string         oauth_client_credentials, token_exchange: token endpoint URL
      --username string               basic: username

Global Flags:
      --debug           Enable debug output
  -o, --output string   Output format: table, json, yaml (default "table")
      --token string    Bearer token (env: AIPLEX_TOKEN)
      --url string      AIPlex API URL (env: AIPLEX_URL)
```

