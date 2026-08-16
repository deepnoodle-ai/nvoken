#!/usr/bin/env node

import { Client, formatNvokenError } from "@deepnoodle/nvoken";

const client = new Client();

try {
  const definition = await client.createAgentDefinition({
    definitionKey: "quickstart",
    name: "Quickstart",
    definition: {
      instructions: "Answer in one short sentence.",
      model: { provider: "anthropic", id: "claude-sonnet-5" },
    },
    idempotencyKey: "quickstart-definition",
  });
  const agents = await client.listAgents({ agentKey: "quickstart" });
  const instance = agents.items[0] ?? await client.createAgent({
    agentKey: "quickstart",
    name: "Quickstart",
    agentDefinitionId: definition.id,
  });
  const agent = client.agent({ agentId: instance.id });
  console.log(await agent.text("Say hello in one short sentence."));
} catch (error) {
  console.error(formatNvokenError(error));
  process.exitCode = 1;
}
