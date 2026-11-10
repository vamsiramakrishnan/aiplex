import util from "node:util";
import { describe, it, expect, beforeEach, afterEach } from "vitest";
import { AiplexClient, AiplexError } from "../src/index.js";
import { startFakeApi, type FakeApi } from "./helpers/fakeServer.js";

const SAMPLE_WHOAMI = {
  configuration_revision: 2,
  identity: {
    principal_id:
      "principal-020d277e494066b68e8581cd77984817925485a3bdf7d9e0c995f871a626963c",
    subject: "developer",
    email: "developer@localhost",
    display_name: "Local developer",
    groups: ["aiplex-local-admins"],
    domain: "",
    provider: "http://127.0.0.1:35789",
    pool_id: "aiplex-local",
    is_workforce: true,
  },
  roles: ["admin"],
  scopes: ["a2a:*", "aiplex:*", "llm:*", "mcp:*", "skill:*"],
  group_scopes: ["a2a:*", "aiplex:*", "llm:*", "mcp:*", "skill:*"],
  matched_bindings: ["local-admin"],
};

const SAMPLE_AP = {
  tenant_id: "development",
  kind: "access-point",
  id: "local",
  revision: 1,
  spec: {
    caller_kinds: ["user", "agent", "client"],
    display_name: "Local development",
    id: "local",
    models: [
      { name: "mock-gpt", native_model: "mock-gpt", surface_ref: "local-models" },
    ],
    subjects: {},
    tools: [{ connection_ref: "local-tools" }],
  },
};

const SAMPLE_CONNECT = {
  access_point_id: "local",
  client: "claude-code",
  base_urls: {
    anthropic: "http://127.0.0.1:19190/ap/local",
    openai: "http://127.0.0.1:19190/ap/local/v1",
  },
  endpoints: [],
  models: [{ name: "mock-gpt", protocols: ["openai-chat-completions"] }],
  mcp_url: "http://127.0.0.1:19190/ap/local/mcp",
  mcp_catalog_url: "http://127.0.0.1:19190/ap/local/v0.1/servers",
  auth: { scheme: "bearer" },
  snippets: [{ title: "snippet", language: "bash", content: "echo hi" }],
  notes: ["note 1"],
};

const SAMPLE_USAGE_RECORD = {
  id: "ul_f22b1f5ea41015a35d9f95ab8ec99b06",
  tenant_id: "development",
  started_at: "2026-11-10T10:23:00.712680814Z",
  finished_at: "2026-11-10T10:23:00.718759131Z",
  latency_ms: 6,
  plane: "llmplex",
  provider: "openai-compatible",
  resource_id: "local-models",
  operation: "compat.chat-completions",
  model_id: "mock-gpt",
  model_version: "mock-gpt",
  principal_id:
    "principal-020d277e494066b68e8581cd77984817925485a3bdf7d9e0c995f871a626963c",
  principal_display: "developer@localhost",
  access_point_id: "local",
  caller_kind: "user",
  request_id: "req-14",
  trace_id: "b8f1efc0972f3351b4518609d0dbdd32",
  span_id: "2563ef6ddc4ac930",
  streaming: false,
  status: "ok",
  http_status: 200,
  source: "managed",
  requests: 1,
  usage: {
    input_tokens: 1,
    output_tokens: 9,
    cache_read_tokens: null,
    cache_write_tokens: null,
    reasoning_tokens: null,
    total_tokens: 10,
  },
  cost: {
    basis: "estimated",
    currency: "USD",
    amount: "6",
    scale: 6,
    amount_decimal: "0.000006",
  },
};

const SAMPLE_PRICE = {
  tenant_id: "development",
  kind: "model-price",
  id: "mock-gpt",
  revision: 1,
  spec: {
    currency: "USD",
    id: "mock-gpt",
    meters: {
      input_tokens: { denominator: 100, numerator: 15 },
      output_tokens: { denominator: 100, numerator: 60 },
    },
    model_id: "mock-gpt",
    scale: 6,
  },
};

const SAMPLE_SURFACE = {
  tenant_id: "development",
  kind: "model-surface",
  id: "local-models",
  revision: 1,
  spec: {
    auth: { type: "none" },
    surface: {
      id: "local-models",
      models: ["mock-gpt"],
      operations: [],
      origin: "http://127.0.0.1:37417",
      protocol: "openai-chat",
      provider: {
        id: "openai-compatible",
        stream_usage: "inject",
        usage_semantics: "openai",
      },
    },
  },
};

const SAMPLE_CONNECTION = {
  tenant_id: "development",
  kind: "tool-connection",
  id: "local-tools",
  revision: 1,
  spec: {
    auth: { type: "none" },
    execution: {
      allowed_tools: ["echo", "add", "reset_counter"],
      credential_mode: "connection-auth",
      endpoint: "http://127.0.0.1:35367",
      id: "local-tools",
      mode: "stateless-json",
      protocol_version: "2025-06-18",
    },
    id: "local-tools",
    resource_url: "http://127.0.0.1:35367",
  },
};

describe("AiplexClient", () => {
  let api: FakeApi;
  let savedUrl: string | undefined;
  let savedToken: string | undefined;

  beforeEach(async () => {
    savedUrl = process.env["AIPLEX_URL"];
    savedToken = process.env["AIPLEX_TOKEN"];
    delete process.env["AIPLEX_URL"];
    delete process.env["AIPLEX_TOKEN"];
    api = await startFakeApi();
  });

  afterEach(async () => {
    if (savedUrl === undefined) {
      delete process.env["AIPLEX_URL"];
    } else {
      process.env["AIPLEX_URL"] = savedUrl;
    }
    if (savedToken === undefined) {
      delete process.env["AIPLEX_TOKEN"];
    } else {
      process.env["AIPLEX_TOKEN"] = savedToken;
    }
    await api.close();
  });

  it("configures from explicit options, env fallback, strips trailing slashes, and rejects missing config", async () => {
    expect(() => new AiplexClient()).toThrow(TypeError);
    expect(() => new AiplexClient({ baseUrl: api.url })).toThrow(TypeError);
    expect(() => new AiplexClient({ token: "tok" })).toThrow(TypeError);

    process.env["AIPLEX_URL"] = `${api.url}///`;
    process.env["AIPLEX_TOKEN"] = "test-token-123";
    api.route("GET", "/api/v1/iam/whoami", { json: SAMPLE_WHOAMI });

    const client = new AiplexClient();
    const who = await client.whoami();
    expect(who.identity?.principalId).toBe(
      "principal-020d277e494066b68e8581cd77984817925485a3bdf7d9e0c995f871a626963c",
    );
    expect(api.requests).toHaveLength(1);
    expect(api.requests[0]!.path).toBe("/api/v1/iam/whoami");
    expect(api.requests[0]!.headers["authorization"]).toBe("Bearer test-token-123");
    expect(api.requests[0]!.headers["user-agent"]).toBe("aiplex-sdk-ts/0.1.0");
  });

  it("redacts token in client representations, server errors, and transport errors", async () => {
    const secret = "super-secret-token-xyz";
    const client = new AiplexClient({ baseUrl: api.url, token: secret });

    expect(String(client)).not.toContain(secret);
    expect(String(client)).toContain("token=<redacted>");
    expect(JSON.stringify(client)).not.toContain(secret);
    expect(util.inspect(client)).not.toContain(secret);

    api.route("GET", "/api/v1/iam/whoami", {
      status: 401,
      json: {
        code: "UNAUTHENTICATED",
        message: `bad token ${secret} provided`,
        details: { echoed: secret },
      },
    });

    await expect(client.whoami()).rejects.toSatisfy((err: unknown) => {
      expect(err).toBeInstanceOf(AiplexError);
      const ae = err as AiplexError;
      expect(ae.message).not.toContain(secret);
      expect(ae.message).toContain("[redacted]");
      expect(String(ae)).not.toContain(secret);
      expect(JSON.stringify(ae)).not.toContain(secret);
      expect(ae.details?.["echoed"]).toBe("[redacted]");
      return true;
    });

    const failingFetch: typeof fetch = () => {
      const e = new Error(`connect failed with ${secret}`);
      e.name = "ConnectError";
      return Promise.reject(e);
    };
    const badClient = new AiplexClient({
      baseUrl: api.url,
      token: secret,
      fetch: failingFetch,
    });

    await expect(badClient.whoami()).rejects.toSatisfy((err: unknown) => {
      expect(err).toBeInstanceOf(AiplexError);
      const ae = err as AiplexError;
      expect(ae.code).toBe("transport_error");
      expect(ae.statusCode).toBeNull();
      expect(ae.message).toBe(
        "aiplex: transport_error: request failed: ConnectError",
      );
      expect(ae.message).not.toContain(secret);
      expect(String(ae)).not.toContain(secret);
      expect(JSON.stringify(ae)).not.toContain(secret);
      return true;
    });
  });

  it("maps error envelopes, non-JSON 502, and 409 revision conflicts", async () => {
    const client = new AiplexClient({ baseUrl: api.url, token: "t" });

    api.route("GET", "/api/v1/access-points/missing", {
      status: 404,
      json: {
        code: "NOT_FOUND",
        message: "resource not found",
        request_id: "req-33",
        trace_id: "5adfc2f17def1fdc9fd7bfd077aa553b",
        details: { field: "id" },
      },
    });

    await expect(client.getAccessPoint("missing")).rejects.toSatisfy(
      (err: unknown) => {
        expect(err).toBeInstanceOf(AiplexError);
        const ae = err as AiplexError;
        expect(ae.name).toBe("AiplexError");
        expect(ae.statusCode).toBe(404);
        expect(ae.code).toBe("NOT_FOUND");
        expect(ae.requestId).toBe("req-33");
        expect(ae.traceId).toBe("5adfc2f17def1fdc9fd7bfd077aa553b");
        expect(ae.details).toEqual({ field: "id" });
        expect(ae.field).toBe("id");
        expect(ae.isRevisionConflict).toBe(false);
        return true;
      },
    );

    api.route("GET", "/api/v1/access-points/bad-gw", {
      status: 502,
      text: "<html>Bad Gateway</html>",
    });
    await expect(client.getAccessPoint("bad-gw")).rejects.toSatisfy(
      (err: unknown) => {
        const ae = err as AiplexError;
        expect(ae.statusCode).toBe(502);
        expect(ae.code).toBe("unknown");
        return true;
      },
    );

    api.route("DELETE", "/api/v1/access-points/local", {
      status: 409,
      json: { code: "CONFLICT", message: "revision mismatch" },
    });
    await expect(client.deleteAccessPoint("local", 1)).rejects.toSatisfy(
      (err: unknown) => {
        const ae = err as AiplexError;
        expect(ae.statusCode).toBe(409);
        expect(ae.isRevisionConflict).toBe(true);
        return true;
      },
    );
  });

  it("handles access point list, get, put, delete, and connect", async () => {
    const client = new AiplexClient({ baseUrl: api.url, token: "t" });

    api.route("GET", "/api/v1/access-points/", { json: [SAMPLE_AP] });
    const list = await client.listAccessPoints();
    expect(list).toHaveLength(1);
    expect(list[0]!.id).toBe("local");
    expect(list[0]!.spec.displayName).toBe("Local development");

    api.route("GET", "/api/v1/access-points/local", { json: SAMPLE_AP });
    const got = await client.getAccessPoint("local");
    expect(got.revision).toBe(1);

    api.route(
      "PUT",
      "/api/v1/access-points/local",
      { json: SAMPLE_AP },
      { json: { ...SAMPLE_AP, revision: 8 } },
    );
    await client.putAccessPoint(got.spec, 0);
    const putReq0 = api.requests[api.requests.length - 1]!;
    expect(putReq0.headers["if-match"]).toBe("0");
    const body0 = putReq0.body as Record<string, unknown>;
    expect(body0["id"]).toBe("local");
    expect(body0["display_name"]).toBe("Local development");
    expect(body0["caller_kinds"]).toEqual(["user", "agent", "client"]);

    const updated = await client.putAccessPoint(got.spec, 7);
    expect(updated.revision).toBe(8);
    expect(api.requests[api.requests.length - 1]!.headers["if-match"]).toBe("7");

    await expect(client.deleteAccessPoint("local", 0)).rejects.toThrow(TypeError);
    const countBefore = api.requests.length;
    await expect(client.deleteAccessPoint("local", -1)).rejects.toThrow(TypeError);
    expect(api.requests.length).toBe(countBefore);

    api.route("DELETE", "/api/v1/access-points/local", { status: 204 });
    await expect(client.deleteAccessPoint("local", 3)).resolves.toBeUndefined();
    expect(api.requests[api.requests.length - 1]!.headers["if-match"]).toBe("3");

    api.route(
      "GET",
      "/api/v1/overview/access-points/local/connect",
      { json: SAMPLE_CONNECT },
      { json: SAMPLE_CONNECT },
    );
    await client.connectAccessPoint("local");
    expect(api.requests[api.requests.length - 1]!.rawSearch).toBe("");

    const conn = await client.connectAccessPoint("local", "claude-code");
    expect(api.requests[api.requests.length - 1]!.query["client"]).toBe(
      "claude-code",
    );
    expect(conn.baseUrls["openai"]).toBe("http://127.0.0.1:19190/ap/local/v1");
  });

  it("handles usage records, filters, unknown counters/costs, and exact decimal money", async () => {
    const client = new AiplexClient({ baseUrl: api.url, token: "t" });

    await expect(client.listUsageRecords({}, { limit: 0 })).rejects.toThrow(
      TypeError,
    );
    await expect(client.listUsageRecords({}, { limit: 1001 })).rejects.toThrow(
      TypeError,
    );
    expect(api.requests).toHaveLength(0);

    api.route("GET", "/api/v1/usage/records", {
      json: { records: [SAMPLE_USAGE_RECORD], next_cursor: "c1" },
    });

    const page = await client.listUsageRecords(
      {
        plane: "llmplex",
        modelId: "mock-gpt",
        batch: false,
        since: "2026-11-10T00:00:00Z",
      },
      { limit: 10, cursor: "c0" },
    );

    const req = api.requests[0]!;
    expect(req.query).toEqual({
      plane: "llmplex",
      model_id: "mock-gpt",
      batch: "false",
      since: "2026-11-10T00:00:00Z",
      limit: "10",
      cursor: "c0",
    });

    const rec = page.records[0]!;
    expect(rec.usage?.inputTokens).toBe(1);
    expect(rec.usage?.cacheReadTokens).toBeNull();
    expect(rec.cost?.amountDecimal).toBe("0.000006");
    expect(rec.cost?.amountMinor).toBe("6");
    expect(rec.cost?.scale).toBe(6);

    api.route("GET", "/api/v1/usage/records/unknown-rec", {
      json: {
        id: "unknown-rec",
        usage: {
          input_tokens: null,
          output_tokens: null,
          cache_read_tokens: null,
          cache_write_tokens: null,
          reasoning_tokens: null,
          total_tokens: null,
        },
        cost: { basis: "unknown" },
      },
    });
    const unk = await client.getUsageRecord("unknown-rec");
    expect(unk.usage?.inputTokens).toBeNull();
    expect(unk.usage?.outputTokens).toBeNull();
    expect(unk.usage?.totalTokens).toBeNull();
    expect(unk.cost?.basis).toBe("unknown");
    expect(unk.cost?.currency).toBeNull();
    expect(unk.cost?.amountMinor).toBeNull();
    expect(unk.cost?.scale).toBeNull();
    expect(unk.cost?.amountDecimal).toBeNull();

    api.route("GET", "/api/v1/usage/records/neg-cost", {
      json: {
        id: "neg-cost",
        cost: {
          basis: "settled",
          currency: "USD",
          amount: "-1500",
          scale: 2,
        },
      },
    });
    const neg = await client.getUsageRecord("neg-cost");
    expect(typeof neg.cost?.amountDecimal).toBe("string");
    expect(neg.cost?.amountDecimal).toBe("-15.00");
  });

  it("handles usage summary and validates groupBy and bucket", async () => {
    const client = new AiplexClient({ baseUrl: api.url, token: "t" });

    await expect(
      client.usageSummary(undefined, { groupBy: ["bogus"] }),
    ).rejects.toThrow(TypeError);
    await expect(
      client.usageSummary(undefined, { groupBy: ["plane", "plane"] }),
    ).rejects.toThrow(TypeError);
    await expect(
      client.usageSummary(undefined, {
        bucket: "week" as unknown as "hour",
      }),
    ).rejects.toThrow(TypeError);
    expect(api.requests).toHaveLength(0);

    api.route("GET", "/api/v1/usage/summary", {
      json: {
        group_by: ["model_id", "plane"],
        groups: [
          {
            key: { model_id: "mock-gpt", plane: "llmplex" },
            records: 1,
            requests: 1,
            errors: 0,
            denied: 0,
            unknown_status: 0,
            tokens: { input_tokens: 1, output_tokens: 9 },
            tokens_unknown: { cache_read_tokens: 1 },
            unknown_usage_records: 0,
            cost: [
              {
                currency: "USD",
                basis: "estimated",
                amount: "6",
                scale: 6,
                records: 1,
                amount_decimal: "0.000006",
              },
            ],
            unknown_cost_records: 0,
          },
        ],
        records_scanned: 1,
        truncated: false,
      },
    });

    const summary = await client.usageSummary(undefined, {
      groupBy: ["model_id", "plane"],
      bucket: "hour",
    });
    expect(api.requests[0]!.query["group_by"]).toBe("model_id,plane");
    expect(api.requests[0]!.query["bucket"]).toBe("hour");
    expect(summary.groups[0]!.tokens["input_tokens"]).toBe(1);
    expect(summary.groups[0]!.tokens["total_tokens"]).toBeUndefined();
  });

  it("handles prices, model surfaces, tool connections, overview, and decode errors", async () => {
    const client = new AiplexClient({ baseUrl: api.url, token: "t" });

    api.route("GET", "/api/v1/usage/prices/", { json: [SAMPLE_PRICE] });
    const prices = await client.listPrices();
    expect(prices).toHaveLength(1);
    expect(prices[0]!.price.meters["input_tokens"]?.numerator).toBe(15);

    api.route("GET", "/api/v1/usage/prices/null-rate", {
      json: {
        ...SAMPLE_PRICE,
        id: "null-rate",
        spec: {
          ...SAMPLE_PRICE.spec,
          id: "null-rate",
          meters: { input_tokens: { numerator: null, denominator: 100 } },
        },
      },
    });
    const nullRate = await client.getPrice("null-rate");
    expect(nullRate.price.meters["input_tokens"]?.numerator).toBeNull();

    api.route("PUT", "/api/v1/usage/prices/mock-gpt", { json: SAMPLE_PRICE });
    await client.putPrice(prices[0]!.price, 2);
    const putPriceReq = api.requests[api.requests.length - 1]!;
    expect(putPriceReq.headers["if-match"]).toBe("2");
    expect((putPriceReq.body as Record<string, unknown>)["model_id"]).toBe(
      "mock-gpt",
    );
    expect((putPriceReq.body as Record<string, unknown>)["spec"]).toBeUndefined();

    await expect(client.deletePrice("mock-gpt", 0)).rejects.toThrow(TypeError);
    api.route("DELETE", "/api/v1/usage/prices/mock-gpt", { status: 204 });
    await client.deletePrice("mock-gpt", 2);
    expect(api.requests[api.requests.length - 1]!.headers["if-match"]).toBe("2");

    api.route("GET", "/api/v1/llm/surfaces", { json: [SAMPLE_SURFACE] });
    api.route("GET", "/api/v1/llm/surfaces/local-models", {
      json: SAMPLE_SURFACE,
    });
    const surfaces = await client.listModelSurfaces();
    expect(surfaces[0]!.protocol).toBe("openai-chat");
    expect(surfaces[0]!.providerId).toBe("openai-compatible");
    expect(surfaces[0]!.models).toEqual(["mock-gpt"]);
    const surface = await client.getModelSurface("local-models");
    expect(surface.id).toBe("local-models");

    api.route("GET", "/api/v1/mcp/connections", { json: [SAMPLE_CONNECTION] });
    api.route("GET", "/api/v1/mcp/connections/local-tools", {
      json: SAMPLE_CONNECTION,
    });
    const conns = await client.listToolConnections();
    expect(conns[0]!.resourceUrl).toBe("http://127.0.0.1:35367");
    expect(conns[0]!.authType).toBe("none");
    expect(conns[0]!.allowedTools).toEqual(["echo", "add", "reset_counter"]);
    const conn = await client.getToolConnection("local-tools");
    expect(conn.id).toBe("local-tools");

    const beforeOverviewCount = api.requests.length;
    await expect(
      client.listOverviewSubjects("admin" as unknown as "user"),
    ).rejects.toThrow(TypeError);
    expect(api.requests.length).toBe(beforeOverviewCount);

    api.route("GET", "/api/v1/overview/subjects/user/u-1", {
      json: { kind: "user", id: "u-1" },
    });
    await client.getOverviewSubject("user", "u-1");
    expect(api.requests[api.requests.length - 1]!.path).toBe(
      "/api/v1/overview/subjects/user/u-1",
    );

    const spiffe = "spiffe://example.org/ns/default/sa/bot";
    api.route("GET", "/api/v1/overview/subjects/agent", {
      json: { kind: "agent", id: spiffe },
    });
    await client.getOverviewSubject("agent", spiffe, {
      since: "2026-11-09T00:00:00Z",
    });
    const spiffeReq = api.requests[api.requests.length - 1]!;
    expect(spiffeReq.path).toBe("/api/v1/overview/subjects/agent");
    expect(spiffeReq.query["id"]).toBe(spiffe);
    expect(spiffeReq.query["since"]).toBe("2026-11-09T00:00:00Z");

    api.route("GET", "/api/v1/overview/access-points", {
      json: {
        access_points: [
          {
            id: "local",
            display_name: "Local development",
            caller_kinds: ["user"],
            counts: { models: 4 },
            quota_coverage: { state: "not_required", detail: "none" },
          },
        ],
      },
    });
    const apPage = await client.listOverviewAccessPoints({
      limit: 5,
      cursor: "cur-1",
    });
    expect(apPage.accessPoints[0]!.quotaCoverageState).toBe("not_required");
    expect(api.requests[api.requests.length - 1]!.query).toEqual({
      limit: "5",
      cursor: "cur-1",
    });

    api.route("GET", "/api/v1/overview/access-points/local", {
      json: {
        access_point: { id: "local", display_name: "Local development" },
        revision: 1,
        since: "2026-11-09T00:00:00Z",
        usage: {
          available: true,
          truncated: false,
          total: { requests: 1, records: 1, tokens: {}, tokens_unknown: {}, cost: [] },
        },
      },
    });
    const apDetail = await client.getOverviewAccessPoint("local", {
      since: "2026-11-09T00:00:00Z",
    });
    expect(apDetail.id).toBe("local");
    expect(apDetail.usage?.requests).toBe(1);
    expect(api.requests[api.requests.length - 1]!.query["since"]).toBe(
      "2026-11-09T00:00:00Z",
    );

    api.route("GET", "/api/v1/usage/records/bad-shape", {
      json: { id: "bad-shape", usage: "not-an-object" },
    });
    await expect(client.getUsageRecord("bad-shape")).rejects.toSatisfy(
      (err: unknown) => {
        expect(err).toBeInstanceOf(AiplexError);
        expect((err as AiplexError).code).toBe("decode_error");
        return true;
      },
    );
  });
});
