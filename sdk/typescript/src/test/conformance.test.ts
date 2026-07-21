import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

import {
  Client,
  NvokenError,
  Reducer,
  deduplicateCallbackResult,
  verifyCallback,
} from "../index.js";

const invocationId = "invk_019b0a12-8d51-7f34-aed2-0e07c1bdb322";
const sessionId = "sesn_019b0a12-8d51-7f34-aed2-0e07c1bdb321";
const toolCallId = "tcal_019b0a12-8d51-7f34-aed2-0e07c1bdb325";
const waitId = "invk_019b0a12-8d51-7f34-aed2-0e07c1bdb328";

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
    retry: { maximumAttempts: 3, minimumDelayMs: 1, maximumDelayMs: 5 },
  });
  const handle = await client.invoke({
    agentRef: "support",
    idempotencyKey: "typescript-lost-ack",
    input: "hello",
    spec: {
      instructions: "help",
      model: { provider: "openai", name: "gpt-test" },
    },
  });
  assert.equal(handle.invocationId, invocationId);
  assert.equal(handle.sessionId, sessionId);

  const resumed = await client.resume(invocationId);
  assert.equal(resumed.status, "completed");

  const waiting = await client.resume(waitId);
  const controller = new AbortController();
  setTimeout(() => controller.abort(), 10);
  await assert.rejects(
    waiting.wait({ signal: controller.signal, minimumDelayMs: 1, maximumDelayMs: 2 }),
    (error: unknown) => error instanceof NvokenError && error.category === "timeout",
  );

  const firstPage = await client.listInvocations();
  assert.equal(firstPage.hasMore, true);
  assert.equal(firstPage.nextCursor, "invocations-page-2");
  const secondPage = await client.listInvocations({ cursor: firstPage.nextCursor ?? undefined });
  assert.equal(secondPage.hasMore, false);
  const messages = await client.listMessages(sessionId);
  assert.equal(messages.nextCursor, "messages-page-2");

  const result = await handle.submitToolResults([{ toolCallId, content: { ok: true } }]);
  assert.equal(result.results[0]?.deduplicated, true);
  assert.equal((await handle.cancel()).status, "cancelled");

  await assert.rejects(
    client.get("conflict"),
    (error: unknown) => error instanceof NvokenError
      && error.category === "conflict"
      && error.status === 409
      && Boolean(error.requestId),
  );
  assert.equal((await client.get("rate-limit")).status, "completed");
  await assert.rejects(
    client.get("rate-limit-always"),
    (error: unknown) => error instanceof NvokenError
      && error.category === "rate_limit"
      && error.status === 429
      && error.retryAfterMs === 1_000,
  );
  await assert.rejects(
    client.get("server-error"),
    (error: unknown) => error instanceof NvokenError
      && error.category === "server"
      && error.status === 503,
  );

  let reduced: { messages: unknown[]; invocationChanges: unknown[]; resumeCursor?: string } | undefined;
  await (await client.resume(invocationId)).stream((_event, snapshot) => {
    reduced = snapshot;
  });
  assert.equal(reduced?.messages.length, 2);
  assert.equal(reduced?.invocationChanges.length, 2);
  assert.equal(reduced?.resumeCursor, "cursor-2");
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
