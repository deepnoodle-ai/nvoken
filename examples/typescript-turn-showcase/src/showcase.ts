import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";

import {
  Client,
  defineHostTool,
  defineJsonSchema,
  formatNvokenError,
} from "@deepnoodle/nvoken";

interface LookupOrderInput {
  orderId: string;
}

interface Classification {
  category: "billing" | "shipping";
  urgent: boolean;
}

const baseUrl = process.env.NVOKEN_BASE_URL ?? "http://localhost:8080";
const apiKey = required("NVOKEN_API_KEY");
const model = `${process.env.NVOKEN_MODEL_PROVIDER ?? "anthropic"}/${required("NVOKEN_MODEL")}`;
const client = new Client({ baseUrl, apiKey });
const runId = randomUUID();
const tenant = `typescript-turn-showcase-${runId}`;

try {
  await main();
} catch (error) {
  console.error(formatNvokenError(error));
  process.exitCode = 1;
}

async function main(): Promise<void> {
  const support = await client.agents.create({
    key: `support-${runId}`,
    name: "Support",
    instructions: "Be concise. Remember relevant details from the Conversation.",
    model,
    limits: { maxOutputTokens: 250, maxIterations: 4 },
  });

  const lookedUp = await client.agent(support.key);
  assert.equal(lookedUp.id, support.id);
  console.log(`PASS Agent create and awaited key lookup: ${support.id}`);

  const conversation = support.conversation({
    tenant,
    key: `support-${runId}`,
    owner: "tenant",
  });
  const first = await conversation.run(
    "Remember that my confirmation code is ORCHID-724. Reply only with remembered.",
  );
  assert.match(first.text ?? "", /remembered/i);
  assert.ok(first.conversationId);

  const second = await conversation.run(
    "What is my confirmation code? Reply only with the code.",
  );
  assert.match(second.text ?? "", /ORCHID-724/i);
  assert.equal(second.conversationId, first.conversationId);
  console.log(`PASS two-Turn Conversation continuity: ${first.conversationId}`);

  const transcript = await client.raw().conversations.listConversationMessages({
    conversationId: first.conversationId,
  }, {
    headers: { "X-Nvoken-Tenant-Key": tenant },
  });
  assert.ok(transcript.items.length >= 4);
  console.log(`PASS exact raw transcript read: ${transcript.items.length} messages`);

  const admitted = await support.start("Reply only with durable.", {
    tenant,
    idempotencyKey: `${runId}:recoverable`,
  });
  const recovered = client.turn(admitted.id, { tenant });
  assert.match((await recovered.result()).text ?? "", /durable/i);
  console.log(`PASS synchronous Turn recovery: ${admitted.id}`);

  const lookupOrder = defineHostTool<LookupOrderInput>({
    mode: "host",
    name: "lookup_order",
    description: "Look up one order by ID.",
    inputSchema: defineJsonSchema<LookupOrderInput>({
      type: "object",
      properties: { orderId: { type: "string" } },
      required: ["orderId"],
      additionalProperties: false,
    }),
  });
  const toolRunner = client.inline({
    instructions: "Call lookup_order before answering an order question.",
    model,
    tools: [lookupOrder],
    limits: { maxOutputTokens: 200, maxIterations: 3 },
  }).bindTools({
    lookup_order: async (input: LookupOrderInput, context) => ({
      orderId: input.orderId,
      state: "ready",
      idempotencyKey: context.toolCallId,
    }),
  });
  assert.match(await toolRunner.text("Where is order-42?", { tenant }), /ready/i);
  console.log("PASS inline behavior and automatic host-tool driving");

  const classifier = client.inline<Classification>({
    instructions: "Classify the support request.",
    model,
    outputSchema: defineJsonSchema<Classification>({
      type: "object",
      properties: {
        category: { type: "string", enum: ["billing", "shipping"] },
        urgent: { type: "boolean" },
      },
      required: ["category", "urgent"],
      additionalProperties: false,
    }),
  });
  const classified = await classifier.run("I was charged twice.", { tenant });
  assert.equal(classified.structuredOutput?.category, "billing");
  console.log("PASS typed structured output");
}

function required(name: string): string {
  const value = process.env[name];
  if (!value) throw new Error(`${name} is required`);
  return value;
}
