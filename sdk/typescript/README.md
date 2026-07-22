# nvoken TypeScript SDK

Use `Client` for durable Runtime workflows. It provides durable handles,
replay-safe retries, async pagination, typed errors, resumable SSE, callback
verification, and canonical assistant-text helpers. Generated operations remain
available from the `raw` export.

## Install

```bash
npm install @deepnoodle/nvoken
```

## Install from a source checkout

Build `dist/` before installing the local package; generated output is not
committed:

```bash
npm ci --prefix sdk/typescript
npm run build --prefix sdk/typescript
```

In a consumer next to this repository, use a `file:` dependency whose path
points to `sdk/typescript`:

```json
{
  "dependencies": {
    "@deepnoodle/nvoken": "file:../nvoken/sdk/typescript"
  }
}
```

Then run `npm install` in the consumer.

## Two-turn quickstart

First start a Runtime with the
[local development quickstart](https://github.com/deepnoodle-ai/nvoken/blob/main/docs/guides/local-development.md).
Its initial `RUNTIME_API_KEY` is the application's `NVOKEN_API_KEY`. Configure
at least one provider key on the daemon, then select that provider and a model
available to your provider account:

```bash
NVOKEN_BASE_URL=http://localhost:8080 \
NVOKEN_API_KEY='<runtime-bearer>' \
NVOKEN_PROVIDER=openai \
NVOKEN_MODEL='<available-model>' \
node dist/examples/quickstart.js
```

The example sends two messages through one durable Session and prints both
canonical assistant responses. Set `NVOKEN_SESSION_KEY` to resolve an existing
host-owned Session key from another process. Provider and model availability is
account-specific and changes over time.

The starter uses output-token and iteration limits but intentionally omits
`maxEstimatedCostUsd`. A cost cap requires known USD pricing metadata and fails
closed with `details.kind = "estimated_cost_unavailable"` when pricing is not
known. Known-unpriceable work is rejected before a provider call.

Failed and cancelled Invocations print their ID, public code/message, safe
details, and a structured-log pointer, then exit nonzero. Raw provider bodies
and credentials are never public diagnostics.

## Canonical assistant text

`Handle` reads the authoritative Session transcript and filters it by the exact
Invocation:

```ts
const handle = await client.invoke(request);
const invocation = await handle.wait();
if (invocation.status !== "completed") {
  throw new Error(`${invocation.error?.code}: ${invocation.error?.message}`);
}

const messages = await handle.listMessages();
const text = await handle.text();
```

For custom content handling, use the exported `isTextContentBlock` type guard.
The helpers do not copy text onto a second result model; canonical Session
history remains authoritative.

## Durable wait and retry semantics

- A local wait timeout, aborted request, process exit, or dropped stream does
  not cancel durable work. Call `handle.cancel()` only to change server state.
- Derive an idempotency key from the host's durable message record. After an
  ambiguous acknowledgement, retry the exact request with that same key.
- Assistant and tool checkpoints from a failed or cancelled Invocation remain
  readable as evidence but are excluded from future model context.

The package supports Node.js 20 and newer. This repository pins Node.js 24 as
its development and CI baseline; that is not the package runtime floor.
