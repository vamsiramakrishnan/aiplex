import { describe, expect, it } from "vitest";
import {
  parseAccessPointModel,
  parseUsageRecord,
  parseUsageSummaryGroup,
  toWireAccessPointSpec,
} from "../src/index.js";

describe("routing fields", () => {
  it("round-trips a model with backends, routing and cache", () => {
    const wire = {
      name: "gpt",
      backends: [
        { surface_ref: "openai", native_model: "gpt-4o-mini" },
        { surface_ref: "azure", native_model: "gpt-4o-mini", weight: 0, priority: 1 },
      ],
      routing: { strategy: "priority", retry: {} },
      cache: { ttl: "10m" },
    };
    const m = parseAccessPointModel(wire);
    expect(m.surfaceRef).toBeNull();
    expect(m.backends?.[1]).toEqual({ surfaceRef: "azure", nativeModel: "gpt-4o-mini", weight: 0, priority: 1 });
    expect(m.cache?.ttl).toBe("10m");
    const spec = toWireAccessPointSpec({ id: "ap", callerKinds: ["user"], models: [m] } as never);
    expect((spec["models"] as unknown[])[0]).toEqual(wire);
  });

  it("parses attempts, backend and a cache hit", () => {
    const r = parseUsageRecord({
      id: "ul_1",
      source: "managed",
      usage: { input_tokens: 0, output_tokens: 0 },
      cost: { basis: "cached", amount: "0", scale: 0, amount_decimal: "0" },
      attempts: [
        { surface_ref: "a", native_model: "m", outcome: "unavailable", upstream_status: 503, retry_after_ms: 1000, duration_ms: 4, breaker_state: "closed", reason: "priority" },
        { surface_ref: "b", native_model: "m", outcome: "cache_hit", duration_ms: 1 },
      ],
      backend: { surface_ref: "b", native_model: "m" },
      routing_strategy: "priority",
      cache: { status: "hit", scope: "principal" },
      cached_response_usage: { input_tokens: 12, output_tokens: 30 },
    });
    expect(r.attempts.map((a) => a.outcome)).toEqual(["unavailable", "cache_hit"]);
    expect(r.attempts[0]?.retryAfterMs).toBe(1000);
    expect(r.backend?.surfaceRef).toBe("b");
    expect(r.cache?.status).toBe("hit");
    expect(r.cost?.basis).toBe("cached");
    expect(r.cachedResponseUsage?.inputTokens).toBe(12);
  });

  it("leaves routing fields empty on an unrouted record", () => {
    const r = parseUsageRecord({ id: "ul_2", source: "managed", cost: { basis: "unknown" } });
    expect(r.attempts).toEqual([]);
    expect(r.backend).toBeNull();
    expect(r.cache).toBeNull();
    expect(r.cachedResponseUsage).toBeNull();
  });

  it("counts cached records in a summary group", () => {
    const g = parseUsageSummaryGroup({ key: {}, records: 2, cached_response_records: 1 });
    expect(g.cachedResponseRecords).toBe(1);
  });
});
