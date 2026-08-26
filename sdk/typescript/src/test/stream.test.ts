import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

import { NvokenError } from "../turn-error.js";
import {
  Reducer,
  streamConversationFrames,
  streamTurnFrames,
  type StreamFrame,
  type StreamPreview,
} from "../stream.js";

interface WireEvent {
  id?: string;
  event: string;
  data: Record<string, unknown>;
}

interface ReducerFixture {
  events: WireEvent[];
  preview_cases: Array<{
    name: string;
    events: WireEvent[];
    expected_previews: Array<{
      turn_id: string;
      attempt: number;
      message_id: string;
      content_index: number;
      kind: string;
      delta: string;
      tool_call_id?: string;
      name?: string;
    }>;
  }>;
  expected: {
    message_sequences: number[];
    turn_revisions: number[];
    cursor: string;
    previews: unknown[];
  };
}

async function reducerFixture(): Promise<ReducerFixture> {
  return JSON.parse(await readFile(
    new URL("../../../conformance/fixtures/reducer.json", import.meta.url),
    "utf8",
  )) as ReducerFixture;
}

function eventText(event: WireEvent, retryMs?: number): string {
  return `${event.id !== undefined ? `id: ${event.id}\n` : ""}`
    + `${retryMs === undefined ? "" : `retry: ${retryMs}\n`}`
    + `event: ${event.event}\n`
    + `data: ${JSON.stringify(event.data)}\n\n`;
}

function eventResponse(events: WireEvent[]): Response {
  return new Response(events.map((event) => eventText(event)).join(""), {
    status: 200,
    headers: { "content-type": "text/event-stream" },
  });
}

async function decodeConversation(events: WireEvent[]): Promise<StreamFrame[]> {
  const frames: StreamFrame[] = [];
  for await (const frame of streamConversationFrames(async () => eventResponse(events))) {
    frames.push(frame);
    if (frames.length === events.length) break;
  }
  return frames;
}

function wirePreview(preview: StreamPreview): Record<string, unknown> {
  return {
    turn_id: preview.turnId,
    attempt: preview.attempt,
    message_id: preview.messageId,
    content_index: preview.contentIndex,
    kind: preview.kind,
    delta: preview.delta,
    ...(preview.toolCallId ? { tool_call_id: preview.toolCallId } : {}),
    ...(preview.name ? { name: preview.name } : {}),
  };
}

test("the shared reducer fixture folds target Conversation and Turn keys", async () => {
  const fixture = await reducerFixture();
  const reducer = new Reducer();
  for (const frame of await decodeConversation(fixture.events)) reducer.apply(frame);

  const snapshot = reducer.snapshot();
  assert.deepEqual(
    snapshot.messages.map((message) => message.sequence),
    fixture.expected.message_sequences,
  );
  assert.deepEqual(
    snapshot.turnChanges.map((change) => change.revision),
    fixture.expected.turn_revisions,
  );
  assert.equal(snapshot.cursor, fixture.expected.cursor);
  assert.deepEqual(snapshot.previews, fixture.expected.previews);
  assert.equal(reducer.settled(snapshot.turnChanges[0].turnId), true);
});

test("every shared preview case follows attempt, resync, and durable handoff rules", async () => {
  const fixture = await reducerFixture();
  for (const previewCase of fixture.preview_cases) {
    const reducer = new Reducer();
    for (const frame of await decodeConversation(previewCase.events)) reducer.apply(frame);
    assert.deepEqual(
      reducer.snapshot().previews.map(wirePreview),
      previewCase.expected_previews,
      previewCase.name,
    );
  }
});

function turnUpdate(
  status: "running" | "completed",
  revision: number,
  cursor: string,
): WireEvent {
  return {
    id: cursor,
    event: "transcript.update",
    data: {
      type: "transcript.update",
      conversation_id: null,
      content_expires_at: null,
      messages: [],
      turn_changes: [{
        turn_id: "turn_1",
        conversation_id: null,
        content_expires_at: null,
        revision,
        status,
        terminal: status === "completed",
        stop_reason: status === "completed" ? "end_turn" : null,
        through_message_sequence: null,
        error: null,
        structured_output: null,
        occurred_at: "2026-07-21T12:00:00Z",
      }],
      cursor,
    },
  };
}

test("a Turn stream reconnects from its durable cursor and stops at settlement", async () => {
  const cursors: Array<string | undefined> = [];
  const running = turnUpdate("running", 1, "cursor-1");
  const completed = turnUpdate("completed", 2, "cursor-2");
  let connects = 0;
  const connect = async (cursor: string | undefined) => {
    cursors.push(cursor);
    connects += 1;
    if (connects === 1) {
      return new Response(eventText(running, 1), {
        status: 200,
        headers: { "content-type": "text/event-stream" },
      });
    }
    return eventResponse([completed]);
  };

  const frames: StreamFrame[] = [];
  for await (const frame of streamTurnFrames(connect, { reconnectTimeoutMs: 1_000 })) {
    frames.push(frame);
  }

  assert.deepEqual(cursors, [undefined, "cursor-1"]);
  assert.deepEqual(frames.map((frame) => frame.type), [
    "transcript.update",
    "transcript.update",
  ]);
  assert.equal(frames[0].retryMs, 1);
  assert.equal((frames[1] as Extract<StreamFrame, { type: "transcript.update" }>).turnChanges[0].terminal, true);
});

test("control-only retry frames are applied without becoming events", async () => {
  const completed = turnUpdate("completed", 1, "cursor-1");
  const response = new Response(`retry: 1\n\n${eventText(completed)}`, {
    status: 200,
    headers: { "content-type": "text/event-stream" },
  });
  const frames: StreamFrame[] = [];
  for await (const frame of streamTurnFrames(async () => response)) frames.push(frame);
  assert.equal(frames.length, 1);
  assert.equal(frames[0].type, "transcript.update");
});

test("unknown future frame types are skipped without hiding known settlement", async () => {
  const future: WireEvent = {
    event: "turn.telemetry.added-later",
    data: { type: "turn.telemetry.added-later", value: 1 },
  };
  const completed = turnUpdate("completed", 1, "cursor-1");
  const frames: StreamFrame[] = [];
  for await (const frame of streamTurnFrames(async () => eventResponse([future, completed]))) {
    frames.push(frame);
  }
  assert.deepEqual(frames.map((frame) => frame.type), ["transcript.update"]);
});

test("a Turn change missing a required field is refused", async () => {
  const invalid = turnUpdate("completed", 1, "cursor-1");
  const changes = invalid.data.turn_changes as Array<Record<string, unknown>>;
  delete changes[0].terminal;

  await assert.rejects(
    async () => {
      for await (const _frame of streamTurnFrames(async () => eventResponse([invalid]))) {
        // Iteration itself performs decoding and validation.
      }
    },
    (error: unknown) => error instanceof NvokenError
      && error.category === "unexpected_response"
      && /turn change/.test(error.message),
  );
});
