import { AiplexError, redactRecord, redactText } from "./errors.js";
import {
  type AccessPointRecord,
  type AccessPointSpec,
  type ConnectConfig,
  type Feedback,
  type ModelPrice,
  type ModelPriceRecord,
  type ModelSurface,
  type OverviewAccessPointDetail,
  type OverviewAccessPointPage,
  type OverviewAccessPointSummary,
  type OverviewSubject,
  type OverviewSubjectDetail,
  type OverviewSubjectPage,
  type ToolConnection,
  type UsageRecord,
  type UsageRecordPage,
  type UsageSummary,
  type Whoami,
  parseAccessPointRecord,
  parseConnectConfig,
  parseList,
  parseModelPriceRecord,
  parseModelSurface,
  parseOverviewAccessPointDetail,
  parseOverviewAccessPointPage,
  parseOverviewSubjectDetail,
  parseOverviewSubjectPage,
  parseToolConnection,
  parseUsageRecord,
  parseFeedback,
  parseUsageRecordPage,
  parseUsageSummary,
  parseWhoami,
  toWireAccessPointSpec,
  toWireModelPrice,
  parseBudgetStatus,
  type BudgetStatus,
} from "./models.js";
import {
  type BudgetPreview,
  type BudgetPreviewInput,
  type BudgetRecord,
  type BudgetSpec,
  type HookPolicyRecord,
  type HookPolicySpec,
  type HookPolicyTestInput,
  type HookPolicyTestResult,
  type ModelSurfaceSpec,
  type SurfaceCapacity,
  type ToolConnectionSpec,
  type UsageConfig,
  parseBudgetPreview,
  parseBudgetRecord,
  parseHookPolicyRecord,
  parseHookPolicyTestResult,
  parseSurfaceCapacity,
  parseUsageConfig,
  toWireBudgetPreview,
  toWireHookPolicyTest,
} from "./admin.js";

const USER_AGENT = "aiplex-sdk-ts/0.1.0";
const DEFAULT_TIMEOUT_MS = 30_000;

const VALID_SUBJECT_KINDS = new Set<string>(["agent", "user", "client"]);

const VALID_GROUP_BY = new Set<string>([
  "plane",
  "provider",
  "model_id",
  "tool_name",
  "actor_id",
  "principal_id",
  "client_id",
  "access_point_id",
  "caller_kind",
  "budget",
  "status",
  "source",
  "error_class",
  "usage_bound",
  "batch",
  "hook_decision",
  "harness",
]);

const TAG_GROUP = /^tag:[a-z][a-z0-9_.-]{0,31}$/;

const VALID_BUCKETS = new Set<string>(["hour", "day"]);

/**
 * Allowed caller subject kinds in overview endpoints.
 */
export type SubjectKind = "agent" | "user" | "client";

/**
 * Filter parameters for querying usage ledger records and summaries.
 */
export interface UsageFilter {
  plane?: string;
  provider?: string;
  modelId?: string;
  toolName?: string;
  actorId?: string;
  principalId?: string;
  clientId?: string;
  accessPointId?: string;
  callerKind?: string;
  /** Records that drew from this tenant budget. */
  budgetId?: string;
  status?: string;
  source?: string;
  errorClass?: string;
  usageBound?: string;
  batch?: boolean;
  /** Reported records from one harness (claude-code, codex, gemini-cli). */
  harness?: string;
  /** allow, deny, transform, error, skipped, or none. */
  hookDecision?: string;
  /** settled, estimated, reported, cached or unknown. */
  costBasis?: string;
  /** Records carrying every tag pair, sent as tag.<key>=<value>. */
  tags?: Record<string, string>;
  since?: string;
  until?: string;
}

/**
 * Configuration options for {@link AiplexClient}.
 */
export interface AiplexClientOptions {
  baseUrl?: string;
  token?: string;
  timeoutMs?: number;
  fetch?: typeof fetch;
}

function validateId(id: string, name = "id"): void {
  if (typeof id !== "string" || id.trim() === "") {
    throw new TypeError(`${name} must be a non-empty string`);
  }
}

function validateLimit(limit: number | undefined): void {
  if (limit === undefined) {
    return;
  }
  if (
    typeof limit !== "number" ||
    !Number.isInteger(limit) ||
    limit < 1 ||
    limit > 1000
  ) {
    throw new TypeError("limit must be an integer between 1 and 1000");
  }
}

function validatePutRevision(revision: number): void {
  if (
    typeof revision !== "number" ||
    !Number.isInteger(revision) ||
    revision < 0
  ) {
    throw new TypeError("revision must be a non-negative integer");
  }
}

function validateDeleteRevision(revision: number): void {
  if (
    typeof revision !== "number" ||
    !Number.isInteger(revision) ||
    revision <= 0
  ) {
    throw new TypeError("revision for delete must be a positive integer");
  }
}

function validateSubjectKind(kind: string): void {
  if (typeof kind !== "string" || !VALID_SUBJECT_KINDS.has(kind)) {
    throw new TypeError("kind must be one of 'agent', 'user', or 'client'");
  }
}

function applyUsageFilter(
  params: URLSearchParams,
  filter: UsageFilter | undefined,
): void {
  if (!filter) {
    return;
  }
  const stringPairs: Array<[string, string | undefined]> = [
    ["plane", filter.plane],
    ["provider", filter.provider],
    ["model_id", filter.modelId],
    ["tool_name", filter.toolName],
    ["actor_id", filter.actorId],
    ["principal_id", filter.principalId],
    ["client_id", filter.clientId],
    ["access_point_id", filter.accessPointId],
    ["caller_kind", filter.callerKind],
    ["budget_id", filter.budgetId],
    ["status", filter.status],
    ["source", filter.source],
    ["error_class", filter.errorClass],
    ["usage_bound", filter.usageBound],
    ["harness", filter.harness],
    ["hook_decision", filter.hookDecision],
    ["cost_basis", filter.costBasis],
    ["since", filter.since],
    ["until", filter.until],
  ];
  for (const [key, val] of stringPairs) {
    if (val !== undefined && val !== null && val !== "") {
      params.set(key, val);
    }
  }
  if (filter.batch !== undefined && filter.batch !== null) {
    params.set("batch", filter.batch ? "true" : "false");
  }
  for (const [key, val] of Object.entries(filter.tags ?? {})) {
    params.set(`tag.${key}`, val);
  }
}

/**
 * HTTP client for the AIPlex control-plane API.
 */
export class AiplexClient {
  readonly #baseUrl: string;
  readonly #token: string;
  readonly #timeoutMs: number;
  readonly #fetch: typeof fetch;

  constructor(options?: AiplexClientOptions) {
    const rawUrl = options?.baseUrl ?? process.env["AIPLEX_URL"] ?? "";
    const trimmedUrl = typeof rawUrl === "string" ? rawUrl.trim().replace(/\/+$/, "") : "";
    if (!trimmedUrl) {
      throw new TypeError(
        "AIPlex baseUrl is required (pass options.baseUrl or set AIPLEX_URL)",
      );
    }

    const rawToken = options?.token ?? process.env["AIPLEX_TOKEN"] ?? "";
    const token = typeof rawToken === "string" ? rawToken.trim() : "";
    if (!token) {
      throw new TypeError(
        "AIPlex token is required (pass options.token or set AIPLEX_TOKEN)",
      );
    }

    const timeoutMs = options?.timeoutMs ?? DEFAULT_TIMEOUT_MS;
    if (
      typeof timeoutMs !== "number" ||
      !Number.isFinite(timeoutMs) ||
      timeoutMs <= 0
    ) {
      throw new TypeError("timeoutMs must be a positive number");
    }

    const fetchImpl = options?.fetch ?? globalThis.fetch;
    if (typeof fetchImpl !== "function") {
      throw new TypeError("fetch implementation must be a function");
    }

    this.#baseUrl = trimmedUrl;
    this.#token = token;
    this.#timeoutMs = timeoutMs;
    this.#fetch = fetchImpl;
  }

  /**
   * Returns a safe string representation with the bearer token redacted.
   */
  public toString(): string {
    return `AiplexClient(baseUrl=${this.#baseUrl}, token=<redacted>)`;
  }

  /**
   * Returns a JSON-serializable summary with the bearer token redacted.
   */
  public toJSON(): { baseUrl: string; token: string } {
    return {
      baseUrl: this.#baseUrl,
      token: "[redacted]",
    };
  }

  /**
   * Custom Node.js inspect hook that redacts the bearer token.
   */
  public [Symbol.for("nodejs.util.inspect.custom")](): string {
    return this.toString();
  }

  async #request(
    method: string,
    path: string,
    options?: {
      query?: URLSearchParams;
      body?: unknown;
      headers?: Record<string, string>;
      expectNoContent?: boolean;
    },
  ): Promise<unknown> {
    const queryStr = options?.query ? options.query.toString() : "";
    const url =
      queryStr.length > 0
        ? `${this.#baseUrl}${path}?${queryStr}`
        : `${this.#baseUrl}${path}`;

    const headers: Record<string, string> = {
      Authorization: `Bearer ${this.#token}`,
      Accept: "application/json",
      "User-Agent": USER_AGENT,
      ...options?.headers,
    };

    let bodyString: string | undefined;
    if (options?.body !== undefined) {
      headers["Content-Type"] = "application/json";
      bodyString = JSON.stringify(options.body);
    }

    let response: Response;
    try {
      response = await this.#fetch(url, {
        method,
        headers,
        body: bodyString,
        signal: AbortSignal.timeout(this.#timeoutMs),
      });
    } catch (err: unknown) {
      let errName = "Error";
      if (err !== null && typeof err === "object" && "name" in err) {
        const candidate = (err as { name?: unknown }).name;
        if (typeof candidate === "string" && candidate.trim() !== "") {
          errName = redactText(candidate, this.#token);
        }
      }
      throw new AiplexError({
        statusCode: null,
        code: "transport_error",
        message: `request failed: ${errName}`,
      });
    }

    if (response.status < 200 || response.status >= 300) {
      let text = "";
      try {
        text = await response.text();
      } catch {
        text = "";
      }
      const fallbackMessage = redactText(
        response.statusText || String(response.status),
        this.#token,
      );
      if (text.trim() !== "") {
        try {
          const parsed: unknown = JSON.parse(text);
          if (
            parsed !== null &&
            typeof parsed === "object" &&
            !Array.isArray(parsed)
          ) {
            const obj = parsed as Record<string, unknown>;
            const rawCode = obj["code"];
            if (typeof rawCode === "string" && rawCode.trim() !== "") {
              const rawMsg =
                typeof obj["message"] === "string" && obj["message"] !== ""
                  ? obj["message"]
                  : fallbackMessage;
              const rawDetails =
                obj["details"] !== null &&
                typeof obj["details"] === "object" &&
                !Array.isArray(obj["details"])
                  ? (obj["details"] as Record<string, unknown>)
                  : null;
              const rawRequestId =
                typeof obj["request_id"] === "string"
                  ? obj["request_id"]
                  : null;
              const rawTraceId =
                typeof obj["trace_id"] === "string" ? obj["trace_id"] : null;

              throw new AiplexError({
                statusCode: response.status,
                code: redactText(rawCode, this.#token),
                message: redactText(rawMsg, this.#token),
                details: redactRecord(rawDetails, this.#token),
                requestId:
                  rawRequestId !== null
                    ? redactText(rawRequestId, this.#token)
                    : null,
                traceId:
                  rawTraceId !== null
                    ? redactText(rawTraceId, this.#token)
                    : null,
              });
            }
          }
        } catch (inner: unknown) {
          if (inner instanceof AiplexError) {
            throw inner;
          }
        }
      }
      throw new AiplexError({
        statusCode: response.status,
        code: "unknown",
        message: fallbackMessage,
      });
    }

    if (options?.expectNoContent || response.status === 204) {
      return undefined;
    }

    let text: string;
    try {
      text = await response.text();
    } catch (err: unknown) {
      let errName = "Error";
      if (err !== null && typeof err === "object" && "name" in err) {
        const candidate = (err as { name?: unknown }).name;
        if (typeof candidate === "string" && candidate.trim() !== "") {
          errName = redactText(candidate, this.#token);
        }
      }
      throw new AiplexError({
        statusCode: null,
        code: "transport_error",
        message: `request failed: ${errName}`,
      });
    }

    try {
      return JSON.parse(text) as unknown;
    } catch {
      throw new AiplexError({
        statusCode: null,
        code: "decode_error",
        message: "response body is not valid JSON",
      });
    }
  }

  /**
   * Returns the caller's authenticated identity, roles, and scopes.
   */
  public async whoami(): Promise<Whoami> {
    const data = await this.#request("GET", "/api/v1/iam/whoami");
    return parseWhoami(data);
  }

  /**
   * Lists all access points in the tenant.
   */
  public async listAccessPoints(): Promise<AccessPointRecord[]> {
    const data = await this.#request("GET", "/api/v1/access-points/");
    return parseList(data, "listAccessPoints", parseAccessPointRecord);
  }

  /**
   * Gets a single access point by ID.
   */
  /**
   * Counters of a budget in its current window. Members see spent, reserved
   * and remaining; operators see more. A non-member gets a not-found error.
   */
  public async getBudgetStatus(id: string): Promise<BudgetStatus> {
    validateId(id, "id");
    const data = await this.#request("GET", `/api/v1/budgets/${encodeURIComponent(id)}/status`);
    return parseBudgetStatus(data);
  }

  public async getAccessPoint(id: string): Promise<AccessPointRecord> {
    validateId(id, "id");
    const data = await this.#request(
      "GET",
      `/api/v1/access-points/${encodeURIComponent(id)}`,
    );
    return parseAccessPointRecord(data);
  }

  /**
   * Creates (when `revision` is 0 or omitted) or replaces an access point.
   */
  public async putAccessPoint(
    spec: AccessPointSpec,
    revision = 0,
  ): Promise<AccessPointRecord> {
    if (!spec || typeof spec !== "object") {
      throw new TypeError("spec must be an AccessPointSpec object");
    }
    validateId(spec.id, "spec.id");
    validatePutRevision(revision);
    const body = toWireAccessPointSpec(spec);
    const data = await this.#request(
      "PUT",
      `/api/v1/access-points/${encodeURIComponent(spec.id)}`,
      {
        body,
        headers: { "If-Match": String(revision) },
      },
    );
    return parseAccessPointRecord(data);
  }

  /**
   * Deletes an access point at the specified positive revision.
   */
  public async deleteAccessPoint(id: string, revision: number): Promise<void> {
    validateId(id, "id");
    validateDeleteRevision(revision);
    await this.#request(
      "DELETE",
      `/api/v1/access-points/${encodeURIComponent(id)}`,
      {
        headers: { "If-Match": String(revision) },
        expectNoContent: true,
      },
    );
  }

  /**
   * Renders client connection configuration for an access point.
   */
  public async connectAccessPoint(
    id: string,
    client?: string,
  ): Promise<ConnectConfig> {
    validateId(id, "id");
    const query = new URLSearchParams();
    if (client !== undefined && client !== null && client !== "") {
      query.set("client", client);
    }
    const data = await this.#request(
      "GET",
      `/api/v1/overview/access-points/${encodeURIComponent(id)}/connect`,
      { query },
    );
    return parseConnectConfig(data);
  }

  /**
   * Lists a single page of access point overview summaries.
   */
  public async listOverviewAccessPoints(options?: {
    limit?: number;
    cursor?: string;
  }): Promise<OverviewAccessPointPage> {
    validateLimit(options?.limit);
    const query = new URLSearchParams();
    if (options?.limit !== undefined) {
      query.set("limit", String(options.limit));
    }
    if (options?.cursor !== undefined && options.cursor !== "") {
      query.set("cursor", options.cursor);
    }
    const data = await this.#request("GET", "/api/v1/overview/access-points", {
      query,
    });
    return parseOverviewAccessPointPage(data);
  }

  /**
   * Lazily iterates through all access point overview summaries across pages.
   */
  public async *iterOverviewAccessPoints(options?: {
    limit?: number;
  }): AsyncGenerator<OverviewAccessPointSummary, void, undefined> {
    validateLimit(options?.limit);
    let cursor: string | undefined;
    while (true) {
      const page = await this.listOverviewAccessPoints({
        limit: options?.limit,
        cursor,
      });
      for (const item of page.accessPoints) {
        yield item;
      }
      if (page.nextCursor === null || page.nextCursor === "") {
        break;
      }
      if (page.nextCursor === cursor) {
        throw new AiplexError({
          statusCode: null,
          code: "pagination_stalled",
          message: "server returned identical next_cursor",
        });
      }
      cursor = page.nextCursor;
    }
  }

  /**
   * Gets the detailed overview for a single access point.
   */
  public async getOverviewAccessPoint(
    id: string,
    options?: { since?: string },
  ): Promise<OverviewAccessPointDetail> {
    validateId(id, "id");
    const query = new URLSearchParams();
    if (options?.since !== undefined && options.since !== "") {
      query.set("since", options.since);
    }
    const data = await this.#request(
      "GET",
      `/api/v1/overview/access-points/${encodeURIComponent(id)}`,
      { query },
    );
    return parseOverviewAccessPointDetail(data);
  }

  /**
   * Lists a single page of overview subjects of the given kind.
   */
  public async listOverviewSubjects(
    kind: SubjectKind,
    options?: { since?: string; limit?: number; cursor?: string },
  ): Promise<OverviewSubjectPage> {
    validateSubjectKind(kind);
    validateLimit(options?.limit);
    const query = new URLSearchParams();
    query.set("kind", kind);
    if (options?.since !== undefined && options.since !== "") {
      query.set("since", options.since);
    }
    if (options?.limit !== undefined) {
      query.set("limit", String(options.limit));
    }
    if (options?.cursor !== undefined && options.cursor !== "") {
      query.set("cursor", options.cursor);
    }
    const data = await this.#request("GET", "/api/v1/overview/subjects", {
      query,
    });
    return parseOverviewSubjectPage(data);
  }

  /**
   * Lazily iterates through all overview subjects of the given kind across pages.
   */
  public async *iterOverviewSubjects(
    kind: SubjectKind,
    options?: { since?: string; limit?: number },
  ): AsyncGenerator<OverviewSubject, void, undefined> {
    validateSubjectKind(kind);
    validateLimit(options?.limit);
    let cursor: string | undefined;
    while (true) {
      const page = await this.listOverviewSubjects(kind, {
        since: options?.since,
        limit: options?.limit,
        cursor,
      });
      for (const subject of page.subjects) {
        yield subject;
      }
      if (page.nextCursor === null || page.nextCursor === "") {
        break;
      }
      if (page.nextCursor === cursor) {
        throw new AiplexError({
          statusCode: null,
          code: "pagination_stalled",
          message: "server returned identical next_cursor",
        });
      }
      cursor = page.nextCursor;
    }
  }

  /**
   * Gets detailed overview information for a single subject.
   */
  public async getOverviewSubject(
    kind: SubjectKind,
    id: string,
    options?: { since?: string },
  ): Promise<OverviewSubjectDetail> {
    validateSubjectKind(kind);
    validateId(id, "id");
    const query = new URLSearchParams();
    let path: string;
    if (id.includes("/")) {
      path = `/api/v1/overview/subjects/${encodeURIComponent(kind)}`;
      query.set("id", id);
    } else {
      path = `/api/v1/overview/subjects/${encodeURIComponent(kind)}/${encodeURIComponent(id)}`;
    }
    if (options?.since !== undefined && options.since !== "") {
      query.set("since", options.since);
    }
    const data = await this.#request("GET", path, { query });
    return parseOverviewSubjectDetail(data);
  }

  /**
   * Lists a single page of usage ledger records matching the optional filter.
   */
  public async listUsageRecords(
    filter?: UsageFilter,
    options?: { limit?: number; cursor?: string },
  ): Promise<UsageRecordPage> {
    validateLimit(options?.limit);
    const query = new URLSearchParams();
    applyUsageFilter(query, filter);
    if (options?.limit !== undefined) {
      query.set("limit", String(options.limit));
    }
    if (options?.cursor !== undefined && options.cursor !== "") {
      query.set("cursor", options.cursor);
    }
    const data = await this.#request("GET", "/api/v1/usage/records", { query });
    return parseUsageRecordPage(data);
  }

  /**
   * Lazily iterates through all usage ledger records matching the filter across pages.
   */
  public async *iterUsageRecords(
    filter?: UsageFilter,
    options?: { limit?: number },
  ): AsyncGenerator<UsageRecord, void, undefined> {
    const limit = options?.limit ?? 100;
    validateLimit(limit);
    let cursor: string | undefined;
    while (true) {
      const page = await this.listUsageRecords(filter, { limit, cursor });
      for (const record of page.records) {
        yield record;
      }
      if (page.nextCursor === null || page.nextCursor === "") {
        break;
      }
      if (page.nextCursor === cursor) {
        throw new AiplexError({
          statusCode: null,
          code: "pagination_stalled",
          message: "server returned identical next_cursor",
        });
      }
      cursor = page.nextCursor;
    }
  }

  /**
   * Gets a single usage ledger record by ID.
   */
  public async getUsageRecord(id: string): Promise<UsageRecord> {
    validateId(id, "id");
    const data = await this.#request(
      "GET",
      `/api/v1/usage/records/${encodeURIComponent(id)}`,
    );
    return parseUsageRecord(data);
  }

  /**
   * Queries aggregated usage summary statistics.
   */
  public async usageSummary(
    filter?: UsageFilter,
    options?: {
      groupBy?: readonly string[];
      bucket?: "hour" | "day";
      /** Add managed and reported records together instead of keeping them apart. */
      mergeSources?: boolean;
      /** Add latest-feedback totals to every group. */
      feedback?: boolean;
    },
  ): Promise<UsageSummary> {
    const query = new URLSearchParams();
    applyUsageFilter(query, filter);

    if (options?.groupBy !== undefined) {
      const cleaned: string[] = [];
      const seen = new Set<string>();
      for (const rawEntry of options.groupBy) {
        if (typeof rawEntry !== "string") {
          throw new TypeError("groupBy entries must be strings");
        }
        const entry = rawEntry.trim();
        if (entry === "") {
          continue;
        }
        if (!VALID_GROUP_BY.has(entry) && !TAG_GROUP.test(entry)) {
          throw new TypeError(`invalid groupBy field: ${entry}`);
        }
        if (seen.has(entry)) {
          throw new TypeError(`duplicate groupBy field: ${entry}`);
        }
        seen.add(entry);
        cleaned.push(entry);
      }
      if (cleaned.length > 0) {
        query.set("group_by", cleaned.join(","));
      }
    }

    if (options?.bucket !== undefined) {
      if (!VALID_BUCKETS.has(options.bucket)) {
        throw new TypeError("bucket must be 'hour' or 'day'");
      }
      query.set("bucket", options.bucket);
    }
    if (options?.mergeSources) {
      query.set("merge_sources", "true");
    }
    if (options?.feedback) {
      query.set("feedback", "true");
    }

    const data = await this.#request("GET", "/api/v1/usage/summary", { query });
    return parseUsageSummary(data);
  }

  /**
   * Adds feedback (score from -1 to 1) to a record the caller may see. It is
   * append-only; the newest entry is the record's current feedback.
   */
  public async addUsageFeedback(
    recordId: string,
    input: { score: number; label?: string; comment?: string },
  ): Promise<Feedback> {
    validateId(recordId, "recordId");
    if (
      typeof input.score !== "number" ||
      !Number.isFinite(input.score) ||
      input.score < -1 ||
      input.score > 1
    ) {
      throw new TypeError("score must be a number between -1 and 1");
    }
    const body: Record<string, unknown> = { score: input.score };
    if (input.label) body["label"] = input.label;
    if (input.comment) body["comment"] = input.comment;
    const data = await this.#request(
      "POST",
      `/api/v1/usage/records/${encodeURIComponent(recordId)}/feedback`,
      { body },
    );
    return parseFeedback(data);
  }

  /**
   * Lists all model price records in the tenant.
   */
  public async listPrices(): Promise<ModelPriceRecord[]> {
    const data = await this.#request("GET", "/api/v1/usage/prices/");
    return parseList(data, "listPrices", parseModelPriceRecord);
  }

  /**
   * Gets a single model price record by ID.
   */
  public async getPrice(id: string): Promise<ModelPriceRecord> {
    validateId(id, "id");
    const data = await this.#request(
      "GET",
      `/api/v1/usage/prices/${encodeURIComponent(id)}`,
    );
    return parseModelPriceRecord(data);
  }

  /**
   * Creates (when `revision` is 0 or omitted) or replaces a model price record.
   */
  public async putPrice(
    price: ModelPrice,
    revision = 0,
  ): Promise<ModelPriceRecord> {
    if (!price || typeof price !== "object") {
      throw new TypeError("price must be a ModelPrice object");
    }
    validateId(price.id, "price.id");
    validatePutRevision(revision);
    const body = toWireModelPrice(price);
    const data = await this.#request(
      "PUT",
      `/api/v1/usage/prices/${encodeURIComponent(price.id)}`,
      {
        body,
        headers: { "If-Match": String(revision) },
      },
    );
    return parseModelPriceRecord(data);
  }

  /**
   * Deletes a model price record at the specified positive revision.
   */
  public async deletePrice(id: string, revision: number): Promise<void> {
    validateId(id, "id");
    validateDeleteRevision(revision);
    await this.#request(
      "DELETE",
      `/api/v1/usage/prices/${encodeURIComponent(id)}`,
      {
        headers: { "If-Match": String(revision) },
        expectNoContent: true,
      },
    );
  }

  /**
   * Lists all LLM model surfaces in the tenant.
   */
  public async listModelSurfaces(): Promise<ModelSurface[]> {
    const data = await this.#request("GET", "/api/v1/llm/surfaces");
    return parseList(data, "listModelSurfaces", parseModelSurface);
  }

  /**
   * Gets a single LLM model surface by ID.
   */
  public async getModelSurface(id: string): Promise<ModelSurface> {
    validateId(id, "id");
    const data = await this.#request(
      "GET",
      `/api/v1/llm/surfaces/${encodeURIComponent(id)}`,
    );
    return parseModelSurface(data);
  }

  /**
   * Lists all MCP tool connections in the tenant.
   */
  public async listToolConnections(): Promise<ToolConnection[]> {
    const data = await this.#request("GET", "/api/v1/mcp/connections");
    return parseList(data, "listToolConnections", parseToolConnection);
  }

  /**
   * Gets a single MCP tool connection by ID.
   */
  public async getToolConnection(id: string): Promise<ToolConnection> {
    validateId(id, "id");
    const data = await this.#request(
      "GET",
      `/api/v1/mcp/connections/${encodeURIComponent(id)}`,
    );
    return parseToolConnection(data);
  }

  /**
   * Lists every budget of the tenant with its revision (operators only).
   */
  public async listBudgets(): Promise<BudgetRecord[]> {
    const data = await this.#request("GET", "/api/v1/budgets");
    return parseList(data, "listBudgets", parseBudgetRecord);
  }

  /**
   * Gets one budget definition with its current revision (operators only).
   * Members read counters with {@link AiplexClient.getBudgetStatus}.
   */
  public async getBudget(id: string): Promise<BudgetRecord> {
    validateId(id, "id");
    const data = await this.#request("GET", `/api/v1/budgets/${encodeURIComponent(id)}`);
    return parseBudgetRecord(data);
  }

  /**
   * Creates (`revision` 0) or replaces (`revision` equal to the stored one) the
   * budget named by `spec.id` (administrators only). A stale revision fails
   * with a 409 `REVISION_CONFLICT` error.
   */
  public async putBudget(spec: BudgetSpec, revision = 0): Promise<BudgetRecord> {
    if (!spec || typeof spec !== "object") {
      throw new TypeError("spec must be a BudgetSpec object");
    }
    validateId(spec.id, "spec.id");
    validatePutRevision(revision);
    const data = await this.#request("PUT", `/api/v1/budgets/${encodeURIComponent(spec.id)}`, {
      body: spec,
      headers: { "If-Match": String(revision) },
    });
    return parseBudgetRecord(data);
  }

  /**
   * Deletes a budget at the specified positive revision (administrators only).
   * Counters stay in the quota store until their windows end.
   */
  public async deleteBudget(id: string, revision: number): Promise<void> {
    validateId(id, "id");
    validateDeleteRevision(revision);
    await this.#request("DELETE", `/api/v1/budgets/${encodeURIComponent(id)}`, {
      headers: { "If-Match": String(revision) },
      expectNoContent: true,
    });
  }

  /**
   * Answers which budgets a hypothetical request would draw from and why
   * (administrators only). Changes nothing; a `draft` is validated, never stored.
   */
  public async previewBudget(input: BudgetPreviewInput): Promise<BudgetPreview> {
    if (!input || typeof input !== "object" || !input.subject || typeof input.subject !== "object") {
      throw new TypeError("input.subject is required");
    }
    const data = await this.#request("POST", "/api/v1/budgets/preview", {
      body: toWireBudgetPreview(input),
    });
    return parseBudgetPreview(data);
  }

  /**
   * Creates (`revision` 0) or replaces a model surface. The server validates
   * the spec. Credentials are references (`auth.secret_ref`), never values.
   */
  public async putModelSurface(
    id: string,
    spec: ModelSurfaceSpec,
    revision = 0,
  ): Promise<ModelSurface> {
    validateId(id, "id");
    if (!spec || typeof spec !== "object") {
      throw new TypeError("spec must be a ModelSurfaceSpec object");
    }
    validatePutRevision(revision);
    const data = await this.#request("PUT", `/api/v1/llm/surfaces/${encodeURIComponent(id)}`, {
      body: spec,
      headers: { "If-Match": String(revision) },
    });
    return parseModelSurface(data);
  }

  /**
   * Deletes a model surface at the specified positive revision.
   */
  public async deleteModelSurface(id: string, revision: number): Promise<void> {
    validateId(id, "id");
    validateDeleteRevision(revision);
    await this.#request("DELETE", `/api/v1/llm/surfaces/${encodeURIComponent(id)}`, {
      headers: { "If-Match": String(revision) },
      expectNoContent: true,
    });
  }

  /**
   * Reads a surface's last provider limit observation on the replica that
   * answered, plus its capacity policy state (administrators only). Advisory.
   */
  public async getModelSurfaceCapacity(id: string): Promise<SurfaceCapacity> {
    validateId(id, "id");
    const data = await this.#request(
      "GET",
      `/api/v1/llm/surfaces/${encodeURIComponent(id)}/capacity`,
    );
    return parseSurfaceCapacity(data);
  }

  /**
   * Creates (`revision` 0) or replaces a tool connection. `spec.id` must equal
   * `id`. Credentials are references (`auth.secret_ref` and similar), never values.
   */
  public async putToolConnection(
    id: string,
    spec: ToolConnectionSpec,
    revision = 0,
  ): Promise<ToolConnection> {
    validateId(id, "id");
    if (!spec || typeof spec !== "object") {
      throw new TypeError("spec must be a ToolConnectionSpec object");
    }
    if (spec.id !== id) {
      throw new TypeError("spec.id must equal id");
    }
    validatePutRevision(revision);
    const data = await this.#request("PUT", `/api/v1/mcp/connections/${encodeURIComponent(id)}`, {
      body: spec,
      headers: { "If-Match": String(revision) },
    });
    return parseToolConnection(data);
  }

  /**
   * Deletes a tool connection at the specified positive revision.
   */
  public async deleteToolConnection(id: string, revision: number): Promise<void> {
    validateId(id, "id");
    validateDeleteRevision(revision);
    await this.#request("DELETE", `/api/v1/mcp/connections/${encodeURIComponent(id)}`, {
      headers: { "If-Match": String(revision) },
      expectNoContent: true,
    });
  }

  /**
   * Lists the tenant's request-hook policies with their revisions.
   */
  public async listHookPolicies(): Promise<HookPolicyRecord[]> {
    const data = await this.#request("GET", "/api/v1/hooks/policies/");
    return parseList(data, "listHookPolicies", parseHookPolicyRecord);
  }

  /**
   * Gets one hook policy with its current revision.
   */
  public async getHookPolicy(id: string): Promise<HookPolicyRecord> {
    validateId(id, "id");
    const data = await this.#request("GET", `/api/v1/hooks/policies/${encodeURIComponent(id)}`);
    return parseHookPolicyRecord(data);
  }

  /**
   * Creates (`revision` 0) or replaces the hook policy named by `policy.id`.
   * The server validates it, including sidecar endpoints.
   */
  public async putHookPolicy(policy: HookPolicySpec, revision = 0): Promise<HookPolicyRecord> {
    if (!policy || typeof policy !== "object") {
      throw new TypeError("policy must be a HookPolicySpec object");
    }
    validateId(policy.id, "policy.id");
    validatePutRevision(revision);
    const data = await this.#request(
      "PUT",
      `/api/v1/hooks/policies/${encodeURIComponent(policy.id)}`,
      { body: policy, headers: { "If-Match": String(revision) } },
    );
    return parseHookPolicyRecord(data);
  }

  /**
   * Deletes a hook policy at the specified positive revision.
   */
  public async deleteHookPolicy(id: string, revision: number): Promise<void> {
    validateId(id, "id");
    validateDeleteRevision(revision);
    await this.#request("DELETE", `/api/v1/hooks/policies/${encodeURIComponent(id)}`, {
      headers: { "If-Match": String(revision) },
      expectNoContent: true,
    });
  }

  /**
   * Runs a saved policy's hooks over sample texts. Nothing is stored or logged.
   */
  public async testHookPolicy(
    id: string,
    sample: HookPolicyTestInput,
  ): Promise<HookPolicyTestResult> {
    validateId(id, "id");
    const body = toWireHookPolicyTest(sample);
    const data = await this.#request(
      "POST",
      `/api/v1/hooks/policies/${encodeURIComponent(id)}/test`,
      { body },
    );
    return parseHookPolicyTestResult(data);
  }

  /**
   * Reads the deployment's usage settings (trace link template, whether
   * feedback and feedback comments are enabled, allowed tag keys).
   */
  public async getUsageConfig(): Promise<UsageConfig> {
    const data = await this.#request("GET", "/api/v1/usage/config");
    return parseUsageConfig(data);
  }

  /**
   * Lists a usage record's feedback history, newest first.
   */
  public async listUsageFeedback(recordId: string): Promise<Feedback[]> {
    validateId(recordId, "recordId");
    const data = await this.#request(
      "GET",
      `/api/v1/usage/records/${encodeURIComponent(recordId)}/feedback`,
    );
    if (data === null || typeof data !== "object" || Array.isArray(data)) {
      throw new AiplexError({
        statusCode: null,
        code: "decode_error",
        message: "listUsageFeedback: expected JSON object",
      });
    }
    // The handler always writes the key (null when there is no history).
    // A missing key is a malformed answer, not "no feedback".
    const list = (data as Record<string, unknown>)["feedback"];
    if (list === undefined) {
      throw new AiplexError({
        statusCode: null,
        code: "decode_error",
        message: "listUsageFeedback: missing feedback list",
      });
    }
    if (list === null) {
      return [];
    }
    return parseList(list, "listUsageFeedback", parseFeedback);
  }
}
