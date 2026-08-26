# TypeScript SDK ergonomics and public terminology

**Status:** Accepted; implementation in progress against the published
Agent, AgentRevision, MemorySpace, Conversation, and Turn contract.

**Date:** 2026-08-24

**Revised:** 2026-08-25 after newcomer reviews, complete-vocabulary comparison,
and review of shared behavior, private Agents, independent memory ownership,
inline behavior, and optional Conversation continuity.

**Pre-cutover reference:** `@deepnoodle/nvoken` 0.29.0 at `3045dc4b`

**Scope:** The handwritten TypeScript facade and the coordinated public
contract vocabulary it requires. Equivalent workflows in Go, Python, and Rust
remain in scope for parity. Generated exact APIs remain available through
`raw()`.

[DIRECTION](DIRECTION.md) is the standing design direction and outranks this
document if they conflict.

## Verdict

The implemented SDK exposes the service accurately, but it does not yet feel
like a natural local agent library. Its hardest concept is not streaming or
durable execution. It is the current two-level Agent model:

- an App-scoped Agent Definition contains the behavior most developers call an
  Agent; and
- a tenant-scoped Agent points to that Definition and owns memory and Sessions.

`client.agent({ tenantKey, agentKey, definitionKey, tools, ... })` then combines
durable identity, possible provisioning, local handlers, and runtime defaults
in one synchronous constructor. A newcomer cannot tell whether it creates,
looks up, or merely describes a remote resource.

The accepted target separates four independent choices:

| Choice | Public concept |
| --- | --- |
| Stored reusable behavior or inline behavior | `Agent` / `AgentRevision` or `InlineBehavior` |
| Durable memory ownership | `MemorySpace` or none |
| Retained transcript continuity | `Conversation` or none |
| One durable execution | `Turn` |

The common path should read like ordinary local composition:

```ts
const analyst = await client.agent("real-estate-analyst");
const readyAnalyst = analyst.bindTools({
  lookup_property: lookupProperty,
});

const conversation = readyAnalyst.conversation({
  tenant: "acme",
  user: "alice",
  key: "property-42",
  owner: "user",
});
const answer = await conversation.text("Is this property overpriced?");
```

Every operation has one meaning:

- `await client.agent()` performs an Agent lookup by caller-owned key.
- `ownedBy` appears only when the Agent is not App-owned.
- `bindTools()` attaches process-local implementations to durable tool
  contracts.
- execution options carry tenant, actor, memory, limits, and optional
  Conversation selection.
- `run()` admits a Turn and waits for its result.
- `start()` admits the same Turn and returns its durable handle.

There is no public `AgentDefinition`, `AgentInstance`, or `definitionKey` in the
target surface. `client.agent()` is an awaited address-only lookup; it never
provisions or accepts behavior, tools, actor, memory, or Conversation options.

## Public model

### Agent and AgentRevision

An Agent is stored, reusable, versioned behavior. It owns a stable identity,
caller-owned key, visibility, archive state, and current revision pointer. An
AgentRevision is an immutable snapshot of instructions, model policy, tool
contracts, limits, output contract, and an optional default memory selection
policy.

Agents may be App-, tenant-, or user-owned. App ownership is the unmarked
default because it is the common reusable-product-Agent case. A non-App owner
is stated only through `ownedBy`:

```ts
const analyst = await client.agent("real-estate-analyst");
const custom = await client.agent("assistant", {
  ownedBy: { tenant: "acme" },
});
const personal = await client.agent("assistant", {
  ownedBy: { tenant: "acme", user: "alice" },
});
```

These calls contain only Agent addressing. They never carry the actor, memory,
Conversation, behavior configuration, or local tool handlers.

One App-owned Agent serves every tenant and user. Publishing a revision
advances one pointer; it does not copy behavior into per-user instances.
Admission resolves the requested current revision and records its exact ID
before queuing work.

`client.agent(key, options?)` always treats the string as a caller-owned key and
performs one remote lookup. `client.agents.getById(id)` is the explicit opaque-ID
lookup. The ID already determines the Agent and its owner, so it takes no
`ownedBy`; credential scope determines visibility and an invisible Agent is not
found. The collection groups creation, ID lookup, and listing; the returned
Agent exposes publication, archive, and restore. A raw cross-resource selector
always states either Agent ID or an owner-qualified key; `agentKey` is not a
relationship field.
`definitionKey` disappears because the target has no separately addressable
Agent Definition.

### MemorySpace

A MemorySpace is durable memory with its own inspection, retention, erasure,
and audit lifecycle. It is scoped inside an App and tenant but is independent
from Agent behavior and Conversation continuity.

```ts
type MemorySelection =
  | { scope: "none" }
  | { scope: "user"; namespace?: string }
  | { scope: "tenant"; namespace?: string };

type InlineMemorySelection =
  | { scope: "none" }
  | { scope: "user"; namespace: string }
  | { scope: "tenant"; namespace: string };
```

- `user` partitions memory by the bound user.
- `tenant` intentionally shares memory across users in the tenant.
- an explicit tenant namespace such as `leasing-team` lets trusted host code
  select product-specific memory shared by several users or Agents.
- an explicit user namespace may likewise survive Agent replacement or be
  shared by several Agents while remaining partitioned by the bound user.
- `none` disables durable memory and its tools.

Actor identity and memory selection are separate. A Turn attributed to Alice
may use Alice's memory, tenant memory, leasing-team memory, or no memory. Passing
a user never silently chooses a MemorySpace. User memory without a Turn actor
fails with `memory_user_required`; anonymous admission always forces `none`.

An AgentRevision may declare a default memory selection so common calls do not
repeat policy. The policy selects a space; the Agent does not own that space.
An explicit Turn option overrides the default. Inline behavior may make the
same selection without creating an Agent. Stored Agent defaults derive omitted
namespaces from immutable Agent ID. Inline user or tenant memory has no stored
Agent identity to derive from, so its SDK type requires an explicit namespace;
exact transports enforce the same rule at admission.

Ordinary SDK calls may lazily resolve or create a MemorySpace during Turn
admission. Exact and administrative APIs expose MemorySpace IDs and lifecycle
operations.

### Conversation

A Conversation is optional retained transcript continuity. It owns ordered
messages, compaction, retention, metadata, and concurrency. It does not own an
Agent or MemorySpace.

A Conversation may therefore contain Turns using a stored Agent revision or
inline behavior, and may be memoryless or use a MemorySpace. A high-level Agent
or inline runner carries behavior into each Turn, while Turn options select
actor, memory, limits, and optional Conversation. None becomes Conversation
identity or a durable Conversation default.

### Turn

A Turn is one durable execution for one input, including the complete model-
and-tool loop. Every Turn records exactly one behavior source and owns one
effective-behavior row:

```ts
type BehaviorSource =
  | { kind: "agent_revision"; agentRevisionId: string }
  | { kind: "inline"; behavior: InlineBehavior };
```

Admission copies the stored AgentRevision or inline value into the effective
row, applies only monotonic execution-limit narrowing, and records its digest.
Instructions, model policy, tool contracts, and output contract cannot be
overridden. The engine loads this one row for both source kinds.

The Turn independently records zero or one Conversation and zero or one
MemorySpace.

All four behavior/continuity combinations are valid:

| Behavior | Standalone | Conversation-bound |
| --- | --- | --- |
| AgentRevision | Yes | Yes |
| InlineBehavior | Yes | Yes |

A standalone Turn does not create a synthetic public Conversation. Its input,
checkpoints, output, failure, and recovery identity remain durable under the
Turn.

`Attempt` is one internal claim or retry attempt for a Turn. It is not a second
public unit of work.

## Design rules

### One spelling, one meaning

A string passed to `client.agent()` always means an App-owned Agent key. The
optional `ownedBy` argument changes only that key's owner namespace. Opaque ID
lookup keeps an explicit name:

```ts
await client.agent("real-estate-analyst");
await client.agent("assistant", { ownedBy: { tenant: "acme" } });
await client.agents.getById(savedAgentId);
await client.agents.getById(savedPersonalAgentId);
```

`await` is important: `client.agent()` performs the lookup now and fails now if
the Agent does not exist or is not visible. Its arguments contain only an
address, so it cannot be mistaken for provisioning or local runner
configuration. Collection creation is the only provisioning path.

Conversation references are also discriminated:

```ts
agent.conversation({
  tenant: "acme",
  user: "alice",
  key: ticket.id,
  owner: "user",
});
agent.conversation({
  tenant: "acme",
  user: "alice",
  id: savedConversationId,
});
```

Key lookup always states tenant- or user-owned Conversation scope. Conversation
ownership never supplies the Turn actor.

### Agent owner and Turn context are separate

Agent ownership is stable for the lifetime of the Agent handle. Turn tenant and
actor normally change per inbound request, so execution calls accept them
directly:

```ts
await analyst.run(input, {
  tenant: "acme",
  user: "alice",
  memory: { scope: "tenant", namespace: "leasing-team" },
});
```

`tenant` is required on every execution and `user` is optional. Tenant- and
user-owned Agents constrain those explicit values; the server rejects a tenant
or actor that disagrees with the owner. No
`forTenant()`, `forUser()`, `scoped()`, or `bindTenant()` facade ships in the
first target.

The same context object scopes Turn recovery:

```ts
const turn = client.turn(savedTurnId, {
  tenant: "acme",
  user: "alice",
});
```

`turn(id, context)` is synchronous because an opaque ID already identifies the
resource. It constructs a recovery handle and delays authorization and
existence checks until the first remote operation. Agent key lookup cannot do
that because a key must first resolve inside its owner namespace.
On a trusted credential, omitting `user` constrains the read only to `tenant`;
it neither asserts nor infers a Turn actor.

`bindTools()` retains the narrower verb because it genuinely pairs durable
tool contracts with process-local executable handlers. It returns a new local
Agent, inline runner, or Turn wrapper and persists nothing.

### Behavior, memory, continuity, and tools compose independently

Agent addressing and local tool binding remain separate from Turn options.
Actor, memory, Conversation, metadata, idempotency, and narrowed limits do share
one execution-options object because they are all admitted facts of the same
Turn.

```ts
const analystWithTools = analyst
  .bindTools({ lookup_property: lookupProperty });

const result = await analystWithTools.run(input, {
  tenant: "acme",
  user: "alice",
  memory: { scope: "tenant", namespace: "leasing-team" },
  conversation: { key: "property-42", owner: "user" },
});
```

### The easy path is standalone

Calling an Agent or inline runner without `conversation` creates a standalone
Turn:

```ts
const context = { tenant: "acme", user: "alice" };
const first = await analyst.text("Classify this invoice.", context);
const second = await analyst.text("Classify this receipt.", context);

// Independent Turns. `second` cannot see `first` through a transcript.
```

Memory is independent: both Turns may still select the same MemorySpace.

Callers opt into transcript continuity with `conversation()`:

```ts
const conversation = analyst.conversation({
  tenant: "acme",
  user: "alice",
  key: ticket.id,
  owner: "user",
  retention: { ttlSeconds: 30 * 24 * 60 * 60 },
});

await conversation.text("My order has not arrived.");
await conversation.text("It was order 4821.");
```

### Recovery is lookup

Recovery does not require re-selecting an Agent because inline Turns have no
Agent and the Turn already records its exact behavior source:

```ts
const turn = client
  .turn(savedTurnId, { tenant: "acme", user: "alice" })
  .bindTools({ lookup_property: lookupProperty });

const result = await turn.result();
```

`turn(id, context)` is synchronous and makes no request. Its first operation
authorizes the Turn against that context. It does not repair, restart, retry,
resume, or otherwise mutate remote work.

### High-level and exact surfaces have separate doors

The facade expresses application workflows. Exact request-shaped APIs live
under `client.raw()`:

```ts
const record = await client.raw().turns.get(savedTurnId);
```

Generated request objects, raw events, transport pagination, and exact conflict
controls do not leak into the high-level types merely because they exist.

## Target TypeScript surface

The signatures below define the shape, not final generated type names.

### Client

```ts
interface Client {
  readonly agents: AgentCollection;

  agent<TOutput extends object = JsonObject>(
    key: string,
    options?: AgentKeyLookupOptions,
  ): Promise<Agent<TOutput>>;

  inline<TOutput extends object = JsonObject>(
    behavior: InlineBehavior<TOutput>,
  ): InlineRunner<TOutput>;

  turn<TOutput extends object = JsonObject>(
    turnId: string,
    context: TurnAccessContext,
  ): Turn<TOutput>;

  raw(): RawClient;
}

type AgentOwnedBy =
  | { tenant: string; user?: never }
  | { tenant: string; user: string };

interface AgentKeyLookupOptions {
  ownedBy?: AgentOwnedBy;
}

interface TurnAccessContext {
  tenant: string;
  user?: string;
}
```

Omitting `ownedBy` always selects the App-owned key namespace. Key lookup lives
deliberately only on singular `client.agent()`: it is the concise hot path,
while `client.agents` groups lifecycle operations and opaque-ID lookup. No
argument shape accepted by `agent()` can create or configure a resource.

### Agent collections and resources

```ts
interface AgentCollection {
  create<TOutput extends object = JsonObject>(
    input: CreateAgent<TOutput>,
  ): Promise<Agent<TOutput>>;

  getById<TOutput extends object = JsonObject>(
    id: string,
  ): Promise<Agent<TOutput>>;

  list<TOutput extends object = JsonObject>(
    options?: ListAgentsOptions,
  ): Promise<Page<Agent<TOutput>>>;
}

interface CreateAgent<TOutput extends object = JsonObject>
  extends BehaviorInput<TOutput> {
  key: string;
  name?: string;
  ownedBy?: AgentOwnedBy;
}

interface ListAgentsOptions {
  ownedBy?: AgentOwnedBy;
  archived?: boolean;
  cursor?: string;
}

type AgentOwner =
  | { kind: "app" }
  | { kind: "tenant"; tenant: string }
  | { kind: "user"; tenant: string; user: string };

interface Agent<TOutput extends object = JsonObject> {
  readonly id: string;
  readonly key: string;
  readonly owner: AgentOwner;
  readonly currentRevision: number;

  publish(input: BehaviorInput<TOutput>): Promise<AgentRevision<TOutput>>;
  archive(): Promise<Agent<TOutput>>;
  restore(): Promise<Agent<TOutput>>;

  bindTools(handlers: ToolHandlers): Agent<TOutput>;
  conversation(options: AgentConversationOptions): Conversation<TOutput>;

  start(
    input: TurnInput,
    options: AgentTurnOptions,
  ): Promise<Turn<TOutput>>;
  run(
    input: TurnInput,
    options: AgentTurnOptions,
  ): Promise<TurnResult<TOutput>>;
  text(input: TurnInput, options: AgentTurnOptions): Promise<string>;
}
```

The signatures show `options` as required because every execution explicitly
states its tenant, even when Agent ownership already constrains it. Every
language exposes one conceptual `Agent`, not an App/Tenant/User Agent and runner
class ladder. A user-owned Agent can execute only as its owner user.

Creating an Agent creates its first immutable revision atomically. Publishing
returns the new revision and advances the Agent's current pointer in one remote
operation. Empty Agents are not observable.

### Behavior and memory

```ts
interface BehaviorInput<TOutput extends object = JsonObject> {
  instructions: string;
  model: string | ModelPolicy;
  tools?: readonly ToolContract[];
  limits?: TurnLimits;
  outputSchema?: JsonSchema<TOutput>;
  memory?: DefaultMemoryPolicy;
}

type InlineBehavior<TOutput extends object = JsonObject> =
  BehaviorInput<TOutput>;

interface TurnLimits {
  totalTimeoutSeconds?: number;
  activeTimeoutSeconds?: number;
  waitingTimeoutSeconds?: number;
  maxOutputTokens?: number;
  maxEstimatedCostUsd?: number;
  maxIterations?: number;
}

type NarrowedTurnLimits = Readonly<TurnLimits>;

interface AgentRevision<TOutput extends object = JsonObject> {
  readonly id: string;
  readonly agentId: string;
  readonly revision: number;
  readonly behavior: Readonly<BehaviorInput<TOutput>>;
}

type DefaultMemoryPolicy =
  | { defaultScope: "none" }
  | { defaultScope: "user"; namespace?: string }
  | { defaultScope: "tenant"; namespace?: string };
```

`NarrowedTurnLimits` deliberately contains only scalar execution ceilings. The
type prevents a per-Turn object from carrying behavior overrides; admission
still performs the value-dependent check that every supplied ceiling is
positive and no greater than the selected revision, credential, or installation
maximum.

Shared memory is a tenant selection with an explicit namespace because that
namespace usually comes from host product data. The server-owned behavior
snapshot remains the authority for tool schemas; local handlers cannot replace
them.

### Execution options and inline runner

```ts
interface RunnerTurnOptions {
  idempotencyKey?: string;
  metadata?: Metadata;
  timeoutMs?: number;
  signal?: AbortSignal;
}

interface AgentTurnOptions extends RunnerTurnOptions {
  tenant: string;
  user?: string;
  memory?: MemorySelection;
  conversation?: ConversationSelection;
  limits?: NarrowedTurnLimits;
}

interface InlineTurnOptions extends RunnerTurnOptions {
  tenant: string;
  user?: string;
  memory?: InlineMemorySelection;
  conversation?: ConversationSelection;
  limits?: NarrowedTurnLimits;
}

interface InlineRunner<TOutput extends object = JsonObject> {
  bindTools(handlers: ToolHandlers): InlineRunner<TOutput>;
  conversation(options: InlineConversationOptions): Conversation<TOutput>;

  start(
    input: TurnInput,
    options: InlineTurnOptions,
  ): Promise<Turn<TOutput>>;
  run(
    input: TurnInput,
    options: InlineTurnOptions,
  ): Promise<TurnResult<TOutput>>;
  text(input: TurnInput, options: InlineTurnOptions): Promise<string>;
}

type ToolHandler<TInput extends object = JsonObject> = (
  input: TInput,
  context: TurnToolContext,
) => JsonValue | void | Promise<JsonValue | void>;

type ToolHandlers = Readonly<Record<string, ToolHandler<object>>>;

interface TurnToolContext {
  readonly turnId: string;
  readonly toolCallId: string;
  readonly signal: AbortSignal;
}
```

`bindTools()` stores an exact-name handler map on a new local wrapper. It makes
no request merely to bind the handlers and sends no handler-name field during
admission. When the exact effective behavior is available, the SDK validates
the names locally before invoking a handler; they do not enter the idempotency
fingerprint. Missing handlers follow the durable waiting policy defined by the
admitted tool contracts. Memory, actor, Conversation, and narrowed limits remain
admitted Turn facts rather than a chain of scope wrappers.

### Conversation

```ts
type ConversationSelection =
  | { id: string; key?: never; owner?: never }
  | ({
      key: string;
      id?: never;
      owner: "tenant" | "user";
    } & ConversationCreateOptions);

interface AgentConversationContext {
  tenant: string;
  user?: string;
  memory?: MemorySelection;
  limits?: NarrowedTurnLimits;
}

interface InlineConversationContext {
  tenant: string;
  user?: string;
  memory?: InlineMemorySelection;
  limits?: NarrowedTurnLimits;
}

type AgentConversationOptions = AgentConversationContext & ConversationSelection;
type InlineConversationOptions = InlineConversationContext & ConversationSelection;

interface ConversationTurnOptions extends RunnerTurnOptions {
  limits?: NarrowedTurnLimits;
}

interface Conversation<TOutput extends object = JsonObject> {
  start(
    input: TurnInput,
    options?: ConversationTurnOptions,
  ): Promise<Turn<TOutput>>;

  run(
    input: TurnInput,
    options?: ConversationTurnOptions,
  ): Promise<TurnResult<TOutput>>;

  text(
    input: TurnInput,
    options?: ConversationTurnOptions,
  ): Promise<string>;
}
```

`conversation({key, owner})` is a local continue-or-create binding; the first
Turn resolves the Conversation and admits work atomically. `conversation({id})`
continues one exact resource. `tenant` and `user` state Turn context; `owner`
states the keyed Conversation namespace. Conversation ownership never supplies
actor identity. The wrapper fixes local tenant, actor, memory, and Conversation
selection for its calls; a per-call option may further narrow its limits but
cannot replace that context. None of those bindings become durable Conversation
configuration.

The high-level wrapper serializes local calls for the same effective
Conversation identity. Exact concurrent conflict controls remain under raw
Turn admission.

### Turn

```ts
interface Turn<TOutput extends object = JsonObject> {
  readonly id: string;

  bindTools(handlers: ToolHandlers): Turn<TOutput>;
  status(signal?: AbortSignal): Promise<TurnSnapshot<TOutput>>;
  result(options?: WaitOptions): Promise<TurnResult<TOutput>>;
  updates(options?: StreamOptions): AsyncIterable<TurnUpdate<TOutput>>;
}

interface TurnSnapshot<TOutput extends object = JsonObject> {
  status: TurnStatus;
  messages: TurnMessage[];
  text: string | null;
  structuredOutput: TOutput | null;
  behaviorSource: "agent_revision" | "inline";
  agentId: string | null;
  agentRevisionId: string | null;
  memorySpaceId: string | null;
  conversationId: string | null;
  contentExpiresAt: Date | null;
}

interface TurnResult<TOutput extends object = JsonObject>
  extends TurnSnapshot<TOutput> {
  turn: Turn<TOutput>;
  admission?: {
    idempotencyKey: string;
    deduplicated: boolean;
  };
}

interface TurnUpdate<TOutput extends object = JsonObject> {
  snapshot: TurnSnapshot<TOutput>;
}
```

`admission` is present on a result reached from the fresh `start()` or `run()`
handle, including an exact idempotent replay. It is absent when the result came
from a recovery handle constructed later with `client.turn()` because that
handle did not observe the admission acknowledgement.

`start()` returns after durable admission. The returned Turn retains the local
handler map, but no background JavaScript promise is a durability guarantee.
`run()`, `turn.result()`, and `turn.updates()` drive compatible local tools
while attached. If no compatible process remains, the durable Turn waits until
one reattaches or its explicit waiting policy expires.

`run()` is specified and tested as `start()` followed by `turn.result()`.
There is no `startTurn()` or `runTurn()` alias. `text()` is `run()` plus required
assistant-text extraction; it is not called `ask()` because Agents also
classify, transform, and perform non-question-shaped work.

`status()` is passive and never drives tools. Leaving an update iterator,
aborting a wait, or hitting a local timeout detaches the observer; it does not
cancel the Turn.

## North-star workflows

These examples should compile in CI and supply the README workflows after the
breaking target is implemented.

### Publish once and run for every user

```ts
const analyst = await client.agents.create({
  key: "real-estate-analyst",
  instructions: "Analyze properties and local market conditions.",
  model: "anthropic/claude-sonnet-5",
  tools: [lookupPropertyContract],
  memory: { defaultScope: "user" },
});

const readyAnalyst = analyst.bindTools({
  lookup_property: lookupProperty,
});

const result = await readyAnalyst
  .conversation({
    tenant: "acme",
    user: "alice",
    key: "property-42",
    owner: "user",
  })
  .run("Is this property overpriced?");
```

Publishing an update is one operation regardless of tenant or user count:

```ts
await analyst.publish({
  instructions: updatedInstructions,
  model: "anthropic/claude-sonnet-5",
  tools: [lookupPropertyContract],
  memory: { defaultScope: "user" },
});
```

### Share Agent memory inside one tenant

```ts
const sharedAnalyst = analyst
  .bindTools({ lookup_property: lookupProperty });

const result = await sharedAnalyst.run(input, {
  tenant: "acme",
  user: "alice",
  memory: { scope: "tenant", namespace: "leasing-team" },
}); // Standalone Turn.
```

Alice remains the actor even though the selected memory is shared.

### Create a private user Agent

```ts
const dealCoach = await client.agents.create({
  key: "deal-coach",
  ownedBy: { tenant: "acme", user: "alice" },
  instructions: "Help me evaluate prospective property deals.",
  model: "anthropic/claude-sonnet-5",
  memory: { defaultScope: "user" },
});

const result = await dealCoach.run(input, {
  tenant: "acme",
  user: "alice",
});
```

Another user cannot resolve this Agent by key or ID.

### Run inline behavior without an Agent

```ts
const extractor = client.inline({
  instructions: "Extract the address and asking price.",
  model: "anthropic/claude-sonnet-5",
  outputSchema: propertySummarySchema,
});

const result = await extractor.run(document, {
  tenant: "acme",
  user: "alice",
  memory: { scope: "none" },
});

console.log(result.agentId);        // null
console.log(result.conversationId); // null
```

Selecting memory or a Conversation in the execution options is valid and still
creates no Agent.

### Admit work now and recover it elsewhere

```ts
const worker = analyst.bindTools(workerTools);

const turn = await worker.start(job, {
  tenant: "acme",
  idempotencyKey: job.id,
});
await jobs.save({ turnId: turn.id, idempotencyKey: job.id });
```

```ts
const turn = client
  .turn(saved.turnId, { tenant: "acme" })
  .bindTools(workerTools);

const result = await turn.result();
```

### Use exact request semantics

```ts
const turn = await client.raw().turns.create({
  tenantKey: "acme",
  userKey: "alice",
  behavior: {
    agent: {
      owner: "app",
      key: "real-estate-analyst",
      revision: "current",
    },
  },
  memory: { scope: "user" },
  conversation: {
    mode: "continue",
    id: conversationId,
    ifActive: "supersede",
  },
  input,
  idempotencyKey,
});
```

The exact wire form may use `{ id, revision }` instead. The server resolves the
owner-qualified key or ID, `current`, the MemorySpace, and the Conversation and
admits the Turn in one transaction. The retained relationship is the resolved
Agent and AgentRevision IDs, never `agentKey`.

## Behavioral contract

| Workflow | Request behavior | Retry and recovery guarantee |
| --- | --- | --- |
| `client.agent(key, {ownedBy?})` | One Agent read | The string is always a key; owner namespace is fixed and lookup fails immediately if absent or invisible |
| `agents.getById(id)` | One Agent read | Opaque ID lookup is explicit; credential-invisible resources are not found |
| `agents.create()` | One atomic Agent plus first-revision create | Idempotent create policy is explicit; no empty Agent is exposed |
| `agent.publish()` | One immutable revision create plus current-pointer update | One update affects later unpinned Turns without fan-out |
| `inline()` | No request | Creates a local runner; never provisions an Agent |
| `bindTools()` | No request | Returns a new local Agent, inline runner, or Turn wrapper; no durable mutation |
| `conversation({context, id/key})` | No request | Captures local Turn defaults; first Turn performs atomic continue or continue-or-create admission |
| Standalone `start()` | One admission with no Conversation | Exact behavior and optional MemorySpace are recorded atomically |
| Conversation `start()` | One admission with Conversation selection | Continuity resolution and Turn admission are atomic |
| Inline `start()` | One admission carrying an immutable behavior snapshot | No Agent or revision is created |
| `run()` / `text()` | `start()`, local tool loop as needed, authoritative terminal result | Admitted Turn remains recoverable if local waiting fails |
| `turn(id, context)` | No request until first operation | Reconstructs one Turn without requiring an Agent association |
| `turn.status()` | One current-state read | Passive; never executes local tools |
| `turn.updates()` | Resumable reduced-state stream | May drive bound local tools; detaching does not cancel |
| `turn.result()` | Wait/stream plus authoritative result read | May drive bound local tools; returns terminal work or typed failure |
| `raw().turns.create()` | Exactly the request-shaped admission | No facade read-before-write behavior |

Conformance tests must assert effective request bodies, headers, transactional
resolution, authorization, retries, recovery, erasure, and isolation across
the behavior × memory × Conversation matrix.

## Timeout and uncertain admission

A local timeout says only that the SDK stopped waiting. It does not prove that
the Turn stopped or that admission failed.

```ts
class TurnTimeoutError extends NvokenError {
  readonly turn?: Turn;
  readonly idempotencyKey?: string;
}
```

- If admission returned before waiting timed out, `turn` is present.
- If the admission transport outcome is unknown, the idempotency key created
  before the first request remains present so the caller can repeat the exact
  request and recover the same Turn.
- A caller never blindly starts a second Turn after a timeout; a tool may
  already have performed an external side effect.

An `AbortSignal` or local timeout detaches the caller. Only an explicit Turn
control asks the service to stop durable work.

## Conversation serialization

Local serialization is useful only if separately constructed wrappers for the
same effective Conversation share a lock. The target lock identity is:

- Conversation ID when selected by ID; or
- effective tenant, optional user ownership boundary, and Conversation key
  when selected by key.

Agent and MemorySpace are not part of Conversation identity. Including either
would permit two local wrappers to race on one transcript merely because they
selected different behavior or memory.

The lock is process-local convenience, not a distributed guarantee. Exact
cross-process conflict behavior remains the server's responsibility.

## Pre-cutover implementation versus accepted target

| Area | 0.29.0 | Accepted target |
| --- | --- | --- |
| Stored behavior | App-scoped Agent Definition plus immutable revisions | App-, tenant-, or user-owned Agent plus AgentRevision |
| Tenant runtime identity | Deliberately created tenant Agent points to one Definition | No required AgentInstance or tenant Agent aggregate |
| Agent construction | `client.agent(options)` mixes identity, provisioning, tools, and defaults | Awaited address-only `client.agent(key, {ownedBy?})` lookup plus `client.agents` lifecycle operations |
| Behavior key | `definitionKey` identifies reusable behavior; `agentKey` identifies tenant Agent | High-level `key` lookup; raw owner-qualified key or opaque ID resolves transactionally; no cross-resource `agentKey` or `definitionKey` |
| Memory | Definition policy selects tenant or `user_key` memory tied to `agent_id` | Independent MemorySpace: none, user namespace, or tenant namespace |
| Inline execution | Low-level Definition snapshots exist but ordinary Turn admission requires Agent identity | InlineBehavior is a first-class Turn source and creates no Agent |
| Conversation ownership | Session is bound to tenant Agent | Conversation owns transcript continuity, not behavior or memory |
| Standalone work | Unbound Agent calls omit public Session; hidden carrier remains | Turn independently omits Conversation and may also omit Agent and memory |
| Scope | `scoped({tenantKey,userKey})` plus identity options in Agent constructors | Tenant and actor are per-execution options; Agent ownership uses only `ownedBy` during lookup and lifecycle operations |
| Local tools | Arrays mixed into Agent construction or `withTools()` | Exact-name `bindTools()` after behavior selection or Turn recovery |
| Recovery | `client.invocation(id)` or proposed `agent.turn(id)` | Synchronous `turn(id, {tenant,user?})` recovery handle; Agent association not required |
| Public execution nouns | Session and Invocation | Conversation and Turn everywhere |
| Results and streaming | `AgentResult`, `InvocationHandle`, raw-first streams | One Turn/TurnResult facade, render-safe updates, raw under `raw()` |

The left column records 0.29.0 as migration evidence. It is not a compatibility
requirement. The published hard-cut contract already uses the target column;
the handwritten SDKs and CLI move to it in the same breaking release.

## Delivery order

1. Write the implementation PRD and migration inventory for current Agent
   Definitions, tenant Agents, memory, Sessions, retained facts, browser and
   anonymous constraints, idempotency, reporting, and composite foreign keys.
2. Publish the coordinated contract model: Agent and AgentRevision ownership,
   MemorySpace, Turn behavior-source union, independently optional Conversation
   and memory, and the Conversation/Turn terminology cutover. Preserve no
   public compatibility aliases.
3. Add cross-SDK fixtures covering App-, tenant-, and user-owned Agents,
   revision publication, inline behavior, all memory selections, standalone
   Turns, and Conversation-bound Turns.
4. Implement awaited address-only `client.agent(key, {ownedBy?})`, the Agent
   lifecycle collection, and a directly runnable Agent. Remove implicit
   provisioning, scoped-client ladders, Agent Definition, tenant Agent,
   `definitionKey`, and public AgentInstance concepts.
5. Add `inline()`, per-execution actor/memory/Conversation/limit options, and
   `bindTools()`, plus `conversation()` as the only high-level continuity
   wrapper.
6. Introduce the high-level Turn facade, `turn(id, context)` recovery,
   `TurnResult`, passive status, reduced updates, and typed timeout recovery.
7. Put generated exact Agent, MemorySpace, Conversation, and Turn APIs solely
   behind `raw()`.
8. Update README examples and release the breaking contract and facade change
   together in TypeScript, Go, Python, and Rust.

## Acceptance criteria

- A newcomer reads Agent as stored reusable behavior; no public Definition or
  Instance noun reverses that expectation.
- App-owned Agent publication affects subsequent unpinned Turns without writes
  proportional to tenant or user count.
- App-, tenant-, and user-owned Agents have explicit, enforced visibility.
- `client.agent(key, {ownedBy?})` is an awaited lookup whose arguments contain
  only Agent addressing; it never provisions or configures an Agent.
- App ownership is the key-lookup default; `ownedBy` explicitly names tenant-
  and user-owned key namespaces. Opaque ID lookup remains
  `agents.getById(id)` and takes no owner argument.
- Agent ownership and Turn context are separate: every execution states
  `tenant`, optional `user` states the actor, and `ownedBy` appears only in Agent
  key lookup and lifecycle operations.
- No `forTenant()`, `forUser()`, `scoped()`, or Agent/runner type ladder is
  required by the initial facade.
- Memory may be disabled, per-user, or tenant-namespaced without
  cloning Agent behavior.
- MemorySpace identity does not require an Agent and can be used by inline
  behavior or intentionally shared across Agents.
- Every Turn records exactly one resolved AgentRevision or inline source and one
  Turn-owned effective-behavior row, with only monotonic limit narrowing and
  independently optional Conversation and MemorySpace.
- `bindTools()` takes an exact-name handler map, returns a new local Agent,
  inline runner, or Turn wrapper, never changes durable configuration, and adds
  no admitted request field.
- `start()` returns one admitted Turn; `run()` is exactly `start()` followed by
  `turn.result()`. No `startTurn()` or `runTurn()` alias exists.
- `turn(id, context)` makes no request and does not repair, retry, restart, or
  resume work merely by constructing a handle.
- Timeout errors retain enough context to recover uncertain admission without
  guessing whether to submit another Turn.
- Conversation serialization uses Conversation identity, not Agent or
  MemorySpace identity.
- Exact Agent, MemorySpace, Conversation, and Turn APIs appear under `raw()`.
- HTTP, generated clients, SDKs, CLI, callbacks, events, errors, telemetry,
  console, and docs use the same Agent, MemorySpace, Conversation, and Turn
  model without compatibility aliases.
- The north-star examples compile and the full behavioral matrix passes in all
  four SDKs.

## Non-goals

- Making nvoken authoritative for tenant membership, user profiles, or host
  group membership.
- Treating a tenant MemorySpace namespace as proof of group authorization.
- Making process-local Conversation serialization a distributed concurrency
  guarantee.
- Preserving `scoped()`, `forTenant()`, `forUser()`, `bindTenant()`,
  `bindSession()`, Agent Definition, tenant Agent, AgentInstance, Session, or
  Invocation as aliases after cutover.
- Requiring every Turn to have an Agent, MemorySpace, or Conversation.
- Hiding exact request semantics from callers that intentionally choose
  `raw()`.

## Closed implementation decisions

- Browser idempotency keys are scoped by tenant and bound user. Trusted-host
  keys remain tenant-scoped.
- A new anonymous-token exchange selects the Agent's current revision only when
  that exact revision still satisfies the full anonymous eligibility policy.
  Existing access tokens remain pinned to their exact revision.
- Inline user and tenant memory requires an explicit namespace in handwritten
  facade types and at wire admission.
- Conversation fork always states the target owner explicitly. Source lineage
  never supplies ownership, and authorization must cover both source and
  target.
