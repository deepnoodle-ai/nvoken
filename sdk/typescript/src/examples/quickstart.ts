import { randomUUID } from "node:crypto";

import { Client, NvokenError, type Handle } from "../index.js";

try {
  await main();
} catch (error) {
  reportError(error);
  process.exitCode = 1;
}

async function main(): Promise<void> {
  const baseUrl = process.env.NVOKEN_BASE_URL ?? "http://localhost:8080";
  const apiKey = process.env.NVOKEN_API_KEY;
  const provider = process.env.NVOKEN_PROVIDER;
  const model = process.env.NVOKEN_MODEL;
  if (!apiKey) throw new Error("NVOKEN_API_KEY is required");
  if (provider !== "anthropic" && provider !== "openai") {
    throw new Error("NVOKEN_PROVIDER must be anthropic or openai");
  }
  if (!model) throw new Error("NVOKEN_MODEL is required");

  const client = new Client({ baseUrl, apiKey });
  const explicitSessionKey = process.env.NVOKEN_SESSION_KEY;
  const runKey = process.env.NVOKEN_RUN_KEY ?? (explicitSessionKey ? undefined : randomUUID());
  if (!runKey) {
    throw new Error("NVOKEN_RUN_KEY is required when NVOKEN_SESSION_KEY resumes an existing Session");
  }
  const sessionKey = explicitSessionKey ?? `typescript-quickstart-${randomUUID()}`;
  const messages = explicitSessionKey
    ? ["What is my code word? Reply with only the word."]
    : [
        "Remember that my code word is cedar.",
        "What is my code word? Reply with only the word.",
      ];

  let sessionId: string | undefined;
  for (const [index, input] of messages.entries()) {
    const handle = await client.invoke({
      agentRef: "typescript-quickstart",
      sessionId,
      sessionKey: sessionId ? undefined : sessionKey,
      idempotencyKey: `${runKey}:message-${index + 1}`,
      input,
      spec: {
        instructions: "Be concise and remember relevant details across this Session.",
        model: { provider, name: model },
        budgets: { maxOutputTokens: 300, maxIterations: 1 },
      },
    });
    sessionId = handle.sessionId;
    const invocation = await handle.wait();
    requireCompleted(handle, invocation, provider);
    console.log(`agent> ${await handle.text()}`);
  }
  console.log(`session_key=${sessionKey}`);
}

function reportError(error: unknown): void {
  if (error instanceof NvokenError) {
    const code = error.code ? ` code=${error.code}` : "";
    const request = error.requestId ? ` request_id=${error.requestId}` : "";
    console.error(`nvoken error [${error.category}]${code}${request}: ${error.message}`);
  } else {
    console.error(error instanceof Error ? error.message : error);
  }
}

function requireCompleted(
  handle: Handle,
  invocation: Awaited<ReturnType<Handle["wait"]>>,
  provider: "anthropic" | "openai",
): void {
  if (invocation.status === "completed") return;
  const publicReason = invocation.error
    ? `${invocation.error.code}: ${terminalSentence(invocation.error.message)}`
    : terminalSentence(invocation.status);
  const details = invocation.error?.details
    ? ` Safe details: ${JSON.stringify(invocation.error.details)}.`
    : "";
  const modelHelp = invocation.error?.code === "provider_error"
    ? ` Check available model IDs at ${modelDocumentation(provider)}.`
    : "";
  throw new Error(
    `Invocation ${handle.invocationId} ${invocation.status}: ${publicReason}${details}${modelHelp} `
      + `Inspect structured daemon logs for invocation_id=${handle.invocationId}; `
      + "raw provider responses are intentionally private.",
  );
}

function terminalSentence(value: string): string {
  const trimmed = value.trim();
  return /[.!?]$/.test(trimmed) ? trimmed : `${trimmed}.`;
}

function modelDocumentation(value: "anthropic" | "openai"): string {
  return value === "openai"
    ? "https://developers.openai.com/api/docs/models"
    : "https://platform.claude.com/docs/en/about-claude/models/overview";
}
