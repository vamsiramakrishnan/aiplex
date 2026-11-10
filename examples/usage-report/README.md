# See who used what

Every exchange that goes through an access point is recorded in the usage ledger: who called, which model or tool, tokens, estimated cost and status. This example sends a few requests and reads the ledger back.

## Run it

Start `aiplex dev` (see the [OpenAI SDK example](../openai-sdk/README.md)), then in another terminal:

```bash
export PATH="$PWD/bin:$PATH"
eval "$(aiplex dev env)"
bash examples/usage-report/report.sh
```

[`report.sh`](report.sh) sends three chat requests and one MCP tool call through the `local` access point, then runs:

```bash
aiplex usage summary --plane llmplex --by user,model
aiplex usage summary --by plane,tool
aiplex usage records
```

On a fresh `aiplex dev`, the summaries look like this (the rule lines are shortened):

```text
# model calls by user and model
USER  MODEL  REQUESTS  ERRORS  DENIED  IN  OUT  CACHE-W  MODALITY  UNITS  BATCH  COST  UNKNOWN  BOUNDED
──────────────────────────────────────────────────────────────────────────────────────────────────
principal-2751cfdeca30cd7898c2e828ee477dd5cf52883188d275a2b745295e0d40d1ff  mock-gpt  3  0  0  8  32  unknown  —  —  —  USD 0.000023 (estimated)  —  —

3 records scanned.

# everything by plane and tool
PLANE  TOOL  REQUESTS  ERRORS  DENIED  IN  OUT  CACHE-W  MODALITY  UNITS  BATCH  COST  UNKNOWN  BOUNDED
──────────────────────────────────────────────────────────────────────────────────────────────────
llmplex  —     3  0  0  8        32       unknown  —  —  —  USD 0.000023 (estimated)  —               —
mcplex   echo  1  0  0  unknown  unknown  unknown  —  —  —  unknown                   usage:1 cost:1  —

4 records scanned.
```

## How to read it

- **User.** The ledger stores a pseudonymous principal ID derived from the verified identity (`principal-` and a hash), not the email address. In `aiplex dev` there is one caller, you, so there is one row. On a platform with several users, agents and clients, `--by user`, `--by agent`, `--by client` and `--by caller-kind` split the same totals.
- **Estimated cost.** The mock model has a placeholder price, so cost is labelled `(estimated)`. A real deployment needs prices you have verified.
- **Unknown stays unknown.** The tool call has no token or cost figures, so those columns say `unknown`, and the `UNKNOWN` column counts the records (`usage:1 cost:1`) rather than treating them as zero.
- Other filters and groupings: `aiplex usage summary --help`. The ledger itself is specified in [usage-ledger](../../docs/usage-ledger.md).

## How it is tested

`make examples-test` runs `report.sh` against a fresh `aiplex dev` and fails unless the output contains the model, both planes, the tool call and the estimated cost.
