# TypeScript Agent and host tools

This example creates a tenant-owned Agent, binds a process-local
`lookup_order` handler to its durable tool contract, and sends two Turns through
one Conversation. The first Turn exercises automatic tool driving. The second
proves that the committed transcript carries forward.

Build it as part of the repository SDK gate:

```bash
corepack enable
pnpm install --frozen-lockfile
pnpm --filter nvoken-typescript-agent-tools... build
```

For a live run, start nvoken and provide an App credential plus a model your
provider account can use:

```bash
NVOKEN_API_KEY='<app-key>' \
NVOKEN_MODEL_PROVIDER='anthropic' \
NVOKEN_MODEL='claude-sonnet-5' \
pnpm --filter nvoken-typescript-agent-tools check
```

The handler uses the stable ToolCall ID as the host-side idempotency key.
That is what makes its own effects safe when tool-result submission or process
recovery causes the handler to run again.

See the [Turn showcase](../typescript-turn-showcase/README.md) for standalone
Turns, structured output, recovery, and raw transcript inspection.
