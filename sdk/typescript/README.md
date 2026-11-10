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
- Usage: `listUsageRecords(filter, options)`, `iterUsageRecords(filter, options)`, `getUsageRecord(id)`, `usageSummary(filter, options)`, `listPrices()`, `getPrice(id)`, `putPrice(price, revision)`, `deletePrice(id, revision)`
- Model surfaces: `listModelSurfaces()`, `getModelSurface(id)`
- Tool connections: `listToolConnections()`, `getToolConnection(id)`
- Overview: `listOverviewAccessPoints()` / `iterOverviewAccessPoints()`, `getOverviewAccessPoint(id)`, `listOverviewSubjects(kind)` / `iterOverviewSubjects(kind)`, `getOverviewSubject(kind, id)`

## Not covered

- Model and tool calls themselves (chat completions, MCP, A2A). Point an OpenAI or Anthropic SDK at the access point URL instead.
- Usage record ingestion, quota administration and QuotaPlex plans, agent and client registrations, identity providers, skills, runs, dashboards, deployment plans, model routes and providers, catalog, instances, and the Go SDK's other administrative resources.
- Automatic retries and streaming responses.
- Typed models cover the documented fields. Every model keeps the full JSON in `raw`, so newer server fields are available there.

## Tests

From the repository root:

```bash
make sdk-test
```

Or in `sdk/typescript`: `npm ci && npm test`. `test/integration.test.ts` runs only when `AIPLEX_URL` and `AIPLEX_TOKEN` are both set, and is skipped otherwise.
