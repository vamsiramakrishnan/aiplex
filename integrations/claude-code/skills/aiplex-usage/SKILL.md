---
name: aiplex-usage
description: Answer "what did I spend", "which models did I use" and "why was my request denied" from the caller's own AIPlex usage ledger. Use when the user asks about AIPlex cost, tokens, errors or denials.
---

# Answer usage and denial questions

Prefer the `aiplex` MCP tools (this plugin starts them with `aiplex mcp serve`). They are read-only, use the user's own token and only return the caller's own usage. If the tools are not available, the CLI gives the same data.

| Question | MCP tool | CLI equivalent |
| --- | --- | --- |
| Who am I to AIPlex? | `whoami` | `aiplex whoami --server` |
| Which access points can I use? | `list_access_points` | `aiplex access-points list` |
| Which models does one offer? | `list_models` | `aiplex connect <access-point>` |
| Totals of requests, tokens, cost | `usage_summary` | `aiplex usage summary --by model --since 7d` |
| Individual requests | `usage_records` | `aiplex usage records --since 24h` |
| Spent and remaining in a budget | (CLI only) | `aiplex budgets status <budget-id>` |

## What did I spend

1. Call `usage_summary` with `group_by` `["model"]` and the window the user asked for (`since` such as `7d`).
2. Report requests, tokens and cost per group.
3. Be exact about unknowns. A missing token count or cost is **unknown**, never zero. If a group says it includes bounded usage (lower bound, ambiguous or partial), say the figure is not final. If a model has no price, its cost is unknown; that is a price-configuration gap, not free usage. Costs in different currencies or bases are not added together.
4. If the summary says it was truncated, say the totals cover only the newest records and narrow the window.

## Why was I denied or why did it fail

1. Call `usage_records` with `status` set to `denied` (policy or quota refused the call) or `error` (the upstream or gateway failed), a recent `since`, and `limit` 10.
2. Read `error_class` and the access point, model and caller kind on each record. The error class names the reason; quote it instead of guessing.
3. For a policy denial, `whoami` shows the roles and scopes the caller actually has. Compare them with what the model or tool needs; a user token can reach an access point yet still lack the grant for a model.
4. For a quota decision, the CLI can show what admission would do: `aiplex quota preview --help` lists its arguments, and `aiplex quota attempt <id>` reads one attempt.
5. A refusal by a budget (an error class such as `budget_exceeded` or `budget_exhausted`) is answered by `aiplex budgets status <budget-id>`: it shows spent, reserved and remaining for the current window. Members see their own budgets; operators see all of them.
6. Operators can also review `aiplex list denials`.

## Feedback

Feedback on a record is a write. Run `aiplex usage feedback <record-id> --score <-1..1>` only when the user asks to rate a specific request, and show the record ID first. It is append-only: a later entry replaces the current one, and the history stays.

If nothing matches, widen `since`, check the `access_point` filter, and confirm with `whoami` that the token is the identity the user expects. A different token means a different caller and different usage.

Never print or ask for the access token. The tools never need it from you.
