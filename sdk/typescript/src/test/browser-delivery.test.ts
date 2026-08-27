import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

import {
  callbackResult,
  createBrowserClient,
  createCallbackReceiver,
  createWebhookReceiver,
  issueAnonymousToken,
  NoOutputTextError,
  verifyCallback,
  verifyWebhook,
  webhookStatusIsRetried,
  webhookSupersedes,
  type CallbackReply,
} from "../index.js";

const BASE_URL = "https://runtime.example.test";
const TURN_ID = "019b0a12-8d51-7f34-aed2-0e07c1bdb322";
const NOW = "2026-07-21T12:00:00Z";

function wireTurn(status: "queued" | "completed" = "queued") {
  return {
    id: TURN_ID,
    tenant_key: "anonymous-partition",
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
  };
}

test("browser construction rejects a machine API key immediately", () => {
  assert.throws(
    () => createBrowserClient({ baseUrl: BASE_URL, clientToken: "nvk_machine_secret" }),
    /machine API key/,
  );
});

test("a dynamically resolved machine key is refused before transport", async () => {
  let requests = 0;
  const browser = createBrowserClient({
    baseUrl: BASE_URL,
    clientToken: async () => "nvk_machine_secret",
    fetch: async () => {
      requests += 1;
      return Response.json(wireTurn(), { status: 202 });
    },
    retry: { maxAttempts: 1 },
  });
  await assert.rejects(browser.start("hello"), /machine API key/);
  assert.equal(requests, 0);
});

test("browser admission relies on token authority and preserves unknown behavior source", async () => {
  const bodies: Record<string, unknown>[] = [];
  const headers: Headers[] = [];
  const browser = createBrowserClient({
    baseUrl: BASE_URL,
    clientToken: "client-token",
    fetch: async (input, init) => {
      const url = new URL(String(input));
      headers.push(new Headers(init?.headers));
      if (url.pathname === "/v1/turns") {
        bodies.push(JSON.parse(String(init?.body)) as Record<string, unknown>);
        return Response.json({ ...wireTurn(), deduplicated: false }, { status: 202 });
      }
      return Response.json({ turn: wireTurn("completed"), messages: [], output_text: "done" });
    },
    retry: { maxAttempts: 1 },
  });

  const turn = await browser.start("hello", {
    conversation: { key: "support", ownedByUser: "visitor-1", ifActive: "interrupt" },
  });
  const snapshot = await turn.status();

  assert.equal(bodies[0].tenant_key, undefined);
  assert.equal(bodies[0].user_key, undefined);
  assert.equal(bodies[0].behavior, undefined);
  assert.equal(bodies[0].memory, undefined);
  assert.deepEqual(bodies[0].conversation, {
    mode: "continue_or_create",
    conversation_key: "support",
    owner: { kind: "user", user_key: "visitor-1" },
    if_active: "interrupt",
  });
  assert.equal(headers[0].get("X-Nvoken-Tenant-Key"), null);
  assert.equal(headers[0].get("X-Nvoken-User-Key"), null);
  assert.equal(snapshot.behaviorSource, undefined);
  assert.equal(snapshot.stopReason, "end_turn");
});

test("browser text raises the same recoverable NoOutputTextError", async () => {
  const browser = createBrowserClient({
    baseUrl: BASE_URL,
    clientToken: "client-token",
    fetch: async (input) => {
      const url = new URL(String(input));
      return url.pathname === "/v1/turns"
        ? Response.json({ ...wireTurn(), deduplicated: false }, { status: 202 })
        : Response.json({ turn: wireTurn("completed"), messages: [], output_text: null });
    },
    retry: { maxAttempts: 1 },
  });

  await assert.rejects(
    browser.text("hello"),
    (error: unknown) => error instanceof NoOutputTextError
      && error.result.turn.id === TURN_ID,
  );
});

test("anonymous exchange uses target Conversation fields and no credential", async () => {
  let seenHeaders = new Headers();
  let seenBody: Record<string, unknown> | undefined;
  const token = await issueAnonymousToken({
    baseUrl: `${BASE_URL}/`,
    appId: "3215b8a9-28f9-720d-80b9-6d736e94f377",
    idempotencyKey: "anonymous-exchange-1",
    visitorToken: "visitor-1",
    fetch: async (_input, init) => {
      seenHeaders = new Headers(init?.headers);
      seenBody = JSON.parse(String(init?.body)) as Record<string, unknown>;
      return Response.json({
        access_token: "access-1",
        access_token_expires_in_seconds: 900,
        visitor_token: "visitor-2",
        visitor_token_expires_at: "2027-08-17T12:00:00Z",
        conversation_id: "18325d9f-b9bc-797d-9259-96ece372defd",
      }, { status: 201 });
    },
  });

  assert.equal(token.conversationId, "18325d9f-b9bc-797d-9259-96ece372defd");
  assert.deepEqual(seenBody, { visitor_token: "visitor-1" });
  assert.equal(seenHeaders.get("Idempotency-Key"), "anonymous-exchange-1");
  assert.equal(seenHeaders.get("Authorization"), null);
});

interface DeliverySigningVectors {
  key: string;
  now: number;
  vectors: {
    callback: { headers: Record<string, string>; body: string };
    webhook: { headers: Record<string, string>; body: string; event: "turn.ended"; sequence: number };
  };
}

async function deliveryVectors(): Promise<DeliverySigningVectors> {
  return JSON.parse(await readFile(
    new URL("../../../../docs/design/delivery-signing-v1.json", import.meta.url),
    "utf8",
  )) as DeliverySigningVectors;
}

test("schema-v2 callback verification and receiver dedup use Turn identity", async () => {
  const vectors = await deliveryVectors();
  const vector = vectors.vectors.callback;
  const body = new TextEncoder().encode(vector.body);
  const now = () => new Date(vectors.now * 1_000);
  const headers = () => new Headers(vector.headers);
  const key = {
    keyId: vector.headers["X-Nvoken-Signing-Key-ID"],
    version: Number(vector.headers["X-Nvoken-Signing-Key-Version"]),
    secret: vectors.key,
  };
  const verified = await verifyCallback(
    new TextEncoder().encode(vectors.key),
    headers(),
    body,
    now(),
  );
  assert.equal(verified.turnId, TURN_ID);
  assert.equal(verified.conversationId, "019b0a12-8d51-7f34-aed2-0e07c1bdb321");
  assert.equal(verified.toolName, "open_ticket");

  const recorded = new Map<string, CallbackReply>();
  let runs = 0;
  const receiver = createCallbackReceiver({
    keys: [key],
    now,
    store: {
      async find(id) { return recorded.get(id); },
      async putIfAbsent(id, value) {
        const existing = recorded.get(id);
        if (existing) return { value: existing, inserted: false };
        recorded.set(id, value);
        return { value, inserted: true };
      },
    },
    tools: {
      open_ticket: () => {
        runs += 1;
        return callbackResult({ ticket: "T-1042" });
      },
    },
  });
  assert.equal((await receiver.handle(headers(), body)).outcome, "settled");
  assert.equal((await receiver.handle(headers(), body)).outcome, "replayed");
  assert.equal(runs, 1);
});

test("schema-v2 webhook receiver keeps host-owned ordering and retry discipline", async () => {
  const vectors = await deliveryVectors();
  const vector = vectors.vectors.webhook;
  const body = new TextEncoder().encode(vector.body);
  const now = () => new Date(vectors.now * 1_000);
  const headers = () => new Headers(vector.headers);
  const key = {
    keyId: vector.headers["X-Nvoken-Signing-Key-ID"],
    version: Number(vector.headers["X-Nvoken-Signing-Key-Version"]),
    secret: vectors.key,
  };
  const verified = await verifyWebhook(
    new TextEncoder().encode(vectors.key),
    headers(),
    body,
    now(),
  );
  assert.equal(verified.event, "turn.ended");
  assert.equal(verified.turnId, TURN_ID);
  assert.equal(webhookSupersedes(verified, vector.sequence - 1), true);
  assert.equal(webhookSupersedes(verified, vector.sequence), false);

  const failed = await createWebhookReceiver({
    keys: [key],
    now,
    events: { "turn.ended": () => { throw new Error("store unavailable"); } },
  }).handle(headers(), body);
  assert.equal(failed.outcome, "failed");
  assert.equal(webhookStatusIsRetried(failed.reply.status), true);

  const ignored = await createWebhookReceiver({ keys: [key], now, events: {} })
    .handle(headers(), body);
  assert.equal(ignored.outcome, "ignored");
  assert.equal(ignored.reply.status, 200);
});
