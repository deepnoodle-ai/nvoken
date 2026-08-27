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
    turnId: "476dd7be-97a1-78f3-8096-d7032468a80a",
    conversationId: "18325d9f-b9bc-797d-9259-96ece372defd",
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

// Keyed by sequence rather than built from one, because an ID assembled by
// interpolation reads as opaque to every check that scans for a retired shape.
const messageIDs: Record<number, string> = {
  1: "102002f7-649e-7a77-85c2-7f1695adb24e",
  2: "5c22fa76-11b7-7d95-b9f7-db8aadb67b76",
  5: "ad17717f-f22f-7ee1-95f4-bff7ff263de7",
  6: "0499aae6-5f21-72e5-bc03-57b6584e7b50",
};

function message(
  sequence: number,
  role: ConversationMessage["role"],
  content: unknown[],
  overrides: Partial<ConversationMessage> = {},
): ConversationMessage {
  return {
    id: messageIDs[sequence],
    conversationId: "18325d9f-b9bc-797d-9259-96ece372defd",
    contentExpiresAt: null,
    agentId: "47fc63e5-ae78-727c-ab52-a2872fe8728f",
    turnId: "476dd7be-97a1-78f3-8096-d7032468a80a",
    sequence,
    role,
    content: content as ConversationMessage["content"],
    createdAt: new Date(sequence * 1_000),
    ...overrides,
  };
}

function conversation(overrides: Partial<Conversation>): Conversation {
  return {
    id: "18325d9f-b9bc-797d-9259-96ece372defd",
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
    change("completed", { turnId: "b7226c24-7259-78c2-b57e-19dc4a24a4c9", revision: 1 }),
  ];
  assert.deepEqual(
    latestChanges(changes).map((item) => [item.turnId, item.status]).sort(),
    [["476dd7be-97a1-78f3-8096-d7032468a80a", "running"], ["b7226c24-7259-78c2-b57e-19dc4a24a4c9", "completed"]],
  );
  assert.deepEqual(resolveActivity(changes, null), { turnId: "476dd7be-97a1-78f3-8096-d7032468a80a", status: "running" });
});

test("stream settlement wins over a stale Conversation claim", () => {
  const changes = [change("completed", { revision: 2 })];
  assert.deepEqual(
    resolveActivity(changes, conversation({ activeTurnId: "476dd7be-97a1-78f3-8096-d7032468a80a", activeTurnStatus: "running" })),
    { turnId: null, status: null },
  );
});

test("an unobserved local admission or Conversation claim bridges the stream gap", () => {
  const settled = [change("completed", { turnId: "b7226c24-7259-78c2-b57e-19dc4a24a4c9" })];
  assert.deepEqual(
    resolveActivity(settled, null, { turnId: "5452a606-2da3-78d1-8834-022f4cf5db28", status: "queued" }),
    { turnId: "5452a606-2da3-78d1-8834-022f4cf5db28", status: "queued" },
  );
  assert.deepEqual(
    resolveActivity(settled, conversation({
      activeTurnId: "531f31e5-a5ba-7134-a6f6-a9b1ce7d3171",
      activeTurnStatus: "waiting",
    })),
    { turnId: "531f31e5-a5ba-7134-a6f6-a9b1ce7d3171", status: "waiting" },
  );
});

test("settlement trusts the change terminal witness for future statuses", () => {
  const future = change("running", {
    turnId: "d9c32888-a52d-72af-af1a-9fddec54030f",
    status: "future_terminal" as TurnChange["status"],
    terminal: true,
  });
  assert.deepEqual([...settledTurns([future])], ["d9c32888-a52d-72af-af1a-9fddec54030f"]);
});

test("parked calls expose only actionable arguments from the newest change", () => {
  const calls = parkedCalls([
    change("running", { revision: 1, toolCalls: [] }),
    change("waiting", {
      revision: 2,
      toolCalls: [
        {
          id: "9f8fd6b3-9060-783d-b759-45c8ec70e8cb",
          name: "lookup",
          mode: "host",
          status: "pending",
          arguments: { order: "42" },
          updatedAt: new Date(0),
        },
        {
          id: "8b6c8687-a698-7aeb-8440-29729d2fc4b7",
          name: "search",
          mode: "builtin",
          status: "running",
          updatedAt: new Date(0),
        },
      ],
    }),
  ], "476dd7be-97a1-78f3-8096-d7032468a80a");
  assert.deepEqual(calls.map((call) => call.id), ["9f8fd6b3-9060-783d-b759-45c8ec70e8cb"]);
});

test("awaiting output distinguishes a final answer from an outstanding tool call", () => {
  assert.equal(awaitingOutput([message(1, "user", [{ type: "text", text: "hi" }])], "476dd7be-97a1-78f3-8096-d7032468a80a"), true);
  assert.equal(awaitingOutput([
    message(2, "assistant", [{ type: "text", text: "done" }]),
  ], "476dd7be-97a1-78f3-8096-d7032468a80a"), false);
  assert.equal(awaitingOutput([
    message(2, "assistant", [{ type: "tool_use", id: "9f8fd6b3-9060-783d-b759-45c8ec70e8cb", name: "lookup" }]),
  ], "476dd7be-97a1-78f3-8096-d7032468a80a"), true);
});

test("settlement notices report the newest problem and clear after recovery", () => {
  const failed = change("failed", {
    error: { code: "provider_error", message: "upstream refused" },
  });
  assert.deepEqual(settlementNotice([failed]), {
    kind: "failed",
    message: "The last turn failed: upstream refused",
  });
  assert.equal(settlementNotice([failed, change("completed", { turnId: "9dcd976a-f0e4-700c-9397-83cf91a3c766" })]), null);
});

test("tool results fold onto their calls under either field spelling", () => {
  const rendered = foldMessages([
    message(1, "assistant", [{ type: "tool_use", id: "9f8fd6b3-9060-783d-b759-45c8ec70e8cb", name: "lookup" }]),
    message(2, "user", [{
      type: "tool_result",
      tool_use_id: "9f8fd6b3-9060-783d-b759-45c8ec70e8cb",
      content: "hit",
      is_error: true,
    }]),
  ]);
  const call = rendered[0].toolCalls.get("9f8fd6b3-9060-783d-b759-45c8ec70e8cb");
  assert.ok(call);
  assert.equal(isSettled(call), true);
  assert.equal(isErrorResult(call), true);
  assert.equal(blockField(call.result!, "toolUseId", "tool_use_id"), "9f8fd6b3-9060-783d-b759-45c8ec70e8cb");
  assert.equal(rendered.length, 1);
});

test("transcript compactions use throughSequence boundaries", () => {
  const rendered = foldMessages([
    message(1, "user", [{ type: "text", text: "one" }]),
    message(2, "assistant", [{ type: "text", text: "two" }]),
    message(5, "user", [{ type: "text", text: "three" }]),
  ]);
  const compaction: ConversationCompaction = {
    id: "5e76d202-9135-7a83-b872-044685d8f7f4",
    conversationId: "18325d9f-b9bc-797d-9259-96ece372defd",
    status: "completed",
    throughSequence: 2,
    summary: "Earlier context",
    createdAt: new Date(0),
    updatedAt: new Date(0),
  };
  assert.deepEqual(
    buildTranscript(rendered, [compaction]).map((entry) => entry.key),
    ["102002f7-649e-7a77-85c2-7f1695adb24e", "5c22fa76-11b7-7d95-b9f7-db8aadb67b76", "5e76d202-9135-7a83-b872-044685d8f7f4", "ad17717f-f22f-7ee1-95f4-bff7ff263de7"],
  );
});

test("preview rows are grouped by message and omit settled Turns", () => {
  const previews: StreamPreview[] = [
    {
      turnId: "476dd7be-97a1-78f3-8096-d7032468a80a",
      attempt: 1,
      messageId: "5c22fa76-11b7-7d95-b9f7-db8aadb67b76",
      contentIndex: 1,
      kind: "text",
      delta: "answer",
    },
    {
      turnId: "476dd7be-97a1-78f3-8096-d7032468a80a",
      attempt: 1,
      messageId: "5c22fa76-11b7-7d95-b9f7-db8aadb67b76",
      contentIndex: 0,
      kind: "thinking",
      delta: "plan",
    },
    {
      turnId: "683649a1-6faa-7fa3-b774-89ecf248cae7",
      attempt: 1,
      messageId: "0499aae6-5f21-72e5-bc03-57b6584e7b50",
      contentIndex: 0,
      kind: "text",
      delta: "ghost",
    },
  ];
  const rows = groupPreviews(previews, new Set(["683649a1-6faa-7fa3-b774-89ecf248cae7"]));
  assert.deepEqual(rows.map((row) => ({
    key: row.key,
    turnId: row.turnId,
    blocks: row.blocks.map((block) => [block.index, block.kind, block.text]),
  })), [{
    key: "5c22fa76-11b7-7d95-b9f7-db8aadb67b76",
    turnId: "476dd7be-97a1-78f3-8096-d7032468a80a",
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
