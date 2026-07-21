import { Client } from "../index.js";

const client = new Client({
  baseUrl: process.env.NVOKEN_BASE_URL ?? "http://localhost:8080",
  apiKey: process.env.NVOKEN_API_KEY ?? "",
});

const handle = await client.invoke({
  agentRef: "support",
  idempotencyKey: "ticket-42:message-1",
  input: "Why was I charged twice?",
  spec: {
    instructions: "Help the customer with billing questions.",
    model: { provider: "anthropic", name: "claude-sonnet-5" },
  },
});
const invocation = await handle.wait();
console.log(invocation.id, invocation.status);
