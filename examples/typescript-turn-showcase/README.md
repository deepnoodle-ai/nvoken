# TypeScript Turn showcase

This source-checkout example makes real provider requests and proves the target
TypeScript facade end to end:

- Agent creation and awaited owner-exact key lookup;
- two Turns sharing one durable Conversation;
- exact raw transcript inspection;
- synchronous `client.turn()` recovery by durable ID;
- inline behavior with automatic host-tool driving;
- typed structured output.

Build the SDK first, then install and run the showcase:

```bash
npm ci --prefix sdk/typescript
npm run build --prefix sdk/typescript
npm ci --prefix examples/typescript-turn-showcase

NVOKEN_API_KEY='<app-key>' \
NVOKEN_MODEL_PROVIDER='anthropic' \
NVOKEN_MODEL='claude-sonnet-5' \
npm run check --prefix examples/typescript-turn-showcase
```

Every run uses unique Agent, tenant, Conversation, and idempotency-key names.
It prints durable IDs and assertions but never credentials. The local `file:`
dependency intentionally targets `sdk/typescript`, so its `dist/` must exist
before installing this contributor example.

For the smallest ordinary host-tool workflow, see
[TypeScript Agent and host tools](../typescript-agent-tools/README.md).
