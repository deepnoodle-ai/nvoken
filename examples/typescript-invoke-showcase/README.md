# TypeScript invoke showcase

This source-checkout example exercises the TypeScript SDK beyond the basic chat
quickstart. It makes real provider requests and verifies:

- two turns sharing one durable Session;
- exact idempotent admission replay and changed-request conflict;
- host ToolCall parking, Session visibility, result submission, and replay;
- schema-bound host-tool input and structured output;
- actionable waiting plus admission acknowledgement metadata;
- `agent_key` identity and `tenant_key` Session partitioning;
- exact Session-key lookup, facade pagination, transcript draining, and
  Invocation-scoped SSE.

This example intentionally demonstrates the lower-level `Client` and durable
handle rung for application-managed orchestration. Use
[TypeScript Agent and host tools](../typescript-agent-tools/README.md) for the
ordinary `Agent`, automatic host-tool dispatch, and bound Session rung. Its
build is part of `make sdk-check`, so the advanced examples fail CI if the
public SDK surface drifts.

Start the source daemon as described in
[Develop nvoken](../../docs/guides/developing-nvoken.md), build the SDK, then
install and run this example:

```bash
npm ci --prefix sdk/typescript
npm run build --prefix sdk/typescript
npm ci --prefix examples/typescript-invoke-showcase

set -a
source .env
set +a
npm run check --prefix examples/typescript-invoke-showcase
```

The example uses a unique tenant, Agent key, Session key, and idempotency
key namespace on every run. It prints identifiers and assertion results but
never prints credentials. Expect up to nine small model requests. They may be
billed by the configured provider.

The local `file:` dependency intentionally targets `sdk/typescript`, so `dist/`
must exist before `npm install`. This example is for contributors evaluating
the checked-out SDK. Applications should install the published
`@deepnoodle/nvoken` package instead.
