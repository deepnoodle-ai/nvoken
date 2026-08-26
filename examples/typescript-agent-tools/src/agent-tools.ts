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

try {
  const runId = randomUUID();
  const tenant = `agent-tools-${runId}`;
  const client = new Client();
  const support = (await client.agents.create({
    key: `order-support-${runId}`,
    name: "Order support",
    ownedBy: { tenant },
    instructions: [
      "Use lookup_order for order questions.",
      "Remember relevant details from the Conversation.",
    ].join(" "),
    model: `${process.env.NVOKEN_MODEL_PROVIDER ?? "anthropic"}/${process.env.NVOKEN_MODEL ?? "claude-sonnet-5"}`,
    tools: [lookupOrder],
  })).bindTools({
    lookup_order: async (input: LookupOrderInput, context) => {
      assert.equal(input.orderId, "order-42");
      return {
        orderId: input.orderId,
        state: "shipped",
        estimatedDelivery: "tomorrow",
        idempotencyKey: context.toolCallId,
      };
    },
  });

  const chat = support.conversation({
    tenant,
    key: `order-chat-${runId}`,
    owner: "tenant",
  });

  console.log(await chat.text(
    "Look up order-42. Say its state and estimated delivery.",
  ));

  const second = await chat.text(
    "What was the estimated delivery? Do not call the tool again.",
  );
  assert.match(second, /tomorrow/i);
  console.log(second);
} catch (error) {
  console.error(formatNvokenError(error));
  process.exitCode = 1;
}
