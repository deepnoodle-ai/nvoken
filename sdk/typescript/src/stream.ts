import type { Client, Handle } from "./client.js";
import { NvokenError } from "./client.js";
import type { InvocationChange, SessionMessage } from "./generated/models/index.js";
import { StreamEndEventFromJSON, TranscriptSnapshotFromJSON } from "./generated/models/index.js";

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

export class Reducer {
  private readonly messages = new Map<number, SessionMessage>();
  private readonly changes = new Map<string, InvocationChange>();
  private cursor?: string;

  apply(event: StreamEvent): void {
    if (event.type !== "transcript.snapshot") return;
    const snapshot = TranscriptSnapshotFromJSON(event.data);
    for (const message of snapshot.messages) this.messages.set(message.sequence, message);
    for (const change of snapshot.invocationChanges) {
      this.changes.set(`${change.invocationId}:${change.revision}`, change);
    }
    this.cursor = event.id ?? snapshot.resumeCursor ?? this.cursor;
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

export async function streamSession(
  client: Client,
  handle: Handle,
  reducer: Reducer,
  consume: (event: StreamEvent, snapshot: ReducedSnapshot) => void | Promise<void>,
  signal?: AbortSignal,
): Promise<void> {
  let retryMs = 1_000;
  for (;;) {
    const request = await client.sessions.streamSessionTranscriptRequestOpts({
      sessionId: handle.sessionId,
      lastEventID: reducer.snapshot().resumeCursor,
    });
    const url = new URL(client.configuration.basePath + request.path);
    for (const [name, value] of Object.entries(request.query ?? {})) {
      if (value !== undefined && value !== null) url.searchParams.set(name, String(value));
    }
    let response: Response;
    try {
      response = await client.fetch(url, { method: request.method, headers: request.headers, signal });
    } catch (error) {
      if (signal?.aborted) throw new NvokenError("timeout", "local stream was cancelled", undefined, undefined, undefined, undefined, undefined, { cause: error });
      await delay(retryMs, signal);
      continue;
    }
    if (!response.ok) {
      let body: { message?: string; code?: string; request_id?: string; details?: Record<string, unknown> } = {};
      try { body = await response.json() as typeof body; } catch { /* status is sufficient */ }
      throw new NvokenError(
        response.status === 401 || response.status === 403 ? "authentication" : response.status === 404 ? "not_found" : response.status >= 500 ? "server" : "unexpected_response",
        body.message ?? `nvoken returned HTTP ${response.status}`,
        response.status,
        body.code,
        body.request_id,
        parseRetryAfter(response.headers.get("retry-after")),
        body.details,
      );
    }
    if (!response.body) throw new NvokenError("unexpected_response", "stream response had no body");
    let terminalEnd = false;
    for await (const event of parseSSE(response.body)) {
      if (event.retryMs !== undefined) retryMs = Math.min(event.retryMs, 30_000);
      reducer.apply(event);
      await consume(event, reducer.snapshot());
      if (event.type === "stream.end" && StreamEndEventFromJSON(event.data).reason === "terminal") {
        terminalEnd = true;
      }
    }
    if (terminalEnd) {
      const invocation = await handle.refresh(signal);
      if (invocation.status === "completed" || invocation.status === "failed" || invocation.status === "cancelled") return;
    }
    await delay(retryMs, signal);
  }
}

export async function* parseSSE(body: ReadableStream<Uint8Array>): AsyncGenerator<StreamEvent> {
  const reader = body.pipeThrough(new TextDecoderStream()).getReader();
  let buffer = "";
  let event: Partial<StreamEvent> = {};
  let data: string[] = [];
  const dispatch = (): StreamEvent | undefined => {
    if (!event.type && !event.id && data.length === 0 && event.retryMs === undefined) return undefined;
    let decoded: unknown = undefined;
    if (data.length > 0) decoded = JSON.parse(data.join("\n"));
    const value: StreamEvent = { type: event.type ?? "message", id: event.id, retryMs: event.retryMs, data: decoded };
    event = {};
    data = [];
    return value;
  };
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
}

function parseRetryAfter(value: string | null): number | undefined {
  if (!value) return undefined;
  const seconds = Number(value);
  if (Number.isFinite(seconds) && seconds >= 0) return seconds * 1_000;
  const when = Date.parse(value);
  return Number.isNaN(when) ? undefined : Math.max(0, when - Date.now());
}

function delay(milliseconds: number, signal?: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    if (signal?.aborted) return reject(new NvokenError("timeout", "local stream was cancelled"));
    const timer = setTimeout(resolve, milliseconds);
    signal?.addEventListener("abort", () => {
      clearTimeout(timer);
      reject(new NvokenError("timeout", "local stream was cancelled"));
    }, { once: true });
  });
}
