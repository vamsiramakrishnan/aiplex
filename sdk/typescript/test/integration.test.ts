import { describe, it, expect } from "vitest";
import { AiplexClient } from "../src/index.js";

const hasLiveEnv = Boolean(
  process.env["AIPLEX_URL"] && process.env["AIPLEX_TOKEN"],
);

describe.skipIf(!hasLiveEnv)("Live development stack integration", () => {
  it("queries read-only endpoints against the live stack", async () => {
    const client = new AiplexClient();

    const who = await client.whoami();
    expect(who.identity?.principalId).toBeTruthy();

    const aps = await client.listAccessPoints();
    expect(Array.isArray(aps)).toBe(true);
    const hasLocal = aps.some((ap) => ap.id === "local");
    if (hasLocal) {
      const localAp = await client.getAccessPoint("local");
      expect(localAp.revision).toBeGreaterThanOrEqual(1);

      const conn = await client.connectAccessPoint("local", "claude-code");
      expect(conn.baseUrls["openai"]).toBeTruthy();

      const ovAp = await client.getOverviewAccessPoint("local");
      expect(ovAp.id).toBe("local");
    }

    const recPage = await client.listUsageRecords({}, { limit: 5 });
    for (const r of recPage.records) {
      expect(r.id.startsWith("ul_")).toBe(true);
    }

    const summary = await client.usageSummary(undefined, {
      groupBy: ["model_id"],
    });
    expect(typeof summary.recordsScanned).toBe("number");

    const prices = await client.listPrices();
    expect(Array.isArray(prices)).toBe(true);

    const surfaces = await client.listModelSurfaces();
    expect(Array.isArray(surfaces)).toBe(true);

    const connections = await client.listToolConnections();
    expect(Array.isArray(connections)).toBe(true);

    let count = 0;
    for await (const rec of client.iterUsageRecords(undefined, { limit: 2 })) {
      expect(rec.id.startsWith("ul_")).toBe(true);
      count++;
      if (count >= 3) {
        break;
      }
    }
  });
});
