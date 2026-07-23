# Invoke and Event Stream Review: Design for the Two-Event Consumer

**Status:** Review for team discussion

**Date:** 2026-07-23

**Scope:** `POST /v1/invocations` (request, acknowledgement, and the Invocation
resource), the SSE stream contract a host receives around one turn, and the
TypeScript facade that presents both. Includes verdicts on the open decisions
in the [TypeScript SDK surface review](2026-07-22-typescript-sdk-surface-review.md).

**Baseline:** Revision `ccfbcd9` plus the in-flight `agent_key` / `tenant_key`
identity rename. This review treats those renames as settled and does not
reargue them.

**Related work:** [Invoke API review](2026-07-22-invoke-api-review.md),
[TypeScript SDK surface review](2026-07-22-typescript-sdk-surface-review.md),
[TypeScript field report](../research/2026-07-22-typescript-invoke-and-sessions-field-report.md).

**Lens:** A developer who has never heard of nvoken, reading each field name
and event type for the first time. Where a claim depends on code, the file and
line are cited.

**Premise:** There are no external adopters yet. Every breaking change in this
document is near-free today and prohibitively expensive after GA. The bar is
not "acceptable." The bar is "obvious."

---

## 1. Executive summary

The invoke call is close to right. Its remaining problems are a deep input
envelope, timeout names that speak operator jargon to application developers,
and two fields whose names contradict their own semantics.

The event stream is a different story. Its durability discipline (durable
frames carry cursor IDs, previews never do, reconnects are race-free) is
genuinely excellent and ahead of the market. Its vocabulary is not. Today's
event names and payload fields describe nvoken's internals: leases, delivery
gaps, snapshot drains, connection rotation. They do not describe the
consumer's turn.

The organizing principle for the fix: **the minimal streaming consumer should
need exactly two event types.** Print `output_text.delta` frames, finish on
`invocation.result`, and safely ignore everything else. Watermarks, resync,
attempts, and rotation are for sophisticated consumers, discovered when
needed. Every naming recommendation below serves that shape.

Top recommendations, in priority order:

| # | Recommendation | Priority |
| --- | --- | --- |
| S1 | Design the invocation stream around the two-event consumer: `output_text.delta` plus `invocation.result` | P0, with the streaming PRD |
| S2 | Frame hygiene: one payload envelope, `attempt` not `lease_attempt`, drop `delta_sequence`, add `iteration`, dedicated frame schemas | P0, rides S1 |
| I1 | String `input` shorthand and optional `instructions` | P0 |
| I2 | Truth renames: `ended_at`, the timeout trio, `deadline_at`, deprecate `execution_lost` | P0, rides the identity-rename sweep |
| F1 | One five-verb facade: `text()`, `run()`, `invoke()`, `stream()`, `session()`, designed together | P1 |
| F2 | Session binding serializes turn admission and throws a typed busy error | P1 |
| F3 | Accept Standard Schema (Zod, Valibot, ArkType) wherever a JSON Schema is accepted | P1 |
| F4 | Commit to client-tool handlers on the tool definition; `run()` executes the loop | P2 direction, decide now |
| C1 | Record the facade as a cross-SDK surface convention, not a TypeScript feature | P1 |

Priorities follow the invoke review's scale: P0 before more of the contract
freezes, P1 next design cycle, P2 polish before GA.

## 2. Evidence that the streaming DX is unproven

Three facts from this repository, not from taste:

1. The flagship showcase never renders a token. Its SSE section collects event
   type names into a `Set` and counts messages
   (`examples/typescript-invoke-showcase/src/showcase.ts:332`).
2. The SDK's own `Reducer` ignores `generation.delta` entirely
   (`sdk/typescript/src/stream.ts:25`). The two fields that exist to order
   deltas, `lease_attempt` and `delta_sequence`, have zero consumers in this
   codebase.
3. No example in the repository prints streamed text. The program every
   newcomer writes first has never been written here.

Nobody has written "print tokens as they arrive" because today's contract
makes it genuinely hard to write: attach a Session-scoped stream, filter
frames by `invocation_id`, parse with generated `FromJSON` imports, handle
resync discard, and detect the end by re-checking the Invocation after the
Session goes idle (`sdk/typescript/src/stream.ts:90`). The R3 redesign in the
invoke review fixes the structure. This review is about getting the words
right when it lands, because the stream is where new users will form their
opinion of nvoken.

## 3. The invoke call

### 3.1 Request, field by field

**`input` is three levels of nesting to say hello.**

```json
{ "input": { "content": [ { "type": "text", "text": "Hello" } ] } }
```

The invoke review's R4a (accept a plain string, normalize server-side into one
text block, bump the fingerprint version) was accepted and has not landed.
With the identity renames done, this is the highest-value remaining wire
change for first impressions. Keep the block array for multi-block and future
multimodal input; the facade should accept `string | TextBlock[]`.

**`spec.instructions` should be optional** (R4b). An instruction-free turn is
a legitimate generation-only use, and a newcomer who omits instructions should
get a generation, not a 400.

**The limit names speak to operators, not applications.** `budgets` → `limits`
is already agreed. Inside the object, `wall_clock_timeout_seconds` and
`active_execution_timeout_seconds` force a newcomer to learn the parked/active
execution distinction just to set a timeout. With the planned waiting budget
(invoke review R6b), the names become a teachable trio:

| Current | Proposed |
| --- | --- |
| `wall_clock_timeout_seconds` | `total_timeout_seconds` |
| `active_execution_timeout_seconds` | `active_timeout_seconds` |
| new (R6b) | `waiting_timeout_seconds` |

One sentence then explains all three: total limits the turn, active limits
execution, waiting limits the park. Rename `wall_clock_deadline_at` to
`deadline_at` in the same sweep or the pair drifts.

**Session selector exclusivity is invisible in generated clients.** The
`session_id` XOR `session_key` rule is expressed with `not: required`
(`openapi/runtime.yaml:1047`), which most generators ignore. Accept that:
state the constraint in both field descriptions, enforce it server-side as
today, and make it unrepresentable in facades with union types.

**Fine as they are:** `idempotency_key` (the name is industry-standard and the
wire requirement is the product), the 64-block and 1 MiB caps, `model` with
the agreed `name` → `id` rename, and the one-element `provider_credentials`
array. On the last: the plural is deliberate runway for the multi-provider
routing on the vision roadmap. Add one sentence to the field description
saying so, and close that recurring debate.

### 3.2 The acknowledgement

`{agent_id, session_id, invocation_id, status, deduplicated}` is tight and
correct. Two notes:

- **Add `deadline_at`.** It costs nothing and tells every poller its budget
  without a second read. It should ride with the streaming PRD, since the
  streaming first frame carries the same payload.
- **Keep 202-on-terminal-replay.** One operation, one acknowledgement
  contract. It surprises once and is consistent forever; `deduplicated`
  explains it. Keep `deduplicated` as-is; `was_deduplicated` is churn without
  payoff.

### 3.3 The Invocation resource

Two names contradict their own semantics. Both are nearly free to fix now and
impossible later.

**`completed_at` is set when the Invocation fails or is cancelled.** The
cancellation and deadline-reap settlement queries both write it
(`internal/adapters/postgres/queries/runtime.sql:527`,
`runtime.sql:552`). A newcomer reading `status: "failed"` next to a non-null
`completed_at` sees a contradiction. The codebase's own vocabulary is
"settles" and "terminal settlement." Rename to **`ended_at`**.

**`execution_lost` is a ghost.** It lives in the public failure-code enum with
a description saying it is never written anymore
(`openapi/runtime.yaml:1738`). A newcomer reads seven codes and one is
historical. Mark it deprecated in the description now; drop it at the freeze.

Smaller observations:

- `active_execution_ms` is jargony but honest, and it pairs with the active
  timeout. Keep.
- The always-present-with-explicit-null pattern across `error`, `usage`,
  `provenance`, and `structured_output` is a real strength for typed SDKs.
  Keep it, and never mix in absent-means-null fields.
- The failure codes other than `execution_lost` are good: stable, lowercase,
  self-describing. `structured_output_unsatisfied` is a mouthful but precise.

### 3.4 The request after these changes

```json
{
  "agent_key": "support",
  "idempotency_key": "ticket-483:first-reply",
  "input": "My invoice is wrong.",
  "spec": {
    "model": { "provider": "anthropic", "id": "claude-sonnet-5" }
  }
}
```

Every remaining field is either identity the host owns or a decision the host
made. Nothing is ceremony.

## 4. The event stream

### 4.1 The structural problem

You cannot stream an invoke. The only stream is Session-scoped
(`GET /v1/sessions/{id}/transcript/stream`), so watching one turn means:
admit on one connection, open the Session firehose on another, filter by
`invocation_id`, reduce snapshot frames, and treat "Session went idle" as a
proxy for "my turn finished," then re-check
(`sdk/typescript/src/stream.ts:90`). The invoke review's R3 (SSE on create
plus an invocation-scoped stream) fixes the structure and is affirmed here.
The rest of this section is about the vocabulary, because most of today's
names should not survive into the invocation stream.

### 4.2 Audit of today's events

**`transcript.snapshot` is a misnomer carrying paging fields.** Each frame is
an incremental batch of newly durable messages and lifecycle changes; the
SDK's Reducer accumulates them. "Snapshot" tells a newcomer the opposite:
full-state replacement. Worse, the frame reuses the paged-read
`TranscriptSnapshot` schema, so every stream frame is required to carry
`has_more` and `next_page_token` (`openapi/runtime.yaml:1920`). Those are
paging concepts a stream consumer should never see. Give stream frames their
own schema and a truthful name (`transcript.update` on the Session stream).

**`generation.delta` leads with internals.** The hottest event in the product
requires, on every frame: `event_type`, `session_id`, `invocation_id`,
`lease_attempt`, `delta_sequence`, `delta`, `emitted_at`
(`openapi/runtime.yaml:1973`). Field by field:

- `lease_attempt`: fencing vocabulary. The consumer-relevant meaning is
  "discard buffered previews when this number increases." Rename to
  **`attempt`** and document exactly that sentence.
- `delta_sequence`: its own description must warn what it is not ("never a
  replay cursor," `openapi/runtime.yaml:1992`), which is a naming smell, and
  nothing consumes it, including the SDK. **Cut it from the public frame.**
  Live-bus ordering is an implementation concern.
- `delta.{type, text|thinking}` nesting: fine. It matches the Anthropic
  pattern and discriminated unions narrow cleanly.
- `content_index`: keep; UIs need block attribution.
- **Missing: message identity.** Deltas carry no iteration or message
  boundary, so in a multi-iteration tool turn a UI cannot tell where one
  assistant message ends and the next begins. Previews from two messages glue
  together until a durable frame corrects them. Add **`iteration`**.
- `emitted_at`: keep; it is harmless and useful for latency measurement.

**`stream.resync` is honest lossy design. Keep it.** Reason
`live_delivery_gap` and the discard-previews contract are correct. But that
contract is the core mental model of the stream, and it is currently taught
only inside an endpoint description. It needs a first-class place in the
streaming guide.

**`stream.end` has one real defect.** Reason `rotate` names what the server
did instead of what the consumer must do (reconnect with the cursor); keep it,
but rewrite the description to lead with the action. Reason `terminal` is the
real problem: it means the Session went idle, not that your turn finished,
which is why the SDK must re-check its target Invocation after every terminal
end. The invocation-scoped stream must end at the Invocation's own settlement.
That dissolves the defect for the invoke path.

**Inconsistent envelopes.** Three of four payloads carry `event_type` and
`session_id`; `transcript.snapshot` carries neither, and the
`TranscriptStreamEvent` oneOf has no discriminator
(`openapi/runtime.yaml:2032`). Generated clients cannot discriminate frames
without SSE framing. Adopt one rule: **every frame payload carries `type`**
(matching the SSE event name and the repo's own discriminator convention;
content blocks and tool specs already use `type`, not `event_type`) **plus its
scope IDs.**

### 4.3 What must be preserved

The durability discipline is the best part of this design and no competitor
has it:

- durable frames carry an SSE `id` that is the resume cursor;
- ephemeral frames never carry an `id`;
- a client that saw watermark W can never miss W+1 by arriving late;
- previews are honest, id-less, and discardable.

The redesign should change the words, not this law.

### 4.4 Target vocabulary for the invocation stream

Design test: a newcomer implements streaming with two event types and ignores
the rest safely.

| Event | Kind | Payload sketch | Notes |
| --- | --- | --- | --- |
| `invocation.accepted` | first frame | the same `InvocationAcknowledgement` as the JSON 202, plus `deadline_at` | one ack shape in both representations |
| `output_text.delta` | ephemeral | `{type, invocation_id, attempt, iteration, content_index, text}` | name matches `result.output_text`; add `thinking.delta` alongside |
| `invocation.update` | durable, `id` = watermark | `{type, invocation_id, invocation, new_messages}` | rename of the proposed `invocation.snapshot`; frames are incremental, the name should say so |
| `invocation.result` | durable, terminal | the full `InvocationResult` | the last frame a happy-path consumer needs |
| `stream.resync` | control | unchanged semantics | discard previews, wait for durable frames |
| `stream.end` | control | `{type, invocation_id, reason}` | fires only when this Invocation settles or the connection rotates |

The naming rule is teachable in one line: `invocation.*` frames are durable
state, `*.delta` frames are ephemeral previews, `stream.*` frames are
transport control.

Use the same delta vocabulary on the Session stream (`output_text.delta`,
`thinking.delta`, `transcript.update`) in the same PRD. Two names for the same
preview concept across two streams is the bilingual-API problem the surface
review warned about, moved into the stream.

Note the coherence dividend from earlier renames: `structured_output` in the
result, `output_text` in the result, `output_text.delta` on the wire. The
vocabulary converges instead of multiplying.

**Acceptance test for the design:** an idempotent replay of a completed
Invocation with `Accept: text/event-stream` must yield exactly three frames:
`invocation.accepted` (`deduplicated: true`), `invocation.result`,
`stream.end`. If that replay falls out of the model naturally, the model is
right.

### 4.5 The streaming program a newcomer should write

```ts
for await (const event of agent.stream("Tell me a story.")) {
  if (event.type === "output_text.delta") process.stdout.write(event.text);
  if (event.type === "invocation.result") {
    console.log(`\n${event.result.invocation.usage?.outputTokens} tokens`);
  }
}
```

Ten lines, two event types, no reducer, no generated imports, no
Session-idle detection. This program should exist in the repository and be
compiled by the SDK gate the day the streaming PRD lands.

## 5. The facade

### 5.1 Verdicts on the surface review's open decisions

The surface review's central proposal is right and is affirmed: bind an Agent
once, then `text()` / `run()` / `invoke()`, with generated idempotency, typed
errors, and `raw()` preserved. Verdicts on its open questions:

| Decision | Verdict | Rationale |
| --- | --- | --- |
| `Client.fromEnv()` | Change shape: resolve environment in `new Client()` | Every SDK the target developer uses (OpenAI, Anthropic, Stripe) reads env in the constructor. That is muscle memory. A second named constructor is one more concept in line one. Explicit options override env. Fold the quickstart's marked-`.env` loader into the same resolution so the npx quickstart and hello world share one code path |
| `agent_key`, `tenant_key` | Adopt (in flight) | `key` already means host-owned in `session_key` and `idempotency_key`; this unifies all four |
| `model.name` → `model.id` | Adopt | Every provider SDK says model ID; `name` reads display-oriented |
| `budgets` → `limits` | Adopt | Both reviews agree; only cost is a budget in the ordinary sense |
| `spec` → `execution` | Reject, keep `spec` | The facade hides it; newcomers meet `AgentOptions`, not `spec`. `execution` reads resource-like. Spend the rename budget elsewhere |
| `waiting` → `waiting_for_tool_results` | Reject, keep `waiting` | Closed enum, persisted state, every SDK, exhaustive consumers. Newcomers meet it in chapter five. `waitForAction()` and `pending_tool_calls` carry the intent |
| Tool mode `client` → `host` | Lean adopt | "Host" is the product's own word for the application. Cut this first if the sweep gets heavy; the cascade (type names, endpoint names) is the widest of the candidates |
| `deduplicated` → `was_deduplicated` | Reject | Bare past participles are conventional; churn without payoff |
| `Handle` → `InvocationHandle` | Adopt | Trivial and self-documenting |
| `Client.resume()` rename | Adopt direction, different name: `client.invocation(id)` | `resume` implies a remote state change; `getInvocationHandle` is clunky. Return a lazy handle synchronously, no fetch; the first `wait()` / `refresh()` / `result()` populates state. Cleaner name and one fewer round trip |
| `RunResult.output` | Rename to `structuredOutput` | `text` next to `output` recreates the exact ambiguity the `structured_output` wire rename just removed. `text` plus `structuredOutput` cannot be misread |
| Generated idempotency, wire stays required | Adopt exactly as written | The exact-body-and-key retry rule is the important part |
| No default `agentKey`, no hidden instructions | Adopt | Both partition identity; a hidden default would collide unrelated apps in one Account's session-key space |
| One-shot omits session selectors | Adopt | Verified: the wire already permits omitting both; the Runtime creates the Session |
| Typed tenant selectors, `waitForResult()`, `waitForAction()`, concise retry and polling names, typed errors, pricing out of hello world | Adopt | As proposed |

### 5.2 What the surface review missed

**Streaming must be a first-class verb.** The proposed facade has three verbs
and no stream. If the verb set is designed now, `agent.stream()` returning an
async iterable of the typed union in section 4.4 must be designed with it,
even if it ships after the wire lands. Otherwise a four-verb vocabulary
freezes and the fifth arrives with a different shape. The current
`handle.stream(consumeCallback)` with `StreamEvent.data: unknown` is the
weakest surface in the SDK: stringly-typed events, payload parsing left to
generated imports, and a `Reducer` class exported because the wire demands
one. When the invocation stream lands, the Reducer leaves the golden path and
stays only behind the Session-scoped multi-turn helper, which is what that
stream is actually good at.

**`agent.session()` makes the busy-session trap more likely, and the proposal
does not specify concurrency.** One nonterminal Invocation per Session is the
rule. The binding makes multi-turn so easy that developers will call
`chat.text()` from concurrent request handlers and hit
`session_invocation_active`. The binding is the right place to fix this:

- serialize turn admission per binding (an internal promise chain);
- surface the cross-process case as a typed `SessionBusyError` carrying the
  active Invocation ID from the 409 details;
- document the pattern next to the binding, not in a far-away guide.

**Tool handlers are the real end state. Commit to the direction now.**
`run()` throwing on an Agent with client tools is correct for today. But the
destination every comparable SDK reached (Vercel AI SDK, the Anthropic tool
runner) is a handler on the tool definition:

```ts
const lookupOrder = defineClientTool({
  name: "lookup_order",
  description: "Look up an order by ID.",
  inputSchema,
  handler: async (input) => orders.get(input.order_id),
});
```

With handlers present, `run()` and `text()` execute the loop: invoke, wait
for actionable, dispatch, submit, repeat. nvoken's durable ToolCalls make this
loop safer than any competitor's version: crash mid-dispatch, resume the
handle, replay the result idempotently. That is a marketing sentence, not
just a convenience. Deciding the direction now prevents the facade from
shipping shapes that fight it later.

**Schema ergonomics: accept Standard Schema.** `defineJsonSchema<T>` requires
writing every type twice: a TS interface and a JSON Schema, with silent drift
between them. Accept any Standard Schema v1 library (Zod 4, Valibot, ArkType)
wherever a JSON Schema is accepted (`outputSchema`, tool `inputSchema`), with
no hard dependency. One declaration then yields the inferred type and the
serialized schema. This is a larger day-two win than several of the proposed
renames combined.

**Cross-SDK convention.** Go, Python, and Rust facades already exist with the
same Client/Handle/raw shape. The verbs, the binding, the error types, and
the stream vocabulary should be recorded as an SDK surface convention that all
four languages track, or the SDKs drift and the docs fork.

**Fingerprint cost of renames.** Material equality is computed over the
canonical request, so the wire renames and the string-input normalization each
require a new fingerprint version. The machinery absorbs this (v1 through v6
exist). Land all wire changes in this document in one coordinated revision so
one version (v7) covers them, and the team explains one break, not three.

### 5.3 Hello world after all of this

```ts
import { Client } from "@deepnoodle/nvoken";

const agent = new Client().agent({
  agentKey: "support",
  instructions: "Be concise.",
});

console.log(await agent.text("Hello."));
```

Six lines. No env parsing, no UUIDs, no session key, no budgets, no status
branch, no formatter imports. Every durable guarantee still holds underneath:
exact model selection, a generated idempotency key reused across retries, an
immutable admitted spec, and a handle-recoverable Invocation.

## 6. Consolidated rename table

All breaking; all cheap now. Confidence reflects how strongly this review
holds the position.

| Surface | Current | Proposed | Confidence |
| --- | --- | --- | --- |
| Wire | `agent_ref` / `tenant_ref` | `agent_key` / `tenant_key` | Done (in flight) |
| Wire | `model.name` | `model.id` | High |
| Wire | `budgets` | `limits` | High |
| Wire | `wall_clock_timeout_seconds` | `total_timeout_seconds` | High |
| Wire | `active_execution_timeout_seconds` | `active_timeout_seconds` | Medium |
| Wire | `wall_clock_deadline_at` | `deadline_at` | High |
| Wire | `completed_at` | `ended_at` | High |
| Wire | `execution_lost` | deprecate now, drop at freeze | High |
| Wire | `input` object-only | also accept plain string | High |
| Wire | `instructions` required | optional | High |
| Wire | tool mode `client` | `host` | Lean adopt; first to cut |
| Wire | `spec` | keep | High (keep) |
| Wire | `waiting` | keep | High (keep) |
| Wire | `deduplicated` | keep | High (keep) |
| Wire | `provider_credentials` plural | keep; document the routing runway | High (keep) |
| Stream | `transcript.snapshot` | `transcript.update`, dedicated schema, no paging fields | High |
| Stream | `generation.delta` | `output_text.delta` / `thinking.delta` | High |
| Stream | `lease_attempt` | `attempt` | High |
| Stream | `delta_sequence` | remove | High |
| Stream | delta frames | add `iteration` | Medium |
| Stream | `event_type` on some frames | `type` on every frame, plus scope IDs | High |
| Stream | proposed `invocation.snapshot` | `invocation.update` | Medium |
| Stream | `stream.end` reason `rotate` | keep; rewrite description to lead with "reconnect" | Low |
| Facade | `Handle` | `InvocationHandle` | High |
| Facade | `Client.resume()` | `client.invocation(id)`, lazy, synchronous | High |
| Facade | `Client.get()` | `getInvocation()` | High |
| Facade | `RunResult.output` | `RunResult.structuredOutput` | High |
| Facade | `maximumAttempts` etc. | `maxAttempts`, `minDelayMs`, `maxDelayMs`, `minPollIntervalMs`, `maxPollIntervalMs` | High |
| Facade | `Client.fromEnv()` proposal | env resolution inside `new Client()` | Medium |

## 7. Recommended sequence

Sliced to PRD-sized steps, contract-first. The wire changes land in one
coordinated break with one fingerprint version, while the adopter count is
zero.

1. **Contract polish PRD (rides the identity-rename sweep).** `model.id`,
   `limits` and the timeout trio, `deadline_at`, `ended_at`, `execution_lost`
   deprecation, string `input`, optional `instructions`, tool mode `host` if
   kept in scope, the `provider_credentials` runway sentence, `deadline_at`
   on the acknowledgement. One OpenAPI revision, one fingerprint version
   (v7), regenerate all four SDKs, update the conformance server, fixtures,
   examples, and guides together.
2. **Streaming PRD (R3 with the section 4.4 vocabulary).** SSE on create, the
   invocation-scoped stream, the two-event consumer contract, uniform frame
   envelopes, the three-frame replay acceptance test, and the same delta
   vocabulary applied to the Session stream.
3. **Facade PRD.** Constructor env resolution, `client.agent()`, the five
   verbs including async-iterable `stream()`, `client.invocation(id)`,
   generated idempotency, typed `NvokenError` / `InvocationError` /
   `SessionBusyError`, session binding with serialized admission,
   `InvocationHandle` with `waitForResult()` and `waitForAction()`.
4. **Schema ergonomics PRD.** Standard Schema acceptance for `outputSchema`
   and tool `inputSchema`; typed inference through `run()` and tool dispatch.
5. **Tool handler PRD.** `handler` on `defineClientTool`; `run()` and
   `text()` execute the dispatch loop; document crash-resume replay as the
   differentiator.
6. **Docs and convention.** The six-step onboarding progression from the
   surface review, plus the cross-SDK surface convention document.

**Acceptance criteria for the arc:**

- hello world in six application lines, no env parsing, no keys;
- streamed tokens in ten lines using exactly two event types;
- the three-frame idempotent replay stream;
- a multi-iteration tool turn renders without glued previews;
- generated idempotency key visible on handle and result; caller-supplied
  keys survive exact replay;
- concurrent `chat.text()` calls on one binding serialize instead of 409ing;
- typed structured output from a Zod schema with no casts;
- the raw generated client still exposes every operation.

## 8. What not to change

Affirmed explicitly, so this review cannot be read as a rewrite request:

- Commit-before-acknowledge admission; the handler never owns execution.
- Required wire-level idempotency with versioned canonical fingerprints.
- The canonical transcript as the single durable content representation;
  `output_text` and `InvocationResult` stay read-time compositions.
- The SSE cursor law: durable frames carry IDs, previews never do, reconnects
  are race-free. This is the best part of the stream. Change the words only.
- Honest lossy previews with `stream.resync` discard semantics.
- The always-present-with-explicit-null resource pattern.
- The tight five-field acknowledgement and 202-on-terminal-replay.
- The one-nonterminal-Invocation Session rule; the facade absorbs it, the
  wire keeps it.
- Strict unknown-field rejection, typed failure codes, `request_id` on every
  error.
- `raw()` as the exact escape hatch in every SDK.

## Conclusion

nvoken's durable machinery is the reason to choose it. The remaining work is
to stop making newcomers read the machinery's internal vocabulary. The invoke
request should contain only host identity and host decisions. The stream
should let a developer write `for await (const event of agent.stream(...))`
and see text, then discover watermarks, attempts, and resync only when their
product needs them. Every rename in this review is in service of that: one
identity suffix, one limits vocabulary, one delta family, one durable-frame
family, and a result model whose names match on the wire, in the stream, and
in the SDK.

There are no external users yet. This is the last cheap moment to make the
contract obvious. Spend it.
