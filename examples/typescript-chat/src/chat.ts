import { randomUUID } from "node:crypto";
import { createInterface } from "node:readline";
import { Client, NvokenError } from "@deepnoodle/nvoken";

const baseUrl = process.env.NVOKEN_BASE_URL ?? "http://localhost:8080";
const apiKey = process.env.NVOKEN_API_KEY;
const provider = process.env.NVOKEN_PROVIDER ?? "openai";
const model = process.env.NVOKEN_MODEL;

if (!apiKey) {
  throw new Error("NVOKEN_API_KEY is required");
}
if (provider !== "anthropic" && provider !== "openai") {
  throw new Error("NVOKEN_PROVIDER must be anthropic or openai");
}
if (!model) {
  throw new Error("NVOKEN_MODEL is required");
}

const client = new Client({ baseUrl, apiKey });
const sessionKey = process.env.NVOKEN_SESSION_KEY ?? `local-chat-${randomUUID()}`;
let sessionId: string | undefined;
let hadError = false;

const input = createInterface({
  input: process.stdin,
  output: process.stdout,
  terminal: process.stdin.isTTY,
});

console.log(`Connected to ${baseUrl}`);
console.log(`Session key: ${sessionKey}`);
if (process.stdin.isTTY) {
  console.log("Type a message, or type exit to quit.\n");
  input.setPrompt("you> ");
  input.prompt();
}

for await (const line of input) {
  const message = line.trim();
  if (!message) {
    if (process.stdin.isTTY) input.prompt();
    continue;
  }
  if (message === "exit" || message === "quit") break;

  try {
    const handle = await client.invoke({
      agentRef: "typescript-local-chat",
      sessionId,
      sessionKey: sessionId ? undefined : sessionKey,
      idempotencyKey: `${sessionKey}:${randomUUID()}`,
      input: message,
      spec: {
        instructions: "You are a concise, helpful assistant. Remember relevant details across this chat.",
        model: { provider, name: model },
        budgets: {
          maxOutputTokens: 300,
          maxIterations: 1,
        },
      },
    });

    sessionId = handle.sessionId;
    const invocation = await handle.wait();
    if (invocation.status !== "completed") {
      const reason = invocation.error
        ? `${invocation.error.code}: ${terminalSentence(invocation.error.message)}`
        : invocation.status;
      const modelHelp = invocation.error?.code === "provider_error"
        ? ` Check available model IDs at ${modelDocumentation(provider)}.`
        : "";
      throw new Error(`Invocation ${invocation.id} did not complete: ${reason}${modelHelp}`);
    }

    const answer = await handle.text();
    console.log(`agent> ${answer}\n`);
  } catch (error) {
    hadError = true;
    if (error instanceof NvokenError) {
      const code = error.code ? ` code=${error.code}` : "";
      const request = error.requestId ? ` request_id=${error.requestId}` : "";
      console.error(`nvoken error [${error.category}]${code}${request}: ${error.message}`);
    } else {
      console.error(error instanceof Error ? error.message : error);
    }
  }

  if (process.stdin.isTTY) input.prompt();
}

input.close();
if (hadError) process.exitCode = 1;

function modelDocumentation(value: "anthropic" | "openai"): string {
  return value === "openai"
    ? "https://developers.openai.com/api/docs/models"
    : "https://platform.claude.com/docs/en/about-claude/models/overview";
}

function terminalSentence(value: string): string {
  const trimmed = value.trim();
  return /[.!?]$/.test(trimmed) ? trimmed : `${trimmed}.`;
}
