import type {
  ConnectionClosingEvent,
  ConversationMessage,
  MessageDeltaEvent,
  StreamResyncEvent,
  TranscriptUpdateEvent,
  TurnChange,
} from "./generated/models/index.js";
import {
  ConnectionClosingEventFromJSON,
  instanceOfConnectionClosingEvent,
  instanceOfConversationMessage,
  instanceOfMessageDeltaEvent,
  instanceOfStreamResyncEvent,
  instanceOfTranscriptUpdateEvent,
  instanceOfTurnChange,
  MessageDeltaEventFromJSON,
  StreamResyncEventFromJSON,
  TranscriptUpdateEventFromJSON,
} from "./generated/models/index.js";
import { ResponseError } from "./generated/runtime.js";
import { isTurnOver } from "./turn-status.js";
import { NvokenError } from "./turn-error.js";

export interface StreamMetadata {
  sseId?: string;
  retryMs?: number;
}

type TypedTurnChange<TOutput extends object> = Omit<TurnChange, "structuredOutput"> & {
  structuredOutput: TOutput | null;
};

type TypedTranscriptUpdate<TOutput extends object> = Omit<TranscriptUpdateEvent, "turnChanges"> & {
  turnChanges: TypedTurnChange<TOutput>[];
};

export type StreamFrame<TOutput extends object = Record<string, never>> = StreamMetadata & (
  | TypedTranscriptUpdate<TOutput>
  | MessageDeltaEvent
  | StreamResyncEvent
  | ConnectionClosingEvent
);

export interface StreamPreview {
  turnId: string;
  attempt: number;
  messageId: string;
  contentIndex: number;
  kind: string;
  delta: string;
  toolCallId?: string;
  name?: string;
}

export interface ReducedSnapshot<TOutput extends object = Record<string, never>> {
  messages: ConversationMessage[];
  turnChanges: TypedTurnChange<TOutput>[];
  previews: StreamPreview[];
  cursor?: string;
}

export interface ReducerOptions<TOutput extends object = Record<string, never>> {
  /** Seed a bounded tail and the exact forward resume position it observed. */
  initial?: Partial<ReducedSnapshot<TOutput>>;
  /** Bound durable messages. Eviction removes whole terminal Turns only. */
  maxMessages?: number;
  /** Bound how many live content previews are retained at once. */
  maxPreviews?: number;
  /** Bound each accumulated preview in UTF-8 bytes. */
  maxPreviewBytes?: number;
}

/**
 * Folds durable frames and provisional previews into one current view.
 *
 * A Turn's lifecycle is folded, not logged: the reducer keeps the highest
 * revision it has seen for each Turn and nothing earlier. Every consumer in
 * this repository already reduced the log that way before publishing it, and
 * the `current` flag cannot substitute for the fold — it means "current as of
 * the read that produced this frame", so two frames for one Turn leave two
 * entries both claiming to be current.
 */
export class Reducer<TOutput extends object = Record<string, never>> {
  private readonly messages = new Map<number, ConversationMessage>();
  private readonly changes = new Map<string, TypedTurnChange<TOutput>>();
  private readonly previews = new Map<string, StreamPreview>();
  private readonly latestAttempts = new Map<string, number>();
  private readonly terminalTurns = new Set<string>();
  private cursor?: string;
  private readonly maxMessages?: number;
  private readonly maxPreviews?: number;
  private readonly maxPreviewBytes?: number;

  constructor(options: ReducerOptions<TOutput> = {}) {
    this.maxMessages = positiveBound(options.maxMessages, "maxMessages");
    this.maxPreviews = positiveBound(options.maxPreviews, "maxPreviews");
    this.maxPreviewBytes = positiveBound(options.maxPreviewBytes, "maxPreviewBytes");
    const initial = options.initial;
    if (!initial) return;
    for (const message of initial.messages ?? []) {
      this.messages.set(message.sequence, message);
    }
    for (const change of initial.turnChanges ?? []) this.storeChange(change);
    for (const preview of initial.previews ?? []) {
      this.previews.set(`${preview.messageId}:${preview.contentIndex}`, { ...preview });
    }
    this.cursor = initial.cursor;
    this.enforceBounds();
  }

  /**
   * Folds a REST transcript page in without moving the stream cursor.
   *
   * This is how a live consumer prepends older history: the page is older than
   * everything the stream has delivered, so adopting its position would replay
   * the conversation from the middle.
   *
   * Bounds are not enforced here. Eviction is oldest-first, and the page being
   * merged is the oldest thing in the window, so enforcing them would discard
   * exactly what was just fetched. A caller with a bound checks the window has
   * room before merging, as the conversation controller does.
   */
  merge(snapshot: Pick<ReducedSnapshot<TOutput>, "messages" | "turnChanges">): void {
    for (const message of snapshot.messages) this.messages.set(message.sequence, message);
    for (const change of snapshot.turnChanges) this.storeChange(change);
  }

  apply(frame: StreamFrame<TOutput>): void {
    if (frame.type === "message.delta") {
      this.appendPreview(frame);
      return;
    }
    if (frame.type === "stream.resync") {
      if (frame.turnId) this.discardPreviews(frame.turnId);
      else {
        this.previews.clear();
        this.latestAttempts.clear();
      }
      return;
    }
    if (frame.type !== "transcript.update") return;

    // A saved message replaces only the preview that was building it. The
    // Turn's next message may already be accumulating, and dropping that
    // prefix loses text no later delta restores.
    for (const message of frame.messages) {
      this.messages.set(message.sequence, message);
      this.discardMessagePreviews(message.id);
    }
    for (const change of frame.turnChanges) {
      this.storeChange(change);
      if (isTurnOver(change)) this.discardPreviews(change.turnId);
    }
    this.cursor = frame.sseId || frame.cursor;
    this.enforceBounds();
  }

  settled(turnId: string): boolean {
    return this.terminalTurns.has(turnId);
  }

  snapshot(): ReducedSnapshot<TOutput> {
    return {
      messages: [...this.messages.values()].sort((left, right) => left.sequence - right.sequence),
      turnChanges: [...this.changes.values()]
        .sort((left, right) => left.turnId.localeCompare(right.turnId)),
      previews: [...this.previews.values()]
        .map((preview) => ({ ...preview }))
        .sort((left, right) => left.messageId.localeCompare(right.messageId)
          || left.contentIndex - right.contentIndex),
      cursor: this.cursor,
    };
  }

  private storeChange(change: TypedTurnChange<TOutput>): void {
    const current = this.changes.get(change.turnId);
    if (!current || change.revision > current.revision) {
      this.changes.set(change.turnId, change);
    }
    if (isTurnOver(change)) this.terminalTurns.add(change.turnId);
  }

  private appendPreview(delta: MessageDeltaEvent): void {
    if (this.terminalTurns.has(delta.turnId)) return;
    const latestAttempt = this.latestAttempts.get(delta.turnId);
    if (latestAttempt !== undefined && delta.attempt < latestAttempt) return;
    if (latestAttempt === undefined || delta.attempt > latestAttempt) {
      this.discardPreviews(delta.turnId);
      this.latestAttempts.set(delta.turnId, delta.attempt);
    }
    const key = `${delta.messageId}:${delta.contentIndex}`;
    const preview = this.previews.get(key) ?? {
      turnId: delta.turnId,
      attempt: delta.attempt,
      messageId: delta.messageId,
      contentIndex: delta.contentIndex,
      kind: delta.kind,
      delta: "",
    };
    preview.attempt = delta.attempt;
    preview.kind = delta.kind;
    preview.delta = appendUTF8(preview.delta, delta.delta, this.maxPreviewBytes);
    if (delta.toolCallId) preview.toolCallId = delta.toolCallId;
    if (delta.name) preview.name = delta.name;
    this.previews.set(key, preview);
    if (this.maxPreviews === undefined) return;
    // Map iteration is insertion order, so the first key is the oldest
    // preview still accumulating.
    while (this.previews.size > this.maxPreviews) {
      const oldest = this.previews.keys().next().value;
      if (oldest === undefined) break;
      this.previews.delete(oldest);
    }
  }

  private discardPreviews(turnId: string): void {
    for (const [key, preview] of this.previews) {
      if (preview.turnId === turnId) this.previews.delete(key);
    }
    this.latestAttempts.delete(turnId);
  }

  private discardMessagePreviews(messageId: string): void {
    for (const [key, preview] of this.previews) {
      if (preview.messageId === messageId) this.previews.delete(key);
    }
  }

  /**
   * Evicts oldest-first, and only at a Turn boundary that has settled.
   *
   * Dropping half a Turn would leave a transcript that reads as though the
   * agent answered a question nobody asked. A live Turn is never evicted at
   * all: it is still producing the messages that finish it, so the window
   * stops growing down rather than cutting into work in progress.
   */
  private enforceBounds(): void {
    if (this.maxMessages === undefined) return;
    while (this.messages.size > this.maxMessages) {
      const ordered = [...this.messages.values()]
        .sort((left, right) => left.sequence - right.sequence);
      const oldest = ordered[0];
      if (!oldest) return;
      const boundary = oldest.turnId;
      if (!boundary) {
        this.messages.delete(oldest.sequence);
        continue;
      }
      if (!this.terminalTurns.has(boundary)) return;
      for (const message of ordered) {
        if (message.turnId === boundary) this.messages.delete(message.sequence);
      }
      this.changes.delete(boundary);
      this.terminalTurns.delete(boundary);
    }
  }
}

function positiveBound(value: number | undefined, name: string): number | undefined {
  if (value === undefined) return undefined;
  if (!Number.isSafeInteger(value) || value < 1) {
    throw new NvokenError("validation", `${name} must be a positive integer`);
  }
  return value;
}

/**
 * Appends within a UTF-8 byte budget, truncating at a code-point boundary.
 *
 * A preview is text a page renders. Cutting one mid code point produces a
 * replacement character that stays on screen until the saved message replaces
 * the whole preview, which is exactly the kind of glitch a bound meant to
 * prevent problems introduces instead.
 */
function appendUTF8(current: string, added: string, maximum?: number): string {
  if (maximum === undefined) return current + added;
  const combined = current + added;
  const bytes = new TextEncoder().encode(combined);
  if (bytes.length <= maximum) return combined;
  const decoder = new TextDecoder("utf-8", { fatal: true });
  for (let end = maximum; end > 0; end -= 1) {
    try {
      return decoder.decode(bytes.slice(0, end));
    } catch {
      // Step back to the prior code-point boundary.
    }
  }
  return "";
}

export interface StreamLoopOptions {
  cursor?: string;
  signal?: AbortSignal;
  timeoutMs?: number;
  reconnectTimeoutMs?: number;
  /**
   * Reports when the transport is up and when it is about to reconnect.
   *
   * A deliberate close is already visible as a `connection.closing` frame. A
   * silent drop is not, and "reconnecting" is the state a page needs in order
   * to stop looking broken while it heals. Observational only: a throw here
   * cannot change what the stream delivers, so it is swallowed.
   */
  onConnectionChange?: (state: "connected" | "reconnecting") => void;
}

export type StreamConnector = (
  cursor: string | undefined,
  signal: AbortSignal | undefined,
) => Promise<Response>;

export function streamTurnFrames<TOutput extends object = Record<string, never>>(
  connect: StreamConnector,
  options: StreamLoopOptions = {},
): AsyncGenerator<StreamFrame<TOutput>> {
  return reconnectingFrames(connect, options, true);
}

export function streamConversationFrames<TOutput extends object = Record<string, never>>(
  connect: StreamConnector,
  options: StreamLoopOptions = {},
): AsyncGenerator<StreamFrame<TOutput>> {
  return reconnectingFrames(connect, options, false);
}

async function* reconnectingFrames<TOutput extends object>(
  connect: StreamConnector,
  options: StreamLoopOptions,
  stopAtTerminalTurn: boolean,
): AsyncGenerator<StreamFrame<TOutput>> {
  const scope = abortScope(options.signal, options.timeoutMs);
  const reconnectTimeoutMs = options.reconnectTimeoutMs ?? 300_000;
  if (!Number.isFinite(reconnectTimeoutMs) || reconnectTimeoutMs <= 0) {
    scope.dispose();
    throw new TypeError("reconnectTimeoutMs must be a positive finite number");
  }
  let cursor = options.cursor;
  let delayMs = 100;
  let failureStartedAt: number | undefined;
  let serverRetryMs: number | undefined;
  try {
    while (!scope.signal?.aborted) {
      try {
        const response = await connect(cursor, scope.signal);
        notifyConnection(options, "connected");
        failureStartedAt = undefined;
        let requestedReconnect = false;
        for await (const event of parseEventStream(response, scope.signal)) {
          if (event.retryMs !== undefined && Number.isFinite(event.retryMs) && event.retryMs >= 0) {
            serverRetryMs = Math.min(10_000, event.retryMs);
          }
          // A frame containing only `retry:` is SSE control data, not an
          // nvoken event. Apply its reconnect delay without trying to decode it.
          if (event.data === undefined) continue;
          const frame = decodeStreamFrame<TOutput>(event);
          // Added frame types must not break an older reader. Unknown frames
          // carry no state this version knows how to reduce, so skip them.
          if (frame === undefined) continue;
          if (frame.sseId) cursor = frame.sseId;
          if (frame.type === "transcript.update") cursor = frame.cursor || cursor;
          yield frame;
          if (stopAtTerminalTurn
            && frame.type === "transcript.update"
            && frame.turnChanges.some((change) => isTurnOver(change))) return;
          if (frame.type === "connection.closing") {
            requestedReconnect = true;
            break;
          }
        }
        delayMs = serverRetryMs ?? (requestedReconnect ? 0 : 100);
      } catch (error) {
        if (scope.signal?.aborted) throw abortReason(scope.signal);
        if (!isRetryableStreamError(error)) throw error;
        failureStartedAt ??= Date.now();
        if (Date.now() - failureStartedAt >= reconnectTimeoutMs) throw error;
      }
      notifyConnection(options, "reconnecting");
      if (delayMs > 0) await pause(delayMs, scope.signal);
      delayMs = serverRetryMs ?? Math.min(2_000, Math.max(100, delayMs * 2));
    }
    throw abortReason(scope.signal);
  } finally {
    scope.dispose();
  }
}

function notifyConnection(
  options: StreamLoopOptions,
  state: "connected" | "reconnecting",
): void {
  try {
    options.onConnectionChange?.(state);
  } catch {
    // Transport correctness cannot depend on an observational callback.
  }
}

interface ParsedEvent {
  id?: string;
  type: string;
  data?: unknown;
  retryMs?: number;
}

async function* parseEventStream(
  response: Response,
  signal?: AbortSignal,
): AsyncGenerator<ParsedEvent> {
  if (!response.body) throw new TypeError("nvoken stream response has no body");
  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  try {
    while (true) {
      if (signal?.aborted) throw abortReason(signal);
      const { done, value } = await reader.read();
      buffer += decoder.decode(value, { stream: !done });
      let boundary = eventBoundary(buffer);
      while (boundary !== undefined) {
        const raw = buffer.slice(0, boundary.index);
        buffer = buffer.slice(boundary.index + boundary.length);
        const event = parseEvent(raw);
        if (event) yield event;
        boundary = eventBoundary(buffer);
      }
      if (done) {
        const event = parseEvent(buffer);
        if (event) yield event;
        return;
      }
    }
  } finally {
    reader.releaseLock();
  }
}

function eventBoundary(value: string): { index: number; length: number } | undefined {
  const match = /\r?\n\r?\n/.exec(value);
  return match ? { index: match.index, length: match[0].length } : undefined;
}

function parseEvent(raw: string): ParsedEvent | undefined {
  if (!raw.trim()) return undefined;
  const data: string[] = [];
  let id: string | undefined;
  let type = "message";
  let retryMs: number | undefined;
  for (const line of raw.split(/\r?\n/)) {
    if (!line || line.startsWith(":")) continue;
    const separator = line.indexOf(":");
    const field = separator < 0 ? line : line.slice(0, separator);
    const value = separator < 0 ? "" : line.slice(separator + 1).replace(/^ /, "");
    if (field === "data") data.push(value);
    else if (field === "event") type = value;
    else if (field === "id" && !value.includes("\0")) id = value;
    else if (field === "retry" && /^\d+$/.test(value)) retryMs = Number(value);
  }
  const joined = data.join("\n");
  let decoded: unknown;
  if (data.length > 0 && joined) {
    try { decoded = JSON.parse(joined); }
    catch (error) { throw new TypeError(`invalid ${type} stream JSON`, { cause: error }); }
  } else if (data.length > 0) {
    decoded = "";
  }
  return { id, type, data: decoded, retryMs };
}

function decodeStreamFrame<TOutput extends object>(
  event: ParsedEvent,
): StreamFrame<TOutput> | undefined {
  if (!event.data || typeof event.data !== "object") {
    throw new NvokenError(
      "unexpected_response",
      `${event.type} stream frame must contain a JSON object`,
    );
  }
  const metadata = { sseId: event.id, retryMs: event.retryMs };
  switch (event.type) {
    case "transcript.update": {
      const update = requireFrameField(
        "transcript.update",
        TranscriptUpdateEventFromJSON(event.data),
        instanceOfTranscriptUpdateEvent,
      );
      for (const message of update.messages) {
        requireFrameField("conversation message", message, instanceOfConversationMessage);
      }
      for (const change of update.turnChanges) {
        requireFrameField("turn change", change, instanceOfTurnChange);
      }
      return { ...update, ...metadata } as StreamFrame<TOutput>;
    }
    case "message.delta":
      return {
        ...requireFrameField(
          "message.delta",
          MessageDeltaEventFromJSON(event.data),
          instanceOfMessageDeltaEvent,
        ),
        ...metadata,
      } as StreamFrame<TOutput>;
    case "stream.resync":
      return {
        ...requireFrameField(
          "stream.resync",
          StreamResyncEventFromJSON(event.data),
          instanceOfStreamResyncEvent,
        ),
        ...metadata,
      } as StreamFrame<TOutput>;
    case "connection.closing":
      return {
        ...requireFrameField(
          "connection.closing",
          ConnectionClosingEventFromJSON(event.data),
          instanceOfConnectionClosingEvent,
        ),
        ...metadata,
      } as StreamFrame<TOutput>;
    default:
      return undefined;
  }
}

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

function isRetryableStreamError(error: unknown): boolean {
  if (error instanceof ResponseError) {
    return error.response.status === 408
      || error.response.status === 425
      || error.response.status === 429
      || error.response.status >= 500;
  }
  return error instanceof TypeError;
}

function pause(milliseconds: number, signal?: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    if (signal?.aborted) { reject(abortReason(signal)); return; }
    const onAbort = () => { clearTimeout(timer); reject(abortReason(signal)); };
    const timer = setTimeout(() => {
      signal?.removeEventListener("abort", onAbort);
      resolve();
    }, milliseconds);
    signal?.addEventListener("abort", onAbort, { once: true });
  });
}

interface AbortScope {
  signal?: AbortSignal;
  dispose(): void;
}

function abortScope(signal: AbortSignal | undefined, timeoutMs: number | undefined): AbortScope {
  if (timeoutMs === undefined) return { signal, dispose: () => undefined };
  if (!Number.isFinite(timeoutMs) || timeoutMs <= 0) {
    throw new TypeError("timeoutMs must be a positive finite number");
  }
  const controller = new AbortController();
  const abort = () => controller.abort(signal?.reason);
  if (signal?.aborted) abort();
  else signal?.addEventListener("abort", abort, { once: true });
  const timer = setTimeout(() => controller.abort(new DOMException("Timed out", "AbortError")), timeoutMs);
  return {
    signal: controller.signal,
    dispose: () => { clearTimeout(timer); signal?.removeEventListener("abort", abort); },
  };
}

function abortReason(signal?: AbortSignal): unknown {
  return signal?.reason ?? new DOMException("Aborted", "AbortError");
}
