/**
 * Types and parsers for the administrative operations: budgets, model
 * surface and tool connection writes, hook policies, usage configuration.
 *
 * Specifications you write (`BudgetSpec`, `ModelSurfaceSpec`,
 * `ToolConnectionSpec`, `HookPolicySpec`) use the stored JSON field names
 * (snake_case), exactly as the API and the Go SDK do, so a read-modify-write
 * never drops a field. Results of dry runs and status reads are camelCase.
 * An absent optional value is `undefined`, never `0` or an empty string.
 * Secrets are references (`secret_ref`, `auth_secret_ref`), never values.
 */
import {
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
} from "./models.js";

function u<T>(value: T | null): T | undefined {
  return value === null ? undefined : value;
}

function reqBoolean(obj: Record<string, unknown>, key: string, context: string): boolean {
  const val = obj[key];
  if (typeof val !== "boolean") {
    throw decodeError(`${context}.${key}: expected boolean`);
  }
  return val;
}

function reqObject(
  obj: Record<string, unknown>,
  key: string,
  context: string,
): Record<string, unknown> {
  return asObject(obj[key], `${context}.${key}`);
}

function optNum(obj: Record<string, unknown>, key: string, context: string): number | undefined {
  const val = obj[key];
  if (val === undefined || val === null) {
    return undefined;
  }
  if (typeof val !== "number" || !Number.isFinite(val)) {
    throw decodeError(`${context}.${key}: expected number`);
  }
  return val;
}

// ---------------------------------------------------------------- budgets

/** A group selector: a source-qualified group from the verified token. */
export interface GroupRef {
  source: string;
  id: string;
}

/** What a budget caps. `amount` is an exact decimal string. */
export interface BudgetLimit {
  /** tokens, requests or cost. */
  unit: string;
  currency?: string;
  amount: string;
}

/** The budget period. */
export interface BudgetWindowSpec {
  /** calendar or tumbling. */
  kind: string;
  /** calendar: day, week or month. */
  unit?: string;
  /** calendar: IANA zone. */
  time_zone?: string;
  /** tumbling: window length in hours. */
  hours?: number;
}

/** Who draws from the budget. A request draws when any selector matches. */
export interface BudgetMembers {
  groups?: GroupRef[];
  principals?: string[];
  agents?: string[];
  client_registrations?: string[];
  access_points?: string[];
  projects?: string[];
}

/** The saved specification of a tenant budget (stored JSON). */
export interface BudgetSpec {
  id: string;
  display_name?: string;
  parent?: string;
  limit: BudgetLimit;
  window: BudgetWindowSpec;
  members?: BudgetMembers;
  /** deny (default) or allow_and_flag. */
  on_unknown_cost?: string;
  thresholds?: number[];
}

/** A stored budget with the revision to pass to {@link AiplexClient.putBudget} and `deleteBudget`. */
export interface BudgetRecord {
  id: string;
  revision: number;
  tenantId?: string;
  kind?: string;
  spec: BudgetSpec;
  raw: Record<string, unknown>;
}

export function parseBudgetSpec(value: unknown, context = "BudgetSpec"): BudgetSpec {
  const obj = asObject(value, context);
  reqString(obj, "id", context);
  const limit = reqObject(obj, "limit", context);
  reqString(limit, "unit", `${context}.limit`);
  reqString(limit, "amount", `${context}.limit`);
  parseBudgetWindow(obj["window"], `${context}.window`);
  return withoutNulls(obj) as unknown as BudgetSpec;
}

/**
 * Drops object members whose value is null (at any depth), keeping every
 * other member, known or not, so a read-modify-write loses nothing. A null
 * becomes "absent" (`undefined`), never `0` or an empty string.
 */
function withoutNulls(value: unknown): unknown {
  if (Array.isArray(value)) {
    return value.map(withoutNulls);
  }
  if (value !== null && typeof value === "object") {
    const out: Record<string, unknown> = {};
    for (const [k, v] of Object.entries(value as Record<string, unknown>)) {
      if (v !== null) {
        out[k] = withoutNulls(v);
      }
    }
    return out;
  }
  return value;
}

function parseBudgetWindow(value: unknown, context: string): BudgetWindowSpec {
  const obj = asObject(value, context);
  reqString(obj, "kind", context);
  optString(obj, "unit", context);
  optString(obj, "time_zone", context);
  optInt(obj, "hours", context);
  return withoutNulls(obj) as unknown as BudgetWindowSpec;
}

export function parseBudgetRecord(value: unknown): BudgetRecord {
  const obj = asObject(value, "BudgetRecord");
  return {
    id: reqString(obj, "id", "BudgetRecord"),
    revision: reqInt(obj, "revision", "BudgetRecord"),
    tenantId: u(optString(obj, "tenant_id", "BudgetRecord")),
    kind: u(optString(obj, "kind", "BudgetRecord")),
    spec: parseBudgetSpec(obj["spec"], "BudgetRecord.spec"),
    raw: obj,
  };
}

/** A hypothetical caller. Nothing in it is verified. */
export interface BudgetPreviewSubject {
  principal?: string;
  groups?: GroupRef[];
  agent?: string;
  client?: string;
}

/** Asks which budgets a request would draw from. */
export interface BudgetPreviewInput {
  subject: BudgetPreviewSubject;
  accessPoint?: string;
  /** llmplex (default), mcplex or a2aplex. */
  plane?: "llmplex" | "mcplex" | "a2aplex";
  /** A specification validated as if saved and included in the answer; never stored. */
  draft?: BudgetSpec;
}

export function toWireBudgetPreview(input: BudgetPreviewInput): Record<string, unknown> {
  const body: Record<string, unknown> = { subject: input.subject };
  if (input.accessPoint !== undefined && input.accessPoint !== "") {
    body["access_point"] = input.accessPoint;
  }
  if (input.plane !== undefined) {
    if (input.plane !== "llmplex" && input.plane !== "mcplex" && input.plane !== "a2aplex") {
      throw new TypeError("plane must be llmplex, mcplex or a2aplex");
    }
    body["plane"] = input.plane;
  }
  if (input.draft !== undefined) {
    body["draft"] = input.draft;
  }
  return body;
}

/** One budget the hypothetical request would draw from. */
export interface BudgetPreviewDraw {
  id: string;
  displayName?: string;
  parent?: string;
  direct: boolean;
  reasons: string[];
  viaChild?: string;
  unit: string;
  currency?: string;
  limit: string;
  window: BudgetWindowSpec;
  /** True for the draft budget. */
  draft: boolean;
}

/** A matched budget that does not count this plane. */
export interface BudgetPreviewSkipped {
  id: string;
  reason: string;
}

/** The draft's validation result. */
export interface BudgetPreviewDraft {
  valid: boolean;
  field?: string;
  error?: string;
}

/** The dry-run answer of {@link AiplexClient.previewBudget}. */
export interface BudgetPreview {
  hypothetical: boolean;
  plane: string;
  accessPoint?: string;
  project?: string;
  draws: BudgetPreviewDraw[];
  skipped: BudgetPreviewSkipped[];
  draft?: BudgetPreviewDraft;
  notes: string[];
  raw: Record<string, unknown>;
}

export function parseBudgetPreview(value: unknown): BudgetPreview {
  const ctx = "BudgetPreview";
  const obj = asObject(value, ctx);
  const draftObj = optObject(obj, "draft", ctx);
  return {
    hypothetical: reqBoolean(obj, "hypothetical", ctx),
    plane: reqString(obj, "plane", ctx),
    accessPoint: u(optString(obj, "access_point", ctx)),
    project: u(optString(obj, "project", ctx)),
    draws: objectList(obj, "draws", ctx).map((d, i) => {
      const c = `${ctx}.draws[${i}]`;
      return {
        id: reqString(d, "id", c),
        displayName: u(optString(d, "display_name", c)),
        parent: u(optString(d, "parent", c)),
        direct: reqBoolean(d, "direct", c),
        reasons: stringList(d, "reasons", c),
        viaChild: u(optString(d, "via_child", c)),
        unit: reqString(d, "unit", c),
        currency: u(optString(d, "currency", c)),
        limit: reqString(d, "limit", c),
        window: parseBudgetWindow(d["window"], `${c}.window`),
        draft: optBoolean(d, "draft", c) === true,
      };
    }),
    skipped: objectList(obj, "skipped", ctx).map((s, i) => ({
      id: reqString(s, "id", `${ctx}.skipped[${i}]`),
      reason: reqString(s, "reason", `${ctx}.skipped[${i}]`),
    })),
    draft:
      draftObj === null
        ? undefined
        : {
            valid: reqBoolean(draftObj, "valid", `${ctx}.draft`),
            field: u(optString(draftObj, "field", `${ctx}.draft`)),
            error: u(optString(draftObj, "error", `${ctx}.draft`)),
          },
    notes: stringList(obj, "notes", ctx),
    raw: obj,
  };
}

// --------------------------------------------------------- model surfaces

/** Pinned provider block of a surface. Unknown server fields are accepted. */
export interface ModelSurfaceProvider {
  /** openai, anthropic, azure-openai, bedrock, openai-compatible or gemini-developer. */
  id: string;
  base_path?: string;
  api_version?: string;
  region?: string;
  organization?: string;
  project?: string;
  anthropic_version?: string;
  anthropic_betas?: string[];
  stream_usage?: string;
  limit_header_passthrough?: boolean;
  usage_semantics?: string;
  [field: string]: unknown;
}

/** The surface block of a model surface specification. */
export interface ModelSurfaceBlock {
  id: string;
  protocol: string;
  origin: string;
  models?: string[];
  /** Catalog operations exactly as the provider descriptor returns them. */
  operations?: Array<Record<string, unknown>>;
  /** A non-Google provider; absent for a Google Cloud surface. */
  provider?: ModelSurfaceProvider;
  websocket?: {
    metering?: string;
    max_session_seconds?: number;
    token_allowance?: number;
    [field: string]: unknown;
  };
  api_version?: string;
  project?: string;
  location?: string;
  publisher?: string;
  data_prefixes?: string[];
  agents?: string[];
  endpoint?: string;
  [field: string]: unknown;
}

/** A credential reference. `secret_ref` names a tenant secret, never a value. */
export interface ModelSurfaceAuth {
  type: string;
  in?: string;
  name?: string;
  prefix?: string;
  secret_ref?: string;
  [field: string]: unknown;
}

/** Opts a provider surface into advisory enforcement of provider limit signals. */
export interface ModelCapacityPolicySpec {
  cooldown_enabled?: boolean;
  max_cooldown_seconds?: number;
  headroom_fraction?: number;
  min_weight?: number;
  default_weight?: number;
  signal_max_age_seconds?: number;
  [field: string]: unknown;
}

/**
 * The spec of a model-surface resource. A provider surface has exactly one of
 * `auth` and `identity_ref`; the server validates this.
 */
export interface ModelSurfaceSpec {
  surface: ModelSurfaceBlock;
  identity_ref?: string;
  network_profile_ref?: string;
  billing_project?: string;
  auth?: ModelSurfaceAuth;
  capacity_policy?: ModelCapacityPolicySpec;
  [field: string]: unknown;
}

/** One provider limit window. An absent member is unknown, not zero. */
export interface LimitWindow {
  limit?: number;
  remaining?: number;
  resetAt?: string;
}

/** The last provider rate-limit observation for a surface. */
export interface ProviderLimits {
  provider: string;
  observedAt: string;
  httpStatus?: number;
  classification?: string;
  retryAfterMs?: number;
  requests?: LimitWindow;
  tokens?: LimitWindow;
  inputTokens?: LimitWindow;
  outputTokens?: LimitWindow;
}

/** The capacity policy with server defaults applied. Absent members were not set. */
export interface CapacityPolicy {
  cooldownEnabled?: boolean;
  maxCooldownSeconds?: number;
  headroomFraction?: number;
  minWeight?: number;
  defaultWeight?: number;
  signalMaxAgeSeconds?: number;
}

/** The capacity gate's cooldown phase (none, cooling, half-open or probing). */
export interface CooldownState {
  phase: string;
  until?: string;
  retryAfterMs?: number;
}

/** The policy a surface enforces and its cooldown; both absent while the policy is off. */
export interface CapacityPolicyState {
  enabled: boolean;
  policy?: CapacityPolicy;
  cooldown?: CooldownState;
}

/** A surface's capacity state on the replica that answered. Advisory. */
export interface SurfaceCapacity {
  surfaceId: string;
  observed: boolean;
  scope: string;
  limits?: ProviderLimits;
  capacityPolicy: CapacityPolicyState;
  raw: Record<string, unknown>;
}

function parseLimitWindow(obj: Record<string, unknown>, key: string, context: string): LimitWindow | undefined {
  const w = optObject(obj, key, context);
  if (w === null) {
    return undefined;
  }
  const c = `${context}.${key}`;
  return {
    limit: u(optInt(w, "limit", c)),
    remaining: u(optInt(w, "remaining", c)),
    resetAt: u(optString(w, "reset_at", c)),
  };
}

export function parseSurfaceCapacity(value: unknown): SurfaceCapacity {
  const ctx = "SurfaceCapacity";
  const obj = asObject(value, ctx);
  const limitsObj = optObject(obj, "limits", ctx);
  const lc = `${ctx}.limits`;
  const policyState = reqObject(obj, "capacity_policy", ctx);
  const pc = `${ctx}.capacity_policy`;
  const policyObj = optObject(policyState, "policy", pc);
  const cooldownObj = optObject(policyState, "cooldown", pc);
  return {
    surfaceId: reqString(obj, "surface_id", ctx),
    observed: reqBoolean(obj, "observed", ctx),
    scope: reqString(obj, "scope", ctx),
    limits:
      limitsObj === null
        ? undefined
        : {
            provider: reqString(limitsObj, "provider", lc),
            observedAt: reqString(limitsObj, "observed_at", lc),
            httpStatus: u(optInt(limitsObj, "http_status", lc)),
            classification: u(optString(limitsObj, "classification", lc)),
            retryAfterMs: u(optInt(limitsObj, "retry_after_ms", lc)),
            requests: parseLimitWindow(limitsObj, "requests", lc),
            tokens: parseLimitWindow(limitsObj, "tokens", lc),
            inputTokens: parseLimitWindow(limitsObj, "input_tokens", lc),
            outputTokens: parseLimitWindow(limitsObj, "output_tokens", lc),
          },
    capacityPolicy: {
      enabled: reqBoolean(policyState, "enabled", pc),
      policy:
        policyObj === null
          ? undefined
          : {
              cooldownEnabled: u(optBoolean(policyObj, "cooldown_enabled", `${pc}.policy`)),
              maxCooldownSeconds: u(optInt(policyObj, "max_cooldown_seconds", `${pc}.policy`)),
              headroomFraction: optNum(policyObj, "headroom_fraction", `${pc}.policy`),
              minWeight: u(optInt(policyObj, "min_weight", `${pc}.policy`)),
              defaultWeight: u(optInt(policyObj, "default_weight", `${pc}.policy`)),
              signalMaxAgeSeconds: u(optInt(policyObj, "signal_max_age_seconds", `${pc}.policy`)),
            },
      cooldown:
        cooldownObj === null
          ? undefined
          : {
              phase: reqString(cooldownObj, "phase", `${pc}.cooldown`),
              until: u(optString(cooldownObj, "until", `${pc}.cooldown`)),
              retryAfterMs: u(optInt(cooldownObj, "retry_after_ms", `${pc}.cooldown`)),
            },
    },
    raw: obj,
  };
}

// ------------------------------------------------------ tool connections

/** The managed execution block of a tool connection. */
export interface ToolConnectionExecution {
  endpoint?: string;
  /** A reference to an identity, never a credential. */
  identity_ref?: string;
  credential_mode?: string;
  allowed_tools?: string[];
  [field: string]: unknown;
}

/** The auth block of a tool connection. Every secret field is a reference. */
export interface ToolConnectionAuth {
  type: string;
  in?: string;
  name?: string;
  prefix?: string;
  secret_ref?: string;
  username?: string;
  token_endpoint?: string;
  client_id?: string;
  client_secret_ref?: string;
  client_auth_method?: string;
  scopes?: string[];
  resource?: string;
  audience?: string;
  subject_token_type?: string;
  requested_token_type?: string;
  identity_ref?: string;
  [field: string]: unknown;
}

/** One policy rule on tool calls. */
export interface ToolConnectionRule {
  id: string;
  description?: string;
  /** A CEL boolean expression. */
  when: string;
  /** allow or deny. */
  effect: string;
  message?: string;
}

/** Connection-level tool authorization. Patterns are exact names or prefixes ending in `*`. */
export interface ToolConnectionPolicy {
  allow?: string[];
  deny?: string[];
  read_only?: boolean;
  rules?: ToolConnectionRule[];
}

/** The spec of a tool-connection resource. */
export interface ToolConnectionSpec {
  id: string;
  resource_url?: string;
  execution?: ToolConnectionExecution;
  auth?: ToolConnectionAuth;
  tools?: ToolConnectionPolicy;
  [field: string]: unknown;
}

// ---------------------------------------------------------- hook policies

/** One redact rule: a built-in or RE2 pattern, and mask or deny on a match. */
export interface RedactRuleSpec {
  id: string;
  builtin?: string;
  pattern?: string;
  action: string;
  placeholder?: string;
}

/** An http sidecar hook. `auth_secret_ref` is a secret reference. */
export interface HookHttpSpec {
  endpoint: string;
  format?: string;
  auth_secret_ref?: string;
  fail_open?: boolean;
  presidio?: Record<string, unknown>;
  llm_guard?: Record<string, unknown>;
}

/** One configured hook. */
export interface HookSpec {
  id: string;
  /** redact, size_limit or http. */
  type: string;
  /** request and/or response. */
  phases: string[];
  timeout_ms?: number;
  on_uninspectable?: string;
  message?: string;
  redact?: { rules: RedactRuleSpec[] };
  size_limit?: { max_chars?: number; max_parts?: number };
  http?: HookHttpSpec;
}

/** An ordered list of hooks bound to a scope. */
export interface HookPolicySpec {
  id: string;
  description?: string;
  /** kind is tenant, access_point, model_surface or tool_connection; `id` is absent for the tenant scope. */
  scope: { kind: string; id?: string };
  planes?: string[];
  streaming?: string;
  non_text?: string;
  hooks: HookSpec[];
}

/** A stored hook policy with the revision to pass to put and delete. */
export interface HookPolicyRecord {
  id: string;
  revision: number;
  policy: HookPolicySpec;
  raw: Record<string, unknown>;
}

export function parseHookPolicyRecord(value: unknown): HookPolicyRecord {
  const ctx = "HookPolicyRecord";
  const obj = asObject(value, ctx);
  const spec = reqObject(obj, "spec", ctx);
  const sc = `${ctx}.spec`;
  reqString(spec, "id", sc);
  reqString(reqObject(spec, "scope", sc), "kind", `${sc}.scope`);
  objectList(spec, "hooks", sc).forEach((h, i) => {
    reqString(h, "id", `${sc}.hooks[${i}]`);
    reqString(h, "type", `${sc}.hooks[${i}]`);
    stringList(h, "phases", `${sc}.hooks[${i}]`);
    optInt(h, "timeout_ms", `${sc}.hooks[${i}]`);
  });
  return {
    id: reqString(obj, "id", ctx),
    revision: reqInt(obj, "revision", ctx),
    policy: withoutNulls(spec) as unknown as HookPolicySpec,
    raw: obj,
  };
}

/** A dry-run sample. The server never stores it. */
export interface HookPolicyTestInput {
  /** 1 to 256 samples. */
  texts: string[];
  /** request (default) or response. */
  phase?: "request" | "response";
  /** llmplex (default) or mcplex. */
  plane?: "llmplex" | "mcplex";
}

export function toWireHookPolicyTest(input: HookPolicyTestInput): Record<string, unknown> {
  if (
    !input ||
    !Array.isArray(input.texts) ||
    input.texts.length === 0 ||
    input.texts.length > 256 ||
    input.texts.some((t) => typeof t !== "string")
  ) {
    throw new TypeError("texts must list 1 to 256 strings");
  }
  const body: Record<string, unknown> = { texts: input.texts };
  if (input.phase !== undefined) {
    if (input.phase !== "request" && input.phase !== "response") {
      throw new TypeError("phase must be request or response");
    }
    body["phase"] = input.phase;
  }
  if (input.plane !== undefined) {
    if (input.plane !== "llmplex" && input.plane !== "mcplex") {
      throw new TypeError("plane must be llmplex or mcplex");
    }
    body["plane"] = input.plane;
  }
  return body;
}

/** What one hook decided. */
export interface HookResult {
  id: string;
  type: string;
  phase: string;
  decision: string;
  rules: Array<{ id: string; count: number }>;
  durationMs: number;
  failOpenUsed: boolean;
}

/**
 * What a policy decided on a sample. `texts` is the output after transforms
 * and is `undefined` when the sample was refused.
 */
export interface HookPolicyTestResult {
  policy: string;
  phase: string;
  decision: string;
  code?: string;
  message?: string;
  texts?: string[];
  results: HookResult[];
  uninspectedParts: number;
  raw: Record<string, unknown>;
}

export function parseHookPolicyTestResult(value: unknown): HookPolicyTestResult {
  const ctx = "HookPolicyTestResult";
  const obj = asObject(value, ctx);
  return {
    policy: reqString(obj, "policy", ctx),
    phase: reqString(obj, "phase", ctx),
    decision: reqString(obj, "decision", ctx),
    code: u(optString(obj, "code", ctx)),
    message: u(optString(obj, "message", ctx)),
    texts: obj["texts"] === undefined || obj["texts"] === null ? undefined : stringList(obj, "texts", ctx),
    results: objectList(obj, "results", ctx).map((r, i) => {
      const c = `${ctx}.results[${i}]`;
      return {
        id: reqString(r, "id", c),
        type: reqString(r, "type", c),
        phase: reqString(r, "phase", c),
        decision: reqString(r, "decision", c),
        rules: objectList(r, "rules", c).map((rule, j) => ({
          id: reqString(rule, "id", `${c}.rules[${j}]`),
          count: reqInt(rule, "count", `${c}.rules[${j}]`),
        })),
        durationMs: reqInt(r, "duration_ms", c),
        failOpenUsed: optBoolean(r, "fail_open_used", c) === true,
      };
    }),
    uninspectedParts: reqInt(obj, "uninspected_parts", ctx),
    raw: obj,
  };
}

// ----------------------------------------------------------- usage config

/** The deployment's usage settings. */
export interface UsageConfig {
  /** Trace viewer link template containing `{trace_id}`; empty when no links are configured. */
  traceUrlTemplate: string;
  feedback: boolean;
  feedbackComments: boolean;
  tagKeysAllowed: string[];
  raw: Record<string, unknown>;
}

export function parseUsageConfig(value: unknown): UsageConfig {
  const ctx = "UsageConfig";
  const obj = asObject(value, ctx);
  return {
    traceUrlTemplate: reqString(obj, "trace_url_template", ctx),
    feedback: reqBoolean(obj, "feedback", ctx),
    feedbackComments: reqBoolean(obj, "feedback_comments", ctx),
    tagKeysAllowed: stringList(obj, "tag_keys_allowed", ctx),
    raw: obj,
  };
}
