// ABOUTME: Runs a resumable browser chat with a short lived scoped client token.
// ABOUTME: Renders the SDK conversation snapshot with safe DOM text operations.
import {
  createBrowserClient,
  createConversation,
  type BrowserClientOptions,
  type ConversationController,
  type ConversationSnapshot,
  type CreateConversationOptions,
} from "@deepnoodle/nvoken/browser";
import { foldMessages, groupPreviews } from "@deepnoodle/nvoken/transcript";

/** Render saved messages and provisional assistant text without parsing content as HTML. */
export function renderTranscript(
  document: Document,
  transcript: HTMLElement,
  snapshot: ConversationSnapshot,
): void {
  const rows: HTMLElement[] = [];
  for (const rendered of foldMessages([...snapshot.messages])) {
    const row = document.createElement("article");
    row.className = `message message-${rendered.message.role}`;
    const label = document.createElement("strong");
    label.textContent = roleLabel(rendered.message.role);
    row.append(label);
    for (const { block } of rendered.visible) {
      if (block.type !== "text" || typeof block.text !== "string") continue;
      const content = document.createElement("p");
      content.textContent = block.text;
      row.append(content);
    }
    rows.push(row);
  }
  for (const preview of groupPreviews([...snapshot.previews])) {
    const visible = preview.blocks.filter((block) => block.kind === "text");
    if (visible.length === 0) continue;
    const row = document.createElement("article");
    row.className = "message message-assistant message-preview";
    const label = document.createElement("strong");
    label.textContent = "Assistant";
    row.append(label);
    for (const block of visible) {
      const content = document.createElement("p");
      content.textContent = block.text;
      row.append(content);
    }
    rows.push(row);
  }
  transcript.replaceChildren(...rows);
}

function roleLabel(role: string): string {
  switch (role) {
    case "user": return "You";
    case "system": return "System";
    case "tool": return "Tool";
    default: return "Assistant";
  }
}

/** Bind the plain chat controls to one resumable controller. */
export function mountConversationPage(
  document: Document,
  controller: ConversationController,
): () => void {
  const transcript = requiredElement<HTMLElement>(document, "transcript");
  const composer = requiredElement<HTMLFormElement>(document, "composer");
  const input = requiredElement<HTMLTextAreaElement>(document, "message");
  const sendButton = requiredElement<HTMLButtonElement>(document, "send");
  const stopButton = requiredElement<HTMLButtonElement>(document, "stop");
  const retrySendButton = requiredElement<HTMLButtonElement>(document, "retry-send");
  const discardSendButton = requiredElement<HTMLButtonElement>(document, "discard-send");
  const reconnectButton = requiredElement<HTMLButtonElement>(document, "reconnect");
  const status = requiredElement<HTMLElement>(document, "status");
  const error = requiredElement<HTMLElement>(document, "error");

  const render = () => {
    const snapshot = controller.getSnapshot();
    renderTranscript(document, transcript, snapshot);
    sendButton.disabled = snapshot.send.status === "uncertain"
      || snapshot.send.action.status !== "enabled";
    stopButton.disabled = snapshot.interruption.action.status !== "enabled";
    stopButton.hidden = snapshot.interruption.action.status === "disabled"
      && snapshot.interruption.action.reason === "no_turn";
    retrySendButton.hidden = snapshot.send.status !== "uncertain";
    retrySendButton.disabled = snapshot.send.status !== "uncertain"
      || snapshot.send.action.status !== "enabled";
    discardSendButton.hidden = snapshot.send.status !== "uncertain";
    discardSendButton.disabled = snapshot.discardSend.status !== "enabled";
    reconnectButton.disabled = snapshot.reconnect.status !== "enabled";
    reconnectButton.hidden = snapshot.reconnect.status === "disabled";
    status.textContent = statusText(snapshot);
    error.textContent = errorText(snapshot);
    error.hidden = error.textContent === "";
  };
  const submit = (event: Event) => {
    event.preventDefault();
    const text = input.value.trim();
    const send = controller.getSnapshot().send;
    if (!text || send.status === "uncertain" || send.action.status !== "enabled") return;
    void controller.send(text).then(() => {
      input.value = "";
    }, () => undefined);
  };
  const interrupt = () => {
    if (controller.getSnapshot().interruption.action.status !== "enabled") return;
    void controller.interrupt().catch(() => undefined);
  };
  const retrySend = () => {
    const send = controller.getSnapshot().send;
    if (send.status !== "uncertain" || send.action.status !== "enabled") return;
    void controller.retrySend().then(() => {
      input.value = "";
    }, () => undefined);
  };
  const discardSend = () => {
    if (controller.getSnapshot().discardSend.status !== "enabled") return;
    controller.discardSend();
  };
  const reconnect = () => {
    if (controller.getSnapshot().reconnect.status !== "enabled") return;
    void controller.reconnect().catch(() => undefined);
  };

  composer.addEventListener("submit", submit);
  stopButton.addEventListener("click", interrupt);
  retrySendButton.addEventListener("click", retrySend);
  discardSendButton.addEventListener("click", discardSend);
  reconnectButton.addEventListener("click", reconnect);
  const unsubscribe = controller.subscribe(render);
  render();
  return () => {
    unsubscribe();
    composer.removeEventListener("submit", submit);
    stopButton.removeEventListener("click", interrupt);
    retrySendButton.removeEventListener("click", retrySend);
    discardSendButton.removeEventListener("click", discardSend);
    reconnectButton.removeEventListener("click", reconnect);
  };
}

function requiredElement<T extends HTMLElement>(document: Document, id: string): T {
  const element = document.getElementById(id);
  if (!element) throw new Error(`missing page element: ${id}`);
  return element as T;
}

function statusText(snapshot: ConversationSnapshot): string {
  if (snapshot.authorization.status === "authorizing") return "Authorizing";
  if (snapshot.authorization.status === "renewing") return "Renewing access";
  if (snapshot.authorization.status === "lost") return "Authorization required";
  if (snapshot.connection.status === "connecting") return "Connecting";
  if (snapshot.connection.status === "reconnecting") return "Reconnecting";
  if (snapshot.connection.status === "error") return "Connection error";
  if (snapshot.activity.status === "admitting") return "Sending";
  if (snapshot.activity.status === "active" || snapshot.activity.status === "unknown") {
    return "Working";
  }
  return "Ready";
}

function errorText(snapshot: ConversationSnapshot): string {
  if (snapshot.authorization.status === "lost") return snapshot.authorization.error.message;
  if (snapshot.connection.status === "error") return snapshot.connection.error.message;
  if (snapshot.send.status === "uncertain" || snapshot.send.status === "error") {
    return snapshot.send.error.message;
  }
  if (snapshot.interruption.status === "error") return snapshot.interruption.error.message;
  if (snapshot.startOver.status === "error") return snapshot.startOver.error.message;
  if (
    snapshot.recovery.status === "authorization_lost"
    || snapshot.recovery.status === "connection_exhausted"
  ) {
    return snapshot.recovery.error.message;
  }
  return "";
}

interface TokenConfiguration {
  token: string;
  baseUrl: string;
  conversationId: string;
}

export interface BrowserChatOptions {
  document?: Document;
  fetch?: typeof globalThis.fetch;
  createClient?: (options: BrowserClientOptions) => ReturnType<typeof createBrowserClient>;
  createController?: (options: CreateConversationOptions) => ConversationController;
}

/** Destroy only when the document is leaving permanently, not when cached for return. */
export function installPageLifecycle(window: Window, destroy: () => void): () => void {
  const pagehide = (event: PageTransitionEvent) => {
    if (event.persisted) return;
    window.removeEventListener("pagehide", pagehide);
    destroy();
  };
  window.addEventListener("pagehide", pagehide);
  return () => window.removeEventListener("pagehide", pagehide);
}

/** Obtain safe host config and hold one authoritative controller for the page lifetime. */
export async function startBrowserChat(options: BrowserChatOptions = {}): Promise<() => void> {
  const document = options.document ?? globalThis.document;
  const fetch = options.fetch ?? globalThis.fetch;
  const makeClient = options.createClient ?? createBrowserClient;
  const makeController = options.createController ?? createConversation;
  const initial = await requestTokenConfiguration(fetch);
  let primedToken: string | undefined = initial.token;
  const client = makeClient({
    baseUrl: initial.baseUrl,
    clientToken: async () => {
      if (primedToken !== undefined) {
        const token = primedToken;
        primedToken = undefined;
        return token;
      }
      return (await requestTokenConfiguration(fetch)).token;
    },
  });
  const controller = makeController({
    client,
    conversation: { id: initial.conversationId },
  });
  const unmount = mountConversationPage(document, controller);
  return () => {
    unmount();
    controller.destroy();
  };
}

async function requestTokenConfiguration(fetch: typeof globalThis.fetch): Promise<TokenConfiguration> {
  const response = await fetch("/api/nvoken-token", { method: "POST" });
  if (!response.ok) throw new Error("Could not obtain browser access");
  const value = await response.json() as Partial<TokenConfiguration>;
  if (
    typeof value.token !== "string"
    || typeof value.baseUrl !== "string"
    || typeof value.conversationId !== "string"
  ) {
    throw new Error("The browser access response was invalid");
  }
  return value as TokenConfiguration;
}
