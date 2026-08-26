# TypeScript Agent and host tools

This example creates a tenant-owned Agent, binds a process-local
`lookup_order` handler to its durable tool contract, and sends two Turns through
one Conversation. The first Turn exercises automatic tool driving. The second
proves that the committed transcript carries forward.

Build it as part of the repository SDK gate:

```bash
npm ci --prefix sdk/typescript
npm run build --prefix sdk/typescript
npm ci --prefix examples/typescript-agent-tools
npm run build --prefix examples/typescript-agent-tools
```

For a live run, start nvoken and provide an App credential plus a model your
provider account can use:

```bash
NVOKEN_API_KEY='<app-key>' \
NVOKEN_MODEL_PROVIDER='anthropic' \
NVOKEN_MODEL='claude-sonnet-5' \
npm run check --prefix examples/typescript-agent-tools
```

The handler uses the stable ToolCall ID as the host-side idempotency key.
That is what makes its own effects safe when tool-result submission or process
recovery causes the handler to run again.

See the [Turn showcase](../typescript-turn-showcase/README.md) for standalone
Turns, structured output, recovery, and raw transcript inspection.
