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

For a broader source-SDK exercise, the
[TypeScript invoke showcase](https://github.com/deepnoodle-ai/nvoken/tree/main/examples/typescript-invoke-showcase)
tests client tools, structured output, multi-turn Sessions, `agent_ref`,
`tenant_ref`, idempotency, pagination, transcript cursors, and SSE against a
real provider. It is intentionally more comprehensive than the first-response
quickstart and may incur several small provider charges.

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
- `handle.wait()` waits for a terminal state. In a client-tool workflow, use
  `handle.wait({ until: "actionable" })`; it returns when the Invocation is
  `waiting`, `completed`, `failed`, or `cancelled`.
- Derive an idempotency key from the host's durable message record. After an
  ambiguous acknowledgement, retry the exact request with that same key.
- A newly admitted Handle retains `agentId` and `deduplicated` from the
  acknowledgement. `deduplicated` is undefined only on a Handle reconstructed
  later with `client.resume()`.
- Assistant and tool checkpoints from a failed or cancelled Invocation remain
  readable as evidence but are excluded from future model context.

## Client tools

Define a tool once to bind its JSON Schema to an application type. nvoken
validates admitted ToolCall input against the schema; `toolInput` checks the
ToolCall name and returns that already-validated input with its TypeScript
type:

```ts
import {
  defineClientTool,
  defineJsonSchema,
  toolInput,
} from "@deepnoodle/nvoken";

interface LookupOrderInput {
  order_id: string;
}

const lookupOrder = defineClientTool<LookupOrderInput>({
  mode: "client",
  name: "lookup_order",
  description: "Look up an order by ID.",
  inputSchema: defineJsonSchema<LookupOrderInput>({
    type: "object",
    properties: {
      order_id: { type: "string" },
    },
    required: ["order_id"],
    additionalProperties: false,
  }),
});

const handle = await client.invoke({
  agentRef: "support",
  sessionKey: "ticket-42",
  idempotencyKey: "message-101",
  input: "Where is order-42?",
  spec: {
    instructions: "Use lookup_order before answering.",
    model: { provider, name: model },
    tools: [lookupOrder],
  },
});

const actionable = await handle.wait({ until: "actionable" });
if (actionable.status === "waiting") {
  for (const call of actionable.pendingToolCalls ?? []) {
    const input = toolInput(lookupOrder, call);
    const order = await orders.get(input.order_id);
    await handle.submitToolResults([{
      toolCallId: call.id,
      content: order,
    }]);
  }
}

const terminal = await handle.wait();
if (terminal.status !== "completed") {
  throw new Error(`${terminal.error?.code}: ${terminal.error?.message}`);
}
console.log(await handle.text());
```

Persist each ToolCall ID with its result. Retrying the same result is safe and
reports `deduplicated: true`; submitting different content for a settled call
returns `tool_result_conflict`. A `waiting` Invocation remains the Session's
active Invocation, so do not admit another turn to that Session. Finish,
cancel, or allow the earlier turn to settle first.

`defineClientTool` and `defineJsonSchema` associate TypeScript types with
runtime schemas; they do not perform local validation. The Runtime remains the
authority that validates the admitted schema and generated tool input.

## Typed structured output

The same schema helper makes terminal structured output type-safe:

```ts
interface Classification {
  category: "billing" | "technical";
  priority: "normal" | "high";
  needs_human: boolean;
}

const classificationSchema = defineJsonSchema<Classification>({
  type: "object",
  properties: {
    category: { type: "string", enum: ["billing", "technical"] },
    priority: { type: "string", enum: ["normal", "high"] },
    needs_human: { type: "boolean" },
  },
  required: ["category", "priority", "needs_human"],
  additionalProperties: false,
});

const handle = await client.invoke({
  agentRef: "support",
  sessionKey: "ticket-43",
  idempotencyKey: "message-102",
  input: "I was charged twice and need a person to review it.",
  spec: {
    instructions: "Classify the request.",
    model: { provider, name: model },
    outputSchema: classificationSchema,
  },
});

const invocation = await handle.wait();
if (invocation.status === "completed") {
  const classification = invocation.structuredOutput; // Classification | null
  console.log(classification?.priority);
}
```

The Runtime validates the terminal object and records its ToolCall and schema
digest in `structuredOutputProvenance`. The generic type is carried through
`Handle`, `refresh()`, `wait()`, and `result()`. When reconstructing a typed
Handle in another process, use `client.resume<Classification>(invocationId)`.

## Agent, tenant, and Session identity

Host references resolve under these scopes:

```text
Agent identity:    Account + agentRef
Session identity:  Account + tenant partition + Agent + sessionKey
Idempotency scope: Account + tenant partition + agentRef + idempotencyKey
```

The same `agentRef` therefore resolves to the same Account-wide `agentId`
across tenants, while Sessions and idempotency remain tenant-partitioned. The
Handle exposes the resolved ID needed for exact Session recovery:

```ts
const handle = await client.invoke({
  agentRef: "support",
  tenantRef: "tenant-acme",
  sessionKey: "ticket-42",
  idempotencyKey: "message-101",
  input: "Hello",
  spec,
});

const session = await client.getSessionByKey("ticket-42", {
  tenantRef: "tenant-acme",
  agentId: handle.agentId,
});

for await (const item of client.sessionPages({
  tenantRef: "tenant-acme",
  agentId: handle.agentId,
  sessionKey: "ticket-42",
  limit: 100,
})) {
  console.log(item.id);
}
```

Use `{ defaultTenant: true, agentId }` instead of `tenantRef` for the default
tenant partition. An Account-wide credential may continue a known Session by
`sessionId` without repeating its tenant reference. Supplying an incompatible
Agent or explicit tenant returns nondisclosing `not_found`.

Only one queued, running, or waiting Invocation may own a Session. Serialize
turn admission per Session. A `session_invocation_active` conflict means the
earlier turn is still active; inspect or resume that Invocation instead of
retrying the new turn under another idempotency key.

## Session history and recovery

Use the async iterators for complete collection traversal:

```ts
for await (const message of client.messagePages(session.id, { limit: 100 })) {
  console.log(message.sequence, message.role);
}

for await (const invocation of client.invocationPages({
  sessionId: session.id,
  limit: 100,
})) {
  console.log(invocation.id, invocation.status);
}
```

`drainTranscript` reads one fixed-cut snapshot and owns the page-token rules.
Persist its `resumeCursor` to recover only newer durable messages and
Invocation lifecycle changes later:

```ts
const initial = await client.drainTranscript(session.id, { pageSize: 100 });
await checkpoints.put(session.id, initial.resumeCursor);

const delta = await client.drainTranscript(session.id, {
  cursor: await checkpoints.get(session.id),
  pageSize: 100,
});
await apply(delta.messages, delta.invocationChanges);
await checkpoints.put(session.id, delta.resumeCursor);
```

Use `handle.stream(...)` when the process should follow new Session output
continuously. The stream resumes with durable cursors and reconciles against
the authoritative transcript; `drainTranscript` is the simpler primitive for
startup recovery, periodic synchronization, and bounded jobs.

The package supports Node.js 20 and newer. This repository pins Node.js 24 as
its development and CI baseline; that is not the package runtime floor.
