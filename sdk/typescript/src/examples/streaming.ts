import { Client } from "../index.js";

const agent = new Client().agent({
	agentKey: "storyteller",
	agentDefinitionId: process.env.NVOKEN_AGENT_DEFINITION_ID ?? "",
});

// One frame previews what the model is writing; `kind` says what it is. The
// turn is over when a change carries a terminal status, and the stream ends
// right behind it.
for await (const event of agent.stream("Tell me a tiny story.")) {
  if (event.type === "message.delta" && event.kind === "text") {
    process.stdout.write(event.delta);
  }
  if (event.type === "transcript.update") {
    for (const change of event.invocationChanges) {
      if (change.status === "completed") {
        console.log(`\n${change.usage?.outputTokens ?? 0} tokens`);
      }
    }
  }
}
