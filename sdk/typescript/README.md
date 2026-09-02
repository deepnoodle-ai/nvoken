# @deepnoodle/nvoken

The TypeScript SDK for nvoken's durable agent runtime.

The high-level API has four things to learn:

- an `Agent` is stored, versioned behavior;
- `inline()` runs one immutable behavior value without creating an Agent;
- a `Conversation` retains transcript continuity across Turns;
- a `Turn` is one durable execution that can be followed or recovered by ID.

Generated request-shaped APIs remain available under `client.raw()`.

## Install

```bash
npm install @deepnoodle/nvoken
```

Node.js 20 or newer is supported. The browser entry uses Web-standard APIs and
accepts a narrow client token, never a machine API key.

Machine callers can pass configuration directly:

```ts
import { Client } from "@deepnoodle/nvoken";

const client = new Client({
  baseUrl: "https://api.nvoken.com",
  apiKey: process.env.NVOKEN_API_KEY!,
});
```

When omitted, `baseUrl` and `apiKey` come from `NVOKEN_BASE_URL` and
`NVOKEN_API_KEY`.

## Run a stored Agent

Agent keys are caller-owned names inside an explicit owner namespace. Omitting
`ownedBy` means the App-owned namespace.

```ts
const support = await client.agent("support");

const answer = await support.text("How do I rotate an API key?", {
  tenant: "acme",
  user: "user-42",
});

console.log(answer);
```

A non-App-owned lookup states its owner exactly:

```ts
const tenantSupport = await client.agent("support", {
  ownedBy: { tenant: "acme" },
});

const personalCoach = await client.agent("coach", {
  ownedBy: { tenant: "acme", user: "user-42" },
});
```

Create an Agent and its first immutable revision atomically:

```ts
const reviewer = await client.agents.create({
  key: "invoice-reviewer",
  name: "Invoice reviewer",
  ownedBy: { tenant: "acme" },
  instructions: "Review invoices and answer concisely.",
  model: "anthropic/claude-sonnet-5",
  limits: { maxOutputTokens: 500, maxIterations: 4 },
});
```

Use `agent.publish()` to append a revision, and `archive()` or `restore()`
for lifecycle changes. `agents.create()` accepts an optional idempotency key;
the SDK generates one when omitted. Publication and lifecycle mutations use
SDK-managed idempotency keys.

## Run inline behavior

Inline behavior creates no Agent:

```ts
const classifier = client.inline<{ category: string }>({
  instructions: "Classify the request.",
  model: "openai/gpt-5",
  outputSchema: {
    type: "object",
    properties: { category: { type: "string" } },
    required: ["category"],
    additionalProperties: false,
  },
});

const result = await classifier.run("I was charged twice.", {
  tenant: "acme",
});

console.log(result.structuredOutput?.category);
```

Inline user or tenant default memory requires an explicit namespace because
there is no stored Agent identity from which to derive one:

```ts
const assistant = client.inline({
  instructions: "Use relevant account context.",
  model: "openai/gpt-5",
  memory: { defaultScope: "tenant", namespace: "customer-support" },
});
```

User-scoped memory also requires an explicit Turn user.

## Keep a Conversation

A Conversation is continuity, not behavior, actor identity, or memory
ownership. Bind it locally, then call it like the Agent:

```ts
const chat = support.conversation({
  tenant: "acme",
  user: "user-42",
  key: "ticket-1042",
  owner: "user",
  memory: { scope: "user" },
  limits: { totalTimeoutSeconds: 120, maxIterations: 6 },
});

await chat.text("My order has not arrived.");
await chat.text("It was order 4821.");
```

Use `{ id: "conv_…" }` instead of `{ key, owner }` to continue one exact
Conversation. Per-call limits may inherit or narrow bound limits; they cannot
widen them.

Conversation creation or lookup and Turn admission are one atomic request.
Calls through the same local Conversation identity are serialized in-process;
the service remains authoritative across processes.

## Bind host tools

Declare durable tool contracts in behavior, then bind process-local
implementations by exact name:

```ts
import {
  Client,
  defineHostTool,
  defineJsonSchema,
} from "@deepnoodle/nvoken";

interface LookupOrderInput {
  orderId: string;
}

const lookupOrder = defineHostTool<LookupOrderInput>({
  mode: "host",
  name: "lookup_order",
  description: "Look up one order.",
  inputSchema: defineJsonSchema<LookupOrderInput>({
    type: "object",
    properties: { orderId: { type: "string" } },
    required: ["orderId"],
    additionalProperties: false,
  }),
});

const orderSupport = client.inline({
  instructions: "Use lookup_order for order questions.",
  model: "anthropic/claude-sonnet-5",
  tools: [lookupOrder],
}).bindTools({
  lookup_order: async (input: LookupOrderInput, context) => ({
    orderId: input.orderId,
    state: await orders.state(input.orderId, context.toolCallId),
  }),
});

console.log(await orderSupport.text("Where is order 42?", {
  tenant: "acme",
}));
```

`result()` and `updates()` drive matching bound tools while attached.
`status()` is passive. If no compatible process remains, the durable Turn
waits until one reattaches or its configured waiting limit expires.

## Start, follow, and recover a Turn

`start()` returns after durable admission:

```ts
const turn = await support.start("Prepare the account summary.", {
  tenant: "acme",
  idempotencyKey: "account-summary:42:v1",
});

console.log(turn.id);
```

Read one passive snapshot:

```ts
const snapshot = await turn.status();
console.log(snapshot.status, snapshot.stopReason);
```

Follow reduced state without handling cursors or raw frames:

```ts
for await (const update of turn.updates()) {
  renderMessages(update.snapshot.messages);
  renderStatus(update.snapshot.status);
}
```

Wait for the authoritative terminal result:

```ts
const completed = await turn.result();
console.log(completed.text, completed.structuredOutput);
```

Recover later from only the durable ID and explicit access context. Constructing
the handle is synchronous and makes no request:

```ts
const recovered = client.turn("turn_…", {
  tenant: "acme",
  user: "user-42",
});

const completed = await recovered.result();
```

Stop a running Turn and keep what it produced:

```ts
const stopping = await recovered.interrupt();
console.log(stopping.status); // often still "running"
```

`interrupt()` returns the Turn's state as of the request. Mid-step the runtime
records the request and stops at the next checkpoint, so follow `updates()` or
`result()` for settlement rather than reading that status as final.
Interrupting a Turn that already ended returns it unchanged and does not throw.

`start()` returns a Turn carrying `admission`: the idempotency key, whether the
request was deduplicated, and the Conversation it resolved to. That last one is
the only place a `continue_or_create` caller learns which Conversation it landed
in. A Turn recovered with `client.turn(id)` has no admission to report, so
`admission` is `undefined` there.

A local timeout or abort only detaches the caller. It does not cancel durable
work. If admission transport is uncertain, `TurnTimeoutError.idempotencyKey`
retains the key needed to retry the exact logical request safely. If waiting
timed out after admission, the error also retains the Turn handle.

`TurnResult` and `TurnExecutionError.result` retain status, stop reason,
typed failure, messages, final-answer text, structured output, behavior source,
Conversation ID, MemorySpace ID, and retention information. `text()` throws
`NoOutputTextError` when a successful call has no final assistant text; its
`result` remains available for recovery and inspection.

## Exact APIs

Use `raw()` for request-shaped administrative or advanced operations:

```ts
const page = await client.raw().conversations.listConversations({
  tenantKey: "acme",
  ownerKind: "user",
  userKey: "user-42",
});

const memory = await client.raw().memorySpaces.resolveMemorySpace({
  resolveMemorySpaceRequest: {
    tenantKey: "acme",
    selector: { scope: "tenant", namespace: "support" },
  },
});
```

Exact Conversation lifecycle, MemorySpace lifecycle, Turn listing and controls,
raw pagination, conflict policies, and generated wire models stay behind this
door. The facade does not mirror every exact request field.

## Browser-direct access

Your backend mints a short-lived client token with `mintClientToken()`. The
page passes a token resolver to the browser entry:

```ts
import { createBrowserClient } from "@deepnoodle/nvoken/browser";

const browser = createBrowserClient({
  baseUrl: "https://api.nvoken.com",
  clientToken: async () => {
    const response = await fetch("/api/nvoken-token", { method: "POST" });
    if (!response.ok) throw new Error("could not obtain nvoken access");
    return (await response.json() as { token: string }).token;
  },
});

const answer = await browser.text("Hello");
```

A browser token pins authority such as Agent revision, tenant, user,
Conversation access, and memory access. Browser admission does not assert those
machine-only coordinates again. The browser client rejects `nvk_` machine
keys before transport.

See [the browser-direct example](../../examples/typescript-browser-direct/README.md)
for token minting, direct execution, transcript reads, and signed Turn webhook
handling.

## Headless conversation controller

`createBrowserClient` gives a page one Turn at a time. A chat needs the part
around it: the conversation survives a reload, a retry never sends the same
message twice, and every control knows whether it may be used right now. That
is what the controller is — state and transitions, no rendering, no framework.

```ts
import { createConversation } from "@deepnoodle/nvoken/browser";

const conversation = createConversation({
  client: browser,
  conversation: { id: conversationId },
});

conversation.subscribe(() => {
  const snapshot = conversation.getSnapshot();
  render(snapshot.messages, snapshot.previews);
  setComposerEnabled(snapshot.send.action.status === "enabled");
  setStopEnabled(snapshot.interruption.action.status === "enabled");
});

const receipt = await conversation.send("What changed since yesterday?");
console.log(receipt.turnId, receipt.conversationId);
```

The Conversation selection is required. Omitting it admits a standalone Turn
with no Conversation — a chat that never persists and never streams, which the
runtime reports as success.

For a visitor with no account, `createAnonymousConversation` needs a base URL
and an App id and nothing else. The page stores an opaque visitor token; no
application credential goes in the bundle, and the grant names the visitor's
canonical Conversation, so nothing selects one.

```ts
import { createAnonymousConversation } from "@deepnoodle/nvoken/browser";

const conversation = createAnonymousConversation({
  baseUrl: "https://api.nvoken.com",
  appId: "app_…",
  storage: "local",
});
```

What it guarantees:

- **The conversation survives the page.** Resuming is one transcript read
  plus a stream from the exact position that read observed, so a reload gets
  recent history and no gap and no replay. The read is the newest 50 messages
  on the wire, with older pages fetched on request, and every page reports the
  cursor of the cut the walk started from, so paging back never moves the
  stream's resume position.
- **A dropped stream comes back on its own.** The `online` event and a
  renewed anonymous grant each restart a stream that stopped on a failure;
  `reconnect()` is for when neither has.
- **Retry never duplicates a Turn.** A send whose outcome is unknown becomes
  `send.status === "uncertain"`, and `retrySend()` repeats the same input under
  the same idempotency key. `discardSend()` is the way out when retries keep
  failing: it reopens the composer without cancelling anything, and a Turn that
  was in fact admitted shows up through the stream like any other.
- **The UI never guesses.** Every action reports `enabled`, `in_flight`, or
  `disabled` with a stated reason.
- **Unknown states stay visible.** A Turn status this SDK version does not know
  is reported as `activity.status === "unknown"`, never read as finished.
- **Memory is bounded.** 500 messages, 8 previews, 64 KiB per preview, and one
  current lifecycle record per Turn. Eviction removes whole settled Turns,
  oldest first, and never cuts into a live one.

`getSnapshot()` returns a frozen value that is replaced rather than mutated, so
an identity comparison is a correct "did anything change" test. `loadEarlier()`
prepends an older window without moving the live stream position.
`startOver()` is anonymous-only and replaces the visitor; it overwrites stored
continuity only once the new grant exists, so a network failure never costs a
visitor the conversation they had. `destroy()` is silent and final.

Verify against your deployment whether an anonymous grant may read a
Conversation transcript and interrupt a Turn. The contract does not say. If
either is refused, the controller disables that one action with
`not_authorized` and leaves the rest working.

## Development

From the repository root:

```bash
npm ci --prefix sdk/typescript
npm run build --prefix sdk/typescript
npm test --prefix sdk/typescript
```

The focused SDK gate also compiles the root TypeScript examples:

```bash
sdk/scripts/check.sh
```
