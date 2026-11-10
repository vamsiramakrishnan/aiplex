import { afterEach, beforeEach, describe, expect, it } from "vitest";
import {
  AiplexClient,
  parseUsageRecord,
  parseUsageSummary,
} from "../src/index.js";
import { startFakeApi, type FakeApi } from "./helpers/fakeServer.js";

const REPORTED = {
  id: "ul_r",
  tenant_id: "t",
  plane: "llmplex",
  model_id: "claude-sonnet-4-5",
  status: "ok",
  source: "reported",
  harness: "claude-code",
  harness_version: "2.1.322",
  session_id: "s1",
  tags: { experiment: "arm-a" },
  reported_attributes: { request_id: "req_1" },
  hooks: { decision: "allow", results: [] },
  requests: 1,
  usage: {
    input_tokens: 130,
    output_tokens: null,
    cache_read_tokens: 30,
    cache_write_tokens: 0,
    reasoning_tokens: null,
    total_tokens: null,
  },
  cost: {
    basis: "reported",
    currency: "USD",
    amount: "4200",
    scale: 6,
    amount_decimal: "0.004200",
  },
  feedback: {
    id: "fb_1",
    record_id: "ul_r",
    by: "alice",
    created_at: "2026-11-10T12:05:00Z",
    score: -0.5,
    label: "wrong",
  },
};

describe("reported records, tags, feedback and source-aware summaries", () => {
  let api: FakeApi;
  beforeEach(async () => {
    api = await startFakeApi();
  });
  afterEach(async () => {
    await api.close();
  });

  it("parses reported fields and keeps unknown as null", () => {
    const r = parseUsageRecord(REPORTED);
    expect(r.source).toBe("reported");
    expect(r.harness).toBe("claude-code");
    expect(r.harnessVersion).toBe("2.1.322");
    expect(r.tags).toEqual({ experiment: "arm-a" });
    expect(r.reportedAttributes).toEqual({ request_id: "req_1" });
    expect(r.hooks).toEqual({ decision: "allow", results: [] });
    expect(r.cost?.basis).toBe("reported");
    expect(r.usage?.outputTokens ?? null).toBeNull();
    expect(r.usage?.cacheWriteTokens).toBe(0);
    expect(r.feedback?.score).toBe(-0.5);

    const managed = parseUsageRecord({ id: "ul_m", source: "managed" });
    expect(managed.harness).toBeNull();
    expect(managed.feedback).toBeNull();
    expect(managed.hooks).toBeNull();
    expect(managed.tags).toEqual({});
  });

  it("parses source split, exclusions and feedback totals without inventing zeros", () => {
    const s = parseUsageSummary({
      group_by: ["source"],
      source_split: true,
      excluded_correlated_reported: 2,
      notes: ["Managed and reported records are separate groups."],
      groups: [
        {
          key: { source: "managed" },
          records: 3,
          feedback: {
            records: 2,
            without_feedback: 1,
            positive: 1,
            negative: 0,
            neutral: 1,
            average_score: 0.5,
          },
        },
        {
          key: { source: "reported" },
          records: 1,
          feedback: { records: 0, without_feedback: 1, average_score: null },
        },
        { key: { source: "x" }, records: 1 },
      ],
    });
    expect(s.sourceSplit).toBe(true);
    expect(s.excludedCorrelatedReported).toBe(2);
    expect(s.notes).toHaveLength(1);
    expect(s.groups[0]!.feedback?.averageScore).toBe(0.5);
    expect(s.groups[1]!.feedback?.averageScore).toBeNull();
    expect(s.groups[1]!.feedback?.withoutFeedback).toBe(1);
    expect(s.groups[2]!.feedback).toBeNull();
    const empty = parseUsageSummary({ groups: [] });
    expect(empty.sourceSplit).toBeNull();
    expect(empty.excludedCorrelatedReported).toBeNull();
    expect(empty.notes).toEqual([]);
  });

  it("sends harness, hook decision, tag filters and summary options", async () => {
    const client = new AiplexClient({ baseUrl: api.url, token: "t" });
    api.route("GET", "/api/v1/usage/summary", { json: { groups: [] } });
    await client.usageSummary(
      { harness: "codex", hookDecision: "deny", tags: { experiment: "arm-a" } },
      {
        groupBy: ["tag:experiment", "harness", "hook_decision"],
        mergeSources: true,
        feedback: true,
      },
    );
    const q = api.requests[0]!.query;
    expect(q["harness"]).toBe("codex");
    expect(q["hook_decision"]).toBe("deny");
    expect(q["tag.experiment"]).toBe("arm-a");
    expect(q["group_by"]).toBe("tag:experiment,harness,hook_decision");
    expect(q["merge_sources"]).toBe("true");
    expect(q["feedback"]).toBe("true");
    await expect(
      client.usageSummary(undefined, { groupBy: ["tag:Bad Key"] }),
    ).rejects.toThrow(TypeError);
  });

  it("adds feedback and validates the score", async () => {
    const client = new AiplexClient({ baseUrl: api.url, token: "t" });
    api.route("POST", "/api/v1/usage/records/ul_r/feedback", {
      status: 201,
      json: {
        id: "fb_2",
        record_id: "ul_r",
        by: "alice",
        created_at: "2026-11-10T12:06:00Z",
        score: 1,
      },
    });
    const saved = await client.addUsageFeedback("ul_r", {
      score: 1,
      label: "helpful",
    });
    expect(saved.id).toBe("fb_2");
    expect(saved.label).toBeNull();
    expect(api.requests[0]!.body).toEqual({ score: 1, label: "helpful" });
    for (const score of [2, -1.5, Number.NaN]) {
      await expect(
        client.addUsageFeedback("ul_r", { score }),
      ).rejects.toThrow(TypeError);
    }
  });
});
