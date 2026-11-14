# Examples

These examples run against `aiplex dev` in a few minutes each. They need Go (to build the binaries), `curl` and a terminal. No Docker, cloud account or API key.

Every example below is run by `make examples-test` (see [test.sh](test.sh)).

| Example | What it shows | Needs |
| --- | --- | --- |
| [openai-sdk](openai-sdk/README.md) | Point an OpenAI SDK (Python, Node) or plain `curl` at an access point | `curl`; optionally the `openai` Python or Node package |
| [mcp-tools](mcp-tools/README.md) | Give an IDE or MCP client a governed tool endpoint; the configuration `aiplex connect` prints | `curl` |
| [usage-report](usage-report/README.md) | See who used what: usage by user, model, plane and tool after a few requests | `curl` |
| [hooks](hooks/README.md) | Mask an email address and refuse a private key before they reach a model (request hooks) | `curl` |
| [budgets](budgets/README.md) | Team and project request budgets: a refusal that names the budget, and the status and ledger for it | `curl`; needs `aiplex dev --quota` (`make examples-test` starts it) |
| [sdk-python](sdk-python/README.md) | The Python SDK (`sdk/python`): access point, usage records and config, and one hook policy round trip | Python 3.10+; installed from the repository (skipped unless `EXAMPLES_INSTALL_SDKS=1`) |
| [sdk-typescript](sdk-typescript/README.md) | The TypeScript SDK (`sdk/typescript`): the same calls as the Python example | Node 20+ and npm; built from the repository (skipped unless `EXAMPLES_INSTALL_SDKS=1`) |

The SDK examples do not wrap model calls: the SDKs cover the control-plane API, and a chat request goes to the access point with an OpenAI or Anthropic SDK, as in [openai-sdk](openai-sdk/README.md).

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

This builds the binaries if needed, starts its own `aiplex dev` on port 18081 with an isolated home directory, runs every example and checks the output. Set `EXAMPLES_INSTALL_SDKS=1` to install the OpenAI Python and Node packages, the `aiplex` Python SDK and the `@aiplex/sdk` TypeScript SDK (built from this checkout) into temporary directories and run the SDK examples too (CI does this); `EXAMPLES_REQUIRE_SDKS=1` turns a skipped SDK example into a failure. See [test.sh](test.sh).

## Other files in this directory

These need more than `aiplex dev` and are not run by `make examples-test`.

| File | What it is | Status |
| --- | --- | --- |
| [quickstart.yaml](quickstart.yaml), [quickstart.json](quickstart.json), [mcplex-only.yaml](mcplex-only.yaml) | Declarative files for `aiplex apply -f` on a platform | Instances need a Kubernetes cluster and the template IDs (`kb-search-server`, `github-mcp-server`, ...) in your catalog: those are cloud-only, and `aiplex apply` under `aiplex dev` skips them and says why. Agents and routes apply under `aiplex dev` (`make examples-test` applies `quickstart.yaml` that way). Applying the same file again changes nothing (`make examples-test` applies `quickstart.yaml` twice and checks that the second run is all `unchanged`). Routes are applied in file order, so a fallback route comes before the route that names it. Every manifest file here passes `aiplex validate`; `make examples-test` checks that. |
| [quota/request-count.json](quota/request-count.json) | A QuotaPlex request-count policy file | Loaded by a Go test (`cmd/aiplex-api/quota_test.go`). See [quotaplex-integration](../docs/quotaplex-integration.md). |
| [modules/echo](modules/echo/README.md) | A standalone external module that implements the public module contract | Has its own Go test. |
| [durable-tape-runtime.yaml](durable-tape-runtime.yaml), [aiplex-tape-treasury](aiplex-tape-treasury/README.md) | An A2A agent on the Tape durable runtime | Needs a sibling `durable-agents` checkout and a cluster. Not run by `make examples-test`. |

`quickstart.yaml` carries a working route example (with the fallback route first).
