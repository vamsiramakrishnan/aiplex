import { AiplexError } from "./errors.js";

function decodeError(message: string): AiplexError {
  return new AiplexError({
    statusCode: null,
    code: "decode_error",
    message,
  });
}

function asObject(value: unknown, context: string): Record<string, unknown> {
  if (value === null || typeof value !== "object" || Array.isArray(value)) {
    throw decodeError(`${context}: expected JSON object`);
  }
  return value as Record<string, unknown>;
}

function optObject(
  obj: Record<string, unknown>,
  key: string,
  context: string,
): Record<string, unknown> | null {
  const val = obj[key];
  if (val === undefined || val === null) {
    return null;
  }
  return asObject(val, `${context}.${key}`);
}

function optString(
  obj: Record<string, unknown>,
  key: string,
  context: string,
): string | null {
  const val = obj[key];
  if (val === undefined || val === null) {
    return null;
  }
  if (typeof val !== "string") {
    throw decodeError(`${context}.${key}: expected string`);
  }
  return val;
}

function reqString(
  obj: Record<string, unknown>,
  key: string,
  context: string,
): string {
  const val = obj[key];
  if (typeof val !== "string") {
    throw decodeError(`${context}.${key}: expected string`);
  }
  return val;
}

function optBoolean(
  obj: Record<string, unknown>,
  key: string,
  context: string,
): boolean | null {
  const val = obj[key];
  if (val === undefined || val === null) {
    return null;
  }
  if (typeof val !== "boolean") {
    throw decodeError(`${context}.${key}: expected boolean`);
  }
  return val;
}

function optNumber(
  obj: Record<string, unknown>,
  key: string,
  context: string,
): number | null {
  const val = obj[key];
  if (val === undefined || val === null) {
    return null;
  }
  if (typeof val !== "number" || !Number.isFinite(val)) {
    throw decodeError(`${context}.${key}: expected number`);
  }
  return val;
}

function optInt(
  obj: Record<string, unknown>,
  key: string,
  context: string,
): number | null {
  const val = obj[key];
  if (val === undefined || val === null) {
    return null;
  }
  if (typeof val !== "number" || !Number.isInteger(val)) {
    throw decodeError(`${context}.${key}: expected integer`);
  }
  return val;
}

function reqInt(
  obj: Record<string, unknown>,
  key: string,
  context: string,
): number {
  const val = obj[key];
  if (typeof val !== "number" || !Number.isInteger(val)) {
    throw decodeError(`${context}.${key}: expected integer`);
  }
  return val;
}

function stringList(
  obj: Record<string, unknown>,
  key: string,
  context: string,
): string[] {
  const val = obj[key];
  if (val === undefined || val === null) {
    return [];
  }
  if (!Array.isArray(val)) {
    throw decodeError(`${context}.${key}: expected array`);
  }
  const out: string[] = [];
  for (let i = 0; i < val.length; i++) {
    const item = val[i];
    if (typeof item !== "string") {
      throw decodeError(`${context}.${key}[${i}]: expected string`);
    }
    out.push(item);
  }
  return out;
}

function objectList(
  obj: Record<string, unknown>,
  key: string,
  context: string,
): Record<string, unknown>[] {
  const val = obj[key];
  if (val === undefined || val === null) {
    return [];
  }
  if (!Array.isArray(val)) {
    throw decodeError(`${context}.${key}: expected array`);
  }
  return val.map((item, idx) => asObject(item, `${context}.${key}[${idx}]`));
}

function stringMap(
  obj: Record<string, unknown>,
  key: string,
  context: string,
): Record<string, string> {
  const val = obj[key];
  if (val === undefined || val === null) {
    return {};
  }
  const rec = asObject(val, `${context}.${key}`);
  const out: Record<string, string> = {};
  for (const [k, v] of Object.entries(rec)) {
    if (v === null || v === undefined) {
      continue;
    }
    if (typeof v !== "string") {
      throw decodeError(`${context}.${key}.${k}: expected string`);
    }
    out[k] = v;
  }
  return out;
}

function nullableStringMap(
  obj: Record<string, unknown>,
  key: string,
  context: string,
): Record<string, string | null> {
  const val = obj[key];
  if (val === undefined || val === null) {
    return {};
  }
  const rec = asObject(val, `${context}.${key}`);
  const out: Record<string, string | null> = {};
  for (const [k, v] of Object.entries(rec)) {
    if (v === null || v === undefined) {
      out[k] = null;
    } else if (typeof v === "string") {
      out[k] = v;
    } else if (typeof v === "number" || typeof v === "boolean") {
      out[k] = String(v);
    } else {
      throw decodeError(`${context}.${key}.${k}: expected string or null`);
    }
  }
  return out;
}

function intMap(
  obj: Record<string, unknown>,
  key: string,
  context: string,
): Record<string, number> {
  const val = obj[key];
  if (val === undefined || val === null) {
    return {};
  }
  const rec = asObject(val, `${context}.${key}`);
  const out: Record<string, number> = {};
  for (const [k, v] of Object.entries(rec)) {
    if (v === null || v === undefined) {
      continue;
    }
    if (typeof v !== "number" || !Number.isInteger(v)) {
      throw decodeError(`${context}.${key}.${k}: expected integer`);
    }
    out[k] = v;
  }
  return out;
}

/**
 * Formats a minor-unit integer string at a given decimal scale into a decimal string using BigInt.
 * Never uses floating-point arithmetic.
 */
export function formatMinorDecimal(amountMinor: string, scale: number): string {
  const trimmed = amountMinor.trim();
  if (!/^-?\d+$/.test(trimmed)) {
    throw decodeError(`cost.amount: invalid integer string "${amountMinor}"`);
  }
  if (!Number.isInteger(scale) || scale < 0) {
    throw decodeError(`cost.scale: invalid scale ${scale}`);
  }
  const big = BigInt(trimmed);
  const negative = big < 0n;
  const absStr = (negative ? -big : big).toString(10);
  if (scale === 0) {
    return (negative ? "-" : "") + absStr;
  }
  const padded = absStr.padStart(scale + 1, "0");
  const splitIdx = padded.length - scale;
  const whole = padded.slice(0, splitIdx);
  const frac = padded.slice(splitIdx);
  return `${negative ? "-" : ""}${whole}.${frac}`;
}

/**
 * Authenticated caller identity from `/api/v1/iam/whoami`.
 */
export interface Identity {
  principalId: string | null;
  subject: string | null;
  email: string | null;
  displayName: string | null;
  groups: string[];
  domain: string | null;
  provider: string | null;
  poolId: string | null;
  isWorkforce: boolean | null;
  raw: Record<string, unknown>;
}

/**
 * Parses an {@link Identity} object.
 */
export function parseIdentity(value: unknown): Identity {
  const obj = asObject(value, "Identity");
  return {
    principalId: optString(obj, "principal_id", "Identity"),
    subject: optString(obj, "subject", "Identity"),
    email: optString(obj, "email", "Identity"),
    displayName: optString(obj, "display_name", "Identity"),
    groups: stringList(obj, "groups", "Identity"),
    domain: optString(obj, "domain", "Identity"),
    provider: optString(obj, "provider", "Identity"),
    poolId: optString(obj, "pool_id", "Identity"),
    isWorkforce: optBoolean(obj, "is_workforce", "Identity"),
    raw: obj,
  };
}

/**
 * Response from `GET /api/v1/iam/whoami`.
 */
export interface Whoami {
  configurationRevision: number | null;
  identity: Identity | null;
  roles: string[];
  scopes: string[];
  groupScopes: string[];
  matchedBindings: string[];
  raw: Record<string, unknown>;
}

/**
 * Parses a {@link Whoami} response.
 */
export function parseWhoami(value: unknown): Whoami {
  const obj = asObject(value, "Whoami");
  const idRaw = optObject(obj, "identity", "Whoami");
  return {
    configurationRevision: optInt(obj, "configuration_revision", "Whoami"),
    identity: idRaw !== null ? parseIdentity(idRaw) : null,
    roles: stringList(obj, "roles", "Whoami"),
    scopes: stringList(obj, "scopes", "Whoami"),
    groupScopes: stringList(obj, "group_scopes", "Whoami"),
    matchedBindings: stringList(obj, "matched_bindings", "Whoami"),
    raw: obj,
  };
}

/**
 * One backend of a routed access-point model (docs/reliability-and-routing.md).
 */
export interface RoutingBackend {
  surfaceRef: string | null;
  nativeModel: string | null;
  /** Share of first attempts under the weighted strategy (default 1). */
  weight?: number | null;
  /** Order under the priority strategy (lower first, default 0). */
  priority?: number | null;
}

/**
 * Parses a {@link RoutingBackend}.
 */
export function parseRoutingBackend(value: unknown): RoutingBackend {
  const obj = asObject(value, "RoutingBackend");
  return {
    surfaceRef: optString(obj, "surface_ref", "RoutingBackend"),
    nativeModel: optString(obj, "native_model", "RoutingBackend"),
    weight: optInt(obj, "weight", "RoutingBackend"),
    priority: optInt(obj, "priority", "RoutingBackend"),
  };
}

function toWireRoutingBackend(b: RoutingBackend): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  if (b.surfaceRef !== null && b.surfaceRef !== undefined) out["surface_ref"] = b.surfaceRef;
  if (b.nativeModel !== null && b.nativeModel !== undefined) out["native_model"] = b.nativeModel;
  if (b.weight !== null && b.weight !== undefined) out["weight"] = b.weight;
  if (b.priority !== null && b.priority !== undefined) out["priority"] = b.priority;
  return out;
}

/**
 * Exact-match response cache of an access-point model (off unless set).
 * scope "access_point" shares answers between callers.
 */
export interface ResponseCacheConfig {
  ttl: string | null;
  scope?: string | null;
  maxEntryBytes?: number | null;
}

/**
 * Model mapping entry within an access point specification: one
 * surfaceRef/nativeModel pair, or backends with a routing block.
 */
export interface AccessPointModel {
  name: string | null;
  surfaceRef: string | null;
  nativeModel: string | null;
  backends?: RoutingBackend[];
  /** The routing block as sent on the wire (strategy, retry, fallback, breaker, lowest_cost, deadline_ms). */
  routing?: Record<string, unknown> | null;
  cache?: ResponseCacheConfig | null;
  raw?: Record<string, unknown>;
}

/**
 * Parses an {@link AccessPointModel}.
 */
export function parseAccessPointModel(value: unknown): AccessPointModel {
  const obj = asObject(value, "AccessPointModel");
  const cacheRaw = optObject(obj, "cache", "AccessPointModel");
  return {
    name: optString(obj, "name", "AccessPointModel"),
    surfaceRef: optString(obj, "surface_ref", "AccessPointModel"),
    nativeModel: optString(obj, "native_model", "AccessPointModel"),
    backends: objectList(obj, "backends", "AccessPointModel").map((b) => parseRoutingBackend(b)),
    routing: optObject(obj, "routing", "AccessPointModel"),
    cache:
      cacheRaw !== null
        ? {
            ttl: optString(cacheRaw, "ttl", "AccessPointModel.cache"),
            scope: optString(cacheRaw, "scope", "AccessPointModel.cache"),
            maxEntryBytes: optInt(cacheRaw, "max_entry_bytes", "AccessPointModel.cache"),
          }
        : null,
    raw: obj,
  };
}

/**
 * Tool connection exposure entry within an access point specification.
 */
export interface AccessPointTool {
  connectionRef: string;
  expose: string[];
  raw?: Record<string, unknown>;
}

/**
 * Parses an {@link AccessPointTool}.
 */
export function parseAccessPointTool(value: unknown): AccessPointTool {
  const obj = asObject(value, "AccessPointTool");
  return {
    connectionRef: reqString(obj, "connection_ref", "AccessPointTool"),
    expose: stringList(obj, "expose", "AccessPointTool"),
    raw: obj,
  };
}

/**
 * Skill server entry within an access point specification.
 */
export interface AccessPointSkill {
  instanceRef: string | null;
  raw?: Record<string, unknown>;
}

/**
 * Parses an {@link AccessPointSkill}.
 */
export function parseAccessPointSkill(value: unknown): AccessPointSkill {
  const obj = asObject(value, "AccessPointSkill");
  return {
    instanceRef: optString(obj, "instance_ref", "AccessPointSkill"),
    raw: obj,
  };
}

/**
 * Specification of an access point.
 */
export interface AccessPointSpec {
  id: string;
  displayName: string | null;
  callerKinds: string[];
  subjects: Record<string, unknown> | null;
  models: AccessPointModel[];
  tools: AccessPointTool[];
  skills: AccessPointSkill[];
  qos: Record<string, unknown> | null;
  raw?: Record<string, unknown>;
}

/**
 * Parses an {@link AccessPointSpec}.
 */
export function parseAccessPointSpec(value: unknown): AccessPointSpec {
  const obj = asObject(value, "AccessPointSpec");
  const modelsRaw = obj["models"];
  let models: AccessPointModel[] = [];
  if (modelsRaw !== undefined && modelsRaw !== null) {
    if (!Array.isArray(modelsRaw)) {
      throw decodeError("AccessPointSpec.models: expected array");
    }
    models = modelsRaw.map((m) => parseAccessPointModel(m));
  }
  const toolsRaw = obj["tools"];
  let tools: AccessPointTool[] = [];
  if (toolsRaw !== undefined && toolsRaw !== null) {
    if (!Array.isArray(toolsRaw)) {
      throw decodeError("AccessPointSpec.tools: expected array");
    }
    tools = toolsRaw.map((t) => parseAccessPointTool(t));
  }
  const skillsRaw = obj["skills"];
  let skills: AccessPointSkill[] = [];
  if (skillsRaw !== undefined && skillsRaw !== null) {
    if (!Array.isArray(skillsRaw)) {
      throw decodeError("AccessPointSpec.skills: expected array");
    }
    skills = skillsRaw.map((s) => parseAccessPointSkill(s));
  }
  return {
    id: reqString(obj, "id", "AccessPointSpec"),
    displayName: optString(obj, "display_name", "AccessPointSpec"),
    callerKinds: stringList(obj, "caller_kinds", "AccessPointSpec"),
    subjects: optObject(obj, "subjects", "AccessPointSpec"),
    models,
    tools,
    skills,
    qos: optObject(obj, "qos", "AccessPointSpec"),
    raw: obj,
  };
}

/**
 * Converts an {@link AccessPointSpec} to its snake_case wire representation.
 */
export function toWireAccessPointSpec(
  spec: AccessPointSpec,
): Record<string, unknown> {
  const out: Record<string, unknown> = {
    id: spec.id,
    caller_kinds: spec.callerKinds,
  };
  if (spec.displayName !== null && spec.displayName !== undefined) {
    out["display_name"] = spec.displayName;
  }
  if (spec.subjects !== null && spec.subjects !== undefined) {
    out["subjects"] = spec.subjects;
  }
  if (spec.models && spec.models.length > 0) {
    out["models"] = spec.models.map((m) => {
      const item: Record<string, unknown> = {};
      if (m.name !== null && m.name !== undefined) {
        item["name"] = m.name;
      }
      if (m.surfaceRef !== null && m.surfaceRef !== undefined) {
        item["surface_ref"] = m.surfaceRef;
      }
      if (m.nativeModel !== null && m.nativeModel !== undefined) {
        item["native_model"] = m.nativeModel;
      }
      if (m.backends && m.backends.length > 0) {
        item["backends"] = m.backends.map((b) => toWireRoutingBackend(b));
      }
      if (m.routing !== null && m.routing !== undefined) {
        item["routing"] = m.routing;
      }
      if (m.cache !== null && m.cache !== undefined) {
        const cache: Record<string, unknown> = {};
        if (m.cache.ttl !== null && m.cache.ttl !== undefined) cache["ttl"] = m.cache.ttl;
        if (m.cache.scope !== null && m.cache.scope !== undefined) cache["scope"] = m.cache.scope;
        if (m.cache.maxEntryBytes !== null && m.cache.maxEntryBytes !== undefined) cache["max_entry_bytes"] = m.cache.maxEntryBytes;
        item["cache"] = cache;
      }
      return item;
    });
  }
  if (spec.tools && spec.tools.length > 0) {
    out["tools"] = spec.tools.map((t) => {
      const item: Record<string, unknown> = {
        connection_ref: t.connectionRef,
      };
      if (t.expose && t.expose.length > 0) {
        item["expose"] = t.expose;
      }
      return item;
    });
  }
  if (spec.skills && spec.skills.length > 0) {
    out["skills"] = spec.skills.map((s) => {
      const item: Record<string, unknown> = {};
      if (s.instanceRef !== null && s.instanceRef !== undefined) {
        item["instance_ref"] = s.instanceRef;
      }
      return item;
    });
  }
  if (spec.qos !== null && spec.qos !== undefined) {
    out["qos"] = spec.qos;
  }
  return out;
}

/**
 * Stored access point resource record with optimistic-concurrency revision.
 */
export interface AccessPointRecord {
  tenantId: string | null;
  kind: string | null;
  id: string;
  revision: number;
  spec: AccessPointSpec;
  raw: Record<string, unknown>;
}

/**
 * Parses an {@link AccessPointRecord}.
 */
export function parseAccessPointRecord(value: unknown): AccessPointRecord {
  const obj = asObject(value, "AccessPointRecord");
  const specObj = asObject(obj["spec"], "AccessPointRecord.spec");
  return {
    tenantId: optString(obj, "tenant_id", "AccessPointRecord"),
    kind: optString(obj, "kind", "AccessPointRecord"),
    id: reqString(obj, "id", "AccessPointRecord"),
    revision: reqInt(obj, "revision", "AccessPointRecord"),
    spec: parseAccessPointSpec(specObj),
    raw: obj,
  };
}

/**
 * Model entry in an access point connect configuration.
 */
export interface ConnectModel {
  name: string | null;
  protocols: string[];
  raw: Record<string, unknown>;
}

/**
 * Parses a {@link ConnectModel}.
 */
export function parseConnectModel(value: unknown): ConnectModel {
  const obj = asObject(value, "ConnectModel");
  return {
    name: optString(obj, "name", "ConnectModel"),
    protocols: stringList(obj, "protocols", "ConnectModel"),
    raw: obj,
  };
}

/**
 * Configuration snippet in an access point connect configuration.
 */
export interface ConnectSnippet {
  title: string | null;
  language: string | null;
  content: string | null;
  raw: Record<string, unknown>;
}

/**
 * Parses a {@link ConnectSnippet}.
 */
export function parseConnectSnippet(value: unknown): ConnectSnippet {
  const obj = asObject(value, "ConnectSnippet");
  return {
    title: optString(obj, "title", "ConnectSnippet"),
    language: optString(obj, "language", "ConnectSnippet"),
    content: optString(obj, "content", "ConnectSnippet"),
    raw: obj,
  };
}

/**
 * Client connection instructions for an access point.
 */
export interface ConnectConfig {
  accessPointId: string | null;
  client: string | null;
  baseUrls: Record<string, string>;
  endpoints: Record<string, unknown>[];
  models: ConnectModel[];
  mcpUrl: string | null;
  mcpCatalogUrl: string | null;
  auth: Record<string, unknown> | null;
  snippets: ConnectSnippet[];
  notes: string[];
  raw: Record<string, unknown>;
}

/**
 * Parses a {@link ConnectConfig}.
 */
export function parseConnectConfig(value: unknown): ConnectConfig {
  const obj = asObject(value, "ConnectConfig");
  const modelsRaw = obj["models"];
  let models: ConnectModel[] = [];
  if (modelsRaw !== undefined && modelsRaw !== null) {
    if (!Array.isArray(modelsRaw)) {
      throw decodeError("ConnectConfig.models: expected array");
    }
    models = modelsRaw.map((m) => parseConnectModel(m));
  }
  const snippetsRaw = obj["snippets"];
  let snippets: ConnectSnippet[] = [];
  if (snippetsRaw !== undefined && snippetsRaw !== null) {
    if (!Array.isArray(snippetsRaw)) {
      throw decodeError("ConnectConfig.snippets: expected array");
    }
    snippets = snippetsRaw.map((s) => parseConnectSnippet(s));
  }
  return {
    accessPointId: optString(obj, "access_point_id", "ConnectConfig"),
    client: optString(obj, "client", "ConnectConfig"),
    baseUrls: stringMap(obj, "base_urls", "ConnectConfig"),
    endpoints: objectList(obj, "endpoints", "ConnectConfig"),
    models,
    mcpUrl: optString(obj, "mcp_url", "ConnectConfig"),
    mcpCatalogUrl: optString(obj, "mcp_catalog_url", "ConnectConfig"),
    auth: optObject(obj, "auth", "ConnectConfig"),
    snippets,
    notes: stringList(obj, "notes", "ConnectConfig"),
    raw: obj,
  };
}

/**
 * Token counters on a usage record. Unknown counters are `null`, never `0`.
 */
export interface UsageCounters {
  inputTokens: number | null;
  outputTokens: number | null;
  cacheReadTokens: number | null;
  cacheWriteTokens: number | null;
  reasoningTokens: number | null;
  totalTokens: number | null;
  /** Part of cacheWriteTokens written with the one-hour TTL (a subset). */
  cacheWrite1hTokens: number | null;
  /** Gemini tool-use prompt tokens; not part of inputTokens. */
  toolUsePromptTokens: number | null;
  raw: Record<string, unknown>;
}

/**
 * Parses {@link UsageCounters}.
 */
export function parseUsageCounters(value: unknown): UsageCounters {
  const obj = asObject(value, "UsageCounters");
  return {
    inputTokens: optInt(obj, "input_tokens", "UsageCounters"),
    outputTokens: optInt(obj, "output_tokens", "UsageCounters"),
    cacheReadTokens: optInt(obj, "cache_read_tokens", "UsageCounters"),
    cacheWriteTokens: optInt(obj, "cache_write_tokens", "UsageCounters"),
    reasoningTokens: optInt(obj, "reasoning_tokens", "UsageCounters"),
    totalTokens: optInt(obj, "total_tokens", "UsageCounters"),
    cacheWrite1hTokens: optInt(obj, "cache_write_1h_tokens", "UsageCounters"),
    toolUsePromptTokens: optInt(obj, "tool_use_prompt_tokens", "UsageCounters"),
    raw: obj,
  };
}

/**
 * Fixed-point monetary cost on a usage record. Unknown cost has `amountMinor: null`, `scale: null`, `amountDecimal: null`.
 */
export interface Cost {
  basis: string | null;
  currency: string | null;
  amountMinor: string | null;
  scale: number | null;
  amountDecimal: string | null;
  /** Unrounded amount in minor units as a fraction ("37/4"); only when amount was rounded up. */
  exact: string | null;
  /** The price an estimate used. */
  priceId: string | null;
  /** Set when the cost was unknown at write time and priced afterwards. */
  backfilledAt: string | null;
  raw: Record<string, unknown>;
}

/**
 * Parses a {@link Cost} object without floating-point conversion.
 */
export function parseCost(value: unknown): Cost {
  const obj = asObject(value, "Cost");
  const basis = optString(obj, "basis", "Cost");
  const currency = optString(obj, "currency", "Cost");
  const amountMinor = optString(obj, "amount", "Cost");
  const scale = optInt(obj, "scale", "Cost");
  let amountDecimal = optString(obj, "amount_decimal", "Cost");
  if (amountMinor !== null) {
    if (!/^-?\d+$/.test(amountMinor.trim())) {
      throw decodeError(`Cost.amount: invalid integer string "${amountMinor}"`);
    }
  }
  if (amountDecimal === null && amountMinor !== null && scale !== null) {
    amountDecimal = formatMinorDecimal(amountMinor, scale);
  }
  return {
    basis,
    currency,
    amountMinor,
    scale,
    amountDecimal,
    exact: basis === "unknown" ? null : optString(obj, "exact", "Cost"),
    priceId: basis === "unknown" ? null : optString(obj, "price_id", "Cost"),
    backfilledAt: basis === "unknown" ? null : optString(obj, "backfilled_at", "Cost"),
    raw: obj,
  };
}

/**
 * Aggregated cost entry in usage summaries and overview totals.
 */
export interface CostTotal {
  currency: string | null;
  basis: string | null;
  amountMinor: string | null;
  scale: number | null;
  amountDecimal: string | null;
  records: number | null;
  raw: Record<string, unknown>;
}

/**
 * Parses a {@link CostTotal} object.
 */
export function parseCostTotal(value: unknown): CostTotal {
  const obj = asObject(value, "CostTotal");
  const currency = optString(obj, "currency", "CostTotal");
  const basis = optString(obj, "basis", "CostTotal");
  const amountMinor = optString(obj, "amount", "CostTotal");
  const scale = optInt(obj, "scale", "CostTotal");
  const records = optInt(obj, "records", "CostTotal");
  let amountDecimal = optString(obj, "amount_decimal", "CostTotal");
  if (amountMinor !== null) {
    if (!/^-?\d+$/.test(amountMinor.trim())) {
      throw decodeError(
        `CostTotal.amount: invalid integer string "${amountMinor}"`,
      );
    }
  }
  if (amountDecimal === null && amountMinor !== null && scale !== null) {
    amountDecimal = formatMinorDecimal(amountMinor, scale);
  }
  return {
    currency,
    basis,
    amountMinor,
    scale,
    amountDecimal,
    records,
    raw: obj,
  };
}

function parseCostTotalList(
  obj: Record<string, unknown>,
  key: string,
  context: string,
): CostTotal[] {
  const val = obj[key];
  if (val === undefined || val === null) {
    return [];
  }
  if (!Array.isArray(val)) {
    throw decodeError(`${context}.${key}: expected array`);
  }
  return val.map((item) => parseCostTotal(item));
}

/**
 * One feedback entry on a usage record. Append-only; the newest is current.
 * `score` is null only when the server omitted it; absence is never neutral.
 */
export interface Feedback {
  id: string | null;
  recordId: string | null;
  by: string | null;
  createdAt: string | null;
  score: number | null;
  label: string | null;
  comment: string | null;
  raw: Record<string, unknown>;
}

/**
 * Parses a {@link Feedback}.
 */
export function parseFeedback(value: unknown): Feedback {
  const obj = asObject(value, "Feedback");
  return {
    id: optString(obj, "id", "Feedback"),
    recordId: optString(obj, "record_id", "Feedback"),
    by: optString(obj, "by", "Feedback"),
    createdAt: optString(obj, "created_at", "Feedback"),
    score: optNumber(obj, "score", "Feedback"),
    label: optString(obj, "label", "Feedback"),
    comment: optString(obj, "comment", "Feedback"),
    raw: obj,
  };
}

/**
 * Latest-feedback totals of a summary group. `averageScore` is null when no
 * record has feedback; records without feedback are in `withoutFeedback`.
 */
export interface FeedbackTotals {
  records: number | null;
  withoutFeedback: number | null;
  positive: number | null;
  negative: number | null;
  neutral: number | null;
  averageScore: number | null;
  raw: Record<string, unknown>;
}

/**
 * Parses a {@link FeedbackTotals}.
 */
export function parseFeedbackTotals(value: unknown): FeedbackTotals {
  const obj = asObject(value, "FeedbackTotals");
  return {
    records: optInt(obj, "records", "FeedbackTotals"),
    withoutFeedback: optInt(obj, "without_feedback", "FeedbackTotals"),
    positive: optInt(obj, "positive", "FeedbackTotals"),
    negative: optInt(obj, "negative", "FeedbackTotals"),
    neutral: optInt(obj, "neutral", "FeedbackTotals"),
    averageScore: optNumber(obj, "average_score", "FeedbackTotals"),
    raw: obj,
  };
}

/**
 * Single usage ledger record.
 */
export interface UsageRecord {
  id: string;
  tenantId: string | null;
  startedAt: string | null;
  finishedAt: string | null;
  latencyMs: number | null;
  plane: string | null;
  provider: string | null;
  resourceId: string | null;
  operation: string | null;
  modelId: string | null;
  modelVersion: string | null;
  principalId: string | null;
  principalDisplay: string | null;
  accessPointId: string | null;
  callerKind: string | null;
  requestId: string | null;
  traceId: string | null;
  spanId: string | null;
  status: string | null;
  source: string | null;
  /** Processing tier the provider reported (default, standard, flex, priority, mixed). */
  serviceTier: string | null;
  /** Provider-side tool requests by kind; empty when none were reported. */
  serverTools: Record<string, number>;
  streaming: boolean | null;
  httpStatus: number | null;
  requests: number | null;
  usage: UsageCounters | null;
  cost: Cost | null;
  /** Reported records: the harness, its version and its own session id. Labels, never identity. */
  harness: string | null;
  harnessVersion: string | null;
  sessionId: string | null;
  clientRequestId: string | null;
  callerSpanId: string | null;
  reportedAttributes: Record<string, string>;
  /** Experiment labels from access point labels and allowlisted headers. */
  tags: Record<string, string>;
  /** Tenant budgets the exchange drew from (its own and ancestors). */
  budgetIds: string[];
  /** Request-hook summary (decision, results); null when no hook ran. */
  hooks: Record<string, unknown> | null;
  /** Latest feedback, present on a record fetched with getUsageRecord. */
  feedback: Feedback | null;
  /** Routed access-point exchanges: every attempt in order. */
  attempts: RouteAttempt[];
  /** The backend that served a routed exchange. */
  backend: RoutingBackend | null;
  routingStrategy: string | null;
  /** Response-cache outcome, when the model has a cache. */
  cache: CacheInfo | null;
  /** On a cache hit, the original exchange's usage; not provider usage of this record. */
  cachedResponseUsage: UsageCounters | null;
  raw: Record<string, unknown>;
}

/**
 * One attempt of a routed exchange (no content).
 */
export interface RouteAttempt {
  surfaceRef: string | null;
  nativeModel: string | null;
  outcome: string | null;
  upstreamStatus: number | null;
  retryAfterMs: number | null;
  durationMs: number | null;
  breakerState: string | null;
  reason: string | null;
}

/**
 * Response-cache outcome of an exchange: hit, miss or bypass.
 */
export interface CacheInfo {
  status: string | null;
  reason: string | null;
  scope: string | null;
}

function parseRouteAttempt(value: unknown): RouteAttempt {
  const obj = asObject(value, "RouteAttempt");
  return {
    surfaceRef: optString(obj, "surface_ref", "RouteAttempt"),
    nativeModel: optString(obj, "native_model", "RouteAttempt"),
    outcome: optString(obj, "outcome", "RouteAttempt"),
    upstreamStatus: optInt(obj, "upstream_status", "RouteAttempt"),
    retryAfterMs: optInt(obj, "retry_after_ms", "RouteAttempt"),
    durationMs: optInt(obj, "duration_ms", "RouteAttempt"),
    breakerState: optString(obj, "breaker_state", "RouteAttempt"),
    reason: optString(obj, "reason", "RouteAttempt"),
  };
}

/**
 * Parses a {@link UsageRecord}.
 */
export function parseUsageRecord(value: unknown): UsageRecord {
  const obj = asObject(value, "UsageRecord");
  const usageRaw = optObject(obj, "usage", "UsageRecord");
  const costRaw = optObject(obj, "cost", "UsageRecord");
  return {
    id: reqString(obj, "id", "UsageRecord"),
    tenantId: optString(obj, "tenant_id", "UsageRecord"),
    startedAt: optString(obj, "started_at", "UsageRecord"),
    finishedAt: optString(obj, "finished_at", "UsageRecord"),
    latencyMs: optInt(obj, "latency_ms", "UsageRecord"),
    plane: optString(obj, "plane", "UsageRecord"),
    provider: optString(obj, "provider", "UsageRecord"),
    resourceId: optString(obj, "resource_id", "UsageRecord"),
    operation: optString(obj, "operation", "UsageRecord"),
    modelId: optString(obj, "model_id", "UsageRecord"),
    modelVersion: optString(obj, "model_version", "UsageRecord"),
    principalId: optString(obj, "principal_id", "UsageRecord"),
    principalDisplay: optString(obj, "principal_display", "UsageRecord"),
    accessPointId: optString(obj, "access_point_id", "UsageRecord"),
    callerKind: optString(obj, "caller_kind", "UsageRecord"),
    requestId: optString(obj, "request_id", "UsageRecord"),
    traceId: optString(obj, "trace_id", "UsageRecord"),
    spanId: optString(obj, "span_id", "UsageRecord"),
    status: optString(obj, "status", "UsageRecord"),
    source: optString(obj, "source", "UsageRecord"),
    serviceTier: optString(obj, "service_tier", "UsageRecord"),
    serverTools: intMap(obj, "server_tools", "UsageRecord"),
    streaming: optBoolean(obj, "streaming", "UsageRecord"),
    httpStatus: optInt(obj, "http_status", "UsageRecord"),
    requests: optInt(obj, "requests", "UsageRecord"),
    usage: usageRaw !== null ? parseUsageCounters(usageRaw) : null,
    cost: costRaw !== null ? parseCost(costRaw) : null,
    harness: optString(obj, "harness", "UsageRecord"),
    harnessVersion: optString(obj, "harness_version", "UsageRecord"),
    sessionId: optString(obj, "session_id", "UsageRecord"),
    clientRequestId: optString(obj, "client_request_id", "UsageRecord"),
    callerSpanId: optString(obj, "caller_span_id", "UsageRecord"),
    reportedAttributes: stringMap(obj, "reported_attributes", "UsageRecord"),
    tags: stringMap(obj, "tags", "UsageRecord"),
    budgetIds: Array.isArray(obj["budget_ids"])
      ? (obj["budget_ids"] as unknown[]).filter((x): x is string => typeof x === "string")
      : [],
    hooks: optObject(obj, "hooks", "UsageRecord"),
    feedback:
      optObject(obj, "feedback", "UsageRecord") !== null
        ? parseFeedback(obj["feedback"])
        : null,
    attempts: objectList(obj, "attempts", "UsageRecord").map((a) => parseRouteAttempt(a)),
    backend: optObject(obj, "backend", "UsageRecord") !== null ? parseRoutingBackend(obj["backend"]) : null,
    routingStrategy: optString(obj, "routing_strategy", "UsageRecord"),
    cache: (() => {
      const c = optObject(obj, "cache", "UsageRecord");
      return c !== null
        ? { status: optString(c, "status", "UsageRecord.cache"), reason: optString(c, "reason", "UsageRecord.cache"), scope: optString(c, "scope", "UsageRecord.cache") }
        : null;
    })(),
    cachedResponseUsage:
      optObject(obj, "cached_response_usage", "UsageRecord") !== null
        ? parseUsageCounters(obj["cached_response_usage"])
        : null,
    raw: obj,
  };
}

/**
 * Page of usage ledger records.
 */
export interface UsageRecordPage {
  records: UsageRecord[];
  nextCursor: string | null;
  raw: Record<string, unknown>;
}

/**
 * Parses a {@link UsageRecordPage}.
 */
export function parseUsageRecordPage(value: unknown): UsageRecordPage {
  const obj = asObject(value, "UsageRecordPage");
  const recsVal = obj["records"];
  let records: UsageRecord[] = [];
  if (recsVal !== undefined && recsVal !== null) {
    if (!Array.isArray(recsVal)) {
      throw decodeError("UsageRecordPage.records: expected array");
    }
    records = recsVal.map((r) => parseUsageRecord(r));
  }
  return {
    records,
    nextCursor: optString(obj, "next_cursor", "UsageRecordPage"),
    raw: obj,
  };
}

/**
 * One group within a {@link UsageSummary}.
 */
export interface UsageSummaryGroup {
  key: Record<string, string | null>;
  records: number | null;
  requests: number | null;
  errors: number | null;
  denied: number | null;
  unknownStatus: number | null;
  unknownUsageRecords: number | null;
  unknownCostRecords: number | null;
  tokens: Record<string, number>;
  tokensUnknown: Record<string, number>;
  cost: CostTotal[];
  /** Present when the summary asked for feedback. */
  feedback: FeedbackTotals | null;
  /** Answers served from the response cache (cost basis "cached"); not provider usage, not unknown cost. */
  cachedResponseRecords: number | null;
  raw: Record<string, unknown>;
}

/**
 * Parses a {@link UsageSummaryGroup}.
 */
export function parseUsageSummaryGroup(value: unknown): UsageSummaryGroup {
  const obj = asObject(value, "UsageSummaryGroup");
  return {
    key: nullableStringMap(obj, "key", "UsageSummaryGroup"),
    records: optInt(obj, "records", "UsageSummaryGroup"),
    requests: optInt(obj, "requests", "UsageSummaryGroup"),
    errors: optInt(obj, "errors", "UsageSummaryGroup"),
    denied: optInt(obj, "denied", "UsageSummaryGroup"),
    unknownStatus: optInt(obj, "unknown_status", "UsageSummaryGroup"),
    unknownUsageRecords: optInt(
      obj,
      "unknown_usage_records",
      "UsageSummaryGroup",
    ),
    unknownCostRecords: optInt(
      obj,
      "unknown_cost_records",
      "UsageSummaryGroup",
    ),
    tokens: intMap(obj, "tokens", "UsageSummaryGroup"),
    tokensUnknown: intMap(obj, "tokens_unknown", "UsageSummaryGroup"),
    cost: parseCostTotalList(obj, "cost", "UsageSummaryGroup"),
    feedback:
      optObject(obj, "feedback", "UsageSummaryGroup") !== null
        ? parseFeedbackTotals(obj["feedback"])
        : null,
    cachedResponseRecords: optInt(obj, "cached_response_records", "UsageSummaryGroup"),
    raw: obj,
  };
}

/**
 * Aggregated usage summary response.
 */
export interface UsageSummary {
  groupBy: string[];
  groups: UsageSummaryGroup[];
  recordsScanned: number | null;
  truncated: boolean | null;
  /** True when managed and reported records are separate groups. */
  sourceSplit: boolean | null;
  /** Reported records left out because they describe a request that has a managed record; null when the server omitted it (zero is omitted). */
  excludedCorrelatedReported: number | null;
  /** How sources were treated, in plain words. */
  notes: string[];
  raw: Record<string, unknown>;
}

/**
 * Parses a {@link UsageSummary}.
 */
export function parseUsageSummary(value: unknown): UsageSummary {
  const obj = asObject(value, "UsageSummary");
  const groupsVal = obj["groups"];
  let groups: UsageSummaryGroup[] = [];
  if (groupsVal !== undefined && groupsVal !== null) {
    if (!Array.isArray(groupsVal)) {
      throw decodeError("UsageSummary.groups: expected array");
    }
    groups = groupsVal.map((g) => parseUsageSummaryGroup(g));
  }
  return {
    groupBy: stringList(obj, "group_by", "UsageSummary"),
    groups,
    recordsScanned: optInt(obj, "records_scanned", "UsageSummary"),
    truncated: optBoolean(obj, "truncated", "UsageSummary"),
    sourceSplit: optBoolean(obj, "source_split", "UsageSummary"),
    excludedCorrelatedReported: optInt(
      obj,
      "excluded_correlated_reported",
      "UsageSummary",
    ),
    notes: stringList(obj, "notes", "UsageSummary"),
    raw: obj,
  };
}

/**
 * Usage totals block embedded in overview responses.
 */
export interface UsageTotals {
  requests: number | null;
  records: number | null;
  errors: number | null;
  denied: number | null;
  tokens: Record<string, number>;
  tokensUnknown: Record<string, number>;
  cost: CostTotal[];
  unknownCostRecords: number | null;
  raw: Record<string, unknown>;
}

/**
 * Parses a {@link UsageTotals} block.
 */
export function parseUsageTotals(value: unknown): UsageTotals {
  const obj = asObject(value, "UsageTotals");
  return {
    requests: optInt(obj, "requests", "UsageTotals"),
    records: optInt(obj, "records", "UsageTotals"),
    errors: optInt(obj, "errors", "UsageTotals"),
    denied: optInt(obj, "denied", "UsageTotals"),
    tokens: intMap(obj, "tokens", "UsageTotals"),
    tokensUnknown: intMap(obj, "tokens_unknown", "UsageTotals"),
    cost: parseCostTotalList(obj, "cost", "UsageTotals"),
    unknownCostRecords: optInt(obj, "unknown_cost_records", "UsageTotals"),
    raw: obj,
  };
}

/**
 * Rational price rate (numerator / denominator minor units per unit of meter).
 */
export interface ModelPriceRate {
  numerator: number | null;
  denominator: number | null;
  raw?: Record<string, unknown>;
}

/**
 * Parses a {@link ModelPriceRate}.
 */
export function parseModelPriceRate(value: unknown): ModelPriceRate {
  const obj = asObject(value, "ModelPriceRate");
  return {
    numerator: optInt(obj, "numerator", "ModelPriceRate"),
    denominator: optInt(obj, "denominator", "ModelPriceRate"),
    raw: obj,
  };
}

/**
 * Long-context override: above this total input the whole request uses these meters.
 */
export interface ModelPriceTier {
  aboveInputTokens: number | null;
  meters: Record<string, ModelPriceRate>;
}

function parseMeters(value: unknown, context: string): Record<string, ModelPriceRate> {
  const meters: Record<string, ModelPriceRate> = {};
  if (value !== undefined && value !== null) {
    for (const [k, v] of Object.entries(asObject(value, context))) {
      if (v !== null && v !== undefined) {
        meters[k] = parseModelPriceRate(v);
      }
    }
  }
  return meters;
}

/**
 * Model price specification.
 */
export interface ModelPrice {
  id: string;
  modelId: string | null;
  currency: string | null;
  scale: number | null;
  meters: Record<string, ModelPriceRate>;
  /** Matches records served on this tier; without it a price covers standard-tier exchanges. */
  serviceTier?: string | null;
  /** Long-context overrides, ascending by threshold. */
  tiers?: ModelPriceTier[];
  raw?: Record<string, unknown>;
}

/**
 * Parses a {@link ModelPrice} specification.
 */
export function parseModelPrice(value: unknown): ModelPrice {
  const obj = asObject(value, "ModelPrice");
  const metersVal = obj["meters"];
  const meters: Record<string, ModelPriceRate> = {};
  if (metersVal !== undefined && metersVal !== null) {
    const metersObj = asObject(metersVal, "ModelPrice.meters");
    for (const [k, v] of Object.entries(metersObj)) {
      if (v === null || v === undefined) {
        continue;
      }
      meters[k] = parseModelPriceRate(v);
    }
  }
  return {
    id: reqString(obj, "id", "ModelPrice"),
    modelId: optString(obj, "model_id", "ModelPrice"),
    currency: optString(obj, "currency", "ModelPrice"),
    scale: optInt(obj, "scale", "ModelPrice"),
    meters,
    serviceTier: optString(obj, "service_tier", "ModelPrice"),
    tiers: Array.isArray(obj["tiers"])
      ? (obj["tiers"] as unknown[]).map((t) => {
          const tier = asObject(t, "ModelPrice.tiers");
          return {
            aboveInputTokens: optInt(tier, "above_input_tokens", "ModelPrice.tiers"),
            meters: parseMeters(tier["meters"], "ModelPrice.tiers.meters"),
          };
        })
      : [],
    raw: obj,
  };
}

/**
 * Converts a {@link ModelPrice} into its snake_case wire representation.
 */
export function toWireModelPrice(price: ModelPrice): Record<string, unknown> {
  const meters: Record<string, unknown> = {};
  for (const [k, rate] of Object.entries(price.meters)) {
    const r: Record<string, unknown> = {};
    if (rate.numerator !== null && rate.numerator !== undefined) {
      r["numerator"] = rate.numerator;
    }
    if (rate.denominator !== null && rate.denominator !== undefined) {
      r["denominator"] = rate.denominator;
    }
    meters[k] = r;
  }
  const out: Record<string, unknown> = {
    id: price.id,
    meters,
  };
  if (price.modelId !== null && price.modelId !== undefined) {
    out["model_id"] = price.modelId;
  }
  if (price.currency !== null && price.currency !== undefined) {
    out["currency"] = price.currency;
  }
  if (price.scale !== null && price.scale !== undefined) {
    out["scale"] = price.scale;
  }
  if (price.serviceTier !== null && price.serviceTier !== undefined) {
    out["service_tier"] = price.serviceTier;
  }
  if (price.tiers !== undefined && price.tiers.length > 0) {
    out["tiers"] = price.tiers.map((t) => {
      const tm: Record<string, unknown> = {};
      for (const [k, rate] of Object.entries(t.meters)) {
        const r: Record<string, unknown> = {};
        if (rate.numerator !== null && rate.numerator !== undefined) r["numerator"] = rate.numerator;
        if (rate.denominator !== null && rate.denominator !== undefined) r["denominator"] = rate.denominator;
        tm[k] = r;
      }
      const tier: Record<string, unknown> = { meters: tm };
      if (t.aboveInputTokens !== null) tier["above_input_tokens"] = t.aboveInputTokens;
      return tier;
    });
  }
  return out;
}

/**
 * Stored model price resource record.
 */
export interface ModelPriceRecord {
  id: string;
  revision: number;
  tenantId: string | null;
  kind: string | null;
  price: ModelPrice;
  raw: Record<string, unknown>;
}

/**
 * Parses a {@link ModelPriceRecord}.
 */
export function parseModelPriceRecord(value: unknown): ModelPriceRecord {
  const obj = asObject(value, "ModelPriceRecord");
  const specObj = asObject(obj["spec"], "ModelPriceRecord.spec");
  return {
    id: reqString(obj, "id", "ModelPriceRecord"),
    revision: reqInt(obj, "revision", "ModelPriceRecord"),
    tenantId: optString(obj, "tenant_id", "ModelPriceRecord"),
    kind: optString(obj, "kind", "ModelPriceRecord"),
    price: parseModelPrice(specObj),
    raw: obj,
  };
}

/**
 * Stored LLM model surface resource record.
 */
export interface ModelSurface {
  id: string;
  revision: number;
  tenantId: string | null;
  kind: string | null;
  protocol: string | null;
  providerId: string | null;
  models: string[];
  raw: Record<string, unknown>;
}

/**
 * Parses a {@link ModelSurface}.
 */
export function parseModelSurface(value: unknown): ModelSurface {
  const obj = asObject(value, "ModelSurface");
  const specObj = optObject(obj, "spec", "ModelSurface");
  const surfaceObj =
    specObj !== null ? optObject(specObj, "surface", "ModelSurface.spec") : null;
  const providerObj =
    surfaceObj !== null
      ? optObject(surfaceObj, "provider", "ModelSurface.spec.surface")
      : null;
  return {
    id: reqString(obj, "id", "ModelSurface"),
    revision: reqInt(obj, "revision", "ModelSurface"),
    tenantId: optString(obj, "tenant_id", "ModelSurface"),
    kind: optString(obj, "kind", "ModelSurface"),
    protocol:
      surfaceObj !== null
        ? optString(surfaceObj, "protocol", "ModelSurface.spec.surface")
        : null,
    providerId:
      providerObj !== null
        ? optString(providerObj, "id", "ModelSurface.spec.surface.provider")
        : null,
    models:
      surfaceObj !== null
        ? stringList(surfaceObj, "models", "ModelSurface.spec.surface")
        : [],
    raw: obj,
  };
}

/**
 * Stored MCP tool connection resource record.
 */
export interface ToolConnection {
  id: string;
  revision: number;
  tenantId: string | null;
  kind: string | null;
  resourceUrl: string | null;
  authType: string | null;
  allowedTools: string[];
  raw: Record<string, unknown>;
}

/**
 * Parses a {@link ToolConnection}.
 */
export function parseToolConnection(value: unknown): ToolConnection {
  const obj = asObject(value, "ToolConnection");
  const specObj = optObject(obj, "spec", "ToolConnection");
  const authObj =
    specObj !== null ? optObject(specObj, "auth", "ToolConnection.spec") : null;
  const execObj =
    specObj !== null
      ? optObject(specObj, "execution", "ToolConnection.spec")
      : null;
  return {
    id: reqString(obj, "id", "ToolConnection"),
    revision: reqInt(obj, "revision", "ToolConnection"),
    tenantId: optString(obj, "tenant_id", "ToolConnection"),
    kind: optString(obj, "kind", "ToolConnection"),
    resourceUrl:
      specObj !== null
        ? optString(specObj, "resource_url", "ToolConnection.spec")
        : null,
    authType:
      authObj !== null
        ? optString(authObj, "type", "ToolConnection.spec.auth")
        : null,
    allowedTools:
      execObj !== null
        ? stringList(execObj, "allowed_tools", "ToolConnection.spec.execution")
        : [],
    raw: obj,
  };
}

/**
 * Summary item in an overview access point page.
 */
export interface OverviewAccessPointSummary {
  id: string;
  displayName: string | null;
  callerKinds: string[];
  counts: Record<string, number>;
  quotaCoverageState: string | null;
  quotaCoverageDetail: string | null;
  raw: Record<string, unknown>;
}

/**
 * Parses an {@link OverviewAccessPointSummary}.
 */
export function parseOverviewAccessPointSummary(
  value: unknown,
): OverviewAccessPointSummary {
  const obj = asObject(value, "OverviewAccessPointSummary");
  const qcObj = optObject(
    obj,
    "quota_coverage",
    "OverviewAccessPointSummary",
  );
  return {
    id: reqString(obj, "id", "OverviewAccessPointSummary"),
    displayName: optString(obj, "display_name", "OverviewAccessPointSummary"),
    callerKinds: stringList(
      obj,
      "caller_kinds",
      "OverviewAccessPointSummary",
    ),
    counts: intMap(obj, "counts", "OverviewAccessPointSummary"),
    quotaCoverageState:
      qcObj !== null
        ? optString(
            qcObj,
            "state",
            "OverviewAccessPointSummary.quota_coverage",
          )
        : null,
    quotaCoverageDetail:
      qcObj !== null
        ? optString(
            qcObj,
            "detail",
            "OverviewAccessPointSummary.quota_coverage",
          )
        : null,
    raw: obj,
  };
}

/**
 * Paginated list of overview access point summaries.
 */
export interface OverviewAccessPointPage {
  accessPoints: OverviewAccessPointSummary[];
  nextCursor: string | null;
  raw: Record<string, unknown>;
}

/**
 * Parses an {@link OverviewAccessPointPage}.
 */
export function parseOverviewAccessPointPage(
  value: unknown,
): OverviewAccessPointPage {
  const obj = asObject(value, "OverviewAccessPointPage");
  const apVal = obj["access_points"];
  let accessPoints: OverviewAccessPointSummary[] = [];
  if (apVal !== undefined && apVal !== null) {
    if (!Array.isArray(apVal)) {
      throw decodeError("OverviewAccessPointPage.access_points: expected array");
    }
    accessPoints = apVal.map((item) => parseOverviewAccessPointSummary(item));
  }
  return {
    accessPoints,
    nextCursor: optString(obj, "next_cursor", "OverviewAccessPointPage"),
    raw: obj,
  };
}

/**
 * Detailed overview of a single access point.
 */
export interface OverviewAccessPointDetail {
  id: string;
  displayName: string | null;
  revision: number | null;
  since: string | null;
  usageAvailable: boolean | null;
  usageTruncated: boolean | null;
  usage: UsageTotals | null;
  raw: Record<string, unknown>;
}

/**
 * Parses an {@link OverviewAccessPointDetail}.
 */
export function parseOverviewAccessPointDetail(
  value: unknown,
): OverviewAccessPointDetail {
  const obj = asObject(value, "OverviewAccessPointDetail");
  const apObj = optObject(obj, "access_point", "OverviewAccessPointDetail");
  const id =
    apObj !== null
      ? reqString(apObj, "id", "OverviewAccessPointDetail.access_point")
      : reqString(obj, "id", "OverviewAccessPointDetail");
  const displayName =
    apObj !== null
      ? optString(
          apObj,
          "display_name",
          "OverviewAccessPointDetail.access_point",
        )
      : optString(obj, "display_name", "OverviewAccessPointDetail");
  const usageWrapper = optObject(obj, "usage", "OverviewAccessPointDetail");
  const usageAvailable =
    usageWrapper !== null
      ? optBoolean(usageWrapper, "available", "OverviewAccessPointDetail.usage")
      : null;
  const usageTruncated =
    usageWrapper !== null
      ? optBoolean(usageWrapper, "truncated", "OverviewAccessPointDetail.usage")
      : null;
  const totalObj =
    usageWrapper !== null
      ? optObject(usageWrapper, "total", "OverviewAccessPointDetail.usage")
      : null;
  return {
    id,
    displayName,
    revision: optInt(obj, "revision", "OverviewAccessPointDetail"),
    since: optString(obj, "since", "OverviewAccessPointDetail"),
    usageAvailable,
    usageTruncated,
    usage: totalObj !== null ? parseUsageTotals(totalObj) : null,
    raw: obj,
  };
}

/**
 * Summary of a caller subject in an overview subjects page.
 */
export interface OverviewSubject {
  kind: string;
  id: string;
  displayName: string | null;
  identity: Record<string, unknown> | null;
  accessPoints: string[];
  lastSeen: string | null;
  knownFrom: string[];
  usage: UsageTotals | null;
  raw: Record<string, unknown>;
}

/**
 * Parses an {@link OverviewSubject}.
 */
export function parseOverviewSubject(value: unknown): OverviewSubject {
  const obj = asObject(value, "OverviewSubject");
  const usageObj = optObject(obj, "usage", "OverviewSubject");
  return {
    kind: reqString(obj, "kind", "OverviewSubject"),
    id: reqString(obj, "id", "OverviewSubject"),
    displayName: optString(obj, "display_name", "OverviewSubject"),
    identity: optObject(obj, "identity", "OverviewSubject"),
    accessPoints: stringList(obj, "access_points", "OverviewSubject"),
    lastSeen: optString(obj, "last_seen", "OverviewSubject"),
    knownFrom: stringList(obj, "known_from", "OverviewSubject"),
    usage: usageObj !== null ? parseUsageTotals(usageObj) : null,
    raw: obj,
  };
}

/**
 * Paginated list of overview subjects.
 */
export interface OverviewSubjectPage {
  kind: string;
  since: string | null;
  subjects: OverviewSubject[];
  usageAvailable: boolean | null;
  usageTruncated: boolean | null;
  derivedFrom: string[];
  limitations: string[];
  nextCursor: string | null;
  raw: Record<string, unknown>;
}

/**
 * Parses an {@link OverviewSubjectPage}.
 */
export function parseOverviewSubjectPage(value: unknown): OverviewSubjectPage {
  const obj = asObject(value, "OverviewSubjectPage");
  const subjVal = obj["subjects"];
  let subjects: OverviewSubject[] = [];
  if (subjVal !== undefined && subjVal !== null) {
    if (!Array.isArray(subjVal)) {
      throw decodeError("OverviewSubjectPage.subjects: expected array");
    }
    subjects = subjVal.map((s) => parseOverviewSubject(s));
  }
  return {
    kind: reqString(obj, "kind", "OverviewSubjectPage"),
    since: optString(obj, "since", "OverviewSubjectPage"),
    subjects,
    usageAvailable: optBoolean(obj, "usage_available", "OverviewSubjectPage"),
    usageTruncated: optBoolean(obj, "usage_truncated", "OverviewSubjectPage"),
    derivedFrom: stringList(obj, "derived_from", "OverviewSubjectPage"),
    limitations: stringList(obj, "limitations", "OverviewSubjectPage"),
    nextCursor: optString(obj, "next_cursor", "OverviewSubjectPage"),
    raw: obj,
  };
}

/**
 * Detailed overview of a single caller subject.
 */
export interface OverviewSubjectDetail {
  kind: string;
  id: string;
  identity: Record<string, unknown> | null;
  accessPoints: Record<string, unknown>[];
  since: string | null;
  lastSeen: string | null;
  usage: UsageTotals | null;
  notes: string[];
  knownFrom: string[];
  raw: Record<string, unknown>;
}

/**
 * Parses an {@link OverviewSubjectDetail}.
 */
export function parseOverviewSubjectDetail(
  value: unknown,
): OverviewSubjectDetail {
  const obj = asObject(value, "OverviewSubjectDetail");
  const usageObj = optObject(obj, "usage", "OverviewSubjectDetail");
  return {
    kind: reqString(obj, "kind", "OverviewSubjectDetail"),
    id: reqString(obj, "id", "OverviewSubjectDetail"),
    identity: optObject(obj, "identity", "OverviewSubjectDetail"),
    accessPoints: objectList(obj, "access_points", "OverviewSubjectDetail"),
    since: optString(obj, "since", "OverviewSubjectDetail"),
    lastSeen: optString(obj, "last_seen", "OverviewSubjectDetail"),
    usage: usageObj !== null ? parseUsageTotals(usageObj) : null,
    notes: stringList(obj, "notes", "OverviewSubjectDetail"),
    knownFrom: stringList(obj, "known_from", "OverviewSubjectDetail"),
    raw: obj,
  };
}

/**
 * Helper to parse a JSON array using an element parser.
 */
export function parseList<T>(
  value: unknown,
  context: string,
  itemParser: (item: unknown) => T,
): T[] {
  if (!Array.isArray(value)) {
    throw decodeError(`${context}: expected JSON array`);
  }
  return value.map((item) => itemParser(item));
}

/**
 * A budget's counters in its current window. Amounts are exact decimal
 * strings. Operator-only fields are null or empty for a member: unknown, not zero.
 */
export interface BudgetStatus {
  id: string;
  displayName: string | null;
  parent: string | null;
  unit: string;
  currency: string | null;
  limit: string;
  spent: string;
  reserved: string;
  remaining: string;
  overLimit: boolean;
  windowStart: string | null;
  windowEnd: string | null;
  unknownCostRequests: number | null;
  thresholdEvents: Array<Record<string, unknown>>;
  children: Array<Record<string, unknown>>;
  topSpenders: Array<Record<string, unknown>>;
  raw: Record<string, unknown>;
}

function budgetObjectList(obj: Record<string, unknown>, key: string): Array<Record<string, unknown>> {
  const val = obj[key];
  return Array.isArray(val)
    ? val.filter((x): x is Record<string, unknown> => typeof x === "object" && x !== null && !Array.isArray(x))
    : [];
}

export function parseBudgetStatus(data: unknown): BudgetStatus {
  const obj = asObject(data, "BudgetStatus");
  const window = optObject(obj, "window", "BudgetStatus") ?? {};
  return {
    id: reqString(obj, "id", "BudgetStatus"),
    displayName: optString(obj, "display_name", "BudgetStatus"),
    parent: optString(obj, "parent", "BudgetStatus"),
    unit: reqString(obj, "unit", "BudgetStatus"),
    currency: optString(obj, "currency", "BudgetStatus"),
    limit: reqString(obj, "limit", "BudgetStatus"),
    spent: reqString(obj, "spent", "BudgetStatus"),
    reserved: reqString(obj, "reserved", "BudgetStatus"),
    remaining: reqString(obj, "remaining", "BudgetStatus"),
    overLimit: optBoolean(obj, "over_limit", "BudgetStatus") === true,
    windowStart: optString(window, "start", "BudgetStatus.window"),
    windowEnd: optString(window, "end", "BudgetStatus.window"),
    unknownCostRequests: optInt(obj, "unknown_cost_requests", "BudgetStatus"),
    thresholdEvents: budgetObjectList(obj, "threshold_events"),
    children: budgetObjectList(obj, "children"),
    topSpenders: budgetObjectList(obj, "top_spenders"),
    raw: obj,
  };
}

export {
  asObject,
  decodeError,
  objectList,
  optBoolean,
  optInt,
  optObject,
  optString,
  reqInt,
  reqString,
  stringList,
};
