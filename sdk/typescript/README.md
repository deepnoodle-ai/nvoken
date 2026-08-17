# nvoken TypeScript SDK

An Invocation is one durable turn by a deliberately created, tenant-scoped
Agent. An Agent binds your `agentKey` to one App-owned, versioned Agent
Definition; a Session is one conversation with that Agent.

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

An `Agent` is one tenant's instance of an Agent Definition — the server record
and the object that runs its turns, which are the same thing. Declare one from
the keys you already own and it creates its record the first time you use it:

```ts
const support = client.agent({
  tenantKey: userId,
  agentKey: "support",
  definitionKey: "support",   // the Definition this instance follows
  tools: [lookupOrder],       // this process's handlers; never on the server
});
```

An Agent's identity and configuration live on the server; its tool *handlers*
are supplied by whichever process runs the turn. That is the only asymmetry —
everything else about the Agent is the record, readable at `agent.resource` and
through `agent.id`, `agent.agentKey`, `agent.name`, and `agent.pinnedRevision`.

`agent.ensure()` creates the record at a moment you choose instead of on first
use. It never mutates: the same keys and Definition resolve onto what exists, a
different Definition is `agent_key_conflict`, an archived record is
`agent_archived`, and a declared `pinnedRevision` that disagrees with the record
is refused rather than silently ignored. Instances follow their Definition's
latest revision unless `pinnedRevision` opts one tenant out, so revising the
Definition is the rollout and a per-tenant fleet never needs walking.

Reading one back gives the same type, ready for this process's handlers:

```ts
const agents = await client.listAgents({ agentKey: "support" });
const support = (await client.getAgent(agents.items[0].id)).withTools([lookupOrder]);
```

`AgentResource` is that record as the wire carries it — what `agent.resource`
holds, what `client.getAgentResource()` returns, and what `JSON.stringify(agent)`
produces.

Opt an agent into the fixed guarded public-web reader with no schema or
transport configuration:

```ts
import { Client, fetchTool } from "@deepnoodle/nvoken";

const client = new Client();
const definition = await client.createAgentDefinition({
  definitionKey: "research",
  name: "Research",
  instructions: "Use nvoken_fetch for public URLs, then summarize the source.",
  model: { provider: "anthropic", id: "claude-sonnet-5" },
  tools: [fetchTool()],
});
const agent = client.agent({ agentKey: "research", definitionKey: definition.definitionKey });
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

const client = new Client();

// Registering the Definition is keyed and idempotent: definitionKey is
// unique, so restating it returns what it already names. Safe to run on
// every deploy.
await client.createAgentDefinition({
  definitionKey: "support",
  instructions: "Be concise and helpful.",
  model: "anthropic/claude-sonnet-5",
});

// The Agent is declared from its keys and creates its record on first use.
const agent = client.agent({ agentKey: "support", definitionKey: "support" });

console.log(await agent.text("Why was I charged twice?"));
```
<!-- public-quickstart:end -->

`new Client()` resolves configuration in this order:

1. explicit constructor options;
2. `NVOKEN_*` process environment variables;
3. a `.env` whose first line is the marker written by `nvokend quickstart`.

Both `baseUrl` and `apiKey` are required after configuration is resolved. If
either is missing, construction fails with a validation error before any
network request is attempted.

It never loads an arbitrary `.env` or mutates `process.env`. `NVOKEN_API_KEY` is
required.

There is no client-level default model. An Agent Definition is durable,
versioned, App-owned configuration shared across tenants, so the model it
publishes must come from the definition rather than from whichever process
happened to publish it. Name it per definition, and override per turn:

```ts
const client = new Client({
  baseUrl: "https://runtime.example.com",
  apiKey: process.env.RUNTIME_KEY,
  // Every response the client sees, for latency and status metrics.
  onResponse: ({ method, url, status, durationMs }) =>
    metrics.record({ method, url, status, durationMs }),
});
```

A model is nameable as `"provider/id"` or as the object form, anywhere a model
appears:

```ts
await client.createAgentDefinition({
  definitionKey: "support",
  instructions: "Be concise and helpful.",
  model: "anthropic/claude-sonnet-5",
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

Set an explicit portable temperature on the Agent Definition or safe per-turn
overrides:

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
const handle = client.invocation("inv_...");
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

Choose a per-turn provider key on the local Agent facade or an individual invoke.
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

## Define and instantiate an Agent

Every turn runs against an App-owned, versioned Agent Definition. Create a
tenant-scoped Agent instance that binds to the template before admitting work:

```ts
const resource = await client.createAgentDefinition({
  definitionKey: "support",
  name: "Support",
  instructions: "Be concise and helpful.",
  model: { provider: "anthropic", id: "claude-sonnet-5" },
});

const support = client.agent({
  agentKey: "support",
  definitionKey: resource.definitionKey,
});

const handle = await support.invoke("Why was I charged twice?", {
  sessionKey: "ticket-483",
});
```

`AgentDefinition` is flat and matches the wire, and a read gives back the same
fields plus `id`, `revision`, and timestamps, so a change is a read, a spread,
and a write:

```ts
const current = await client.getAgentDefinition(resource.id);
await client.updateAgentDefinition(
  current.id,
  { ...current, instructions: "Be concise." },
  { expectedRevision: current.revision },
);
```

An update replaces the whole resource, so send back everything you want kept;
spreading the resource is what keeps it whole, and the read-only fields it
carries are dropped on the way to the wire. `expectedRevision` travels as
`If-Match`, so a concurrent write fails rather than overwriting.

Both writes are ensure-shaped: restating what nvoken already holds publishes
nothing and returns the current revision. So a deploy step that owns its
definitions in source does not read anything first — `syncDefinitions()` writes
them all and reports what moved:

```ts
for (const { definitionKey, outcome } of await client.syncDefinitions(definitions)) {
  if (outcome !== "unchanged") console.log(`${definitionKey}: ${outcome}`);
}
```

Each definition costs one call, or two when its contents differ: the create
conflict names the resource to replace, so nothing has to be looked up. Do not
compare a definition against what you read back to decide whether to write —
nvoken canonicalizes one before comparing it, and a second copy of that rule in
your code is free to disagree the first time either side gains a field. Write
unconditionally and read the outcome.

Creating a Definition starts no turn. It has an immutable `definitionKey`, a
stable ID, and an increasing revision; `getAgentDefinitionRevision()` reads
historical revisions. Updating a Definition does not rewrite an Agent's
binding. An Agent or Session may pin a revision, while an Invocation may select
one revision for that turn. Safe `overrides` cover model, sampling, reasoning,
tool choice, limits, and output schema; they cannot expand tools, data access,
memory authority, or instructions. Host tool handlers remain local to the SDK
facade.

## Record changing application state

Keep `instructions` static. Product state that changes between turns — a board
snapshot, customer facts, the current policy — belongs in `context`:

```ts
const support = client.agent({
  agentKey: "support",
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
Definition field: it never changes `definitionId`, and later turns keep
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

Probe the projected catalog, then keep the declaration in the Agent Definition
and pass only its one-turn secret headers through the local facade:

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
  tenantKey: "acme",
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
const chat = support.session({ sessionKey: "ticket-483" });

await chat.text("Remember that my code is ORCHID-724.");
console.log(await chat.text("What is my code?"));
```

You can also bind a durable Session ID:

```ts
const chat = agent.session({ sessionId: "sess_..." });
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

// The Agent Definition already declares lookup_order; the handler stays local.
const support = new Client().agent({
  agentKey: "support",
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
for work that will outlive this tool's reply deadline — its declared
`timeout_seconds`, or the App's default when it declares none. Settle it later
with `client.submitToolResults()`, reusing the delivery's ToolCall ID.

Every delivery names its tool inside the signed body, so one endpoint can serve
several tools: dispatch on `delivery.toolName` rather than on a path suffix
nothing signs.

Acknowledging trades away the fail-loud guarantee. nvoken marks an
unacknowledged delivery failed once its retries are exhausted, so the turn
always moves on. An acknowledged call instead waits under your responsibility,
bounded only by the Invocation's `limits.waitingTimeoutSeconds`. Acknowledge
only when something durable will settle the call.

An acknowledged call appears in a waiting Invocation's pending calls the same
way a host call does, so `answerableToolCalls` includes it. `hostToolCalls` is
the narrower set you must run yourself — answerable and `mode: "host"` — and
`Agent` dispatches exactly that, whatever its own definition declares.

## A callback endpoint, whole

`verifyCallback` is the signature. Around it every receiver writes the same
key table, the same deduplication, and the same reply discipline —
`createCallbackReceiver` is that, so you write the tools:

```ts
import { callbackResult, createCallbackReceiver } from "@deepnoodle/nvoken";

const receiver = createCallbackReceiver({
  // Two entries span a rotation: nvoken mints the next version while still
  // signing with the current one.
  keys: [
    { keyId: env.SIGNING_KEY_ID, version: 2, secret: env.SIGNING_SECRET },
    { keyId: env.SIGNING_KEY_ID, version: 1, secret: env.SIGNING_SECRET_PREVIOUS },
  ],
  store: ticketReplies,
  tools: {
    open_ticket: async (delivery) => {
      const board = delivery.authorizationContext?.board;
      return callbackResult(await openTicket(board, delivery.envelope.input));
    },
  },
});

export default {
  async fetch(request: Request): Promise<Response> {
    const { reply, outcome, reason } = await receiver.handle(
      request.headers,
      new Uint8Array(await request.arrayBuffer()),
    );
    log("nvoken callback", { outcome, reason });
    return new Response(reply.body ?? null, { status: reply.status });
  },
};
```

`handle` never throws. Everything that can go wrong is a status nvoken
understands, and the returned `outcome` — `settled`, `acknowledged`,
`replayed`, `refused`, `failed` — is what the status alone cannot tell you,
with a stable `reason` token for the log line.

The statuses are decisions about whether nvoken tries again:

| situation | status |
| --- | --- |
| no keys configured | 503 — an operator error, still fixable in the window |
| signing identity not held | 401 — redelivery reproduces it |
| signature or envelope invalid | 401 — the same bytes fail the same way |
| no handler for the signed tool name | 400 — nothing here can ever run it |
| a tool answered, or failed | 200 — settle it, carrying `is_error` if it failed |
| your handler threw | 503 — you failed, not the tool |

A failed tool is not a failed receiver: settle it with
`callbackResult(reason, true)` and the model can correct itself, where a 5xx
only has nvoken deliver the same doomed call again.

`store` is a `find`-then-`putIfAbsent` pair, in that order. `find` runs before
your tool does, because delivery is at least once and re-running the tool
repeats every effect it had; `putIfAbsent` runs after, because two deliveries of
one ToolCall can be in flight at once and only one reply may win. Omit `store`
only when every tool on the endpoint is safe to run twice.

### Authorizing a delivery

Verification proves the delivery came from nvoken. It does not say what the
work belongs to, and the tool input cannot either — the model wrote it.
`session_options.authorizationContext` is what you asserted when the Session was
created, and it arrives inside the signed body as a **sibling** of `nvoken`:

```json
{
  "nvoken": { "tool_name": "open_ticket", "tenant_key": "acme" },
  "authorization_context": { "board": "brd_9f21" },
  "input": { "board": "brd_9f21", "ticket": "A-42" }
}
```

Everything inside `nvoken` is a fact nvoken minted; this is a value you asserted
and nvoken carried unchanged. Signing proves it reached you as recorded, not
that it is true. Which gives the rule:

> **A value repeated in tool input may only agree with the authorization
> context, never establish it.**

Checking that the two agree is reasonable. Reading the board out of `input` when
the context is absent is not. Authorizing from the signed sibling is also what
removes the per-delivery `getInvocation` a receiver otherwise needs to recover
which of your objects the work is for.

[Receiving signed deliveries](../../docs/reference/callback-receivers.md) is the
long form, language-neutral.

## Invocation webhooks

A turn that ends tells you so, without you holding a connection open to hear
it. Ask for it when you start the turn:

```ts
const invocation = await client.createInvocation({
  agentKey: "support",
  input: "Where is my order?",
  webhook: { url: "https://example.com/nvoken/webhooks" },
});
```

Omitting `events` selects all three. `invocation.ended` fires once when the
turn reaches a terminal status; `invocation.waiting` fires when it needs a host
tool run; `invocation.budget_hold` fires when a spending limit stopped it.

Receiving one is the same verification you already wrote for callbacks — the
signature scheme is identical, and `verifyWebhook` is the same code path with a
different body check:

```ts
import { acceptWebhook, retryWebhook, verifyWebhook, webhookSupersedes } from "@deepnoodle/nvoken";

const delivery = await verifyWebhook(webhookSigningKey, request.headers, rawBody);
const applied = await lastAppliedSequence(delivery.invocationId);
if (webhookSupersedes(delivery, applied)) {
  await settle(delivery.invocationId, delivery.envelope.invocation, delivery.sequence);
}
return new Response(null, acceptWebhook());
```

The key is the App's `webhook`-purpose signing key, not its `callback` key. A
receiver serving both endpoints holds two, and must not try either against the
other's deliveries.

Three rules make a receiver correct:

**Fold by sequence, not by arrival.** Delivery is at least once, so the same
transition can arrive twice and a redelivery can land after a later one. Keep
the highest `sequence` you have applied per Invocation and apply only what
`webhookSupersedes` accepts. That is the deduplication too — a repeat carries a
sequence you already applied — so nothing else is needed to make handling
idempotent.

**The payload is a pointer, not a copy.** It carries `status`, `stop_reason`,
`failure_code`, `waiting_tool_call_ids`, and `credit_block`, and deliberately
nothing else: no transcript, no output text, no usage. Read `getInvocation` or
`getInvocationResult` when you need more, so you are reconciling against the
authoritative record rather than a staler copy of it.

**Answer `retryWebhook()` when you could not record it.** Any 5xx is
redelivered, as are 408, 425, and 429. Every other non-2xx answer is permanent
and that transition is never delivered again, so a 400 from a receiver that was
merely busy is a settlement you silently lost.

Retries are bounded, so webhooks alone are not a settlement guarantee.
`client.listEndedInvocations` is the backstop: it walks turns in the order they
ended, so a delivery that never landed is one you still find.

`createWebhookReceiver` is the callback receiver's twin — same key table, same
reply discipline — for the endpoint that has more than one event:

```ts
import { createWebhookReceiver } from "@deepnoodle/nvoken";

const receiver = createWebhookReceiver({
  keys: [{ keyId: env.WEBHOOK_KEY_ID, version: 1, secret: env.WEBHOOK_SECRET }],
  events: {
    "invocation.ended": (delivery) => settleInOneTransaction(delivery),
    "invocation.budget_hold": (delivery) => alertOnBudgetHold(delivery),
  },
});
```

A handler that returns answers 200; one that throws answers 503 and nvoken
delivers again. An event you subscribed to but registered no handler for
answers 200 with outcome `ignored` — retrying it would only spend nvoken's
bounded attempts reaching the same absent handler.

The sequence fold stays yours, deliberately: the compare and the write have to
happen in the same transaction as the state they guard, and the receiver cannot
open it. Call `webhookSupersedes` inside yours, and record nothing when it says
no — a superseded delivery is still a delivery, and still answers 200.

## Structured output and schema libraries

Raw JSON Schema keeps an application type:

```ts
interface Classification {
  category: "billing" | "other";
  needsHuman: boolean;
}

const classificationSchema = defineJsonSchema<Classification>({
  type: "object",
  properties: {
    category: { type: "string", enum: ["billing", "other"] },
    needsHuman: { type: "boolean" },
  },
  required: ["category", "needsHuman"],
  additionalProperties: false,
});

// The classifier Agent's Definition owns its instructions; this turn safely
// replaces only its output schema.
const classifier = new Client().agent<Classification>({
  agentKey: "classifier",
});

const result = await classifier.run("I was charged twice.", {
  overrides: { outputSchema: classificationSchema },
});
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

const classifier = new Client().agent<Classification>({
  agentKey: "classifier",
});

const result = await classifier.run("I was charged twice.", {
  overrides: { outputSchema },
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
  agentId: "agent_...",
  sessionKey: "ticket-483",
});

for await (const session of client.sessionPages({ tenantKey: "acme" })) {
  console.log(session.id);
}

for await (const message of client.messagePages("sess_...", { limit: 100 })) {
  console.log(message.sequence, message.role);
}

const transcript = await client.drainTranscript("sess_...", {
  cursor: previousCursor,
  pageSize: 100,
});
```

`drainTranscript()` holds one fixed snapshot cut across pages and returns the
next durable `resumeCursor`.

## Browser-direct access

Your page can talk to nvoken itself, with no server of yours in the path. Mint
a short-lived grant in backend code and hand the browser that:

```ts
const token = await mintClientToken(clientKeySeed, {
  appId,
  keyId: clientKeyId,
  subject: user.id,          // from your session, never from the request
  tenantKey: user.workspaceId,
  agentKey: "support",
  operations: ["create_invocation", "get_session_transcript"],
  lifetimeMs: 10 * 60 * 1_000,
});
```

The page then uses the browser entry, which takes a function so it can refresh:

```ts
import { createBrowserClient } from "@deepnoodle/nvoken/browser";

const client = createBrowserClient({
  baseUrl: "https://api.nvoken.example",
  clientToken: async () => (await fetch("/api/nvoken-token", { method: "POST" })).json()
    .then((body) => body.token),
});
```

It refuses an `nvk_` API key outright. A machine credential in a page is
readable by everyone who loads it and reaches every tenant in the App, and
nothing about the request would look wrong — it would simply work, for anyone.

`nvoken client-key generate <app-id> --name web` produces the keypair and
registers its public half in one step. The private seed is the App's browser
authority, so it belongs in backend configuration and never in a bundle.

Three things are worth deciding rather than defaulting. `operations` is
required: nvoken reads an absent list as every operation a browser may perform,
so this SDK refuses to spell "I did not think about scope" the same way as
`allBrowserOperations()`. `sessionId` confines the token to one conversation,
which a single-conversation UI should set. And a lifetime is capped at fifteen
minutes, because short lifetimes are the whole safety story of a bearer token
in a page.

**Invocation webhooks stop being optional here.** The browser holds the stream,
so your backend never observes settlement any other way. See
`examples/typescript-browser-direct` for both halves.

## Acting for one tenant or one end user

An app-wide credential can reach every tenant in its App, so an id that arrives
from the wrong place — a stale link, a mixed-up webhook, a tampered form field
— is an id it can act on. Say the scope once instead of re-reading the resource
before every call:

```ts
const tenant = client.scoped({ tenantKey: "acme", userKey: "user-7c1f" });
await tenant.getSession(sessionIdFromTheRequest);
```

Anything outside the scope is reported as `not_found`, so a Session or
Invocation belonging to somebody else cannot be read, cancelled, interrupted,
forked, answered, or erased — and you learn nothing about whether the id
exists. Writes take the same scope: an omitted `tenantKey` or `userKey`
inherits it, and one naming somebody else is refused.

A scope may only narrow. A credential already bound to one tenant refuses a
scope naming another with `forbidden` rather than silently returning nothing.
`client` itself is unchanged, so the unscoped one keeps doing administrative
reads. Browser tokens already carry their tenant and end user and neither need
this nor may send it.

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
