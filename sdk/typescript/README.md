# nvoken TypeScript SDK

Use `Client` for durable Runtime workflows. It provides durable handles,
replay-safe retries, async pagination, typed errors, resumable SSE, callback
verification, and canonical assistant-text helpers. Generated operations remain
available from the `raw` export.

## Try the published package

The shortest proof needs no project files. Start the official daemon with the
[Run nvoken locally guide](https://github.com/deepnoodle-ai/nvoken/blob/main/docs/guides/run-locally.md),
then run this in the generated quickstart directory:

```bash
npx --yes --package "@deepnoodle/nvoken@$(nvokend --version)" nvoken-quickstart
```

The packaged TypeScript app writes and recalls the code word `cedar` across two
durable Invocations. It reads only the `NVOKEN_*` values from the marked `.env`
that `nvokend quickstart` created.

## Add nvoken to an app

In an empty consumer directory:

```bash
npm init -y
npm install @deepnoodle/nvoken
```

## Minimal application

After the Runtime is running, save this complete example as `quickstart.mjs`
next to the consumer's `package.json`:

<!-- public-quickstart:start -->
```js
import { randomUUID } from "node:crypto";
import {
  Client,
  formatInvocationFailure,
  formatNvokenError,
} from "@deepnoodle/nvoken";

try {
  await main();
} catch (error) {
  console.error(formatNvokenError(error));
  process.exitCode = 1;
}

async function main() {
  const apiKey = process.env.NVOKEN_API_KEY;
  const provider = process.env.NVOKEN_PROVIDER;
  const model = process.env.NVOKEN_MODEL;
  if (!apiKey) throw new Error("NVOKEN_API_KEY is required");
  if (provider !== "anthropic" && provider !== "openai") {
    throw new Error("NVOKEN_PROVIDER must be anthropic or openai");
  }
  if (!model) throw new Error("NVOKEN_MODEL is required");

  const client = new Client({
    baseUrl: process.env.NVOKEN_BASE_URL ?? "http://localhost:8080",
    apiKey,
  });
  const pricing = await client.pricingCapability({ provider, name: model });
  console.log(`pricing=${pricing.status} registry=${pricing.registryVersion}`);

  const handle = await client.invoke({
    agentRef: "typescript-package-quickstart",
    sessionKey: `typescript-package-${randomUUID()}`,
    idempotencyKey: `typescript-package-message-${randomUUID()}`,
    input: "Reply with a short hello.",
    spec: {
      instructions: "Be concise.",
      model: { provider, name: model },
      budgets: { maxOutputTokens: 100, maxIterations: 1 },
    },
  });
  const invocation = await handle.wait();
  if (invocation.status !== "completed") {
    throw new Error(formatInvocationFailure(handle.invocationId, invocation, provider));
  }
  console.log(`agent> ${await handle.text()}`);
}
```
<!-- public-quickstart:end -->

Choose an exact model ID that the configured provider account can access from
the official [OpenAI model catalog](https://developers.openai.com/api/docs/models)
or [Anthropic model overview](https://platform.claude.com/docs/en/about-claude/models/overview),
then run:

```bash
NVOKEN_API_KEY='<runtime-bearer>' \
NVOKEN_PROVIDER=openai \
NVOKEN_MODEL='<available-model>' \
node quickstart.mjs
```

The pricing preflight reports only this nvoken installation's local registry
capability. `priced` means nvoken has standard USD pricing for that exact model,
`unpriced` means it knows no such entry exists, and `unknown` means the adapter
cannot decide without a response. It does not check provider-account access or
guarantee the served model and usage evidence returned by a provider. Hosts can
use that status to set a cost cap, reject locally, or knowingly accept the
documented post-response risk.

The public snippet uses random keys only to create one bounded demonstration.
Production hosts should derive and persist idempotency keys from durable message
records, then reuse a key only when retrying that exact request.

## Install from a source checkout

This is the contributor path, not the first-time Run path. Follow
[Develop nvoken](https://github.com/deepnoodle-ai/nvoken/blob/main/docs/guides/developing-nvoken.md)
for the complete repository setup. From the nvoken repository root,
build `dist/` before installing the local package; generated output is not committed:

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

## Source-checkout two-turn and resume proof

The repository-only quickstart sends two turns through a new Session and prints
its host-owned Session key. Run it from the repository root after building the
SDK:

```bash
NVOKEN_BASE_URL=http://localhost:8080 \
NVOKEN_API_KEY='<runtime-bearer>' \
NVOKEN_PROVIDER=openai \
NVOKEN_MODEL='<available-model>' \
node sdk/typescript/dist/examples/quickstart.js
```

To append a new turn from another process, reuse the printed Session key and
supply a new durable run key:

```bash
NVOKEN_SESSION_KEY='<printed-session-key>' \
NVOKEN_RUN_KEY='<durable-host-message-id>' \
NVOKEN_API_KEY='<runtime-bearer>' \
NVOKEN_PROVIDER=openai \
NVOKEN_MODEL='<same-model>' \
node sdk/typescript/dist/examples/quickstart.js
```

The resume process asks a new question about context written by the first
process. Persist `NVOKEN_RUN_KEY` and reuse it only when retrying that exact
request after an uncertain acknowledgement; use a different durable message ID
for a genuinely new turn.

The starter uses output-token and iteration limits but intentionally omits
`maxEstimatedCostUsd`. A cost cap requires known USD pricing metadata and fails
closed with `details.kind = "estimated_cost_unavailable"` when pricing is not
known. Known-unpriceable work is rejected before a provider call.

Failed and cancelled Invocations print their ID, public code/message, safe
details, and a structured-log pointer, then exit nonzero. Raw provider bodies
and credentials are never public diagnostics. Applications can reuse the
exported `formatNvokenError` and `formatInvocationFailure` helpers to keep that
rendering consistent; `includeLogGuidance` adds the local-daemon pointer when it
is useful to an operator.

## Canonical assistant text

`Handle` serves messages and text from the one composed result read,
`GET /v1/invocations/{invocation_id}/result`:

```ts
const handle = await client.invoke(request);
const invocation = await handle.wait();
if (invocation.status !== "completed") {
  throw new Error(`${invocation.error?.code}: ${invocation.error?.message}`);
}

const result = await handle.result();   // invocation + messages + outputText
const messages = await handle.listMessages();
const text = await handle.text();       // the wire output_text projection
```

`text()` throws an actionable error when `output_text` is null or empty: the
projection is non-null only for a completed Invocation with assistant text, so
failed and cancelled turns never masquerade as answers. The wire keeps null
and `""` distinct; the helper deliberately treats both as "no useful answer",
and hosts that need the distinction read `result().outputText`. For custom content handling, read
`result().messages` and use the exported `isTextContentBlock` type guard. The
projection is composed from canonical Session history at read time; nothing is
stored twice.

## Durable wait and retry semantics

- A local wait timeout, aborted request, process exit, or dropped stream does
  not cancel durable work. Call `handle.cancel()` only to change server state.
- Derive an idempotency key from the host's durable message record. After an
  ambiguous acknowledgement, retry the exact request with that same key.
- Assistant and tool checkpoints from a failed or cancelled Invocation remain
  readable as evidence but are excluded from future model context.

The package supports Node.js 20 and newer. This repository pins Node.js 24 as
its development and CI baseline; that is not the package runtime floor.
