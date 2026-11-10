# Examples

Three small examples run against `aiplex dev` in a few minutes each. They need Go (to build the binaries), `curl` and a terminal. No Docker, cloud account or API key.

| Example | What it shows | Needs |
| --- | --- | --- |
| [openai-sdk](openai-sdk/README.md) | Point an OpenAI SDK (Python, Node) or plain `curl` at an access point | `curl`; optionally the `openai` Python or Node package |
| [mcp-tools](mcp-tools/README.md) | Give an IDE or MCP client a governed tool endpoint; the configuration `aiplex connect` prints | `curl` |
| [usage-report](usage-report/README.md) | See who used what: usage by user, model, plane and tool after a few requests | `curl` |

Start the local stack once and keep it running:

```bash
make build
export PATH="$PWD/bin:$PATH"
aiplex dev                          # terminal 1
eval "$(aiplex dev env)"            # terminal 2, from the repository root
```

## Test them

```bash
make examples-test
```

This builds the binaries if needed, starts its own `aiplex dev` on port 18081 with an isolated home directory, runs every example and checks the output. Set `EXAMPLES_INSTALL_SDKS=1` to install the OpenAI Python and Node packages into a temporary directory and run the SDK examples too (CI does this); `EXAMPLES_REQUIRE_SDKS=1` turns a skipped SDK example into a failure. See [test.sh](test.sh).

## Other files in this directory

These need more than `aiplex dev` and are not run by `make examples-test`.

| File | What it is | Status |
| --- | --- | --- |
| [quickstart.yaml](quickstart.yaml), [quickstart.json](quickstart.json), [mcplex-only.yaml](mcplex-only.yaml) | Declarative files for `aiplex apply -f` on a platform | The template IDs (`kb-search-server`, `github-mcp-server`, ...) must exist in your catalog. `aiplex dev` has no catalog, so applying them there reports `template ... not found`. Agent registration in these files works; the route sections and instances have not been run against a platform. |
| [quota/request-count.json](quota/request-count.json) | A QuotaPlex request-count policy file | Loaded by a Go test (`cmd/aiplex-api/quota_test.go`). See [quotaplex-integration](../docs/quotaplex-integration.md). |
| [modules/echo](modules/echo/README.md) | A standalone external module that implements the public module contract | Has its own Go test. |
| [durable-tape-runtime.yaml](durable-tape-runtime.yaml), [aiplex-tape-treasury](aiplex-tape-treasury/README.md) | An A2A agent on the Tape durable runtime | Needs a sibling `durable-agents` checkout and a cluster. Not run here. |

Earlier versions of this directory also had `llm-routing.yaml` and `multi-agent.yaml`. They were removed because the project could not make them apply: under `aiplex dev`, `llm-routing.yaml` (which needs no templates) is rejected with `invalid backend configuration` or `fallback model requires a saved route configuration`, and `multi-agent.yaml` combines six template IDs with the same routes block. A corrected route example needs to be verified on a platform before it is added back.
