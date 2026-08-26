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

export class Reducer<TOutput extends object = Record<string, never>> {
  private readonly messages = new Map<number, ConversationMessage>();
  private readonly changes = new Map<string, TypedTurnChange<TOutput>>();
  private readonly previews = new Map<string, StreamPreview>();
  private readonly latestAttempts = new Map<string, number>();
  private readonly terminalTurns = new Set<string>();
  private cursor?: string;

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

    for (const message of frame.messages) {
      this.messages.set(message.sequence, message);
      if (message.role === "assistant" && message.turnId) this.discardPreviews(message.turnId);
    }
    for (const change of frame.turnChanges) {
      this.changes.set(`${change.turnId}:${change.revision}`, change);
      if (isTurnOver(change)) {
        this.terminalTurns.add(change.turnId);
        this.discardPreviews(change.turnId);
      }
    }
    this.cursor = frame.sseId || frame.cursor;
  }

  settled(turnId: string): boolean {
    return this.terminalTurns.has(turnId);
  }

  snapshot(): ReducedSnapshot<TOutput> {
    return {
      messages: [...this.messages.values()].sort((left, right) => left.sequence - right.sequence),
      turnChanges: [...this.changes.values()].sort((left, right) => {
        const turnOrder = left.turnId.localeCompare(right.turnId);
        return turnOrder || left.revision - right.revision;
      }),
      previews: [...this.previews.values()]
        .map((preview) => ({ ...preview }))
        .sort((left, right) => left.messageId.localeCompare(right.messageId)
          || left.contentIndex - right.contentIndex),
      cursor: this.cursor,
    };
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
    preview.delta += delta.delta;
    if (delta.toolCallId) preview.toolCallId = delta.toolCallId;
    if (delta.name) preview.name = delta.name;
    this.previews.set(key, preview);
  }

  private discardPreviews(turnId: string): void {
    for (const [key, preview] of this.previews) {
      if (preview.turnId === turnId) this.previews.delete(key);
    }
    this.latestAttempts.delete(turnId);
  }
}

export interface StreamLoopOptions {
  cursor?: string;
  signal?: AbortSignal;
  timeoutMs?: number;
  reconnectTimeoutMs?: number;
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
      if (delayMs > 0) await pause(delayMs, scope.signal);
      delayMs = serverRetryMs ?? Math.min(2_000, Math.max(100, delayMs * 2));
    }
    throw abortReason(scope.signal);
  } finally {
    scope.dispose();
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
