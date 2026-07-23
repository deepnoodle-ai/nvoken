# TypeScript SDK Surface Review: Make the First Invocation Obvious

**Status:** Proposal for team discussion

**Date:** 2026-07-22

**Scope:** The handwritten TypeScript SDK facade, the public names it exposes,
and the wire-contract names that materially affect every SDK.

**Audience:** A developer who knows TypeScript but knows little or nothing
about nvoken.

**Related work:** [Invoke API review](2026-07-22-invoke-api-review.md),
[TypeScript field report](../research/2026-07-22-typescript-invoke-and-sessions-field-report.md),
and [TypeScript onboarding design](../design/typescript-onboarding.md).

## Executive summary

nvoken's current TypeScript SDK exposes its durable machinery before it
delivers the first answer. A new developer must understand environment
validation, model selection, pricing capability, Agent identity, Session
identity, idempotency, execution specs, limits, asynchronous lifecycle
states, failure formatting, and result composition to print one short reply.

Those concepts are important, but they should not all be prerequisites.

The proposed developer model is:

```text
Client.fromEnv()   connect using conventional local configuration
    .agent()       bind one host-owned Agent definition
    .text()        get text
    .run()         get a complete typed result
    .invoke()      get a durable asynchronous Invocation handle

client.raw()       use the exact generated API when needed
```

The first successful interaction should look like this:

```ts
import { Client } from "@deepnoodle/nvoken";

const agent = Client.fromEnv().agent({
  agentKey: "typescript-package-quickstart",
  instructions: "Be concise.",
});

console.log(await agent.text("Reply with a short hello."));
```

This is not a proposal to weaken the durable wire contract. The SDK should
still send an exact model selection and idempotency key on every admission.
It should resolve safe defaults, generate an idempotency key when the caller
does not provide one, reuse the exact request for internal retries, and expose
the resolved values to applications that need them.

The central recommendation is to preserve two layers:

1. an ergonomic, progressive TypeScript facade for application developers;
2. an exact generated client for protocol-level control.

Simple things should be simple. Durable, resumable, tool-driven, multi-tenant
workflows should remain possible without leaving the package.

### Relationship to the earlier invoke review

The broader [invoke API review](2026-07-22-invoke-api-review.md) evaluates
Runtime flexibility and competitive positioning. This review uses a narrower
question: what does each name and required step communicate to a TypeScript
developer who has no nvoken context?

That newcomer lens deliberately reopens several earlier recommendations:

- prefer `agent_key`, `tenant_key`, and the existing `session_key` over making
  all three `*_ref`;
- reconsider `spec` in favor of `execution` or `executionSpec`;
- reconsider the generic `waiting` status if it only means waiting for host
  tool results;
- reconsider a permanently one-element `provider_credentials` array; and
- reconsider `Client.resume()` because it reconstructs a local handle rather
  than changing remote execution state.

These differences are presented as decisions for the team, not as adopted
contract changes. The earlier review's result-model and long-polling
recommendations remain complementary to the facade proposed here.

## 1. The developer's first impression today

The current minimal application is complete and operationally careful, but it
reads more like a qualification program than a first SDK interaction:

```ts
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
  const pricing = await client.pricingCapability({ provider, id: model });
  console.log(`pricing=${pricing.status} registry=${pricing.registryVersion}`);

  const handle = await client.invoke({
    agentKey: "typescript-package-quickstart",
    sessionKey: `typescript-package-${randomUUID()}`,
    idempotencyKey: `typescript-package-message-${randomUUID()}`,
    input: "Reply with a short hello.",
    spec: {
      instructions: "Be concise.",
      model: { provider, id: model },
      limits: { maxOutputTokens: 100, maxIterations: 1 },
    },
  });
  const invocation = await handle.wait();
  if (invocation.status !== "completed") {
    throw new Error(
      formatInvocationFailure(handle.invocationId, invocation, provider),
    );
  }
  console.log(`agent> ${await handle.text()}`);
}
```

Before seeing an answer, the developer encounters:

| Concept | Why it appears | Why it should not block hello world |
| --- | --- | --- |
| Manual environment validation | The constructor requires explicit transport config and the request requires a model | The SDK can validate its own conventional environment |
| Pricing capability | Demonstrates cost-awareness | It does not verify provider access and is not required to invoke |
| Random Session key | Forces a new Session | A one-shot invocation can let the Runtime create the Session |
| Random idempotency key | Satisfies the wire contract | The SDK can generate and reuse one for process-local retries |
| Inline execution spec | The wire request is a complete immutable snapshot | A bound Agent can capture repeated configuration once |
| Explicit limits | Demonstrates controls | Runtime defaults already make them optional |
| Handle polling | Execution is asynchronous | A high-level method can compose admission, waiting, and result read |
| Terminal-state branch | `wait()` returns every terminal state | A high-level method can throw a typed Invocation failure |
| Error formatters | The example tries to be production-complete | Normal SDK errors should already be actionable |

The issue is not any one line. It is that the API exposes transport,
durability, identity, policy, and lifecycle concerns at the same level as the
input text.

## 2. Design goals

### 2.1 Optimize for progressive disclosure

A developer should be able to learn nvoken in this order:

1. connect;
2. define the Agent they want to use;
3. get an answer;
4. continue a Session;
5. add durable recovery identity;
6. add structured output or tools;
7. use background execution, streaming, cursors, and raw APIs.

The first example should not teach steps four through seven.

### 2.2 Preserve durable semantics

Ergonomics must not make retries unsafe:

- the wire request still carries an idempotency key;
- the SDK generates it once before admission when omitted;
- all ambiguous-admission retries use the exact body and key;
- caller-supplied durable keys remain supported;
- generated keys are exposed on handles and results;
- documentation states that cross-process recovery requires persisting or
  supplying a durable key.

### 2.3 Make ownership visible in names

Use one vocabulary everywhere:

- `*Id` means an opaque identity generated by nvoken;
- `*Key` means a stable identity supplied and owned by the host;
- `*Cursor` means an opaque continuation position.

This makes `agentId` versus `agentKey`, and `sessionId` versus `sessionKey`,
understandable without first reading the identity design.

### 2.4 Keep the immutable execution snapshot

Environment and Agent defaults are SDK conveniences, not ambient Runtime
policy. Before admission, the SDK resolves them into the complete request.
The Runtime still persists the exact instructions, model, limits, tools, and
output contract used for the Invocation.

### 2.5 Keep an exact escape hatch

The handwritten facade should not attempt to mirror every generated operation.
`client.raw()` remains the exact OpenAPI-derived client for uncommon options,
new endpoints, and protocol-level integrations.

## 3. Proposed TypeScript surface

### 3.1 `Client.fromEnv()`

Add an environment-aware constructor:

```ts
const client = Client.fromEnv();
```

It should resolve:

| Setting | Resolution |
| --- | --- |
| Base URL | `NVOKEN_BASE_URL`, otherwise `http://localhost:8080` |
| API key | Required `NVOKEN_API_KEY` |
| Default model provider | Optional `NVOKEN_PROVIDER`; must be paired with `NVOKEN_MODEL` |
| Default model ID | Optional `NVOKEN_MODEL`; must be paired with `NVOKEN_PROVIDER` |

It should report one actionable configuration error that identifies every
missing or invalid transport value and an incomplete model pair. An Agent must
provide a model when the client has no default. The package already targets
Node.js 20 or newer, so an explicit environment-aware entry point is
appropriate.

The existing constructor remains for explicit configuration:

```ts
const client = new Client({
  baseUrl: "https://nvoken.example.com",
  apiKey: secret,
  defaultModel: {
    provider: "openai",
    id: "gpt-5",
  },
});
```

Explicit options override environment defaults. `Client.fromEnv()` should be
the documented local and server-side path; the constructor should remain the
test, framework, and programmatic path.

### 3.2 Bind an Agent once

Add `client.agent()`:

```ts
const agent = client.agent({
  agentKey: "support",
  instructions: "Help the customer clearly and concisely.",
});
```

The bound Agent is an SDK object, not a new stored Runtime resource. It captures
the host-owned definition that would otherwise be repeated on every invoke:

```ts
interface AgentOptions<TOutput extends object = JsonObject> {
  agentKey: string;
  instructions: string;
  model?: ModelSelection;
  limits?: InvocationLimits;
  tools?: Tool[];
  outputSchema?: JsonSchema<TOutput>;
}
```

This respects nvoken's product boundary: the host remains the source of truth
for Agent definitions, while nvoken receives an immutable snapshot on every
Invocation.

Do not default `agentKey` to `"default"` and do not inject a hidden generic
prompt. Both values affect identity and behavior and should be chosen by the
application.

### 3.3 Offer three levels of execution

#### Text convenience

```ts
const text = await agent.text("Reply with a short hello.");
```

`text()` should:

1. admit the Invocation;
2. wait for successful completion;
3. read the composed Invocation result;
4. return canonical nonempty assistant text;
5. throw a typed `InvocationError` for failure, cancellation, or missing text.

It is the default documentation path for ordinary chat.

#### Complete result

```ts
const result = await agent.run("Classify this support request.");

console.log(result.text);
console.log(result.output);
console.log(result.invocationId);
console.log(result.sessionId);
```

`run()` should return a completed result carrying the information an
application commonly needs:

```ts
interface RunResult<TOutput extends object> {
  invocationId: string;
  sessionId: string;
  agentId: string;
  idempotencyKey: string;
  wasDeduplicated: boolean;
  text: string | null;
  output: TOutput | null;
  messages: SessionMessage[];
  usage: InvocationUsage | null;
}
```

The exact shape should reuse the governing `InvocationResult` model rather
than inventing another representation. `run()` is composition, not a second
source of truth.

#### Durable asynchronous handle

```ts
const handle = await agent.invoke("Prepare the account summary.");

await jobs.put({
  invocationId: handle.invocationId,
  idempotencyKey: handle.idempotencyKey,
});
```

`invoke()` should return immediately after durable admission. It remains the
path for:

- background work;
- host tool calls;
- custom waiting;
- cancellation;
- streaming;
- process-independent resumption.

Rename the exported `Handle` type to `InvocationHandle`. Add a composed helper
such as `waitForResult()` so callers do not have to remember the
`wait()`-then-`result()` sequence:

```ts
const result = await handle.waitForResult();
```

Keep `wait()`, `refresh()`, `result()`, `submitToolResults()`, `cancel()`, and
`stream()` for advanced control.

### 3.4 Bind a Session when conversation identity matters

The one-shot path should not require a Session key. The Runtime creates the
Session and returns its ID.

For a host-owned conversation, bind it explicitly:

```ts
const chat = agent.session({
  tenantKey: "customer-acme",
  sessionKey: "ticket-42",
});

await chat.text("My invoice is wrong.");
await chat.text("Which charge did I dispute?");
```

For a production message record, supply durable admission identity:

```ts
const result = await chat.run(message.text, {
  idempotencyKey: message.id,
});
```

The Session binding should accept either `sessionId` or `sessionKey`, never
both. The TypeScript type should make the invalid combination impossible.

This creates a straightforward progression:

```text
agent.text()          one turn in a Runtime-created Session
agent.session()       repeated turns in a host-owned conversation
chat.run(..., key)    process-independent durable recovery
```

### 3.5 Make generated idempotency the simple default

`idempotencyKey` should be optional throughout the handwritten facade,
including `invoke()`.

When omitted, the SDK should:

1. generate a UUID before serializing the request;
2. retain the exact serialized body and generated key;
3. reuse both across every retry;
4. expose the key on `InvocationHandle` and `RunResult`.

This provides correct in-process retry behavior and keeps idempotency out of
hello world:

```ts
await agent.text("Hello.");
```

Production applications can opt into cross-process recovery without changing
methods:

```ts
await agent.run(message.text, {
  idempotencyKey: message.id,
});
```

The wire contract should continue to require `idempotency_key`. A generated
key cannot recover an ambiguously admitted request after the caller loses its
process state; that limitation belongs in the production reliability guide,
not in the first example.

### 3.6 Errors should be useful without formatting helpers

The shortest example should not need a top-level `try`/`catch` or imports for
`formatNvokenError` and `formatInvocationFailure`.

High-level methods should throw:

- `NvokenError` for authentication, validation, rate limiting, transport, and
  protocol failures;
- `InvocationError` for a failed or cancelled admitted Invocation.

`InvocationError` should carry the Invocation ID, Session ID, terminal status,
stable failure code, request ID, and provider diagnostics that are safe to
expose. Its normal `message` should already be suitable for logs.

Formatting helpers can remain for command-line programs that want a consistent
presentation, but they should not be required to understand an error.

## 4. Before and after

### 4.1 Hello world

Before:

```ts
const client = new Client({ baseUrl, apiKey });
const handle = await client.invoke({
  agentKey: "support",
  sessionKey: randomUUID(),
  idempotencyKey: randomUUID(),
  input: "Hello.",
  spec: {
    instructions: "Be concise.",
    model: { provider, id: model },
    limits: { maxOutputTokens: 100, maxIterations: 1 },
  },
});
const invocation = await handle.wait();
if (invocation.status !== "completed") {
  throw new Error(formatInvocationFailure(handle.invocationId, invocation, provider));
}
console.log(await handle.text());
```

After:

```ts
const agent = Client.fromEnv().agent({
  agentKey: "support",
  instructions: "Be concise.",
});

console.log(await agent.text("Hello."));
```

### 4.2 Production multi-turn chat

Before:

```ts
const handle = await client.invoke({
  agentKey: "support",
  tenantKey: customer.id,
  sessionKey: conversation.id,
  idempotencyKey: message.id,
  input: message.text,
  spec: {
    instructions: supportInstructions,
    model: supportModel,
    limits: supportLimits,
  },
});
await handle.wait();
const text = await handle.text();
```

After:

```ts
const chat = supportAgent.session({
  tenantKey: customer.id,
  sessionKey: conversation.id,
});

const result = await chat.run(message.text, {
  idempotencyKey: message.id,
});

console.log(result.text);
```

The production example retains every meaningful durable identity. It stops
resending static Agent configuration and stops manually composing lifecycle
operations.

### 4.3 Structured output

```ts
interface Classification {
  category: "billing" | "technical" | "other";
  needsHuman: boolean;
}

const classifier = client.agent({
  agentKey: "support-classifier",
  instructions: "Classify the request.",
  outputSchema: defineJsonSchema<Classification>({
    type: "object",
    properties: {
      category: { enum: ["billing", "technical", "other"] },
      needsHuman: { type: "boolean" },
    },
    required: ["category", "needsHuman"],
    additionalProperties: false,
  }),
});

const { output } = await classifier.run("I was charged twice.");
```

The output type is established where the Agent is defined and flows through
every run without casts.

### 4.4 Host tools

Host tools need the asynchronous level unless the SDK also accepts tool
handlers:

```ts
const handle = await supportAgent.invoke("Where is order 42?");
const invocation = await handle.waitForAction();

for (const call of invocation.pendingToolCalls) {
  await handle.submitToolResults([
    {
      toolCallId: call.id,
      content: await dispatch(call),
    },
  ]);
}

const result = await handle.waitForResult();
```

Whether `run()` should execute registered host-tool handlers automatically
is a separate design decision. It should not silently hang when an Invocation
needs caller action. Until handlers exist, `run()` should reject an Agent with
host tools or throw an actionable error that includes its handle.

## 5. Defaults: what the SDK should and should not decide

| Concern | Recommended default | Reason |
| --- | --- | --- |
| Local base URL | `http://localhost:8080` | Matches the official local Runtime |
| API key | Read `NVOKEN_API_KEY`; error if absent | Conventional and explicit |
| Model | Read provider and model ID from environment or client config | Removes repeated plumbing while preserving an exact request |
| Idempotency key | Generate once when omitted | Safe for internal retries and approachable for simple code |
| Session selector | Omit | Runtime creates a Session and returns its ID |
| Limits | Omit | Runtime resolves bounded installation defaults |
| Pricing preflight | Do not run automatically | It adds latency and does not verify provider-account access |
| Agent key | No default | It is application identity |
| Instructions | No default | A hidden prompt is hidden application behavior |
| Tenant key | Omit | Selects the default tenant partition |

Pricing capability belongs in cost-control documentation:

```ts
const capability = await client.getModelPricingCapability(model);
```

Applications that enforce estimated-cost limits can call it at startup or
configuration time. It should not be prerequisite to the first generation.

## 6. Naming review

### 6.1 Governing identity vocabulary

The current `ref` suffix is vague. A new developer cannot tell whether
`agentKey` is nvoken's Agent ID, an object reference, a display name, or an
external identifier.

Adopt this rule:

```text
agentId       opaque ID generated by nvoken
agentKey      stable key supplied by the host
sessionId     opaque ID generated by nvoken
sessionKey    stable key supplied by the host
tenantKey     stable partition key supplied by the host
```

`agentName` is not recommended because it sounds mutable and display-oriented.
`externalAgentId` is accurate but verbose and creates two kinds of `Id`.
`agentKey` is concise, pairs naturally with `sessionKey`, and communicates
lookup identity.

### 6.2 Strongly recommended renames

| Current TypeScript | Proposed TypeScript | Current wire | Proposed wire | Reason |
| --- | --- | --- | --- | --- |
| `agentKey` | `agentKey` | `agent_key` | `agent_key` | Caller-owned stable identity, not a generic reference |
| `tenantKey` | `tenantKey` | `tenant_key` | `tenant_key` | Same identity rule |
| `model.id` | `model.id` | `model.id` | `model.id` | Provider-defined model identifier, not display name |
| `Handle` | `InvocationHandle` | — | — | Self-documenting exported type |
| `Client.get()` | `getInvocation()` | — | — | Clear at the call site |
| `Client.resume()` | `getInvocationHandle()` | — | — | Does not imply a remote state transition |
| `deduplicated` | `wasDeduplicated` | `deduplicated` | Consider `was_deduplicated` | Describes the admission event as a boolean |

The identity names should change in OpenAPI, callbacks, errors, examples, and
generated clients together. Exposing `agentKey` in the handwritten SDK while
curl and generated users see `agent_key` creates a bilingual API and moves the
confusion into documentation.

### 6.3 Replace invalid boolean combinations with selectors

List APIs currently accept mutually exclusive `tenantKey` and
`defaultTenant`. A plain object can represent an invalid request:

```ts
client.listSessions({
  tenantKey: "acme",
  defaultTenant: true,
});
```

Use a union:

```ts
type TenantSelector =
  | { key: string; default?: never }
  | { default: true; key?: never };

client.listSessions({
  tenant: { key: "acme" },
});

client.listSessions({
  tenant: { default: true },
});
```

Omitting `tenant` on an Account-wide list means no tenant filter. Admission
can continue to use optional `tenantKey`, where omission means the default
partition.

### 6.4 Recommended configuration renames

| Current | Proposed | Confidence | Reason |
| --- | --- | --- | --- |
| `spec` | `execution` | Medium | Makes the immutable launch configuration apparent at the field |
| `limits` | `limits` | High | The object contains time, token, cost, and iteration ceilings |
| `wallClockTimeoutSeconds` | `totalTimeoutSeconds` | High | Avoids systems jargon |
| `activeExecutionTimeoutSeconds` | `activeTimeoutSeconds` | Medium | Shorter while preserving the active/parked distinction |
| `maximumAttempts` | `maxAttempts` | High, facade only | Conventional TypeScript and consistent with other `max*` fields |
| `minimumDelayMs` | `minDelayMs` | High, retry only | Conventional and concise |
| `maximumDelayMs` | `maxDelayMs` | High, retry only | Conventional and concise |
| Wait `minimumDelayMs` | `minPollIntervalMs` | High, facade only | Says that the delay controls polling |
| Wait `maximumDelayMs` | `maxPollIntervalMs` | High, facade only | Distinguishes polling from admission retry |
| Transcript input `cursor` | `afterCursor` | Medium, facade only | Distinguishes an input checkpoint from page continuation |

`spec` and `execution` deserve explicit team discussion. The earlier invoke
review recommended keeping `spec` because it distinguishes configuration from
the Agent identity anchor. This review favors `execution` because a newcomer
can understand it without that design history. `executionSpec` is the more
explicit compromise if `execution` feels too resource-like.

### 6.5 Status and tool vocabulary

`waiting` currently means that the Invocation is waiting for host tool
results. If that remains its only meaning, `waiting_for_tool_results` is more
descriptive:

```ts
if (invocation.status === "waiting_for_tool_results") {
  // The application must submit results.
}
```

This is a high-cost rename because it touches persisted state, OpenAPI enums,
generated clients, tests, and host state machines. It is worth deciding before
a compatibility freeze, but it should be a coordinated contract migration.

At the facade level, prefer intent-revealing helpers:

```ts
await handle.waitForAction();
await handle.waitForResult();
```

They are clearer than requiring a newcomer to learn
`wait({ until: "actionable" })` before their first host tool.

The tool mode name `client` also deserves scrutiny. In a server-side SDK,
“client” can mean the SDK, the host, a browser, or the end user's device.
`caller` or `host` is clearer. The recommended direction is:

```ts
mode: "host"
```

Keep `callback` for Runtime-delivered callback tools.

### 6.6 Singular versus plural provider credential selection

`providerCredentials` is currently an array constrained to at most one item.
That shape is either:

- unnecessary plurality for today's one-model Invocation; or
- intentional compatibility runway for future multi-provider execution.

If one Invocation will continue to select one provider credential, rename it
to singular `providerCredential`. If multi-provider specs are an intended
near-term contract, keep the plural name and eventually permit multiple
entries. Do not retain a permanently plural one-element collection without
stating why.

### 6.7 Names to keep

These names are already clear:

- `sessionId` and `sessionKey`;
- `idempotencyKey`;
- `invocationId`;
- `instructions`;
- `input`;
- `inputSchema` and `outputSchema`;
- `provider`;
- `invoke`;
- `run`;
- `cursor`, where it is ordinary list pagination;
- `raw()`.

Keep `input` rather than `prompt`: the Runtime already supports content
blocks, and `input` leaves room for non-text content.

## 7. Facade and wire-contract boundary

Not every ergonomic improvement requires a breaking REST change.

### Facade-only changes

- `Client.fromEnv()`;
- `client.agent()`;
- `agent.text()`, `agent.run()`, and bound `agent.invoke()`;
- `agent.session()`;
- optional facade idempotency with generated keys;
- typed tenant selectors;
- `InvocationHandle`;
- `waitForAction()` and `waitForResult()`;
- concise retry and polling option names;
- typed terminal errors.

### Changes that should be coordinated through OpenAPI

- `agent_key` to `agent_key`;
- `tenant_key` to `tenant_key`;
- model `name` to `id`;
- `limits` to `limits`;
- `spec` to `execution`, if adopted;
- `waiting` to `waiting_for_tool_results`, if adopted;
- provider credential singularity, if adopted.

The generated API should remain mechanically derived from OpenAPI. Do not
hand-edit generated names. If compatibility is already required, introduce
an explicit contract-version or deprecation plan rather than accepting both
names indefinitely.

## 8. Recommended documentation progression

The public TypeScript guide should be organized by developer intent.

### Step 1: Get one answer

```ts
const agent = Client.fromEnv().agent({
  agentKey: "hello",
  instructions: "Be concise.",
});

console.log(await agent.text("Say hello."));
```

No pricing preflight, random UUID import, Session key, budget, status branch,
or error formatter.

### Step 2: Continue a conversation

```ts
const chat = agent.session({ sessionKey: "conversation-42" });

await chat.text("Remember the word cedar.");
console.log(await chat.text("What word did I ask you to remember?"));
```

### Step 3: Make admission recoverable across processes

```ts
const result = await chat.run(message.text, {
  idempotencyKey: message.id,
});
```

Explain here—not in step one—that generated keys support internal retry while
caller-controlled durable keys support recovery after process loss.

### Step 4: Request structured output

Define the schema once on the Agent and show the inferred result type.

### Step 5: Handle tools

Explain actionable waiting, stable ToolCall IDs, equal result replay, and the
one-active-Invocation Session rule.

### Step 6: Operate durable background work

Introduce handles, persistence, resumption, cancellation, transcript cursors,
SSE, pagination, pricing capability, and cost limits.

This sequence teaches complexity when the developer first needs it.

## 9. Recommended decision and implementation sequence

### Phase 1: Agree on vocabulary before adding more public surface

Decide:

1. `agentKey` and `tenantKey`;
2. model `id`;
3. `limits`;
4. `spec` versus `execution` versus `executionSpec`;
5. whether `waiting` and tool mode `client` should be renamed;
6. provider credential singularity.

Identity vocabulary is the most important decision. It appears in every
Invocation, Session lookup, callback, log, generated client, and guide.

### Phase 2: Update breaking wire names, if adopted

Change OpenAPI first, then regenerate SDKs and update backend mappings,
callbacks, fixtures, examples, and documentation in one coordinated revision.

### Phase 3: Add the ergonomic TypeScript layer

Implement:

1. `Client.fromEnv()`;
2. `client.agent()`;
3. generated idempotency in the handwritten facade;
4. `agent.text()` and `agent.run()`;
5. `InvocationHandle.waitForResult()`;
6. typed Invocation terminal errors;
7. `agent.session()`;
8. `waitForAction()`.

### Phase 4: Rewrite and exercise onboarding

Validate the progression against a locally deployed Runtime and at least one
real provider:

- first text in fewer than ten application lines;
- no manual environment parsing;
- no explicit idempotency or Session key in hello world;
- exact internal admission retry with the generated key;
- generated key visible on result and handle;
- production caller-controlled key survives exact replay;
- two-turn Session context;
- typed structured output;
- host tool waiting and result submission;
- actionable errors for invalid environment and terminal failure;
- raw generated API remains available.

## 10. Team decisions requested

| Decision | Recommendation |
| --- | --- |
| Happy-path model | Adopt `Client.fromEnv().agent(...).text(...)` |
| Complete-result method | Add `agent.run()` |
| Background method | Keep `agent.invoke()` returning `InvocationHandle` |
| Simple-example idempotency | Generate in the SDK and omit from hello world |
| Durable idempotency | Accept caller keys on the same methods and document in the production step |
| Agent identity name | Rename `agentKey` / `agent_key` to `agentKey` / `agent_key` |
| Tenant identity name | Rename `tenantKey` / `tenant_key` to `tenantKey` / `tenant_key` |
| Model identifier | Rename `model.id` to `model.id` |
| Static Agent configuration | Bind once with `client.agent()` and serialize on every Invocation |
| One-shot Session behavior | Omit selectors and use the returned Runtime Session ID |
| Multi-turn behavior | Bind with `agent.session()` |
| Pricing | Remove from hello world; keep explicit for cost policy |
| Limits | Omit from hello world; support Agent defaults and Invocation overrides |
| Errors | High-level methods throw useful typed errors without formatter imports |
| Generated API | Preserve under `client.raw()` |

## Conclusion

nvoken's durability is a reason to use the product, but the developer should
experience it as confidence rather than ceremony.

The first interaction should communicate three ideas:

1. connect to nvoken;
2. describe the Agent;
3. ask it to do something.

Agent and Session identity, durable replay, tools, structured output,
multi-tenancy, pricing controls, streaming, and recovery should appear as
capabilities the developer can add—not as a tax paid before the first answer.

The proposed facade makes that possible without reducing the rigor of the
wire contract. It resolves conventional defaults at the SDK boundary,
serializes an exact immutable request, and keeps the generated API available
for full control. That is the API shape most likely to feel obvious to a new
TypeScript developer while still earning nvoken's durable-runtime claims.
