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
      messages: [],
      turn_changes: [{
        turn_id: "476dd7be-97a1-78f3-8096-d7032468a80a",
        conversation_id: null,
        content_expires_at: null,
        revision,
        status,
        terminal: status === "completed",
        current: true,
        through_message_sequence: null,
        error: null,
        structured_output: null,
        occurred_at: "2026-07-21T12:00:00Z",
        ...(status === "completed" ? { stop_reason: "end_turn" } : {}),
        tool_calls: [],
      }],
      has_more: false,
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

function message(
  sequence: number,
  turnId: string | null,
  text = `m${sequence}`,
): Record<string, unknown> {
  return {
    id: `00000000-0000-7000-8000-${String(sequence).padStart(12, "0")}`,
    conversation_id: "db4eaf24-1ac6-776e-8f98-badc6d0dc764",
    agent_id: "e1ec4be4-ffd5-7789-83bc-fb8f0f1eb276",
    turn_id: turnId,
    content_expires_at: null,
    user_key: null,
    sequence,
    role: sequence % 2 === 1 ? "user" : "assistant",
    content: [{ type: "text", text }],
    created_at: "2026-07-21T12:00:00Z",
  };
}

function change(
  turnId: string,
  revision: number,
  status: "running" | "completed",
): Record<string, unknown> {
  return {
    turn_id: turnId,
    conversation_id: "db4eaf24-1ac6-776e-8f98-badc6d0dc764",
    content_expires_at: null,
    revision,
    status,
    terminal: status === "completed",
    current: true,
    through_message_sequence: null,
    error: null,
    structured_output: null,
    occurred_at: "2026-07-21T12:00:00Z",
    tool_calls: [],
  };
}

function transcript(
  cursor: string,
  messages: Array<Record<string, unknown>>,
  changes: Array<Record<string, unknown>>,
): WireEvent {
  return {
    id: cursor,
    event: "transcript.update",
    data: {
      type: "transcript.update",
      messages,
      turn_changes: changes,
      has_more: false,
      cursor,
    },
  };
}

function delta(
  turnId: string,
  messageId: string,
  contentIndex: number,
  fragment: string,
): WireEvent {
  return {
    event: "message.delta",
    data: {
      type: "message.delta",
      turn_id: turnId,
      attempt: 1,
      message_id: messageId,
      content_index: contentIndex,
      offset: 0,
      kind: "text",
      delta: fragment,
      emitted_at: "2026-07-21T12:00:00Z",
    },
  };
}

test("two revisions for one Turn fold to one current change", async () => {
  const turnId = "476dd7be-97a1-78f3-8096-d7032468a80a";
  const reducer = new Reducer();
  const frames = await decodeConversation([
    transcript("cursor-1", [], [change(turnId, 1, "running")]),
    transcript("cursor-2", [], [change(turnId, 2, "completed")]),
  ]);
  for (const frame of frames) reducer.apply(frame);

  const changes = reducer.snapshot().turnChanges;
  assert.equal(changes.length, 1);
  assert.equal(changes[0].revision, 2);
  assert.equal(changes[0].status, "completed");
  assert.equal(reducer.settled(turnId), true);
});

test("an out-of-order lower revision never replaces the current change", async () => {
  const turnId = "476dd7be-97a1-78f3-8096-d7032468a80a";
  const reducer = new Reducer();
  const frames = await decodeConversation([
    transcript("cursor-1", [], [change(turnId, 2, "completed")]),
    transcript("cursor-2", [], [change(turnId, 1, "running")]),
  ]);
  for (const frame of frames) reducer.apply(frame);

  const changes = reducer.snapshot().turnChanges;
  assert.equal(changes.length, 1);
  assert.equal(changes[0].revision, 2);
});

test("the reducer seeds from a bounded tail and its exact cursor", () => {
  const reducer = new Reducer({
    initial: {
      messages: [message(7, null) as never, message(8, null) as never],
      cursor: "cursor-tail",
    },
    maxMessages: 500,
  });
  const snapshot = reducer.snapshot();
  assert.deepEqual(snapshot.messages.map((item) => item.sequence), [7, 8]);
  assert.equal(snapshot.cursor, "cursor-tail");
});

test("merging an older page leaves the live cursor where it was", async () => {
  const reducer = new Reducer({ initial: { messages: [], cursor: "cursor-live" } });
  reducer.merge({
    messages: [message(1, null) as never, message(2, null) as never],
    turnChanges: [],
  });
  assert.deepEqual(reducer.snapshot().messages.map((item) => item.sequence), [1, 2]);
  assert.equal(reducer.snapshot().cursor, "cursor-live");
});

test("a non-positive reducer bound is refused as a validation error", () => {
  for (const options of [
    { maxMessages: 0 },
    { maxPreviews: -1 },
    { maxPreviewBytes: 1.5 },
  ]) {
    assert.throws(
      () => new Reducer(options),
      (error: unknown) => error instanceof NvokenError && error.category === "validation",
    );
  }
});

test("message eviction removes whole terminal Turns and stops at a live one", async () => {
  const settled = "476dd7be-97a1-78f3-8096-d7032468a80a";
  const live = "8f2b0c1e-3d4a-7b5c-9e6f-0a1b2c3d4e5f";
  const reducer = new Reducer({ maxMessages: 3 });
  const frames = await decodeConversation([
    transcript(
      "cursor-1",
      [message(1, settled), message(2, settled)],
      [change(settled, 1, "completed")],
    ),
    transcript(
      "cursor-2",
      [message(3, live), message(4, live)],
      [change(live, 1, "running")],
    ),
  ]);
  for (const frame of frames) reducer.apply(frame);

  const snapshot = reducer.snapshot();
  // The settled Turn goes as a unit; the live Turn is never cut into, so the
  // window sits above its bound rather than dropping half an exchange.
  assert.deepEqual(snapshot.messages.map((item) => item.sequence), [3, 4]);
  assert.deepEqual(snapshot.turnChanges.map((item) => item.turnId), [live]);
  assert.equal(reducer.settled(settled), false);
});

test("preview eviction drops the oldest preview past the bound", async () => {
  const turnId = "476dd7be-97a1-78f3-8096-d7032468a80a";
  const reducer = new Reducer({ maxPreviews: 2 });
  const frames = await decodeConversation([
    delta(turnId, "aaaaaaaa-0000-7000-8000-000000000001", 0, "one"),
    delta(turnId, "aaaaaaaa-0000-7000-8000-000000000002", 0, "two"),
    delta(turnId, "aaaaaaaa-0000-7000-8000-000000000003", 0, "three"),
  ]);
  for (const frame of frames) reducer.apply(frame);

  const previews = reducer.snapshot().previews;
  assert.equal(previews.length, 2);
  assert.deepEqual(previews.map((preview) => preview.delta), ["two", "three"]);
});

test("a preview truncated at its byte bound never splits a code point", async () => {
  const turnId = "476dd7be-97a1-78f3-8096-d7032468a80a";
  const messageId = "aaaaaaaa-0000-7000-8000-000000000001";
  // Four bytes each, so a bound of 6 lands inside the second character.
  const reducer = new Reducer({ maxPreviewBytes: 6 });
  const frames = await decodeConversation([
    delta(turnId, messageId, 0, "🙂"),
    delta(turnId, messageId, 0, "🙃"),
  ]);
  for (const frame of frames) reducer.apply(frame);

  const preview = reducer.snapshot().previews[0];
  assert.equal(preview.delta, "🙂");
  assert.equal(new TextEncoder().encode(preview.delta).length, 4);
  assert.ok(!preview.delta.includes("�"));
});

test("the connection callback reports connect and each reconnect", async () => {
  const states: string[] = [];
  const closing: WireEvent = {
    event: "connection.closing",
    data: { type: "connection.closing", reason: "rotation", retry_after_ms: 0 },
  };
  const completed = turnUpdate("completed", 1, "cursor-1");
  let connects = 0;
  const connect = async () => {
    connects += 1;
    return eventResponse(connects === 1 ? [closing] : [completed]);
  };

  for await (const _frame of streamTurnFrames(connect, {
    onConnectionChange: (state) => states.push(state),
  })) {
    // Iteration drives the loop; the callback records the transitions.
  }

  assert.deepEqual(states, ["connected", "reconnecting", "connected"]);
});

test("a throwing connection callback cannot break the stream", async () => {
  const completed = turnUpdate("completed", 1, "cursor-1");
  const frames: StreamFrame[] = [];
  for await (const frame of streamTurnFrames(async () => eventResponse([completed]), {
    onConnectionChange: () => { throw new Error("renderer blew up"); },
  })) {
    frames.push(frame);
  }
  assert.deepEqual(frames.map((frame) => frame.type), ["transcript.update"]);
});
