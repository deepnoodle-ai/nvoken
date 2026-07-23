import type { Client, InvocationHandle, JsonObject, TypedInvocationResult } from "./client.js";
import { NvokenError, normalizeError, SessionBusyError } from "./client.js";
import type {
  CreateInvocationRequest,
  InvocationAcceptedEvent,
  InvocationResultEvent,
  InvocationStreamEvent as GeneratedInvocationStreamEvent,
  InvocationUpdateEvent,
  OutputTextDeltaEvent,
  StreamEndEvent,
  StreamResyncEvent,
  ThinkingDeltaEvent,
  InvocationChange,
  SessionMessage,
} from "./generated/models/index.js";
import {
  InvocationAcceptedEventFromJSON,
  InvocationResultEventFromJSON,
  InvocationUpdateEventFromJSON,
  OutputTextDeltaEventFromJSON,
  StreamEndEventFromJSON,
  StreamResyncEventFromJSON,
  ThinkingDeltaEventFromJSON,
  TranscriptUpdateFromJSON,
} from "./generated/models/index.js";

export interface StreamEvent {
  id?: string;
  type: string;
  data: unknown;
  retryMs?: number;
}

export interface ReducedSnapshot {
  messages: SessionMessage[];
  invocationChanges: InvocationChange[];
  resumeCursor?: string;
}

export interface StreamUpdate {
  event: StreamEvent;
  snapshot: ReducedSnapshot;
}

export interface StreamMetadata {
  /** SSE cursor on durable frames. Persist this to resume after a disconnect. */
  sseId?: string;
  /** Server-selected reconnect delay, when present. */
  retryMs?: number;
}

type TypedInvocationUpdateEvent<TOutput extends object> =
  Omit<InvocationUpdateEvent, "invocation"> & {
    invocation: InvocationUpdateEvent["invocation"] & {
      structuredOutput: TOutput | null;
    };
  };

type TypedInvocationResultEvent<TOutput extends object> =
  Omit<InvocationResultEvent, "result"> & {
    result: TypedInvocationResult<TOutput>;
  };

export type InvocationStreamEvent<TOutput extends object = JsonObject> = StreamMetadata & (
  | InvocationAcceptedEvent
  | TypedInvocationUpdateEvent<TOutput>
  | TypedInvocationResultEvent<TOutput>
  | OutputTextDeltaEvent
  | ThinkingDeltaEvent
  | StreamResyncEvent
  | StreamEndEvent
);

export class Reducer {
  private readonly messages = new Map<number, SessionMessage>();
  private readonly changes = new Map<string, InvocationChange>();
  private cursor?: string;

  apply(event: StreamEvent): void {
    if (event.type !== "transcript.update") return;
    const update = TranscriptUpdateFromJSON(event.data);
    for (const message of update.messages) this.messages.set(message.sequence, message);
    for (const change of update.invocationChanges) {
      this.changes.set(`${change.invocationId}:${change.revision}`, change);
    }
    this.cursor = event.id ?? update.resumeCursor ?? this.cursor;
  }

  snapshot(): ReducedSnapshot {
    return {
      messages: [...this.messages.values()].sort((left, right) => left.sequence - right.sequence),
      invocationChanges: [...this.changes.values()].sort((left, right) => {
        const invocationOrder = left.invocationId.localeCompare(right.invocationId);
        return invocationOrder || left.revision - right.revision;
      }),
      resumeCursor: this.cursor,
    };
  }
}

export async function* streamSession<TOutput extends object>(
  client: Client,
  handle: InvocationHandle<TOutput>,
  reducer: Reducer,
  signal?: AbortSignal,
): AsyncGenerator<StreamUpdate> {
  let retryMs = 1_000;
  for (;;) {
    const sessionId = await handle.requireSessionId(signal);
    const request = await client.sessions.streamSessionTranscriptRequestOpts({
      sessionId,
      lastEventID: reducer.snapshot().resumeCursor,
    });
    const response = await fetchStream(client, request, signal);
    let terminalEnd = false;
    for await (const event of parseSSE(response.body!)) {
      if (event.retryMs !== undefined) retryMs = Math.min(event.retryMs, 30_000);
      reducer.apply(event);
      yield { event, snapshot: reducer.snapshot() };
      if (
        event.type === "stream.end"
        && StreamEndEventFromJSON(event.data).reason === "terminal"
      ) {
        terminalEnd = true;
      }
    }
    if (terminalEnd) return;
    await delay(retryMs, signal);
  }
}

/**
 * Admit and stream one Invocation. Admission is retried with the exact
 * serialized request until `invocation.accepted`; reconnects after that use
 * the Invocation-scoped durable cursor.
 */
export function streamInvocation<TOutput extends object>(
  client: Client,
  request: CreateInvocationRequest,
  signal?: AbortSignal,
): AsyncGenerator<InvocationStreamEvent<TOutput>> {
  return streamInvocationLoop(client, request, undefined, undefined, signal);
}

/** Resume an existing Invocation stream without re-admitting work. */
export function streamInvocationByID<TOutput extends object>(
  client: Client,
  invocationId: string,
  signal?: AbortSignal,
): AsyncGenerator<InvocationStreamEvent<TOutput>> {
  return streamInvocationLoop(client, undefined, invocationId, undefined, signal);
}

async function* streamInvocationLoop<TOutput extends object>(
  client: Client,
  admission: CreateInvocationRequest | undefined,
  initialInvocationId: string | undefined,
  initialCursor: string | undefined,
  signal?: AbortSignal,
): AsyncGenerator<InvocationStreamEvent<TOutput>> {
  let invocationId = initialInvocationId;
  let cursor = initialCursor;
  let retryMs = 1_000;
  let admissionAttempts = 0;
  for (;;) {
    const admitting = invocationId === undefined;
    if (admitting) admissionAttempts += 1;
    const request = invocationId
      ? await client.invocations.streamInvocationRequestOpts({
        invocationId,
        lastEventID: cursor,
      })
      : await client.invocations.createInvocationRequestOpts({
        createInvocationRequest: admission!,
      });
    request.headers = { ...request.headers, Accept: "text/event-stream" };

    let response: Response;
    try {
      response = await fetchStream(client, request, signal);
    } catch (error) {
      const normalized = await normalizeError(error);
      if (
        !streamRetryable(normalized)
        || (admitting && admissionAttempts >= client.retry.maxAttempts)
      ) {
        throw normalized;
      }
      await delay(streamDelay(client, admissionAttempts, retryMs, normalized), signal);
      continue;
    }

    for await (const raw of parseSSE(response.body!)) {
      if (raw.retryMs !== undefined) retryMs = Math.min(raw.retryMs, 30_000);
      if (raw.id) cursor = raw.id;
      const event = decodeInvocationEvent(raw);
      if (event.type === "invocation.accepted") {
        invocationId = event.invocationId;
      }
      yield event as InvocationStreamEvent<TOutput>;
      if (event.type === "invocation.result") return;
    }
    if (admitting && invocationId === undefined) {
      const error = new NvokenError(
        "transport",
        "Invocation stream closed before acknowledgement",
      );
      if (admissionAttempts >= client.retry.maxAttempts) throw error;
      await delay(streamDelay(client, admissionAttempts, retryMs, error), signal);
      continue;
    }
    await delay(retryMs, signal);
  }
}

function streamDelay(
  client: Client,
  attempt: number,
  serverDelayMs: number,
  error: NvokenError,
): number {
  if (error.retryAfterMs !== undefined) {
    return Math.min(error.retryAfterMs, client.retry.maxDelayMs);
  }
  return Math.min(
    client.retry.maxDelayMs,
    Math.max(serverDelayMs, client.retry.minDelayMs * 2 ** Math.max(0, attempt - 1)),
  );
}

function decodeInvocationEvent(raw: StreamEvent): InvocationStreamEvent<object> {
  if (!raw.data || typeof raw.data !== "object") {
    throw new NvokenError(
      "unexpected_response",
      `Invocation stream event ${raw.type} had no object payload`,
    );
  }
  let event: GeneratedInvocationStreamEvent;
  switch (raw.type) {
  case "invocation.accepted":
    event = InvocationAcceptedEventFromJSON(raw.data);
    break;
  case "invocation.update":
    event = InvocationUpdateEventFromJSON(raw.data);
    break;
  case "invocation.result":
    event = InvocationResultEventFromJSON(raw.data);
    break;
  case "output_text.delta":
    event = OutputTextDeltaEventFromJSON(raw.data);
    break;
  case "thinking.delta":
    event = ThinkingDeltaEventFromJSON(raw.data);
    break;
  case "stream.resync":
    event = StreamResyncEventFromJSON(raw.data);
    break;
  case "stream.end":
    event = StreamEndEventFromJSON(raw.data);
    break;
  default:
    throw new NvokenError(
      "unexpected_response",
      `Unknown Invocation stream event ${raw.type}`,
    );
  }
  return { ...event, sseId: raw.id, retryMs: raw.retryMs } as InvocationStreamEvent<object>;
}

async function fetchStream(
  client: Client,
  request: { path: string; method: string; headers: Record<string, string>; query?: unknown; body?: unknown },
  signal?: AbortSignal,
): Promise<Response> {
  const url = new URL(client.configuration.basePath + request.path);
  for (const [name, value] of Object.entries(request.query ?? {})) {
    if (value !== undefined && value !== null) url.searchParams.set(name, String(value));
  }
  let response: Response;
  try {
    const contentType = request.headers["Content-Type"] ?? request.headers["content-type"];
    const body = request.body !== undefined
      && request.body !== null
      && typeof request.body === "object"
      && contentType?.includes("application/json")
      ? JSON.stringify(request.body)
      : request.body as BodyInit | null | undefined;
    response = await client.fetch(url, {
      method: request.method,
      headers: request.headers,
      body,
      signal,
    });
  } catch (error) {
    if (signal?.aborted) {
      throw new NvokenError(
        "timeout",
        "local stream was cancelled",
        undefined,
        undefined,
        undefined,
        undefined,
        undefined,
        { cause: error },
      );
    }
    throw error;
  }
  if (!response.ok) {
    throw await responseError(response);
  }
  if (!response.body) {
    throw new NvokenError("unexpected_response", "stream response had no body");
  }
  return response;
}

async function responseError(response: Response): Promise<NvokenError> {
  let body: {
    message?: string;
    code?: string;
    request_id?: string;
    details?: Record<string, unknown>;
  } = {};
  try {
    body = await response.clone().json() as typeof body;
  } catch {
    // Status and headers still produce a useful error.
  }
  const requestId = body.request_id ?? response.headers.get("x-request-id") ?? undefined;
  if (body.code === "session_invocation_active") {
    return new SessionBusyError(
      body.message ?? "This Session already has a nonterminal Invocation.",
      typeof body.details?.invocation_id === "string"
        ? body.details.invocation_id
        : undefined,
      invocationStatus(body.details?.status),
      requestId,
      body.details,
    );
  }
  return new NvokenError(
    response.status === 401 || response.status === 403
      ? "authentication"
      : response.status === 404
        ? "not_found"
        : response.status === 409
          ? "conflict"
          : response.status === 429
            ? "rate_limit"
            : response.status >= 500
              ? "server"
              : "unexpected_response",
    body.message ?? `nvoken returned HTTP ${response.status}`,
    response.status,
    body.code,
    requestId,
    parseRetryAfter(response.headers.get("retry-after")),
    body.details,
  );
}

function invocationStatus(value: unknown) {
  return value === "queued"
    || value === "running"
    || value === "waiting"
    || value === "completed"
    || value === "failed"
    || value === "cancelled"
    ? value
    : undefined;
}

export async function* parseSSE(
  body: ReadableStream<Uint8Array>,
): AsyncGenerator<StreamEvent> {
  const reader = body.pipeThrough(new TextDecoderStream()).getReader();
  let buffer = "";
  let event: Partial<StreamEvent> = {};
  let data: string[] = [];
  const dispatch = (): StreamEvent | undefined => {
    if (!event.type && !event.id && data.length === 0 && event.retryMs === undefined) {
      return undefined;
    }
    let decoded: unknown = undefined;
    if (data.length > 0) decoded = JSON.parse(data.join("\n"));
    const value: StreamEvent = {
      type: event.type ?? "message",
      id: event.id,
      retryMs: event.retryMs,
      data: decoded,
    };
    event = {};
    data = [];
    return value;
  };
  try {
    for (;;) {
      const { done, value } = await reader.read();
      buffer += value ?? "";
      let newline = buffer.indexOf("\n");
      while (newline >= 0) {
        const line = buffer.slice(0, newline).replace(/\r$/, "");
        buffer = buffer.slice(newline + 1);
        if (line === "") {
          const value = dispatch();
          if (value) yield value;
        } else if (!line.startsWith(":")) {
          const separator = line.indexOf(":");
          const field = separator < 0 ? line : line.slice(0, separator);
          const raw = separator < 0 ? "" : line.slice(separator + 1).replace(/^ /, "");
          if (field === "event") event.type = raw;
          else if (field === "id") event.id = raw;
          else if (field === "retry" && /^\d+$/.test(raw)) event.retryMs = Number(raw);
          else if (field === "data") data.push(raw);
        }
        newline = buffer.indexOf("\n");
      }
      if (done) break;
    }
    if (buffer) data.push(buffer);
    const final = dispatch();
    if (final) yield final;
  } finally {
    reader.releaseLock();
  }
}

function streamRetryable(error: NvokenError): boolean {
  return error.category === "transport"
    || error.status === 408
    || error.status === 425
    || error.status === 429
    || error.status === 500
    || error.status === 502
    || error.status === 503
    || error.status === 504;
}

function parseRetryAfter(value: string | null): number | undefined {
  if (!value) return undefined;
  const seconds = Number(value);
  if (Number.isFinite(seconds) && seconds >= 0) return seconds * 1_000;
  const when = Date.parse(value);
  return Number.isNaN(when) ? undefined : Math.max(0, when - Date.now());
}

function delay(milliseconds: number, signal?: AbortSignal): Promise<void> {
  return new Promise((resolvePromise, reject) => {
    if (signal?.aborted) {
      reject(new NvokenError("timeout", "local stream was cancelled"));
      return;
    }
    const onAbort = () => {
      clearTimeout(timer);
      reject(new NvokenError("timeout", "local stream was cancelled"));
    };
    const timer = setTimeout(() => {
      signal?.removeEventListener("abort", onAbort);
      resolvePromise();
    }, milliseconds);
    signal?.addEventListener("abort", onAbort, { once: true });
  });
}
