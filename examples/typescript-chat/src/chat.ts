import { randomUUID } from "node:crypto";
import { createInterface } from "node:readline";

import { Client, formatNvokenError } from "@deepnoodle/nvoken";

const client = new Client();
const sessionKey = process.env.NVOKEN_SESSION_KEY ?? `local-chat-${randomUUID()}`;
await client.createAgentDefinition({
  definitionKey: "typescript-local-chat",
  name: "Local chat",
  instructions: "Be concise, helpful, and remember relevant details across this chat.",
  model: {
    provider: (process.env.NVOKEN_MODEL_PROVIDER ?? "anthropic") as "anthropic",
    id: process.env.NVOKEN_MODEL ?? "claude-sonnet-5",
  },
  limits: { maxOutputTokens: 300 },
});
const chat = client.agent({
  agentKey: "typescript-local-chat",
  definitionKey: "typescript-local-chat",
}).bindSession({ sessionKey });

const input = createInterface({
  input: process.stdin,
  output: process.stdout,
  terminal: process.stdin.isTTY,
});

console.log(`Connected to ${client.configuration.basePath}`);
console.log(`Session key: ${sessionKey}`);
if (process.stdin.isTTY) {
  console.log("Type a message, or type exit to quit.\n");
  input.setPrompt("you> ");
  input.prompt();
}

let hadError = false;
for await (const line of input) {
  const message = line.trim();
  if (!message) {
    if (process.stdin.isTTY) input.prompt();
    continue;
  }
  if (message === "exit" || message === "quit") break;

  try {
    console.log(`agent> ${await chat.text(message)}\n`);
  } catch (error) {
    hadError = true;
    console.error(formatNvokenError(error));
  }
  if (process.stdin.isTTY) input.prompt();
}

input.close();
if (hadError) process.exitCode = 1;
