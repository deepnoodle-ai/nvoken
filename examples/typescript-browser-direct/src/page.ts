/**
 * The browser half.
 *
 * This code ships to the page. It holds a client token and nothing else: no
 * API key, no signing key, no secret of any kind. Everything it can do is
 * what the token names, and the token names one user, one tenant, one Agent,
 * and one set of operations.
 */
import { createBrowserClient } from "@deepnoodle/nvoken/browser";

/**
 * Fetches a fresh token from this application's own backend.
 *
 * Passed as a function rather than a string because client tokens live at most
 * fifteen minutes. A page open longer than that — which is most of them —
 * needs the next one, and a function is where that happens.
 */
async function currentToken(): Promise<string> {
  const response = await fetch("/api/nvoken-token", { method: "POST" });
  if (!response.ok) throw new Error("could not obtain an nvoken client token");
  const { token } = (await response.json()) as { token: string };
  return token;
}

const client = createBrowserClient({
  baseUrl: "https://api.nvoken.example",
  clientToken: currentToken,
});

/**
 * Sends a message and renders the reply as it arrives.
 *
 * The turn is admitted by the browser and streamed to the browser. No server
 * of this application's is in the path, which is the point: nothing has to
 * stay alive for the length of a turn, and a closed tab costs a turn nothing.
 */
export async function send(sessionId: string, text: string): Promise<void> {
  // No Agent named here. The token names it, along with the tenant, the end
  // user, and the Definition revision, so there is nothing for the page to
  // choose and nothing for it to get wrong.
  const turn = await client.invoke({
    session: { mode: "continue", id: sessionId },
    input: text,
  });

  for await (const event of turn.stream()) {
    if (event.type === "message.delta" && event.kind === "text") {
      appendText(event.delta);
    }
  }
}

/**
 * Reads the conversation back.
 *
 * A reload has no local state to recover: the transcript is in nvoken, and the
 * token authorizes reading exactly this user's copy of it.
 */
export async function history(sessionId: string) {
  return client.listSessionMessages(sessionId, { order: "asc" });
}

declare function appendText(delta: string): void;
