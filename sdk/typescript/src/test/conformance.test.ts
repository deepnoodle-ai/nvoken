import assert from "node:assert/strict";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

import {
  Client,
  SessionBusyError,
  NvokenError,
  Reducer,
  deduplicateCallbackResult,
  defineHostTool,
  defineJsonSchema,
  formatInvocationFailure,
  formatNvokenError,
  toolInput,
  verifyCallback,
  type HostTool,
  type Tool,
  type StandardJSONSchemaV1,
} from "../index.js";

const agentId = "agnt_019b0a12-8d51-7f34-aed2-0e07c1bdb320";
const invocationId = "invk_019b0a12-8d51-7f34-aed2-0e07c1bdb322";
const sessionId = "sesn_019b0a12-8d51-7f34-aed2-0e07c1bdb321";
const toolCallId = "tcal_019b0a12-8d51-7f34-aed2-0e07c1bdb325";
const waitId = "invk_019b0a12-8d51-7f34-aed2-0e07c1bdb328";

interface Answer {
  answer: string;
}

test("public diagnostics stay concise and provider-aware", () => {
  assert.equal(
    formatNvokenError(new NvokenError(
      "authentication",
      "invalid runtime API key",
      401,
      "unauthenticated",
      "req_test",
    )),
    "nvoken error [authentication] code=unauthenticated request_id=req_test: invalid runtime API key",
  );
  assert.equal(formatNvokenError(new Error("NVOKEN_MODEL is required")), "NVOKEN_MODEL is required");

  const invocation = {
    status: "failed" as const,
    error: {
      code: "provider_error" as const,
      message: "The provider rejected the requested model",
      details: { classification: "upstream_rejected", retryable: false },
    },
  };
  const publicDiagnostic = formatInvocationFailure(invocationId, invocation, "openai");
  assert.match(publicDiagnostic, /^Invocation invk_.* failed: provider_error:/);
  assert.match(publicDiagnostic, /"classification":"upstream_rejected"/);
  assert.match(publicDiagnostic, /https:\/\/developers\.openai\.com\/api\/docs\/models\.$/);
  assert.doesNotMatch(publicDiagnostic, /structured daemon logs/);

  const anthropicDiagnostic = formatInvocationFailure(invocationId, invocation, "anthropic");
  assert.match(
    anthropicDiagnostic,
    /https:\/\/platform\.claude\.com\/docs\/en\/about-claude\/models\/overview\.$/,
  );

  const localDiagnostic = formatInvocationFailure(
    invocationId,
    invocation,
    "openai",
    { includeLogGuidance: true },
  );
  assert.match(localDiagnostic, new RegExp(`structured daemon logs for invocation_id=${invocationId}`));
  assert.match(localDiagnostic, /private upstream response bodies are intentionally omitted\.$/);
});

test("shared fault server semantics", async (context) => {
  const baseUrl = process.env.NVOKEN_CONFORMANCE_URL;
  if (!baseUrl) {
    context.skip("NVOKEN_CONFORMANCE_URL is not set");
    return;
  }
  await fetch(`${baseUrl}/__test/reset`, { method: "POST" });
  const client = new Client({
    baseUrl,
    apiKey: "test-key",
    retry: { maxAttempts: 3, minDelayMs: 1, maxDelayMs: 5 },
  });
  const pricing = await client.pricingCapability({ provider: "openai", id: "gpt-test" });
  assert.deepEqual(pricing, {
    provider: "openai",
    model: "gpt-test",
    status: "priced",
    registryVersion: "conformance-v1",
  });
  const handle = await client.invoke({
    agentKey: "support",
    idempotencyKey: "typescript-lost-ack",
    input: "hello",
    spec: {
      instructions: "help",
      model: { provider: "openai", id: "gpt-test" },
      outputSchema: defineJsonSchema<Answer>({
        type: "object",
        properties: { answer: { type: "string" } },
        required: ["answer"],
        additionalProperties: false,
      }),
    },
  });
  assert.equal(handle.invocationId, invocationId);
  assert.equal(handle.sessionId, sessionId);
  assert.equal(handle.agentId, agentId);
  assert.equal(handle.deduplicated, true);

  const resumed = client.invocation(invocationId);
  assert.equal(resumed.status, undefined);
  await resumed.refresh();
  assert.equal(resumed.status, "completed");
  assert.equal(resumed.agentId, agentId);
  assert.equal(resumed.deduplicated, undefined);

  const waiting = client.invocation(waitId);
  const actionable = await waiting.waitForAction({
    minPollIntervalMs: 1,
    maxPollIntervalMs: 2,
  });
  assert.equal(actionable.status, "waiting");
  assert.equal(actionable.pendingToolCalls?.[0]?.id, toolCallId);

  const controller = new AbortController();
  setTimeout(() => controller.abort(), 10);
  await assert.rejects(
    waiting.wait({ signal: controller.signal, minPollIntervalMs: 1, maxPollIntervalMs: 2 }),
    (error: unknown) => error instanceof NvokenError && error.category === "timeout",
  );

  const firstPage = await client.listInvocations();
  assert.equal(firstPage.hasMore, true);
  assert.equal(firstPage.nextCursor, "invocations-page-2");
  const secondPage = await client.listInvocations({ cursor: firstPage.nextCursor ?? undefined });
  assert.equal(secondPage.hasMore, false);
  const messages = await client.listMessages(sessionId);
  assert.equal(messages.nextCursor, "messages-page-2");
  const traversedMessages = [];
  for await (const message of client.messagePages(sessionId, { limit: 1 })) {
    traversedMessages.push(message);
  }
  assert.deepEqual(traversedMessages.map((message) => message.role), ["user", "assistant"]);

  const traversedSessions = [];
  for await (const session of client.sessionPages({
    tenantKey: "acme",
    agentId,
    sessionKey: "ticket-A-42",
    limit: 1,
  })) {
    traversedSessions.push(session);
  }
  assert.equal(traversedSessions.length, 2);
  assert.equal(traversedSessions[0]?.id, sessionId);

  const exactSession = await client.getSessionByKey("ticket-A-42", {
    tenantKey: "acme",
    agentId,
  });
  assert.equal(exactSession.id, sessionId);

  const transcript = await client.drainTranscript(sessionId, { pageSize: 1 });
  assert.deepEqual(transcript.messages.map((message) => message.role), ["user", "assistant"]);
  assert.deepEqual(transcript.invocationChanges.map((change) => change.revision), [1, 2]);
  assert.equal(transcript.resumeCursor, "cursor-2");

  assert.deepEqual((await handle.listMessages()).map((message) => message.role), ["user", "assistant"]);
  assert.equal(await handle.text(), "world");

  const composed = await handle.result();
  assert.equal(composed.invocation.id, invocationId);
  assert.equal(composed.invocation.status, "completed");
  assert.deepEqual(composed.invocation.structuredOutput, { answer: "world" });
  assert.equal(composed.invocation.structuredOutput?.answer, "world");
  assert.equal(composed.invocation.structuredOutputProvenance?.source, "tool_call");
  assert.deepEqual(composed.messages.map((message) => message.role), ["user", "assistant"]);
  assert.equal(composed.outputText, await handle.text());

  const result = await handle.submitToolResults([{ toolCallId, content: { ok: true } }]);
  assert.equal(result.results[0]?.deduplicated, true);
  assert.equal((await handle.cancel()).status, "cancelled");

  await assert.rejects(
    client.getInvocation("conflict"),
    (error: unknown) => error instanceof NvokenError
      && error.category === "conflict"
      && error.status === 409
      && Boolean(error.requestId),
  );
  assert.equal((await client.getInvocation("rate-limit")).status, "completed");
  await assert.rejects(
    client.getInvocation("rate-limit-always"),
    (error: unknown) => error instanceof NvokenError
      && error.category === "rate_limit"
      && error.status === 429
      && error.retryAfterMs === 1_000,
  );
  await assert.rejects(
    client.getInvocation("server-error"),
    (error: unknown) => error instanceof NvokenError
      && error.category === "server"
      && error.status === 503,
  );

  const eventTypes: string[] = [];
  let streamedText: string | null = null;
  for await (const event of client.invocation(invocationId).stream()) {
    eventTypes.push(event.type);
    if (event.type === "invocation.result") {
      streamedText = event.result.outputText;
    }
  }
  assert.deepEqual(eventTypes, [
    "invocation.update",
    "stream.end",
    "invocation.update",
    "invocation.result",
  ]);
  assert.equal(streamedText, "world");
  const state = await fetch(`${baseUrl}/__test/state`).then((response) => response.json()) as {
    admission_attempts: number;
    result_attempts: number;
    cancel_attempts: number;
    stream_attempts: number;
    last_event_id: string;
  };
  assert.deepEqual(state, {
    admission_attempts: 2,
    result_attempts: 2,
    cancel_attempts: 1,
    stream_attempts: 3,
    last_event_id: "cursor-1",
  });
});

test("schema-bound tool helpers preserve application types", () => {
  interface LookupOrderInput {
    orderId: string;
  }

  const lookupOrder = defineHostTool<LookupOrderInput>({
    mode: "host",
    name: "lookup_order",
    description: "Look up one order.",
    inputSchema: defineJsonSchema<LookupOrderInput>({
      type: "object",
      properties: { orderId: { type: "string" } },
      required: ["orderId"],
      additionalProperties: false,
    }),
  });
  const input = toolInput(lookupOrder, {
    id: toolCallId,
    name: "lookup_order",
    input: { orderId: "order-42" },
    deadlineAt: new Date("2026-07-21T12:05:00Z"),
  });
  assert.equal(input.orderId, "order-42");
  assert.throws(
    () => toolInput(lookupOrder, {
      id: toolCallId,
      name: "different_tool",
      input: {},
      deadlineAt: new Date("2026-07-21T12:05:00Z"),
    }),
    (error: unknown) => error instanceof NvokenError && error.category === "validation",
  );

  // @ts-expect-error callback mode requires callback configuration.
  const invalidCallback: Tool = {
    mode: "callback",
    name: "notify",
    description: "Notify a host.",
    inputSchema: {},
  };
  void invalidCallback;

  const invalidHost: HostTool = {
    mode: "host",
    name: "lookup",
    description: "Lookup",
    inputSchema: {},
    // @ts-expect-error host tools cannot carry callback configuration.
    callback: { url: "https://example.com/callback" },
  };
  void invalidHost;
});

test("agent run converts standard schemas, retries one admission, and dispatches host tools", async () => {
  interface LookupInput {
    orderId: string;
  }
  interface StructuredAnswer {
    answer: string;
  }

  const standardSchema = <Input extends object, Output extends object>(
    schema: Record<string, unknown>,
  ): StandardJSONSchemaV1<Input, Output> => ({
    "~standard": {
      version: 1,
      vendor: "nvoken-test",
      jsonSchema: {
        input: () => ({ $schema: "https://json-schema.org/draft/2020-12/schema", ...schema }),
        output: () => ({ $schema: "https://json-schema.org/draft/2020-12/schema", ...schema }),
      },
    },
  });
  const inputSchema = standardSchema<LookupInput, LookupInput>({
    type: "object",
    properties: { orderId: { type: "string" } },
    required: ["orderId"],
    additionalProperties: false,
  });
  const outputSchema = standardSchema<StructuredAnswer, StructuredAnswer>({
    type: "object",
    properties: { answer: { type: "string" } },
    required: ["answer"],
    additionalProperties: false,
  });

  let admissions = 0;
  let submitted = false;
  const admissionBodies: Array<Record<string, unknown>> = [];
  const json = (status: number, value: unknown) => new Response(JSON.stringify(value), {
    status,
    headers: { "content-type": "application/json" },
  });
  const invocation = (status: "waiting" | "completed") => ({
    id: invocationId,
    agent_id: agentId,
    session_id: sessionId,
    status,
    error: null,
    usage: null,
    provenance: null,
    structured_output: status === "completed" ? { answer: "ready" } : null,
    structured_output_provenance: null,
    limits: {
      total_timeout_seconds: 300,
      active_timeout_seconds: 120,
      waiting_timeout_seconds: 180,
      max_iterations: 3,
    },
    active_execution_ms: 0,
    deadline_at: "2026-07-21T12:05:00Z",
    created_at: "2026-07-21T12:00:00Z",
    updated_at: "2026-07-21T12:00:01Z",
    ended_at: status === "completed" ? "2026-07-21T12:00:01Z" : null,
    pending_tool_calls: status === "waiting" ? [{
      id: toolCallId,
      name: "lookup_order",
      input: { orderId: "order-42" },
      deadline_at: "2026-07-21T12:05:00Z",
    }] : [],
  });
  const fetchMock: typeof fetch = async (input, init) => {
    const url = new URL(typeof input === "string" ? input : input instanceof URL ? input : input.url);
    if (url.pathname === "/v1/invocations" && init?.method === "POST") {
      admissions += 1;
      admissionBodies.push(JSON.parse(String(init.body)) as Record<string, unknown>);
      if (admissions === 1) {
        return json(503, { code: "unavailable", message: "retry" });
      }
      return json(202, {
        agent_id: agentId,
        session_id: sessionId,
        invocation_id: invocationId,
        status: "queued",
        deduplicated: true,
      });
    }
    if (url.pathname.endsWith("/tool-results") && init?.method === "POST") {
      submitted = true;
      return json(202, {
        invocation_id: invocationId,
        session_id: sessionId,
        status: "queued",
        results: [{ tool_call_id: toolCallId, status: "completed", deduplicated: false }],
        pending_tool_calls: [],
      });
    }
    if (url.pathname.endsWith("/result")) {
      return json(200, {
        invocation: invocation("completed"),
        messages: [],
        output_text: "ready",
      });
    }
    if (url.pathname === `/v1/invocations/${invocationId}`) {
      return json(200, invocation(submitted ? "completed" : "waiting"));
    }
    throw new Error(`unexpected request ${init?.method} ${url.pathname}`);
  };

  const client = new Client({
    baseUrl: "http://nvoken.test",
    apiKey: "test-key",
    model: { provider: "openai", id: "gpt-test" },
    fetch: fetchMock,
    retry: { maxAttempts: 2, minDelayMs: 1, maxDelayMs: 1 },
  });
  const lookup = defineHostTool({
    name: "lookup_order",
    description: "Look up an order.",
    inputSchema,
    handler: async (input) => ({ state: input.orderId === "order-42" ? "ready" : "missing" }),
  });
  const result = await client.agent({
    agentKey: "support",
    instructions: "Help the customer.",
    tools: [lookup],
    outputSchema,
  }).run("Where is my order?");

  assert.equal(result.text, "ready");
  assert.deepEqual(result.structuredOutput, { answer: "ready" });
  assert.equal(result.deduplicated, true);
  assert.equal(admissions, 2);
  assert.equal(
    admissionBodies[0]?.idempotency_key,
    admissionBodies[1]?.idempotency_key,
  );
  assert.match(String(admissionBodies[0]?.idempotency_key), /^nvoken-/);
  const admittedSpec = admissionBodies[0]?.spec as {
    model: { id: string };
    tools: Array<{ mode: string; input_schema: Record<string, unknown> }>;
    output: { schema: Record<string, unknown> };
  };
  assert.equal(admittedSpec.model.id, "gpt-test");
  assert.equal(admittedSpec.tools[0]?.mode, "host");
  assert.equal(admittedSpec.tools[0]?.input_schema.$schema, undefined);
  assert.equal(admittedSpec.output.schema.$schema, undefined);
});

test("client reads only marked quickstart env files and explicit options win", async () => {
  const directory = await mkdtemp(join(tmpdir(), "nvoken-client-"));
  const marked = join(directory, ".env");
  const unmarked = join(directory, "unmarked.env");
  try {
    await writeFile(marked, [
      "# Generated by nvokend quickstart. Disposable local use only.",
      "NVOKEN_API_KEY=file-key",
      "NVOKEN_BASE_URL=http://file.test:8080",
      "NVOKEN_PROVIDER=openai",
      "NVOKEN_MODEL=gpt-file",
      "",
    ].join("\n"));
    await writeFile(unmarked, "NVOKEN_API_KEY=ignored\n");
    const fromFile = new Client({ envFile: marked });
    assert.equal(fromFile.configuration.basePath, "http://file.test:8080");
    assert.deepEqual(fromFile.defaultModel, { provider: "openai", id: "gpt-file" });

    const explicit = new Client({
      envFile: marked,
      baseUrl: "http://explicit.test",
      apiKey: "explicit-key",
      model: { provider: "anthropic", id: "claude-explicit" },
    });
    assert.equal(explicit.configuration.basePath, "http://explicit.test");
    assert.deepEqual(explicit.defaultModel, {
      provider: "anthropic",
      id: "claude-explicit",
    });
    assert.throws(
      () => new Client({ envFile: unmarked }),
      (error: unknown) => error instanceof NvokenError && error.category === "validation",
    );
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test("session conflicts normalize to SessionBusyError with active work", async () => {
  const client = new Client({
    baseUrl: "http://nvoken.test",
    apiKey: "test-key",
    fetch: async () => new Response(JSON.stringify({
      code: "session_invocation_active",
      message: "This Session already has a nonterminal Invocation.",
      request_id: "req_busy",
      details: { invocation_id: invocationId, status: "waiting" },
    }), {
      status: 409,
      headers: { "content-type": "application/json" },
    }),
    retry: { maxAttempts: 1 },
  });
  await assert.rejects(
    client.invoke({
      agentKey: "support",
      input: "hello",
      spec: {
        instructions: "help",
        model: { provider: "openai", id: "gpt-test" },
      },
    }),
    (error: unknown) => error instanceof SessionBusyError
      && error.activeInvocationId === invocationId
      && error.activeInvocationStatus === "waiting",
  );
});

test("agent stream exposes the two-event consumer without a reducer", async () => {
  const frames = [
    {
      event: "invocation.accepted",
      data: {
        type: "invocation.accepted",
        agent_id: agentId,
        session_id: sessionId,
        invocation_id: invocationId,
        status: "queued",
        deduplicated: false,
        deadline_at: "2026-07-21T12:05:00Z",
      },
    },
    {
      event: "output_text.delta",
      data: {
        type: "output_text.delta",
        session_id: sessionId,
        invocation_id: invocationId,
        attempt: 1,
        iteration: 1,
        content_index: 0,
        text: "hello",
        emitted_at: "2026-07-21T12:00:00Z",
      },
    },
    {
      event: "invocation.result",
      id: "cursor-2",
      data: {
        type: "invocation.result",
        session_id: sessionId,
        invocation_id: invocationId,
        result: {
          invocation: {
            id: invocationId,
            agent_id: agentId,
            session_id: sessionId,
            status: "completed",
            error: null,
            usage: null,
            provenance: null,
            structured_output: null,
            structured_output_provenance: null,
            limits: {
              total_timeout_seconds: 300,
              active_timeout_seconds: 120,
              waiting_timeout_seconds: 180,
              max_iterations: 1,
            },
            active_execution_ms: 5,
            deadline_at: "2026-07-21T12:05:00Z",
            created_at: "2026-07-21T12:00:00Z",
            updated_at: "2026-07-21T12:00:01Z",
            ended_at: "2026-07-21T12:00:01Z",
            pending_tool_calls: [],
          },
          messages: [],
          output_text: "hello",
        },
      },
    },
    {
      event: "stream.end",
      data: {
        type: "stream.end",
        session_id: sessionId,
        invocation_id: invocationId,
        reason: "terminal",
        resume_cursor: "cursor-2",
      },
    },
  ];
  const sse = frames.map((frame) =>
    `${frame.id ? `id: ${frame.id}\n` : ""}event: ${frame.event}\n`
    + `data: ${JSON.stringify(frame.data)}\n\n`
  ).join("");
  const requestBodies: string[] = [];
  let attempts = 0;
  const client = new Client({
    baseUrl: "http://nvoken.test",
    apiKey: "test-key",
    model: { provider: "openai", id: "gpt-test" },
    fetch: async (_input, init) => {
      attempts += 1;
      requestBodies.push(String(init?.body));
      if (attempts === 1) {
        return Response.json(
          { code: "unavailable", message: "retry", request_id: "req_stream_retry" },
          { status: 503 },
        );
      }
      return new Response(sse, {
        status: 202,
        headers: { "content-type": "text/event-stream" },
      });
    },
    retry: { maxAttempts: 2, minDelayMs: 1, maxDelayMs: 1 },
  });

  let text = "";
  let output = "";
  for await (const event of client.agent({ agentKey: "support" }).stream("hello")) {
    if (event.type === "output_text.delta") text += event.text;
    if (event.type === "invocation.result") output = event.result.outputText ?? "";
  }

  assert.equal(text, "hello");
  assert.equal(output, "hello");
  assert.equal(attempts, 2);
  assert.equal(requestBodies[0], requestBodies[1]);
  assert.equal((JSON.parse(requestBodies[0]!) as { input: string }).input, "hello");
});

test("bound session serializes invoke admission until the prior turn ends", async () => {
  const secondInvocationId = "invk_019b0a12-8d51-7f34-aed2-0e07c1bdb329";
  let admissions = 0;
  let finishFirst!: () => void;
  const firstFinished = new Promise<void>((resolvePromise) => {
    finishFirst = resolvePromise;
  });
  const completedInvocation = (id: string) => ({
    id,
    agent_id: agentId,
    session_id: sessionId,
    status: "completed",
    error: null,
    usage: null,
    provenance: null,
    structured_output: null,
    structured_output_provenance: null,
    limits: {
      total_timeout_seconds: 300,
      active_timeout_seconds: 120,
      waiting_timeout_seconds: 180,
      max_iterations: 1,
    },
    active_execution_ms: 1,
    deadline_at: "2026-07-21T12:05:00Z",
    created_at: "2026-07-21T12:00:00Z",
    updated_at: "2026-07-21T12:00:01Z",
    ended_at: "2026-07-21T12:00:01Z",
    pending_tool_calls: [],
  });
  const client = new Client({
    baseUrl: "http://nvoken.test",
    apiKey: "test-key",
    model: { provider: "openai", id: "gpt-test" },
    fetch: async (input, init) => {
      const url = new URL(
        typeof input === "string" ? input : input instanceof URL ? input : input.url,
      );
      if (url.pathname === "/v1/invocations" && init?.method === "POST") {
        admissions += 1;
        const id = admissions === 1 ? invocationId : secondInvocationId;
        return new Response(JSON.stringify({
          agent_id: agentId,
          session_id: sessionId,
          invocation_id: id,
          status: "queued",
          deduplicated: false,
          deadline_at: "2026-07-21T12:05:00Z",
        }), {
          status: 202,
          headers: { "content-type": "application/json" },
        });
      }
      if (url.pathname === `/v1/invocations/${invocationId}`) {
        await firstFinished;
        return Response.json(completedInvocation(invocationId));
      }
      if (url.pathname === `/v1/invocations/${secondInvocationId}`) {
        return Response.json(completedInvocation(secondInvocationId));
      }
      throw new Error(`unexpected request ${init?.method} ${url.pathname}`);
    },
  });
  const chat = client.agent({ agentKey: "support" }).session({ sessionKey: "ticket-42" });

  const first = await chat.invoke("first");
  const secondPromise = chat.invoke("second");
  await new Promise((resolvePromise) => setTimeout(resolvePromise, 0));
  assert.equal(admissions, 1);

  finishFirst();
  const second = await secondPromise;
  assert.equal(first.invocationId, invocationId);
  assert.equal(second.invocationId, secondInvocationId);
  assert.equal(admissions, 2);
});

test("shared reducer vector", async () => {
  const fixture = JSON.parse(await readFile(
    new URL("../../../conformance/fixtures/reducer.json", import.meta.url),
    "utf8",
  )) as {
    events: Array<{ id: string; event: string; data: unknown }>;
    expected: {
      message_sequences: number[];
      invocation_revisions: number[];
      resume_cursor: string;
    };
  };
  const reducer = new Reducer();
  for (const event of fixture.events) {
    reducer.apply({ id: event.id, type: event.event, data: event.data });
  }
  const snapshot = reducer.snapshot();
  assert.deepEqual(snapshot.messages.map((message) => message.sequence), fixture.expected.message_sequences);
  assert.deepEqual(snapshot.invocationChanges.map((change) => change.revision), fixture.expected.invocation_revisions);
  assert.equal(snapshot.resumeCursor, fixture.expected.resume_cursor);
});

test("shared callback signing and deduplication vector", async () => {
  const vector = JSON.parse(await readFile(
    new URL("../../../../docs/design/callback-signing-v1.json", import.meta.url),
    "utf8",
  )) as {
    key: string;
    now: number;
    headers: Record<string, string>;
    body: string;
  };
  const key = new TextEncoder().encode(vector.key);
  const body = new TextEncoder().encode(vector.body);
  const verified = await verifyCallback(
    key,
    new Headers(vector.headers),
    body,
    new Date(vector.now * 1_000),
  );
  assert.equal(verified.toolCallId, toolCallId);

  const mutations: Array<(headers: Headers, candidate: Uint8Array) => Uint8Array> = [
    (_headers, candidate) => new Uint8Array([...candidate, 32]),
    (headers, candidate) => {
      headers.set("x-nvoken-timestamp", "1784635801");
      return candidate;
    },
    (headers, candidate) => {
      headers.set("x-nvoken-delivery-id", "different");
      return candidate;
    },
    (headers, candidate) => {
      headers.set("x-nvoken-signature", "sha256=00");
      return candidate;
    },
  ];
  for (const mutate of mutations) {
    const headers = new Headers(vector.headers);
    const candidate = mutate(headers, body);
    await assert.rejects(verifyCallback(key, headers, candidate, new Date(vector.now * 1_000)));
  }

  let stored: { ok: boolean } | undefined;
  const store = {
    async putIfAbsent(_identity: string, value: { ok: boolean }) {
      if (stored) return { value: stored, inserted: false };
      stored = value;
      return { value, inserted: true };
    },
  };
  assert.equal((await deduplicateCallbackResult(store, toolCallId, { ok: true })).replayed, false);
  const duplicate = await deduplicateCallbackResult(store, toolCallId, { ok: false });
  assert.equal(duplicate.replayed, true);
  assert.deepEqual(duplicate.value, { ok: true });
});
