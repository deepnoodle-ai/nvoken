# TypeScript SDK ergonomics and explicit facade surface

**Status:** Proposed facade refinements. The standalone Invocation contract and
the four-SDK `start()` and `bindSession()` foundations shipped in 0.29.0; the
identity, result, streaming, timeout, and raw-transport changes below have not.
**Date:** 2026-08-24
**Revised:** 2026-08-25 after reviewing the proposal against 0.29.0 and walking
the examples from the perspective of a developer new to nvoken.
**Reviewed version:** `@deepnoodle/nvoken` 0.29.0 at `60c1303a`
**Scope:** The handwritten TypeScript `Client`, `Agent`, bound Session, and
Invocation surfaces. Equivalent workflow operations in Go, Python, and Rust
remain in scope for parity. Generated APIs remain available through `raw()`.

[DIRECTION](DIRECTION.md) is the standing design direction and outranks this
proposal. [Design 006](006-sdk-write-shape-parity.md) establishes cross-SDK
parity. [Design 007](007-host-supplied-context.md) governs where host-supplied
facts belong.

## Verdict

Keep the Agent-first workflow, but make identity and provisioning explicit.
The common path should look like this:

```ts
const support = client
  .scoped({ tenantKey: customer.id })
  .agentByKey("support")
  .withTools([lookupOrder]);

const conversation = support.bindSession({ sessionKey: ticket.id });
const answer = await conversation.text("Why was I charged twice?");
```

Every argument has one meaning:

- `"support"` is a caller-owned Agent key. It does not also name an Agent
  Definition and it does not ask the SDK to create anything.
- `withTools()` attaches process-local handlers. It does not change the
  durable Agent record.
- `bindSession()` selects conversation continuity. Without it, the turn is a
  standalone Invocation.

An Agent must already exist before `agentByKey()` or `agentById()` can run a
turn. Provisioning stays a separate, explicit operation:

```ts
await client.scoped({ tenantKey: customer.id }).createAgent({
  agentKey: "billing-support",
  definitionKey: "customer-support",
});
```

The two keys are intentionally visible here because this operation names two
different durable concepts: the tenant's Agent being created or resolved and
the existing App-owned Definition it follows. Repeating the same value remains
valid, but it is no longer implied by a single string.

Recovery by an opaque nvoken ID is equally direct:

```ts
const support = client
  .agentById(savedAgentId)
  .withTools([lookupOrder]);
```

This replaces two earlier ideas in this document:

- `agent("support")` is rejected because it left creation and Definition
  selection implicit.
- `agent({ agentId }, { tools })` is rejected because two adjacent option
  objects make the boundary between durable identity and local behavior hard
  to see. Identity is selected first; local handlers are attached by a named
  method.

## Design rules

### One spelling, one meaning

A string accepted by `agentByKey()` is always an Agent key. An ID accepted by
`agentById()` is always an Agent ID. Neither spelling infers a Definition,
provisions a resource, or attaches runtime behavior.

The same rule applies elsewhere:

- `scoped()` sets the tenant and end-user boundary for requests made through
  the returned client.
- `bindSession()` selects a Session by ID or by caller-owned key.
- `start()` admits durable work and returns a handle.
- `run()` waits for a terminal result while executing local host tools.
- `text()` is the convenience form of `run()` that requires assistant text.
- `stream()` yields render-safe reduced state.
- `streamEvents()` exposes raw protocol events when a consumer needs them.
- `snapshot()` reads the Invocation as it exists now.
- `waitForResult()` waits for a terminal result.

### Durable configuration and local behavior do not share an options bag

Agent identity, Definition selection, revision pins, display names, and
provisioning belong to durable resource operations. Host tool handlers are
local code supplied by the process executing a turn. MCP secret headers and
other per-run secrets belong to invocation defaults or invocation options,
not to identity selection.

The facade should make those lifetimes visible instead of combining them in
`AgentOptions`.

### The easy path is standalone; conversation is explicit

An unbound Agent admits a standalone Invocation. It does not create a public
Session and it does not share history with another unbound call:

```ts
const first = await support.text("Classify this invoice.");
const second = await support.text("Classify this receipt.");
// Independent turns. `second` cannot see `first`.
```

Callers opt into conversation continuity with `bindSession()`:

```ts
const conversation = support.bindSession(
  { sessionKey: ticket.id },
  { retention: { ttlSeconds: 30 * 24 * 60 * 60 } },
);

await conversation.text("My order has not arrived.");
await conversation.text("It was order 4821.");
```

The wire-level `session` discriminator remains available on `Client.invoke()`
for callers that need exact request control. It should not also appear on
Agent methods, where it competes with `bindSession()`.

### High-level and wire-level surfaces have separate doors

The handwritten facade owns ordinary workflows. Generated APIs are a complete
escape hatch, not a second set of top-level properties competing in
autocomplete:

```ts
const { invocations, sessions, models } = client.raw();
```

`Client.invoke()` remains the request-shaped admission API. `Client.raw()` is
the only public route to generated transports. The facade must not simulate a
server guarantee with a read-before-write sequence; if a workflow requires new
atomicity, replay, or recovery behavior, change the HTTP contract first.

## Proposed TypeScript surface

The declarations below are directional TypeScript. They settle ownership and
method relationships; implementation details and generated wire types may use
different internal names.

### Client

```ts
interface Client {
  scoped(scope: Scope): Client;

  agentByKey<TOutput extends object = JsonObject>(
    agentKey: string,
  ): Agent<TOutput>;

  agentById<TOutput extends object = JsonObject>(
    agentId: string,
  ): Agent<TOutput>;

  createAgent<TOutput extends object = JsonObject>(
    request: CreateAgentRequest,
    signal?: AbortSignal,
  ): Promise<Agent<TOutput>>;

  invocation<TOutput extends object = JsonObject>(
    invocationId: string,
  ): InvocationHandle<TOutput>;

  invoke<TOutput extends object = JsonObject>(
    request: InvokeRequest<TOutput>,
    signal?: AbortSignal,
  ): Promise<InvocationHandle<TOutput>>;

  raw(): RawClient;
}
```

`agentByKey()` and `agentById()` construct local references and make no
request. `createAgent()` is the create-or-resolve operation. A missing Agent
named by `agentByKey()` fails when first used; it is never silently created
from an inferred Definition.

`invocation()` constructs a recovery handle locally. Its constructor is not a
public application API.

### Agent

```ts
interface Agent<TOutput extends object = JsonObject> {
  withTools(tools: readonly Tool<object>[]): Agent<TOutput>;

  bindSession(
    binding: { sessionId: string } | { sessionKey: string },
    options?: SessionOptions,
  ): BoundSession<TOutput>;

  start(
    input: InvokeInput,
    options?: AgentInvocationOptions,
  ): Promise<InvocationHandle<TOutput>>;

  run(
    input: InvokeInput,
    options?: AgentInvocationOptions,
  ): Promise<InvocationResult<TOutput>>;

  text(
    input: InvokeInput,
    options?: AgentInvocationOptions,
  ): Promise<string>;

  stream(
    input: InvokeInput,
    options?: AgentInvocationOptions,
  ): AsyncIterable<InvocationStreamUpdate<TOutput>>;
}

type AgentInvocationOptions = Omit<
  InvocationOptions,
  "session" | "ifActive"
>;
```

`AgentInvocationOptions` does not contain `session`. Session selection belongs
to `bindSession()` or the request-shaped `Client.invoke()` API.

`withTools()` returns an Agent with the supplied local handlers. It does not
mutate the original Agent, create a server resource, or imply that the Agent
Definition declares those tools. The server-owned Definition remains the
schema authority.

### Bound Session

```ts
interface BoundSession<TOutput extends object = JsonObject> {
  start(
    input: InvokeInput,
    options?: BoundInvocationOptions,
  ): Promise<InvocationHandle<TOutput>>;

  run(
    input: InvokeInput,
    options?: BoundInvocationOptions,
  ): Promise<InvocationResult<TOutput>>;

  text(
    input: InvokeInput,
    options?: BoundInvocationOptions,
  ): Promise<string>;

  stream(
    input: InvokeInput,
    options?: BoundInvocationOptions,
  ): AsyncIterable<InvocationStreamUpdate<TOutput>>;
}

type BoundInvocationOptions = Omit<
  AgentInvocationOptions,
  "retention" | "compaction" | "authorizationContext"
>;
```

A bound Session serializes calls made through SDK objects representing the
same effective Session identity. `BoundInvocationOptions` therefore does not
expose `ifActive`: a local queue would make `reject`, `supersede`, or
`interrupt` surprising or ineffective. Callers intentionally coordinating
concurrent work use `Client.invoke()` and the wire-level policy instead.

### Invocation handle and result

```ts
interface InvocationHandle<TOutput extends object = JsonObject> {
  readonly invocationId: string;

  snapshot(signal?: AbortSignal): Promise<InvocationSnapshot<TOutput>>;
  wait(options?: WaitOptions): Promise<TypedInvocation<TOutput>>;
  waitForAction(options?: WaitOptions): Promise<TypedInvocation<TOutput>>;
  waitForResult(options?: WaitOptions): Promise<InvocationResult<TOutput>>;

  stream(
    options?: StreamOptions,
  ): AsyncIterable<InvocationStreamUpdate<TOutput>>;

  streamEvents(
    options?: StreamOptions,
  ): AsyncIterable<InvocationStreamEvent<TOutput>>;
}

interface InvocationSnapshot<TOutput extends object = JsonObject> {
  invocation: TypedInvocation<TOutput>;
  messages: SessionMessage[];
  text: string | null;
  structuredOutput: TOutput | null;
  agentId: string;
  sessionId: string | null;
  contentExpiresAt: Date | null;
}

interface InvocationResult<TOutput extends object = JsonObject>
  extends InvocationSnapshot<TOutput> {
  handle: EndedInvocationHandle<TOutput>;
  admission?: {
    idempotencyKey: string;
    deduplicated: boolean;
  };
}
```

`Agent.run()`, `BoundSession.run()`, and
`InvocationHandle.waitForResult()` return the same terminal result type.
Admission metadata is present when the current process admitted the turn; it
may be absent on a handle restored from only an Invocation ID.

`snapshot()` is deliberately not called `result()`: it reads the Invocation's
current result projection and may return while the Invocation is still
running or waiting. `waitForResult()` is the terminal operation.

A standalone Invocation has `sessionId: null`. `contentExpiresAt` states when
its content is eligible for erasure. Session-bound Invocations may have no
content deadline, so that field is nullable too.

### Streaming

The default stream surface must be safe to render. It folds raw frames into a
snapshot and handles resyncs, saved messages, terminal transitions, and
execution-attempt changes:

```ts
for await (const update of support.stream("Draft a reply.")) {
  render(update.snapshot);
}
```

Consumers implementing protocol diagnostics or their own reducer opt into raw
events explicitly:

```ts
const handle = await support.start("Draft a reply.");

for await (const event of handle.streamEvents()) {
  recordProtocolEvent(event);
}
```

The stream is live state, not settlement authority. Consumers that need the
settled output use `run()` or `waitForResult()`, which reads the authoritative
terminal result.

## North-star workflows

These examples should compile in CI and supply the README examples. They are
usage proof, not the complete behavioral contract.

### Provision once, then run by Agent key

```ts
const tenant = client.scoped({ tenantKey: customer.id });

await tenant.createAgent({
  agentKey: "billing-support",
  definitionKey: "customer-support",
});

const support = tenant
  .agentByKey("billing-support")
  .withTools([lookupOrder]);

const answer = await support.text("Why was I charged twice?");
```

### Run a standalone structured task

```ts
const classifier = client.agentById(classifierAgentId);
const result = await classifier.run(invoice, {
  retention: { ttlSeconds: 60 * 60 },
});

console.log(result.structuredOutput);
console.log(result.sessionId); // null
```

### Continue a conversation

```ts
const conversation = client
  .scoped({ tenantKey: customer.id, userKey: customer.userId })
  .agentByKey("billing-support")
  .withTools([lookupOrder])
  .bindSession({ sessionKey: ticket.id });

await conversation.text("My order has not arrived.");
await conversation.text("It was order 4821.");
```

### Admit work now and recover it elsewhere

```ts
const handle = await client
  .agentById(workerAgentId)
  .start(job, { idempotencyKey: job.id });

await jobs.save({
  invocationId: handle.invocationId,
  idempotencyKey: job.id,
});
```

```ts
const handle = client.invocation(saved.invocationId);
const result = await handle.waitForResult();
```

### Use exact request semantics

```ts
const handle = await client.invoke({
  agentId,
  input: "Replace the active draft.",
  session: {
    mode: "continue",
    id: sessionId,
    ifActive: "supersede",
  },
  idempotencyKey,
});
```

## Behavioral contract

Compiling examples prove that the surface is usable. Conformance tests must
also prove requests, atomicity, retries, and recovery:

| Workflow | HTTP behavior | Retry and recovery guarantee |
| --- | --- | --- |
| `agentByKey()` / `agentById()` | No request | The reference contains identity only |
| `withTools()` | No request | Returns a new local wrapper; no durable mutation |
| `createAgent()` | One create-or-resolve request | Repeating the same identity and Definition resolves the same Agent; a conflicting Definition fails |
| Unbound `start()` | One admission with no `session` | SDK creates an idempotency key before the first attempt and reuses it for exact-body retries |
| Bound `start()` by Session key | One admission with `continue_or_create` | Session resolution and admission are atomic on the server |
| Bound `start()` by Session ID | One admission with `continue` | The named Session is the authority; no read-before-write check |
| `run()` / `text()` | Admission, tool loop as needed, terminal result read | The admitted handle remains recoverable when local waiting fails |
| `client.invocation(id)` | No request until first operation | Recovery needs only the durable Invocation ID |
| `snapshot()` | One current result read | Does not imply terminal status |
| `waitForResult()` | Status reads/stream plus one authoritative result read | Returns work for `completed` and `incomplete`; typed failure for `failed`, `cancelled`, or erased content |
| `Client.invoke()` | Exactly the request-shaped admission | No facade defaults beyond documented idempotency and scope behavior |

Each language facade should run the same fixture matrix. Tests should assert
the effective request body and headers, not only returned values.

## Timeout and uncertain admission

A local timeout says that the SDK stopped waiting. It does not prove that the
Invocation stopped or that admission failed. The typed error must preserve the
recovery material available at the point of failure:

```ts
class InvocationTimeoutError extends NvokenError {
  readonly handle?: InvocationHandle;
  readonly idempotencyKey?: string;
}
```

- When admission returned before waiting timed out, `handle` is present.
- When the transport outcome is unknown on an admission path, the idempotency
  key generated before the first request is still present so the caller can
  repeat the exact admission request and recover the same Invocation. A handle
  restored from only an Invocation ID may not know that key.
- A caller must not blindly start a new turn after a timeout. A host tool may
  already have performed an external side effect. Resume the Invocation under
  its ToolCall claim, or reconcile the external operation before resubmitting
  a tool result.

Timeout tests must cover both known-handle and uncertain-admission cases.

## Bound Session serialization

Local serialization is useful only if separately constructed wrappers for the
same Session share a lock. The lock identity is:

- Session ID when bound by ID; or
- effective tenant identity, resolved Agent identity, and Session key when
  bound by key.

The Agent component is required because Session-key uniqueness is scoped to an
Agent. The tenant component is required because a caller-owned Agent key can
repeat across tenants. A lock attached only to one `BoundSession` object, or a
lock keyed only by `sessionKey`, is not sufficient.

The lock is process-local convenience, not a distributed guarantee. Exact
cross-process conflict behavior remains the server's job and is available
through `Client.invoke()`.

## Current implementation versus this proposal

| Area | 0.29.0 | Proposed refinement |
| --- | --- | --- |
| Standalone turns | Unbound Agent calls omit `session`; nullable `sessionId` and `contentExpiresAt` are implemented | Keep |
| Workflow methods | `start()` and `bindSession()` exist in all four SDKs | Keep |
| Agent construction | `client.agent(options)` mixes ID/key identity, provisioning fields, local tools, and per-Agent runtime defaults | Replace with `agentByKey()`, `agentById()`, and named attachment methods |
| Provisioning | `createAgent()` exists, while a declared Agent can also create itself on first use when given a Definition | Make `createAgent()` the only provisioning path |
| Session selection | `Agent` invocation options still expose the wire `session` union | Keep the union only on `Client.invoke()` |
| Bound conflicts | Bound calls expose `ifActive` while also serializing locally | Remove `ifActive` from bound options |
| Results | Agent workflows return `AgentResult`; handle result reads return a different shape | Return one `InvocationResult` from all terminal workflow paths |
| Immediate result read | `handle.result()` can read a nonterminal projection | Rename to `snapshot()` |
| Streaming | Raw events are the default; reduced streaming is a second method | Make reduced state the default and name raw access `streamEvents()` |
| Timeouts | Generic timeout errors can lose the handle or retry token | Throw `InvocationTimeoutError` with recovery context |
| Bound locks | Locks are wrapper-local or do not include the full effective Session identity in every SDK | Share locks by Session ID or effective tenant + Agent + Session key |
| Generated APIs | Generated API objects are also public fields on `Client` | Expose them only through `raw()` |
| Handle construction | `InvocationHandle` has a public constructor | Construct handles through `start()`, `invoke()`, or `invocation()` |

## Delivery order

1. Add cross-SDK workflow fixtures and the behavioral conformance matrix for
   the 0.29.0 behavior before renaming public methods.
2. Split Agent identity from provisioning and local runtime attachments.
   Introduce `agentByKey()` and `agentById()`; remove implicit provisioning
   from Agent first use.
3. Remove Session selection and conflict controls from high-level options;
   correct the shared lock identity.
4. Unify terminal results, rename the immediate read to `snapshot()`, make
   reduced streaming the default, and add the typed timeout error.
5. Put generated APIs solely behind `raw()` and make handle construction an
   SDK implementation detail.
6. Update README examples and release the breaking facade change together in
   TypeScript, Go, Python, and Rust. Do not let one language establish a
   different workflow vocabulary.

No OpenAPI change is currently expected for these refinements. That is an
implementation finding, not a constraint: if the conformance matrix finds
that a facade workflow needs stronger server atomicity or recovery behavior,
the contract change lands and is published before its SDK implementation.

## Acceptance criteria

- A newcomer can tell from the method name whether an argument is an Agent key
  or an Agent ID.
- No single string selects both an Agent and its Definition.
- No constructor takes one options object for durable identity and a second
  options object for local behavior.
- Referencing an Agent never provisions it; `createAgent()` is the explicit
  create-or-resolve operation.
- Two unbound calls are documented and tested as independent standalone
  Invocations.
- Session continuity is selected by `bindSession()` on the high-level facade
  and by the `session` discriminator on `Client.invoke()`.
- All terminal high-level paths return one result shape with nullable Session
  identity and content expiry.
- Timeout errors retain enough context to recover without guessing whether a
  second turn should be admitted.
- Default streams are safe to render; raw protocol access is explicit.
- Bound Session serialization uses the complete effective Session identity.
- Generated APIs appear under `raw()` and nowhere else on the public client.
- The north-star examples compile and the behavioral matrix passes in all four
  SDKs.

## Non-goals

- Renaming wire fields such as `agent_key`, `tenant_key`, or the `session`
  discriminator for TypeScript style.
- Hiding the distinction between Agent and Agent Definition during
  provisioning.
- Making process-local Session serialization a distributed concurrency
  guarantee.
- Adding a second alias for `scoped()`.
- Preserving the current facade for compatibility. There are no compatibility
  requirements for this proposal; the goal is one durable, novice-readable
  surface.
