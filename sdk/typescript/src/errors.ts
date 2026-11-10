/**
 * Options for constructing an {@link AiplexError}.
 */
export interface AiplexErrorInit {
  statusCode?: number | null;
  code: string;
  message: string;
  details?: Record<string, unknown> | null;
  requestId?: string | null;
  traceId?: string | null;
}

/**
 * Error thrown by {@link AiplexClient} for every API, decode, pagination, or transport failure.
 */
export class AiplexError extends Error {
  public readonly statusCode: number | null;
  public readonly code: string;
  public readonly details: Record<string, unknown> | null;
  public readonly requestId: string | null;
  public readonly traceId: string | null;

  constructor(init: AiplexErrorInit) {
    const statusCode = init.statusCode ?? null;
    const formatted =
      statusCode === null
        ? `aiplex: ${init.code}: ${init.message}`
        : `aiplex: ${init.code} (${statusCode}): ${init.message}`;
    super(formatted);
    this.name = "AiplexError";
    this.statusCode = statusCode;
    this.code = init.code;
    this.details = init.details ?? null;
    this.requestId = init.requestId ?? null;
    this.traceId = init.traceId ?? null;
    Object.setPrototypeOf(this, AiplexError.prototype);
  }

  /**
   * Returns true when the server responded with HTTP 409 Conflict (optimistic concurrency failure).
   */
  public get isRevisionConflict(): boolean {
    return this.statusCode === 409;
  }

  /**
   * Returns the specification field named in a validation error's details, or null.
   */
  public get field(): string | null {
    if (this.details === null) {
      return null;
    }
    const f = this.details["field"];
    return typeof f === "string" ? f : null;
  }
}

/**
 * Redacts all occurrences of a secret token from a string.
 */
export function redactText(text: string, token: string): string {
  if (!token || !text) {
    return text;
  }
  return text.split(token).join("[redacted]");
}

/**
 * Deeply redacts all occurrences of a secret token from string values within a value.
 */
export function redactValue(value: unknown, token: string): unknown {
  if (!token) {
    return value;
  }
  if (typeof value === "string") {
    return redactText(value, token);
  }
  if (Array.isArray(value)) {
    return value.map((item) => redactValue(item, token));
  }
  if (value !== null && typeof value === "object") {
    const out: Record<string, unknown> = {};
    for (const [k, v] of Object.entries(value)) {
      out[redactText(k, token)] = redactValue(v, token);
    }
    return out;
  }
  return value;
}

/**
 * Deeply redacts all occurrences of a secret token from a record object.
 */
export function redactRecord(
  record: Record<string, unknown> | null,
  token: string,
): Record<string, unknown> | null {
  if (record === null) {
    return null;
  }
  return redactValue(record, token) as Record<string, unknown>;
}
