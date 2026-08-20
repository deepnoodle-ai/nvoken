import assert from "node:assert/strict";
import test from "node:test";

import type { SessionCompaction, SessionMessage, StreamPreview } from "../index.js";
import {
  blockField,
  buildTranscript,
  foldMessages,
  groupPreviews,
  isErrorResult,
  isSettled,
  mediaReference,
} from "../transcript.js";

function message(
  sequence: number,
  role: SessionMessage["role"],
  content: unknown[],
): SessionMessage {
  return {
    id: `msg_${sequence}`,
    sessionId: "sess_1",
    agentId: "agent_1",
    invocationId: "inv_1",
    sequence,
    role,
    content,
    createdAt: new Date(0),
  } as SessionMessage;
}

function compaction(
  id: string,
  coversThrough: number,
  overrides: Partial<SessionCompaction> = {},
): SessionCompaction {
  return {
    id,
    invocationId: "inv_1",
    coversThrough,
    status: "applied",
    failureClass: null,
    usage: null,
    summary: "…",
    createdAt: new Date(coversThrough * 1000),
    ...overrides,
  } as SessionCompaction;
}

function preview(overrides: Partial<StreamPreview>): StreamPreview {
  return {
    invocationId: "inv_1",
    attempt: 1,
    messageId: "msg_1",
    contentIndex: 0,
    kind: "text",
    delta: "",
    ...overrides,
  };
}

// Both spellings are real, which is the whole reason `blockField` takes two
// names. A snapshot from the Reducer carries `toolUseId`; the same transcript
// read straight from the HTTP API carries `tool_use_id`. A version of this that
// reads only one of them strands every tool result on the other path.
test("blockField reads either spelling, preferring the modeled one", () => {
  assert.equal(blockField({ toolUseId: "a" }, "toolUseId", "tool_use_id"), "a");
  assert.equal(blockField({ tool_use_id: "b" }, "toolUseId", "tool_use_id"), "b");
  assert.equal(
    blockField({ toolUseId: "a", tool_use_id: "b" }, "toolUseId", "tool_use_id"),
    "a",
  );
  assert.equal(blockField({}, "toolUseId", "tool_use_id"), undefined);
});

test("foldMessages folds a result onto the call it answers, across messages", () => {
  const rendered = foldMessages([
    message(1, "assistant", [{ type: "tool_use", id: "call_1", name: "search" }]),
    message(2, "user", [{ type: "tool_result", toolUseId: "call_1", content: "hit" }]),
  ]);
  // The result message has nothing of its own left to show.
  assert.equal(rendered.length, 1);
  assert.equal(rendered[0].toolCalls.get("call_1")?.result?.content, "hit");
});

test("foldMessages folds a result that kept the runtime's spelling too", () => {
  const rendered = foldMessages([
    message(1, "assistant", [{ type: "tool_use", id: "call_1", name: "search" }]),
    message(2, "user", [{ type: "tool_result", tool_use_id: "call_1", content: "hit" }]),
  ]);
  assert.equal(rendered.length, 1);
  assert.equal(isSettled(rendered[0].toolCalls.get("call_1")!), true);
});

test("foldMessages reads an error result under either spelling", () => {
  const camel = foldMessages([
    message(1, "assistant", [{ type: "tool_use", id: "a", name: "t" }]),
    message(2, "user", [{ type: "tool_result", toolUseId: "a", isError: true }]),
  ]);
  assert.equal(isErrorResult(camel[0].toolCalls.get("a")!), true);

  const wire = foldMessages([
    message(1, "assistant", [{ type: "tool_use", id: "a", name: "t" }]),
    message(2, "user", [{ type: "tool_result", tool_use_id: "a", is_error: true }]),
  ]);
  assert.equal(isErrorResult(wire[0].toolCalls.get("a")!), true);
});

test("foldMessages keeps an orphan result visible rather than dropping it", () => {
  const rendered = foldMessages([
    message(1, "user", [{ type: "tool_result", toolUseId: "call_missing" }]),
  ]);
  assert.equal(rendered.length, 1);
});

test("foldMessages drops nothing else", () => {
  const rendered = foldMessages([
    message(1, "user", [{ type: "text", text: "hi" }]),
    message(2, "assistant", [{ type: "text", text: "hello" }]),
  ]);
  assert.deepEqual(
    rendered.map((entry) => entry.message.sequence),
    [1, 2],
  );
});

test("mediaReference reads a transcript media block, which carries no bytes", () => {
  assert.deepEqual(
    mediaReference({
      type: "document",
      mediaType: "application/pdf",
      title: "invoice.pdf",
      bytes: 2048,
      digest: "sha256:abc",
    }),
    {
      kind: "document",
      mediaType: "application/pdf",
      title: "invoice.pdf",
      bytes: 2048,
      digest: "sha256:abc",
    },
  );
});

test("mediaReference reads the wire spelling of the media type", () => {
  assert.equal(
    mediaReference({ type: "image", media_type: "image/png" })?.mediaType,
    "image/png",
  );
});

test("mediaReference is null for a block that is not media", () => {
  assert.equal(mediaReference({ type: "text", text: "hi" }), null);
});

const renderedFixture = () =>
  foldMessages([
    message(1, "user", [{ type: "text", text: "one" }]),
    message(2, "assistant", [{ type: "text", text: "two" }]),
    message(5, "user", [{ type: "text", text: "three" }]),
  ]);

test("buildTranscript puts the boundary after the last message the pass folded away", () => {
  const entries = buildTranscript(renderedFixture(), [compaction("cmp_1", 2)]);
  assert.deepEqual(
    entries.map((entry) => entry.key),
    ["msg_1", "msg_2", "cmp_1", "msg_5"],
  );
});

test("buildTranscript places a boundary that lands on a message the fold dropped", () => {
  // coversThrough 3 names a sequence with nothing rendered at it; the marker
  // still belongs in the gap before message 5, not after it.
  const entries = buildTranscript(renderedFixture(), [compaction("cmp_1", 3)]);
  assert.deepEqual(
    entries.map((entry) => entry.key),
    ["msg_1", "msg_2", "cmp_1", "msg_5"],
  );
});

test("buildTranscript keeps a pass covering the whole transcript at the end", () => {
  const entries = buildTranscript(renderedFixture(), [compaction("cmp_1", 9)]);
  assert.equal(entries.at(-1)?.key, "cmp_1");
});

test("buildTranscript orders several passes by what they cover", () => {
  const entries = buildTranscript(renderedFixture(), [
    compaction("cmp_late", 4),
    compaction("cmp_early", 1),
  ]);
  assert.deepEqual(
    entries.map((entry) => entry.key),
    ["msg_1", "cmp_early", "msg_2", "cmp_late", "msg_5"],
  );
});

test("buildTranscript is the plain transcript when nothing has compacted", () => {
  assert.equal(
    buildTranscript(renderedFixture(), []).every((entry) => entry.kind === "message"),
    true,
  );
});

test("groupPreviews keeps one turn's blocks in one row", () => {
  // Thinking and text arrive as separate content indices. Rendering a row each
  // puts two assistant avatars on screen that collapse into one when the
  // durable message lands.
  const rows = groupPreviews([
    preview({ contentIndex: 0, kind: "thinking", delta: "weighing it" }),
    preview({ contentIndex: 1, delta: "Because names carry meaning." }),
  ]);
  assert.equal(rows.length, 1);
  assert.equal(rows[0].invocationId, "inv_1");
  assert.deepEqual(
    rows[0].blocks.map((block) => block.kind),
    ["thinking", "text"],
  );
});

test("groupPreviews orders blocks by content index whatever order they arrive in", () => {
  const rows = groupPreviews([
    preview({ contentIndex: 2, delta: "second" }),
    preview({ contentIndex: 1, delta: "first" }),
  ]);
  assert.deepEqual(
    rows[0].blocks.map((block) => block.text),
    ["first", "second"],
  );
});

test("groupPreviews separates the messages a multi-step turn writes", () => {
  // One turn that calls a tool and then answers writes two messages. The
  // message id is what tells them apart.
  const rows = groupPreviews([
    preview({ messageId: "msg_1", delta: "let me look" }),
    preview({ messageId: "msg_2", delta: "here it is" }),
  ]);
  assert.equal(rows.length, 2);
});

test("groupPreviews drops tool-argument previews, which a transcript does not render", () => {
  const rows = groupPreviews([
    preview({ kind: "tool_arguments", delta: '{"city":"Bos' }),
    preview({ contentIndex: 1, delta: "Checking the weather." }),
  ]);
  assert.equal(rows.length, 1);
  assert.deepEqual(
    rows[0].blocks.map((block) => block.kind),
    ["text"],
  );
});

test("groupPreviews drops previews for a turn that has settled", () => {
  // The Reducer clears these on the terminal frame for the statuses its version
  // knows; one it has not learned must not leave a row streaming under a
  // finished turn.
  const rows = groupPreviews([preview({ delta: "half a sentence" })], new Set(["inv_1"]));
  assert.deepEqual(rows, []);
});

test("groupPreviews ignores whitespace-only deltas", () => {
  assert.deepEqual(groupPreviews([preview({ delta: "  \n" })]), []);
});
