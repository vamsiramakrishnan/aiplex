# aiplex (Python)

A small typed Python client for the AIPlex HTTP API. It mirrors the Go SDK in `sdk/aiplex/` for the operations listed below. It has one runtime dependency, `httpx`, and a synchronous API.

This package is **not published to PyPI**. Install it from the repository path. Its tests have only been run against the local development stack (`aiplex dev`) and the in-process fake server used by the unit tests.

## Install

```bash
pip install ./sdk/python            # from the repository root
pip install -e './sdk/python[test]' # editable, with pytest for the test suite
```

Requires Python 3.10 or newer.

## Example

```python
from aiplex import Client, UsageFilter

# Reads AIPLEX_URL and AIPLEX_TOKEN when the arguments are omitted.
with Client("http://127.0.0.1:19190", "<token>") as client:
    print(client.whoami().identity)

    for record in client.iter_usage_records(UsageFilter(access_point_id="local"), limit=100):
        # Unknown counters and costs are None, never 0.
        cost = record.cost.amount if record.cost else None  # decimal.Decimal or None
        print(record.id, record.model_id, record.status, cost)

    summary = client.usage_summary(group_by=["model_id"])
    print(summary.records_scanned, summary.truncated)
```

Errors raise `aiplex.AiplexError` with `status_code`, `code`, `message`, `details`, `request_id` and `trace_id`. The token is never included in an error message, `repr()` or `str()`.

## Covered operations

- `whoami()`
- Access points: `list_access_points()`, `get_access_point(id)`, `put_access_point(spec, revision)` (revision 0 creates), `delete_access_point(id, revision)`, `connect_access_point(id, client=None)`
- Budgets: `get_budget_status(id)` (counters in the current window; operators and members only); administration (operators only): `list_budgets()`, `get_budget(id)`, `put_budget(spec, revision)` (revision 0 creates), `delete_budget(id, revision)`, `preview_budget(request)` (dry run, nothing stored)
- Usage: `list_usage_records(filter, limit=, cursor=)`, `iter_usage_records(filter, limit=)`, `get_usage_record(id)`, `usage_summary(filter, group_by=, bucket=)`, `list_prices()`, `get_price(id)`, `put_price(price, revision)`, `delete_price(id, revision)`, `add_usage_feedback(record_id, score, label=, comment=)`, `list_usage_feedback(record_id)`, `get_usage_config()`
- Model surfaces: `list_model_surfaces()`, `get_model_surface(id)`, `put_model_surface(id, spec, revision)`, `delete_model_surface(id, revision)`, `get_model_surface_capacity(id)` (advisory, per replica)
- Tool connections: `list_tool_connections()`, `get_tool_connection(id)`, `put_tool_connection(id, spec, revision)`, `delete_tool_connection(id, revision)`
- Hook policies: `list_hook_policies()`, `get_hook_policy(id)`, `put_hook_policy(policy, revision)`, `delete_hook_policy(id, revision)`, `test_hook_policy(id, sample)`
- Overview: `list_overview_access_points()` / `iter_overview_access_points()`, `get_overview_access_point(id)`, `list_overview_subjects(kind)` / `iter_overview_subjects(kind)`, `get_overview_subject(kind, id)`

## Not covered

- Model and tool calls themselves (chat completions, MCP, A2A). Point an OpenAI or Anthropic SDK at the access point URL instead.
- Usage record ingestion, quota administration and QuotaPlex plans, agent and client registrations, identity providers, skills, runs, tool connection auth catalogue and the read-modify-write helpers of the Go SDK (`UpdateToolConnection`), model operation catalogues, batches, dashboards, deployment plans, model routes and providers, catalog, instances, and the Go SDK's other administrative resources.
- Asynchronous client, automatic retries, streaming responses.
- Writes take typed specifications (`Budget`, `ModelSurfaceSpec`, `ToolConnectionSpec`, `HookPolicy`) and send `If-Match` with the revision you pass; a stale revision raises `AiplexError` with `is_revision_conflict` true. The client checks only argument shapes: the server validates specifications (hook regular expressions, sidecar endpoints, secret references). Specification types keep the JSON they were read from in `raw` and write it back, so a read-modify-write does not drop fields this version does not model.
- Secrets are references: `auth.secret_ref`, `client_secret_ref` and `auth_secret_ref` name tenant secrets; no method accepts or returns a secret value.
- The administration methods are covered by the in-process fake server in `tests/test_admin.py` and were exercised once by hand against `aiplex dev --quota`. They have not been run against any other deployment.
- Typed models cover the documented fields. Every model keeps the full JSON in `raw`, so newer server fields are available there.

## Tests

From the repository root:

```bash
make sdk-test
```

The unit tests run against an in-process fake HTTP server. `tests/test_integration.py` runs only when `AIPLEX_URL` and `AIPLEX_TOKEN` are both set, and is skipped otherwise.
