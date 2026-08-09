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

const lookupOrder = defineHostTool({
  name: "lookup_order",
  description: "Look up one order by ID.",
  inputSchema: defineJsonSchema<LookupOrderInput>({
    type: "object",
    properties: {
      orderId: { type: "string" },
    },
    required: ["orderId"],
    additionalProperties: false,
  }),
  async handler(input) {
    assert.equal(input.orderId, "order-42");
    return {
      orderId: input.orderId,
      state: "shipped",
      estimatedDelivery: "tomorrow",
    };
  },
});

try {
  const runId = randomUUID();
  const support = new Client().agent({
    agentKey: `agent-tools-${runId}`,
    instructions: [
      "Use lookup_order for order questions.",
      "Remember durable Session context between turns.",
    ].join(" "),
    tools: [lookupOrder],
  });
  const chat = support.session({
    sessionKey: `order-chat-${runId}`,
  });

  const first = await chat.text(
    "Look up order-42. Say its state and estimated delivery.",
  );
  console.log(first);

  const second = await chat.text(
    "What was the estimated delivery? Do not call the tool again.",
  );
  assert.match(second, /tomorrow/i);
  console.log(second);
} catch (error) {
  console.error(formatNvokenError(error));
  process.exitCode = 1;
}
