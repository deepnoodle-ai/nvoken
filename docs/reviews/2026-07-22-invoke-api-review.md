# Invoke API Review: Usability, Flexibility, Competitive Position

**Status:** Review for discussion
**Date:** 2026-07-22
**Revised:** 2026-07-22, twice, after internal review. First pass promoted
streaming to P0 and defined one result model. Second pass closed three
implementation gaps: queueing now requires staged input with transcript
promotion, POST streaming requires a bounded replay buffer and an explicit
HTTP boundary, and the Invocation stream gets a complete cursor law built on
one watermark that also serves long-polling. `InvocationResult` is slimmed,
revision semantics are corrected, and the messages filter is demoted from P0.
**Scope:** The Runtime invoke surface: `POST /v1/invocations` plus the reads,
streams, and commands a host needs around one agent turn.
**Inputs:** `docs/design/api.md`, `docs/design/architecture.md`,
`docs/design/vision.md`, `docs/design/decisions.md`, `openapi/runtime.yaml`,
the Go services and Postgres queries, the TypeScript SDK, Claude Managed
Agents docs (2026-04 beta), Mistral Agents and Conversations docs.
**Lens:** Simple things should be simple. Complex things should be possible.
nvoken should be ahead of Claude Managed Agents (CMA) and Mistral Agents, not
behind.

---

## 1. Summary

The durable core of this API is genuinely differentiated. Neither CMA nor
Mistral has an answer to durable admission with replay-safe idempotency,
checkpoint recovery, a parked `waiting` state that holds no compute, or
cursor-resumable streaming. The tenancy model is first-class where competitors
have nothing. Zero provisioning before the first turn beats both. These are
the right bets and this review does not question them.

The problems concentrate on the read side of the golden path. The API admits
work beautifully and then makes the common case hard. Getting plain assistant
text requires a minimum of three round trips plus a client-side filter. A
schema-bearing invocation gets its answer in one poll. The optional feature
(structured output) is more ergonomic than the default (text). That is an
inversion of intent, and it is the first thing a developer comparing us to
Mistral will hit. Mistral returns the answer in the body of the one call they
make.

The organizing principle for the fix: **invoking, waiting, and streaming are
three delivery modes over one Invocation result model.** Define the result
once, order it with one watermark, and deliver it as a blocking JSON read, as
the terminal frame of a stream, or as a plain background read. Every
recommendation below serves that shape.

The second gap is interaction flexibility. A busy Session returns `409` and
pushes queueing onto every host. CMA buffers input events and supports
steering and interrupt. Mistral supports branching a conversation from any
entry. nvoken supports none of these today. Queueing is desirable but not
additive: the current implementation commits caller input to the canonical
transcript at admission, so queued input needs a staging design first
(section 4.5).

None of the fixes require abandoning the canonical-transcript law
(decision 14) or the equality-proven output projection (decision 26).
Decision 16 needs two explicit, recorded amendments: a streaming
representation of the create operation, and staged input for queued turns.
The JSON `202` acknowledgement contract itself stays intact.

Top recommendations, in priority order:

| # | Recommendation | Priority |
| --- | --- | --- |
| R1 | One slim `InvocationResult` model, a composed result read, and `output` renamed `structured_output` | P0 |
| R2 | One Invocation watermark; watermark-based long-polling on the result read | P0 |
| R3 | Streaming as a delivery mode: SSE on create with a bounded replay buffer, plus a cursor-complete Invocation stream | P0 |
| R4 | Request-floor easing: string `input`, optional `instructions`; idempotency stays required at the wire | P1 |
| R5 | Busy-session posture: document the pattern now; queueing requires staged input and transcript promotion | P1 |
| R6 | Per-tool deadlines and an explicit `waiting_timeout_seconds` | P1 |
| R7 | Land spec-by-digest caching to complete the inline-spec story | P1 |
| R8 | Naming sweep before freeze: `budgets`, `session_key`, pricing path, `structured_output` | P2 |

Priorities: P0 = before more external adopters see the contract. P1 = next
design cycle. P2 = polish before any GA freeze. P3 = roadmap.

## 2. Competitive scoreboard

Where each product stands on the axes that matter for an embedded agent
backend:

| Capability | nvoken today | CMA | Mistral | Position |
| --- | --- | --- | --- | --- |
| Zero provisioning before first turn | Yes | No (agent + environment + session) | Partial (`model` shortcut) | Ahead |
| Answer in hand, fewest calls | 3+ calls | Stream attach | 1 call | Behind both |
| Synchronous or long-poll option | No | No | Yes (default) | Behind Mistral |
| Streaming on the create call | No | No (separate attach) | Stream variants | Behind Mistral |
| Durable admission, idempotent replay | Yes | No | No | Ahead |
| Crash recovery from checkpoints | Yes | Opaque (`rescheduling`) | No | Ahead |
| Resumable streaming with cursors | Yes | No documented resume | No | Ahead |
| Steering / interrupt mid-turn | No | Yes | No | Behind CMA |
| Concurrent input on one conversation | `409` | Buffered events | Not addressed | Behind CMA |
| Branch / regenerate from history | No | No | `restart` from entry | Behind Mistral |
| Multi-tenant partitioning | First-class | None | None | Ahead |
| Durable HITL tools without compute | Yes | Permission confirmations | Function calling | Ahead |
| Per-turn cost and budget guardrails | Yes | No | No | Ahead |
| Stored, versioned agent configs | No, by design | Yes | Yes | Different bet |
| Managed sandbox and builtins | No, by design | Yes | Yes | Different bet |

The last two rows are deliberate scope cuts and should stay cut. The
"behind" rows are all addressable without touching the product law.

## 3. The golden path, measured

Hello world on each product, counted in HTTP requests to a printed answer:

- **Mistral:** 1. `conversations.start` returns `outputs` and usage in the
  response body.
- **CMA:** 5 on first run (create agent, create environment, create session,
  send event, attach stream), 2 steady-state. The stream makes it feel live.
- **nvoken:** 1 invoke, then N status polls, then M transcript pages, then a
  client-side filter. Minimum 3 requests, typically more. The Invocation read
  never contains the text.

The SDK hides this behind `handle.wait()` and `handle.text()`, but the wire
contract is what the README leads with, what curl users see, and what every
non-SDK integration builds against. With R1 through R3 below, the paths
become:

- **Streamed:** 1 request. Invoke with `Accept: text/event-stream`, watch the
  turn, receive the full result as the terminal frame.
- **Blocking JSON:** 2 requests. Invoke, then one watermark-aware result read.
- **Background:** unchanged. Invoke, come back later by durable ID.

All three are durable, replay-safe, and reconnectable. One request for the
streamed path matches Mistral while keeping guarantees Mistral does not have.
That trade we can defend everywhere.

## 4. Findings and recommendations

### 4.1 One result model, and getting the answer (P0, R1)

**Observation.** For a text turn, `GET /v1/invocations/{id}` returns lifecycle,
usage, provenance, and `output: null`. Assistant text lives only in
`SessionMessage` rows (`architecture.md`, decision 14). The messages endpoint
has no `invocation_id` filter, so the TypeScript SDK pages the entire Session
from the beginning and filters client-side
(`sdk/typescript/src/client.ts:340`). On a long-lived Session, reading one
turn's reply reads the whole transcript. Structured output, the opt-in
feature, arrives in one poll via the sanctioned projection (decision 26). And
the field carrying it is named `output`, which implies text turns have none.

**Analysis.** No decision chose this outcome. Decision 14 locked content into
the transcript and decision 26 punched exactly one hole for machine-readable
output. Text never got an equivalent, so the default case has the worst
ergonomics in the API. The single-representation law bans storing a second
copy of content. It does not ban composing a read-time projection. That
distinction is the whole fix. The fix must also be one model, not a
grab-bag read: the same representation should arrive whether the host blocks,
streams, or polls, or the asymmetry just reappears between delivery modes.
The model must also not duplicate fields the nested Invocation already
carries; two copies of the same value in one payload is a new equality
obligation.

**Recommendations.**

- **R1a.** Define one slim `InvocationResult` contract object and serve it
  from `GET /v1/invocations/{invocation_id}/result`:

  ```json
  {
    "invocation": { "...": "authoritative Invocation state" },
    "messages": [],
    "output_text": "Hello"
  }
  ```

  Semantics:

  - `invocation` is the full authoritative resource. After the R1b rename it
    already carries `structured_output`, its provenance, and
    `pending_tool_calls`. The result does not repeat them at the top level.
  - `messages` are this Invocation's canonical transcript rows, composed at
    read time. Nothing is stored twice, so decision 14 is intact.
  - `output_text` is a convenience projection: the Invocation's assistant
    text blocks concatenated. Non-null only for `completed`.
  - Failed and cancelled Invocations keep their messages readable as
    evidence, but `output_text` stays null. Evidence must not masquerade as
    successful output.
  - The terminal frame of every stream (R3) carries this same object. One
    result model, three delivery modes.

- **R1b.** Rename `Invocation.output` to `structured_output` (and
  `output_provenance` to match) before freeze. Reserving the word "output"
  for structured JSON alone recreates the asymmetry this review is trying to
  remove. Breaking, so it must land now while the adopter count is low.
- **R1c (demoted from P0).** An `invocation_id` filter on
  `GET /v1/sessions/{session_id}/messages` was originally P0 here. With
  `InvocationResult.messages`, SDKs no longer need it for the golden path.
  Keep it only as a convenience for raw transcript consumers. If kept, the
  filter must join the message cursor binding; today that cursor binds only
  Account and Session (`internal/services/recovery_cursor.go`), and a
  filter outside the binding breaks the cursor's normalized-filter rule.
- **R1d (optional, later).** If profiling shows the composed read is hot,
  revisit a stored terminal text projection under the same equality-proven
  discipline as decision 26. Do not start here. Start with composition.

### 4.2 One watermark, and waiting on it (P0, R2)

**Observation.** The contract offers no way to wait server-side. Background
`202` admission is the only mode (decision 16). The SDK polls with exponential
backoff (100 ms to 2 s). Mistral is synchronous by default.

**Analysis.** The admission law is right: the request handler must never own
execution, and `202` after commit is the correct durable acknowledgement.
But the law constrains admission, not reads. A read that blocks until state
changes owns nothing.

Two subtleties make the naive designs wrong:

1. A "hold until terminal" wait sleeps through `waiting`, the one state where
   the host must act immediately on pending ToolCalls, until the tool
   deadline kills the turn. The wait must wake on actionable change.
2. The lifecycle revision alone is not a complete change signal. Partial
   client tool-result batches append tool-result messages and advance the
   checkpoint without reserving a lifecycle revision; only closing the final
   open call does (`internal/services/client_tools.go:284`). A revision-only
   wait would miss real changes to the result representation.

The durable ordering that captures both already exists at Session scope: the
transcript position is the composite
`(message_sequence, lifecycle_revision)` pair
(`internal/services/recovery_cursor.go`). Message-sequence advance covers
content changes including partial tool results; lifecycle revision covers
state transitions including entering `waiting` and terminal settlement.

**Recommendations.**

- **R2a.** Define one opaque **Invocation watermark**: the composite of the
  Invocation's covered message sequence and lifecycle revision, bound to
  Account and Invocation. Expose it on `InvocationResult` (and optionally the
  plain get). This watermark is also the stream cursor in R3; wait and
  stream share one ordering law. Do not expose a bare integer named
  `revision`: it would promise more than the lifecycle revision delivers.
- **R2b.** Add bounded long-polling to the result read:

  ```http
  GET /v1/invocations/{id}/result?wait_seconds=60&after_watermark=...
  ```

  Return immediately when the stored watermark already exceeds
  `after_watermark`; otherwise hold and return when the watermark advances
  (message appended, `waiting` entered, pending set changed by a committed
  result, terminal settled) or the window expires (return current state).
  A client that saw watermark W can never miss W+1 by arriving late; this is
  the race-free reconnect contract.
- **R2c.** Do not add a wait parameter to the JSON `POST /v1/invocations`.
  It would fork the one-acknowledgement contract from decision 16 into
  `200`-or-`202` ambiguity. The SDK composes create plus blocking result
  read into one call (section 4.10); the wire stays clean.
- **R2d.** Point the SDK `wait()` at the server-side wait and keep the
  polling loop only as a fallback for old servers. Implement the server wait
  on the existing LISTEN/NOTIFY plus poll-fallback machinery that
  cancellation already uses.

### 4.3 Streaming as a first-class delivery mode (P0, R3)

**Observation.** The only stream is Session-scoped SSE
(`GET /v1/sessions/{id}/transcript/stream`). Measured against "stream one
turn", it has five structural problems:

1. A first attach with no cursor replays the retained Session from the
   beginning. A fresh turn on a long session pays for the whole history.
2. Live deltas emitted between admission and stream attachment are lost.
   Committed content arrives later via snapshots, but the live window at the
   start of the turn, exactly when a UI wants first tokens, is a gap.
3. The event vocabulary is transcript-shaped (`transcript.snapshot`,
   `generation.delta`, `stream.resync`, `stream.end`). Consumers must run a
   reducer over snapshot pages and lifecycle changes to answer "what did this
   turn say". The SDK ships a `Reducer` class because the wire model demands
   one.
4. The stream ends only when the Session has no nonterminal Invocation, so
   the SDK must detect Session-terminal, then re-check its target Invocation,
   then loop (`sdk/typescript/src/stream.ts:90-97`).
5. That end condition conflicts with the queueing proposal (R5b): with a
   queued turn behind the active one, the Session never goes idle when the
   requested turn settles, so a per-turn stream built on the Session stream
   would follow later turns instead of ending.

The original version of this review called streaming a presentation gap and
said no new wire endpoint was needed. That was too optimistic. The industry
default is also against us here: OpenAI Responses and Gemini Interactions
both stream from their central create operation. CMA attaches a stream as a
second call. Only nvoken requires stream consumers to adopt a
whole-conversation replay model to watch one turn.

**Recommendations.**

- **R3a.** Stream on create via content negotiation:
  `POST /v1/invocations` with `Accept: text/event-stream`. The admission
  transaction commits exactly as today, then the handler returns an SSE
  stream whose first frame is `invocation.accepted` carrying the same payload
  as the JSON `202`. The handler observes background execution; it never
  owns it. A dropped connection changes nothing: the work continues, and the
  client retries with the same idempotency key or reattaches via R3b. The
  JSON representation and its `202` contract are untouched; this is a second
  representation of the same operation and must be recorded as a deliberate
  amendment in the decision log.
- **R3b (first-token race).** Commit-then-subscribe is not sufficient on its
  own. Committed work is immediately claimable: embedded mode signals workers
  before the handler returns (`internal/services/runtime.go:608`), the Cloud
  Tasks dispatch commits inside the admission transaction, and the live bus
  is deliberately lossy (`internal/ports/streaming.go`). A worker can emit
  first deltas before any handler subscribes. The Streaming PRD must pick one
  closing mechanism:

  - a bounded per-Invocation ephemeral replay buffer that late subscribers
    drain on attach; or
  - subscription established before the work becomes claimable, with
    crash-safe recovery if the subscribing handler dies.

  The buffer is the recommended choice. It preserves fully independent
  execution, and it also improves attaching to an already-running turn, for
  example after an idempotent replay of the create.
- **R3c (HTTP boundary).** Specify the negotiation exactly:

  - JSON representation: `202` with `application/json`, unchanged.
  - Streaming representation: `200` with `text/event-stream`; the first
    frame is `invocation.accepted`.
  - Admission failures before stream commitment: ordinary JSON error
    responses with their existing status codes.
  - Failures after the first frame: terminal SSE frames, never a late status
    change.
  - `Accept: */*` or absent: JSON. Unsupported media types: `406`.

- **R3d.** Add an Invocation-scoped stream for reconnect and background
  attach: `GET /v1/invocations/{invocation_id}/stream`, resumable, ending
  when that Invocation settles.
- **R3e (cursor law).** Durable frames need a complete ordering contract,
  not an informal projection of the Session cursor. Today's durable position
  covers message sequence plus lifecycle revision only; pending ToolCall
  projections are not independently ordered. Two viable designs:

  - **Snapshot frames (recommended).** One durable frame type,
    `invocation.snapshot`, carrying newly committed messages, current state,
    and pending calls, with the R2a watermark as its SSE ID. Ephemeral
    `output_text.delta` frames provide liveness between snapshots. This
    mirrors the proven Session-stream design (decision 24) at Invocation
    scope, reuses the watermark from R2, and needs no new ordered record.
  - **Individual semantic frames** (`message.completed`,
    `tool_call.pending`, lifecycle frames). Ergonomically richer, but then
    the PRD must define the durable source record behind every frame type,
    the total order across message, tool, lifecycle, and terminal frames,
    whether an SSE ID means "this frame" or "all durable state through this
    frame", and how reconnect avoids skipping or duplicating sibling frames
    that share a position. That requires a new ordered per-Invocation event
    position. Do not ship this without one.

  Either way: cursors are bound to Account and Invocation, the terminal
  frame carries the full `InvocationResult` (R1a), and deltas remain honest,
  id-less, best-effort previews. The delta boundary from decision 24 is
  unchanged.
- **R3f.** Keep the Session transcript stream, repositioned as what it is
  good at: full-conversation replay, multi-turn UIs, and observability. It
  stops being the primitive every `invokeStream()` call must wrap.
- **R3g (later).** Short-lived signed stream tokens usable as a query
  parameter, so browsers can attach directly without proxying (bearer-only
  SSE blocks `EventSource` today). Until then, document the proxy pattern.

### 4.4 The request-shape floor (P1, R4)

**Observation.** The minimal valid request requires `agent_ref`,
`idempotency_key`, an `input.content` array of typed text blocks, and a `spec`
with `instructions` and `model`. Mistral's floor is `model` plus a bare string.
CMA's floor, after setup, is one `user.message` event.

**Analysis.** Each required field is individually defensible. Together they
raise the floor above both competitors for a first request. The envelope and
the instructions requirement can ease without losing anything. The
idempotency key is different: it is the flagship guarantee, and the least
experienced curl user is exactly the developer most likely to retry after an
ambiguous connection failure and accidentally create duplicate work. Making
the key optional at the wire would remove the product's core protection from
the people who need it most. The friction belongs in the SDK's power to
absorb, not in the wire's power to waive.

**Recommendations.**

- **R4a.** Accept `input` as a plain string, normalized server-side to one
  text block before fingerprinting. Keep the block array for multi-block and
  future multimodal input. The fingerprint machinery already versions cleanly
  (v1 through v6 exist), so normalization lands as the next fingerprint
  version with the string form canonicalized identically to its equivalent
  block form. Mirror the shorthand in every SDK, and let the TypeScript SDK
  accept blocks too (today it accepts only a string, which narrows the wire:
  `sdk/typescript/src/client.ts:85`).
- **R4b.** Make `spec.instructions` optional. Mistral's `instructions` is
  optional. An instruction-free turn is a legitimate generation-only use.
- **R4c.** Keep `idempotency_key` required at the wire. Ease it in the SDKs:
  the parameter becomes optional there, the SDK generates a key when omitted,
  reuses it across its own transport retries, and exposes it on the handle
  and result so the caller can persist it. Document the limitation honestly:
  a generated key protects retries within the live SDK call only. If the
  process dies before the handle is returned, the ambiguous request cannot be
  recovered, because the key died with it. Production callers must derive and
  persist a host-owned key before invoking; quickstarts may use the generated
  path.

### 4.5 Busy Sessions, queueing, and steering (P1, R5)

**Observation.** One nonterminal Invocation per Session; a distinct concurrent
request gets `409 session_invocation_active` (decision 16). There is no
steering, no interrupt-and-redirect, and no input buffering. CMA buffers
events sent before the stream attaches, supports mid-run steering, and
supports interrupt. The docs anticipate "future steering" via narrow commands
(`api.md` section 3) but nothing is designed.

**Analysis.** The one-nonterminal invariant is the right consistency
foundation. But "user sent a second message while the agent was working" is
the single most common real-world event in a chat product, and today every
host must build the queue, the retry loop, and the cancel-and-resend logic
themselves. This is the largest interaction-model gap against CMA.

Three constraints shape the design:

1. **Queued input cannot join the transcript at admission.** Admission
   commits the caller-input message into `SessionMessage` in the admission
   transaction (`internal/services/runtime.go:495`). Provider context reads
   every eligible Session message, and user messages are always eligible
   (`ListSessionMessagesForGeneration`,
   `internal/adapters/postgres/queries/runtime.sql`). Recovery validation
   requires the current Invocation's transcript to be contiguous through the
   end of the Session (`internal/services/generation.go:625`). A queued
   turn's input committed at admission would therefore leak into the active
   turn's model context and break recovery validation. Queueing requires a
   staging design: persist queued input outside `SessionMessage`, then
   atomically promote it into the canonical transcript, deleting the staged
   payload in the same transaction, when the queued Invocation reaches the
   head. One durable content representation at all times. This is an
   explicit amendment to decision 16's input-committed-at-admission rule.
2. **No casual new public status.** The status enum is closed and
   decision 16 declares the six states exact. A new status breaks every
   generated SDK enum and exhaustive consumer.
3. **Streaming ordering.** Queueing changes when a Session goes idle, which
   the Session-scoped stream uses as its end condition. Queueing therefore
   lands after Invocation-scoped streaming (R3d).

**Recommendations.**

- **R5a (now, docs only).** Publish the recommended host pattern for the busy
  case: read the `409` details (`invocation_id` and `status` are already
  returned), then either wait via R2 and resubmit, or cancel and resubmit.
  A short recipe in the guides removes most of the sting.
- **R5b (design after R3, with the staging prerequisite).** Opt-in admission
  queueing: `queue: true` on create admits the Invocation behind the active
  one, with its input staged as above. No new public status: a queued-behind
  turn is honestly `queued`, with sibling fields (`queue_position`,
  `blocked_by_invocation_id`) making the position observable. The invariant
  becomes "at most one claimable Invocation per Session". Each queued turn
  keeps its own durable identity, idempotency, and cancellation; cancelling
  a staged turn discards its staged input without touching the transcript.
  This leapfrogs CMA: they buffer events without identity; we would queue
  durable, individually cancellable turns. If design work concludes a
  distinct status is genuinely clearer, adding one is a deliberate pre-freeze
  breaking revision, decided in the decision log, not a side effect.
- **R5c (roadmap).** Steering as the already-anticipated narrow command:
  append caller input to a running Invocation at a checkpoint boundary, and an
  interrupt command that cancels the in-flight provider call but settles the
  turn cleanly at the last checkpoint rather than terminal-failing it. Both
  compose with the checkpoint spine that already exists, which is exactly the
  infrastructure CMA does not expose. Steering input inherits the same
  staging-then-promotion discipline as queued input.

### 4.6 Client tools and human-in-the-loop deadlines (P1, R6)

**Observation.** Pending client ToolCalls expose `deadline_at`, but the spec
offers no way to set it: `ClientToolSpec` carries only name, description,
mode, and schema. The deadline is an installation default. Meanwhile
wall-clock time keeps accruing while an Invocation is parked
(`architecture.md`, "Wall-clock time continues while parked"), and the
wall-clock default in the examples is 1800 seconds.

**Analysis.** The parked `waiting` state is the best HITL primitive in this
market: durable, no compute, idempotent result submission. But its flagship
use case is approvals that take hours or days, and today a day-long approval
collides with a 30-minute wall clock and an uncontrollable tool deadline. The
primitive is ahead of the competition; the knobs around it make it unusable
for its best scenario.

**Recommendations.**

- **R6a.** Add an optional per-tool `deadline_seconds` (or a spec-level
  `tool_deadline_seconds`) with an installation maximum.
- **R6b.** Add an explicit `waiting_timeout_seconds` limit rather than a
  pause-the-wall-clock boolean. Pausing wall-clock time ripples into
  reapers, queued turns, credential lease expiry, and cleanup, and it makes
  an Invocation unbounded. A separate waiting budget keeps every Invocation
  bounded and is easy to reason about: wall clock limits the turn, the
  waiting budget limits the park. Document the interplay between wall clock,
  active execution, tool deadlines, and the waiting budget in one place;
  today it takes three documents to derive it.

### 4.7 Inline specs and the resend cost (P1, R7)

**Observation.** The spec arrives inline on every call. Spec references are
designed (`vision.md` section 6, architecture open question 1) but rejected at
admission today. Both competitors converged on stored, versioned agents, and
both then added escape hatches back toward per-call config (Mistral's
`model`-only conversations, CMA's `agent_with_overrides`).

**Analysis.** The competitor convergence is evidence that hosts want
server-side versioning for ops and rollout. nvoken's answer is "your Git is
the registry, pin by digest", which is the better answer for multi-tenant
apps, but only once digest caching actually ships. Until then, every turn
resends full instructions plus up to 32 tool schemas, all of it inside the
1 MiB cap and the fingerprint. The README's central claim ("nothing to
register, sync, or migrate") deserves the mechanism that makes it cheap.

**Recommendation.**

- **R7.** Prioritize spec-by-digest: send `spec_digest` alone; the server
  admits from cache or returns a typed `spec_unknown` error telling the
  caller to resend inline (which primes the cache). This keeps statelessness,
  cuts steady-state payload to near zero, and gives hosts a pinnable,
  auditable version identity that beats agent-version integers. It also
  future-proofs the fingerprint: a digest is already canonical.

### 4.8 Naming polish (P2, R8)

Do this once, before freeze; all are breaking renames later.

- **`output` → `structured_output`.** Covered as R1b; listed here because it
  belongs in the same coordinated sweep.
- **`budgets` → `limits`.** The object mixes timeouts, token ceilings, cost
  caps, and iteration caps. Only cost is a budget in the ordinary sense.
  `limits` covers all five members honestly, and `waiting_timeout_seconds`
  (R6b) joins them naturally. Field names inside are good.
- **`session_key` vs `agent_ref`.** Both are host-owned names with
  resolve-or-create semantics, but one is a `_key` and the other a `_ref`.
  Pick one suffix for "host-owned name that resolves or creates" and use it
  for both (`session_ref` is the smaller change). `tenant_ref` already
  matches.
- **`/v1/model-pricing-capabilities`.** The name is a mouthful and the shape
  is a capability lookup. Fold it under the existing capabilities surface,
  for example `GET /v1/capabilities/model-pricing?provider=&model=`, before
  more capability probes accrete as top-level resources.
- **`Invocation` (keep).** It is infrastructure-flavored next to "session" and
  "conversation", and the docs themselves define it as "one durable agent
  turn". `turn` was considered and the vocabulary is reserved (decision 9).
  Renaming the resource now would churn every ID prefix, SDK, and document
  for a marginal warmth gain, and `invoke()` as the SDK verb already reads
  well. Keep it, and keep leading with the verb in docs.
- **`spec` (keep).** Distinct from the Agent identity anchor on purpose.
  Renaming it `agent` would collide with the anchor noun.
- **`waiting` (keep).** Fine once R6b documents what it waits for.

### 4.9 Smaller contract observations (P2/P3)

- The `202` acknowledgement could include `wall_clock_deadline_at`. The host
  then knows its polling budget without a second read. Additive, cheap.
- `ModelProvider` is a closed enum (`anthropic`, `openai`). Every new
  provider is a contract rev even though `/v1/capabilities` exists to
  advertise installed adapters. Prefer an open string validated against
  capabilities at admission, keeping the enum only in docs examples.
- `CreateInvocationRequest` uses a top-level `not: required` to express
  session selector exclusivity. Many generators ignore `not`, so generated
  clients will not surface the constraint. Server validation covers it;
  consider also stating it in both field descriptions (done) and accepting
  that generated types allow the invalid combination.
- List endpoints have no time-range filters (`created_after` /
  `created_before`) and Sessions cannot be filtered by activity. Fine for
  launch; worth adding before operators lean on lists for triage.
- Cursors are forward-only where CMA offers bidirectional paging. Acceptable
  for machine consumers; note it in the API doc so it reads as a choice.
- `Retry-After` is defined only on `429`. Add it to `503` responses, where
  it is at least as useful.
- Terminal replays returning `202` (decision 16) is mildly surprising but
  correct: one operation, one acknowledgement contract. Keep, and keep the
  `deduplicated` flag prominent in docs.
- `provider_credentials` as a one-element array is good runway design:
  the shape already fits future multi-provider specs without a break.

### 4.10 SDK observations and the developer surface

The SDK should present the three delivery modes as three intents over the
same result model:

| Intent | SDK experience | Wire behavior |
| --- | --- | --- |
| Get the final response | `await client.invoke(request)` | JSON POST (`202`), then watermark-aware blocking result read (R2) |
| Stream one response | `for await (const event of client.invokeStream(request))` | POST with `Accept: text/event-stream` (R3a); reconnect via the Invocation stream (R3d) |
| Explicit background work | `client.invocations.create(request)` | Existing durable `202` acknowledgement, unchanged |

Specific observations:

- `InvokeRequest.input` accepts only a string, so multi-block wire capability
  is unreachable from TypeScript. Widen to `string | TextBlock[]` (pairs with
  R4a).
- `streamSession` must wait for a Session-terminal end and then re-check its
  target Invocation (`stream.ts:90-97`). R3d removes that workaround; the
  Session stream helper remains for multi-turn UIs.
- The public quickstart runs a pricing preflight before the first invoke.
  It is good cost hygiene but noise on the first run. Move it to the
  cost-controls section and let hello world be invoke, wait, print.
- `handle.wait()` should adopt the server-side watermark wait when R2 lands.
- `handle.text()` inherits the full-transcript paging problem;
  `InvocationResult.output_text` supersedes it for the common case.
- `client.resume(invocationId)` and the `raw()` escape hatch are good and
  worth keeping stable.

### 4.11 Flexibility runway (P3)

Not launch work. Recorded so the contract avoids foreclosing them.

- **Branching.** Mistral's `restart` forks a conversation from any entry.
  The equivalent here is a Session created as a fork of an existing Session
  at a message-sequence cut. The immutable-transcript model makes this
  natural. Valuable for regeneration UX, evals, and A/B of specs.
- **Indexed request metadata.** Already an open question (architecture
  question 4). Hosts will want to find Sessions and Invocations by host IDs
  beyond `tenant_ref` and `session_key`. This is the main missing lookup
  affordance for embedded apps.
- **Model routing and fallback.** The vision promises per-step routing across
  providers. `ModelSelection` as an object leaves room to add `fallbacks` or
  step routing additively. Keep it an object; never flatten to a string.
- **Direct end-user access.** Deferred (vision section 7). The stream token
  in R3g is the first concrete piece.
- **Agent memory.** Endpoints are sketched but the data model is open. No
  change requested; keep it out of the golden path as today.

## 5. What not to change

Explicitly affirmed, because a usability review that only lists complaints
invites overcorrection:

- Commit-before-acknowledge admission in one atomic transaction, with the
  handler owning no model execution. Streaming on create observes execution;
  it never owns it. The foundation stays.
- The canonical transcript as the single durable content representation
  (decision 14). Every fix above composes reads over it rather than copying,
  and queued-input staging deletes its staged payload on promotion so no
  second representation survives.
- Required wire-level idempotency with versioned canonical fingerprints. Six
  fingerprint versions in the log show the mechanism absorbs change safely.
- The tenancy model: `tenant_ref` partitioning with credential constraint,
  no Tenant resource, no per-tenant provisioning.
- No stored agent configuration. Both competitors' escape hatches back toward
  per-call config validate the bet. R7 completes it rather than reversing it.
- The three tool modes and the durable `waiting` state. Ahead of the market;
  R6 adds knobs, not redesign.
- The honest delta boundary from decision 24: durable frames carry cursors,
  previews are ephemeral and id-less. The new streams keep it exactly.
- Strict rejection of unknown fields, typed failure codes, `request_id` on
  every error, and `additionalProperties: false` discipline.
- No managed sandbox, no scheduler, no loops. Host-owned, as designed.

## 6. Recommended sequence

Sliced to PRD-sized steps, contract-first where a contract is touched. The
first three steps are one arc: define the result, order it, deliver it.

1. **Result model PRD:** slim `InvocationResult`, the composed result read,
   and the `output` → `structured_output` rename (R1). Read-side only, no
   fingerprint impact, immediate SDK payoff. The messages filter (R1c) rides
   along only if raw-transcript consumers justify it.
2. **Watermark and wait PRD:** the Invocation watermark, `wait_seconds` +
   `after_watermark` on the result read, SDK adoption (R2).
3. **Streaming PRD:** SSE on create with the bounded replay buffer and the
   specified HTTP boundary, the Invocation-scoped stream with the snapshot
   cursor law, terminal frames carrying `InvocationResult`, SDK `invoke()` /
   `invokeStream()` facades, and the decision-log amendment for the second
   representation (R3).
4. **Request floor PRD:** string input shorthand and optional instructions
   (one new fingerprint version); SDK-generated idempotency keys with the
   documented limitation (R4).
5. **HITL knobs PRD:** per-tool deadlines, `waiting_timeout_seconds` (R6).
6. **Busy-session recipe:** R5a docs now. **Queueing PRD:** R5b design, after
   the streaming PRD lands, with queued-input staging and transcript
   promotion as an explicit prerequisite and decision-16 amendment.
7. **Spec-by-digest PRD:** R7.
8. **Pre-freeze naming sweep:** R8 items in one coordinated change.

Steps 1 through 3 turn the golden path into: one streaming call, or two
durable JSON calls, to a printed answer. At that point the scoreboard in
section 2 has no "behind both" rows, the streamed path matches Mistral's call
count with guarantees Mistral cannot make, and the durable rows remain ours
alone.
