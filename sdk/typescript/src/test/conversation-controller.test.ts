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
import { NvokenError } from "../turn-error.js";

const CONVERSATION_ID = "db4eaf24-1ac6-776e-8f98-badc6d0dc764";
const TURN_ID = "476dd7be-97a1-78f3-8096-d7032468a80a";
const NOW = "2026-07-21T12:00:00Z";

test("the host controller admits once and resumes the acknowledged Conversation", async () => {
  const requests: Array<{ url: URL; body?: Record<string, unknown> }> = [];
  let admissionAttempts = 0;
  const client = createBrowserClient({
    baseUrl: "https://controller.example.test",
    clientToken: () => "header.payload.signature",
    retry: { maxAttempts: 2, minDelayMs: 0, maxDelayMs: 0 },
    fetch: async (input, init) => {
      const url = requestURL(input);
      const body = init?.body
        ? JSON.parse(String(init.body)) as Record<string, unknown>
        : undefined;
      requests.push({ url, body });
      if (url.pathname === "/v1/turns") {
        admissionAttempts += 1;
        if (admissionAttempts === 1) throw new TypeError("response was lost");
        return admissionResponse();
      }
      if (url.pathname.endsWith("/transcript")) return transcriptResponse(url);
      if (url.pathname.endsWith("/stream")) return openSSE(init?.signal);
      throw new Error(`unexpected request ${url.pathname}`);
    },
  });
  const controller = createConversation({ client, conversation: { id: CONVERSATION_ID } });
  try {
    const initial = controller.getSnapshot();
    assert.strictEqual(controller.getSnapshot(), initial);

    let notifiedAfterReplacement = false;
    controller.subscribe(() => { throw new Error("one renderer failed"); });
    const unsubscribe = controller.subscribe(() => {
      notifiedAfterReplacement ||= controller.getSnapshot() !== initial;
    });
    await assert.rejects(
      () => controller.startOver(),
      (error: unknown) => isInvalidState(error, "host_owned"),
    );

    await waitFor(controller, (snapshot) => snapshot.connection.status === "connected");
    const receipt = await controller.send("Keep this exact draft in the renderer.");
    assert.deepEqual(receipt, {
      turnId: TURN_ID,
      conversationId: CONVERSATION_ID,
      deduplicated: false,
    });
    assert.equal(Object.isFrozen(receipt), true);
    assert.equal(admissionAttempts, 2, "the low-level client recovered the lost response");
    const admissions = requests.filter((request) => request.url.pathname === "/v1/turns");
    assert.equal(admissions[0].body?.idempotency_key, admissions[1].body?.idempotency_key);
    assert.equal(admissions[0].body?.input, "Keep this exact draft in the renderer.");
    assert.deepEqual(admissions[0].body?.conversation, {
      mode: "continue",
      conversation_id: CONVERSATION_ID,
    });
    assert.equal(notifiedAfterReplacement, true);

    const stream = requests.find((request) => request.url.pathname.endsWith("/stream"));
    assert.equal(stream?.url.searchParams.get("cursor"), "cursor-tail");
    unsubscribe();
  } finally {
    controller.destroy();
  }
});

test("bootstrap seeds activity from the Conversation before any frame arrives", async () => {
  const client = createBrowserClient({
    baseUrl: "https://seed.example.test",
    clientToken: "header.payload.signature",
    retry: { maxAttempts: 1 },
    fetch: async (input, init) => {
      const url = requestURL(input);
      if (url.pathname.endsWith("/transcript")) {
        return transcriptResponse(url, {
          conversation: wireConversation({
            active_turn_id: TURN_ID,
            active_turn_status: "running",
          }),
        });
      }
      // Never delivers a frame, so nothing but the Conversation read can be
      // the source of what the page shows.
      if (url.pathname.endsWith("/stream")) return openSSE(init?.signal);
      throw new Error(`unexpected request ${url.pathname}`);
    },
  });
  const controller = createConversation({ client, conversation: { id: CONVERSATION_ID } });
  try {
    const snapshot = await waitFor(
      controller,
      (value) => value.activity.status === "active",
    );
    assert.deepEqual(snapshot.activity, {
      status: "active",
      turnId: TURN_ID,
      turnStatus: "running",
    });
    assert.deepEqual(snapshot.send.action, { status: "disabled", reason: "conversation_active" });
    assert.equal(snapshot.interruption.action.status, "enabled");
  } finally {
    controller.destroy();
  }
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
        return transcriptResponse(url, {
          conversation: wireConversation({
            active_turn_id: TURN_ID,
            active_turn_status: "paused_by_future_runtime",
          }),
        });
      }
      if (url.pathname.endsWith("/stream")) return openSSE(init?.signal);
      if (url.pathname === `/v1/turns/${TURN_ID}/interrupt`) {
        interruptAttempts += 1;
        if (interruptAttempts === 1) throw new TypeError("interrupt response was lost");
        return Response.json(wireTurn("running"));
      }
      throw new Error(`unexpected request ${url.pathname}`);
    },
  });
  const controller = createConversation({ client, conversation: { id: CONVERSATION_ID } });
  try {
    const snapshot = await waitFor(
      controller,
      (value) => value.activity.status === "unknown" && value.connection.status === "connected",
    );
    assert.deepEqual(snapshot.activity, {
      status: "unknown",
      turnId: TURN_ID,
      turnStatus: "paused_by_future_runtime",
    });
    assert.deepEqual(snapshot.send.action, { status: "disabled", reason: "conversation_active" });
    await controller.interrupt();
    assert.equal(interruptAttempts, 2);
  } finally {
    controller.destroy();
  }
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
      if (url.pathname.endsWith("/transcript")) return transcriptResponse(url);
      if (url.pathname.endsWith("/stream")) return openSSE(init?.signal);
      throw new Error(`unexpected request ${url.pathname}`);
    },
  });
  const controller = createConversation({ client, conversation: { id: CONVERSATION_ID } });
  try {
    const lost = await waitFor(controller, (snapshot) => snapshot.authorization.status === "lost");
    assert.equal(lost.recovery.status, "authorization_lost");
    assert.equal(lost.retryAuthorization.status, "enabled");
    const stoppedAt = paths.length;
    await Promise.resolve();
    assert.equal(paths.length, stoppedAt, "authorization loss does not spin a reconnect loop");

    refreshed = true;
    await controller.retryAuthorization();
    const recovered = await waitFor(
      controller,
      (snapshot) => snapshot.connection.status === "connected",
    );
    assert.equal(recovered.authorization.status, "ready");
    assert.equal(recovered.recovery.status, "none");
    assert.equal(tokenReads >= 2, true, "the existing host callback resolves again per request");
    assert.equal(paths.some((path) => path.includes("anonymous-tokens")), false);
  } finally {
    controller.destroy();
  }
});

test("a refused optional operation disables only that action", async () => {
  const client = createBrowserClient({
    baseUrl: "https://ceiling.example.test",
    clientToken: "header.payload.signature",
    retry: { maxAttempts: 1 },
    fetch: async (input, init) => {
      const url = requestURL(input);
      if (url.pathname.endsWith("/transcript")) {
        return transcriptResponse(url, {
          conversation: wireConversation({
            active_turn_id: TURN_ID,
            active_turn_status: "running",
          }),
        });
      }
      if (url.pathname.endsWith("/stream")) return openSSE(init?.signal);
      if (url.pathname === `/v1/turns/${TURN_ID}/interrupt`) {
        return Response.json(
          { message: "operation is outside this token", code: "forbidden" },
          { status: 403 },
        );
      }
      throw new Error(`unexpected request ${url.pathname}`);
    },
  });
  const controller = createConversation({ client, conversation: { id: CONVERSATION_ID } });
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

test("a busy Conversation refreshes the claim and disables send for the stated reason", async () => {
  let transcriptReads = 0;
  const client = createBrowserClient({
    baseUrl: "https://busy.example.test",
    clientToken: "header.payload.signature",
    retry: { maxAttempts: 1 },
    fetch: async (input, init) => {
      const url = requestURL(input);
      if (url.pathname.endsWith("/transcript")) {
        transcriptReads += 1;
        // The first read is bootstrap and finds the Conversation idle. The
        // refresh after the 409 is where the other client's Turn shows up.
        return transcriptResponse(url, transcriptReads === 1 ? {} : {
          conversation: wireConversation({
            active_turn_id: TURN_ID,
            active_turn_status: "running",
          }),
        });
      }
      if (url.pathname.endsWith("/stream")) return openSSE(init?.signal);
      if (url.pathname === "/v1/turns") {
        return Response.json(
          { message: "a Turn is already running", code: "conversation_active" },
          { status: 409 },
        );
      }
      throw new Error(`unexpected request ${url.pathname}`);
    },
  });
  const controller = createConversation({ client, conversation: { id: CONVERSATION_ID } });
  try {
    await waitFor(controller, (snapshot) => snapshot.connection.status === "connected");
    assert.equal(controller.getSnapshot().send.action.status, "enabled");
    await assert.rejects(
      () => controller.send("hello"),
      (error: unknown) => error instanceof NvokenError && error.category === "conflict",
    );
    assert.equal(transcriptReads, 2, "the 409 triggers exactly one refreshing read");
    const snapshot = controller.getSnapshot();
    assert.deepEqual(snapshot.send.action, { status: "disabled", reason: "conversation_active" });
    assert.deepEqual(snapshot.activity, {
      status: "active",
      turnId: TURN_ID,
      turnStatus: "running",
    });
  } finally {
    controller.destroy();
  }
});

test("an uncertain send is retryable with the same input and the same key", async () => {
  const admissions: Array<Record<string, unknown>> = [];
  let admissionAttempts = 0;
  const client = createBrowserClient({
    baseUrl: "https://uncertain.example.test",
    clientToken: "header.payload.signature",
    retry: { maxAttempts: 1 },
    fetch: async (input, init) => {
      const url = requestURL(input);
      if (url.pathname === "/v1/turns") {
        admissions.push(JSON.parse(String(init?.body)) as Record<string, unknown>);
        admissionAttempts += 1;
        if (admissionAttempts === 1) throw new TypeError("response was lost");
        return admissionResponse();
      }
      if (url.pathname.endsWith("/transcript")) return transcriptResponse(url);
      if (url.pathname.endsWith("/stream")) return openSSE(init?.signal);
      throw new Error(`unexpected request ${url.pathname}`);
    },
  });
  const controller = createConversation({ client, conversation: { id: CONVERSATION_ID } });
  try {
    await waitFor(controller, (snapshot) => snapshot.connection.status === "connected");
    const media = [{
      type: "image" as const,
      source: { kind: "bytes" as const, mediaType: "image/png", data: new Uint8Array([1, 2, 3]) },
    }];
    await assert.rejects(() => controller.send(media as never));

    const uncertain = controller.getSnapshot();
    assert.equal(uncertain.send.status, "uncertain");
    assert.equal(uncertain.send.action.status, "enabled");

    const receipt = await controller.retrySend();
    assert.equal(receipt.turnId, TURN_ID);
    assert.equal(admissions.length, 2);
    assert.equal(admissions[0].idempotency_key, admissions[1].idempotency_key);
    // structuredClone, not a spread: the retry has to send the same bytes.
    assert.deepEqual(admissions[0].input, admissions[1].input);
    assert.equal(controller.getSnapshot().send.status, "idle");
  } finally {
    controller.destroy();
  }
});

test("discarding an uncertain send reopens the composer under a fresh key", async () => {
  const admissions: Array<Record<string, unknown>> = [];
  let admissionAttempts = 0;
  const client = createBrowserClient({
    baseUrl: "https://discard.example.test",
    clientToken: "header.payload.signature",
    retry: { maxAttempts: 1 },
    fetch: async (input, init) => {
      const url = requestURL(input);
      if (url.pathname === "/v1/turns") {
        admissions.push(JSON.parse(String(init?.body)) as Record<string, unknown>);
        admissionAttempts += 1;
        if (admissionAttempts === 1) throw new TypeError("response was lost");
        return admissionResponse();
      }
      if (url.pathname.endsWith("/transcript")) return transcriptResponse(url);
      if (url.pathname.endsWith("/stream")) return openSSE(init?.signal);
      throw new Error(`unexpected request ${url.pathname}`);
    },
  });
  const controller = createConversation({ client, conversation: { id: CONVERSATION_ID } });
  try {
    await waitFor(controller, (snapshot) => snapshot.connection.status === "connected");
    assert.deepEqual(controller.getSnapshot().discardSend, {
      status: "disabled",
      reason: "no_pending_send",
    });
    assert.throws(() => controller.discardSend(), (error: unknown) => isInvalidState(error, "no_pending_send"));

    await assert.rejects(() => controller.send("first draft"));
    const uncertain = controller.getSnapshot();
    assert.equal(uncertain.send.status, "uncertain");
    assert.equal(uncertain.discardSend.status, "enabled");
    // Retrying is the one way to send *this* draft; discarding is the one way
    // to send anything else. Both are on offer, and the page picks.
    assert.deepEqual(uncertain.send.action, { status: "enabled" });

    controller.discardSend();
    const reopened = controller.getSnapshot();
    assert.deepEqual(reopened.send, { status: "idle", action: { status: "enabled" } });
    assert.deepEqual(reopened.discardSend, { status: "disabled", reason: "no_pending_send" });
    await assert.rejects(
      () => controller.retrySend(),
      (error: unknown) => isInvalidState(error, "no_pending_send"),
    );

    // Nothing was cancelled, so the next send is a different logical request
    // rather than a retry of the discarded one.
    const receipt = await controller.send("second draft");
    assert.equal(receipt.turnId, TURN_ID);
    assert.equal(admissions.length, 2);
    assert.notEqual(admissions[0].idempotency_key, admissions[1].idempotency_key);
    assert.equal(admissions[1].input, "second draft");
  } finally {
    controller.destroy();
  }
});

test("recovery actions say nothing is lost rather than that something is running", async () => {
  const client = createBrowserClient({
    baseUrl: "https://intact.example.test",
    clientToken: "header.payload.signature",
    retry: { maxAttempts: 1 },
    fetch: async (input, init) => {
      const url = requestURL(input);
      if (url.pathname.endsWith("/transcript")) return transcriptResponse(url);
      if (url.pathname.endsWith("/stream")) return openSSE(init?.signal);
      throw new Error(`unexpected request ${url.pathname}`);
    },
  });
  const controller = createConversation({ client, conversation: { id: CONVERSATION_ID } });
  try {
    // A stream being opened is a reconnect in flight, not one that is refused.
    assert.equal(controller.getSnapshot().connection.status, "connecting");
    assert.deepEqual(controller.getSnapshot().reconnect, { status: "in_flight" });

    const connected = await waitFor(
      controller,
      (snapshot) => snapshot.connection.status === "connected",
    );
    assert.deepEqual(connected.reconnect, { status: "disabled", reason: "nothing_to_recover" });
    assert.deepEqual(connected.retryAuthorization, {
      status: "disabled",
      reason: "nothing_to_recover",
    });
    await assert.rejects(
      () => controller.reconnect(),
      (error: unknown) => isInvalidState(error, "nothing_to_recover"),
    );
  } finally {
    controller.destroy();
  }

  const unbound = createConversation({
    client,
    conversation: { key: "support", ownedByUser: "visitor-1" },
  });
  try {
    // No Conversation yet, so there is no stream to re-establish.
    assert.deepEqual(unbound.getSnapshot().reconnect, {
      status: "disabled",
      reason: "conversation_missing",
    });
  } finally {
    unbound.destroy();
  }
});

test("anonymous controllers coordinate one exchange and persist only namespaced opaque continuity", async () => {
  const values = new Map<string, string>();
  const seenKeys: string[] = [];
  const storage: ConversationStorageAdapter = {
    get: (key) => { seenKeys.push(key); return values.get(key) ?? null; },
    set: (key, value) => { seenKeys.push(key); values.set(key, value); },
    delete: (key) => { seenKeys.push(key); values.delete(key); },
  };
  let exchanges = 0;
  const fetch: typeof globalThis.fetch = async () => {
    exchanges += 1;
    await Promise.resolve();
    return grantResponse(exchanges, null);
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
    assert.equal(persisted.includes("visitor-2"), true);
    assert.equal(persisted.includes("access-2"), false);
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
      return grantResponse(1, null, 60);
    },
  });
  try {
    const snapshot = await waitFor(controller, (value) => value.authorization.status === "ready");
    assert.equal(snapshot.authorization.status, "ready");
    if (snapshot.authorization.status === "ready") {
      // A token minted for 60 seconds is not usable for 60 seconds once the
      // exchange itself has already spent half of one.
      assert.equal(snapshot.authorization.expiresInMs, 59_500);
    }
    assert.equal(clock.nextDelay, 29_500);
    assert.equal(persisted.includes("visitor-1"), true);
    assert.equal(persisted.includes("access-1"), false);
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
      return grantResponse(1, null, 60);
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
    // An authorization attempt is running; a manual retry is not refused, it
    // is already happening.
    assert.deepEqual(renewing.retryAuthorization, { status: "in_flight" });
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
    fetch: async () => grantResponse(1, null),
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

test("start-over replaces the visitor, and a failed exchange keeps the old one", async () => {
  const values = new Map<string, string>();
  const exchangeBodies: Array<Record<string, unknown>> = [];
  const stream = sseChannel();
  let exchanges = 0;
  let streamOpens = 0;
  let refuseExchange = false;
  const controller = createAnonymousConversation({
    baseUrl: "https://reset.example.test",
    appId: "app_reset",
    storage: mapAdapter(values),
    clock: new FakeClock(),
    fetch: async (input, init) => {
      const url = requestURL(input);
      if (url.pathname.endsWith("/anonymous-tokens")) {
        if (refuseExchange) {
          return Response.json({ message: "no", code: "forbidden" }, { status: 403 });
        }
        exchanges += 1;
        exchangeBodies.push(JSON.parse(String(init?.body)) as Record<string, unknown>);
        return grantResponse(exchanges, exchanges === 1 ? CONVERSATION_ID : null);
      }
      if (url.pathname.endsWith("/transcript")) return transcriptResponse(url);
      if (url.pathname.endsWith("/stream")) {
        streamOpens += 1;
        return stream.respond(init?.signal);
      }
      throw new Error(`unexpected request ${init?.method} ${url.pathname}`);
    },
  });
  try {
    await waitFor(controller, (snapshot) => snapshot.connection.status === "connected");
    assert.equal([...values.values()][0].includes("visitor-1"), true);

    // A refused exchange must not cost the visitor their conversation: the
    // stored token is only overwritten once a replacement exists.
    refuseExchange = true;
    await assert.rejects(() => controller.startOver());
    assert.equal([...values.values()][0].includes("visitor-1"), true);

    // Nor their stream. The one they had is still the one delivering, and
    // the snapshot says so rather than reporting a connection that is gone.
    assert.equal(controller.getSnapshot().connection.status, "connected");
    assert.equal(controller.getSnapshot().startOver.status, "error");
    stream.emit(transcriptUpdate("cursor-after-refusal", wireMessages(1, 1)));
    await waitFor(controller, (snapshot) => snapshot.messages.length === 1);
    assert.equal(streamOpens, 1, "a failed start-over neither drops nor reopens the stream");

    refuseExchange = false;
    await controller.startOver();
    assert.equal(exchanges, 2);
    assert.deepEqual(exchangeBodies[1], {}, "start-over does not send the prior visitor token");
    assert.equal(controller.getSnapshot().conversationId, null);
    assert.equal([...values.values()][0].includes("visitor-2"), true);
  } finally {
    controller.destroy();
  }
});

test("older history never moves the live cursor and stops at the 500-message window", async () => {
  const streamCursors: string[] = [];
  // 600 messages exist. The window holds 500, so the tenth page is the one
  // that would push it over.
  const all = wireMessages(1, 600);
  const client = createBrowserClient({
    baseUrl: "https://history.example.test",
    clientToken: "header.payload.signature",
    retry: { maxAttempts: 1 },
    fetch: async (input, init) => {
      const url = requestURL(input);
      if (url.pathname.endsWith("/transcript")) {
        return transcriptResponse(url, { messages: all });
      }
      if (url.pathname.endsWith("/stream")) {
        streamCursors.push(url.searchParams.get("cursor") ?? "");
        return openSSE(init?.signal);
      }
      throw new Error(`unexpected request ${url.pathname}`);
    },
  });
  const controller = createConversation({ client, conversation: { id: CONVERSATION_ID } });
  try {
    await waitFor(controller, (snapshot) => snapshot.connection.status === "connected");
    assert.equal(controller.getSnapshot().messages.length, 50);
    for (let page = 0; page < 10; page += 1) {
      if (controller.getSnapshot().olderHistory.action.status !== "enabled") break;
      await controller.loadEarlier();
    }
    const snapshot = controller.getSnapshot();
    assert.equal(snapshot.messages.length, 500);
    assert.equal(snapshot.messages[0].sequence, 101);
    assert.equal(snapshot.messages.at(-1)?.sequence, 600);
    assert.equal(snapshot.olderHistory.status, "window_full");
    assert.deepEqual(streamCursors, ["cursor-tail"]);
    assert.equal(Object.isFrozen(snapshot.messages), true);
    assert.equal(Object.isFrozen(snapshot.messages[0].content), true);
  } finally {
    controller.destroy();
  }
});

test("the wire read is bounded on bootstrap and on every older page", async () => {
  const transcriptQueries: string[] = [];
  const streamCursors: string[] = [];
  const all = wireMessages(1, 180);
  const client = createBrowserClient({
    baseUrl: "https://bounded.example.test",
    clientToken: "header.payload.signature",
    retry: { maxAttempts: 1 },
    fetch: async (input, init) => {
      const url = requestURL(input);
      if (url.pathname.endsWith("/transcript")) {
        transcriptQueries.push(url.search);
        return transcriptResponse(url, { messages: all });
      }
      if (url.pathname.endsWith("/stream")) {
        streamCursors.push(url.searchParams.get("cursor") ?? "");
        return openSSE(init?.signal);
      }
      throw new Error(`unexpected request ${url.pathname}`);
    },
  });
  const controller = createConversation({ client, conversation: { id: CONVERSATION_ID } });
  try {
    await waitFor(controller, (snapshot) => snapshot.connection.status === "connected");

    // Bootstrap asks for a window, not the transcript.
    assert.equal(transcriptQueries.length, 1);
    const bootstrap = new URLSearchParams(transcriptQueries[0]);
    assert.equal(bootstrap.get("limit"), "50");
    assert.equal(bootstrap.has("page_token"), false);
    assert.equal(controller.getSnapshot().messages.length, 50);
    assert.equal(controller.getSnapshot().messages[0].sequence, 131);

    // Each older page carries the previous response's token and its own bound.
    await controller.loadEarlier();
    await controller.loadEarlier();
    assert.equal(transcriptQueries.length, 3);
    for (const search of transcriptQueries.slice(1)) {
      const query = new URLSearchParams(search);
      assert.equal(query.get("limit"), "50");
      assert.notEqual(query.get("page_token"), null);
    }
    // A token names a window, and the walk never re-reads one it already had.
    const tokens = transcriptQueries.slice(1).map((search) =>
      new URLSearchParams(search).get("page_token"));
    assert.equal(new Set(tokens).size, tokens.length);

    // No read ever asked for the whole transcript.
    assert.equal(
      transcriptQueries.every((search) => new URLSearchParams(search).has("limit")),
      true,
      "every transcript read was bounded on the wire",
    );

    const snapshot = controller.getSnapshot();
    assert.equal(snapshot.messages.length, 150);
    assert.equal(snapshot.messages[0].sequence, 31);
    assert.equal(snapshot.messages.at(-1)?.sequence, 180);
    // Every page reported the cut's cursor, so the stream was opened once and
    // paging older history never restarted it.
    assert.deepEqual(streamCursors, ["cursor-tail"]);
  } finally {
    controller.destroy();
  }
});

test("a walk to the beginning ends with no continuation and no lost message", async () => {
  const all = wireMessages(1, 120);
  const client = createBrowserClient({
    baseUrl: "https://walk.example.test",
    clientToken: "header.payload.signature",
    retry: { maxAttempts: 1 },
    fetch: async (input, init) => {
      const url = requestURL(input);
      if (url.pathname.endsWith("/transcript")) {
        return transcriptResponse(url, { messages: all });
      }
      if (url.pathname.endsWith("/stream")) return openSSE(init?.signal);
      throw new Error(`unexpected request ${url.pathname}`);
    },
  });
  const controller = createConversation({ client, conversation: { id: CONVERSATION_ID } });
  try {
    await waitFor(controller, (snapshot) => snapshot.connection.status === "connected");
    while (controller.getSnapshot().olderHistory.action.status === "enabled") {
      await controller.loadEarlier();
    }
    const snapshot = controller.getSnapshot();
    assert.equal(snapshot.messages.length, 120);
    assert.deepEqual(
      snapshot.messages.map((message) => message.sequence),
      all.map((message) => Number(message.sequence)),
    );
    // The service said there was no continuation, so the control retires
    // rather than offering a page that would come back empty.
    assert.equal(snapshot.olderHistory.status, "ready");
    assert.deepEqual(snapshot.olderHistory.action, {
      status: "disabled",
      reason: "no_earlier_history",
    });
  } finally {
    controller.destroy();
  }
});

test("a controller with no Conversation selection is refused at construction", () => {
  const client = createBrowserClient({
    baseUrl: "https://required.example.test",
    clientToken: "header.payload.signature",
    fetch: async () => { throw new Error("no request expected"); },
  });
  assert.throws(
    () => createConversation({ client, conversation: undefined as never }),
    (error: unknown) => error instanceof NvokenError && error.category === "validation",
  );
});

test("destroy is silent and final", async () => {
  const client = createBrowserClient({
    baseUrl: "https://destroy.example.test",
    clientToken: "header.payload.signature",
    fetch: async () => { throw new Error("no request expected"); },
  });
  const controller = createConversation({
    client,
    conversation: { key: "support", ownedByUser: "visitor-1" },
  });
  let notifications = 0;
  controller.subscribe(() => { notifications += 1; });
  controller.destroy();
  const afterDestroy = notifications;
  controller.destroy();
  await Promise.resolve();
  assert.equal(notifications, afterDestroy);
  assert.equal(controller.getSnapshot().recovery.status, "destroyed");
});

test("reconnect after a failed bootstrap reads the transcript it never got", async () => {
  let transcriptReads = 0;
  const client = createBrowserClient({
    baseUrl: "https://exhausted.example.test",
    clientToken: "header.payload.signature",
    retry: { maxAttempts: 1 },
    fetch: async (input, init) => {
      const url = requestURL(input);
      if (url.pathname.endsWith("/transcript")) {
        transcriptReads += 1;
        if (transcriptReads === 1) {
          return Response.json({ message: "later", code: "unavailable" }, { status: 503 });
        }
        return transcriptResponse(url, { messages: wireMessages(1, 2) });
      }
      if (url.pathname.endsWith("/stream")) return openSSE(init?.signal);
      throw new Error(`unexpected request ${url.pathname}`);
    },
  });
  const controller = createConversation({ client, conversation: { id: CONVERSATION_ID } });
  try {
    const failed = await waitFor(controller, (snapshot) => snapshot.connection.status === "error");
    assert.equal(failed.recovery.status, "connection_exhausted");
    assert.equal(failed.reconnect.status, "enabled");

    // The snapshot said reconnect was enabled, so it has to work: there is no
    // stream position to resume from yet, and the read is what supplies one.
    await controller.reconnect();
    const recovered = await waitFor(
      controller,
      (snapshot) => snapshot.connection.status === "connected",
    );
    assert.equal(transcriptReads, 2);
    assert.deepEqual(recovered.messages.map((message) => message.sequence), [1, 2]);
    assert.equal(recovered.recovery.status, "none");
  } finally {
    controller.destroy();
  }
});

test("a stream rejected for authorization comes back when the anonymous grant renews", async () => {
  const clock = new FakeClock();
  let exchanges = 0;
  let streamOpens = 0;
  const controller = createAnonymousConversation({
    baseUrl: "https://renew-stream.example.test",
    appId: "app_renew_stream",
    storage: "memory",
    clock,
    fetch: async (input, init) => {
      const url = requestURL(input);
      if (url.pathname.endsWith("/anonymous-tokens")) {
        exchanges += 1;
        return grantResponse(exchanges, CONVERSATION_ID);
      }
      if (url.pathname.endsWith("/transcript")) return transcriptResponse(url);
      if (url.pathname.endsWith("/stream")) {
        streamOpens += 1;
        if (streamOpens === 1) {
          return Response.json({ message: "expired", code: "invalid_token" }, { status: 401 });
        }
        return openSSE(init?.signal);
      }
      throw new Error(`unexpected request ${url.pathname}`);
    },
  });
  try {
    const lost = await waitFor(controller, (snapshot) => snapshot.authorization.status === "lost");
    // No stream is running, and the snapshot has to say so rather than keep
    // reporting the connection it had before the token was refused.
    assert.equal(lost.connection.status, "error");
    assert.deepEqual(lost.reconnect, { status: "disabled", reason: "authorization_required" });
    assert.equal(lost.retryAuthorization.status, "enabled");

    // The grant renews on its own timer. Nobody presses anything.
    clock.runNext();
    const recovered = await waitFor(
      controller,
      (snapshot) => snapshot.connection.status === "connected",
    );
    assert.equal(exchanges, 2);
    assert.equal(streamOpens, 2);
    assert.equal(recovered.authorization.status, "ready");
    assert.equal(recovered.recovery.status, "none");
    assert.equal(recovered.conversationId, CONVERSATION_ID);
  } finally {
    controller.destroy();
  }
});

test("an interrupt that finds no Turn leaves the Conversation in place", async () => {
  let transcriptReads = 0;
  const client = createBrowserClient({
    baseUrl: "https://gone-turn.example.test",
    clientToken: "header.payload.signature",
    retry: { maxAttempts: 1 },
    fetch: async (input, init) => {
      const url = requestURL(input);
      if (url.pathname.endsWith("/transcript")) {
        transcriptReads += 1;
        // The first read finds a Turn mid-flight; by the time the stop
        // button is pressed the runtime no longer has it.
        return transcriptResponse(url, transcriptReads === 1 ? {
          conversation: wireConversation({
            active_turn_id: TURN_ID,
            active_turn_status: "running",
          }),
          messages: wireMessages(1, 3),
        } : { messages: wireMessages(1, 3) });
      }
      if (url.pathname.endsWith("/stream")) return openSSE(init?.signal);
      if (url.pathname === `/v1/turns/${TURN_ID}/interrupt`) {
        return Response.json({ message: "no such Turn", code: "not_found" }, { status: 404 });
      }
      throw new Error(`unexpected request ${url.pathname}`);
    },
  });
  const controller = createConversation({ client, conversation: { id: CONVERSATION_ID } });
  try {
    await waitFor(controller, (snapshot) => snapshot.activity.status === "active");
    await assert.rejects(
      () => controller.interrupt(),
      (error: unknown) => error instanceof NvokenError && error.category === "not_found",
    );
    const snapshot = controller.getSnapshot();
    // A 404 on the Turn is not a missing Conversation: the transcript, the
    // id, and the stream all stay.
    assert.equal(snapshot.conversationId, CONVERSATION_ID);
    assert.equal(snapshot.messages.length, 3);
    assert.equal(snapshot.recovery.status, "none");
    assert.equal(snapshot.connection.status, "connected");
    assert.equal(snapshot.interruption.status, "error");
    // The Turn that was holding the composer closed is gone, and the re-read
    // is what lets the composer open again.
    assert.equal(transcriptReads, 2);
    assert.equal(snapshot.activity.status, "idle");
    assert.equal(snapshot.send.action.status, "enabled");
  } finally {
    controller.destroy();
  }
});

test("collections keep their identity across frames that do not change them", async () => {
  const stream = sseChannel();
  const messageId = "00000000-0000-7000-8000-000000000001";
  const client = createBrowserClient({
    baseUrl: "https://identity.example.test",
    clientToken: "header.payload.signature",
    retry: { maxAttempts: 1 },
    fetch: async (input, init) => {
      const url = requestURL(input);
      if (url.pathname.endsWith("/transcript")) {
        return transcriptResponse(url, { messages: wireMessages(1, 2) });
      }
      if (url.pathname.endsWith("/stream")) return stream.respond(init?.signal);
      throw new Error(`unexpected request ${url.pathname}`);
    },
  });
  const controller = createConversation({ client, conversation: { id: CONVERSATION_ID } });
  try {
    const connected = await waitFor(
      controller,
      (snapshot) => snapshot.connection.status === "connected",
    );

    // A streamed token changes the preview and nothing else. A renderer that
    // memoizes on `messages` must not redraw the transcript for it.
    stream.emit(messageDelta(TURN_ID, messageId, "Hel"));
    const previewed = await waitFor(controller, (snapshot) => snapshot.previews.length === 1);
    assert.notStrictEqual(previewed, connected);
    assert.strictEqual(previewed.messages, connected.messages);
    assert.strictEqual(previewed.lifecycles, connected.lifecycles);
    assert.notStrictEqual(previewed.previews, connected.previews);
    assert.equal(Object.isFrozen(previewed.previews), true);

    // A saved message replaces both: the transcript grew, and the preview it
    // supersedes is discarded.
    stream.emit(transcriptUpdate("cursor-3", [
      { ...wireMessages(3, 3)[0], id: messageId, turn_id: TURN_ID },
    ]));
    const saved = await waitFor(controller, (snapshot) => snapshot.messages.length === 3);
    assert.notStrictEqual(saved.messages, previewed.messages);
    assert.strictEqual(saved.lifecycles, previewed.lifecycles);
    assert.equal(saved.previews.length, 0);
    assert.equal(Object.isFrozen(saved.messages[2]), true);
  } finally {
    controller.destroy();
  }
});

function requestURL(input: RequestInfo | URL): URL {
  return new URL(typeof input === "string" ? input : input instanceof URL ? input : input.url);
}

function wireConversation(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    id: CONVERSATION_ID,
    tenant_key: "acme",
    user_key: null,
    conversation_key: null,
    owner: { kind: "tenant", tenant_key: "acme" },
    agent_id: null,
    active_turn_id: null,
    active_turn_status: null,
    message_count: 0,
    retention: null,
    compaction: null,
    metadata: null,
    created_at: NOW,
    updated_at: NOW,
    ...overrides,
  };
}

/**
 * The transcript endpoint as the service implements it.
 *
 * `limit` selects the newest window at one committed cut; `page_token` returns
 * the window before it at that same cut, which the token carries. `has_more`
 * and `next_page_token` are decided here, so a controller that trimmed
 * client-side would be visibly reading more than it asked for. Absent both
 * parameters the whole transcript comes back with `has_more` false, which is
 * what the operation did before it gained a bound.
 */
function transcriptResponse(url: URL, overrides: Record<string, unknown> = {}): Response {
  const { messages: seededMessages, cursor: seededCursor, ...rest } = overrides;
  const all = (seededMessages as Record<string, unknown>[] | undefined) ?? [];
  const headCursor = (seededCursor as string | undefined) ?? "cursor-tail";
  const limitParameter = url.searchParams.get("limit");
  const pageToken = url.searchParams.get("page_token");
  const page = pageToken === null ? null : decodeTranscriptPageToken(pageToken);
  const limit = limitParameter === null
    ? (page === null ? Number.POSITIVE_INFINITY : 100)
    : Number(limitParameter);
  // The cut is fixed when the walk opens, so a later page reports the cursor
  // the first page did rather than the head as it now stands.
  const cursor = page?.cursor ?? headCursor;
  const through = page?.through ?? Number.POSITIVE_INFINITY;
  const eligible = all.filter((message) => Number(message.sequence) <= through);
  const messages = Number.isFinite(limit) ? eligible.slice(-limit) : eligible;
  const hasMore = messages.length < eligible.length;
  return Response.json({
    conversation: wireConversation(),
    messages,
    compactions: [],
    cursor,
    has_more: hasMore,
    next_page_token: hasMore
      ? encodeTranscriptPageToken({ through: Number(messages[0]!.sequence) - 1, cursor })
      : null,
    captured_at: NOW,
    ...rest,
  });
}

function encodeTranscriptPageToken(token: { through: number; cursor: string }): string {
  return Buffer.from(JSON.stringify(token), "utf8").toString("base64url");
}

function decodeTranscriptPageToken(token: string): { through: number; cursor: string } {
  return JSON.parse(Buffer.from(token, "base64url").toString("utf8"));
}

function wireTurn(status: "queued" | "running" | "completed" = "queued"): Record<string, unknown> {
  return {
    id: TURN_ID,
    tenant_key: "acme",
    behavior_source: { kind: "inline", digest: "sha256:inline" },
    conversation_id: CONVERSATION_ID,
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

function admissionResponse(): Response {
  return Response.json({ ...wireTurn(), deduplicated: false }, { status: 202 });
}

function grantResponse(
  index: number,
  conversationId: string | null,
  expiresInSeconds = 900,
): Response {
  return Response.json({
    access_token: `access-${index}`,
    access_token_expires_in_seconds: expiresInSeconds,
    visitor_token: `visitor-${index}`,
    visitor_token_expires_at: "2030-01-01T00:00:00Z",
    conversation_id: conversationId,
  }, { status: 201 });
}

function openSSE(signal?: AbortSignal | null): Response {
  const encoder = new TextEncoder();
  return new Response(new ReadableStream<Uint8Array>({
    start(controller) {
      controller.enqueue(encoder.encode("retry: 1000\n\n"));
      signal?.addEventListener("abort", () => {
        try { controller.close(); } catch { /* already closed */ }
      }, { once: true });
    },
  }), { headers: { "content-type": "text/event-stream" } });
}

interface WireEvent {
  id?: string;
  event: string;
  data: Record<string, unknown>;
}

/** An SSE response a test can keep writing to after the controller opened it. */
function sseChannel(): {
  respond(signal?: AbortSignal | null): Response;
  emit(event: WireEvent): void;
} {
  const encoder = new TextEncoder();
  let sink: ReadableStreamDefaultController<Uint8Array> | undefined;
  return {
    respond(signal) {
      return new Response(new ReadableStream<Uint8Array>({
        start(controller) {
          sink = controller;
          controller.enqueue(encoder.encode("retry: 1000\n\n"));
          signal?.addEventListener("abort", () => {
            try { controller.close(); } catch { /* already closed */ }
            if (sink === controller) sink = undefined;
          }, { once: true });
        },
      }), { headers: { "content-type": "text/event-stream" } });
    },
    emit(event) {
      if (!sink) throw new Error("no stream is open");
      sink.enqueue(encoder.encode(
        `${event.id === undefined ? "" : `id: ${event.id}\n`}`
        + `event: ${event.event}\n`
        + `data: ${JSON.stringify(event.data)}\n\n`,
      ));
    },
  };
}

function transcriptUpdate(cursor: string, messages: Record<string, unknown>[]): WireEvent {
  return {
    id: cursor,
    event: "transcript.update",
    data: { type: "transcript.update", messages, turn_changes: [], has_more: false, cursor },
  };
}

function messageDelta(turnId: string, messageId: string, fragment: string): WireEvent {
  return {
    event: "message.delta",
    data: {
      type: "message.delta",
      turn_id: turnId,
      attempt: 1,
      message_id: messageId,
      content_index: 0,
      offset: 0,
      kind: "text",
      delta: fragment,
      emitted_at: NOW,
    },
  };
}

function wireMessages(start: number, end: number): Record<string, unknown>[] {
  const messages: Record<string, unknown>[] = [];
  for (let sequence = start; sequence <= end; sequence += 1) {
    messages.push({
      id: `00000000-0000-7000-8000-${String(sequence).padStart(12, "0")}`,
      conversation_id: CONVERSATION_ID,
      agent_id: null,
      turn_id: null,
      content_expires_at: null,
      user_key: null,
      sequence,
      role: "assistant",
      content: [{ type: "text", text: `message ${sequence}` }],
      created_at: NOW,
    });
  }
  return messages;
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
      reject(new Error(
        `controller did not reach expected state: ${JSON.stringify(controller.getSnapshot())}`,
      ));
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
