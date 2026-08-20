import type { InvocationHandle, JsonObject, StreamClient } from "./client.js";
import { NvokenError, normalizeError, SessionBusyError } from "./client.js";
import type {
  MessageDeltaEvent,
  ConnectionClosingEvent,
  StreamResyncEvent,
  TranscriptUpdateEvent,
  InvocationChange,
  SessionMessage,
} from "./generated/models/index.js";
import { isTurnOver } from "./invocation-status.js";
import {
  instanceOfInvocationChange,
  instanceOfMessageDeltaEvent,
  instanceOfSessionMessage,
  instanceOfConnectionClosingEvent,
  instanceOfStreamResyncEvent,
  instanceOfTranscriptUpdateEvent,
  MessageDeltaEventFromJSON,
  ConnectionClosingEventFromJSON,
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
  | ConnectionClosingEvent
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
      this.appendPreview(decodeMessageDelta(event.data));
      return;
    }
    if (event.type === "stream.resync") {
      const resync = decodeStreamResync(event.data);
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
    const update = decodeTranscriptUpdate(event.data);
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

/**
 * Subscribe to the Session a handle belongs to, waiting for its id first.
 *
 * Reconnects on its own — see {@link streamSessionByID}, which this calls.
 * Do not wrap it in a retry loop.
 */
export async function* streamSession<TOutput extends object>(
  client: StreamClient,
  handle: InvocationHandle<TOutput>,
  reducer: Reducer,
  signal?: AbortSignal,
): AsyncGenerator<StreamUpdate> {
  yield* streamSessionWithOptions(client, handle, reducer, {}, signal);
}

/**
 * {@link streamSession} with per-connection delivery options.
 *
 * Reconnects on its own — see {@link streamSessionByID}. Do not wrap it in
 * a retry loop.
 */
export async function* streamSessionWithOptions<TOutput extends object>(
  client: StreamClient,
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
 *
 * ## Do not add your own retry
 *
 * Reconnection is this function's job and it is already doing it. A dropped
 * connection, a `connection.closing` frame, and a rotation at the connection
 * lifetime are all the same thing to it: reconnect from the cursor the Reducer
 * holds, so no frame is missed and nothing is replayed twice. Reconnecting is
 * otherwise unbounded, because the turn is durable on the server and a brief
 * outage should not end a stream that will heal.
 *
 * So a throw is not "try again in a moment". It means one of two things, and a
 * timer of your own makes both worse:
 *
 * - the error was not retryable, and retrying it will fail the same way; or
 * - it failed to connect *continuously* for `streamReconnectTimeoutMs`
 *   (5 minutes by default), having backed off across every attempt in that
 *   window. A successful connect resets the window, so reaching this means the
 *   stream is broken rather than briefly interrupted.
 *
 * Either way the loop you would be restarting is one this function deliberately
 * ended. Surface the error instead. To wait longer before giving up, raise
 * `streamReconnectTimeoutMs` on the client rather than wrapping the call.
 */
export async function* streamSessionByID(
  client: StreamClient,
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
 *
 * Reconnects from its cursor on any connection end, exactly as
 * {@link streamSessionByID} does, and throws on the same two conditions. Do not
 * wrap it in a retry loop; returning normally is how it reports the turn is
 * over.
 */
export async function* streamInvocationByID<TOutput extends object>(
  client: StreamClient,
  sessionId: string,
  invocationId: string,
  signal?: AbortSignal,
): AsyncGenerator<SessionStreamEvent<TOutput>> {
  yield* streamInvocationByIDWithOptions(client, sessionId, invocationId, {}, signal);
}

/**
 * Follow one turn with per-connection delivery options.
 *
 * Reconnects on its own — see {@link streamSessionByID}. Do not wrap it in
 * a retry loop.
 */
export async function* streamInvocationByIDWithOptions<TOutput extends object>(
  client: StreamClient,
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
 * connection end. A `connection.closing` frame says only that, and a silent
 * drop says nothing at all, so neither is a reason to stop.
 *
 * This is private, so the promise callers actually rely on is stated on the
 * exported helpers that reach it — see {@link streamSessionByID}. Weakening
 * the guarantee here means weakening it there too.
 */
async function* readStream(
  client: StreamClient,
  sessionId: string,
  invocationId: string | undefined,
  reducer: Reducer,
  options: StreamOptions,
  signal?: AbortSignal,
): AsyncGenerator<StreamUpdate> {
  let retryMs = 1_000;
  // The current run of consecutive connect failures. A stream that connects
  // clears it, so this measures "cannot connect at all", not "has been
  // streaming for a long time".
  let failingSince: number | undefined;
  let consecutiveFailures = 0;
  for (;;) {
    const request = await client.sessions.streamSessionRequestOpts({
      sessionId,
      invocationId,
      deltas: options.deltas,
      // The query parameter, not the `Last-Event-ID` header. Both carry the
      // same value and the contract says this one wins, but a header on a
      // cross-origin request costs a CORS preflight before every reconnect —
      // and fails outright against any runtime whose CORS policy predates
      // allowing it. This SDK reads the stream with `fetch` rather than
      // `EventSource`, so nothing here is obliged to speak SSE's mechanics.
      cursor: reducer.snapshot().cursor,
    });
    request.headers = { ...request.headers, Accept: "text/event-stream" };

    let response: Response;
    try {
      response = await fetchStream(client, request, signal);
      failingSince = undefined;
      consecutiveFailures = 0;
    } catch (error) {
      const normalized = await normalizeError(error);
      if (!streamRetryable(normalized)) throw normalized;
      // Reconnecting is otherwise unbounded, because the turn is already
      // durable and a brief outage should not end a stream that will heal.
      // But a retry that can never succeed — a runtime whose fetch always
      // throws, a URL that never resolves — must not spin silently while the
      // turn it was meant to drive parks forever. Failing to connect for a
      // continuous window says the difference: it survives a long outage and
      // still surfaces a stream that is simply broken.
      const now = Date.now();
      failingSince ??= now;
      consecutiveFailures += 1;
      if (now - failingSince >= client.streamReconnectTimeoutMs) {
        throw new NvokenError(
          normalized.category,
          `stream could not reconnect after ${consecutiveFailures} attempts over `
            + `${Math.round((now - failingSince) / 1_000)}s: ${normalized.message}`,
          normalized.status,
          normalized.code,
          normalized.requestId,
          normalized.retryAfterMs,
          normalized.details,
          { cause: normalized },
        );
      }
      await delay(streamDelay(client, consecutiveFailures, retryMs, normalized), signal);
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
  client: StreamClient,
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

/**
 * Refuses a frame that is missing a field the contract requires.
 *
 * The generated decoders copy whatever is there and leave the rest undefined,
 * so a required field the server never sent becomes a plausible-looking value
 * rather than an error — and for a boolean that is a confident wrong answer.
 * The generated `instanceOf` guards already encode the required set from the
 * contract, so this wires them in rather than restating it.
 *
 * Frames may gain fields over time and a decoder must ignore what it does not
 * recognize. Requiring what the contract requires is the other half of that
 * rule, not a contradiction of it.
 */
function requireFrameField<T>(
  what: string,
  value: T,
  valid: (candidate: object) => boolean,
): T {
  if (!valid(value as object)) {
    throw new NvokenError(
      "unexpected_response",
      `${what} is missing a field the contract requires`,
    );
  }
  return value;
}

/**
 * The four frame decoders, each refusing a payload the contract would not
 * allow. Both readers go through these: `Reducer.apply`, which the Session
 * subscription folds with, and `decodeStreamEvent`, which produces the typed
 * value a filtered stream yields.
 */
function decodeTranscriptUpdate(data: unknown): TranscriptUpdateEvent {
  const update = requireFrameField(
    "transcript.update",
    TranscriptUpdateEventFromJSON(data),
    instanceOfTranscriptUpdateEvent,
  );
  // The frame guard only proves the two collections are present. Their entries
  // are what a reader actually folds on, and a change missing `terminal` is the
  // case worth catching: it decodes as "not the end of the turn".
  for (const message of update.messages) {
    requireFrameField("session message", message, instanceOfSessionMessage);
  }
  for (const change of update.invocationChanges) {
    requireFrameField("invocation change", change, instanceOfInvocationChange);
  }
  return update;
}

function decodeMessageDelta(data: unknown): MessageDeltaEvent {
  return requireFrameField(
    "message.delta",
    MessageDeltaEventFromJSON(data),
    instanceOfMessageDeltaEvent,
  );
}

function decodeStreamResync(data: unknown): StreamResyncEvent {
  return requireFrameField(
    "stream.resync",
    StreamResyncEventFromJSON(data),
    instanceOfStreamResyncEvent,
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
    return decodeTranscriptUpdate(raw.data);
  case "message.delta":
    return decodeMessageDelta(raw.data);
  case "stream.resync":
    return decodeStreamResync(raw.data);
  case "connection.closing":
    return requireFrameField(
      "connection.closing",
      ConnectionClosingEventFromJSON(raw.data),
      instanceOfConnectionClosingEvent,
    );
  default:
    return undefined;
  }
}

async function fetchStream(
  client: StreamClient,
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
