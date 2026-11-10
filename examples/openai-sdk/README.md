# Point an OpenAI SDK at AIPlex

An OpenAI SDK needs two settings to talk to AIPlex instead of OpenAI: a base URL and a token. `aiplex dev` exports both.

## Run it

Start the local stack in one terminal (from the repository root, after `make build`):

```bash
export PATH="$PWD/bin:$PATH"
aiplex dev
```

In another terminal:

```bash
export PATH="$PWD/bin:$PATH"
eval "$(aiplex dev env)"        # sets OPENAI_BASE_URL and OPENAI_API_KEY (and AIPLEX_URL, AIPLEX_TOKEN)
```

Then pick a language:

| File | Needs | Command |
| --- | --- | --- |
| [`chat.py`](chat.py) | `pip install openai` | `python3 examples/openai-sdk/chat.py` |
| [`chat.mjs`](chat.mjs) | `npm install openai` (Node 20+, run from the folder that has `node_modules`) | `node chat.mjs` |
| [`chat.sh`](chat.sh) | `curl` only | `bash examples/openai-sdk/chat.sh` |

Each prints the mock model's reply:

```text
Hello from the AIPlex mock model. You said: Hello
```

`chat.sh` uses plain HTTP, so it works without any SDK and shows the raw response:

```json
{"choices":[{"finish_reason":"stop","index":0,"message":{"content":"Hello from the AIPlex mock model. You said: Hello","role":"assistant"}}],"created":1794283869,"id":"chatcmpl-mock-1794283869879476177","model":"mock-gpt","object":"chat.completion","usage":{"completion_tokens":9,"prompt_tokens":1,"total_tokens":10}}
```

## What is going on

`OPENAI_BASE_URL` is `http://127.0.0.1:8080/ap/local/v1`, the OpenAI-compatible root of the access point `local`. `OPENAI_API_KEY` holds an AIPlex token, not an OpenAI key. The SDK sends it as a bearer token; AIPlex checks who is calling before the request reaches the model, and writes a usage record (see [usage-report](../usage-report/README.md)).

To use a real model, restart `aiplex dev` with `--provider openai --api-key-env OPENAI_API_KEY` and change `mock-gpt` to the model it reports (`gpt-4o-mini` by default). That path has not been run against a live provider by the project.

## How it is tested

`make examples-test` runs `chat.sh` always. It runs `chat.py` and `chat.mjs` when the SDK is available, or installs them into a temporary directory when `EXAMPLES_INSTALL_SDKS=1` (CI does). The versions it was run with are pinned in [`../test.sh`](../test.sh) (Python `openai` 3.48.0, Node `openai` 7.54.0). If an SDK is missing the test prints `SKIP` for that file and still passes, unless `EXAMPLES_REQUIRE_SDKS=1` is set.
