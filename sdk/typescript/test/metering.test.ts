import { afterEach, beforeEach, describe, it, expect } from "vitest";
import { startFakeApi, type FakeApi } from "./helpers/fakeServer.js";
import {
  AiplexClient,
  parseCost,
  parseModelPrice,
  parseUsageCounters,
  parseUsageRecord,
  toWireModelPrice,
} from "../src/index.js";

describe("metering fields", () => {
  let api: FakeApi;
  beforeEach(async () => {
    api = await startFakeApi();
  });
  afterEach(async () => {
    await api.close();
  });

  it("sends the cost_basis filter", async () => {
    const client = new AiplexClient({ baseUrl: api.url, token: "t" });
    api.route("GET", "/api/v1/usage/summary", { json: { groups: [] } });
    await client.usageSummary({ costBasis: "reported" });
    expect(api.requests[0]!.query["cost_basis"]).toBe("reported");
  });

  it("keeps cost exact, price id and backfill time as strings, and unknown as null", () => {
    const c = parseCost({
      basis: "estimated",
      currency: "USD",
      amount: "10",
      scale: 6,
      exact: "37/4",
      price_id: "p1",
      backfilled_at: "2026-11-10T00:00:00Z",
    });
    expect(c.exact).toBe("37/4");
    expect(c.priceId).toBe("p1");
    expect(c.backfilledAt).toBe("2026-11-10T00:00:00Z");
    expect(c.amountMinor).toBe("10");
    expect(c.amountDecimal).toBe("0.000010");
    const u = parseCost({ basis: "unknown" });
    expect([u.exact, u.priceId, u.backfilledAt, u.amountMinor, u.amountDecimal]).toEqual([null, null, null, null, null]);
    expect(parseCost({ basis: "reported", currency: "USD", amount: "12346", scale: 6 }).exact).toBeNull();
  });

  it("reads service tier, server tools and the new counters; absent stays null or empty", () => {
    const r = parseUsageRecord({
      id: "ul_1",
      service_tier: "flex",
      server_tools: { web_search: 2 },
      usage: { input_tokens: 5, cache_write_1h_tokens: 4, tool_use_prompt_tokens: 9 },
    });
    expect(r.serviceTier).toBe("flex");
    expect(r.serverTools).toEqual({ web_search: 2 });
    expect(r.usage?.cacheWrite1hTokens).toBe(4);
    expect(r.usage?.toolUsePromptTokens).toBe(9);
    expect(r.usage?.outputTokens).toBeNull();
    const bare = parseUsageRecord({ id: "ul_2" });
    expect(bare.serviceTier).toBeNull();
    expect(bare.serverTools).toEqual({});
    expect(parseUsageCounters({ input_tokens: 1 }).cacheWrite1hTokens).toBeNull();
    // Every cache-write TTL the ledger records is read; absent ones stay null.
    const ttl = parseUsageCounters({
      cache_write_tokens: 30,
      cache_write_5m_tokens: 10,
      cache_write_30m_tokens: 0,
      cache_write_12h_tokens: 20,
    });
    expect([ttl.cacheWrite5mTokens, ttl.cacheWrite30mTokens, ttl.cacheWrite12hTokens]).toEqual([10, 0, 20]);
    expect(ttl.cacheWrite1hTokens).toBeNull();
    expect(() => parseUsageRecord({ id: "x", server_tools: { web_search: 1.5 } })).toThrow();
  });

  it("round-trips price tiers and service tier", () => {
    const wire = {
      id: "p",
      model_id: "m",
      currency: "USD",
      scale: 6,
      service_tier: "priority",
      meters: { input_tokens: { numerator: 1, denominator: 4 } },
      tiers: [{ above_input_tokens: 100000, meters: { input_tokens: { numerator: 1, denominator: 2 } } }],
    };
    const price = parseModelPrice(wire);
    expect(price.serviceTier).toBe("priority");
    expect(price.tiers?.[0].aboveInputTokens).toBe(100000);
    expect(toWireModelPrice(price)).toEqual(wire);
    const plain = toWireModelPrice(parseModelPrice({ id: "q", meters: {} }));
    expect(plain).not.toHaveProperty("tiers");
    expect(plain).not.toHaveProperty("service_tier");
  });

  it("reads tool response bytes and duration; zero is 0, absent and null are null", () => {
    const present = parseUsageCounters({ tool_response_bytes: 2048, tool_duration_milliseconds: 350 });
    expect([present.toolResponseBytes, present.toolDurationMilliseconds]).toEqual([2048, 350]);
    const zero = parseUsageCounters({ tool_response_bytes: 0, tool_duration_milliseconds: 0 });
    expect([zero.toolResponseBytes, zero.toolDurationMilliseconds]).toEqual([0, 0]);
    const absent = parseUsageCounters({ input_tokens: 1 });
    expect([absent.toolResponseBytes, absent.toolDurationMilliseconds]).toEqual([null, null]);
    const nulled = parseUsageCounters({ tool_response_bytes: null, tool_duration_milliseconds: null });
    expect([nulled.toolResponseBytes, nulled.toolDurationMilliseconds]).toEqual([null, null]);
    const rec = parseUsageRecord({ id: "ul_3", usage: { tool_response_bytes: 7, tool_duration_milliseconds: 0 } });
    expect([rec.usage?.toolResponseBytes, rec.usage?.toolDurationMilliseconds]).toEqual([7, 0]);
    expect(rec.usage?.raw["tool_response_bytes"]).toBe(7);
  });
});
