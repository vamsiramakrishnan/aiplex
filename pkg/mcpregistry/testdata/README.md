Recorded answers of the official MCP Registry (https://registry.modelcontextprotocol.io, API v0.1),
fetched once on 2026-11-10 and trimmed to a few entries. Tests read these files and never use the network.

- `latest_adside.json`: GET /v0.1/servers/ai.adside%2Fadside/versions/latest
- `search_adside.json`: GET /v0.1/servers?limit=3&version=latest&search=adside
- `server_arena.json`, `server_biel.json`, `server_2kw_package.json`, `server_aetherya_mixed.json`,
  `server_duvera.json`: GET /v0.1/servers/{name}/versions/{version} for ai.alphabazaar/arena@1.0.1,
  ai.biel/biel-ai@2.0.0, ai.2kw/mcp-server@8.0.3, ai.aetherya/mcp@1.0.5 and ai.duvera/gateway@1.0.0

They show: a secret header without a value (adside), a secret header with a `Bearer {var}` template (arena), URL
variables and a legacy SSE remote (biel), a local npm package (2kw), a server with both a package and a remote
(aetherya) and a secret header whose template variable is not declared (duvera).
