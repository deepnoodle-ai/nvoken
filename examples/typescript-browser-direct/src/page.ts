/**
 * The browser half. It holds a short-lived client token and no machine key.
 */
import {
  createBrowserClient,
  createConversation,
  type ConversationController,
} from "@deepnoodle/nvoken/browser";

async function currentToken(): Promise<string> {
  const response = await fetch("/api/nvoken-token", { method: "POST" });
  if (!response.ok) throw new Error("could not obtain an nvoken client token");
  return ((await response.json()) as { token: string }).token;
}

const client = createBrowserClient({
  baseUrl: "https://api.nvoken.com",
  clientToken: currentToken,
});

/** Admit one Turn and render reduced, authoritative updates. */
export async function send(conversationId: string, text: string): Promise<string> {
  const turn = await client.start(text, {
    conversation: { id: conversationId },
  });

  let answer = "";
  for await (const update of turn.updates()) {
    if (update.snapshot.text !== null && update.snapshot.text !== answer) {
      answer = update.snapshot.text;
      renderAnswer(answer);
    }
    renderStatus(update.snapshot.status);
  }
  return answer;
}

/** Recover an admitted Turn after a reload, using only its durable ID. */
export async function recover(turnId: string): Promise<string | null> {
  return (await client.turn(turnId).result()).text;
}

/** Stop a running Turn and keep everything it produced. */
export async function stop(turnId: string): Promise<string> {
  return (await client.turn(turnId).interrupt()).status;
}

/**
 * The whole page, as a resumable conversation.
 *
 * Everything above is the low-level path: one Turn at a time, with the page
 * responsible for what happens across a reload. The controller is the same
 * runtime with the resumption already written — one transcript read plus a
 * stream from the position it observed, and a snapshot that says what every
 * control may do right now. Hold one for the life of the page.
 */
export function conversation(conversationId: string): ConversationController {
  const controller = createConversation({
    client,
    conversation: { id: conversationId },
  });
  controller.subscribe(() => {
    const snapshot = controller.getSnapshot();
    renderMessages(snapshot.messages.length);
    renderStatus(snapshot.activity.status);
    // Never guess: the snapshot already says whether the composer is usable.
    renderComposer(snapshot.send.action.status === "enabled");
  });
  return controller;
}

declare function renderAnswer(text: string): void;
declare function renderStatus(status: string): void;
declare function renderMessages(count: number): void;
declare function renderComposer(enabled: boolean): void;
