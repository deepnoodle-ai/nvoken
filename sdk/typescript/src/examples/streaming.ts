import { Client } from "../index.js";

const agent = new Client().agent({ agentKey: "storyteller" });

for await (const event of agent.stream("Tell me a tiny story.")) {
  if (event.type === "output_text.delta") process.stdout.write(event.text);
  if (event.type === "invocation.result") {
    console.log(`\n${event.result.invocation.usage?.outputTokens ?? 0} tokens`);
  }
}
