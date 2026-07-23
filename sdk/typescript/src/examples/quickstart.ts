#!/usr/bin/env node

import { Client, formatNvokenError } from "@deepnoodle/nvoken";

const agent = new Client().agent({
  agentKey: "quickstart",
  instructions: "Be concise and helpful.",
});

try {
  console.log(await agent.text("Say hello in one short sentence."));
} catch (error) {
  console.error(formatNvokenError(error));
  process.exitCode = 1;
}
