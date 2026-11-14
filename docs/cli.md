# The aiplex command line

`aiplex` is one command for the AIPlex API. This page covers the parts every command shares: how it
finds the API and the token, output formats, exit codes, confirmation prompts, colour, shell completion
and `--debug`. The per-command reference is generated from the binary's help; run `aiplex <command> --help`
for flags and examples. For the local stack, see [local development](local-development.md).

The CLI is implemented in `cmd/aiplex-cli`. The resolution order below is from `resolveTarget` in
`cmd/aiplex-cli/target.go` and the context handling in `cmd/aiplex-cli/main.go`.

## Command groups

These are the groups `aiplex --help` lists, with the page that covers each. Every group also has
its own `--help`.

| Group | Commands | Covered in |
| --- | --- | --- |
| Get started | `dev`, `login`, `logout`, `auth`, `whoami`, `config`, `ctx`, `doctor`, `health`, `version`, `init`, `quickstart` | [local development](local-development.md), this page |
| Connect clients | `access-points`, `connect`, `who`, `clients` | [access points](access-points.md), [harness onboarding](harness-onboarding.md) |
| Models | `llm` (surfaces, routes, batches, capacity) | [model providers](model-providers.md), [model batches](model-batches.md) |
| Tools | `mcp` (connections, registry, serve), `tools`, `tool-auth`, `skills` | [tool connection auth](tool-connection-auth.md), [policy rules](tool-policy-rules.md), [MCP Registry import](mcp-registry-import.md) |
| Agents and identity | `agents`, `identity`, `a2a` | [agent identity federation](agent-identity-federation.md), [connected Plexes](connected-plexes.md) |
| Usage and quota | `usage` (records, summary, prices, backfill, feedback, config), `quota`, `budgets`, `hooks` | [usage ledger](usage-ledger.md), [QuotaPlex administration](quotaplex-administration.md), [budgets](budgets.md), [request hooks](request-hooks.md) |
| Operate | `status`, `resources`, `apply`, `validate`, `modules`, `platform`, `upgrade`, `infra`, `native`, `catalog`, `deploy`, `undeploy`, `list`, `get`, `logs`, `runs`, `console`, `tui` | [Google Cloud permissions](gcp-permissions.md), [modules](modules.md), [native agent governance](native-agent-governance.md) |
| Additional | `completion`, `help` | [shell completion](#shell-completion) |

`aiplex access-points routing` shows the live routing and breaker state of an access point
([reliability and routing](reliability-and-routing.md)). The request order that hooks, routing, budgets
and tool rules apply is in [request lifecycle](request-lifecycle.md).

## Finding the API and the token

Every command resolves three things, in this order. The first source that is set wins.

| Setting | Order | Notes |
| --- | --- | --- |
| API URL | `--url`, then `AIPLEX_URL`, then the URL of the context, then `http://localhost:8080` | The last one is the default when nothing else is set. |
| Token | `--token`, then `AIPLEX_TOKEN`, then the token stored for the context | A stored token that is within five minutes of expiry is refreshed first, when it has a refresh token. |
| Context | `--context`, then `AIPLEX_CONTEXT`, then the current context | A name that does not exist stops the command with exit 2. |

A context is a named URL and credential pair. The CLI keeps contexts in `~/.aiplex/config.json` and tokens in
`~/.aiplex/credentials.json`. `aiplex login` signs in to the current context (`aiplex login --context prod` for
another) with the device grant and stores the token there. Switch contexts with `aiplex ctx <name>` or
`aiplex config use-context <name>`, and list them with `aiplex config show`.

`aiplex whoami` shows which of these the CLI is using. Against a local `aiplex dev`, after `eval "$(aiplex dev env)"`:

```text
Context:  local
API:      http://127.0.0.1:8080  (from AIPLEX_URL)
Token:    from AIPLEX_TOKEN, expires in 11h
Identity: developer@localhost
Subject:  developer
ID:       principal-3962fbf95aa914f120b3db67f2d5f5aac482b65d6e63475a80e231a19c65f093
Groups:   aiplex-local-admins
Roles:    admin
Grants:   a2a:* aiplex:* llm:* mcp:* skill:*
```

`whoami` names the source of each value. If the token is rejected, it says so and that the token came from
`AIPLEX_TOKEN`; unset that variable to use the stored token.

An unknown context stops before the command runs:

```text
Error: context "nope" does not exist
  List contexts with: aiplex config show
```

## Output formats

`-o` (`--output`) takes one value for every command:

| Format | Shows |
| --- | --- |
| `table` (default) | A compact table. Long values are shortened. |
| `wide` | Every column, untruncated. Usage records add the record ID, plane, agent, principal ID, client, session, batch, cache, modality, units and latency columns. |
| `json` | The API's response as JSON. Use it in scripts. |
| `yaml` | The same data as YAML. |

Usage records show the default columns and the wide ones:

```text
TIME      MODEL/TOOL  CALLER               ACCESS POINT  STATUS  IN  OUT  COST
just now  mock-gpt    developer@localhost  local         ok      1   9    USD 0.000006 (estimated)

Record IDs and every counter: -o wide. One record in full: aiplex usage get <id>.
```

`-o yaml` on a single resource prints the resource as YAML:

```text
id: local
kind: access-point
revision: 1
spec:
  caller_kinds:
    - user
    - agent
    - client
  display_name: Local development
  id: local
```

Use `-o json` for scripts. The JSON carries the revision that the write commands need.

## Colour

Colour is used only when standard output is a terminal and `NO_COLOR` is not set. Two ways to turn it off:

- `--no-color` on any command.
- `NO_COLOR=1` (any value) in the environment. This is the [no-color.org](https://no-color.org) convention.

## Exit codes

Every command exits with one of these codes. Scripts can branch on them.

| Code | Meaning |
| --- | --- |
| 0 | success |
| 1 | error (including server errors) |
| 2 | invalid input: bad flags or arguments, or a specification the server rejected |
| 3 | not signed in, token rejected, or not permitted |
| 4 | not found |
| 5 | conflict: the resource changed since it was read, or already exists |
| 6 | the API could not be reached |

A few examples. A mistyped access point ID exits 4:

```text
Error: access point "locl" not found
  Did you mean: local
  List them with: aiplex access-points list
  aiplex dev keeps state in memory: anything created before it restarted is gone.
```

An unreachable API exits 6, with the place it looked:

```text
Error: nothing is answering at http://127.0.0.1:1 (from --url)
  Start a local AIPlex with: aiplex dev   (or in the background: aiplex dev --detach)
  Or point the CLI elsewhere with --url, AIPLEX_URL or: aiplex config use-context <name>
```

A bad flag exits 2 and shows the usage:

```text
Error: unknown flag: --bogus
  Usage: aiplex usage summary [flags]
  Example: aiplex usage summary --by model,agent --since 7d
  More: aiplex usage summary --help
```

Errors that name a resource often add a `Did you mean` line for a close match, and a `List them with` line.

## Debugging

`--debug` prints the raw error after the friendly message, with the HTTP status, the API error code and the
request ID. Send the request ID when you report a problem:

```text
Error: access point "locl" not found
  Did you mean: local
  List them with: aiplex access-points list
  aiplex dev keeps state in memory: anything created before it restarted is gone.
[DEBUG] *aiplex.Error: get access point: aiplex: NOT_FOUND (404): resource not found
[DEBUG] HTTP 404 code=NOT_FOUND request_id=req-25 details=map[]
```

## Confirmation prompts and `--yes`

Destructive commands ask first when standard input and standard error are both a terminal. The question ends
`[y/N]`, and anything but `y` or `yes` cancels with nothing deleted.

When there is no terminal, as in a script, a pipe or CI, the command **refuses** (exit 2) and deletes nothing,
unless `--yes` (or `-y`) is given. The refusal says to add `--yes`. The check is in `confirm` in
`cmd/aiplex-cli/cmd_mcp.go`. For example, `aiplex access-points delete local < /dev/null` exits 2 and leaves the
access point in place.

The commands that take `--yes` include:

- Subcommands named `delete`: `aiplex access-points delete`, `aiplex llm surfaces delete`, `aiplex mcp connections delete`,
  `aiplex usage prices delete`, `aiplex agents delete`, `aiplex clients delete` and `aiplex resources delete`. `remove` and
  `rm` are aliases of `delete` for access points, model surfaces and tool connections.
- `aiplex undeploy`
- `aiplex config delete-context`
- `aiplex platform apply` (approves the Terraform changes) and `aiplex platform destroy`

The commands that write outside the API ask the same way, and refuse without a terminal unless `--yes` is given:

- `aiplex connect <access-point> --client <client> --write` (a diff of each client config file) and `aiplex connect --undo`
  ([harness onboarding](harness-onboarding.md))
- `aiplex usage prices catalog import` (creates price resources; `--dry-run` writes nothing)
- `aiplex usage prices backfill` (prices records whose cost was unknown; shows the dry-run result first, `--dry-run` writes nothing)

`aiplex platform preflight` only reads, and it changes nothing in Google Cloud.

`aiplex platform destroy` does not use the terminal check. Without `--yes` it reads a line from standard input
and proceeds only if that line is `destroy`. There is no top-level `aiplex delete`, `aiplex remove` or
`aiplex delete-context` command.

## Shell completion

`aiplex completion <shell>` prints a completion script for `bash`, `zsh`, `fish` or `powershell`. It completes
commands and flags, and the IDs of access points, model surfaces, tool connections, prices and identity providers,
by asking the current API with one request and a 1.5 second timeout. It is silent when the API does not answer.

```sh
# Bash (~/.bashrc or ~/.bash_profile)
echo 'source <(aiplex completion bash)' >> ~/.bashrc
source ~/.bashrc

# Zsh (~/.zshrc)
echo 'source <(aiplex completion zsh)' >> ~/.zshrc
source ~/.zshrc

# Fish
aiplex completion fish > ~/.config/fish/completions/aiplex.fish
```

The command's own help also gives the PowerShell line (`aiplex completion powershell | Out-String | Invoke-Expression`)
and a zsh form that writes to `fpath`. Run `aiplex completion --help` for them.

## Related pages

- [Local development](local-development.md): `aiplex dev`, its flags, background runs and the local stack.
- [Access points](access-points.md): `aiplex access-points create`, `apply` and the single pane.
- [Tool connection auth](tool-connection-auth.md): `aiplex mcp connections`.
- [Model providers](model-providers.md): `aiplex llm surfaces create`.
- [Usage ledger](usage-ledger.md#cli): `aiplex usage` and its columns.
- [Usage ledger, price catalogue](usage-ledger.md#price-catalogue): `aiplex usage prices catalog search|show|import`.
- [Harness onboarding](harness-onboarding.md): `aiplex connect --write` and `--undo`, `aiplex auth print-token` and `aiplex mcp serve`.
- [Observability](observability.md): `aiplex dev --otel`, and the telemetry variables the API reads.
- [Google Cloud permissions](gcp-permissions.md): `aiplex platform preflight`, and the upgrade steps.
