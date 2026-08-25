# TypeScript SDK ergonomics and public terminology

**Status:** Accepted terminology and facade direction; not implemented. The
standalone execution contract and the four-SDK `start()` and `bindSession()`
foundations shipped in 0.29.0 under the current Agent Definition, Agent,
Session, and Invocation model.

**Date:** 2026-08-24

**Revised:** 2026-08-25 after newcomer reviews, complete-vocabulary comparison,
and review of shared behavior, private Agents, independent memory ownership,
inline behavior, and optional Conversation continuity.

**Reviewed version:** `@deepnoodle/nvoken` 0.29.0 at `3045dc4b`

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
const analyst = await client.agents.getByKey("real-estate-analyst");

const aliceAnalyst = analyst
  .forTenant("acme")
  .forUser("alice")
  .bindTools({ lookup_property: lookupProperty });

const conversation = aliceAnalyst.conversation({ key: "property-42" });
const answer = await conversation.text("Is this property overpriced?");
```

Every operation has one meaning:

- `agents.getByKey()` performs an Agent lookup by caller-owned key.
- `forTenant()` and `forUser()` create immutable local scope views.
- `bindTools()` attaches process-local implementations to durable tool
  contracts.
- `conversation()` selects optional continuity.
- `run()` admits a Turn and waits for its result.
- `start()` admits the same Turn and returns its durable handle.

There is no public `AgentDefinition`, `AgentInstance`, `definitionKey`, or
overloaded `client.agent()` in the target surface.

## Public model

### Agent and AgentRevision

An Agent is stored, reusable, versioned behavior. It owns a stable identity,
caller-owned key, visibility, archive state, and current revision pointer. An
AgentRevision is an immutable snapshot of instructions, model policy, tool
contracts, limits, output contract, and an optional default memory selection
policy.

Agents may be owned at three scopes:

- `client.agents` contains App-owned Agents visible throughout the App;
- `client.forTenant(key).agents` contains tenant-owned Agents visible only in
  that tenant; and
- `client.forTenant(key).forUser(key).agents` contains user-owned Agents visible
  only to that user inside the tenant.

One App-owned Agent serves every tenant and user. Publishing a revision
advances one pointer; it does not copy behavior into per-user instances.
Admission resolves the requested current revision and records its exact ID
before queuing work.

`key` is sufficient inside an Agent collection. A cross-resource wire
reference uses `agentKey`. `definitionKey` disappears because the target has no
separately addressable Agent Definition resource.

### MemorySpace

A MemorySpace is durable memory with its own inspection, retention, erasure,
and audit lifecycle. It is scoped inside an App and tenant but is independent
from Agent behavior and Conversation continuity.

```ts
type MemorySelection =
  | { scope: "none" }
  | { scope: "user"; namespace?: string }
  | { scope: "tenant"; namespace?: string }
  | { scope: "named"; key: string };
```

- `user` partitions memory by the bound user.
- `tenant` intentionally shares memory across users in the tenant.
- `named` lets trusted host code select a group or product-specific shared
  space, such as `leasing-team`.
- `none` disables durable memory and its tools.

Actor identity and memory selection are separate. A Turn attributed to Alice
may use Alice's memory, tenant memory, named team memory, or no memory. Binding
a user never silently chooses a MemorySpace.

An AgentRevision may declare a default memory selection so common calls do not
repeat policy. The policy selects a space; the Agent does not own that space.
An explicit runner selection overrides the default. Inline behavior may make
the same selection without creating an Agent.

Ordinary SDK calls may lazily resolve or create a MemorySpace during Turn
admission. Exact and administrative APIs expose MemorySpace IDs and lifecycle
operations.

### Conversation

A Conversation is optional retained transcript continuity. It owns ordered
messages, compaction, retention, metadata, and concurrency. It does not own an
Agent or MemorySpace.

A Conversation may therefore contain Turns using a stored Agent revision or
inline behavior, and may be memoryless or use a MemorySpace. A high-level
runner carries behavior and memory defaults into each Turn without making
those defaults part of Conversation identity.

### Turn

A Turn is one durable execution for one input, including the complete model-
and-tool loop. Every Turn records exactly one behavior source:

```ts
type BehaviorSource =
  | { kind: "agent_revision"; agentRevisionId: string }
  | { kind: "inline"; behavior: InlineBehavior };
```

It independently records zero or one Conversation and zero or one MemorySpace.
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

A single string never ambiguously means an opaque ID or caller-owned key:

```ts
await client.agents.getByKey("real-estate-analyst");
await client.agents.getById(savedAgentId);
```

The same applies to Conversation references:

```ts
runner.conversation({ key: ticket.id });
runner.conversation({ id: savedConversationId });
```

Collection creation is the only provisioning path. Retrieval, local binding,
and Turn admission never silently create an Agent.

### Scope is a local view

`forTenant()` and `forUser()` return immutable local views and make no network
request:

```ts
const tenant = client.forTenant("acme");
const alice = tenant.forUser("alice");
```

`bindTenant()` is rejected. “Bind” can imply mutation, durable association, or
remote provisioning. `forTenant()` reads as selection of a scoped view.

`bindTools()` retains the narrower verb because it genuinely pairs durable
tool contracts with process-local executable handlers. It returns a new local
runner and persists nothing.

### Behavior, memory, continuity, and tools compose independently

Durable Agent publication, MemorySpace selection, Conversation selection, and
local tool binding do not share one options bag. Their lifetimes and authority
are different.

```ts
const runner = analyst
  .forTenant("acme")
  .forUser("alice")
  .withMemory({ scope: "named", key: "leasing-team" })
  .bindTools({ lookup_property: lookupProperty });
```

### The easy path is standalone

Calling a runner directly creates a standalone Turn:

```ts
const first = await runner.text("Classify this invoice.");
const second = await runner.text("Classify this receipt.");

// Independent Turns. `second` cannot see `first` through a transcript.
```

Memory is independent: both Turns may still use the same MemorySpace if the
runner selected one.

Callers opt into transcript continuity with `conversation()`:

```ts
const conversation = runner.conversation({
  key: ticket.id,
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
  .forTenant("acme")
  .forUser("alice")
  .turn(savedTurnId)
  .bindTools({ lookup_property: lookupProperty });

const result = await turn.result();
```

`turn(id)` is synchronous and makes no request. Its first operation authorizes
the Turn against the scoped client. It does not repair, restart, retry, resume,
or otherwise mutate remote work.

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

### Client and scoped clients

```ts
interface Client {
  readonly agents: AppAgentCollection;

  forTenant(tenantKey: string): TenantClient;
  raw(): RawClient;
}

interface TenantClient {
  readonly agents: TenantAgentCollection;

  forUser(userKey: string): UserClient;
  inline<TOutput extends object = JsonObject>(
    behavior: InlineBehavior<TOutput>,
  ): TurnRunner<TOutput>;
  turn<TOutput extends object = JsonObject>(turnId: string): Turn<TOutput>;
}

interface UserClient {
  readonly agents: UserAgentCollection;

  inline<TOutput extends object = JsonObject>(
    behavior: InlineBehavior<TOutput>,
  ): TurnRunner<TOutput>;
  turn<TOutput extends object = JsonObject>(turnId: string): Turn<TOutput>;
}
```

Tenant and user clients are facade views, not server-owned Tenant or User
resources. They carry scope into later operations.

### Agent collections and resources

```ts
interface AppAgentCollection {
  create<TOutput extends object = JsonObject>(
    input: CreateAgent<TOutput>,
  ): Promise<AppAgent<TOutput>>;

  getByKey<TOutput extends object = JsonObject>(
    key: string,
  ): Promise<AppAgent<TOutput>>;
  getById<TOutput extends object = JsonObject>(
    id: string,
  ): Promise<AppAgent<TOutput>>;
}

interface TenantAgentCollection {
  create<TOutput extends object = JsonObject>(
    input: CreateAgent<TOutput>,
  ): Promise<TenantAgent<TOutput>>;
  getByKey<TOutput extends object = JsonObject>(
    key: string,
  ): Promise<TenantAgent<TOutput>>;
  getById<TOutput extends object = JsonObject>(
    id: string,
  ): Promise<TenantAgent<TOutput>>;
}

interface UserAgentCollection {
  create<TOutput extends object = JsonObject>(
    input: CreateAgent<TOutput>,
  ): Promise<UserAgent<TOutput>>;
  getByKey<TOutput extends object = JsonObject>(
    key: string,
  ): Promise<UserAgent<TOutput>>;
  getById<TOutput extends object = JsonObject>(
    id: string,
  ): Promise<UserAgent<TOutput>>;
}

interface CreateAgent<TOutput extends object = JsonObject>
  extends BehaviorInput<TOutput> {
  key: string;
  name?: string;
}

interface AgentResource<TOutput extends object = JsonObject> {
  readonly id: string;
  readonly key: string;
  readonly currentRevision: number;

  publish(input: BehaviorInput<TOutput>): Promise<AgentRevision<TOutput>>;
}

interface AppAgent<TOutput extends object = JsonObject>
  extends AgentResource<TOutput> {
  forTenant(tenantKey: string): TenantAgentRunner<TOutput>;
}

interface TenantAgent<TOutput extends object = JsonObject>
  extends AgentResource<TOutput>, TurnRunner<TOutput> {
  forUser(userKey: string): UserAgentRunner<TOutput>;
}

interface UserAgent<TOutput extends object = JsonObject>
  extends AgentResource<TOutput>, TurnRunner<TOutput> {}
```

An App Agent cannot run until a tenant is selected. A tenant-owned Agent can run
without a user actor or can add one with `forUser()`. A user-owned Agent is
already fully scoped when returned from the user's collection.

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

Named shared memory is deliberately selected at runtime because its key usually
comes from host product data. The server-owned behavior snapshot remains the
authority for tool schemas; local handlers cannot replace them.

### TurnRunner

```ts
interface TurnRunner<TOutput extends object = JsonObject> {
  withMemory(selection: MemorySelection): TurnRunner<TOutput>;
  withoutMemory(): TurnRunner<TOutput>;
  bindTools(handlers: ToolHandlers): TurnRunner<TOutput>;

  conversation(binding: ConversationBinding): Conversation<TOutput>;

  start(
    input: TurnInput,
    options?: RunnerTurnOptions,
  ): Promise<Turn<TOutput>>;

  run(
    input: TurnInput,
    options?: RunnerTurnOptions,
  ): Promise<TurnResult<TOutput>>;

  text(
    input: TurnInput,
    options?: RunnerTurnOptions,
  ): Promise<string>;
}

interface TenantAgentRunner<TOutput extends object = JsonObject>
  extends TurnRunner<TOutput> {
  forUser(userKey: string): UserAgentRunner<TOutput>;
}

interface UserAgentRunner<TOutput extends object = JsonObject>
  extends TurnRunner<TOutput> {}

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

`withMemory()` and `withoutMemory()` return new runners. `bindTools()` validates
handler names against the resolved behavior's durable contracts no later than
admission and returns a new local runner. None makes a request merely to create
the wrapper.

### Conversation

```ts
type ConversationBinding =
  | { id: string; key?: never }
  | ({ key: string; id?: never } & ConversationCreateOptions);

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

`conversation({key})` is a local continue-or-create binding; the first Turn
resolves the Conversation and admits work atomically. `conversation({id})`
continues one exact resource. Conversation options do not repeat behavior,
memory, actor, or tools already selected on the runner.

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
  messages: ConversationMessage[];
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

const aliceAnalyst = analyst
  .forTenant("acme")
  .forUser("alice")
  .bindTools({ lookup_property: lookupProperty });

const result = await aliceAnalyst
  .conversation({ key: "property-42" })
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
  .forTenant("acme")
  .forUser("alice")
  .withMemory({ scope: "named", key: "leasing-team" })
  .bindTools({ lookup_property: lookupProperty });

const result = await sharedAnalyst.run(input); // Standalone Turn.
```

Alice remains the actor even though the selected memory is shared.

### Create a private user Agent

```ts
const alice = client.forTenant("acme").forUser("alice");

const dealCoach = await alice.agents.create({
  key: "deal-coach",
  instructions: "Help me evaluate prospective property deals.",
  model: "anthropic/claude-sonnet-5",
  memory: { defaultScope: "user" },
});

const result = await dealCoach.run(input);
```

Another user cannot resolve this Agent by key or ID.

### Run inline behavior without an Agent

```ts
const extractor = alice
  .inline({
    instructions: "Extract the address and asking price.",
    model: "anthropic/claude-sonnet-5",
    outputSchema: propertySummarySchema,
  })
  .withoutMemory();

const result = await extractor.run(document);

console.log(result.agentId);        // null
console.log(result.conversationId); // null
```

Adding `.withMemory(...)` or `.conversation(...)` is valid and still creates
no Agent.

### Admit work now and recover it elsewhere

```ts
const worker = analyst
  .forTenant("acme")
  .bindTools(workerTools);

const turn = await worker.start(job, { idempotencyKey: job.id });
await jobs.save({ turnId: turn.id, idempotencyKey: job.id });
```

```ts
const turn = client
  .forTenant("acme")
  .turn(saved.turnId)
  .bindTools(workerTools);

const result = await turn.result();
```

### Use exact request semantics

```ts
const turn = await client.raw().turns.create({
  tenantKey: "acme",
  userKey: "alice",
  behavior: {
    agent: { id: analyst.id, revision: "current" },
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

The server resolves `current`, the MemorySpace, and the Conversation and admits
the Turn in one transaction.

## Behavioral contract

| Workflow | Request behavior | Retry and recovery guarantee |
| --- | --- | --- |
| `forTenant()` / `forUser()` | No request | Returns an immutable local scope view |
| `agents.getByKey()` / `getById()` | One Agent read | Key and ID meanings never overlap |
| `agents.create()` | One atomic Agent plus first-revision create | Idempotent create policy is explicit; no empty Agent is exposed |
| `agent.publish()` | One immutable revision create plus current-pointer update | One update affects later unpinned Turns without fan-out |
| `inline()` | No request | Creates a local runner; never provisions an Agent |
| `withMemory()` / `withoutMemory()` | No request | Returns a new local runner; selection resolves at admission |
| `bindTools()` | No request | Returns a new local runner or Turn; no durable mutation |
| `conversation({id/key})` | No request | First Turn performs atomic continue or continue-or-create admission |
| Standalone `start()` | One admission with no Conversation | Exact behavior and optional MemorySpace are recorded atomically |
| Conversation `start()` | One admission with Conversation selection | Continuity resolution and Turn admission are atomic |
| Inline `start()` | One admission carrying an immutable behavior snapshot | No Agent or revision is created |
| `run()` / `text()` | `start()`, local tool loop as needed, authoritative terminal result | Admitted Turn remains recoverable if local waiting fails |
| scoped `turn(id)` | No request until first operation | Reconstructs one Turn without requiring an Agent association |
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

## Current implementation versus accepted target

| Area | 0.29.0 | Accepted target |
| --- | --- | --- |
| Stored behavior | App-scoped Agent Definition plus immutable revisions | App-, tenant-, or user-owned Agent plus AgentRevision |
| Tenant runtime identity | Deliberately created tenant Agent points to one Definition | No required AgentInstance or tenant Agent aggregate |
| Agent construction | `client.agent(options)` mixes identity, provisioning, tools, and defaults | Explicit Agent collections, scoped runners, and named local attachments |
| Behavior key | `definitionKey` identifies reusable behavior; `agentKey` identifies tenant Agent | Collection `key` / cross-resource `agentKey` identifies stored Agent; no `definitionKey` |
| Memory | Definition policy selects tenant or `user_key` memory tied to `agent_id` | Independent MemorySpace: none, user, tenant, or named shared |
| Inline execution | Low-level Definition snapshots exist but ordinary Turn admission requires Agent identity | InlineBehavior is a first-class Turn source and creates no Agent |
| Conversation ownership | Session is bound to tenant Agent | Conversation owns transcript continuity, not behavior or memory |
| Standalone work | Unbound Agent calls omit public Session; hidden carrier remains | Turn independently omits Conversation and may also omit Agent and memory |
| Scope | `scoped({tenantKey,userKey})` plus identity options in Agent constructors | `forTenant()` / `forUser()` immutable views |
| Local tools | Arrays mixed into Agent construction or `withTools()` | Exact-name `bindTools()` after behavior selection or Turn recovery |
| Recovery | `client.invocation(id)` or proposed `agent.turn(id)` | Tenant/user-scoped `turn(id)`; Agent association not required |
| Public execution nouns | Session and Invocation | Conversation and Turn everywhere |
| Results and streaming | `AgentResult`, `InvocationHandle`, raw-first streams | One Turn/TurnResult facade, render-safe updates, raw under `raw()` |

Current behavior remains the truth until the coordinated breaking release.
Examples in this document are target API, not compatibility aliases or claims
about 0.29.0.

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
4. Implement Agent collections and `forTenant()` / `forUser()` scope views.
   Remove `client.agent()`, implicit provisioning, Agent Definition, tenant
   Agent, `definitionKey`, and public AgentInstance concepts.
5. Add `inline()`, `withMemory()`, `withoutMemory()`, and `bindTools()` runners,
   plus `conversation()` as the only high-level continuity spelling.
6. Introduce the high-level Turn facade, scoped `turn(id)` recovery,
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
- Agent lookup by key and ID uses different method names.
- No `client.agent()` constructor mixes identity, provisioning, local tools, or
  runtime defaults.
- `forTenant()` and `forUser()` are side-effect-free and preserve actor
  attribution independently from memory selection.
- Memory may be disabled, per-user, tenant-wide, or named shared without
  cloning Agent behavior.
- MemorySpace identity does not require an Agent and can be used by inline
  behavior or intentionally shared across Agents.
- Every Turn records exactly one resolved AgentRevision or immutable inline
  behavior snapshot, with independently optional Conversation and MemorySpace.
- `bindTools()` takes an exact-name handler map, returns a new local runner or
  Turn, and never changes durable configuration.
- `start()` returns one admitted Turn; `run()` is exactly `start()` followed by
  `turn.result()`. No `startTurn()` or `runTurn()` alias exists.
- Scoped `turn(id)` makes no request and does not repair, retry, restart, or
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
- Treating a named MemorySpace key as proof of group authorization.
- Making process-local Conversation serialization a distributed concurrency
  guarantee.
- Preserving `scoped()`, `bindTenant()`, `bindSession()`, Agent Definition,
  tenant Agent, AgentInstance, Session, or Invocation as aliases after cutover.
- Requiring every Turn to have an Agent, MemorySpace, or Conversation.
- Hiding exact request semantics from callers that intentionally choose
  `raw()`.

## Open implementation questions

- Whether constrained browser credentials select a pre-authorized MemorySpace
  ID, a bounded memory selector, or both.
- Whether a durable Binding or Deployment is needed later for per-tenant
  rollout pins after tenant Agent leaves the common model.
- Whether Conversation creation may declare immutable default behavior or
  memory, or whether those remain Turn selections exclusively.
- How compatible local tool executors discover and reclaim waiting Turns after
  the process that called `start()` detaches.
