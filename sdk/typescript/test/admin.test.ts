import { describe, it, expect, beforeEach, afterEach } from "vitest";
import {
  AiplexClient,
  AiplexError,
  type BudgetSpec,
  type HookPolicySpec,
  type ModelSurfaceSpec,
  type ToolConnectionSpec,
} from "../src/index.js";
import { startFakeApi, type FakeApi } from "./helpers/fakeServer.js";

const BUDGET: BudgetSpec = {
  id: "team",
  display_name: "Team",
  limit: { unit: "cost", currency: "USD", amount: "250.00" },
  window: { kind: "calendar", unit: "month", time_zone: "UTC" },
  members: { principals: ["alice"] },
  thresholds: [50, 100],
};
const BUDGET_RECORD = { tenant_id: "t", kind: "budget", id: "team", revision: 4, spec: BUDGET };

const SURFACE: ModelSurfaceSpec = {
  surface: {
    id: "oa",
    protocol: "openai",
    origin: "https://api.openai.com",
    models: ["gpt-x"],
    provider: { id: "openai" },
  },
  auth: { type: "bearer", secret_ref: "openai-key" },
};
const SURFACE_RECORD = { tenant_id: "t", kind: "model-surface", id: "oa", revision: 2, spec: SURFACE };

const TOOL: ToolConnectionSpec = {
  id: "github",
  resource_url: "https://mcp.example.com/mcp",
  auth: { type: "api_key", in: "header", name: "X-Key", secret_ref: "gh-key" },
  tools: { allow: ["read_*"], read_only: true },
};
const TOOL_RECORD = { tenant_id: "t", kind: "tool-connection", id: "github", revision: 5, spec: TOOL };

const HOOK: HookPolicySpec = {
  id: "pii",
  scope: { kind: "tenant" },
  hooks: [
    {
      id: "mask",
      type: "redact",
      phases: ["request"],
      redact: { rules: [{ id: "email", builtin: "email", action: "mask" }] },
    },
  ],
};
const HOOK_RECORD = { id: "pii", revision: 3, spec: HOOK };

function conflict(): { status: number; json: unknown } {
  return { status: 409, json: { code: "REVISION_CONFLICT", message: "stale revision" } };
}
function notFound(): { status: number; json: unknown } {
  return { status: 404, json: { code: "NOT_FOUND", message: "missing" } };
}

async function expectStatus(p: Promise<unknown>, status: number, code: string): Promise<void> {
  await expect(p).rejects.toSatisfy(
    (e: unknown) => e instanceof AiplexError && e.statusCode === status && e.code === code,
  );
}

describe("admin operations", () => {
  let api: FakeApi;
  let client: AiplexClient;
  const last = () => api.requests[api.requests.length - 1]!;

  beforeEach(async () => {
    api = await startFakeApi();
    client = new AiplexClient({ baseUrl: api.url, token: "test-token" });
  });
  afterEach(async () => {
    await api.close();
  });

  describe("budgets", () => {
    it("lists and gets budgets with revision and spec", async () => {
      api.route("GET", "/api/v1/budgets", { json: [BUDGET_RECORD] });
      api.route("GET", "/api/v1/budgets/team", { json: BUDGET_RECORD });
      const list = await client.listBudgets();
      expect(list).toHaveLength(1);
      expect(list[0]!.revision).toBe(4);
      expect(list[0]!.spec.limit.amount).toBe("250.00");
      const one = await client.getBudget("team");
      expect(one.id).toBe("team");
      expect(one.spec.window.hours).toBeUndefined();
      expect(one.spec.thresholds).toEqual([50, 100]);
      await expect(client.getBudget("")).rejects.toThrow(TypeError);
    });

    it("puts with If-Match and sends the spec as stored JSON", async () => {
      api.route("PUT", "/api/v1/budgets/team", { json: BUDGET_RECORD });
      const rec = await client.putBudget(BUDGET, 0);
      expect(last().headers["if-match"]).toBe("0");
      expect(last().body).toEqual(BUDGET);
      expect(rec.revision).toBe(4);
      await client.putBudget(BUDGET, 4);
      expect(last().headers["if-match"]).toBe("4");
      await expect(client.putBudget(BUDGET, -1)).rejects.toThrow(TypeError);
    });

    it("surfaces revision conflict and not found", async () => {
      api.route("PUT", "/api/v1/budgets/team", conflict());
      await expectStatus(client.putBudget(BUDGET, 1), 409, "REVISION_CONFLICT");
      api.route("GET", "/api/v1/budgets/nope", notFound());
      await expectStatus(client.getBudget("nope"), 404, "NOT_FOUND");
      api.route("DELETE", "/api/v1/budgets/team", conflict());
      await expectStatus(client.deleteBudget("team", 1), 409, "REVISION_CONFLICT");
    });

    it("deletes at a positive revision only", async () => {
      api.route("DELETE", "/api/v1/budgets/team", { status: 204 });
      await expect(client.deleteBudget("team", 4)).resolves.toBeUndefined();
      expect(last().headers["if-match"]).toBe("4");
      const before = api.requests.length;
      await expect(client.deleteBudget("team", 0)).rejects.toThrow(TypeError);
      expect(api.requests.length).toBe(before);
    });

    it("previews which budgets a request draws from", async () => {
      api.route("POST", "/api/v1/budgets/preview", {
        json: {
          hypothetical: true,
          plane: "llmplex",
          access_point: "local",
          draws: [
            {
              id: "team",
              direct: true,
              reasons: ["principal alice"],
              unit: "cost",
              currency: "USD",
              limit: "250.00",
              window: { kind: "calendar", unit: "month" },
              draft: true,
            },
          ],
          skipped: [{ id: "old", reason: "plane not counted" }],
          draft: { valid: false, field: "limit.amount", error: "bad" },
          notes: ["hypothetical"],
        },
      });
      const out = await client.previewBudget({
        subject: { principal: "alice", groups: [{ source: "idp", id: "eng" }] },
        accessPoint: "local",
        draft: BUDGET,
      });
      expect(last().body).toEqual({
        subject: { principal: "alice", groups: [{ source: "idp", id: "eng" }] },
        access_point: "local",
        draft: BUDGET,
      });
      expect(out.hypothetical).toBe(true);
      expect(out.draws[0]!.direct).toBe(true);
      expect(out.draws[0]!.draft).toBe(true);
      expect(out.draws[0]!.viaChild).toBeUndefined();
      expect(out.skipped[0]!.reason).toBe("plane not counted");
      expect(out.draft).toEqual({ valid: false, field: "limit.amount", error: "bad" });
    });
  });

  describe("null handling in specs", () => {
    it("turns null members into absent ones, keeps unknown fields, never 0", async () => {
      api.route("GET", "/api/v1/budgets/team", {
        json: {
          id: "team",
          revision: 1,
          spec: { ...BUDGET, window: { kind: "tumbling", hours: null }, thresholds: null, future_field: { a: 1 } },
        },
      });
      const b = await client.getBudget("team");
      expect(b.spec.window.hours).toBeUndefined();
      expect("hours" in b.spec.window).toBe(false);
      expect(b.spec.thresholds).toBeUndefined();
      expect((b.spec as Record<string, unknown>)["future_field"]).toEqual({ a: 1 });
      api.route("GET", "/api/v1/hooks/policies/pii", {
        json: { id: "pii", revision: 1, spec: { ...HOOK, hooks: [{ ...HOOK.hooks[0], timeout_ms: null }] } },
      });
      const h = await client.getHookPolicy("pii");
      expect(h.policy.hooks[0]!.timeout_ms).toBeUndefined();
    });
  });

  describe("model surfaces", () => {
    it("puts a surface with If-Match and returns the record", async () => {
      api.route("PUT", "/api/v1/llm/surfaces/oa", { json: SURFACE_RECORD });
      const rec = await client.putModelSurface("oa", SURFACE, 0);
      expect(last().headers["if-match"]).toBe("0");
      expect(last().body).toEqual(SURFACE);
      expect(rec.id).toBe("oa");
      expect(rec.revision).toBe(2);
      expect(rec.providerId).toBe("openai");
      await expect(client.putModelSurface("", SURFACE, 0)).rejects.toThrow(TypeError);
    });

    it("surfaces revision conflict and not found", async () => {
      api.route("PUT", "/api/v1/llm/surfaces/oa", conflict());
      await expectStatus(client.putModelSurface("oa", SURFACE, 9), 409, "REVISION_CONFLICT");
      api.route("DELETE", "/api/v1/llm/surfaces/oa", conflict());
      await expectStatus(client.deleteModelSurface("oa", 9), 409, "REVISION_CONFLICT");
      api.route("GET", "/api/v1/llm/surfaces/nope/capacity", notFound());
      await expectStatus(client.getModelSurfaceCapacity("nope"), 404, "NOT_FOUND");
    });

    it("deletes at a positive revision only", async () => {
      api.route("DELETE", "/api/v1/llm/surfaces/oa", { status: 204 });
      await expect(client.deleteModelSurface("oa", 2)).resolves.toBeUndefined();
      expect(last().headers["if-match"]).toBe("2");
      await expect(client.deleteModelSurface("oa", 0)).rejects.toThrow(TypeError);
    });

    it("reads capacity; unobserved limits and unknown windows stay undefined", async () => {
      api.route(
        "GET",
        "/api/v1/llm/surfaces/oa/capacity",
        {
          json: {
            surface_id: "oa",
            observed: true,
            scope: "replica-local last observation; advisory",
            limits: {
              provider: "openai",
              observed_at: "2026-11-13T10:00:00Z",
              http_status: 429,
              classification: "rate_limited",
              retry_after_ms: 1500,
              requests: { limit: 100, remaining: 0, reset_at: "2026-11-13T10:01:00Z" },
              tokens: { remaining: 5 },
            },
            capacity_policy: {
              enabled: true,
              policy: { cooldown_enabled: true, max_cooldown_seconds: 60 },
              cooldown: { phase: "cooling", until: "2026-11-13T10:01:00Z", retry_after_ms: 1500 },
            },
          },
        },
        {
          json: {
            surface_id: "oa",
            observed: false,
            scope: "replica-local last observation; advisory",
            capacity_policy: { enabled: false },
          },
        },
      );
      const cap = await client.getModelSurfaceCapacity("oa");
      expect(cap.observed).toBe(true);
      expect(cap.limits?.requests?.remaining).toBe(0);
      expect(cap.limits?.tokens?.limit).toBeUndefined();
      expect(cap.limits?.tokens?.remaining).toBe(5);
      expect(cap.limits?.inputTokens).toBeUndefined();
      expect(cap.limits?.retryAfterMs).toBe(1500);
      expect(cap.capacityPolicy.enabled).toBe(true);
      expect(cap.capacityPolicy.policy?.cooldownEnabled).toBe(true);
      expect(cap.capacityPolicy.policy?.headroomFraction).toBeUndefined();
      expect(cap.capacityPolicy.cooldown?.phase).toBe("cooling");
      const idle = await client.getModelSurfaceCapacity("oa");
      expect(idle.observed).toBe(false);
      expect(idle.limits).toBeUndefined();
      expect(idle.capacityPolicy.policy).toBeUndefined();
      expect(idle.capacityPolicy.cooldown).toBeUndefined();
    });
  });

  describe("tool connections", () => {
    it("puts a tool connection with If-Match and returns the record", async () => {
      api.route("PUT", "/api/v1/mcp/connections/github", { json: TOOL_RECORD });
      const rec = await client.putToolConnection("github", TOOL, 0);
      expect(last().headers["if-match"]).toBe("0");
      expect(last().body).toEqual(TOOL);
      expect(rec.revision).toBe(5);
      expect(rec.authType).toBe("api_key");
      await expect(client.putToolConnection("github", TOOL, -2)).rejects.toThrow(TypeError);
    });

    it("rejects a spec id that differs from the path id before sending", async () => {
      const before = api.requests.length;
      await expect(client.putToolConnection("other", TOOL, 0)).rejects.toThrow(TypeError);
      expect(api.requests.length).toBe(before);
    });

    it("surfaces revision conflict and not found", async () => {
      api.route("PUT", "/api/v1/mcp/connections/github", conflict());
      await expectStatus(client.putToolConnection("github", TOOL, 1), 409, "REVISION_CONFLICT");
      api.route("DELETE", "/api/v1/mcp/connections/github", conflict(), notFound());
      await expectStatus(client.deleteToolConnection("github", 1), 409, "REVISION_CONFLICT");
      await expectStatus(client.deleteToolConnection("github", 1), 404, "NOT_FOUND");
    });

    it("deletes at a positive revision only", async () => {
      api.route("DELETE", "/api/v1/mcp/connections/github", { status: 204 });
      await expect(client.deleteToolConnection("github", 5)).resolves.toBeUndefined();
      expect(last().headers["if-match"]).toBe("5");
      await expect(client.deleteToolConnection("github", 0)).rejects.toThrow(TypeError);
    });
  });

  describe("hook policies", () => {
    it("lists and gets policies", async () => {
      api.route("GET", "/api/v1/hooks/policies/", { json: [HOOK_RECORD] });
      api.route("GET", "/api/v1/hooks/policies/pii", { json: HOOK_RECORD });
      const list = await client.listHookPolicies();
      expect(list[0]!.revision).toBe(3);
      expect(list[0]!.policy.hooks[0]!.type).toBe("redact");
      const one = await client.getHookPolicy("pii");
      expect(one.id).toBe("pii");
      expect(one.policy.hooks[0]!.timeout_ms).toBeUndefined();
    });

    it("puts with If-Match; the policy id names the path", async () => {
      api.route("PUT", "/api/v1/hooks/policies/pii", { json: HOOK_RECORD });
      const rec = await client.putHookPolicy(HOOK, 3);
      expect(last().headers["if-match"]).toBe("3");
      expect(last().body).toEqual(HOOK);
      expect(rec.revision).toBe(3);
    });

    it("surfaces revision conflict and not found", async () => {
      api.route("PUT", "/api/v1/hooks/policies/pii", conflict());
      await expectStatus(client.putHookPolicy(HOOK, 1), 409, "REVISION_CONFLICT");
      api.route("GET", "/api/v1/hooks/policies/nope", notFound());
      await expectStatus(client.getHookPolicy("nope"), 404, "NOT_FOUND");
      api.route("DELETE", "/api/v1/hooks/policies/pii", conflict());
      await expectStatus(client.deleteHookPolicy("pii", 1), 409, "REVISION_CONFLICT");
      api.route("POST", "/api/v1/hooks/policies/nope/test", notFound());
      await expectStatus(client.testHookPolicy("nope", { texts: ["a"] }), 404, "NOT_FOUND");
    });

    it("deletes at a positive revision only", async () => {
      api.route("DELETE", "/api/v1/hooks/policies/pii", { status: 204 });
      await expect(client.deleteHookPolicy("pii", 3)).resolves.toBeUndefined();
      expect(last().headers["if-match"]).toBe("3");
      await expect(client.deleteHookPolicy("pii", 0)).rejects.toThrow(TypeError);
    });

    it("dry-runs a policy on text", async () => {
      api.route("POST", "/api/v1/hooks/policies/pii/test", {
        json: {
          policy: "pii",
          phase: "request",
          decision: "transform",
          texts: ["mail [email]"],
          results: [
            {
              id: "mask",
              type: "redact",
              phase: "request",
              decision: "transform",
              rules: [{ id: "email", count: 1 }],
              duration_ms: 2,
            },
          ],
          uninspected_parts: 0,
        },
      });
      const out = await client.testHookPolicy("pii", { texts: ["mail a@b.co"], plane: "mcplex" });
      expect(last().body).toEqual({ texts: ["mail a@b.co"], plane: "mcplex" });
      expect(out.decision).toBe("transform");
      expect(out.texts).toEqual(["mail [email]"]);
      expect(out.code).toBeUndefined();
      expect(out.results[0]!.rules[0]).toEqual({ id: "email", count: 1 });
      expect(out.results[0]!.durationMs).toBe(2);
      expect(out.uninspectedParts).toBe(0);
      await expect(client.testHookPolicy("pii", { texts: [] })).rejects.toThrow(TypeError);
    });

    it("keeps a refusal's code and message and does not invent texts", async () => {
      api.route("POST", "/api/v1/hooks/policies/pii/test", {
        json: {
          policy: "pii",
          phase: "request",
          decision: "deny",
          code: "HOOK_DENIED",
          message: "no",
          results: [],
          uninspected_parts: 1,
        },
      });
      const out = await client.testHookPolicy("pii", { texts: ["x"] });
      expect(out.decision).toBe("deny");
      expect(out.code).toBe("HOOK_DENIED");
      expect(out.texts).toBeUndefined();
      expect(out.uninspectedParts).toBe(1);
    });
  });

  describe("usage configuration and feedback", () => {
    it("reads the usage configuration", async () => {
      api.route("GET", "/api/v1/usage/config", {
        json: {
          trace_url_template: "https://t/{trace_id}",
          feedback: true,
          feedback_comments: false,
          tag_keys_allowed: null,
        },
      });
      const cfg = await client.getUsageConfig();
      expect(cfg.traceUrlTemplate).toBe("https://t/{trace_id}");
      expect(cfg.feedback).toBe(true);
      expect(cfg.feedbackComments).toBe(false);
      expect(cfg.tagKeysAllowed).toEqual([]);
    });

    it("fails closed when a flag is missing", async () => {
      api.route("GET", "/api/v1/usage/config", { json: { trace_url_template: "" } });
      await expect(client.getUsageConfig()).rejects.toSatisfy(
        (e: unknown) => e instanceof AiplexError && e.code === "decode_error",
      );
    });

    it("lists feedback newest first", async () => {
      api.route("GET", "/api/v1/usage/records/r1/feedback", {
        json: {
          feedback: [
            { id: "f2", record_id: "r1", by: "alice", created_at: "2026-11-13T10:00:00Z", score: -1, label: "bad" },
            { id: "f1", record_id: "r1", score: 0.5 },
          ],
        },
      });
      const out = await client.listUsageFeedback("r1");
      expect(out.map((f) => f.id)).toEqual(["f2", "f1"]);
      expect(out[0]!.score).toBe(-1);
      expect(out[1]!.comment).toBeNull();
      await expect(client.listUsageFeedback("")).rejects.toThrow(TypeError);
    });

    it("treats an empty history as an empty list and a missing record as not found", async () => {
      api.route("GET", "/api/v1/usage/records/r2/feedback", { json: { feedback: null } });
      expect(await client.listUsageFeedback("r2")).toEqual([]);
      api.route("GET", "/api/v1/usage/records/r3/feedback", { json: { feedback: [] } });
      expect(await client.listUsageFeedback("r3")).toEqual([]);
      api.route("GET", "/api/v1/usage/records/r4/feedback", { json: { record_id: "r4" } });
      await expect(client.listUsageFeedback("r4")).rejects.toSatisfy(
        (e: unknown) => e instanceof AiplexError && e.code === "decode_error",
      );
      api.route("GET", "/api/v1/usage/records/nope/feedback", notFound());
      await expectStatus(client.listUsageFeedback("nope"), 404, "NOT_FOUND");
    });
  });
});
