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

On a fresh `aiplex dev`, the output of `report.sh` is this (copied as printed; the time column reads `just now` because the calls were just made):

```text
# model calls by user and model
USER                 MODEL     REQUESTS  ERRORS  DENIED  IN  OUT  COST
developer@localhost  mock-gpt  3         0       0       8   32   USD 0.000023 (estimated)

3 records scanned.

# everything by plane and tool
PLANE    TOOL  REQUESTS  ERRORS  DENIED  IN       OUT      COST
llmplex  —     3         0       0       8        32       USD 0.000023 (estimated)
mcplex   echo  1         0       0       unknown  unknown  unknown

4 records scanned.

# the individual records
TIME      MODEL/TOOL  CALLER               ACCESS POINT  STATUS  IN       OUT      COST
just now  echo        developer@localhost  local         ok      unknown  unknown  unknown
just now  mock-gpt    developer@localhost  local         ok      2        10       USD 0.000007 (estimated)
just now  mock-gpt    developer@localhost  local         ok      3        11       USD 0.000008 (estimated)
just now  mock-gpt    developer@localhost  local         ok      3        11       USD 0.000008 (estimated)

Record IDs and every counter: -o wide. One record in full: aiplex usage get <id>.
```

## How to read it

- **User.** The caller is shown by name. Each record carries `principal_display`, a readable label that the server takes from the verified identity: a verified email, display name or subject. It can be an email address, so do not publish these outputs without checking them. The key the ledger filters on is `principal_id`, shown as `USER` in `aiplex usage records -o wide`. In `aiplex dev` there is one caller, you, so there is one row. On a platform with several users, agents and clients, `--by user`, `--by agent`, `--by client` and `--by caller-kind` split the same totals.
- **Estimated cost.** The mock model has a placeholder price, so cost is labelled `(estimated)`. A real deployment needs prices you have verified.
- **Unknown stays unknown.** The tool call has no token or cost figures, so those columns say `unknown` rather than zero. `aiplex usage summary -o wide` adds an `UNKNOWN` column that counts the records whose usage or cost is unknown.
- Other filters and groupings: `aiplex usage summary --help`. Column layouts, and what `-o wide` adds, are in [the CLI section of the usage ledger](../../docs/usage-ledger.md#cli). The ledger itself is specified in [usage-ledger](../../docs/usage-ledger.md).

## How it is tested

`make examples-test` runs `report.sh` against a fresh `aiplex dev` and fails unless the output contains the model, both planes, the tool call and the estimated cost.
