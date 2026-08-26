import type { Turn, TurnResult } from "./facade-types.js";
import type { JsonObject } from "./facade-types.js";
import { FetchError, ResponseError } from "./generated/runtime.js";

export type ErrorCategory =
  | "authentication"
  | "permission"
  | "validation"
  | "not_found"
  | "conflict"
  | "rate_limit"
  | "server"
  | "transport"
  | "cancelled"
  | "timeout"
  | "turn"
  | "unexpected_response";

export class NvokenError extends Error {
  constructor(
    public readonly category: ErrorCategory,
    message: string,
    public readonly status?: number,
    public readonly code?: string,
    public readonly requestId?: string,
    public readonly retryAfterMs?: number,
    public readonly details?: Record<string, unknown>,
    options?: ErrorOptions,
  ) {
    super(message, options);
    this.name = "NvokenError";
  }
}

export class TurnErasedError extends NvokenError {
  constructor(
    message: string,
    public readonly turnId?: string,
    public readonly erasedAt?: Date,
    requestId?: string,
    details?: Record<string, unknown>,
    options?: ErrorOptions,
  ) {
    super("not_found", message, 410, "turn_erased", requestId, undefined, details, options);
    this.name = "TurnErasedError";
  }
}

export class TurnTimeoutError<TOutput extends object = JsonObject> extends NvokenError {
  constructor(
    message: string,
    public readonly turn?: Turn<TOutput>,
    public readonly idempotencyKey?: string,
    options?: ErrorOptions,
  ) {
    super("timeout", message, undefined, "turn_timeout", undefined, undefined, undefined, options);
    this.name = "TurnTimeoutError";
  }
}

export class TurnExecutionError<TOutput extends object = JsonObject> extends NvokenError {
  constructor(public readonly result: TurnResult<TOutput>) {
    super(
      "turn",
      `Turn ${result.turn.id} ended with status ${result.status}`,
      undefined,
      "turn_ended_unsuccessfully",
      undefined,
      undefined,
      { turn_id: result.turn.id, status: result.status },
    );
    this.name = "TurnExecutionError";
  }
}

export class NoOutputTextError<TOutput extends object = JsonObject> extends NvokenError {
  constructor(public readonly result: TurnResult<TOutput>) {
    super(
      "unexpected_response",
      `Turn ${result.turn.id} produced no final assistant text; call run() and inspect the full result`,
      undefined,
      "no_output_text",
      undefined,
      undefined,
      { turn_id: result.turn.id },
    );
    this.name = "NoOutputTextError";
  }
}

export async function normalizeError(error: unknown): Promise<NvokenError> {
  if (error instanceof NvokenError) return error;
  if (error instanceof ResponseError) {
    const response = error.response;
    let body: {
      code?: string;
      message?: string;
      request_id?: string;
      details?: Record<string, unknown>;
    } = {};
    try {
      body = await response.clone().json() as typeof body;
    } catch {
      // The status and headers still make a useful error.
    }
    const requestId = body.request_id ?? response.headers.get("x-request-id") ?? undefined;
    if (response.status === 410 && body.code === "turn_erased") {
      const erasedAt = typeof body.details?.erased_at === "string"
        ? new Date(body.details.erased_at)
        : undefined;
      return new TurnErasedError(
        body.message ?? "The Turn content has been erased.",
        typeof body.details?.turn_id === "string" ? body.details.turn_id : undefined,
        erasedAt && !Number.isNaN(erasedAt.getTime()) ? erasedAt : undefined,
        requestId,
        body.details,
        { cause: error },
      );
    }
    const category: ErrorCategory = response.status === 401
      ? "authentication"
      : response.status === 403
        ? "permission"
        : response.status === 400 || response.status === 422
          ? "validation"
          : response.status === 404 || response.status === 410
            ? "not_found"
            : response.status === 409
              ? "conflict"
              : response.status === 429
                ? "rate_limit"
                : response.status >= 500
                  ? "server"
                  : "unexpected_response";
    return new NvokenError(
      category,
      body.message ?? `nvoken returned HTTP ${response.status}`,
      response.status,
      body.code,
      requestId,
      parseRetryAfter(response.headers.get("retry-after")),
      body.details,
      { cause: error },
    );
  }
  if (error instanceof FetchError || error instanceof TypeError) {
    return new NvokenError(
      "transport",
      "nvoken transport failed",
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      { cause: error },
    );
  }
  if (error instanceof DOMException && error.name === "AbortError") {
    return new NvokenError(
      "cancelled",
      "local wait or request was cancelled",
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      { cause: error },
    );
  }
  return new NvokenError(
    "unexpected_response",
    "unexpected nvoken client failure",
    undefined,
    undefined,
    undefined,
    undefined,
    undefined,
    { cause: error },
  );
}

export function isNotFound(error: unknown): error is NvokenError {
  return error instanceof NvokenError && error.category === "not_found";
}

export function formatNvokenError(error: unknown): string {
  if (!(error instanceof NvokenError)) return error instanceof Error ? error.message : String(error);
  const code = error.code ? ` [${error.code}]` : "";
  const request = error.requestId ? ` (request ${error.requestId})` : "";
  return `${error.message}${code}${request}`;
}

export function formatTurnFailure(
  turn: { id?: string; status: string; error?: { code?: string; message?: string } | null },
): string {
  const id = turn.id ? ` ${turn.id}` : "";
  const code = turn.error?.code ? ` [${turn.error.code}]` : "";
  const message = turn.error?.message ? `: ${turn.error.message}` : "";
  return `Turn${id} ${turn.status}${code}${message}`;
}

function parseRetryAfter(value: string | null): number | undefined {
  if (!value) return undefined;
  const seconds = Number(value);
  if (Number.isFinite(seconds) && seconds >= 0) return seconds * 1_000;
  const at = Date.parse(value);
  return Number.isNaN(at) ? undefined : Math.max(0, at - Date.now());
}
