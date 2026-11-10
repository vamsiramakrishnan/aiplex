import { describe, it, expect, beforeEach, afterEach } from "vitest";
import { AiplexClient, parseUsageRecord } from "../src/index.js";
import { startFakeApi, type FakeApi } from "./helpers/fakeServer.js";

const MEMBER = {
  id: "team",
  unit: "cost",
  currency: "USD",
  limit: "250.000000",
  spent: "12.500000",
  reserved: "1.000000",
  remaining: "236.500000",
  window: { start: "2026-11-01T00:00:00Z", end: "2026-12-01T00:00:00Z" },
};

describe("budgets", () => {
  let api: FakeApi;
  let client: AiplexClient;

  beforeEach(async () => {
    api = await startFakeApi();
    client = new AiplexClient({ baseUrl: api.url, token: "test-token" });
  });

  afterEach(async () => {
    await api.close();
  });

  it("reads member and operator status views; operator fields are null, not zero", async () => {
    api.route("GET", "/api/v1/budgets/team/status", { json: MEMBER });
    api.route("GET", "/api/v1/budgets/org/status", {
      json: { ...MEMBER, unknown_cost_requests: 3, children: [{ id: "c" }] },
    });
    const member = await client.getBudgetStatus("team");
    expect(member.spent).toBe("12.500000");
    expect(member.remaining).toBe("236.500000");
    expect(member.windowEnd).toBe("2026-12-01T00:00:00Z");
    expect(member.unknownCostRequests).toBeNull();
    expect(member.children).toEqual([]);
    const op = await client.getBudgetStatus("org");
    expect(op.unknownCostRequests).toBe(3);
    expect(op.children).toEqual([{ id: "c" }]);
    await expect(client.getBudgetStatus("")).rejects.toThrow();
  });

  it("filters usage by budget and parses budget_ids", async () => {
    api.route("GET", "/api/v1/usage/records", { json: { records: [{ id: "ul_1", budget_ids: ["org", "team"] }] } });
    const page = await client.listUsageRecords({ budgetId: "team" });
    expect(page.records[0]?.budgetIds).toEqual(["org", "team"]);
    expect(api.requests.at(-1)?.query["budget_id"]).toBe("team");
    expect(parseUsageRecord({ id: "ul_2" }).budgetIds).toEqual([]);
  });
});
