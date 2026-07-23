# nvoken TypeScript SDK

The supported entry point is `Client`. It provides a small agent API for the
common path, durable `InvocationHandle` objects when you need control, and the
generated transport under `client.raw()`.

## Install

```bash
npm install @deepnoodle/nvoken
```

Node.js 20 or newer is required.

## First response

After following the
[local quickstart](https://github.com/deepnoodle-ai/nvoken/blob/main/docs/guides/run-locally.md),
the generated `.env` contains the API key and model:

<!-- public-quickstart:start -->
```ts
import { Client } from "@deepnoodle/nvoken";

const agent = new Client().agent({
  agentKey: "support",
  instructions: "Be concise and helpful.",
});

console.log(await agent.text("Why was I charged twice?"));
```
<!-- public-quickstart:end -->

`new Client()` resolves configuration in this order:

1. explicit constructor options;
2. `NVOKEN_*` process environment variables;
3. a `.env` whose first line is the marker written by `nvokend quickstart`;
4. `http://localhost:8080` for the base URL.

It never loads an arbitrary `.env` or mutates `process.env`. `NVOKEN_API_KEY` is
required. `NVOKEN_PROVIDER` and `NVOKEN_MODEL` must be supplied together unless
the Agent receives an explicit model:

```ts
const client = new Client({
  baseUrl: "https://runtime.example.com",
  apiKey: process.env.RUNTIME_KEY,
  model: { provider: "anthropic", id: "claude-sonnet-5" },
});
```

## Discover models

List nvoken's curated selections, then inspect the exact model you plan to use:

```ts
const catalog = await client.listModels({ provider: "anthropic" });
const recommended = catalog.items.find((model) => model.recommended);

const selected = await client.getModel({
  provider: "anthropic",
  id: recommended?.id ?? "claude-sonnet-5",
});
console.log(selected.cataloged, selected.pricing.status);
```

Catalog membership does not guarantee that your provider account can access a
model. `getModel()` also accepts uncataloged IDs and safely encodes IDs
containing `/`, reserved characters, or Unicode.

## Choose the level of control

`agent.text()` returns only the assistant text:

```ts
const text = await agent.text("Summarize this issue.");
```

`agent.run()` returns the complete typed result:

```ts
const result = await agent.run("Summarize this issue.");

console.log(result.text);
console.log(result.invocation.usage);
console.log(result.agentId, result.sessionId, result.deduplicated);
```

`agent.invoke()` admits the turn and immediately returns its durable handle:

```ts
const handle = await agent.invoke("Summarize this issue.");
const result = await handle.waitForResult();
```

Use a lazy handle to recover work in another process. Creating it performs no
request:

```ts
const handle = client.invocation("invk_...");
const result = await handle.waitForResult();
```

Useful handle methods are:

- `refresh()` for the current authoritative state;
- `waitForAction()` for `waiting` or terminal state;
- `waitForResult()` for successful terminal work, with
  `InvocationError` on failure or cancellation;
- `result()`, `text()`, and `listMessages()` for composed result reads;
- `submitToolResults()` and `cancel()` for explicit orchestration;
- `stream()` for the lower-level Invocation event stream.

The SDK generates an idempotency key before admission and reuses the exact body
and key on ambiguous retries. The key is exposed as `handle.idempotencyKey`.
Supply `idempotencyKey` yourself only when the application needs to reproduce
the same logical admission across a process boundary.

## Multiple turns

Bind a Session once and use it like a chat:

```ts
const chat = agent.session({ sessionKey: "ticket-483", tenantKey: "acme" });

await chat.text("Remember that my code is ORCHID-724.");
console.log(await chat.text("What is my code?"));
```

You can also bind a durable Session ID:

```ts
const chat = agent.session({ sessionId: "sesn_..." });
```

Every turn admitted through the same binding is serialized locally, including
`invoke()` and `stream()`, matching nvoken's
one-nonterminal-Invocation-per-Session rule. `invoke()` still returns as soon as
its turn is admitted, while the binding keeps that Session reserved until the
Invocation ends. Use `agent.invoke()` directly for application-managed
concurrency. A race from another binding or process throws `SessionBusyError`
with the active Invocation ID and status.

## Host tools

A host tool can carry its handler. `run()` parks safely at `waiting`, dispatches
the matching handlers, submits results under stable ToolCall IDs, and resumes
until completion:

```ts
import { Client, defineHostTool, defineJsonSchema } from "@deepnoodle/nvoken";

interface LookupOrder {
  orderId: string;
}

const lookupOrder = defineHostTool({
  name: "lookup_order",
  description: "Look up one order.",
  inputSchema: defineJsonSchema<LookupOrder>({
    type: "object",
    properties: { orderId: { type: "string" } },
    required: ["orderId"],
    additionalProperties: false,
  }),
  async handler(input, context) {
    return {
      orderId: input.orderId,
      state: await orders.state(input.orderId),
      toolCallId: context.toolCallId,
    };
  },
});

const support = new Client().agent({
  agentKey: "support",
  instructions: "Use lookup_order for order questions.",
  tools: [lookupOrder],
});

console.log(await support.text("Where is order 42?"));
```

If a requested host tool has no handler, `run()` throws
`MissingToolHandlerError` with the handle and pending call. The low-level
`invoke()` path remains available for queues, browsers, and external workers.

Stable ToolCall IDs let a handler make its own side effects idempotent. They do
not make arbitrary side effects exactly-once.

## Structured output and schema libraries

Raw JSON Schema keeps an application type:

```ts
interface Classification {
  category: "billing" | "other";
  needsHuman: boolean;
}

const classifier = new Client().agent({
  agentKey: "classifier",
  instructions: "Classify the request.",
  outputSchema: defineJsonSchema<Classification>({
    type: "object",
    properties: {
      category: { type: "string", enum: ["billing", "other"] },
      needsHuman: { type: "boolean" },
    },
    required: ["category", "needsHuman"],
    additionalProperties: false,
  }),
});

const result = await classifier.run("I was charged twice.");
console.log(result.structuredOutput?.category);
```

The SDK also accepts the dependency-free `StandardJSONSchemaV1` interface for
tool inputs and output schemas. Compatible Zod 4.2+, ArkType, and Valibot
schemas can therefore be passed directly with inferred types:

```ts
import * as z from "zod";

const outputSchema = z.object({
  category: z.enum(["billing", "other"]),
  needsHuman: z.boolean(),
});

const classifier = new Client().agent({
  agentKey: "classifier",
  instructions: "Classify the request.",
  outputSchema,
});
```

Conversion targets JSON Schema draft 2020-12. Unsupported library conversion
throws a typed validation error before admission; schemas outside nvoken's
bounded supported subset are rejected by the Runtime.

## Streaming

`agent.stream()` admits and follows one Invocation as a typed async iterable.
The minimal consumer needs only two event types:

```ts
for await (const event of agent.stream("Write a haiku.")) {
  if (event.type === "output_text.delta") process.stdout.write(event.text);
  if (event.type === "invocation.result") {
    console.log(`\n${event.result.invocation.usage?.outputTokens ?? 0} tokens`);
  }
}
```

`invocation.*` events carry durable state, `*.delta` events are discardable
previews, and `stream.*` events control transport recovery. The SDK reconnects
with the latest durable cursor; `stream.resync` means discard buffered previews
and wait for a durable `invocation.update` or `invocation.result`. Host handlers
configured on the Agent are dispatched whenever the Invocation parks.
Disconnecting the caller never cancels the Invocation.

Use `handle.stream()` to reconnect to one already-admitted Invocation. The
lower-level Session stream and its `Reducer` remain available for applications
that need to follow every turn in a conversation.

## Sessions, messages, and transcripts

The facade has symmetric page and drain helpers:

```ts
const sessions = await client.listSessions({
  tenantKey: "acme",
  agentId: "agnt_...",
  sessionKey: "ticket-483",
});

for await (const session of client.sessionPages({ tenantKey: "acme" })) {
  console.log(session.id);
}

for await (const message of client.messagePages("sesn_...", { limit: 100 })) {
  console.log(message.sequence, message.role);
}

const transcript = await client.drainTranscript("sesn_...", {
  cursor: previousCursor,
  pageSize: 100,
});
```

`drainTranscript()` holds one fixed snapshot cut across pages and returns the
next durable `resumeCursor`.

## Errors and raw access

All facade failures normalize to `NvokenError`, with `category`, HTTP `status`,
wire `code`, `requestId`, retry metadata, and safe structured `details`.
`SessionBusyError`, `InvocationError`, and
`MissingToolHandlerError` add workflow-specific context.

Use generated APIs only when you need the one-to-one wire surface:

```ts
const { invocations, sessions, modelPricing } = client.raw();
```

The wire contract uses `agent_key`, `tenant_key`, `model.id`, `spec.limits`,
and tool mode `host`. The TypeScript facade exposes their idiomatic camel-case
forms.
