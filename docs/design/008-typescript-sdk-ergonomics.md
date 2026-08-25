# TypeScript SDK ergonomics review

**Status:** Proposed. No recommendation in this document is accepted or
implemented yet.
**Date:** 2026-08-24
**Reviewed version:** `@deepnoodle/nvoken` 0.28.0 at `89cbe88`
**Scope:** The handwritten TypeScript `Client`, `Agent`, Session binding, and
Invocation surfaces. Generated transports, browser-only calls, callback
receivers, and the other language SDKs are considered only where they affect
this public shape.
**Reading order:** [DIRECTION](DIRECTION.md) remains the standing direction and
outranks this review.

## Verdict

The TypeScript SDK has a strong happy path:

```ts
const agent = client.agent({
  agentKey: "support",
  definitionKey: "support",
});

console.log(await agent.text("Why was I charged twice?"));
```

That reads the way a TypeScript developer expects. The SDK also gets the hard
parts of a durable runtime right: replay-safe admission, resumable handles,
authoritative result reads, local input validation, typed workflow errors, and
automatic host-tool execution on the high-level paths.

The trouble begins when a developer moves beyond one response. `Agent`,
Session, and Invocation each have several nearby surfaces whose names suggest
similar behavior while their responsibilities differ. The API is capable, but
the caller has to learn too much about which layer they are standing on.

The main recommendation is to keep the Agent-first design and make the level of
control explicit in method names:

```ts
agent.text()         // complete text, automatic host tools
agent.run()          // complete result, automatic host tools
agent.stream()       // reduced live state, automatic host tools
agent.start()        // admit durable work and return its handle

agent.bindSession()  // add one local serialization boundary
client.invoke()      // low-level request-shaped admission
client.raw()         // generated transport, and the only way to reach it
```

The recommendations below explain why that vocabulary is easier to learn and
where it costs us.

## Review method

This review takes the perspective of a TypeScript developer who knows ordinary
HTTP clients and modern agent APIs but is new to nvoken. It follows the package
README, the executable TypeScript examples, the exported types, and the runtime
paths in `sdk/typescript/src/client.ts`.

The questions are practical:

- Can a developer predict whether a call creates, waits for, or merely names
  remote work?
- Does autocomplete lead toward the handwritten facade?
- Do similar methods return similar values?
- Does TypeScript reject impossible combinations before a request is made?
- Can a caller recover every durable turn after a local failure?

This is a source review. It does not claim live Runtime behavior beyond what
the current SDK and its tests establish.

## The current mental model

The product model is sound:

- An Agent Definition is App-owned, durable, and versioned.
- An Agent is one tenant's durable binding to a Definition.
- A Session is one durable conversation with that Agent.
- An Invocation is one durable turn in the Session.

The TypeScript object model adds local behavior around those records:

```text
Client
├── Agent                 server record plus workflow facade
│   └── AgentSession      local Session binding and serialization queue
└── InvocationHandle      durable turn control and recovery
```

That extra layer is useful. The problem is that its names do not always say
when the SDK is giving the caller a record, a local binding, or a generated
transport.

## What already works well

### The first useful call is small

`agent.text(input)` is the right top rung. It returns the thing most callers
want and raises `NoOutputTextError` with the full result when the turn produced
something else. A caller can move to `run()` without changing how the Agent is
declared.

### Durable work has a durable handle

`client.invocation(id)` performs no request. The returned handle can refresh,
wait, read the composed result, submit tool results, cancel, interrupt, nudge,
and reconnect to the stream. Recovery is part of the ordinary API instead of a
separate emergency path.

Automatic idempotency keys and exact-body retries also fit the durability
model. A transient transport failure does not need application-specific retry
code.

### The bound Session protects the common chat case

`agent.session({sessionKey})` serializes turns locally and keeps the binding
reserved after `invoke()` returns until that Invocation becomes terminal. This
matches nvoken's one-nonterminal-Invocation-per-Session rule and saves a chat
application from building its own promise queue.

### Configuration lives at sensible boundaries

The Definition holds durable model behavior and tool declarations. The Agent
facade holds process-local handlers and reusable provider selections. Changing
application state travels as per-turn context. The separation is explainable
and avoids putting secrets or handler code into reusable configuration.

### The facade adds real TypeScript value

The handwritten layer provides camel-case types, media and schema preflight,
specialized errors, typed async iterators, and normalized retry behavior. It is
more than a renamed generated client.

## Findings and recommendations

### 1. `Agent.invoke()` looks like orchestration but performs admission only

**Priority:** Highest

`Agent.run()` and `Agent.stream()` admit work, observe it, execute matching host
tool handlers, submit their results, and continue until settlement. The current
[`Agent.invoke()`](../../sdk/typescript/src/client.ts#L3432) only resolves the
Agent record and calls `Client.invoke()`.

That makes this code unsafe as a generic replacement for `run()`:

```ts
const handle = await agent.invoke("Look up order 42");
const result = await handle.waitForResult();
```

If the model requests a host tool, the Invocation parks at `waiting` and
`waitForResult()` keeps waiting. The handlers attached to `agent` are never
dispatched on this path. An external worker can finish the turn, but nothing in
the call shape says one is required.

The README eventually calls `invoke()` the low-level path in the
[host-tools section](../../sdk/typescript/README.md#host-tools). Earlier, the
[level-of-control section](../../sdk/typescript/README.md#choose-the-level-of-control)
presents it beside `text()` and `run()` without making the behavioral break as
prominent as it needs to be.

#### Recommendation

Rename admission-only `Agent.invoke()` and `AgentSession.invoke()` to
`start()`. Keep `Client.invoke(request)` as the request-shaped low-level
operation because its receiver already identifies the layer.

Do not keep both names indefinitely. The standing design direction treats two
names for one operation as a cost. If `start()` is accepted, it should replace
the admission-only Agent methods in the same release.

#### Cost

This is a breaking TypeScript API change. It also leaves `invoke` with two
meanings across the product vocabulary: the resource is still an Invocation,
while the high-level Agent starts one. That distinction is useful here because
it tells the caller whether the SDK owns the workflow or only the admission.

### 2. Session has four public meanings

**Priority:** Highest

Autocomplete currently exposes these paths:

| Surface | What it returns or controls |
| --- | --- |
| `client.sessions` | Generated `SessionsApi` transport |
| `client.raw().sessions` | The same generated transport |
| `client.getSession()` | Plain generated `Session` data |
| `agent.session()` | Local `AgentSession` binding and promise queue |

The generated APIs are public fields on
[`Client`](../../sdk/typescript/src/client.ts#L1584) even though `raw()` is
documented as the low-level escape hatch. The local `AgentSession` does not
represent the durable Session resource; it holds `{sessionId}` or
`{sessionKey}` and serializes calls.

A newcomer can reasonably expect this to work:

```ts
const session = await client.createSession({ agentKey: "support" });
await session.run("Hello");
```

It does not. `createSession()` returns data. To run a turn, the developer must
re-enter through `agent.session({sessionId: session.id})`.

#### Recommendation

Make two boundaries explicit:

1. Generated transports are reachable only through `client.raw()`.
2. Rename the current local object to `BoundSession` and its factory to
   `agent.bindSession()`.

The binding should accept Session creation and conflict assertions at construction time:

```ts
const chat = agent.bindSession({
  sessionKey: "ticket-483",
  sessionOptions: {
    retention: { ttlSeconds: 86_400 },
  },
});
```

Per-turn methods should not require a caller to restate those options. A
binding created by `sessionId` can retain comparison semantics when assertions
are supplied.

This recommendation deliberately does not turn every generated Session record
into an active object. Unbound Sessions and host-seeded history remain valid
resource operations. `BoundSession` says exactly what the local object adds:
an Agent plus a Session identity plus serialization.

#### Cost

Hiding the generated fields requires the stream implementation to depend on an
internal transport interface rather than the public `client.sessions` field.
Renaming the binding is breaking. The payoff is one documented low-level door
and one honest name for the local behavior.

### 3. Completed results have two shapes

**Priority:** High

`agent.run()` returns `AgentResult`:

```ts
const result = await agent.run(input);
result.text;
result.structuredOutput;
result.sessionId;
```

`handle.waitForResult()` returns `TypedInvocationResult`:

```ts
const result = await handle.waitForResult();
result.outputText;
result.invocation.structuredOutput;
result.invocation.sessionId;
```

Both calls mean “wait for successful terminal work and give me the composed
result.” Changing how the turn is started should not make the caller relearn
the result.

#### Recommendation

Return one facade result from `Agent.run()`, `InvocationHandle.result()`, and
`InvocationHandle.waitForResult()`:

```ts
interface InvocationResult<TOutput extends object> {
  handle: InvocationHandle<TOutput>;
  invocation: TypedInvocation<TOutput>;
  messages: SessionMessage[];
  text: string | null;
  structuredOutput: TOutput | null;
  agentId: string;
  sessionId: string;
  idempotencyKey?: string;
  deduplicated?: boolean;
}
```

Admission-only fields are optional because a lazy handle recovered by ID did
not observe the original acknowledgement. The generated wire result remains
available through `client.raw()`.

This also settles the facade spelling: use `text` at every handwritten level;
reserve `outputText` for the generated wire projection.

#### Cost

The package already exports a generated `InvocationResult` type at the root.
Adopting this name requires moving the generated type under the `raw` namespace
or choosing a less direct facade name. The generated namespace is the cleaner
long-term answer because it also reduces root-level autocomplete noise.

### 4. A local timeout can lose the handle to work that keeps running

**Priority:** High

`Agent.run()` and `Agent.stream()` use a local timeout. Disconnecting or timing
out does not cancel the remote Invocation, which is the correct durability
rule. Once admission succeeds, however, the timeout error does not include the
handle held inside the run loop.

The caller can therefore lose the easiest recovery path while the paid turn
continues. A caller-supplied idempotency key can help reproduce admission, but
the convenience method generates that key by default and does not expose it on
the error either.

#### Recommendation

Add an `InvocationTimeoutError` with recovery context:

```ts
class InvocationTimeoutError extends NvokenError {
  handle?: InvocationHandle;
  idempotencyKey: string;
  remoteCancelled: false;
}
```

When the timeout fires after acknowledgement, `handle` is present. When it
fires during an ambiguous admission, the exact idempotency key is still
present so the caller can retry safely. The error message and README should
state that the remote turn was not cancelled.

Do not make local timeout imply remote cancellation. Those are different
operations, and the existing `cancel()` method already names the destructive
one.

### 5. TypeScript permits combinations the SDK immediately rejects

**Priority:** High

The low-level `InvokeRequest` uses unions to prevent callers from supplying
both `agentId` and `agentKey`, or both `sessionId` and `sessionKey`.
`InvocationOptions` does not preserve the Session union, so the same invalid
combination compiles through the high-level Agent.

`AgentOptions` also permits `definitionKey` with `definitionId`, or a
Definition reference beside `agentId`. The constructor rejects both. An empty
`sessionOptions: {}` compiles even though serialization rejects it.

These are TypeScript modeling defects. The editor knows enough to stop them.

#### Recommendation

- Define the Agent declaration as a union of by-ID and by-key forms, with
  Definition references allowed only on the by-key form.
- Define `InvocationOptions` as common turn options intersected with the same
  three-way Session target used by `InvokeRequest`: by ID, by key, or a new
  anonymous Session.
- Model `SessionOptions` as a non-empty union, or provide a constructor helper
  whose return type proves at least one assertion exists.

Keep runtime validation. JavaScript callers and values decoded from storage
still need it. The type system should catch the ordinary mistakes first.

### 6. The safest stream is not the high-level stream

**Priority:** Medium

`agent.stream()` yields raw protocol events. Rendering raw deltas correctly
requires the caller to discard previews on resync, on a saved message, on a
terminal change, and when a restarted execution raises `attempt`. Messages in
a durable frame must also be applied before Invocation changes.

The source documentation for
[`handle.streamReduced()`](../../sdk/typescript/src/client.ts#L4036) says the
Reducer-backed form “is the path that should be reached for.” The Agent and
bound Session do not expose that safer form while preserving their automatic
host-tool dispatch.

#### Recommendation

- Make high-level `agent.stream()` and `BoundSession.stream()` yield reduced
  `StreamUpdate` values and continue dispatching host tools.
- Name the protocol-level iterable `streamEvents()`.
- Keep low-level Session subscription helpers in the stream module for callers
  intentionally implementing their own fold.

The most obvious method should be safe for a UI. Raw frames remain important,
but they should require an explicit raw-sounding name.

### 7. The README teaches internals before the first complete response

**Priority:** Medium

The package README installs the package, then spends roughly sixty lines on
Agent record lifecycle and the built-in fetch tool before reaching the first
runnable response. The opening Agent snippet references `client`, `userId`, and
`lookupOrder` without defining them.

The conceptual material is accurate. Its order makes the SDK feel harder than
the first response actually is.

#### Recommendation

Put the complete quickstart immediately after installation. Follow it with the
four-resource mental model and then the control ladder.

The first page should also state two behaviors that newcomers otherwise infer
incorrectly:

- Two independent `agent.text()` calls do not share conversation history;
  bind a Session or supply the same Session identity.
- Explain what omitting `tenantKey` means in the local quickstart, then show the
  production multi-tenant form.

Move built-in fetch, model controls, credential management, and callback tools
after Agent, Session, Invocation, result, and error fundamentals.

The raw-access example also needs a direct correction: `client.raw()` exposes
`models`, not the documented `modelPricing` member.

### 8. The public surface leaks implementation choices

**Priority:** Medium

The root package exports the handwritten facade, generated resource types,
stream helpers, callback helpers, browser helpers, and a generated `raw`
namespace. `Client` also exposes generated API instances as fields and through
its `raw()` method. `InvocationHandle` has a public constructor with nine
arguments even though callers should obtain one from the Client.

Each export is explainable in isolation. Together they weaken the three-level
story told by the README and make editor discovery noisy.

#### Recommendation

- Keep `Client`, `Agent`, `BoundSession`, `InvocationHandle`, common facade
  types, and ordinary helpers at the root.
- Keep generated transports and generated-only models under `raw`.
- Make `InvocationHandle` construction internal; preserve
  `client.invocation(id)` as the public recovery factory.
- Use package subpaths for browser, callback, transcript, and other specialist
  surfaces. Re-export at the root only when the ordinary Agent workflow needs
  the symbol.

Tree shaking reduces bundle cost. It does not reduce the learning cost of a
large root namespace, which is the problem here.

## Recommended end state

The ordinary server-side workflow should fit this shape:

```ts
import { Client } from "@deepnoodle/nvoken";

const client = new Client();
const support = client.agent({
  tenantKey: customer.id,
  agentKey: "support",
  definitionKey: "support",
  tools: [lookupOrder],
});

const chat = support.bindSession({
  sessionKey: ticket.id,
  sessionOptions: {
    retention: { ttlSeconds: 30 * 24 * 60 * 60 },
  },
});

const answer = await chat.text("Why was I charged twice?");
const result = await chat.run("Check order 42 and explain its status.");

const handle = await chat.start("Prepare a longer account review.");
await queue.save(handle.invocationId);
```

A worker recovering the last turn should get the same result shape:

```ts
const handle = client.invocation(savedInvocationId);
const result = await handle.waitForResult();

console.log(result.text);
```

The request-shaped path stays available without competing with the Agent
facade:

```ts
const handle = await client.invoke({
  agentKey: "support",
  sessionKey: ticket.id,
  input: "Prepare a longer account review.",
});
```

## Delivery order

These changes should be designed together even if they land in more than one
commit. The names, return types, and docs describe one mental model.

1. Settle `start()`, `bindSession()`, the common result type, and the generated
   transport boundary as one public API decision.
2. Update TypeScript types so the accepted call shapes are enforced at compile
   time.
3. Preserve handles and idempotency keys on local timeout errors.
4. Add reduced streaming to the Agent and bound Session before changing the
   default stream name.
5. Rewrite the README around the settled vocabulary and compile every embedded
   TypeScript example in CI.
6. Update executable examples and TypeScript conformance tests in the same
   change that removes the old names.

Before implementation, check the other SDKs for conceptual parity. Their
method spelling can remain idiomatic, but `run`, admission-only start, bound
Session behavior, results, and timeout recovery should describe the same
operations across languages.

## Acceptance criteria for a follow-up implementation

- A newcomer can reach one complete response from the package README before
  learning Agent lifecycle administration.
- No high-level method that sounds complete can park on an attached host tool
  without either dispatching it or naming the caller-owned orchestration.
- Agent run and recovered-handle result reads return one facade shape.
- Every post-admission local timeout exposes the Invocation handle.
- Impossible Agent and Session identity combinations fail TypeScript
  compilation.
- Generated API clients have one public entry point: `client.raw()`.
- The default high-level stream is safe to render through the SDK Reducer.
- Bound Session options are declared once at the binding boundary.
- All README snippets compile as part of the TypeScript SDK gate.

## Non-goals

This review does not propose:

- changing the HTTP contract or the durable Agent, Session, and Invocation
  resource model;
- making a local timeout cancel remote work;
- hiding low-level admission, raw stream events, or generated transports from
  advanced callers;
- moving tool handlers or secret MCP headers into Agent Definitions;
- turning every Session resource read into an active object; or
- changing the other SDKs before their own language-specific review.

The goal is narrower: make the TypeScript facade say what each layer does, and
make the obvious path the safe one.
