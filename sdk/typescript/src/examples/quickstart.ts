#!/usr/bin/env node

import { Client, formatNvokenError } from "@deepnoodle/nvoken";

const client = new Client();

try {
  const agent = await client.agents.create({
    key: "quickstart",
    instructions: "Answer in one short sentence.",
    model: "anthropic/claude-sonnet-5",
    idempotencyKey: "quickstart-agent-v1",
  });
  console.log(await agent.text("Say hello in one short sentence.", {
    tenant: process.env.NVOKEN_TENANT_KEY ?? "quickstart",
  }));
} catch (error) {
  console.error(formatNvokenError(error));
  process.exitCode = 1;
}
