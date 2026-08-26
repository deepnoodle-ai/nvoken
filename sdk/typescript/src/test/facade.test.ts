import assert from "node:assert/strict";
import test from "node:test";

import { Client, defineHostTool, NvokenError, TurnTimeoutError } from "../index.js";

const BASE_URL = "https://runtime.example.test";
const TURN_ID = "turn_019b0a12-8d51-7f34-aed2-0e07c1bdb322";
const AGENT_ID = "agent_019b0a12-8d51-7f34-aed2-0e07c1bdb320";
const NOW = "2026-07-21T12:00:00Z";

function wireAgent(owner: Record<string, unknown> = { kind: "app" }) {
  return {
    id: AGENT_ID,
    agent_key: "support",
    name: "Support",
    owner,
    current_revision: 1,
    created_at: NOW,
    updated_at: NOW,
    archived_at: null,
  };
}

function wireTurn(
  status: "queued" | "running" | "waiting" | "completed" = "queued",
  overrides: Record<string, unknown> = {},
) {
  return {
    id: TURN_ID,
    tenant_key: "acme",
    behavior_source: { kind: "inline", digest: "sha256:inline" },
    conversation_id: null,
    memory_space_id: null,
    content_expires_at: null,
    status,
    stop_reason: status === "completed" ? "end_turn" : null,
    attempt: 1,
    error: null,
    structured_output: null,
    active_execution_ms: 1,
    deadline_at: null,
    created_at: NOW,
    updated_at: NOW,
    ended_at: status === "completed" ? NOW : null,
    tool_calls: [],
    ...overrides,
  };
}

function turnResult(
  status: "queued" | "running" | "waiting" | "completed",
  overrides: Record<string, unknown> = {},
  outputText: string | null = null,
): Response {
  return Response.json({
    turn: wireTurn(status, overrides),
    messages: [],
    output_text: outputText,
  });
}

function requestBody(init?: RequestInit): Record<string, unknown> {
  return JSON.parse(String(init?.body)) as Record<string, unknown>;
}

function clientWith(fetch: typeof globalThis.fetch, maxAttempts = 1): Client {
  return new Client({
    baseUrl: BASE_URL,
    apiKey: "test-key",
    fetch,
    retry: { maxAttempts, minDelayMs: 0, maxDelayMs: 0 },
  });
}

test("awaited Agent lookup is exact about the owner namespace", async () => {
  let observed: URL | undefined;
  const client = clientWith(async (input) => {
    observed = new URL(String(input));
    return Response.json({
      items: [wireAgent({ kind: "user", tenant_key: "acme", user_key: "alice" })],
      has_more: false,
      next_cursor: null,
    });
  });

  const agent = await client.agent("support", {
    ownedBy: { tenant: "acme", user: "alice" },
  });

  assert.equal(agent.id, AGENT_ID);
  assert.deepEqual(agent.owner, { kind: "user", tenant: "acme", user: "alice" });
  assert.equal(observed?.pathname, "/v1/agents");
  assert.equal(observed?.searchParams.get("agent_key"), "support");
  assert.equal(observed?.searchParams.get("owner_kind"), "user");
  assert.equal(observed?.searchParams.get("tenant_key"), "acme");
  assert.equal(observed?.searchParams.get("user_key"), "alice");
  assert.equal(observed?.searchParams.get("limit"), "1");
});

test("Agent creation forwards its owner and generates idempotency", async () => {
  let body: Record<string, unknown> | undefined;
  let headers = new Headers();
  const client = clientWith(async (_input, init) => {
    body = requestBody(init);
    headers = new Headers(init?.headers);
    return Response.json(wireAgent({ kind: "tenant", tenant_key: "acme" }), { status: 201 });
  });

  const agent = await client.agents.create({
    key: "support",
    name: "Support",
    ownedBy: { tenant: "acme" },
    instructions: "Answer briefly.",
    model: "anthropic/claude-sonnet-5",
  });

  assert.equal(agent.key, "support");
  assert.deepEqual(body?.owner, { kind: "tenant", tenant_key: "acme" });
  assert.equal(body?.agent_key, "support");
  assert.match(headers.get("Idempotency-Key") ?? "", /^nvoken-/);
});

test("the facade copies only its six behavior fields", async () => {
  const bodies: Record<string, unknown>[] = [];
  const client = clientWith(async (input, init) => {
    const url = new URL(String(input));
    if (init?.body) bodies.push(requestBody(init));
    if (url.pathname === "/v1/agents") {
      return Response.json(wireAgent(), { status: 201 });
    }
    return Response.json({
      id: "arev_1",
      agent_id: AGENT_ID,
      revision: 2,
      behavior: {
        instructions: "Updated.",
        model: "openai/gpt-5",
        sampling: { temperature: 0.9 },
        reasoning: { effort: "high" },
        mcp_servers: [{ name: "hidden", url: "https://example.test" }],
      },
      behavior_sha256: "sha256:revision",
      created_at: NOW,
    });
  });

  const injected = {
    key: "support",
    instructions: "Be concise.",
    model: "openai/gpt-5",
    sampling: { temperature: 0.2 },
    providerTools: [{ type: "web_search" }],
  } as never;
  const agent = await client.agents.create(injected);
  const revision = await agent.publish({
    instructions: "Updated.",
    model: "openai/gpt-5",
  });

  assert.equal("sampling" in bodies[0], false);
  assert.equal("provider_tools" in bodies[0], false);
  assert.equal("sampling" in revision.behavior, false);
  assert.equal("reasoning" in revision.behavior, false);
  assert.equal("mcpServers" in revision.behavior, false);
});

test("inline start uses direct admission, explicit context, and auto idempotency", async () => {
  let body: Record<string, unknown> | undefined;
  let headers = new Headers();
  const client = clientWith(async (_input, init) => {
    body = requestBody(init);
    headers = new Headers(init?.headers);
    return Response.json({ ...wireTurn(), deduplicated: false }, { status: 202 });
  });

  const turn = await client.inline({
    instructions: "Be useful.",
    model: "anthropic/claude-sonnet-5",
  }).start("hello", { tenant: "acme", user: "alice" });

  assert.equal(turn.id, TURN_ID);
  assert.match(String(body?.idempotency_key), /^nvoken-/);
  assert.equal(body?.tenant_key, "acme");
  assert.equal(body?.user_key, "alice");
  assert.deepEqual(body?.behavior, {
    kind: "inline",
    behavior: {
      instructions: "Be useful.",
      model: "anthropic/claude-sonnet-5",
    },
  });
  assert.equal(headers.get("X-Nvoken-Tenant-Key"), "acme");
  assert.equal(headers.get("X-Nvoken-User-Key"), "alice");
});

test("client.turn is synchronous recovery and status is one passive read", async () => {
  const calls: Array<{ method: string; path: string }> = [];
  const client = clientWith(async (input, init) => {
    const url = new URL(String(input));
    calls.push({ method: init?.method ?? "GET", path: url.pathname });
    return turnResult("running");
  });

  const turn = client.turn(TURN_ID, { tenant: "acme" });
  assert.equal(turn.id, TURN_ID);
  assert.equal(calls.length, 0);

  const snapshot = await turn.status();
  assert.equal(snapshot.status, "running");
  assert.deepEqual(calls, [{ method: "GET", path: `/v1/turns/${TURN_ID}/result` }]);
});

test("user memory always requires an explicit Turn user", async () => {
  let calls = 0;
  const client = clientWith(async () => {
    calls += 1;
    return Response.json({ ...wireTurn(), deduplicated: false }, { status: 202 });
  });
  const inline = client.inline({ instructions: "Remember.", model: "openai/gpt-5" });

  await assert.rejects(
    inline.start("hello", {
      tenant: "acme",
      memory: { scope: "user", namespace: "support" },
    }),
    (error: unknown) => error instanceof NvokenError && error.code === "memory_user_required",
  );
  assert.throws(
    () => inline.conversation({
      tenant: "acme",
      key: "support",
      owner: "tenant",
      memory: { scope: "user", namespace: "support" },
    }),
    (error: unknown) => error instanceof NvokenError && error.code === "memory_user_required",
  );
  assert.equal(calls, 0);
});

test("inline default memory requires a namespace and user actor", async () => {
  const client = clientWith(async () => (
    Response.json({ ...wireTurn(), deduplicated: false }, { status: 202 })
  ));
  assert.throws(
    () => client.inline({
      instructions: "Remember.",
      model: "openai/gpt-5",
      memory: { defaultScope: "tenant" },
    } as never),
    NvokenError,
  );

  const runner = client.inline({
    instructions: "Remember.",
    model: "openai/gpt-5",
    memory: { defaultScope: "user", namespace: "support" },
  });
  await assert.rejects(
    runner.start("hello", { tenant: "acme" }),
    (error: unknown) => error instanceof NvokenError && error.code === "memory_user_required",
  );
});

test("Conversation calls inherit omitted limits and may only narrow them", async () => {
  const bodies: Record<string, unknown>[] = [];
  const client = clientWith(async (_input, init) => {
    bodies.push(requestBody(init));
    return Response.json({ ...wireTurn(), deduplicated: false }, { status: 202 });
  });
  const conversation = client.inline({
    instructions: "Be concise.",
    model: "openai/gpt-5",
  }).conversation({
    tenant: "acme",
    key: "ticket-42",
    owner: "tenant",
    limits: { totalTimeoutSeconds: 60, maxIterations: 4 },
  });

  await conversation.start("hello", { limits: { totalTimeoutSeconds: 30 } });
  assert.deepEqual(bodies[0].limits, {
    total_timeout_seconds: 30,
    max_iterations: 4,
  });

  await assert.rejects(
    conversation.start("again", { limits: { totalTimeoutSeconds: 61 } }),
    (error: unknown) => error instanceof NvokenError && error.code === "limits_must_narrow",
  );
  assert.equal(bodies.length, 1);
});

test("retry replays exactly one admission body and idempotency key", async () => {
  const bodies: string[] = [];
  let attempts = 0;
  const client = clientWith(async (_input, init) => {
    bodies.push(String(init?.body));
    attempts += 1;
    if (attempts === 1) {
      return Response.json({ code: "unavailable", message: "retry" }, { status: 503 });
    }
    return Response.json({ ...wireTurn(), deduplicated: true }, { status: 202 });
  }, 2);

  const turn = await client.inline({
    instructions: "Retry safely.",
    model: "openai/gpt-5",
  }).start("hello", { tenant: "acme" });

  assert.equal(turn.id, TURN_ID);
  assert.equal(bodies.length, 2);
  assert.equal(bodies[0], bodies[1]);
  assert.match(String((JSON.parse(bodies[0]) as Record<string, unknown>).idempotency_key), /^nvoken-/);
});

test("uncertain admission failures retain the recovery idempotency key", async () => {
  const client = clientWith(async () => {
    throw new TypeError("connection dropped after send");
  });

  await assert.rejects(
    client.inline({
      instructions: "Recover safely.",
      model: "openai/gpt-5",
    }).start("hello", { tenant: "acme", idempotencyKey: "recover-turn-1" }),
    (error: unknown) => error instanceof TurnTimeoutError
      && error.idempotencyKey === "recover-turn-1",
  );
});

test("Agent publish keeps one generated idempotency key across retries", async () => {
  const publishKeys: string[] = [];
  let publishAttempts = 0;
  const client = clientWith(async (input, init) => {
    const url = new URL(String(input));
    if (url.pathname === "/v1/agents") {
      return Response.json(wireAgent(), { status: 201 });
    }
    publishKeys.push(new Headers(init?.headers).get("Idempotency-Key") ?? "");
    publishAttempts += 1;
    if (publishAttempts === 1) {
      return Response.json({ code: "unavailable", message: "retry" }, { status: 503 });
    }
    return Response.json({
      id: "arev_2",
      agent_id: AGENT_ID,
      revision: 2,
      behavior: { instructions: "Updated.", model: "openai/gpt-5" },
      behavior_sha256: "sha256:updated",
      created_at: NOW,
    });
  }, 2);

  const agent = await client.agents.create({
    key: "support",
    instructions: "Initial.",
    model: "openai/gpt-5",
  });
  await agent.publish({ instructions: "Updated.", model: "openai/gpt-5" });

  assert.equal(publishKeys.length, 2);
  assert.equal(publishKeys[0], publishKeys[1]);
  assert.match(publishKeys[0], /^nvoken-/);
});

test("result drives a bound host tool once before returning", async () => {
  const requests: Array<{ method: string; path: string; body?: Record<string, unknown> }> = [];
  let resultReads = 0;
  const client = clientWith(async (input, init) => {
    const url = new URL(String(input));
    requests.push({
      method: init?.method ?? "GET",
      path: url.pathname,
      body: init?.body ? requestBody(init) : undefined,
    });
    if (url.pathname === "/v1/turns" && init?.method === "POST") {
      return Response.json({ ...wireTurn(), deduplicated: false }, { status: 202 });
    }
    if (url.pathname.endsWith("/tool-results")) {
      return Response.json({
        turn_id: TURN_ID,
        conversation_id: null,
        content_expires_at: null,
        status: "running",
        results: [{ tool_call_id: "call_1", status: "completed", deduplicated: false }],
        tool_calls: [],
      }, { status: 202 });
    }
    resultReads += 1;
    if (resultReads === 1) {
      return turnResult("waiting", {
        tool_calls: [{
          id: "call_1",
          name: "lookup",
          mode: "host",
          status: "pending",
          arguments: { city: "Boston" },
          updated_at: NOW,
        }],
      });
    }
    return turnResult("completed", {}, "Sunny");
  });

  const lookupTool = defineHostTool<{ city: string }>({
    mode: "host",
    name: "lookup",
    description: "Look up weather by city.",
    inputSchema: {
      type: "object",
      properties: { city: { type: "string" } },
      required: ["city"],
      additionalProperties: false,
    },
  });
  const inline = client.inline({
    instructions: "Use the lookup tool.",
    model: "openai/gpt-5",
    tools: [lookupTool],
  });
  assert.throws(
    () => inline.bindTools({ missing: () => undefined }),
    (error: unknown) => error instanceof NvokenError && error.code === "unknown_tool_handler",
  );

  let handled = 0;
  const runner = inline.bindTools({
    lookup: (input: { city: string }) => {
      handled += 1;
      return { city: input.city, forecast: "Sunny" };
    },
  });
  const turn = await runner.start("weather", { tenant: "acme" });
  const result = await turn.result({ minPollIntervalMs: 0, maxPollIntervalMs: 0 });

  assert.equal(result.text, "Sunny");
  assert.equal(result.stopReason, "end_turn");
  assert.equal(result.error, null);
  assert.equal(handled, 1);
  const submission = requests.find((request) => request.path.endsWith("/tool-results"));
  assert.deepEqual(submission?.body, {
    results: [{
      tool_call_id: "call_1",
      content: { city: "Boston", forecast: "Sunny" },
    }],
  });
});

test("a recovered Turn can finish without another admission", async () => {
  const paths: string[] = [];
  const client = clientWith(async (input) => {
    paths.push(new URL(String(input)).pathname);
    return turnResult("completed", {}, "Recovered");
  });

  const result = await client.turn(TURN_ID, { tenant: "acme" }).result({
    minPollIntervalMs: 0,
    maxPollIntervalMs: 0,
  });

  assert.equal(result.text, "Recovered");
  assert.deepEqual(paths, [`/v1/turns/${TURN_ID}/result`]);
});
