#!/usr/bin/env node

import { Client, formatNvokenError } from "@deepnoodle/nvoken";

const client = new Client();

// Both creates are keyed and idempotent, so this file is safe to run twice.
// definitionKey and (tenantKey, agentKey) are unique, and restating one
// returns what it already names rather than making a second.
try {
  const definition = await client.createAgentDefinition({
    definitionKey: "quickstart",
    definition: {
      instructions: "Answer in one short sentence.",
      model: "anthropic/claude-sonnet-5",
    },
  });
  const instance = await client.createAgent({
    agentKey: "quickstart",
    agentDefinitionId: definition.id,
  });
  const agent = client.agent({ agentId: instance.id });
  console.log(await agent.text("Say hello in one short sentence."));
} catch (error) {
  console.error(formatNvokenError(error));
  process.exitCode = 1;
}
