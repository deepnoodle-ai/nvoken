import { randomUUID } from "node:crypto";
import { createInterface } from "node:readline";

import { Client, formatNvokenError } from "@deepnoodle/nvoken";

const baseUrl = process.env.NVOKEN_BASE_URL ?? "http://localhost:8080";
const tenant = process.env.NVOKEN_TENANT_KEY ?? "local-chat";
const conversationKey = process.env.NVOKEN_CONVERSATION_KEY ?? `local-chat-${randomUUID()}`;
const client = new Client({ baseUrl });
const agent = await client.agents.create({
  key: `typescript-local-chat-${randomUUID()}`,
  name: "Local chat",
  instructions: "Be concise, helpful, and remember relevant details across this Conversation.",
  model: `${process.env.NVOKEN_MODEL_PROVIDER ?? "anthropic"}/${process.env.NVOKEN_MODEL ?? "claude-sonnet-5"}`,
  limits: { maxOutputTokens: 300 },
});
const chat = agent.conversation({
  tenant,
  key: conversationKey,
  owner: "tenant",
});

const input = createInterface({
  input: process.stdin,
  output: process.stdout,
  terminal: process.stdin.isTTY,
});

console.log(`Connected to ${baseUrl}`);
console.log(`Conversation key: ${conversationKey}`);
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
