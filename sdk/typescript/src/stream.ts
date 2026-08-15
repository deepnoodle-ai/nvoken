import type { Client, InvocationHandle, JsonObject } from "./client.js";
import { NvokenError, normalizeError, SessionBusyError } from "./client.js";
import type {
  MessageDeltaEvent,
  StreamEndEvent,
  StreamResyncEvent,
  TranscriptUpdateEvent,
  InvocationChange,
  SessionMessage,
} from "./generated/models/index.js";
import { isTurnOver } from "./invocation-status.js";
import {
  MessageDeltaEventFromJSON,
  StreamEndEventFromJSON,
  StreamResyncEventFromJSON,
  TranscriptUpdateEventFromJSON,
} from "./generated/models/index.js";

export interface StreamEvent {
  id?: string;
  type: string;
  data: unknown;
  retryMs?: number;
}

export interface StreamOptions {
  deltas?: boolean;
}

export interface ReducedSnapshot {
  messages: SessionMessage[];
  invocationChanges: InvocationChange[];
  previews: StreamPreview[];
  cursor?: string;
}

/**
 * One message the model is writing, accumulated from the fragments of one
 * content block. One field carries every kind of fragment, because one
 * accumulator handles all of them.
 */
export interface StreamPreview {
  invocationId: string;
  attempt: number;
  /**
   * The saved message this preview is building. It is the key: the handoff to
   * the saved message updates a row that already has its permanent identity,
   * rather than one row disappearing and another taking its place.
   */
  messageId: string;
  contentIndex: number;
  kind: string;
  delta: string;
  /** Present on tool_arguments previews, naming the call being written. */
  toolCallId?: string;
  name?: string;
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

type TypedInvocationChange<TOutput extends object> =
  Omit<InvocationChange, "structuredOutput"> & { structuredOutput: TOutput | null };

type TypedTranscriptUpdateEvent<TOutput extends object> =
  Omit<TranscriptUpdateEvent, "invocationChanges"> & {
    invocationChanges: TypedInvocationChange<TOutput>[];
  };

/** Every frame the one stream can carry. Switch on `type`. */
export type SessionStreamEvent<TOutput extends object = JsonObject> = StreamMetadata & (
  | TypedTranscriptUpdateEvent<TOutput>
  | MessageDeltaEvent
  | StreamResyncEvent
  | StreamEndEvent
);

export class Reducer {
  private readonly messages = new Map<number, SessionMessage>();
  private readonly changes = new Map<string, InvocationChange>();
  private readonly previews = new Map<string, StreamPreview>();
  private readonly latestAttempts = new Map<string, number>();
  private readonly terminalInvocations = new Set<string>();
  private cursor?: string;

  apply(event: StreamEvent): void {
    if (event.type === "message.delta") {
      this.appendPreview(MessageDeltaEventFromJSON(event.data));
      return;
    }
    if (event.type === "stream.resync") {
      const resync = StreamResyncEventFromJSON(event.data);
      // An absent Invocation is scope: discard previews for the whole Session.
      if (resync.invocationId) {
        this.discardPreviews(resync.invocationId);
      } else {
        this.previews.clear();
        this.latestAttempts.clear();
      }
      return;
    }
    if (event.type !== "transcript.update") return;
    const update = TranscriptUpdateEventFromJSON(event.data);
    // Messages before changes, so a turn is never marked settled before its
    // final message exists.
    for (const message of update.messages) {
      this.messages.set(message.sequence, message);
      if (message.role === "assistant" && message.invocationId) {
        this.discardPreviews(message.invocationId);
      }
    }
    for (const change of update.invocationChanges) {
      this.changes.set(`${change.invocationId}:${change.revision}`, change);
      if (isTurnOver(change)) {
        this.terminalInvocations.add(change.invocationId);
        this.discardPreviews(change.invocationId);
      }
    }
    const cursor = event.id || update.cursor;
    if (cursor) this.cursor = cursor;
  }

  /**
   * Whether a change carrying a terminal status has arrived for this turn.
   * That is the terminal signal, and there is no other.
   */
  settled(invocationId: string): boolean {
    return this.terminalInvocations.has(invocationId);
  }

  snapshot(): ReducedSnapshot {
    return {
      messages: [...this.messages.values()].sort((left, right) => left.sequence - right.sequence),
      invocationChanges: [...this.changes.values()].sort((left, right) => {
        const invocationOrder = left.invocationId.localeCompare(right.invocationId);
        return invocationOrder || left.revision - right.revision;
      }),
      previews: [...this.previews.values()]
        .map((preview) => ({ ...preview }))
        .sort((left, right) =>
          left.messageId.localeCompare(right.messageId)
          || left.contentIndex - right.contentIndex),
      cursor: this.cursor,
    };
  }

  private appendPreview(delta: MessageDeltaEvent): void {
    if (this.terminalInvocations.has(delta.invocationId)) return;
    const latestAttempt = this.latestAttempts.get(delta.invocationId);
    if (latestAttempt !== undefined && delta.attempt < latestAttempt) return;
    if (latestAttempt === undefined || delta.attempt > latestAttempt) {
      this.discardPreviews(delta.invocationId);
      this.latestAttempts.set(delta.invocationId, delta.attempt);
    }
    const key = `${delta.messageId}:${delta.contentIndex}`;
    const preview = this.previews.get(key) ?? {
      invocationId: delta.invocationId,
      attempt: delta.attempt,
      messageId: delta.messageId,
      contentIndex: delta.contentIndex,
      kind: delta.kind,
      delta: "",
    };
    preview.attempt = delta.attempt;
    preview.kind = delta.kind;
    preview.delta += delta.delta;
    if (delta.toolCallId) preview.toolCallId = delta.toolCallId;
    if (delta.name) preview.name = delta.name;
    this.previews.set(key, preview);
  }

  private discardPreviews(invocationId: string): void {
    for (const [key, preview] of this.previews) {
      if (preview.invocationId === invocationId) this.previews.delete(key);
    }
    this.latestAttempts.delete(invocationId);
  }
}

export async function* streamSession<TOutput extends object>(
  client: Client,
  handle: InvocationHandle<TOutput>,
  reducer: Reducer,
  signal?: AbortSignal,
): AsyncGenerator<StreamUpdate> {
  yield* streamSessionWithOptions(client, handle, reducer, {}, signal);
}

export async function* streamSessionWithOptions<TOutput extends object>(
  client: Client,
  handle: InvocationHandle<TOutput>,
  reducer: Reducer,
  options: StreamOptions,
  signal?: AbortSignal,
): AsyncGenerator<StreamUpdate> {
  const sessionId = await handle.requireSessionId(signal);
  yield* streamSessionByID(client, sessionId, reducer, options, signal);
}

/**
 * Subscribe to a Session. Connecting with a fresh Reducer replays the durable
 * transcript from the start, so the stream doubles as the bootstrap, and it
 * reconnects from the Reducer's cursor on any connection end.
 *
 * It never returns on its own: the stream stays open while the Session is idle
 * and a turn started later appears on it. Leave it by breaking out of the loop
 * or aborting the signal.
 */
export async function* streamSessionByID(
  client: Client,
  sessionId: string,
  reducer: Reducer,
  options: StreamOptions = {},
  signal?: AbortSignal,
): AsyncGenerator<StreamUpdate> {
  yield* readStream(client, sessionId, undefined, reducer, options, signal);
}

/**
 * Follow one turn. The stream is filtered to it, and the generator ends once a
 * change for that turn carries a terminal status, which is the terminal signal
 * and the only one.
 */
export async function* streamInvocationByID<TOutput extends object>(
  client: Client,
  sessionId: string,
  invocationId: string,
  signal?: AbortSignal,
): AsyncGenerator<SessionStreamEvent<TOutput>> {
  yield* streamInvocationByIDWithOptions(client, sessionId, invocationId, {}, signal);
}

/** Follow one turn with per-connection delivery options. */
export async function* streamInvocationByIDWithOptions<TOutput extends object>(
  client: Client,
  sessionId: string,
  invocationId: string,
  options: StreamOptions,
  signal?: AbortSignal,
): AsyncGenerator<SessionStreamEvent<TOutput>> {
  const reducer = new Reducer();
  for await (const update of readStream(client, sessionId, invocationId, reducer, options, signal)) {
    // Frame types may appear that this SDK version does not know. Handle the
    // ones you know and ignore the rest, which is the rule the contract sets
    // and the only way an added frame is not a breaking change.
    const decoded = decodeStreamEvent(update.event);
    if (decoded !== undefined) {
      yield {
        ...decoded,
        sseId: update.event.id,
        retryMs: update.event.retryMs,
      } as SessionStreamEvent<TOutput>;
    }
    if (reducer.settled(invocationId)) return;
  }
}

/**
 * The one read loop. It reconnects from its last durable cursor on any
 * connection end, because stream.end never says a turn is over and a silent
 * drop says nothing at all.
 */
async function* readStream(
  client: Client,
  sessionId: string,
  invocationId: string | undefined,
  reducer: Reducer,
  options: StreamOptions,
  signal?: AbortSignal,
): AsyncGenerator<StreamUpdate> {
  let retryMs = 1_000;
  for (;;) {
    const request = await client.sessions.streamSessionRequestOpts({
      sessionId,
      invocationId,
      deltas: options.deltas,
      lastEventID: reducer.snapshot().cursor,
    });
    request.headers = { ...request.headers, Accept: "text/event-stream" };

    let response: Response;
    try {
      response = await fetchStream(client, request, signal);
    } catch (error) {
      const normalized = await normalizeError(error);
      if (!streamRetryable(normalized)) throw normalized;
      // Reconnecting is unbounded — the turn is already durable, so there is
      // nothing to re-admit and nothing an attempt cap would protect.
      await delay(streamDelay(client, 1, retryMs, normalized), signal);
      continue;
    }

    for await (const event of parseSSE(response.body!)) {
      if (event.retryMs !== undefined) retryMs = Math.min(event.retryMs, 30_000);
      // A frame carrying only `retry:` is a control frame, not an event. The
      // runtime opens every stream with one. Its bookkeeping is applied above;
      // the frame itself has no payload to decode or reduce.
      if (event.data === undefined) continue;
      reducer.apply(event);
      yield { event, snapshot: reducer.snapshot() };
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

function decodeStreamEvent(raw: StreamEvent): object | undefined {
  if (!raw.data || typeof raw.data !== "object") {
    throw new NvokenError(
      "unexpected_response",
      `stream event ${raw.type} had no object payload`,
    );
  }
  switch (raw.type) {
  case "transcript.update":
    return TranscriptUpdateEventFromJSON(raw.data);
  case "message.delta":
    return MessageDeltaEventFromJSON(raw.data);
  case "stream.resync":
    return StreamResyncEventFromJSON(raw.data);
  case "stream.end":
    return StreamEndEventFromJSON(raw.data);
  default:
    return undefined;
  }
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
        "cancelled",
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
    || value === "incomplete"
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
      reject(new NvokenError("cancelled", "local stream was cancelled"));
      return;
    }
    const onAbort = () => {
      clearTimeout(timer);
      reject(new NvokenError("cancelled", "local stream was cancelled"));
    };
    const timer = setTimeout(() => {
      signal?.removeEventListener("abort", onAbort);
      resolvePromise();
    }, milliseconds);
    signal?.addEventListener("abort", onAbort, { once: true });
  });
}
