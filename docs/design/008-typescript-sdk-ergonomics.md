# TypeScript SDK ergonomics review

**Status:** Proposed. No recommendation in this document is accepted or
implemented yet.
**Date:** 2026-08-24
**Revised:** 2026-08-24, after a review against the implementation and the
Go, Python, and Rust facades. The review's findings are folded into the
sections below. Where it corrected this document, the correction is marked.
**Reviewed version:** `@deepnoodle/nvoken` 0.28.0 at `89cbe88`
**Scope:** The handwritten TypeScript `Client`, `Agent`, Session binding, and
Invocation surfaces. Generated transports, browser-only calls, and callback
receivers are considered only where they affect this public shape. The other
language SDKs are in scope for exactly the operations this document renames,
because a rename that lands in one SDK splits a vocabulary the four share
today.
**Reading order:** [DIRECTION](DIRECTION.md) remains the standing direction and
outranks this review. [Design 006](006-sdk-write-shape-parity.md) set
cross-SDK parity as a value this document keeps.
[Design 007](007-host-supplied-context.md) has SDK facade work still open,
and the delivery order below sequences against it.

## Verdict

The TypeScript SDK has a strong execution path, but its declaration-first
happy path starts in persistence vocabulary:

```ts
const agent = client.agent({
  agentKey: "support",
  definitionKey: "support",
});

console.log(await agent.text("Why was I charged twice?"));
```

`agent.text()` reads the way a TypeScript developer expects. The declaration
does not yet. Before receiving one response, the caller has to understand how
an Agent key and a Definition key differ, why the common example repeats
`"support"`, and which other keys establish tenant and Session identity.

The SDK gets the hard parts of a durable runtime right: replay-safe admission,
resumable handles, authoritative result reads, local input validation, typed
workflow errors, and automatic host-tool execution on the high-level paths.

The trouble begins when a developer moves beyond one response. `Agent`,
Session, and Invocation each have several nearby surfaces whose names suggest
similar behavior while their responsibilities differ. The API is capable, but
the caller has to learn too much about which layer they are standing on.

Keep the Agent-first design and make the level of control explicit in method
names:

```ts
agent.text()         // complete text, automatic host tools
agent.run()          // complete result, automatic host tools
agent.stream()       // reduced live state, automatic host tools
agent.start()        // admit durable work and return its handle

agent.bindSession()  // add one local serialization boundary
client.scoped()      // narrow every request to one tenant or end user
client.invoke()      // low-level request-shaped admission
client.raw()         // generated transport, and the only way to reach it
```

About half of what follows needs no naming decision at all. Several findings
are defects with one right answer, and one of them is a silent correctness
bug. The delivery order separates what ships now from the one public API
decision.

## Proposal: settle the contract as one compiling example

The findings below fall into two groups. Five are defects or gaps with one
right answer, and they should ship now. The rest are one naming decision
about `start()`, `bindSession()`, the result shape, the stream default, and
the generated transport boundary. That decision should be made once, for all
four SDKs, and land in one change.

**Corrected on review.** The first draft asked for a set of runnable
workflows to be written and approved before any change, then mapped onto the
HTTP API. That is a second design document by another name. Write the six
workflows (one-shot text, a tenant conversation, host tools, streaming,
admission-only work, and recovery) as one file under
`sdk/typescript/src/examples/` that compiles in CI. That file is the
contract, the acceptance test, and the README source. Nothing else is needed
before implementation.

The caller-owned key mechanism should remain. It gives nvoken stable names for
idempotent resolution and creation, and it lets an application recover work
without storing every nvoken ID first. The problem is its place in the public
vocabulary. A developer thinks in terms of the support Agent for this customer
and this conversation, not a tuple of `tenantKey`, `agentKey`,
`definitionKey`, and `sessionKey`.

An illustrative common path is:

```ts
const support = client
  .scoped({ tenantKey: customer.id })
  .agent("support", { tools: [lookupOrder] });

const conversation = support.bindSession(
  { sessionKey: ticket.id },
  { retention: { ttlSeconds: 30 * 24 * 60 * 60 } },
);

const answer = await conversation.text("Why was I charged twice?");
```

**Corrected on review.** The first draft proposed a new `client.forTenant()`
method. That operation already exists as
[`Client.scoped()`](../../sdk/typescript/src/client.ts#L1706), present in Go
and Python too and documented in the README. The wire says an omitted body
`tenant_key` inherits the `X-Nvoken-Tenant-Key` header, so this works today,
before any change:

```ts
client
  .scoped({ tenantKey: customer.id })
  .agent({ agentKey: "support", definitionKey: "support" });
```

The mapping table below points at `scoped()`. If `forTenant` is judged the
better name, rename `scoped` in all four SDKs. Do not add a second name for
one operation; that is the redundancy DIRECTION rules out.

The first draft also spelled the binding two ways: `bindSession(ticket.id,
{...})` in one place and `bindSession({ sessionKey, sessionOptions })` in
another. A bare string cannot say whether it is a key or an ID, and the
existing `SessionBinding` union already encodes that distinction. The binding
takes the identity union first and the Session options second, as shown
above.

The exact method names remain part of the decision. The important change is
that the common path expresses application concepts while the SDK performs
the wire mapping:

| Developer-facing concept | Proposed SDK spelling | Current wire mechanism |
| --- | --- | --- |
| Customer or workspace | `client.scoped({ tenantKey })` | `X-Nvoken-Tenant-Key`, which an omitted body `tenant_key` inherits |
| Ordinary Agent | `.agent("support")` | `agent_key` plus a matching `definition_key` default |
| Conversation | `.bindSession({ sessionKey })` | `session_key` |
| Nvoken resource recovery | `.invocation(invocationId)` and resource reads | opaque Nvoken IDs |
| Retry safety | automatic on ordinary calls | idempotency key |

The repeated Agent and Definition name should be a default, not a requirement.
The less common case can remain explicit:

```ts
const returnsAgent = client
  .scoped({ tenantKey: customer.id })
  .agent("returns", { definition: "support" });
```

**What the default changes.** Today an Agent declared by `agentKey` alone
means "must already exist":
[`ready()`](../../sdk/typescript/src/client.ts#L3447) skips creation and the
turn admits by key in one round trip. Once `.agent("support")` defaults the
Definition, every declaration by key is create-or-resolve. The recommendation
is to accept that and retire the must-exist spelling: an Agent declared by
key is created when missing, a Definition that does not exist fails the first
use with `not_found`, and a record known by ID is declared as
`.agent({ agentId })`. The cost is one create-or-resolve round trip per
process per Agent, after which the ID is cached and every turn admits by ID
as it does now.

This vocabulary separates three things currently called keys:

- Caller-owned references name tenant, Agent, Definition, and Session
  resources.
- Idempotency keys are retry tokens generated by the SDK and exposed when
  recovery needs them.
- API, provider, and signing keys are credentials.

The generated client may keep the exact `*_key` wire names under
`client.raw()`. Compatibility alone is a good reason not to rename transport
fields for style.

Surface-first design does not allow the SDK to fake server guarantees. If a
pleasant facade would need a read-before-write race, could admit duplicate
work, or could lose the durable handle after admission, update OpenAPI and the
Runtime first. If the difference is only scoping or spelling, keep the HTTP
contract stable and solve it in the handwritten facade.

**Corrected on review.** That mapping has been done for the six workflows
above, and it found no gap. Scope-header inheritance, `session_key`
create-or-resolve, automatic idempotency keys with exact-body retry, and lazy
handles all exist on the current contract. No OpenAPI or Runtime change is
expected. The rule stands for anything the implementation turns up.

## Review method

This review takes the perspective of a TypeScript developer who knows ordinary
HTTP clients and modern agent APIs but is new to nvoken. It follows the package
README, the executable TypeScript examples, the exported types, and the runtime
paths in `sdk/typescript/src/client.ts`. It also reads the Go, Python, and
Rust facades for the operations this document renames.

The questions are practical:

- Can a developer predict whether a call creates, waits for, or merely names
  remote work?
- Does the common path use application concepts before wire identifiers?
- Does autocomplete lead toward the handwritten facade?
- Do similar methods return similar values?
- Does TypeScript reject impossible combinations before a request is made?
- Can a caller recover every durable turn after a local failure?
- Do the four SDKs describe the same operation with the same behavior?

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

### Tenant scoping already has a home

`client.scoped({ tenantKey, userKey })` narrows every request the returned
client makes, and the wire inherits an omitted body key from the header. The
proposal builds on it rather than replacing it.

### The bound Session protects the common chat case

`agent.session({sessionKey})` serializes turns locally and keeps the binding
reserved after `invoke()` returns until that Invocation becomes terminal. This
matches nvoken's one-nonterminal-Invocation-per-Session rule and saves a chat
application from building its own promise queue. Finding 9 records where the
TypeScript binding falls short of the other three SDKs.

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

**Corrected on review.** On a bound Session the trap is worse than a stalled
wait. [`AgentSession.invoke()`](../../sdk/typescript/src/client.ts#L3766)
keeps the Session reserved until the Invocation is terminal. If the model
parks on a host tool, nothing dispatches it, and every later `run()` or
`text()` on that binding queues behind it until the waiting timeout ends the
turn. Nothing is logged. The application sees a chat that stopped answering.

The README eventually calls `invoke()` the low-level path in the
[host-tools section](../../sdk/typescript/README.md#host-tools). Earlier, the
[level-of-control section](../../sdk/typescript/README.md#choose-the-level-of-control)
presents it beside `text()` and `run()` without making the behavioral break as
prominent as it needs to be.

#### Recommendation

Rename admission-only `Agent.invoke()` and `AgentSession.invoke()` to
`start()`, in all four SDKs, in one change. Go, Python, and Rust spell this
operation `invoke` today, so a TypeScript-only rename would split the one
facade vocabulary design 006 unified. DIRECTION says there are no external
users and every collapse lands in the same change that adds the new name.
Keep `Client.invoke(request)` as the request-shaped low-level operation
because its receiver already identifies the layer.

Do not keep both names. If `start()` is accepted, it replaces the
admission-only Agent methods in the same release.

#### Cost

This is a breaking change in four SDKs. It also leaves `invoke` with two
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

**Corrected on review.** `BoundSession` is not a new name. The Python SDK
already calls its binding `BoundSession`; Go and Rust call it `AgentSession`.
This recommendation aligns the other three with Python rather than inventing
a fourth spelling.

The binding takes the Session identity and the Session options at
construction, and nothing per turn:

```ts
const chat = agent.bindSession(
  { sessionKey: "ticket-483" },
  { retention: { ttlSeconds: 86_400 } },
);
```

Per design 002, those options are a creation payload on the first turn and a
precondition on every later one, so declaring them once at the binding is what
makes them true for the whole conversation. Per-turn methods should not
require a caller to restate them. A binding created by `sessionId` can retain
comparison semantics when assertions are supplied.

This recommendation deliberately does not turn every generated Session record
into an active object. Unbound Sessions and host-seeded history remain valid
resource operations. `BoundSession` says exactly what the local object adds:
an Agent plus a Session identity plus serialization.

#### Cost

Hiding the generated fields touches two types, not one.
[`StreamClient`](../../sdk/typescript/src/client.ts#L1576) lists `sessions`
because the stream helpers accept a `BrowserClient`, and
[`BrowserClient`](../../sdk/typescript/src/browser.ts#L79) is
`Omit<Client, "invoke">`, which only sees public members. The stream side
needs an internal accessor. The browser type has to be written out rather
than derived from `Client`. Renaming the binding is breaking in Go and Rust
and a no-op in Python. The payoff is one documented low-level door and one
honest name for the local behavior.

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

Both calls mean "wait for successful terminal work and give me the composed
result." Changing how the turn is started should not make the caller relearn
the result. `handle.outputText()` is a third spelling of the same value.

#### Recommendation

Return one facade result from `Agent.run()`, `InvocationHandle.result()`, and
`InvocationHandle.waitForResult()`:

```ts
interface InvocationResult<TOutput extends object> {
  handle: InvocationHandle<TOutput>;
  invocation: TypedInvocation<TOutput>;
  status: OutcomeStatus;
  messages: SessionMessage[];
  text: string | null;
  structuredOutput: TOutput | null;
  agentId: string;
  sessionId: string;
  idempotencyKey?: string;
  deduplicated?: boolean;
}
```

`status` stays required. Both paths know the turn is terminal by
construction, and today's `EndedInvocationHandle` narrowing exists to make
callers branch on `incomplete`. The result keeps that guarantee even though
its `handle` member is a plain `InvocationHandle`. Admission-only fields are
optional because a lazy handle recovered by ID did not observe the original
acknowledgement. The generated wire result remains available through
`client.raw()`.

This also settles the facade spelling: use `text` at every handwritten level;
reserve `outputText` for the generated wire projection.

#### Cost

The package already exports a generated `InvocationResult` type at the root.
Adopting this name requires moving the generated type under the `raw`
namespace or choosing a less direct facade name. The generated namespace is the
cleaner long-term answer because it also reduces root-level autocomplete noise.

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

**Corrected on review.** "Keeps running" is only half of it. If the timeout
fires while a host tool handler is executing, the handler finishes in the
background,
[`submitToolResults`](../../sdk/typescript/src/client.ts#L3761) fails on the
aborted signal, and the turn is parked at `waiting` with nobody left to
answer it. It stays there until the waiting timeout fails it. The common
outcome is not a turn that runs on but one that hangs on the caller's own
tool.

#### Recommendation

Add an `InvocationTimeoutError` with recovery context:

```ts
class InvocationTimeoutError extends NvokenError {
  handle?: InvocationHandle;
  idempotencyKey: string;
}
```

When the timeout fires after acknowledgement, `handle` is present. When it
fires during an ambiguous admission, the exact idempotency key is still
present so the caller can retry safely.

The first draft gave the class a `remoteCancelled: false` member. A field that
can hold one value is documentation, so it is dropped; the message carries
the fact instead. The message and the README name the two exits:
`handle.cancel()` to stop paying for the turn, or
`agent.answerToolCalls(handle.invocationId)` from a worker to finish a turn
parked on host tools.

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

**Corrected on review.** One case is not rejected at all. `Client.invoke`
refuses a request carrying both `sessionId` and `sessionKey`, but
[`Agent.request()`](../../sdk/typescript/src/client.ts#L3482) never passes
both through: it takes `sessionId` first and drops `sessionKey` silently.
Through the high-level Agent, the combination compiles, runs, and targets a
Session the caller did not name. That is a bug to fix now, ahead of the type
change.

These are TypeScript modeling defects. The editor knows enough to stop them.

#### Recommendation

- Fix `Agent.request()` to refuse both Session identities, so JavaScript
  callers get the same answer `Client.invoke` already gives.
- Define the Agent declaration as a union of by-ID and by-key forms, with
  Definition references allowed only on the by-key form.
- Define `InvocationOptions` as common turn options intersected with the same
  three-way Session target used by `InvokeRequest`: by ID, by key, or a new
  anonymous Session.
- Model `SessionOptions` with a require-at-least-one mapped type, the
  standard TypeScript pattern for a non-empty options object.

Keep runtime validation. JavaScript callers and values decoded from storage
still need it. The type system should catch the ordinary mistakes first.

### 6. The safest stream is not the high-level stream

**Priority:** Medium

`agent.stream()` yields raw protocol events. Rendering raw deltas correctly
requires the caller to discard previews on resync, on a saved message, on a
terminal change, and when a restarted execution raises `attempt`. Messages in
a durable frame must also be applied before Invocation changes.

The source documentation for
[`handle.streamReduced()`](../../sdk/typescript/src/client.ts#L4052) says the
Reducer-backed form "is the path that should be reached for." The Agent and
bound Session do not expose that safer form while preserving their automatic
host-tool dispatch.

Design 004's protocol end state landed on 2026-08-14, so nothing upstream
blocks this. The reduced fold already carries what dispatch needs:
`snapshot.invocationChanges` reports a change to `waiting`, which is where
[`streamLoop`](../../sdk/typescript/src/client.ts#L3554) detects it on raw
frames today.

#### Recommendation

- Make high-level `agent.stream()` and `BoundSession.stream()` yield reduced
  `StreamUpdate` values and continue dispatching host tools.
- Name the protocol-level iterable `streamEvents()`.
- Keep low-level Session subscription helpers in the stream module for callers
  intentionally implementing their own fold.
- Update `src/examples/streaming.ts` with the default, since it renders raw
  deltas today.

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
- Explain what omitting the tenant means in the local quickstart, then show
  the production form with `client.scoped()`.

Move built-in fetch, model controls, credential management, and callback tools
after Agent, Session, Invocation, result, and error fundamentals.

The raw-access example also needs a direct correction: `client.raw()` exposes
`models`, not the documented `modelPricing` member. That fix and the
quickstart move ship now. The rest of the rewrite waits for the vocabulary.

### 8. The public surface leaks implementation choices

**Priority:** Medium

The root package exports the handwritten facade, generated resource types,
stream helpers, callback helpers, browser helpers, and a generated `raw`
namespace. `Client` also exposes generated API instances as fields and through
its `raw()` method. `InvocationHandle` has a public constructor with nine
arguments even though callers should obtain one from the Client.

Each export is explainable in isolation. Together they weaken the three-level
story told by the README and make editor discovery noisy.

**Corrected on review.**
[`index.ts`](../../sdk/typescript/src/index.ts#L7) records why the browser
entry is exported at the root: a surface reachable only by a subpath a caller
has to already know about gets rebuilt by hand. That reason is about
discovery from the README, not from autocomplete, and a README pointer to
`@deepnoodle/nvoken/browser` answers it. This document overturns that
decision on purpose and says so here, so the reversal is not silent.

#### Recommendation

- Keep `Client`, `Agent`, `BoundSession`, `InvocationHandle`, common facade
  types, and ordinary helpers at the root.
- Keep generated transports and generated-only models under `raw`.
- Make `InvocationHandle` construction internal; preserve
  `client.invocation(id)` as the public recovery factory. This part ships
  now; nothing depends on the naming decision.
- Use package subpaths for browser, callback, transcript, and other specialist
  surfaces. Re-export at the root only when the ordinary Agent workflow needs
  the symbol.

Tree shaking reduces bundle cost. It does not reduce the learning cost of a
large root namespace, which is the problem here.

### 9. Bound Session serialization is per object in TypeScript only

**Priority:** High

Go, Python, and Rust keep one lock table on the client and key it by
`(tenantKey, sessionKey)` or by `sessionId`, so every binding of one Session
in a process serializes against every other. TypeScript keeps a promise chain
on each [`AgentSession`](../../sdk/typescript/src/client.ts#L3766) instance.
Two `agent.session({ sessionKey: "ticket-483" })` objects in one Node process
do not serialize against each other; the second learns about the first from
`SessionBusyError` after a round trip. The README documents this. The other
three SDKs do something better, and the four should agree.

The other three have a fault of their own that the proposal makes live. Their
lock key falls back to the literal `default` when the Agent options carry no
tenant ([Go](../../sdk/go/agent.go#L494),
[Python](../../sdk/python/src/nvoken/agent.py#L491),
[Rust](../../sdk/rust/src/agent.rs#L582)). Once `scoped()` is the ordinary way
to name a tenant, the tenant lives on the client rather than in the Agent
options, so two tenants' `ticket-1` conversations would share one lock.

#### Recommendation

Key TypeScript's serialization on a client-level table like the others. In
all four, build the key from the effective tenant: the Agent's declared
tenant, else the client scope's tenant, else the default partition. Ship both
before the scoped path is documented as the ordinary one.

#### Cost

A client-level table is shared with `scoped()` clients, which Python already
does by handing the table to the narrowed client. The other three follow the
same pattern.

## Recommended end state

The ordinary server-side workflow should fit this shape:

```ts
import { Client } from "@deepnoodle/nvoken";

const client = new Client();
const support = client
  .scoped({ tenantKey: customer.id })
  .agent("support", { tools: [lookupOrder] });

const chat = support.bindSession(
  { sessionKey: ticket.id },
  { retention: { ttlSeconds: 30 * 24 * 60 * 60 } },
);

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

### Ship now

Each of these is a defect with one right answer, or a change nothing else
depends on. None of them waits for the naming decision.

1. Refuse both Session identities in `Agent.request()`, and add the
   `AgentOptions`, `InvocationOptions`, and `SessionOptions` types from
   finding 5.
2. Add `InvocationTimeoutError` with the handle and idempotency key, and the
   parked-tool guidance, from finding 4.
3. Make `InvocationHandle` construction internal; `client.invocation(id)` is
   the factory.
4. Key bound Session serialization on the client in TypeScript, and on the
   effective tenant in all four SDKs, from finding 9.
5. Fix `modelPricing` and move the quickstart to the top of the README, from
   finding 7.

### One decision, four SDKs, one change

6. Write the six workflows as one compiling example file. That file settles
   `start()`, `bindSession()`, the result shape, the reduced stream default,
   and the transport boundary.
7. Land the renames, the result type, the stream default, and the export
   cleanup in TypeScript, Go, Python, and Rust together. Method spelling
   stays idiomatic per language; the operations and their behavior match.
8. Fold in the open SDK facade work from design 007 in the same pass, so the
   facades change once.
9. Rewrite the README around the settled vocabulary, then update executable
   examples and conformance tests in the same change that removes old names.
   Compile every embedded TypeScript example in CI.

No OpenAPI or Runtime change is expected. If implementation finds one, it
lands first and the clients are regenerated before the facade changes.

## Acceptance criteria for a follow-up implementation

- A newcomer can reach one complete response from the package README before
  learning Agent lifecycle administration.
- The ordinary Agent and Session workflow does not require key-suffixed option
  names. Caller-owned references remain available through scoped methods and
  advanced selectors.
- No new method duplicates an existing one under another name. `scoped()` is
  the tenant boundary.
- Every facade workflow has a recorded wire mapping, and no convenience method
  hides a read-before-write race or weakens retry and recovery guarantees.
- No high-level method that sounds complete can park on an attached host tool
  without either dispatching it or naming the caller-owned orchestration.
- Agent run and recovered-handle result reads return one facade shape.
- Every post-admission local timeout exposes the Invocation handle.
- Impossible Agent and Session identity combinations fail TypeScript
  compilation, and from JavaScript are refused rather than dropped.
- Every SDK serializes a bound Session by its identity across the process,
  keyed on the effective tenant.
- Go, Python, Rust, and TypeScript describe admission-only start, the bound
  Session, the result, and timeout recovery as the same operations.
- Generated API clients have one public entry point: `client.raw()`.
- The default high-level stream is safe to render through the SDK Reducer.
- Bound Session options are declared once at the binding boundary.
- All README snippets compile as part of the TypeScript SDK gate.

## Non-goals

This review does not propose:

- renaming every `*_key` wire field to match the facade, or changing the HTTP
  contract where scoping and spelling alone solve the SDK problem;
- changing the durable Agent, Session, and Invocation resource model;
- making a local timeout cancel remote work;
- hiding low-level admission, raw stream events, or generated transports from
  advanced callers;
- moving tool handlers or secret MCP headers into Agent Definitions;
- turning every Session resource read into an active object; or
- reviewing the other SDKs' ergonomics in full. Only the operations this
  document renames, and the serialization defect in finding 9, land in all
  four.

The goal is narrower: make the TypeScript facade say what each layer does, and
make the obvious path the safe one.
