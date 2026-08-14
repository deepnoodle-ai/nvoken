# nvoken TypeScript SDK

An Invocation is one durable agent turn. The host supplies `agentKey`,
optional `tenantKey`, `sessionKey`, and `idempotencyKey`; instructions, model,
and tools travel with the turn as an `agentDefinition`, either inline or
referenced by a reusable `agentDefinitionId`.

The package has three deliberate levels:

- `Agent` is the ordinary workflow facade: `text`, `run`, `invoke`, `stream`,
  and locally serialized bound Sessions.
- `Client` and `InvocationHandle` expose durable operations, transcript
  drains, collection iterators, configurable waits, and resumable streams.
- `client.raw()` is the complete generated Runtime transport and low-level
  escape hatch.

## Install

```bash
npm install @deepnoodle/nvoken
```

Node.js 20 or newer is required.

Resolve the identity-only Agent anchor without admitting work:

```ts
const agents = await client.listAgentIdentities({ agentKey: "support" });
const identity = await client.getAgentIdentity(agents.items[0].id);
```

The identity contains only its nvoken ID, host-owned key, and creation time.
Instructions, models, tools, and provider keys remain per Invocation.

Opt an agent into the fixed guarded public-web reader with no schema or
transport configuration:

```ts
import { Client, fetchTool } from "@deepnoodle/nvoken";

const agent = new Client().agent({
  agentKey: "research",
  instructions: "Use nvoken_fetch for public URLs, then summarize the source.",
  tools: [fetchTool()],
});
```

The Runtime accepts only `{name: "nvoken_fetch", mode: "builtin"}`. It owns
public-address checks, up to five guarded redirects, one transient retry,
HTML-to-Markdown conversion, and the ten-second and 64 KiB limits.

## First response

After following the
[nvoken quickstart](https://nvoken.com/docs/quickstart), configure the App API
base URL, API key, provider, and model:

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
  defaultModel: { provider: "anthropic", id: "claude-sonnet-5" },
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

Set an explicit portable temperature on the request or Agent:

```ts
const agentDefinition = {
  model: { provider: "anthropic", id: "claude-haiku-4-5" },
  sampling: { temperature: 0 },
};
```

Omit `sampling` to preserve the provider default. Check
`selected.controls?.sampling.temperature` first; absent controls are unknown,
and unsupported or unknown selections fail before durable admission. The
portable range is `[0,1]`. `top_p` and stop sequences are intentionally absent;
`limits.maxOutputTokens` is the output guardrail.

Reasoning is typed and fail closed:

```ts
const agentDefinition = {
  model: { provider: "anthropic", id: "claude-opus-5" },
  reasoning: { effort: "high" as const },
};
```

Check `selected.controls?.reasoning` first. `budgetTokens` requires a larger
explicit `limits.maxOutputTokens`. Omission preserves provider defaults;
unsupported values and combinations are rejected without aliasing. OpenAI
reasoning remains unavailable until its complete continuation representation
is durable.

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

Install restart-stable compaction on a new or existing Session:

```ts
const handle = await agent.invoke("hello", {
  sessionKey: "support:123",
  sessionOptions: {
    compaction: { triggerTokens: "auto" as const },
  },
});
```

An exact integer trigger and optional same-provider summary model are also
typed. A Session without a policy accepts late opt-in; once installed, the
policy is immutable. Supplied options on an existing Session must equal stored
values or admission returns `session_options_conflict`.

Summary usage appears in Session usage rather than Invocation usage; Session
messages, streams, and results remain unchanged. Read applied and fell-through
diagnostics with `client.listSessionCompactions(sessionId)`.

Correlate a turn with your own records using per-call `metadata`:

```ts
const handle = await agent.invoke("Summarize this issue.", {
  metadata: { board: "brand-2026", traceId: "018f-4a" },
});
```

It is part of the admitted input, so it is immutable and material to
idempotency: a replay carrying different metadata conflicts rather than
updating it. That is why it is per-call rather than an Agent-level default.

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
- `result()`, `outputText()`, and `listMessages()` for composed result reads;
- `submitToolResults()` and `cancel()` for explicit orchestration;
- `stream()` for the lower-level Invocation event stream.

`client.listSessionMessages(sessionId)` is the Session-scoped message read;
`handle.listMessages()` reads only messages owned by that Invocation.

The SDK generates an idempotency key before admission and reuses the exact body
and key on ambiguous retries. The key is exposed as `handle.idempotencyKey`.
Supply `idempotencyKey` yourself only when the application needs to reproduce
the same logical admission across a process boundary.

Choose a per-turn provider key on the Agent or an individual invoke.
Only `caller_ephemeral` carries secret material:

```ts
const agent = client.agent({
  agentKey: "support",
  providerKeys: [{
    provider: "openai",
    source: "caller_ephemeral",
    key: { apiKey: providerKey },
  }],
});
```

Stored nonsecret selections use `app_byok`, `tenant_byok`, or `platform`.

Manage the same reusable provider keys without dropping to the generated
client:

```ts
const storedKey = await client.createProviderKey({
  provider: "anthropic",
  scope: "app",
  apiKey: process.env.ANTHROPIC_API_KEY!,
});

const usage = await client.getProviderKeyUsage(storedKey.id);
const rotated = await client.rotateProviderKey(storedKey.id, {
  apiKey: process.env.NEXT_ANTHROPIC_API_KEY!,
  overlapSeconds: 300,
});
await client.revokeProviderKey(rotated.id);
```

The facade also provides `getProviderKey()`,
`listProviderKeys()`, and the async `providerKeyPages()`
iterator. Lifecycle retries reuse one generated idempotency key. Secret
material appears only in create and rotate requests and never in returned
metadata.

Manage nvoken's own API credentials with an Operator key through the Identity
surface:

```ts
const issued = await client.createCredential({
  name: "production worker",
  profile: "runtime",
  operations: ["create_invocation"],
});
const page = await client.listCredentials({ status: "active", limit: 100 });
```

`getCurrentIdentity()`, `getCredential()`, `rotateCredential()`, and
`revokeCredential()` complete the lifecycle. Create and rotate preserve the
one-time `secret`, `deliveryExpiresAt`, and `replayed` fields; store the secret
before its delivery deadline. `client.raw().identity` is the generated
low-level transport, and `credentialPages()` iterates the cursor envelope.

## Reuse an Agent Definition

Every turn runs against an Agent Definition: instructions, model, sampling,
reasoning, tool choice, limits, tools, MCP servers, provider tools, and output
schema. Sending it inline is the ordinary path. Register it instead when many
turns share one configuration and you would rather send a short ID:

```ts
const resource = await client.createAgentDefinition({
  instructions: "Be concise and helpful.",
  model: { provider: "anthropic", id: "claude-sonnet-5" },
}, "support-definition-v1");

const handle = await client.invoke({
  agentKey: "support",
  sessionKey: "ticket-483",
  input: "Why was I charged twice?",
  agentDefinitionId: resource.id,
});
```

Creating a definition starts no turn and creates no Agent, Session, or message.
The resource has a stable ID and an increasing revision. Use
`getAgentDefinition()` and `updateAgentDefinition()` to read and replace it.
An idempotency key makes create retries safe; equal content under another key
creates an independent resource.

Send exactly one of `agentDefinition` and `agentDefinitionId`. The types make
the pair mutually exclusive, and the facade rejects a request carrying both or
neither before it reaches the network. `Agent` supports the same choice; host
tool handlers remain local when a reusable resource supplies the declarations.

## Record changing application state

Keep `instructions` static. Product state that changes between turns — a board
snapshot, customer facts, the current policy — belongs in `context`:

```ts
const support = client.agent({
  agentKey: "support",
  instructions: "Be concise and helpful.",
});

const answer = await support.text("Can I refund the duplicate charge?", {
  sessionKey: "ticket-483",
  context: [
    { name: "customer", tier: "contextual", content: "plan: pro" },
    { name: "refund-policy", tier: "operator", content: "Self-serve refunds cap at 50 USD" },
  ],
});
```

A name is a stable identity. Send it once and nvoken records it as a leading
message the model reads as `app-customer`; omit that reserved prefix here.
Send the same name again only when its value changes — a byte-identical resend
is accepted but adds no message, so a stateless host may resend its whole
snapshot every turn and get the same transcript as a host that tracks changes.

Use `contextual` for conversation-adjacent facts and `operator` for policy or
other application-authoritative state. Context is Session history, not an Agent
Definition field: it never changes `agentDefinitionId`, and later turns keep
sending it to the model even when you omit it. That is what keeps the prompt
prefix stable enough for provider caching, which rewriting the same state into
`instructions` would break on every turn.

The list is order-sensitive and part of idempotency, so a replay that reorders
or edits an item conflicts rather than updating it. A request accepts at most
eight items, 8 KiB per item, and 16 KiB in total; the SDK checks all three
before the request leaves the process. A Session may accumulate at most 16
distinct names, which only the service can check. Retire a name by superseding
it with a short current value such as `"ticket: closed"`.

## Remote MCP tools

Probe the projected catalog, then attach the same declaration to an Agent:

```ts
import { Client, mcpServer } from "@deepnoodle/nvoken";

const server = mcpServer({
  name: "support",
  url: "https://mcp.example.com/rpc",
  allowedTools: ["lookup_order"],
  timeouts: { discoverySeconds: 10, callSeconds: 30 },
});

const headers = { Authorization: `Bearer ${mcpToken}` };
console.log((await client.listMcpTools(server, headers)).tools);

const support = client.agent({
  agentKey: "support",
  instructions: "Use support tools when needed.",
  mcpServers: [server],
  mcpServerHeaders: [{ name: "support", headers }],
});
```

The declaration carries no secrets. An Agent Definition may be reused across
turns, so authentication headers travel per Invocation in
`mcpServerHeaders`, keyed to the server name. They are one-Invocation secret
material and are absent from durable Agent Definitions, responses, streams,
transcripts, errors, and logs. The SDK checks the name and header values, and
that every named server exists, before the request leaves the process.

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

For an intentional replace/regenerate action, bypass the bound Session queue
with a new idempotency key and the typed policy:

```ts
const handle = await agent.invoke("Try that answer again.", {
  sessionKey: "ticket-483",
  idempotencyKey: "ticket-483:regenerate-2",
  ifActive: "supersede",
});
```

Omission or `"reject"` preserves the default conflict response. Supersession
atomically cancels active work and admits the successor; the Runtime credential
must also allow cancellation.

`ifActive: "interrupt"` is the keep-the-work variant: the active Invocation
stops at its next execution seam and settles `completed` with `stopReason`
`"interrupted"`, so the replacement turn builds on what it already produced.
`handle.interrupt()` asks for the same graceful stop without admitting a
replacement, and `Invocation.stopReason` names why any turn ended.

A turn can also stop without ending: `"incomplete"` means the Runtime enforced
a budget at a seam, with `stopReason` naming which one. It is terminal — the
wait helpers stop there — and its work is kept, so treat it as an unfinished
answer rather than an error. `SessionMessage.phase` says which assistant
message was the reply: `"final_answer"` on the one that ended a completed
turn, `"commentary"` on everything else, so an incomplete turn has none.

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

If a requested host tool has no handler, `run()` cancels the parked Invocation
before throwing `MissingToolHandlerError` with the handle and pending call.
Set `leaveWaitingOnMissingHandler` only when another worker deliberately owns
the call. The low-level `invoke()` path remains available for queues, browsers,
and external workers.

Stable ToolCall IDs let a handler make its own side effects idempotent. They do
not make arbitrary side effects exactly-once.

## Callback tools

A callback tool runs on an HTTPS endpoint nvoken posts to, rather than on a
worker that polls for parked calls. Verify the signed delivery, then answer:

```ts
import { acknowledgeCallback, callbackResult, verifyCallback } from "@deepnoodle/nvoken";

const delivery = await verifyCallback(signingKey, request.headers, rawBody);
const reply = callbackResult(await runMigration(delivery.envelope.input));

return new Response(reply.body, { status: reply.status });
```

`callbackResult(content, isError?)` settles the ToolCall inline and the turn
resumes as soon as nvoken records the reply. `acknowledgeCallback()` returns
`202` with no body instead: it accepts the delivery without settling the call,
for work that will outlive the App's callback reply deadline. Settle it later
with `client.submitToolResults()`, reusing the delivery's ToolCall ID.

Acknowledging trades away the fail-loud guarantee. nvoken marks an
unacknowledged delivery failed once its retries are exhausted, so the turn
always moves on. An acknowledged call instead waits under your responsibility,
bounded only by the Invocation's `limits.waitingTimeoutSeconds`. Acknowledge
only when something durable will settle the call.

An acknowledged call appears in a waiting Invocation's pending calls the same
way a host call does. `Agent` skips the callback tools its own definition
declares rather than dispatching them locally.

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
throws a typed validation error before admission. `Client.invoke` and every
Agent path then preflight nvoken's bounded subset before transport. Call
`preflightOutputSchema(schema)` directly when loading configuration. Failure
uses code `schema_preflight_failed` and safe `details` with the portable issue
`code`, RFC 6901 `path`, and optional `keyword`. Standard Schema output is
converted exactly once before this check. `client.raw()` remains a generated
escape hatch and relies on Runtime validation.

## Streaming

`agent.stream()` admits with a plain POST and then follows the one stream,
filtered to the turn that acknowledgement named, as a typed async iterable. Two
frames are the whole consumer:

```ts
for await (const event of agent.stream("Write a haiku.")) {
  if (event.type === "message.delta" && event.kind === "text") {
    process.stdout.write(event.delta);
  }
  if (event.type === "transcript.update") {
    for (const change of event.invocationChanges) {
      if (change.status === "completed") {
        console.log(`\n${change.usage?.outputTokens ?? 0} tokens`);
      }
    }
  }
}
```

`transcript.update` carries durable state, `message.delta` is a discardable
preview, and `stream.*` events control transport recovery. **A turn is over
when a change for it carries a terminal status**, and the iterable ends right
behind that change; read the Invocation if you want the composed result. The
SDK reconnects with the latest durable cursor; `stream.resync` means discard
buffered previews and wait for the saved messages. Host handlers configured on
the Agent are dispatched whenever the turn parks. Disconnecting the caller
never cancels the turn.

Use `handle.stream()` to follow one already-admitted turn. The Session stream
and its `Reducer` follow every turn in a conversation; that form is a
subscription, so it stays open while the Session is idle and you leave it by
breaking out of the loop. The
[streaming and recovery guide](https://nvoken.com/docs/guides/streaming-and-recovery)
states the preview, resync, cursor, and authoritative-ending guarantees.

Use `handle.streamWithOptions({ deltas: false })` for durable frames only.
List several states as one union with
`client.listInvocations({ status: ["queued", "running", "waiting"] })`;
reordering that equivalent set does not change cursor identity. Session get
and list responses expose typed nullable `usage`, computed from durable
Invocation usage as a convenience estimate rather than a billing ledger.

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

The wire contract uses `agent_key`, `tenant_key`, `model.id`, `limits`,
and tool mode `host`. The TypeScript facade exposes their idiomatic camel-case
forms.
