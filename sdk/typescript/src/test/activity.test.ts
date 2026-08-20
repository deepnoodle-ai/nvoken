import assert from "node:assert/strict";
import test from "node:test";

import {
  awaitingOutput,
  parkedCalls,
  resolveActivity,
  settledInvocations,
  settlementNotice,
} from "../activity.js";
import type { InvocationChange, Session, SessionMessage } from "../index.js";

let clock = 0;

function change(
  overrides: Partial<Omit<InvocationChange, "status">> & { status: string },
): InvocationChange {
  clock += 1000;
  return {
    invocationId: "inv_1",
    revision: 1,
    throughMessageSequence: null,
    error: null,
    usage: null,
    provenance: null,
    structuredOutput: null,
    structuredOutputProvenance: null,
    occurredAt: new Date(clock),
    ...overrides,
  } as InvocationChange;
}

function message(
  overrides: Partial<SessionMessage> & { role: SessionMessage["role"] },
): SessionMessage {
  return {
    id: "msg_1",
    sessionId: "sess_1",
    agentId: "agent_1",
    invocationId: "inv_1",
    sequence: 1,
    content: [],
    createdAt: new Date(0),
    ...overrides,
  } as SessionMessage;
}

function session(overrides: Partial<Session>): Session {
  return {
    activeInvocationId: null,
    activeInvocationStatus: null,
    ...overrides,
  } as Session;
}

test("resolveActivity reports the running turn from the stream", () => {
  assert.deepEqual(resolveActivity([change({ status: "running" })], null), {
    invocationId: "inv_1",
    status: "running",
  });
});

test("resolveActivity takes the highest revision for an Invocation", () => {
  const activity = resolveActivity(
    [change({ status: "queued", revision: 1 }), change({ status: "running", revision: 2 })],
    null,
  );
  assert.equal(activity.status, "running");
});

test("resolveActivity prefers the stream's status over a stale Session read", () => {
  // The Session was read when the turn was admitted; the stream has since
  // watched it start. The badge should say what is happening now.
  const activity = resolveActivity(
    [change({ status: "running", revision: 2 })],
    session({ activeInvocationId: "inv_1", activeInvocationStatus: "queued" }),
  );
  assert.equal(activity.status, "running");
});

test("resolveActivity goes idle as soon as the stream settles, without waiting for a re-read", () => {
  // Holding the Session's word here is what leaves a composer's stop button up
  // for a round trip after the answer has finished.
  const activity = resolveActivity(
    [change({ status: "completed", revision: 3 })],
    session({ activeInvocationId: "inv_1", activeInvocationStatus: "running" }),
  );
  assert.deepEqual(activity, { invocationId: null, status: null });
});

test("resolveActivity treats incomplete as settled", () => {
  assert.equal(resolveActivity([change({ status: "incomplete" })], null).invocationId, null);
});

test("resolveActivity holds a just-admitted turn live until the stream reports on it", () => {
  // The create call has returned but the reopened stream has not delivered a
  // frame yet. Without this a composer blinks back to idle.
  const activity = resolveActivity(
    [change({ status: "completed", invocationId: "inv_old" })],
    null,
    { invocationId: "inv_new", status: "queued" },
  );
  assert.deepEqual(activity, { invocationId: "inv_new", status: "queued" });
});

test("resolveActivity drops the admitted claim once the stream has an opinion", () => {
  const activity = resolveActivity(
    [change({ status: "completed", invocationId: "inv_new" })],
    null,
    { invocationId: "inv_new", status: "queued" },
  );
  assert.deepEqual(activity, { invocationId: null, status: null });
});

test("resolveActivity trusts the Session for a turn the stream has not mentioned", () => {
  // Another client owns this Session too and can start a turn between frames.
  const activity = resolveActivity(
    [change({ status: "completed", invocationId: "inv_old" })],
    session({ activeInvocationId: "inv_new", activeInvocationStatus: "queued" }),
  );
  assert.deepEqual(activity, { invocationId: "inv_new", status: "queued" });
});

test("settledInvocations collects every terminal Invocation, incomplete included", () => {
  const settled = settledInvocations([
    change({ status: "completed", invocationId: "inv_a" }),
    change({ status: "incomplete", invocationId: "inv_b" }),
    change({ status: "running", invocationId: "inv_c" }),
  ]);
  assert.deepEqual([...settled].sort(), ["inv_a", "inv_b"]);
});

test("settledInvocations believes the change's own terminal field over the status it carries", () => {
  // `terminal` is what the runtime says, and it is the reason a consumer should
  // not keep its own set: a status this build has never heard of still settles
  // correctly.
  const settled = settledInvocations([
    change({ status: "some_future_status", invocationId: "inv_a", terminal: true }),
    change({ status: "running", invocationId: "inv_b", terminal: false }),
  ]);
  assert.deepEqual([...settled], ["inv_a"]);
});

test("awaitingOutput is false when no turn is live", () => {
  assert.equal(awaitingOutput([], null), false);
});

test("awaitingOutput is true before the turn has produced anything", () => {
  assert.equal(awaitingOutput([message({ role: "user" })], "inv_1"), true);
});

test("awaitingOutput is false once the model has answered without calling a tool", () => {
  // The Invocation has not settled yet — the terminal frame follows the message
  // — but the model has said its last word, so nothing should announce work
  // still to come.
  const messages = [
    message({ role: "user", sequence: 1 }),
    message({
      role: "assistant",
      sequence: 2,
      content: [{ type: "text", text: "Done." }] as SessionMessage["content"],
    }),
  ];
  assert.equal(awaitingOutput(messages, "inv_1"), false);
});

test("awaitingOutput is true while a tool call is outstanding", () => {
  const messages = [
    message({
      role: "assistant",
      sequence: 2,
      content: [{ type: "tool_use", id: "call_1", name: "search" }] as SessionMessage["content"],
    }),
  ];
  assert.equal(awaitingOutput(messages, "inv_1"), true);
});

test("awaitingOutput ignores the previous turn's messages", () => {
  const messages = [
    message({
      role: "assistant",
      sequence: 1,
      invocationId: "inv_old",
      content: [{ type: "text", text: "Earlier." }] as SessionMessage["content"],
    }),
    message({ role: "user", sequence: 2 }),
  ];
  assert.equal(awaitingOutput(messages, "inv_1"), true);
});

test("settlementNotice is silent when the newest settlement was clean", () => {
  assert.equal(settlementNotice([change({ status: "completed" })]), null);
});

test("settlementNotice reports a failure with its message", () => {
  const notice = settlementNotice([
    change({
      status: "failed",
      error: { code: "provider_error", message: "upstream refused" } as InvocationChange["error"],
    }),
  ]);
  assert.deepEqual(notice, {
    kind: "failed",
    message: "The last turn failed: upstream refused",
  });
});

test("settlementNotice names a budget stop as its own outcome", () => {
  assert.equal(settlementNotice([change({ status: "incomplete" })])?.kind, "incomplete");
});

test("settlementNotice clears once a later turn completes", () => {
  const failed = change({ status: "failed", invocationId: "inv_a" });
  const completed = change({ status: "completed", invocationId: "inv_b" });
  assert.equal(settlementNotice([failed, completed]), null);
});

// parkedCalls replaced a 15-second Session poll. The lifecycle change carries
// the turn's tool calls, so what a parked turn is waiting on is already on the
// stream and reading it again over HTTP was the redundancy.
const call = (id: string, extra: Record<string, unknown> = {}) =>
  ({
    id,
    name: "lookup",
    status: "running",
    occurredAt: new Date(0),
    ...extra,
  }) as never;

test("parkedCalls returns only the calls carrying arguments to answer with", () => {
  // One collection, filtered on arguments. A builtin call nvoken runs itself
  // and a settled one both carry none, and neither is anyone else's to run.
  const calls = parkedCalls(
    [
      change({
        status: "waiting",
        toolCalls: [call("tc_1", { arguments: { city: "Boston" } }), call("tc_2")],
      } as never),
    ],
    "inv_1",
  );
  assert.deepEqual(
    calls.map((entry) => entry.id),
    ["tc_1"],
  );
});

test("parkedCalls reads the highest revision, not the first change seen", () => {
  const calls = parkedCalls(
    [
      change({ status: "running", revision: 1, toolCalls: [] } as never),
      change({
        status: "waiting",
        revision: 2,
        toolCalls: [call("tc_9", { arguments: {} })],
      } as never),
    ],
    "inv_1",
  );
  assert.deepEqual(
    calls.map((entry) => entry.id),
    ["tc_9"],
  );
});

test("parkedCalls is empty for an idle Session and for a turn with no change yet", () => {
  assert.deepEqual(parkedCalls([], null), []);
  assert.deepEqual(parkedCalls([change({ status: "waiting" })], "inv_other"), []);
});
