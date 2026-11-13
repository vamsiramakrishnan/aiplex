# @aiplex/sdk (TypeScript)

A small typed TypeScript client for the AIPlex HTTP API. It mirrors the Go SDK in `sdk/aiplex/` for the operations listed below. It has no runtime dependencies, uses the global `fetch`, and targets Node 20 or newer as ESM.

This package is **not published to npm**. Install it from the repository path. Its tests have only been run against the local development stack (`aiplex dev`) and the local fake server used by the unit tests.

## Install

Build the package once, then install it from the repository path:

```bash
cd sdk/typescript && npm ci && npm run build   # produces dist/
npm install /path/to/repo/sdk/typescript        # in your own project
```

## Example

```ts
import { AiplexClient, type UsageFilter } from "@aiplex/sdk";

// Reads AIPLEX_URL and AIPLEX_TOKEN when the options are omitted.
const client = new AiplexClient({ baseUrl: "http://127.0.0.1:19190", token: "<token>" });

const who = await client.whoami();
console.log(who.identity?.principalId);

const filter: UsageFilter = { accessPointId: "local" };
for await (const record of client.iterUsageRecords(filter, { limit: 100 })) {
  // Unknown counters and costs are null, never 0. Money is a string.
  console.log(record.id, record.modelId, record.status, record.cost?.amountDecimal ?? null);
}

const summary = await client.usageSummary(undefined, { groupBy: ["model_id"] });
console.log(summary.recordsScanned, summary.truncated);
```

Errors throw `AiplexError` with `statusCode`, `code`, `message`, `details`, `requestId` and `traceId`. The token is never included in an error message, `String(client)` or `JSON.stringify(client)`.

## Covered operations

- `whoami()`
- Access points: `listAccessPoints()`, `getAccessPoint(id)`, `putAccessPoint(spec, revision)` (revision 0 creates), `deleteAccessPoint(id, revision)`, `connectAccessPoint(id, client?)`
- Budgets: `listBudgets()`, `getBudget(id)`, `putBudget(spec, revision)` (revision 0 creates), `deleteBudget(id, revision)`, `previewBudget({subject, accessPoint?, plane?, draft?})` (dry run, changes nothing), `getBudgetStatus(id)` (counters in the current window; operators and members). Definitions, writes and previews are for operators and administrators; budgets need the QuotaPlex module, otherwise a write fails with 409 `QUOTAPLEX_REQUIRED`.
- Usage: `listUsageRecords(filter, options)`, `iterUsageRecords(filter, options)`, `getUsageRecord(id)`, `addUsageFeedback(recordId, {score, label?, comment?})`, `listUsageFeedback(recordId)` (newest first), `getUsageConfig()` (trace link template, feedback and comment switches, allowed tag keys), `usageSummary(filter, options)`, `listPrices()`, `getPrice(id)`, `putPrice(price, revision)`, `deletePrice(id, revision)`
- Model surfaces: `listModelSurfaces()`, `getModelSurface(id)`, `putModelSurface(id, spec, revision)`, `deleteModelSurface(id, revision)`, `getModelSurfaceCapacity(id)` (last provider limit observation on the replica that answered, and the capacity policy state; advisory, administrators only)
- Tool connections: `listToolConnections()`, `getToolConnection(id)`, `putToolConnection(id, spec, revision)`, `deleteToolConnection(id, revision)`
- Hook policies: `listHookPolicies()`, `getHookPolicy(id)`, `putHookPolicy(policy, revision)`, `deleteHookPolicy(id, revision)`, `testHookPolicy(id, {texts, phase?, plane?})` (dry run; the sample is not stored)

Writes take the revision you read (0 creates) and send it as `If-Match`; a stale revision throws `AiplexError` with status 409 and code `REVISION_CONFLICT`. Deletes need a positive revision. The specifications you write (`BudgetSpec`, `ModelSurfaceSpec`, `ToolConnectionSpec`, `HookPolicySpec`) use the stored JSON field names (snake_case), so reading a record's `spec`, changing it and writing it back keeps every field, including ones this SDK version does not know. Credentials in them are references (`secret_ref`, `auth_secret_ref`, `identity_ref`), never values. An absent optional number in a result (a limit window's `remaining`, a policy's `headroomFraction`) is `undefined`, never `0`. The SDK does not validate these specifications beyond their required fields; the server does.
- Overview: `listOverviewAccessPoints()` / `iterOverviewAccessPoints()`, `getOverviewAccessPoint(id)`, `listOverviewSubjects(kind)` / `iterOverviewSubjects(kind)`, `getOverviewSubject(kind, id)`

## Not covered

- Model and tool calls themselves (chat completions, MCP, A2A). Point an OpenAI or Anthropic SDK at the access point URL instead.
- Usage record ingestion, quota administration and QuotaPlex plans, agent and client registrations, identity providers, skills, runs, dashboards, deployment plans, model routes and providers, model batches, tool connection auth catalogs and tool rule dry runs, the model operation catalog, catalog, instances, and the Go SDK's other administrative resources.
- Budget writes could not be run against `aiplex dev` (it does not enable QuotaPlex); they are covered by the unit tests against a canned server only. Model surface, tool connection and hook policy writes, the capacity read, usage configuration and feedback listing were run against `aiplex dev`.
- Automatic retries and streaming responses.
- Typed models cover the documented fields. Every model keeps the full JSON in `raw`, so newer server fields are available there.

## Tests

From the repository root:

```bash
make sdk-test
```

Or in `sdk/typescript`: `npm ci && npm test`. `test/integration.test.ts` runs only when `AIPLEX_URL` and `AIPLEX_TOKEN` are both set, and is skipped otherwise.
