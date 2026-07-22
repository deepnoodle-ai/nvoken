import { randomUUID } from "node:crypto";

import { Client, NvokenError, type Handle } from "../index.js";

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
const sessionKey = process.env.NVOKEN_SESSION_KEY ?? `typescript-quickstart-${randomUUID()}`;

try {
  let sessionId: string | undefined;
  for (const [index, input] of [
    "Remember that my code word is cedar.",
    "What is my code word? Reply with only the word.",
  ].entries()) {
    const handle = await client.invoke({
      agentRef: "typescript-quickstart",
      sessionId,
      sessionKey: sessionId ? undefined : sessionKey,
      idempotencyKey: `${sessionKey}:message-${index + 1}`,
      input,
      spec: {
        instructions: "Be concise and remember relevant details across this Session.",
        model: { provider, name: model },
        budgets: { maxOutputTokens: 300, maxIterations: 1 },
      },
    });
    sessionId = handle.sessionId;
    const invocation = await handle.wait();
    requireCompleted(handle, invocation);
    console.log(`agent> ${await handle.text()}`);
  }
  console.log(`session_key=${sessionKey}`);
} catch (error) {
  if (error instanceof NvokenError) {
    const code = error.code ? ` code=${error.code}` : "";
    const request = error.requestId ? ` request_id=${error.requestId}` : "";
    console.error(`nvoken error [${error.category}]${code}${request}: ${error.message}`);
  } else {
    console.error(error instanceof Error ? error.message : error);
  }
  process.exitCode = 1;
}

function requireCompleted(
  handle: Handle,
  invocation: Awaited<ReturnType<Handle["wait"]>>,
): void {
  if (invocation.status === "completed") return;
  const publicReason = invocation.error
    ? `${invocation.error.code}: ${invocation.error.message}`
    : invocation.status;
  const details = invocation.error?.details
    ? ` details=${JSON.stringify(invocation.error.details)}`
    : "";
  throw new Error(
    `Invocation ${handle.invocationId} ${invocation.status}: ${publicReason}.${details} `
      + `Inspect structured daemon logs for invocation_id=${handle.invocationId}; `
      + "raw provider responses are intentionally private.",
  );
}
