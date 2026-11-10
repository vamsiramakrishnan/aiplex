# Give an IDE or MCP client a governed tool endpoint

An access point serves MCP tools at one URL. Any MCP client that speaks streamable HTTP can use it with a bearer token. AIPlex checks who is calling before a tool runs and records the call in the usage ledger.

For `aiplex dev` the URL is:

```text
http://127.0.0.1:8080/ap/local/mcp
```

## Get the configuration

With `aiplex dev` running (see the [OpenAI SDK example](../openai-sdk/README.md) for starting it):

```bash
export PATH="$PWD/bin:$PATH"
eval "$(aiplex dev env)"
aiplex connect local
```

Among the snippets `aiplex connect local` prints is the MCP client configuration. This is its output, saved as [`mcp-config.json`](mcp-config.json):

```json
{
  "mcpServers": {
    "aiplex-local": {
      "type": "http",
      "url": "http://127.0.0.1:8080/ap/local/mcp",
      "headers": { "Authorization": "Bearer ${AIPLEX_TOKEN}" }
    }
  }
}
```

Put that block in your MCP client's configuration (where depends on the client; clients that use an `mcpServers` map, such as Claude Code's project `.mcp.json`, take it as is) and start the client from a shell where `AIPLEX_TOKEN` is set, since the header reads it from the environment. The project has tested this URL with the plain-HTTP script below, not with a specific third-party client. The configuration contains no secret; the token is not printed by `aiplex connect`.

### About `--client`

`aiplex connect local --client <profile>` renders profile-specific configuration only when the access point offers a wire that the client speaks. The `local` access point offers OpenAI chat only, so `--client claude-code`, `--client claude-desktop` and `--client codex` are refused:

```text
Error: connect: aiplex: CLIENT_NOT_APPLICABLE (422): client claude-code speaks anthropic-messages, vertex-anthropic, claude-apps, but this access point offers openai-chat
```

The default (`generic`) output above is the MCP configuration to use. Named client profiles become useful on an access point whose models match the client; see [ClientPlex](../../docs/clientplex.md).

## Try the endpoint without a client

[`mcp.sh`](mcp.sh) does what an MCP client does, with plain HTTP and `curl` (no MCP SDK): `initialize`, `tools/list`, then `tools/call`.

```bash
bash examples/mcp-tools/mcp.sh
```

Output on a fresh `aiplex dev` (copied as printed):

```text
# initialize
{"id":1,"jsonrpc":"2.0","result":{"capabilities":{"tools":{"listChanged":false}},"protocolVersion":"2025-06-18","serverInfo":{"name":"aiplex-access-point/local","title":"Local development","version":"1.0.0"}}}
# tools/list
{"id":2,"jsonrpc":"2.0","result":{"tools":[{"annotations":{"readOnlyHint":true},"description":"Echo the given text back.","inputSchema":{"properties":{"text":{"type":"string"}},"required":["text"],"type":"object"},"name":"local-tools__echo"},{"annotations":{"readOnlyHint":true},"description":"Add two numbers.","inputSchema":{"properties":{"a":{"type":"number"},"b":{"type":"number"}},"required":["a","b"],"type":"object"},"name":"local-tools__add"},{"annotations":{"destructiveHint":true,"readOnlyHint":false},"description":"Reset the server's call counter.","inputSchema":{"type":"object"},"name":"local-tools__reset_counter"}]}}
# tools/call local-tools__add {a:2, b:40}
{"id":3,"jsonrpc":"2.0","result":{"content":[{"text":"42","type":"text"}],"isError":false}}
```

The three tools (`local-tools__echo`, `local-tools__add`, `local-tools__reset_counter`) come from the built-in MCP server that `aiplex dev` starts. Tool names are prefixed with the connection name. Run `aiplex usage records` afterwards to see the calls.

## How it is tested

`make examples-test` runs `mcp.sh` and asserts that `tools/list` returns the tools and `add` returns 42. It also reads the MCP client configuration from `aiplex connect local -o json`, checks that its URL is the access point's MCP URL, calls `tools/list` at that URL, and fails if the saved [`mcp-config.json`](mcp-config.json) differs from what the command prints.
