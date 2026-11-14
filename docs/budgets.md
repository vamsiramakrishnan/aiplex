# Budgets

A budget caps cost, tokens or requests for a team, project, access point or other set of callers over a calendar window. Budgets sit above [QuotaPlex](quotaplex-core.md) per-caller controls and form a tree: a request draws from its own budget and from every parent, and is refused when any one of them is exhausted.

Budgets are not a second limiter. A budget is a saved tenant resource (`kind: budget`). When a managed model or tool request is admitted, the quota host works out which budgets the request draws from and passes them to QuotaPlex in the request's demand. QuotaPlex checks them in the same transaction as the operator policy's own budgets, rates and concurrency limits, so there is one admission decision, one reservation and one settlement.

What this page states is what the code does and what its tests cover. The last section lists what is not implemented or not validated.

## Requirements

QuotaPlex must be on for the tenant (`AIPLEX_MODULES` includes `quotaplex` and `AIPLEX_QUOTA_FILE` names an operator policy file; see [managed QuotaPlex execution](quotaplex-integration.md)). Saving a budget without QuotaPlex is refused with `409 QUOTAPLEX_REQUIRED`: a budget that cannot be enforced must not look active. Locally, `aiplex dev --quota` turns QuotaPlex on for the mock surfaces and tools ([below](#local-development)).

Every managed operation still needs its QuotaPlex binding; budgets do not add coverage. An operation without a binding is refused as before.

## Specification

```json
{
  "id": "platform",
  "display_name": "Platform team",
  "parent": "engineering",
  "limit": {"unit": "cost", "currency": "USD", "amount": "250.00"},
  "window": {"kind": "calendar", "unit": "month", "time_zone": "Australia/Melbourne"},
  "members": {
    "groups": [{"source": "source-3f9c...", "id": "platform-eng"}],
    "principals": [],
    "agents": [],
    "client_registrations": [],
    "access_points": ["ide"],
    "projects": ["apollo"]
  },
  "on_unknown_cost": "deny",
  "include_tool_calls": false,
  "thresholds": [50, 80, 100]
}
```

| Field | Rules |
| --- | --- |
| `id` | `[A-Za-z0-9._-]`, 1 to 64 characters, equal to the resource id. |
| `display_name` | Optional text, up to 128 characters. Shown in refusals to members. |
| `parent` | Optional id of another budget. The tree has at most 4 levels, no cycles, and at most 24 budgets per tenant. A parent must exist when the child is saved; a parent with children cannot be deleted. |
| `limit.unit` | `cost`, `tokens` or `requests`. |
| `limit.currency` | Required for `cost` (three upper-case letters), refused otherwise. |
| `limit.amount` | An exact non-negative decimal **string**. Whole numbers for `tokens` and `requests`. For `cost`, no more decimal places than the scale of the policy's money meter; an amount that is finer is refused, never rounded. |
| `window` | See [windows](#windows). |
| `members` | See [members](#members). Empty members match nobody directly; such a budget draws only through its children. |
| `on_unknown_cost` | `cost` only. `deny` (default) or `allow_and_flag`. See [unknown cost](#cost-and-unknown-prices). |
| `include_tool_calls` | `cost` only, optional, default `false`. When `true` the budget also counts managed tool calls at their price. Deployment actions are not counted by it: they are never charged or refused through this field, whatever the price book says. See [tool calls in a cost budget](#tool-calls-in-a-cost-budget). Refused on a `requests` budget (it already counts tool calls) and on a `tokens` budget (tool calls have no token meters); the error names the field and says why. |
| `thresholds` | Up to 8 distinct percentages from 1 to 100. Default 50, 80, 100. See [thresholds](#thresholds-and-status). |

Unknown fields are refused. A cost budget's currency must equal the currency of every cost budget above it: budgets do not roll up across currencies. Saving also checks, when QuotaPlex runs, that the quota policy has a money meter in that currency (for cost) or a meter of that unit, and that the amount is exact at the meter's scale.

## Members

A request draws from a budget when any member selector matches. Every selector is a verified fact or administrator-set configuration. None is read from a header, query or body.

| Selector | Matches | Applies to |
| --- | --- | --- |
| `groups` | A group in the caller's **verified** token groups, source-qualified exactly as access point subjects are | every managed route |
| `principals` | The caller's canonical principal id | every managed route |
| `agents` | The delegated actor's id, or a workload principal acting as itself | every managed route |
| `client_registrations` | A ClientPlex registration id equal to the token's verified authorized party | requests through an access point |
| `access_points` | The access point the request entered through | requests through an access point |
| `projects` | The `project` label an administrator set on the access point | requests through an access point |

Access points also carry `labels` (trusted tag keys stamped on usage records, see the usage ledger). A budget's `projects` selector deliberately matches the access point's `project` field only, not `labels`: `project` is a single, validated, dedicated field, so a tag key someone adds for reporting cannot silently move spend between budgets. Tags that callers may send in an allowlisted header never select a budget.

A free-form project label is trusted only because only an administrator can set it, on the access point specification (`"project": "apollo"`). A request header such as `X-Project` never selects a budget (there is a test for this).

A request also draws from **every ancestor** of each budget it matches: if `platform` has parent `engineering`, a platform request draws from both, and `engineering` may have no members of its own. A caller who belongs to two unrelated budgets draws from both and their ancestors. A request may draw from at most 16 budgets; beyond that it is refused (`QUOTA_BUDGET_TOO_MANY`).

### Unknown group membership

If the identity source did not verify the caller's groups (the group claim is missing, malformed or an overage), **the request is refused** with `503 QUOTA_MEMBERSHIP_UNAVAILABLE`, whether or not the caller would have matched a group budget. This is not new behaviour: QuotaPlex has always refused a managed request whose principal membership cannot be established. Budgets inherit it for a reason. Access point admission and role bindings treat unknown groups as "no match", which is safe there because a non-match withholds a privilege. A budget is a restriction, so treating unknown as "not a member" would let a caller escape a limit. A caller whose group list is verified and empty is a non-member of every group budget and is unaffected by them.

Reads follow the same rule: a caller with unknown groups is not a member for the purposes of `status`, and gets the same `404` as a non-member.

## Units

| Unit | What is counted | Which requests |
| --- | --- | --- |
| `requests` | The binding's request meters (meters of unit `requests`) | every plane |
| `tokens` | The **sum** of the binding's mapped token meters (uncached input, cached, output, thought and so on) | model requests only |
| `cost` | The sum of the money meters in the budget's currency, priced by the policy's price book | model requests; also tool calls when `include_tool_calls` is `true` |

Token budgets do not count tool or agent calls. Cost budgets do not count agent calls, and count tool calls only when they opt in with `include_tool_calls`. A model request on a binding that maps no token meter (a `request-count` binding) cannot be metered in tokens: it is refused with `403 QUOTA_BUDGET_UNSUPPORTED_OPERATION` rather than counted as zero.

Token meters are disjoint, so their sum is not a provider's "total tokens" figure. It includes cache reads and writes if the binding maps them. The reservation at admission is the sum of each meter's trusted exposure bound; each input subset reserves the whole attested input ceiling, so a token or cost budget can reserve several times what a request finally uses. See [model metering profiles](model-metering-profiles.md).

### Cost and unknown prices

A cost budget counts money meters. Money comes only from QuotaPlex price books in the operator policy: versioned, exact rational prices per raw meter. The usage ledger's price resources and the price catalogue are separate and are not used by budgets.

A request whose cost cannot be computed is never counted as zero. A request is uncostable when no price book matches its target, or the matching book does not price every billable meter the request bounds.

| `on_unknown_cost` | Behaviour |
| --- | --- |
| `deny` (default) | The request is refused: `403 QUOTA_BUDGET_COST_UNKNOWN`, naming the budget to its members. |
| `allow_and_flag` | The request is admitted, draws nothing from that budget, and increments the budget's `unknown_cost_requests` counter, shown by `status`. It still draws from every other budget. |

`allow_and_flag` means the budget does not see that spend. Treat `unknown_cost_requests` as spend the budget cannot bound.

If the quota policy has a price book that matches a target but does not price all of its billable meters, QuotaPlex itself refuses the request (`unpriced_meter`) even without a cost budget, unless every cost budget that applies is `allow_and_flag`. That is the existing QuotaPlex rule.

### Tool calls in a cost budget

A cost budget counts model calls only, unless its specification sets `"include_tool_calls": true` (`--include-tool-calls` on `aiplex budgets create` or `update`). This is opt-in and off by default: a budget saved before the field existed, or saved without it, is not charged for tool calls and never refuses one, whether or not the tool is priced. Turning the field on or off keeps the budget's counter; it is not part of the counter's shape.

With the field set, a managed tool call (the `mcplex` plane, through an access point or `/api/v1/mcp/connections/{id}/invoke`) by a member draws from the budget exactly as a model call does:

* **Price.** The cost is what the quota policy's price book gives the tool call's binding meters (`requests`, `tool_calls`) in the budget's currency. A price book matches a tool call by `plane: mcplex`, the connection id as `resource_id`, or `operation` `tools/call:<tool>`. The usage ledger's price resources are not used.
* **Admission.** The priced call is reserved against the budget, in the same transaction as every other control, before the tool server is contacted. If the budget cannot cover it the call is refused (`QUOTA_BUDGET_EXHAUSTED`, HTTP 429 with `Retry-After`) and the tool server sees no request.
* **No price.** A tool call with no matching price is refused with `QUOTA_BUDGET_COST_UNKNOWN` (`budget "…" is a cost budget and this tool call has no price`), before dispatch, under the default `on_unknown_cost: deny`. Under `allow_and_flag` it is admitted and counted in `unknown_cost_requests`, as for a model.
* **Settlement.** A completed call settles at its priced usage. A call whose outcome is unknown (a truncated response, a stream without a final message, a timeout or a connection reset after the request was sent, a client disconnect) keeps its reservation as held exposure: it is never released as zero, and it keeps counting against the budget until recovery or reconciliation.
* **Mixed spend.** Model and tool calls draw from the same counter, so `spent`, `reserved` and `remaining` in `status` include both.

A managed tool call refused this way returns the budget's message and HTTP status. Through an access point it is a JSON-RPC error with `data.http_status`; on the connection's own invoke route it is a plain-text HTTP error. Neither carries the `QUOTA_BUDGET_*` code, which the managed tool gateway does not render (the code is in the API's admission decision and in its tests).

Deployment actions run on the `mcplex` plane too (operation `tools/call:anvil.<phase>:…`), but they are not tool calls for this field. The deployment path marks its admissions as deployment actions, and a cost budget never draws from one or refuses one, whatever the price book says, even when the same member's tool calls have exhausted it. A `requests` budget still counts deployment actions, as it counts every call.

A priced call and an unpriced call admitted under `allow_and_flag` share the budget's one counter: the priced call's cost is held and consumed, and the unpriced call is counted in `unknown_cost_requests`, never as zero spend, in either order. (Before this was fixed, the second kind of call in a window was refused with `409 QUOTA_ATTEMPT_CONFLICT`; this affected a priced and an unpriced model sharing such a budget too.) Counter records written before the fix still load and keep their counts: one first written by an unpriced call (no meter identity) adopts the budget's money meter on its next use, and one first written by a priced call already carries it.

## Windows

| `window.kind` | Fields | Meaning |
| --- | --- | --- |
| `calendar` | `unit`: `day`, `week` or `month`; `time_zone`: an IANA zone (required) | Resets at local midnight in that zone: every day, every Monday, or the first of the month. |
| `tumbling` | `hours`: 1 to 744 | Consecutive windows of that length, aligned to 2024-01-01T00:00Z. |

**Rolling windows are not supported.** QuotaPlex rejects them: a rolling total needs per-event expiry history that its counters do not keep, and the code refuses them rather than approximate. A `tumbling` window is not a rolling one: a spender can use a full allowance just before a boundary and another just after it.

A calendar window is refused for a zone where local midnight does not exist or is ambiguous on a boundary date; QuotaPlex does not guess an offset. Windows are half-open: a request is counted in the window in which it was admitted and settles there even if it finishes after the boundary.

Editing a budget's amount, name, parent, members or thresholds keeps its counter. Editing its unit, currency or window starts a new counter; the old one stays in the store until it expires and is no longer read.

## Reservation, settlement and the overshoot bound

Tokens and cost are only known after the response, so budgets use QuotaPlex's reserve-then-settle lifecycle. Where this admission sits among the other checks is in [request lifecycle](request-lifecycle.md#models).

1. **Admission** reserves the request's trusted exposure bound against every budget it draws from. A request is admitted only if `limit - consumed - reserved` is at least the reservation, for every budget, in one transaction. The bound is the request count for `requests`, the attested token bounds for `tokens` (the requested output cap plus the provider limits an operator attested) and the same bounds priced for `cost`.
2. **Settlement** replaces the reservation with the observed usage from the terminal upstream response. Unused reservation is released.
3. A request refused or aborted **before dispatch** (hook denial, missing grant, a failed dispatch-time recheck) releases its reservation and draws nothing.

**The bound.** Because every admission is atomic and checks reserved plus consumed against the limit, N concurrent requests cannot admit more than the limit allows. A concurrency test admits 40 parallel requests against a limit of 7 and gets exactly 7. A budget can end above its limit in one way only: a settled overrun, where the provider reports more usage than the reserved bound. That is retained as consumed debt, `over_limit` is set in the status, and new requests are refused until the window ends. For token profiles the observed overrun also quarantines the binding.

The bound is conservative in the other direction: because reservations are worst-case, a request can be refused while actual spend so far is below the limit.

**Streams and unknown usage.** A stream that ends without terminal usage, a client disconnect and a cancelled response are not settled from what was observed: the existing rule applies, which retains the hold as unknown exposure. Such a request counts as reserved, not consumed, until recovery or reconciliation. A budget therefore stays conservative when usage is unknown. [Quota administration](quotaplex-administration.md) describes reviewed reconciliation.

**Cached responses and routed attempts.** An answer served from the [response cache](reliability-and-routing.md#response-cache) takes the quota lease and settles it as one request with every money meter at zero: request-count budgets draw one, cost and token budgets draw nothing, and the record still lists the budgets it was admitted under. Each attempt of a routed model takes its own admission, so it draws from the caller's budgets like any request; an attempt whose connection was never established settles at zero and draws nothing, and a quota or budget denial is final unless the model sets `fallback.on_quota_denied: next_backend` (see [reliability and routing](reliability-and-routing.md#grants-quota-and-capacity-per-attempt)).

## Thresholds and status

When settled consumption reaches a configured threshold, QuotaPlex records a threshold event in the budget's counter for that window. The list is bounded by the threshold set (at most 8), resets with the window, and is read through the status route. There are **no outbound notifications**: no webhook, email or chat message is sent. A reader has to poll `status`.

`GET /api/v1/budgets/{id}/status` returns the current window's counters.

| Field | Who sees it |
| --- | --- |
| `limit`, `spent`, `reserved`, `remaining`, `window`, `over_limit`, `include_tool_calls` (set when the cost budget counts tool calls) | direct members and operators |
| `unknown_cost_requests`, `on_unknown_cost` (cost budgets), `thresholds`, `threshold_events`, `children` (each child's own counters), `notes` | operators |
| `top_spenders` (principals by ledger usage in the window) | admins |

A member is a caller who matches the budget's own members; reaching a budget only through a child does not make a caller a member of it. The ledger is written asynchronously, so `top_spenders` can lag the counters by moments. Anyone else gets `404`, the same answer as a missing budget. `spent` plus `reserved` plus `remaining` equals `limit` while the budget is under its limit. Amounts are exact decimal strings: whole numbers, or the cost in the currency at the money meter's scale.

### What a refusal reveals

When a request is refused because a budget is exhausted, the error is the protocol's own quota error (HTTP 429 with `Retry-After` until the window ends) with the quota code `QUOTA_BUDGET_EXHAUSTED`.

* A caller who matches the budget directly learns its display name, id and remaining allowance: `budget "Platform team" (platform) is exhausted; remaining 0 requests; the window resets in 3h12m`.
* A caller who reaches the exhausted budget only through a child learns only the child's name: `a budget above "Platform team" is exhausted`. The ancestor's name is not disclosed.
* A budget never refuses a non-member, so no one else sees one.

## Preview

`POST /api/v1/budgets/preview` (admin) answers which budgets a hypothetical request would draw from and why, without changing anything. The body names a subject (`principal`, `groups`, `agent`, `client`), an optional `access_point` (its `project` label applies) and a `plane`. It may carry a `draft` budget, which is validated exactly like a save and included in the answer, so a change is reviewed before it is applied with `PUT`. The subject is hypothetical: nothing in it is verified. With `plane: mcplex` a cost budget that sets `include_tool_calls` is listed as a draw (with `include_tool_calls: true`), and one that does not is listed under `skipped` with the reason.

```sh
aiplex budgets preview --group platform-eng --access-point ide
aiplex budgets preview --group platform-eng --plane mcplex
aiplex budgets preview --group platform-eng -f new-budget.yaml
```

## API

All routes are tenant-scoped; the tenant comes from the verified binding. Writes use `If-Match` revisions like other resources.

| Route | Access | Purpose |
| --- | --- | --- |
| `GET /api/v1/budgets` | admin, deployer | List budgets with revisions. |
| `GET /api/v1/budgets/{id}` | admin, deployer | One budget; the `ETag` is its revision. |
| `PUT /api/v1/budgets/{id}` | admin | Create (`If-Match: 0`) or replace. Validation errors name the field (`limit/currency`, `parent`, `window/kind`, and so on). |
| `DELETE /api/v1/budgets/{id}` | admin | Delete (`If-Match: <revision>`). `409 BUDGET_HAS_CHILDREN` while it has children. Counters stay until their windows end. |
| `POST /api/v1/budgets/preview` | admin | The dry run above. |
| `GET /api/v1/budgets/{id}/status` | any verified caller | Counters; scoped as described. |

Budget definitions are cached for 5 seconds per replica and invalidated at once on the replica that handled a write. A replica that cannot refresh keeps using its previous tree for at most 30 more seconds, then refuses requests (`503 QUOTA_BUDGETS_UNAVAILABLE`). Saved budgets that do not validate (for example a tree broken by concurrent edits) also refuse requests until fixed; the system does not enforce a partial tree.

The [overview](access-points.md#the-single-pane) adds a `budgets` block to a subject's and an access point's detail: the applicable budgets, whether each matches directly or through a child, and their counters. Budgets that select by group apply per request from the token's groups, which the overview cannot know; it says so in its `note`. `counters` is `unavailable` when QuotaPlex is off, never zero.

## Usage ledger

A ledger record carries `budget_ids`: the budgets the attempt drew from (its own and every ancestor), set only for an attempt that was dispatched. `aiplex usage summary --by budget` and `GET /api/v1/usage/summary?group_by=budget` group by budget, and `--budget` / `budget_id` filter records. Only **managed** records have budgets: records reported through OTLP ingest (`source=reported`) never drew from a budget, so `--by budget` and `--budget` count managed records only, the default managed/reported source split does not apply to them, and the summary says so in its notes. A record that drew from several budgets is counted in each group, so budget groups overlap by design (a child's spend is also its parent's) and do not sum to the total. Requests refused by a budget have no attempt and appear in the group with an empty budget.

The ledger and the counters agree on requests and on cost when both come from the quota policy. They are separate records with separate price sources: the ledger's `cost` for a record is QuotaPlex's settled money when the policy priced the attempt, and otherwise an estimate from the ledger's own price resources (basis `estimated`). A request admitted under `allow_and_flag` is unknown cost to the budget but can still carry an estimate in the ledger. The end-to-end test reconciles counters and ledger for a sequence that includes an unpriced request.

## Command line

```sh
aiplex budgets create platform --name "Platform team" --unit cost --currency USD --amount 250.00 --window month --group platform-eng
aiplex budgets create platform-chat --parent platform --unit tokens --amount 20000000 --window week --access-point ide
aiplex budgets list
aiplex budgets get platform
aiplex budgets update platform --amount 400.00 --add group=data-eng
aiplex budgets update platform --include-tool-calls          # count tool calls too; =false turns it off
aiplex budgets status platform
aiplex budgets preview --group platform-eng --access-point ide
aiplex budgets delete platform --yes
aiplex who user <principal-id>          # includes the budgets that apply
```

`--group` names are qualified with the identity provider you are signed in to, as for `access-points create --allow-groups`. `delete` needs `--yes` without a terminal. The generated CLI reference lists every flag (`aiplex budgets --help`).

## Replicas and restart

Counters are QuotaPlex counters, so they have QuotaPlex's durability and sharing:

* With the shared durable Firestore quota store (required in production), counters are shared by every API replica and survive restarts. Admission is a store transaction, so replicas cannot jointly overshoot.
* With the process-local store (explicit development mode only), counters live in one process and are lost on restart. Under `aiplex dev` the resource store is in memory too, so a restart also forgets the budgets.
* The 5 second definition cache means another replica may admit under the previous definition of a budget for that long after an edit.

Budget draws on the Firestore store have been exercised against the Firestore emulator only (the `Firestore emulator` CI job runs `TestQuotaEmulatorBudget*` in `internal/registry`). With two engines on two clients of one database, for each unit (requests, tokens, cost): concurrent admit-and-settle never admitted more than the limit, a settlement replayed on the second engine or issued by both at once charged once, aborted holds returned exactly once, each threshold was recorded once, requests of unknown cost under `allow_and_flag` were counted as unknown and added no spend, and a new client over the same database saw identical balances and could settle holds taken before the restart. The tests use at most 4 concurrent callers on one counter: the emulator's transactions on a single hot document back off for seconds under contention, and a few dozen simultaneous callers exhausted the SDK's retries (a refusal, never an overshoot). Managed Firestore, its latency and contention limits, restore and failover behaviour of the store are not validated here; see the [production qualification](production-qualification.md) and [QuotaPlex requirements and limits](quotaplex-prerequisites.md).

## Local development

`aiplex dev --quota` writes a quota policy for the seeded mock surfaces and the tool connection, with `request-count` bindings, and starts the API with QuotaPlex on. Budgets that count requests then work with no hand-written policy; tokens and cost do not, because a token or cost budget needs operator-attested provider limits and price books that a local default would have to invent. The end-to-end tests start the same stack with a placeholder tariff (0.001 USD per request on the primary mock surface, 0.002 USD per call on the seeded tool connection) to exercise cost budgets, including `include_tool_calls`; `aiplex dev` has no flag for it. Run [examples/budgets](../examples/budgets/README.md) for the loop, and see its `quota-cost.json` for a hand-written policy with a placeholder per-request tariff. `make examples-test` runs the example.

## Not implemented, and not validated

* **Notifications.** Threshold events are recorded and readable; nothing is sent.
* **Rolling windows.** Refused. Use calendar or tumbling windows.
* **Cross-currency roll-up** and currency conversion. A tree's cost budgets share one currency.
* **Forecasting** and burn-rate projection.
* **Agent cost, and tool tokens.** Cost and token budgets never count agent calls; token budgets never count tool calls. Cost budgets count tool calls only with `include_tool_calls`.
* **Tool cost beyond a per-call price.** A tool call is priced from its binding meters (`requests`, `tool_calls`). Response bytes and duration are not metered, so a price cannot depend on them.
* **Refusal codes on the tool wire.** A budget refusal of a tool call carries its message and HTTP status but not its `QUOTA_BUDGET_*` code (see [above](#tool-calls-in-a-cost-budget)).
* **Budgets without QuotaPlex.** Direct provider, Envoy and other paths that bypass managed dispatch are not metered, as for all of QuotaPlex.
* **Per-principal counters inside a budget.** A budget has one shared counter. "Top spenders" come from the usage ledger, which is a separate record.
* **Live validation.** Budgets are exercised with the in-memory store, mock upstreams and placeholder prices. Counters against managed Firestore (only the emulator was exercised, see above), real provider usage and live billing have not been compared with budgets. Tool calls in a cost budget are exercised against the mock tool server and a placeholder per-call price only.
