import { randomUUID } from "node:crypto";
import { createInterface } from "node:readline";
import {
  Client,
  formatInvocationFailure,
  formatNvokenError,
} from "@deepnoodle/nvoken";

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
      throw new Error(formatInvocationFailure(
        handle.invocationId,
        invocation,
        provider,
        { includeLogGuidance: true },
      ));
    }

    const answer = await handle.text();
    console.log(`agent> ${answer}\n`);
  } catch (error) {
    hadError = true;
    console.error(formatNvokenError(error));
  }

  if (process.stdin.isTTY) input.prompt();
}

input.close();
if (hadError) process.exitCode = 1;
