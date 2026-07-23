import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";

import {
  Client,
  NvokenError,
  defineHostTool,
  defineJsonSchema,
  formatInvocationFailure,
  formatNvokenError,
  toolInput,
  type InvocationHandle,
  type InvokeRequest,
} from "@deepnoodle/nvoken";

type Invocation = Awaited<ReturnType<Client["getInvocation"]>>;

interface LookupOrderInput {
  order_id: string;
}

interface SupportClassification {
  category: "billing";
  priority: "high";
  needs_human: boolean;
}

const baseUrl = process.env.NVOKEN_BASE_URL ?? "http://localhost:8080";
const apiKey = required("NVOKEN_API_KEY");
const provider = modelProvider();
const model = required("NVOKEN_MODEL");

const client = new Client({ baseUrl, apiKey });
const runId = `typescript-showcase-${randomUUID()}`;
const tenantA = `${runId}-tenant-a`;
const tenantB = `${runId}-tenant-b`;
const primaryAgentKey = `${runId}-support`;
const alternateAgentKey = `${runId}-alternate`;
const sharedSessionKey = `${runId}-shared-session`;
const lookupOrder = defineHostTool<LookupOrderInput>({
  mode: "host",
  name: "lookup_order",
  description: "Look up an order by its order ID.",
  inputSchema: defineJsonSchema<LookupOrderInput>({
    type: "object",
    properties: {
      order_id: { type: "string" },
    },
    required: ["order_id"],
    additionalProperties: false,
  }),
});
const supportClassification = defineJsonSchema<SupportClassification>({
  type: "object",
  properties: {
    category: { type: "string", enum: ["billing"] },
    priority: { type: "string", enum: ["high"] },
    needs_human: { type: "boolean" },
  },
  required: ["category", "priority", "needs_human"],
  additionalProperties: false,
});

try {
  await main();
} catch (error) {
  console.error(formatNvokenError(error));
  process.exitCode = 1;
}

async function main(): Promise<void> {
  console.log(`nvoken=${baseUrl}`);
  console.log(`run_id=${runId}`);

  const pricing = await client.pricingCapability({ provider, id: model });
  console.log(`PASS pricing preflight: ${pricing.status} (${pricing.registryVersion})`);

  const firstRequest: InvokeRequest = {
    agentKey: primaryAgentKey,
    tenantKey: tenantA,
    sessionKey: sharedSessionKey,
    idempotencyKey: `${runId}:turn-1`,
    input: "Remember that my confirmation code is ORCHID-724. Reply only with remembered.",
    spec: chatSpec(),
  };
  const first = await completed(firstRequest);
  assert.match(first.text, /remembered/i);
  assert.equal(first.handle.deduplicated, false);

  const firstTranscript = await client.drainTranscript(
    first.handle.sessionId!,
    { pageSize: 1 },
  );
  assert.deepEqual(
    new Set(firstTranscript.messages.map((message) => message.invocationId)),
    new Set([first.handle.invocationId]),
  );
  console.log("PASS first turn and initial transcript cursor");

  const replay = await client.invoke(firstRequest);
  assert.equal(replay.invocationId, first.handle.invocationId);
  assert.equal(replay.sessionId, first.handle.sessionId);
  assert.equal(replay.agentId, first.handle.agentId);
  assert.equal(replay.deduplicated, true);
  console.log("PASS exact idempotent admission replay and acknowledgement metadata");

  const idempotencyConflict = await expectNvokenError(
    () => client.invoke({ ...firstRequest, input: "This is a different request." }),
    "idempotency_conflict",
  );
  console.log(`PASS changed idempotency replay: ${idempotencyConflict.code}`);

  const second = await completed({
    agentKey: primaryAgentKey,
    sessionId: first.handle.sessionId!,
    idempotencyKey: `${runId}:turn-2`,
    input: "What is my confirmation code? Reply only with the code.",
    spec: chatSpec(),
  });
  assert.match(second.text, /ORCHID-724/i);
  assert.equal(second.handle.sessionId, first.handle.sessionId);
  console.log("PASS two-turn Session memory using sessionId without tenantKey");

  const secondDelta = await client.drainTranscript(first.handle.sessionId!, {
    cursor: firstTranscript.resumeCursor,
    pageSize: 1,
  });
  assert.deepEqual(
    new Set(secondDelta.messages.map((message) => message.invocationId)),
    new Set([second.handle.invocationId]),
  );
  console.log("PASS incremental transcript cursor contains only the second turn");

  const session = await client.getSession(first.handle.sessionId!);
  assert.equal(session.tenantKey, tenantA);
  assert.equal(session.sessionKey, sharedSessionKey);
  assert.equal(session.activeInvocationId, null);

  const tenantSessions = await client.listSessions({
    tenantKey: tenantA,
    agentId: session.agentId,
    limit: 100,
  });
  assert.ok(tenantSessions.items.some((item) => item.id === session.id));

  const exactSessionLookup = await client.getSessionByKey(sharedSessionKey, {
    agentId: session.agentId,
    tenantKey: tenantA,
  });
  assert.equal(exactSessionLookup.id, session.id);

  const messages = [];
  for await (const message of client.messagePages(session.id, { limit: 1 })) {
    messages.push(message);
  }
  assert.deepEqual(messages.map((message) => message.role), [
    "user",
    "assistant",
    "user",
    "assistant",
  ]);
  assert.deepEqual(messages.map((message) => message.sequence), [1, 2, 3, 4]);

  const invocationIds: string[] = [];
  for await (const invocation of client.invocationPages({ sessionId: session.id, limit: 1 })) {
    invocationIds.push(invocation.id);
  }
  assert.deepEqual(new Set(invocationIds), new Set([
    first.handle.invocationId,
    second.handle.invocationId,
  ]));
  console.log("PASS Session get/filter/exact lookup, message pages, and Invocation pages");

  const tenantBResult = await completed({
    agentKey: primaryAgentKey,
    tenantKey: tenantB,
    sessionKey: sharedSessionKey,
    idempotencyKey: firstRequest.idempotencyKey,
    input: "Reply only with beta.",
    spec: chatSpec(),
  });
  const tenantBSession = await client.getSession(tenantBResult.handle.sessionId!);
  assert.equal(tenantBSession.agentId, session.agentId);
  assert.notEqual(tenantBSession.id, session.id);
  assert.equal(tenantBSession.tenantKey, tenantB);

  const alternateResult = await completed({
    agentKey: alternateAgentKey,
    tenantKey: tenantA,
    sessionKey: sharedSessionKey,
    idempotencyKey: firstRequest.idempotencyKey,
    input: "Reply only with alternate.",
    spec: chatSpec(),
  });
  const alternateSession = await client.getSession(alternateResult.handle.sessionId!);
  assert.notEqual(alternateSession.agentId, session.agentId);
  assert.notEqual(alternateSession.id, session.id);

  const defaultSessions = await client.listSessions({ defaultTenant: true, limit: 100 });
  assert.ok(!defaultSessions.items.some((item) => (
    item.id === session.id || item.id === tenantBSession.id || item.id === alternateSession.id
  )));
  console.log("PASS tenantKey and agentKey scope Sessions and idempotency; Agent identity spans tenants");

  const agentMismatch = await expectNvokenError(
    () => client.invoke({
      agentKey: alternateAgentKey,
      sessionId: session.id,
      idempotencyKey: `${runId}:agent-mismatch`,
      input: "This should not be admitted.",
      spec: chatSpec(),
    }),
    "not_found",
  );
  const tenantMismatch = await expectNvokenError(
    () => client.invoke({
      agentKey: primaryAgentKey,
      tenantKey: tenantB,
      sessionId: session.id,
      idempotencyKey: `${runId}:tenant-mismatch`,
      input: "This should not be admitted.",
      spec: chatSpec(),
    }),
    "not_found",
  );
  console.log(`PASS Session scope mismatch is nondisclosing: ${agentMismatch.code}, ${tenantMismatch.code}`);

  const toolHandle = await client.invoke({
    agentKey: primaryAgentKey,
    tenantKey: tenantA,
    sessionKey: `${runId}-tool-session`,
    idempotencyKey: `${runId}:tool`,
    input: "Check order order-42 and tell me its current state. You must use lookup_order first.",
    spec: {
      instructions: "Always call lookup_order exactly once before answering an order-status question. Never invent tool results.",
      model: { provider, id: model },
      limits: {
        maxOutputTokens: 200,
        maxIterations: 3,
      },
      tools: [lookupOrder],
    },
  });

  const waiting = await toolHandle.wait({ until: "actionable" });
  assert.equal(waiting.status, "waiting", failure(toolHandle, waiting));
  assert.equal(waiting.pendingToolCalls?.length, 1);
  const toolCall = waiting.pendingToolCalls?.[0];
  assert.ok(toolCall);
  assert.equal(toolInput(lookupOrder, toolCall).order_id, "order-42");

  const waitingSession = await client.getSession(toolHandle.sessionId!);
  assert.equal(waitingSession.activeInvocationId, toolHandle.invocationId);
  assert.equal(waitingSession.activeInvocationStatus, "waiting");
  assert.equal(waitingSession.pendingToolCalls?.[0]?.id, toolCall.id);

  const busySession = await expectNvokenError(
    () => client.invoke({
      agentKey: primaryAgentKey,
      sessionId: toolHandle.sessionId!,
      idempotencyKey: `${runId}:busy-session`,
      input: "This turn should not be admitted while the tool call is waiting.",
      spec: chatSpec(),
    }),
    "session_invocation_active",
  );

  const toolResult = {
    toolCallId: toolCall.id,
    content: {
      order_id: "order-42",
      state: "ready",
      estimated_delivery: "tomorrow",
    },
  };
  const accepted = await toolHandle.submitToolResults([toolResult]);
  assert.equal(accepted.results[0]?.deduplicated, false);
  const replayedResult = await toolHandle.submitToolResults([toolResult]);
  assert.equal(replayedResult.results[0]?.deduplicated, true);
  const changedToolResult = await expectNvokenError(
    () => toolHandle.submitToolResults([{
      ...toolResult,
      content: { order_id: "order-42", state: "cancelled" },
    }]),
    "tool_result_conflict",
  );

  const toolInvocation = await toolHandle.wait();
  assert.equal(toolInvocation.status, "completed", failure(toolHandle, toolInvocation));
  const toolText = await toolHandle.text();
  assert.match(toolText, /ready|tomorrow/i);
  const toolMessages = await toolHandle.listMessages();
  assert.deepEqual(toolMessages.map((message) => message.role), [
    "user",
    "assistant",
    "tool",
    "assistant",
  ]);
  console.log(`PASS host ToolCall wait/Session visibility/busy guard/result replay: ${busySession.code}, ${changedToolResult.code}`);

  const toolFollowup = await completed({
    agentKey: primaryAgentKey,
    sessionId: toolHandle.sessionId!,
    idempotencyKey: `${runId}:tool-followup`,
    input: "Based on our previous order lookup, when is the estimated delivery? Reply only with the answer.",
    spec: chatSpec(),
  });
  assert.match(toolFollowup.text, /tomorrow/i);
  console.log("PASS a later Invocation receives the successful host-tool transcript as Session context");

  const structuredHandle = await client.invoke({
    agentKey: primaryAgentKey,
    tenantKey: tenantA,
    sessionKey: `${runId}-structured-session`,
    idempotencyKey: `${runId}:structured`,
    input: "Classify this request: I was billed twice and need a human to review it today.",
    spec: {
      instructions: "Submit the requested structured classification, then give one short confirmation sentence.",
      model: { provider, id: model },
      limits: {
        maxOutputTokens: 200,
        maxIterations: 3,
      },
      outputSchema: supportClassification,
    },
  });

  const streamEvents = new Set<string>();
  let streamedMessages = 0;
  const streamController = new AbortController();
  const streamTimer = setTimeout(() => streamController.abort(), 60_000);
  const stream = (async () => {
    for await (const event of structuredHandle.stream(streamController.signal)) {
      streamEvents.add(event.type);
      if (event.type === "output_text.delta") {
        process.stdout.write(event.text);
      }
      if (event.type === "invocation.update") {
        streamedMessages += event.newMessages.length;
      }
      if (event.type === "invocation.result") {
        streamedMessages = Math.max(streamedMessages, event.result.messages.length);
      }
    }
  })();
  const [structuredInvocation] = await Promise.all([
    structuredHandle.wait(),
    stream,
  ]).finally(() => clearTimeout(streamTimer));

  assert.equal(
    structuredInvocation.status,
    "completed",
    failure(structuredHandle, structuredInvocation),
  );
  const classification = structuredInvocation.structuredOutput;
  assert.deepEqual(classification, {
    category: "billing",
    needs_human: true,
    priority: "high",
  });
  assert.equal(classification?.priority, "high");
  assert.equal(classification?.needs_human, true);
  assert.equal(structuredInvocation.structuredOutputProvenance?.source, "tool_call");
  assert.ok(structuredInvocation.structuredOutputProvenance?.toolCallId.startsWith("tcal_"));
  assert.ok(structuredInvocation.structuredOutputProvenance?.schemaSha256);
  assert.ok((await structuredHandle.text()).length > 0);
  assert.ok(streamEvents.has("invocation.result"));
  assert.ok(streamEvents.has("stream.end"));
  assert.ok(streamedMessages >= 3);
  console.log("PASS structured output, provenance, composed text, and resumable Invocation SSE");

  console.log("\nAll TypeScript invoke showcase checks passed.");
  console.log(JSON.stringify({
    runId,
    provider,
    model,
    agentId: session.agentId,
    tenantASessionId: session.id,
    tenantBSessionId: tenantBSession.id,
    alternateAgentId: alternateSession.agentId,
    toolInvocationId: toolHandle.invocationId,
    structuredInvocationId: structuredHandle.invocationId,
  }, null, 2));
}

function chatSpec(): InvokeRequest["spec"] {
  return {
    instructions: "You are a concise support assistant. Remember relevant facts from earlier turns in this Session.",
    model: { provider, id: model },
    limits: {
      maxOutputTokens: 100,
      maxIterations: 1,
    },
  };
}

async function completed(request: InvokeRequest): Promise<{
  handle: InvocationHandle;
  invocation: Invocation;
  text: string;
}> {
  const handle = await client.invoke(request);
  const invocation = await handle.wait();
  assert.equal(invocation.status, "completed", failure(handle, invocation));
  return {
    handle,
    invocation,
    text: await handle.text(),
  };
}

async function expectNvokenError(
  action: () => Promise<unknown>,
  expectedCode: string,
): Promise<NvokenError> {
  try {
    await action();
  } catch (error) {
    assert.ok(error instanceof NvokenError);
    assert.equal(error.code, expectedCode);
    return error;
  }
  throw new Error(`Expected nvoken error ${expectedCode}`);
}

function failure<TOutput extends object>(
  handle: InvocationHandle<TOutput>,
  invocation: Pick<Invocation, "status" | "error">,
): string {
  return formatInvocationFailure(handle.invocationId, invocation, provider, {
    includeLogGuidance: true,
  });
}

function required(name: string): string {
  const value = process.env[name];
  if (!value) throw new Error(`${name} is required`);
  return value;
}

function modelProvider(): "anthropic" | "openai" {
  const value = process.env.NVOKEN_PROVIDER;
  if (value !== "anthropic" && value !== "openai") {
    throw new Error("NVOKEN_PROVIDER must be anthropic or openai");
  }
  return value;
}
