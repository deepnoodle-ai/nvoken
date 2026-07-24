# TypeScript Agent and host tools

This example demonstrates the ordinary high-level rung of the TypeScript SDK:
`Agent`, automatic host-tool dispatch, and a bound durable Session. Use the
[invoke showcase](../typescript-invoke-showcase/README.md) when you need to
study lower-level handles, explicit ToolCall submission, pagination, and
recovery.

The first turn asks the model to call `lookup_order`. The Agent waits for the
durable parked call, invokes the local handler, submits its result under the
stable ToolCall ID, and resumes the Invocation. The second turn proves that the
bound Session carries the committed transcript forward.

Build this executable example as part of the repository SDK gate:

```bash
npm ci --prefix sdk/typescript
npm run build --prefix sdk/typescript
npm ci --prefix examples/typescript-agent-tools
npm run build --prefix examples/typescript-agent-tools
```

For a live smoke path, start a local Runtime, load the generated quickstart
environment, and run:

```bash
set -a
source .env
set +a
npm run check --prefix examples/typescript-agent-tools
```

The Runtime permits one nonterminal Invocation per Session. The bound Session
serializes this example's turns in-process; the server remains authoritative
across processes. A stable ToolCall ID makes the handler's own side effects
idempotent only if the handler uses that ID as its idempotency key.
