import { Client, isTerminalTurnStatus } from "../index.js";

const agent = await new Client().agent("storyteller");
const turn = await agent.start("Tell me a tiny story.", {
  tenant: process.env.NVOKEN_TENANT_KEY ?? "quickstart",
});

let renderedText = "";
for await (const update of turn.updates()) {
  const text = update.snapshot.text;
  if (text !== null && text !== renderedText) {
    renderedText = text;
    process.stdout.write(`${text}\n`);
  }

  if (isTerminalTurnStatus(update.snapshot.status)) {
    console.log(`turn ${turn.id}: ${update.snapshot.status}`);
  }
}
