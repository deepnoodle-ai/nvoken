/**
 * The browser half. It holds a short-lived client token and no machine key.
 */
import { createBrowserClient } from "@deepnoodle/nvoken/browser";

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

/** Read the retained transcript through the exact generated API. */
export async function history(conversationId: string) {
  return client.raw().conversations.listConversationMessages({ conversationId });
}

declare function renderAnswer(text: string): void;
declare function renderStatus(status: string): void;
