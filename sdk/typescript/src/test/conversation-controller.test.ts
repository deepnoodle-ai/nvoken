import assert from "node:assert/strict";
import test from "node:test";
import {
  createAnonymousConversation,
  createBrowserClient,
  createConversation,
  type ConversationClock,
  type ConversationSnapshot,
  type ConversationStorageAdapter,
} from "../browser.js";
import { NvokenError } from "../client.js";
import { Reducer } from "../stream.js";

const sessionId = "sess_019b0a12-8d51-7f34-aed2-0e07c1bdb321";
const invocationId = "inv_019b0a12-8d51-7f34-aed2-0e07c1bdb322";

test("the host controller admits once, resumes the acknowledged Session, and keeps browser authority honest", async () => {
  const requests: Array<{ url: URL; body?: Record<string, unknown> }> = [];
  let admissionAttempts = 0;
  const client = createBrowserClient({
    baseUrl: "https://controller.example.test",
    clientToken: () => "header.payload.signature",
    retry: { maxAttempts: 2, minDelayMs: 0, maxDelayMs: 0 },
    fetch: async (input, init) => {
      const url = requestURL(input);
      const body = init?.body ? JSON.parse(String(init.body)) as Record<string, unknown> : undefined;
      requests.push({ url, body });
      if (url.pathname === "/v1/invocations") {
        admissionAttempts += 1;
        if (admissionAttempts === 1) throw new TypeError("response was lost");
        return admissionResponse();
      }
      if (url.pathname.endsWith("/transcript")) return transcriptResponse();
      if (url.pathname.endsWith("/stream")) return openSSE(init?.signal);
      throw new Error(`unexpected request ${url.pathname}`);
    },
  });
  const controller = createConversation({ client });
  const initial = controller.getSnapshot();
  assert.strictEqual(controller.getSnapshot(), initial);
  assert.equal(initial.connection.status, "no_session");
  assert.equal(requests.length, 0, "a Sessionless constructor performs no read or stream");

  let notifiedAfterReplacement = false;
  controller.subscribe(() => { throw new Error("one renderer failed"); });
  const unsubscribe = controller.subscribe(() => {
    notifiedAfterReplacement ||= controller.getSnapshot() !== initial;
  });
  await assert.rejects(
    () => controller.startOver(),
    (error: unknown) => isInvalidState(error, "host_owned"),
  );
  await assert.rejects(
    () => controller.erase(),
    (error: unknown) => isInvalidState(error, "host_owned"),
  );
  assert.equal(requests.length, 0, "disabled host actions perform no work");

  const receipt = await controller.send("Keep this exact draft in the renderer.");
  assert.deepEqual(receipt, { invocationId, sessionId, deduplicated: false });
  assert.equal(Object.isFrozen(receipt), true);
  assert.equal(admissionAttempts, 2, "the low-level client recovered the lost response");
  const admissions = requests.filter((request) => request.url.pathname === "/v1/invocations");
  assert.equal(admissions[0].body?.idempotency_key, admissions[1].body?.idempotency_key);
  assert.equal(admissions[0].body?.input, "Keep this exact draft in the renderer.");
  assert.equal(notifiedAfterReplacement, true);

  await waitFor(controller, (snapshot) => snapshot.connection.status === "connected");
  const transcript = requests.find((request) => request.url.pathname.endsWith("/transcript"));
  assert.equal(transcript?.url.searchParams.get("tail"), "true");
  assert.equal(transcript?.url.searchParams.get("limit"), "50");
  const stream = requests.find((request) => request.url.pathname.endsWith("/stream"));
  assert.equal(stream?.url.searchParams.get("cursor"), "cursor-tail");
  unsubscribe();
  controller.destroy();
});

test("an unknown nonterminal lifecycle stays explicit and cannot enable a second send", async () => {
  let interruptAttempts = 0;
  const client = createBrowserClient({
    baseUrl: "https://unknown.example.test",
    clientToken: () => "header.payload.signature",
    retry: { maxAttempts: 2, minDelayMs: 0, maxDelayMs: 0 },
    fetch: async (input, init) => {
      const url = requestURL(input);
      if (url.pathname.endsWith("/transcript")) {
        return transcriptResponse({
          invocation_changes: [wireChange("paused_by_future_runtime", false)],
        });
      }
      if (url.pathname.endsWith("/stream")) return openSSE(init?.signal);
      if (url.pathname.endsWith(`/invocations/${invocationId}/interrupt`)) {
        interruptAttempts += 1;
        if (interruptAttempts === 1) throw new TypeError("interrupt response was lost");
        return admissionResponse();
      }
      throw new Error(`unexpected request ${url.pathname}`);
    },
  });
  const controller = createConversation({ client, sessionId });
  const snapshot = await waitFor(
    controller,
    (value) => value.activity.status === "unknown" && value.connection.status === "connected",
  );
  assert.deepEqual(snapshot.activity, {
    status: "unknown",
    invocationId,
    invocationStatus: "paused_by_future_runtime",
  });
  assert.deepEqual(snapshot.send.action, { status: "disabled", reason: "conversation_active" });
  assert.equal(snapshot.connection.status, "connected");
  await controller.interrupt();
  assert.equal(interruptAttempts, 2);
  controller.destroy();
});

test("host authorization loss stops automatic work and recovers through the token callback", async () => {
  let refreshed = false;
  let tokenReads = 0;
  const paths: string[] = [];
  const client = createBrowserClient({
    baseUrl: "https://authorization.example.test",
    clientToken: () => {
      tokenReads += 1;
      return refreshed ? "fresh.header.signature" : "expired.header.signature";
    },
    retry: { maxAttempts: 1 },
    fetch: async (input, init) => {
      const url = requestURL(input);
      paths.push(url.pathname);
      if (url.pathname.endsWith("/transcript") && !refreshed) {
        return Response.json({ message: "expired", code: "invalid_token" }, { status: 401 });
      }
      if (url.pathname.endsWith("/transcript")) return transcriptResponse();
      if (url.pathname.endsWith("/stream")) return openSSE(init?.signal);
      throw new Error(`unexpected request ${url.pathname}`);
    },
  });
  const controller = createConversation({ client, sessionId });
  const lost = await waitFor(controller, (snapshot) => snapshot.authorization.status === "lost");
  assert.equal(lost.recovery.status, "authorization_lost");
  assert.equal(lost.retryAuthorization.status, "enabled");
  const stoppedAt = paths.length;
  await Promise.resolve();
  assert.equal(paths.length, stoppedAt, "authorization loss does not spin a reconnect loop");

  refreshed = true;
  await controller.retryAuthorization();
  const recovered = await waitFor(controller, (snapshot) => snapshot.connection.status === "connected");
  assert.equal(recovered.authorization.status, "ready");
  assert.equal(recovered.recovery.status, "none");
  assert.equal(tokenReads >= 2, true, "the existing host callback resolves again per request");
  assert.equal(paths.some((path) => path.includes("anonymous-tokens")), false);
  controller.destroy();
});

test("a missing optional operation disables only that action", async () => {
  const client = createBrowserClient({
    baseUrl: "https://ceiling.example.test",
    clientToken: "header.payload.signature",
    retry: { maxAttempts: 1 },
    fetch: async (input, init) => {
      const url = requestURL(input);
      if (url.pathname.endsWith("/transcript")) {
        return transcriptResponse({ invocation_changes: [wireChange("running", false)] });
      }
      if (url.pathname.endsWith("/stream")) return openSSE(init?.signal);
      if (url.pathname.endsWith(`/invocations/${invocationId}/interrupt`)) {
        return Response.json(
          { message: "operation is outside this token", code: "forbidden" },
          { status: 403 },
        );
      }
      throw new Error(`unexpected request ${url.pathname}`);
    },
  });
  const controller = createConversation({ client, sessionId });
  try {
    await waitFor(controller, (snapshot) => snapshot.connection.status === "connected");
    await assert.rejects(
      () => controller.interrupt(),
      (error: unknown) => error instanceof NvokenError && error.category === "permission",
    );
    const snapshot = controller.getSnapshot();
    assert.equal(snapshot.authorization.status, "ready");
    assert.equal(snapshot.connection.status, "connected");
    assert.deepEqual(snapshot.interruption.action, {
      status: "disabled",
      reason: "not_authorized",
    });
    assert.equal(snapshot.recovery.status, "none");
  } finally {
    controller.destroy();
  }
});

test("anonymous controllers coordinate one exchange and persist only namespaced opaque continuity", async () => {
  const values = new Map<string, string>();
  const seenKeys: string[] = [];
  const storage: ConversationStorageAdapter = {
    get: (key) => {
      seenKeys.push(key);
      return values.get(key) ?? null;
    },
    set: (key, value) => {
      seenKeys.push(key);
      values.set(key, value);
    },
    delete: (key) => {
      seenKeys.push(key);
      values.delete(key);
    },
  };
  let exchanges = 0;
  const fetch: typeof globalThis.fetch = async () => {
    exchanges += 1;
    await Promise.resolve();
    return Response.json({
      access_token: "opaque-access-token",
      access_token_expires_in_seconds: 900,
      visitor_token: "opaque-visitor-token",
      visitor_token_expires_at: "2027-08-17T12:00:00Z",
      session_id: null,
    }, { status: 201 });
  };
  const first = createAnonymousConversation({
    baseUrl: "https://runtime.example.test/",
    appId: "app_one",
    storage: "memory",
    fetch,
  });
  const second = createAnonymousConversation({
    baseUrl: "https://runtime.example.test",
    appId: "app_one",
    storage: "memory",
    fetch,
  });
  let persistedController: ReturnType<typeof createAnonymousConversation> | undefined;
  try {
    await Promise.all([
      waitFor(first, (snapshot) => snapshot.authorization.status === "ready"),
      waitFor(second, (snapshot) => snapshot.authorization.status === "ready"),
    ]);
    assert.equal(exchanges, 1);
    persistedController = createAnonymousConversation({
      baseUrl: "https://runtime.example.test",
      appId: "app_two",
      storage,
      fetch,
    });
    await waitFor(persistedController, (snapshot) => snapshot.authorization.status === "ready");
    assert.equal(exchanges, 2);
    assert.equal(new Set(seenKeys).size, 1);
    const [[key, persisted]] = [...values.entries()];
    assert.match(key, /runtime\.example\.test:app_two$/);
    assert.equal(persisted.includes("opaque-visitor-token"), true);
    assert.equal(persisted.includes("opaque-access-token"), false);
  } finally {
    first.destroy();
    second.destroy();
    persistedController?.destroy();
  }
});

test("anonymous expiry uses request elapsed time on the supplied monotonic clock", async () => {
  const clock = new FakeClock();
  let persisted = "";
  const controller = createAnonymousConversation({
    baseUrl: "https://clock.example.test",
    appId: "app_clock",
    storage: {
      get: () => null,
      set: (_key, value) => { persisted = value; },
      delete: () => undefined,
    },
    clock,
    fetch: async () => {
      clock.advance(500);
      return Response.json({
        access_token: "opaque-access",
        access_token_expires_in_seconds: 60,
        visitor_token: "opaque-visitor",
        visitor_token_expires_at: "2030-01-01T00:00:00Z",
        session_id: null,
      }, { status: 201 });
    },
  });
  try {
    const snapshot = await waitFor(controller, (value) => value.authorization.status === "ready");
    assert.equal(snapshot.authorization.status, "ready");
    if (snapshot.authorization.status === "ready") {
      assert.equal(snapshot.authorization.expiresInMs, 59_500);
    }
    assert.equal(clock.nextDelay, 29_500);
    assert.equal(persisted.includes("opaque-visitor"), true);
    assert.equal(persisted.includes("opaque-access"), false);
  } finally {
    controller.destroy();
  }
});

test("anonymous renewal honors Retry-After while the still-valid token remains usable", async () => {
  const clock = new FakeClock();
  let exchanges = 0;
  const controller = createAnonymousConversation({
    baseUrl: "https://renewal.example.test",
    appId: "app_renewal",
    storage: "memory",
    clock,
    fetch: async () => {
      exchanges += 1;
      if (exchanges > 1) {
        return Response.json(
          { message: "try later", code: "temporarily_unavailable" },
          { status: 503, headers: { "retry-after": "5" } },
        );
      }
      return Response.json({
        access_token: "still-valid-access",
        access_token_expires_in_seconds: 60,
        visitor_token: "visitor-renewal",
        visitor_token_expires_at: "2030-01-01T00:00:00Z",
        session_id: null,
      }, { status: 201 });
    },
  });
  try {
    await waitFor(controller, (snapshot) => snapshot.authorization.status === "ready");
    clock.runNext();
    const renewing = await waitFor(
      controller,
      (snapshot) => snapshot.authorization.status === "renewing",
    );
    for (let attempt = 0; attempt < 10 && clock.nextDelay === undefined; attempt += 1) {
      await new Promise((resolve) => setTimeout(resolve, 0));
    }
    assert.equal(renewing.send.action.status, "enabled");
    assert.equal(clock.nextDelay, 5_000);
  } finally {
    controller.destroy();
  }
});

test("throwing anonymous storage degrades to nonfatal memory continuity", async () => {
  const throwing: ConversationStorageAdapter = {
    get: () => { throw new Error("storage denied"); },
    set: () => { throw new Error("storage denied"); },
    delete: () => { throw new Error("storage denied"); },
  };
  const controller = createAnonymousConversation({
    baseUrl: "https://storage.example.test",
    appId: "app_storage",
    storage: throwing,
    fetch: async () => Response.json({
      access_token: "access",
      access_token_expires_in_seconds: 900,
      visitor_token: "visitor",
      visitor_token_expires_at: "2030-01-01T00:00:00Z",
      session_id: null,
    }, { status: 201 }),
  });
  try {
    const snapshot = await waitFor(controller, (value) => value.authorization.status === "ready");
    assert.equal(snapshot.recovery.status, "storage_unavailable");
    assert.equal(snapshot.authorization.continuity, "memory");
    assert.equal(snapshot.send.action.status, "enabled");
  } finally {
    controller.destroy();
  }
});

test("anonymous erase retains continuity while start-over replaces the visitor", async () => {
  const values = new Map<string, string>();
  const exchangeBodies: Array<Record<string, unknown>> = [];
  let exchanges = 0;
  let erasures = 0;
  const controller = createAnonymousConversation({
    baseUrl: "https://reset.example.test",
    appId: "app_reset",
    storage: mapAdapter(values),
    fetch: async (input, init) => {
      const url = requestURL(input);
      if (url.pathname.endsWith("/anonymous-tokens")) {
        exchanges += 1;
        exchangeBodies.push(JSON.parse(String(init?.body)) as Record<string, unknown>);
        return Response.json({
          access_token: `access-${exchanges}`,
          access_token_expires_in_seconds: 900,
          visitor_token: `visitor-${exchanges}`,
          visitor_token_expires_at: "2030-01-01T00:00:00Z",
          session_id: exchanges === 1 ? sessionId : null,
        }, { status: 201 });
      }
      if (url.pathname.endsWith("/transcript")) return transcriptResponse();
      if (url.pathname.endsWith("/stream")) return openSSE(init?.signal);
      if (url.pathname === `/v1/sessions/${sessionId}` && init?.method === "DELETE") {
        erasures += 1;
        return new Response(null, { status: 204 });
      }
      throw new Error(`unexpected request ${init?.method} ${url.pathname}`);
    },
  });
  try {
    await waitFor(controller, (snapshot) => snapshot.connection.status === "connected");
    await controller.erase({ force: true });
    assert.equal(erasures, 1);
    assert.equal(controller.getSnapshot().sessionId, null);
    assert.equal([...values.values()][0].includes("visitor-1"), true, "erase retains continuity");

    await controller.startOver();
    assert.equal(exchanges, 2);
    assert.deepEqual(exchangeBodies[1], {}, "start-over does not send the prior visitor token");
    assert.equal(controller.getSnapshot().sessionId, null);
    assert.equal([...values.values()][0].includes("visitor-2"), true);
  } finally {
    controller.destroy();
  }
});

test("older history never moves the live cursor and stops at the 500-message window", async () => {
  let olderPage = 0;
  const streamCursors: string[] = [];
  const client = createBrowserClient({
    baseUrl: "https://history.example.test",
    clientToken: "header.payload.signature",
    retry: { maxAttempts: 1 },
    fetch: async (input, init) => {
      const url = requestURL(input);
      if (url.pathname.endsWith("/transcript") && url.searchParams.get("tail") === "true") {
        return transcriptResponse({
          messages: wireMessages(451, 500),
          has_more: true,
          next_page_token: "older-1",
        });
      }
      if (url.pathname.endsWith("/transcript")) {
        olderPage += 1;
        const start = olderPage <= 9 ? 451 - olderPage * 50 : 501;
        return transcriptResponse({
          messages: wireMessages(start, start + 49),
          has_more: true,
          next_page_token: `older-${olderPage + 1}`,
        });
      }
      if (url.pathname.endsWith("/stream")) {
        streamCursors.push(url.searchParams.get("cursor") ?? "");
        return openSSE(init?.signal);
      }
      throw new Error(`unexpected request ${url.pathname}`);
    },
  });
  const controller = createConversation({ client, sessionId });
  try {
    await waitFor(controller, (snapshot) => snapshot.connection.status === "connected");
    for (let page = 0; page < 10; page += 1) await controller.loadEarlier();
    const snapshot = controller.getSnapshot();
    assert.equal(snapshot.messages.length, 500);
    assert.equal(snapshot.messages[0].sequence, 1);
    assert.equal(snapshot.messages.at(-1)?.sequence, 500);
    assert.equal(snapshot.olderHistory.status, "window_full");
    assert.deepEqual(streamCursors, ["cursor-tail"]);
    assert.equal(Object.isFrozen(snapshot.messages), true);
    assert.equal(Object.isFrozen(snapshot.messages[0].content), true);
  } finally {
    controller.destroy();
  }
});

test("bounded reducer keeps latest lifecycles and evicts only the oldest terminal boundary", () => {
  const reducer = new Reducer({
    latestChangesOnly: true,
    maxMessages: 2,
    maxPreviews: 2,
    maxPreviewBytes: 3,
  });
  reducer.apply({
    id: "cursor-1",
    type: "transcript.update",
    data: {
      type: "transcript.update",
      session_id: sessionId,
      messages: [wireMessage(1, "inv_old"), wireMessage(2, "inv_old")],
      invocation_changes: [wireChange("completed", true, "inv_old", 1)],
      cursor: "cursor-1",
    },
  });
  reducer.apply({
    id: "cursor-2",
    type: "transcript.update",
    data: {
      type: "transcript.update",
      session_id: sessionId,
      messages: [wireMessage(3, invocationId)],
      invocation_changes: [
        wireChange("queued", false, invocationId, 1),
        wireChange("running", false, invocationId, 2),
      ],
      cursor: "cursor-2",
    },
  });
  reducer.apply({
    type: "message.delta",
    data: wireDelta("msg_preview_1", 0, "ééé"),
  });
  reducer.apply({
    type: "message.delta",
    data: wireDelta("msg_preview_2", 0, "two"),
  });
  reducer.apply({
    type: "message.delta",
    data: wireDelta("msg_preview_3", 0, "three"),
  });
  const snapshot = reducer.snapshot();
  assert.deepEqual(snapshot.messages.map((message) => message.sequence), [3]);
  assert.deepEqual(snapshot.invocationChanges.map((change) => [change.invocationId, change.revision]), [
    [invocationId, 2],
  ]);
  assert.equal(snapshot.previews.length, 2);
  assert.equal(new TextEncoder().encode(snapshot.previews[0].delta).length <= 3, true);
});

test("browser APIs are canonical to the browser subpath and destroy is silent and final", async () => {
  const root = await import("../index.js");
  const browser = await import("../browser.js");
  assert.equal("createConversation" in root, false);
  assert.equal("createBrowserClient" in root, false);
  assert.equal(typeof browser.createConversation, "function");
  assert.equal(typeof browser.issueAnonymousToken, "function");

  const client = createBrowserClient({
    baseUrl: "https://destroy.example.test",
    clientToken: "header.payload.signature",
    fetch: async () => { throw new Error("no request expected"); },
  });
  const controller = createConversation({ client });
  let notifications = 0;
  controller.subscribe(() => { notifications += 1; });
  controller.destroy();
  const afterDestroy = notifications;
  controller.destroy();
  await Promise.resolve();
  assert.equal(notifications, afterDestroy);
  assert.equal(controller.getSnapshot().recovery.status, "destroyed");
});

function requestURL(input: RequestInfo | URL): URL {
  return new URL(typeof input === "string" ? input : input instanceof URL ? input : input.url);
}

function transcriptResponse(overrides: Record<string, unknown> = {}): Response {
  return Response.json({
    messages: [],
    invocation_changes: [],
    has_more: false,
    cursor: "cursor-tail",
    next_page_token: null,
    ...overrides,
  });
}

function admissionResponse(): Response {
  return Response.json({
    id: invocationId,
    agent_id: "agent_1",
    session_id: sessionId,
    definition_id: "def_1",
    definition: null,
    status: "queued",
    stop_reason: null,
    attempt: 1,
    error: null,
    usage: null,
    provenance: null,
    structured_output: null,
    structured_output_provenance: null,
    metadata: {},
    limits: {
      total_timeout_seconds: 300,
      active_timeout_seconds: 120,
      waiting_timeout_seconds: 180,
      max_iterations: 3,
    },
    active_execution_ms: 1,
    deadline_at: "2026-07-21T12:05:00Z",
    created_at: "2026-07-21T12:00:00Z",
    updated_at: "2026-07-21T12:00:01Z",
    ended_at: null,
    tool_calls: [],
    deduplicated: false,
  }, { status: 202 });
}

function openSSE(signal?: AbortSignal | null): Response {
  const encoder = new TextEncoder();
  return new Response(new ReadableStream<Uint8Array>({
    start(controller) {
      controller.enqueue(encoder.encode("retry: 1000\n\n"));
      signal?.addEventListener("abort", () => controller.close(), { once: true });
    },
  }), { headers: { "content-type": "text/event-stream" } });
}

function wireChange(
  status: string,
  terminal: boolean,
  id: string = invocationId,
  revision = 1,
): Record<string, unknown> {
  return {
    invocation_id: id,
    revision,
    status,
    terminal,
    stop_reason: terminal ? "end_turn" : null,
    through_message_sequence: null,
    error: null,
    structured_output: null,
    occurred_at: "2026-07-21T12:00:01Z",
  };
}

function wireMessage(sequence: number, invocation: string): Record<string, unknown> {
  return {
    id: `msg_${sequence}`,
    session_id: sessionId,
    sequence,
    invocation_id: invocation,
    role: "assistant",
    phase: "final_answer",
    content: [{ type: "text", text: `message ${sequence}` }],
    created_at: "2026-07-21T12:00:01Z",
  };
}

function wireMessages(start: number, end: number): Record<string, unknown>[] {
  const messages: Record<string, unknown>[] = [];
  for (let sequence = start; sequence <= end; sequence += 1) {
    messages.push(wireMessage(sequence, `inv_${sequence}`));
  }
  return messages;
}

function wireDelta(messageId: string, contentIndex: number, delta: string): Record<string, unknown> {
  return {
    type: "message.delta",
    session_id: sessionId,
    invocation_id: invocationId,
    attempt: 1,
    message_id: messageId,
    content_index: contentIndex,
    kind: "text",
    delta,
  };
}

function isInvalidState(error: unknown, reason: string): boolean {
  return error instanceof NvokenError
    && error.code === "invalid_state"
    && error.details?.reason === reason;
}

function mapAdapter(values: Map<string, string>): ConversationStorageAdapter {
  return {
    get: (key) => values.get(key) ?? null,
    set: (key, value) => { values.set(key, value); },
    delete: (key) => { values.delete(key); },
  };
}

async function waitFor(
  controller: { getSnapshot(): ConversationSnapshot; subscribe(listener: () => void): () => void },
  predicate: (snapshot: ConversationSnapshot) => boolean,
): Promise<ConversationSnapshot> {
  const current = controller.getSnapshot();
  if (predicate(current)) return current;
  return new Promise((resolve, reject) => {
    const timeout = setTimeout(() => {
      unsubscribe();
      reject(new Error(`controller did not reach expected state: ${JSON.stringify(controller.getSnapshot())}`));
    }, 1_000);
    const unsubscribe = controller.subscribe(() => {
      const snapshot = controller.getSnapshot();
      if (!predicate(snapshot)) return;
      clearTimeout(timeout);
      unsubscribe();
      resolve(snapshot);
    });
  });
}

class FakeClock implements ConversationClock {
  private value = 1_000;
  nextDelay?: number;
  private callback?: () => void;

  now(): number {
    return this.value;
  }

  advance(milliseconds: number): void {
    this.value += milliseconds;
  }

  setTimeout(callback: () => void, delayMs: number): unknown {
    this.callback = callback;
    this.nextDelay = delayMs;
    return 1;
  }

  clearTimeout(_handle: unknown): void {
    this.callback = undefined;
    this.nextDelay = undefined;
  }

  runNext(): void {
    const callback = this.callback;
    if (!callback || this.nextDelay === undefined) throw new Error("no timer is scheduled");
    this.advance(this.nextDelay);
    this.callback = undefined;
    this.nextDelay = undefined;
    callback();
  }

  random(): number {
    return 0;
  }
}
