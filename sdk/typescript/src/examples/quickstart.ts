import { Client } from "@deepnoodle/nvoken";

const agent = new Client().agent({
  agentKey: "quickstart",
  instructions: "Be concise and helpful.",
});

console.log(await agent.text("Say hello in one short sentence."));
