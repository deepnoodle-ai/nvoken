import assert from "node:assert/strict";
import test from "node:test";

import {
  awaitingOutput,
  latestChanges,
  parkedCalls,
  resolveActivity,
  settledTurns,
  settlementNotice,
} from "../activity.js";
import type {
  ConversationCompaction,
  ConversationMessage,
  TurnChange,
} from "../index.js";
import type { Conversation } from "../generated/models/Conversation.js";
import type { StreamPreview } from "../stream.js";
import {
  blockField,
  buildTranscript,
  foldMessages,
  groupPreviews,
  isErrorResult,
  isSettled,
  mediaReference,
} from "../transcript.js";

let clock = 0;

function change(
  status: TurnChange["status"],
  overrides: Partial<TurnChange> = {},
): TurnChange {
  clock += 1_000;
  return {
    turnId: "turn_01kc514000e008000000000001",
    conversationId: "conv_01kc514000e008000000000001",
    contentExpiresAt: null,
    revision: 1,
    status,
    terminal: ["completed", "incomplete", "failed", "cancelled"].includes(status),
    throughMessageSequence: null,
    error: null,
    structuredOutput: null,
    occurredAt: new Date(clock),
    ...overrides,
  };
}

function message(
  sequence: number,
  role: ConversationMessage["role"],
  content: unknown[],
  overrides: Partial<ConversationMessage> = {},
): ConversationMessage {
  return {
    id: `msg_01kc514000e00800000000000${sequence}`,
    conversationId: "conv_01kc514000e008000000000001",
    contentExpiresAt: null,
    agentId: "agent_01kc514000e008000000000001",
    turnId: "turn_01kc514000e008000000000001",
    sequence,
    role,
    content: content as ConversationMessage["content"],
    createdAt: new Date(sequence * 1_000),
    ...overrides,
  };
}

function conversation(overrides: Partial<Conversation>): Conversation {
  return {
    id: "conv_01kc514000e008000000000001",
    tenantKey: "acme",
    owner: { kind: "tenant" },
    conversationKey: "support",
    forkedFrom: null,
    activeTurnId: null,
    activeTurnStatus: null,
    retention: null,
    compaction: null,
    metadata: null,
    expiresAt: null,
    createdAt: new Date(0),
    updatedAt: new Date(0),
    ...overrides,
  };
}

test("activity folds each Turn to its newest revision", () => {
  const changes = [
    change("queued", { revision: 1 }),
    change("running", { revision: 2 }),
    change("completed", { turnId: "turn_01kc514000e008000000000004", revision: 1 }),
  ];
  assert.deepEqual(
    latestChanges(changes).map((item) => [item.turnId, item.status]).sort(),
    [["turn_01kc514000e008000000000001", "running"], ["turn_01kc514000e008000000000004", "completed"]],
  );
  assert.deepEqual(resolveActivity(changes, null), { turnId: "turn_01kc514000e008000000000001", status: "running" });
});

test("stream settlement wins over a stale Conversation claim", () => {
  const changes = [change("completed", { revision: 2 })];
  assert.deepEqual(
    resolveActivity(changes, conversation({ activeTurnId: "turn_01kc514000e008000000000001", activeTurnStatus: "running" })),
    { turnId: null, status: null },
  );
});

test("an unobserved local admission or Conversation claim bridges the stream gap", () => {
  const settled = [change("completed", { turnId: "turn_01kc514000e008000000000004" })];
  assert.deepEqual(
    resolveActivity(settled, null, { turnId: "turn_01kc514000e008000000000005", status: "queued" }),
    { turnId: "turn_01kc514000e008000000000005", status: "queued" },
  );
  assert.deepEqual(
    resolveActivity(settled, conversation({
      activeTurnId: "turn_01kc514000e008000000000006",
      activeTurnStatus: "waiting",
    })),
    { turnId: "turn_01kc514000e008000000000006", status: "waiting" },
  );
});

test("settlement trusts the change terminal witness for future statuses", () => {
  const future = change("running", {
    turnId: "turn_01kc514000e008000000000007",
    status: "future_terminal" as TurnChange["status"],
    terminal: true,
  });
  assert.deepEqual([...settledTurns([future])], ["turn_01kc514000e008000000000007"]);
});

test("parked calls expose only actionable arguments from the newest change", () => {
  const calls = parkedCalls([
    change("running", { revision: 1, toolCalls: [] }),
    change("waiting", {
      revision: 2,
      toolCalls: [
        {
          id: "call_01kc514000e008000000000001",
          name: "lookup",
          mode: "host",
          status: "pending",
          arguments: { order: "42" },
          updatedAt: new Date(0),
        },
        {
          id: "call_01kc514000e008000000000002",
          name: "search",
          mode: "builtin",
          status: "running",
          updatedAt: new Date(0),
        },
      ],
    }),
  ], "turn_01kc514000e008000000000001");
  assert.deepEqual(calls.map((call) => call.id), ["call_01kc514000e008000000000001"]);
});

test("awaiting output distinguishes a final answer from an outstanding tool call", () => {
  assert.equal(awaitingOutput([message(1, "user", [{ type: "text", text: "hi" }])], "turn_01kc514000e008000000000001"), true);
  assert.equal(awaitingOutput([
    message(2, "assistant", [{ type: "text", text: "done" }]),
  ], "turn_01kc514000e008000000000001"), false);
  assert.equal(awaitingOutput([
    message(2, "assistant", [{ type: "tool_use", id: "call_01kc514000e008000000000001", name: "lookup" }]),
  ], "turn_01kc514000e008000000000001"), true);
});

test("settlement notices report the newest problem and clear after recovery", () => {
  const failed = change("failed", {
    error: { code: "provider_error", message: "upstream refused" },
  });
  assert.deepEqual(settlementNotice([failed]), {
    kind: "failed",
    message: "The last turn failed: upstream refused",
  });
  assert.equal(settlementNotice([failed, change("completed", { turnId: "turn_01kc514000e008000000000002" })]), null);
});

test("tool results fold onto their calls under either field spelling", () => {
  const rendered = foldMessages([
    message(1, "assistant", [{ type: "tool_use", id: "call_01kc514000e008000000000001", name: "lookup" }]),
    message(2, "user", [{
      type: "tool_result",
      tool_use_id: "call_01kc514000e008000000000001",
      content: "hit",
      is_error: true,
    }]),
  ]);
  const call = rendered[0].toolCalls.get("call_01kc514000e008000000000001");
  assert.ok(call);
  assert.equal(isSettled(call), true);
  assert.equal(isErrorResult(call), true);
  assert.equal(blockField(call.result!, "toolUseId", "tool_use_id"), "call_01kc514000e008000000000001");
  assert.equal(rendered.length, 1);
});

test("transcript compactions use throughSequence boundaries", () => {
  const rendered = foldMessages([
    message(1, "user", [{ type: "text", text: "one" }]),
    message(2, "assistant", [{ type: "text", text: "two" }]),
    message(5, "user", [{ type: "text", text: "three" }]),
  ]);
  const compaction: ConversationCompaction = {
    id: "comp_01kc514000e008000000000001",
    conversationId: "conv_01kc514000e008000000000001",
    status: "completed",
    throughSequence: 2,
    summary: "Earlier context",
    createdAt: new Date(0),
    updatedAt: new Date(0),
  };
  assert.deepEqual(
    buildTranscript(rendered, [compaction]).map((entry) => entry.key),
    ["msg_01kc514000e008000000000001", "msg_01kc514000e008000000000002", "comp_01kc514000e008000000000001", "msg_01kc514000e008000000000005"],
  );
});

test("preview rows are grouped by message and omit settled Turns", () => {
  const previews: StreamPreview[] = [
    {
      turnId: "turn_01kc514000e008000000000001",
      attempt: 1,
      messageId: "msg_01kc514000e008000000000002",
      contentIndex: 1,
      kind: "text",
      delta: "answer",
    },
    {
      turnId: "turn_01kc514000e008000000000001",
      attempt: 1,
      messageId: "msg_01kc514000e008000000000002",
      contentIndex: 0,
      kind: "thinking",
      delta: "plan",
    },
    {
      turnId: "turn_01kc514000e008000000000008",
      attempt: 1,
      messageId: "msg_01kc514000e008000000000006",
      contentIndex: 0,
      kind: "text",
      delta: "ghost",
    },
  ];
  const rows = groupPreviews(previews, new Set(["turn_01kc514000e008000000000008"]));
  assert.deepEqual(rows.map((row) => ({
    key: row.key,
    turnId: row.turnId,
    blocks: row.blocks.map((block) => [block.index, block.kind, block.text]),
  })), [{
    key: "msg_01kc514000e008000000000002",
    turnId: "turn_01kc514000e008000000000001",
    blocks: [[0, "thinking", "plan"], [1, "text", "answer"]],
  }]);
});

test("media references retain metadata but never bytes", () => {
  assert.deepEqual(mediaReference({
    type: "document",
    media_type: "application/pdf",
    title: "invoice.pdf",
    bytes: 2_048,
    digest: "sha256:abc",
  }), {
    kind: "document",
    mediaType: "application/pdf",
    title: "invoice.pdf",
    bytes: 2_048,
    digest: "sha256:abc",
  });
});
