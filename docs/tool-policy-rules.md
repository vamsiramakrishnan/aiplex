# Policy rules on tool calls

A tool connection's `tools` block can carry `rules`: policy expressions that decide
allow or deny on structured facts about one `tools/call`: the tool and its annotations,
the call's arguments, the caller and the request time. They answer questions the name
lists cannot, such as "deny `write_file` outside `/srv/app`", "only the `eng` group may
call `deploy`" or "never call a destructive tool from an agent".

Rules decide. They do not change content: redaction and content checks are
[request hooks](request-hooks.md), which run after the rules. For the rest of the
tools block (`allow`, `deny`, `read_only`) see [tool connection auth](tool-connection-auth.md).

```yaml
# aiplex mcp connections rules set search -f rules.yaml
- id: no-etc
  effect: deny
  when: path_under(args.path, '/etc')
  message: files under /etc are off limits
- id: no-destructive-for-agents
  effect: deny
  when: caller.kind == 'agent' && has(tool.annotations.destructive) && tool.annotations.destructive
  message: agents may not call destructive tools
- id: eng-only
  effect: allow
  when: caller.groups_known && 'eng' in caller.groups
```

## The rule

| Field | Meaning |
| --- | --- |
| `id` | Required. Lower-case letters, digits, `-` and `_`, starting with a letter, at most 63 characters. Unique in the set. It identifies the rule in the caller's error, the ledger, spans and the dry run. |
| `when` | Required. A [CEL](https://cel.dev) expression that must be a boolean. |
| `effect` | Required. `deny` or `allow`. |
| `message` | Optional. Shown to the caller when this rule refuses a call. Printable text, at most 200 bytes, never templated: it cannot repeat the arguments. Write it as something safe to publish. Without one, the caller sees `tool call denied by policy rule <id>`. |
| `description` | Optional. What the rule is for. Operators only. At most 500 bytes. |

Rules go in `tools.rules` of a connection (up to 64) and in `tools[].rules` of an
[access point](access-points.md) (up to 64 per exposed connection). An access point's
rules apply in addition to the connection's, so a narrower scope can only add
restrictions; see [evaluation order](#evaluation-order).

## Evaluation order

For a `tools/call`, in this order, stopping at the first refusal:

1. The call is a valid JSON-RPC `tools/call` for a tool in `execution.allowed_tools`.
2. The connection's `tools.allow` and `tools.deny` lists.
3. The caller's `mcp:tools:{name}` grant.
4. `read_only`, when set.
5. **The connection's rules.** Every rule is evaluated in order. The first `deny` rule
   whose `when` is true refuses the call. If the connection has at least one `allow`
   rule and no `allow` rule matched, the call is refused (default deny once allow rules
   exist). A connection with only `deny` rules allows what no rule matches. A rule
   that cannot be evaluated refuses the call at once; see
   [fail closed](#fail-closed).
6. **The access point's rules**, for calls that come through an access point's
   `/mcp` endpoint, with the same semantics, on their own. The call must pass both sets.
   An `allow` rule on an access point cannot loosen the connection, because the
   connection's set already had to pass.
7. Request hooks, then quota admission, then the upstream credential and the call.

An `allow` rule that matches does not stop later rules: a later `deny` still refuses.
Rules are evaluated even after an `allow` matched, so an error in a later rule still
refuses the call. Every refusal at steps 1 to 6 happens before quota is reserved and
before a credential is acquired. A rule is never evaluated for a call an earlier step
already refused.

## Variables

All variables come from the verified identity, the saved configuration, the gateway's own
observations or the request itself. None comes from a header or query parameter the caller
controls.

| Variable | Type | Meaning |
| --- | --- | --- |
| `tool.name` | string | The tool being called, as the connection names it (not the `{connection}__{tool}` aggregate name). |
| `tool.connection` | string | The connection id. |
| `tool.annotations` | map | `read_only`, `destructive`, `idempotent`, `open_world` (booleans), from the MCP tool annotations (`readOnlyHint`, `destructiveHint`, `idempotentHint`, `openWorldHint`). **A member is absent when it is unknown.** Reading it without `has()` is an evaluation error, so the call is refused. On a call, the values come from the last `tools/list` this gateway made for the connection revision, at most 24 hours old (the record `read_only` uses). In `tools/list` they come from the listing entry. |
| `args` | map | The call's `arguments` object exactly as the caller sent it (before hooks transform it). `{}` when there are none. JSON numbers are numbers (`args.limit > 100` works with integer literals). A missing key is an error: guard with `has(args.key)`. |
| `caller.kind` | string | `user`, `agent` or `client`, derived from the verified token (the same derivation as access point admission). |
| `caller.principal` | string | The verified principal id. |
| `caller.groups` | list of string | The verified group ids as the identity provider sent them (not qualified by source). **Absent unless `caller.groups_known`.** |
| `caller.groups_known` | bool | `false` when the verifier recorded the caller's group view as missing or invalid. Unknown membership is not empty membership: an `allow` rule on a group never matches an unknown caller, and reading `caller.groups` unguarded is an evaluation error. |
| `caller.agent` | string | The delegated actor id (RFC 8693 `act`), or the principal id of a workload identity; `""` for a person. |
| `caller.client` | string | The ClientPlex registration id the token's authorized party matches; `""` when none. |
| `access_point.id` | string | The access point the call came through; `""` for a call to `/api/v1/mcp/connections/{id}/invoke` directly. |
| `access_point.labels` | map of string | Always empty: access points do not carry labels yet. |
| `request.time` | timestamp | The gateway's clock at evaluation (UTC). The only time source: there is no `now()`. |

Unknown fields are rejected when you save: `tool.nme`, `caller.email` or
`tool.annotations.nope` is a save error, not a runtime surprise.

## Helpers

CEL's standard library applies (`startsWith`, `endsWith`, `contains`, `size`, `in`, `has`,
`exists`, `all`, comparisons across integer and decimal numbers, `request.time` accessors).
AIPlex adds a few pure functions. None has side effects, reads a clock, touches the network or a file.

| Function | Meaning |
| --- | --- |
| `s.matches('re')` | RE2 match (linear time). The pattern must be a **string literal** of at most 256 bytes, so a caller cannot choose the pattern. |
| `lower(s)` | Lower-case. The string is at most 8192 bytes. |
| `path_under(path, root)` | True when `path` is `root` or below it after lexical normalization: `.`, `..` and repeated `/` are resolved first. Both must be absolute (start with `/`), at most 4096 bytes, with no NUL or backslash; otherwise it is an error. It does not decode percent escapes and does not resolve symbolic links. |
| `url_host(u)` | The lower-case host of a strictly parsed `http` or `https` URL, without port or trailing dot. A URL with credentials, spaces, backslashes, control or non-ASCII characters, a percent-encoded host, no host or another scheme is an error. Compare with `==` or `in`; do not use `endsWith` without a leading dot. |
| `any_in(a, b)` | True when the lists share an element. At most 256 entries each. |

## Fail closed

A call is refused with the error class `tool_policy_error`, and the rule that failed
is named, when:

- an expression fails to evaluate: a missing key without a guard, a type mismatch, an
  absent annotation or group list, an out-of-range index, division by zero, a helper
  refusing its input, or a result that is not a boolean;
- an expression exceeds the evaluation cost limit (10,000 CEL cost units) or the
  request is cancelled;
- the arguments are larger than 64 KiB or are not a JSON object (nothing is evaluated);
- the caller's identity facts cannot be established;
- the saved rules no longer compile (a damaged record): every call and every listing is
  refused until the connection is saved again.

Refusal by a matching `deny` rule, or by default deny, is `tool_policy_denied`.

Save time is the first check. A rule is compiled with a declared environment and rejected
when it does not parse or type-check, uses an unknown field or function, is not a boolean
expression (compare dynamic values: `args.flag == true`), has a `matches` with a pattern that is
not a short literal, or is too large. Limits: at most 64 rules per set, 1024 bytes per
expression, 256 syntax nodes, a nesting depth of 24.

## What the caller sees

A JSON-RPC error with code `-32001`, the request id, the rule's safe message and the rule id:

```json
{"jsonrpc":"2.0","id":"7","error":{"code":-32001,"message":"files under /etc are off limits",
 "data":{"reason":"tool_policy_denied","rule":"no-etc"}}}
```

`data.reason` is `tool_policy_denied` or `tool_policy_error`. `data.rule` is absent for the
default deny. Through `/api/v1/mcp/connections/{id}/invoke` the HTTP status is 403; through an
access point's `/mcp` endpoint the error is relayed in a normal JSON-RPC answer, like other
governed denials. For an evaluation error the message says which rule failed, never why.

## tools/list

A tool is hidden from `tools/list` only when every call to it would be refused **without
looking at the arguments or the time**. That is decided by the rules that read neither `args`
nor `request`:

- a `deny` rule of that kind that matches hides the tool;
- a rule of that kind that errors hides the tool (every call would reach it);
- if there are `allow` rules, all of them are of that kind and none matches, the tool is hidden;
- a rule that reads `args` or `request.time` never hides a tool, and an `allow` rule that reads
  them keeps the tool listed (it might match).

A tool that is listed can still be refused on call. Listing is not authorization: every call is
evaluated with its arguments. Annotations in a listing come from the listing entry, so a deny
rule on `tool.annotations.destructive` hides destructive tools from callers.

## Dry run

`POST /api/v1/mcp/connections/{id}/rules/test` evaluates a sample call and returns the
per-rule trace. Operators only (`admin` or `deployer`). Nothing in the sample is stored,
logged or written to the ledger.

```bash
aiplex mcp connections rules test search --tool read_file \
  --args '{"path":"/etc/hosts"}' --kind user --group eng
```

```json
{"tool":"read_file","args":{"path":"/etc/hosts"},"subject":{"kind":"user","groups":["eng"]},
 "access_point":"team","annotations":{"destructive":false},"rules":[],"time":"2026-11-10T12:00:00Z"}
```

`subject` describes the caller (`kind`, `principal`, `groups`, `groups_known`, `agent`, `client`);
`groups_known` defaults to true when `groups` is given and to false otherwise. `access_point` adds
that access point's rules for this connection. `annotations` simulates observed annotations;
without it the gateway's stored observations are used. `rules`, when present, replaces the saved
rules for this test only, to try candidates before saving. The answer has `decision` (`allow`,
`deny` or `error`), `error_class`, `reason`, `rule`, `message`, `trace` (each rule with
`set` = `connection` or `access_point`, `result` = `matched`, `not_matched`, `error` or
`not_evaluated`, and the evaluator's `error` text) and `list_hidden`. The dry run evaluates rules
only: name lists, scopes, `read_only`, quota and hooks are not part of it. Because it is
operator-only and returns the evaluator's error text, treat the answer as operator data.

## Recording

A refused call's usage record has `error_class` `tool_policy_denied` or `tool_policy_error`,
status `denied` and `tool_policy_rule`, the id of the rule that decided (empty for the default
deny). The exchange's span carries `aiplex.tool_policy.decision` and `aiplex.tool_policy.rule`.
The metric `aiplex.tool_policy.decisions` counts rule-set evaluations with the single label
`aiplex.tool_policy.decision` (`allow`, `deny`, `error`). Arguments, caller facts and messages
are never recorded; a test plants a marker in the arguments and checks the ledger, spans, metrics,
logs and the error body. The rule id is operator-chosen text: do not put sensitive text in it.

## Limits

- **Rules see the arguments as the caller sent them.** A request hook that masks text runs
  afterwards, so a rule cannot see masked values and the tool never sees the original.
- **Annotations are the server's claims.** AIPlex records the MCP annotations the server
  declared in a `tools/list` this gateway made; it does not verify the tool's behaviour. A call
  made before the tools were listed through the gateway, or more than 24 hours after, has no
  annotations (absent), so a rule that reads one unguarded refuses it. Hints for annotations other
  than `readOnlyHint` are recorded from listings made after this feature; list again to record them.
- **Group ids are not source-qualified.** Two identity providers using the same group id are the same
  string to a rule.
- **`access_point.labels` is always empty** because access points have no labels.
- **`path_under` is lexical.** It does not resolve symbolic links, case folding or encodings the tool
  applies itself. A tool that interprets paths differently from the gateway is outside what a rule can know.
- **Cost is limited at run time**, not estimated when you save, so an expensive expression is found by the
  limit (and refuses calls), not by a save error. Test with the dry run.
- **Not run against real MCP servers or clients beyond the local mock server** used by the end-to-end tests.
- **No transformation.** A rule cannot rewrite arguments or results, and cannot call out to anything.

The expression engine is CEL, from `cel.dev/cel-go` (Apache-2.0; the module formerly published
as `github.com/google/cel-go`). It adds about 6.2 MB (19%) to the `aiplex-api` binary, measured
with the release build flags.
