// ABOUTME: Verifies browser chat presentation and controller wiring.
// ABOUTME: Uses a small DOM boundary fake so page behavior runs under Node.
import assert from "node:assert/strict";
import test from "node:test";

import type {
  BrowserClient,
  BrowserClientOptions,
  ConversationController,
  ConversationSnapshot,
  CreateConversationOptions,
} from "@deepnoodle/nvoken/browser";

import {
  installPageLifecycle,
  mountConversationPage,
  renderTranscript,
  startBrowserChat,
} from "./page.js";

class FakeElement {
  readonly children: FakeElement[] = [];
  className = "";
  disabled = false;
  hidden = false;
  value = "";
  private content = "";
  private readonly listeners = new Map<string, Set<(event: Event) => void>>();

  get textContent(): string {
    return this.content;
  }

  set textContent(value: string | null) {
    this.content = value ?? "";
    this.children.length = 0;
  }

  set innerHTML(_value: string) {
    throw new Error("unsafe HTML rendering");
  }

  append(...children: FakeElement[]): void {
    this.children.push(...children);
  }

  replaceChildren(...children: FakeElement[]): void {
    this.children.splice(0, this.children.length, ...children);
  }

  addEventListener(type: string, listener: (event: Event) => void): void {
    const listeners = this.listeners.get(type) ?? new Set();
    listeners.add(listener);
    this.listeners.set(type, listeners);
  }

  removeEventListener(type: string, listener: (event: Event) => void): void {
    this.listeners.get(type)?.delete(listener);
  }

  dispatch(type: string): void {
    const event = { preventDefault() {} } as Event;
    for (const listener of this.listeners.get(type) ?? []) listener(event);
  }
}

class FakeDocument {
  private readonly elements = new Map<string, FakeElement>();

  createElement(): FakeElement {
    return new FakeElement();
  }

  add(id: string): FakeElement {
    const element = new FakeElement();
    this.elements.set(id, element);
    return element;
  }

  getElementById(id: string): FakeElement | null {
    return this.elements.get(id) ?? null;
  }
}

test("transcript renders durable messages and live previews as text", () => {
  const transcript = new FakeElement();
  const snapshot = snapshotWith({
    messages: [{
      id: "message-1",
      conversationId: "conversation-1",
      contentExpiresAt: null,
      turnId: "turn-1",
      sequence: 1,
      role: "user",
      content: [{ type: "text", text: "<img src=x onerror=alert(1)>" }],
      createdAt: new Date("2026-01-01T00:00:00Z"),
    }],
    previews: [{
      turnId: "turn-2",
      attempt: 1,
      messageId: "message-2",
      contentIndex: 0,
      kind: "text",
      delta: "Reply in progress",
    }],
  });

  renderTranscript(
    new FakeDocument() as unknown as Document,
    transcript as unknown as HTMLElement,
    snapshot,
  );

  assert.equal(transcript.children.length, 2);
  assert.equal(transcript.children[0]?.children[0]?.textContent, "You");
  assert.equal(
    transcript.children[0]?.children[1]?.textContent,
    "<img src=x onerror=alert(1)>",
  );
  assert.equal(transcript.children[1]?.children[0]?.textContent, "Assistant");
  assert.equal(transcript.children[1]?.children[1]?.textContent, "Reply in progress");
});

test("transcript labels protocol roles accurately and omits thinking previews", () => {
  const transcript = new FakeElement();
  const snapshot = snapshotWith({
    messages: [
      message("message-system", 1, "system", "System context"),
      message("message-tool", 2, "tool", "Tool output"),
    ],
    previews: [
      {
        turnId: "turn-preview",
        attempt: 1,
        messageId: "message-preview",
        contentIndex: 0,
        kind: "thinking",
        delta: "Private reasoning",
      },
      {
        turnId: "turn-preview",
        attempt: 1,
        messageId: "message-preview",
        contentIndex: 1,
        kind: "text",
        delta: "Visible answer",
      },
    ],
  });

  renderTranscript(
    new FakeDocument() as unknown as Document,
    transcript as unknown as HTMLElement,
    snapshot,
  );

  assert.equal(transcript.children[0]?.children[0]?.textContent, "System");
  assert.equal(transcript.children[1]?.children[0]?.textContent, "Tool");
  assert.equal(transcript.children[2]?.children.length, 2);
  assert.equal(transcript.children[2]?.children[1]?.textContent, "Visible answer");
});

test("page wiring sends composer text and follows controller action state", async () => {
  const document = new FakeDocument();
  const transcript = document.add("transcript");
  const composer = document.add("composer");
  const input = document.add("message");
  const sendButton = document.add("send");
  const stopButton = document.add("stop");
  document.add("retry-send");
  document.add("discard-send");
  document.add("reconnect");
  const status = document.add("status");
  document.add("error");
  let current = snapshotWith({});
  let listener: () => void = () => undefined;
  const sent: string[] = [];
  let interrupts = 0;
  const controller = {
    getSnapshot: () => current,
    subscribe: (next: () => void) => {
      listener = next;
      return () => undefined;
    },
    send: async (text: string) => {
      sent.push(text);
      return { turnId: "turn-2", conversationId: "conversation-1", deduplicated: false };
    },
    interrupt: async () => {
      interrupts += 1;
    },
    destroy: () => undefined,
  } as unknown as ConversationController;

  const unmount = mountConversationPage(
    document as unknown as Document,
    controller,
  );
  assert.equal(status.textContent, "Ready");
  assert.equal(sendButton.disabled, false);
  assert.equal(stopButton.hidden, true);
  assert.equal(transcript.children.length, 0);

  input.value = "  Hello  ";
  composer.dispatch("submit");
  await Promise.resolve();
  assert.deepEqual(sent, ["Hello"]);
  assert.equal(input.value, "");

  current = snapshotWith({
    activity: { status: "active", turnId: "turn-2", turnStatus: "running" },
    send: {
      status: "idle",
      action: { status: "disabled", reason: "conversation_active" },
    },
    interruption: { status: "idle", action: { status: "enabled" } },
  });
  listener();
  assert.equal(status.textContent, "Working");
  assert.equal(sendButton.disabled, true);
  assert.equal(stopButton.hidden, false);

  stopButton.dispatch("click");
  await Promise.resolve();
  assert.equal(interrupts, 1);
  unmount();
});

test("page exposes snapshot errors and reconnects only when the action is enabled", async () => {
  const document = new FakeDocument();
  document.add("transcript");
  document.add("composer");
  document.add("message");
  document.add("send");
  document.add("stop");
  document.add("retry-send");
  document.add("discard-send");
  const reconnectButton = document.add("reconnect");
  document.add("status");
  const error = document.add("error");
  let reconnects = 0;
  const current = snapshotWith({
    connection: {
      status: "error",
      error: new Error("stream unavailable") as never,
    },
    reconnect: { status: "enabled" },
  });
  const controller = {
    getSnapshot: () => current,
    subscribe: () => () => undefined,
    reconnect: async () => {
      reconnects += 1;
    },
  } as unknown as ConversationController;

  const unmount = mountConversationPage(
    document as unknown as Document,
    controller,
  );

  assert.equal(error.hidden, false);
  assert.equal(error.textContent, "stream unavailable");
  assert.equal(reconnectButton.hidden, false);
  assert.equal(reconnectButton.disabled, false);
  reconnectButton.dispatch("click");
  await Promise.resolve();
  assert.equal(reconnects, 1);
  unmount();
});

test("browser bootstrap scopes one conversation controller from host supplied config", async () => {
  const document = new FakeDocument();
  for (const id of [
    "transcript",
    "composer",
    "message",
    "send",
    "stop",
    "retry-send",
    "discard-send",
    "reconnect",
    "status",
    "error",
  ]) document.add(id);
  const controller = {
    getSnapshot: () => snapshotWith({}),
    subscribe: () => () => undefined,
    destroy: () => undefined,
  } as unknown as ConversationController;
  let clientBaseUrl = "";
  let resolveToken: (() => string | Promise<string>) | undefined;
  let selectedConversation = "";
  let tokenRequests = 0;

  await startBrowserChat({
    document: document as unknown as Document,
    fetch: (async (_input: RequestInfo | URL, init?: RequestInit) => {
      tokenRequests += 1;
      assert.equal(init?.method, "POST");
      return Response.json({
        token: `token-${tokenRequests}`,
        baseUrl: "https://runtime.example.test",
        conversationId: "conversation-1",
      });
    }) as typeof fetch,
    createClient: (options: BrowserClientOptions) => {
      clientBaseUrl = options.baseUrl;
      assert.equal(typeof options.clientToken, "function");
      resolveToken = options.clientToken as () => string | Promise<string>;
      return {} as BrowserClient;
    },
    createController: (options: CreateConversationOptions) => {
      selectedConversation = options.conversation.id ?? "";
      return controller;
    },
  });

  assert.equal(clientBaseUrl, "https://runtime.example.test");
  assert.equal(selectedConversation, "conversation-1");
  assert.equal(await resolveToken?.(), "token-1");
  assert.equal(await resolveToken?.(), "token-2");
  assert.equal(tokenRequests, 2);
});

test("successful uncertain send retry clears the original composer input", async () => {
  const document = new FakeDocument();
  for (const id of [
    "transcript",
    "composer",
    "message",
    "send",
    "stop",
    "retry-send",
    "discard-send",
    "reconnect",
    "status",
    "error",
  ]) document.add(id);
  const composer = document.getElementById("composer")!;
  const input = document.getElementById("message")!;
  const sendButton = document.getElementById("send")!;
  const retryButton = document.getElementById("retry-send")!;
  const discardButton = document.getElementById("discard-send")!;
  let sends = 0;
  let retries = 0;
  let render: () => void = () => undefined;
  let resolveRetry!: (receipt: {
    turnId: string;
    conversationId: string;
    deduplicated: boolean;
  }) => void;
  const retryReceipt = new Promise<{
    turnId: string;
    conversationId: string;
    deduplicated: boolean;
  }>((resolve) => {
    resolveRetry = resolve;
  });
  let snapshot: ConversationSnapshot = snapshotWith({
    send: {
      status: "uncertain",
      action: { status: "enabled" },
      idempotencyKey: "send-key",
      error: new Error("admission outcome unknown") as never,
    },
    discardSend: { status: "enabled" },
  });
  const controller = {
    getSnapshot: () => snapshot,
    subscribe: (listener: () => void) => {
      render = listener;
      return () => undefined;
    },
    send: async () => {
      sends += 1;
      return { turnId: "turn-new", conversationId: "conversation-1", deduplicated: false };
    },
    retrySend: () => {
      retries += 1;
      snapshot = snapshotWith({
        activity: { status: "admitting" },
        send: {
          status: "admitting",
          action: { status: "in_flight" },
          idempotencyKey: "send-key",
        },
        discardSend: { status: "disabled", reason: "operation_in_flight" },
      });
      render();
      return retryReceipt;
    },
  } as unknown as ConversationController;

  const unmount = mountConversationPage(
    document as unknown as Document,
    controller,
  );
  input.value = "original input";

  assert.equal(sendButton.disabled, true);
  assert.equal(retryButton.hidden, false);
  assert.equal(retryButton.disabled, false);
  assert.equal(discardButton.hidden, false);
  assert.equal(discardButton.disabled, false);

  composer.dispatch("submit");
  retryButton.dispatch("click");

  assert.equal(sends, 0);
  assert.equal(retries, 1);
  assert.equal(input.value, "original input");

  snapshot = snapshotWith({});
  render();
  resolveRetry({
    turnId: "turn-original",
    conversationId: "conversation-1",
    deduplicated: true,
  });
  await retryReceipt;

  assert.equal(input.value, "");
  unmount();
});

test("discarding an uncertain send keeps the composer input for a new send", () => {
  const document = new FakeDocument();
  for (const id of [
    "transcript",
    "composer",
    "message",
    "send",
    "stop",
    "retry-send",
    "discard-send",
    "reconnect",
    "status",
    "error",
  ]) document.add(id);
  const input = document.getElementById("message")!;
  const discardButton = document.getElementById("discard-send")!;
  let discards = 0;
  let render: () => void = () => undefined;
  let snapshot: ConversationSnapshot = snapshotWith({
    send: {
      status: "uncertain",
      action: { status: "enabled" },
      idempotencyKey: "send-key",
      error: new Error("admission outcome unknown") as never,
    },
    discardSend: { status: "enabled" },
  });
  const controller = {
    getSnapshot: () => snapshot,
    subscribe: (listener: () => void) => {
      render = listener;
      return () => undefined;
    },
    discardSend: () => {
      discards += 1;
      snapshot = snapshotWith({});
      render();
    },
  } as unknown as ConversationController;

  const unmount = mountConversationPage(
    document as unknown as Document,
    controller,
  );
  input.value = "original input";

  assert.equal(discardButton.hidden, false);
  assert.equal(discardButton.disabled, false);
  discardButton.dispatch("click");

  assert.equal(discards, 1);
  assert.equal(input.value, "original input");
  assert.equal(discardButton.hidden, true);
  unmount();
});

test("persisted pagehide preserves the controller until a final page exit", () => {
  const window = new EventTarget();
  let destroys = 0;
  installPageLifecycle(window as unknown as Window, () => {
    destroys += 1;
  });
  const cached = new Event("pagehide");
  Object.defineProperty(cached, "persisted", { value: true });
  window.dispatchEvent(cached);
  assert.equal(destroys, 0);

  const final = new Event("pagehide");
  Object.defineProperty(final, "persisted", { value: false });
  window.dispatchEvent(final);
  window.dispatchEvent(final);

  assert.equal(destroys, 1);
});

function snapshotWith(
  overrides: Partial<ConversationSnapshot>,
): ConversationSnapshot {
  return {
    revision: 1,
    mode: "host",
    conversationId: "conversation-1",
    messages: [],
    lifecycles: [],
    previews: [],
    authorization: { status: "ready", continuity: "memory" },
    connection: { status: "connected" },
    activity: { status: "idle" },
    send: { status: "idle", action: { status: "enabled" } },
    interruption: {
      status: "idle",
      action: { status: "disabled", reason: "no_turn" },
    },
    olderHistory: {
      status: "ready",
      action: { status: "disabled", reason: "no_earlier_history" },
    },
    startOver: {
      status: "idle",
      action: { status: "disabled", reason: "host_owned" },
    },
    retryAuthorization: { status: "disabled", reason: "nothing_to_recover" },
    reconnect: { status: "disabled", reason: "nothing_to_recover" },
    discardSend: { status: "disabled", reason: "no_pending_send" },
    recovery: { status: "none" },
    ...overrides,
  };
}

function message(
  id: string,
  sequence: number,
  role: "system" | "user" | "assistant" | "tool",
  text: string,
): ConversationSnapshot["messages"][number] {
  return {
    id,
    conversationId: "conversation-1",
    contentExpiresAt: null,
    turnId: "turn-1",
    sequence,
    role,
    content: [{ type: "text", text }],
    createdAt: new Date("2026-01-01T00:00:00Z"),
  };
}
