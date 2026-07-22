import { randomUUID } from "node:crypto";

import {
  Client,
  formatInvocationFailure,
  formatNvokenError,
} from "../index.js";

try {
  await main();
} catch (error) {
  console.error(formatNvokenError(error));
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
    if (invocation.status !== "completed") {
      throw new Error(formatInvocationFailure(
        handle.invocationId,
        invocation,
        provider,
        { includeLogGuidance: true },
      ));
    }
    console.log(`agent> ${await handle.text()}`);
  }
  console.log(`session_key=${sessionKey}`);
}
