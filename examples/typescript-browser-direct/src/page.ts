/**
 * The browser half. It holds a short-lived client token, never a machine key,
 * and calls nvoken directly.
 */
import { createBrowserClient, createConversation } from "@deepnoodle/nvoken/browser";
import { blocksOf } from "@deepnoodle/nvoken/transcript";

interface Access {
  token: string;
  baseUrl: string;
  conversationId: string;
}

async function access(): Promise<Access> {
  const response = await fetch("/token", { method: "POST" });
  if (!response.ok) throw new Error("could not obtain an nvoken client token");
  return response.json();
}

const { baseUrl, conversationId } = await access();
const client = createBrowserClient({
  baseUrl,
  // Called per request, so an expiring token is replaced from the host.
  clientToken: async () => (await access()).token,
});

// The controller reads the transcript, follows any active Turn, and resumes
// after a reload. The page only renders its snapshot.
const chat = createConversation({ client, conversation: { id: conversationId } });

const transcript = document.querySelector("#transcript")!;
const form = document.querySelector("form")!;
const input = form.querySelector("input")!;
const send = form.querySelector<HTMLButtonElement>("#send")!;
const stop = form.querySelector<HTMLButtonElement>("#stop")!;
const error = document.querySelector("#error")!;

chat.subscribe(() => {
  const snapshot = chat.getSnapshot();
  const lines = [
    ...snapshot.messages.flatMap((message) =>
      blocksOf(message)
        .filter((block) => block.type === "text")
        .map((block) => `${message.role}: ${block.text}`),
    ),
    ...snapshot.previews
      .filter((preview) => preview.kind === "text")
      .map((preview) => `assistant: ${preview.delta}`),
  ];
  // textContent, never innerHTML: model output is untrusted.
  transcript.replaceChildren(...lines.map((line) => {
    const p = document.createElement("p");
    p.textContent = line;
    return p;
  }));
  send.disabled = snapshot.send.action.status !== "enabled";
  stop.disabled = snapshot.interruption.action.status !== "enabled";
  const failed = [snapshot.authorization, snapshot.connection, snapshot.send]
    .find((state) => "error" in state);
  error.textContent = failed && "error" in failed ? failed.error.message : "";
});

form.addEventListener("submit", (event) => {
  event.preventDefault();
  // Failures surface in the snapshot, so only success needs handling here.
  chat.send(input.value).then(() => (input.value = ""), () => {});
});
stop.addEventListener("click", () => chat.interrupt());
