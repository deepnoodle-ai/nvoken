# The streaming protocol becomes implementable from the contract alone

> **This document is not the target, despite its file name.** It is a
> remediation list against the protocol we happen to have. It never asked what
> the protocol should be, and in two places it made existing redundancy
> permanent by writing it into the contract. The direction we are actually
> aiming for, and what this document got wrong, is in
> [Design direction](DIRECTION.md). Read that first.

**Status:** Implemented, with two documented deviations. Areas 1 through 9 and
area 10's contract boundary landed on 2026-08-13. Area 7's durable half was
not implemented and area 5's revision bump is scoped to settlement; both are
explained under Implementation notes. The four open decisions were resolved
with Curtis on 2026-08-13 and are recorded under Decisions.
**Author:** Claude Fable 5 with Curtis Myzie
**Date:** 2026-08-13
**Workflow:** Remediation specification, written as though it were a
target-state one. The
[streaming protocol reference](../reference/streaming-protocol.md) describes
what is; the [assessment](../reference/streaming-protocol-assessment.md)
argues what to change and in what order; this document specifies the state
those particular changes converge on. That is narrower than a target, because
the set of changes was drawn from the rough-edge list rather than from what
the protocol should be. See [Design direction](DIRECTION.md).
**Applies to:** `nvoken-cloud` for the contract and runtime behavior; this
repository for regenerated types, SDK changes, and conformance fixtures.

## Context

The reference records 61 rough edges. The assessment triages them: some are
defects to fix, some are accepted costs, and about twenty change what the
contract says or what the wire carries. Those twenty are this document.

Two properties define the target, and every change below serves one of them:

1. **Implementable by strangers.** A third party holding only
   `openapi/nvoken.yaml` writes a correct client on the first attempt. No
   guarantee lives only in the runtime's behavior, and no invariant has to be
   rediscovered by reading our console.
2. **Evolvable without breakage.** Adding a field, a frame type, a union
   member, or an enum value never breaks an existing strict client. The
   protocol can absorb its own future.

Today neither holds. Our four SDKs disagree with each other despite a shared
fixture, the guarantee every read loop depends on is unwritten, and every
event schema is closed to extension.

Both properties are necessary and neither is sufficient. A protocol can be
implementable and evolvable while still saying the same thing three ways, and
this one does. The missing third property is that it says each thing once.
Nothing below serves it, and the browser and machine response unions violate
property 1 outright and survive this document untouched. See
[Design direction](DIRECTION.md).

## Scope

**In scope:** the compatibility policy, the stated guarantees, Session-stream
lifecycle parity, preview identity, tool-call visibility, flow control, the
thinking story, and generated-name cleanups. These are the assessment's
waves 2 through 6. Two boundary stances joined on 2026-08-13: the transport
binding (area 9) and bulk content (area 10). A forward stance on streaming
at the tool boundary (area 11) is recorded without a wave.

**Out of scope, deliberately:**

- **Wave 1 and wave 7 SDK work** (C1, C2, C6, C10, T1, T2, T3, C4, C7, C8,
  C9, C3, I11). Defect fixes and hygiene go straight to implementation; they
  need no design.
- **The bundled break** (N3, N4, N10, N14, N15, S4 restructuring). Wire
  renames that cannot be additive. The target state deliberately keeps
  today's names on the wire. If a versioned break ever happens, it gets its
  own design.
- **What the Session stream is** (I10, I12). Whether the Session stream
  becomes a true subscription is a product decision with its own tradeoffs
  and deserves its own design document when taken up.
- **Everything the assessment marks accept** (P5, P7, I3, I6, I8, I9, N7,
  N9, N11, N16, N17, R1, R2). I13 left this list on 2026-08-13; area 10 now
  gives it a direction.

## The target, by area

Each area states current behavior, target behavior, and the concrete contract
change. Rough edge IDs refer to the reference.

### 1. Compatibility policy (S1, S2, S3, N8)

**Current.** The stream event unions are bare `oneOf`s with no discriminator.
All eight event schemas carry `additionalProperties: false`. Both reason
enums are closed, unnamed, and carry no forward-compatibility guidance, and
only one has `x-enum-varnames`.

**Target.**

- `InvocationStreamEvent` and `TranscriptStreamEvent`, and their `Client*`
  counterparts, declare `discriminator` on `type` with a full mapping, as
  `SessionContentBlock` already does.
- The event schemas drop `additionalProperties: false`. The contract states
  the norm once, at the stream section level: **stream events may gain fields
  over time; clients must ignore fields they do not recognize.**
- The reason enums become named schemas, `StreamEndReason` and
  `StreamResyncReason`, both with `x-enum-varnames`, both carrying the same
  guidance `InvocationStopReason` already carries: new values may be added;
  clients must handle values they do not recognize.
- The contract defines the safe behavior for unknown values: an unknown
  resync reason is treated as `live_delivery_gap` (discard previews); an
  unknown end reason is treated as `rotate` (reconnect with the last durable
  cursor). The second is safe precisely because of the P1 guarantee in area
  2: reconnecting to a settled turn always re-yields the terminal result, so
  a client that wrongly reconnects converges instead of hanging.

This area gates every other wire change in this document. After it, all of
them are additive. C5 (three untyped SDKs) is retired by regeneration, not by
hand-written code.

### 2. Guarantees the contract states (P1, P2, P4, S5, S6, S7, I2, N1, N2, N6, P6)

**Current.** These behaviors are real, verified against the runtime, and
documented only in the reference.

**Target.** The contract carries them. The commitments, in the sentences the
contract should make:

- **P1.** Reconnecting to a settled Invocation always yields
  `invocation.result` followed by `stream.end` reason `terminal`, regardless
  of cursor position. This is a guarantee, not an observation.
- **P2.** Both `invocation.result` and `stream.end` reason `terminal` are
  valid termination signals; a client may exit on either.
- **P4.** `invocation.accepted` is emitted only by the inline `POST` path.
  The `GET` stream never emits it. SDKs that admit separately synthesize an
  equivalent locally; the contract says so, so nobody reimplements the SDKs
  from the contract and wonders why the first event never arrives.
- **S5.** The Invocation stream's durable frames are enumerated:
  `invocation.accepted`, `invocation.update`, `invocation.result`. Everything
  else is ephemeral.
- **S6.** An `invocation.update` never carries a terminal status, and its
  Invocation projection is a live re-read at write time, not a snapshot at
  the drained cursor.
- **I2.** One model iteration produces exactly one persisted assistant
  message. This is the invariant that lets a client group previews by
  `(invocation_id, attempt, iteration)` and get rows shaped like the messages
  that replace them.
- **N1.** One gloss, where streaming is introduced: a "turn" is an
  Invocation; the endpoint prose and the schema names are two registers for
  one noun.
- **N2.** One passage naming the resume position's four spellings: SSE `id`,
  payload `resume_cursor`, query `cursor`, header `Last-Event-ID`, and the
  precedence rule when both request forms are present.
- **N6.** `attempt`, `iteration`, `revision`, and `sequence` are 1-based;
  `content_index` is 0-based. Stated where the preview identity tuple is
  defined.
- **P6.** Cursors are Session-scoped on both streams and interchangeable
  between them. Documented as supported, since the SDKs may already rely on
  it implicitly.
- **S7.** `PendingHostToolCall.input` is described as an arbitrary JSON
  object whose shape is the tool's declared input schema, so the four
  generators' four renderings at least share a stated meaning.

### 3. Session-stream lifecycle parity (I5, P3, I4)

**Current.** `InvocationChange` is a strictly weaker projection than
`Invocation`: no `stop_reason`, no `pending_tool_calls`, no `credit_block`.
A Session-stream client sees `waiting` without knowing what for and polls to
recover it. Separately, `phase` on a message is computed at read time and
delivered once on a strictly forward stream, so a client that receives an
assistant message before its turn settles holds `commentary` for what later
becomes `final_answer`, permanently (P3). The terminal status also trails the
settling message by a frame (I4), so consoles re-derive completion from
content.

**Target.**

- `InvocationChange` gains `stop_reason`, `pending_tool_calls`, and
  `credit_block`, all optional, all additive after area 1. The two streams
  stop disagreeing about how much lifecycle detail a client deserves. The
  console's 15 second `getSession` poll while parked is deleted.
- **P3, resolved: no correction mechanism.** The contract scopes the claim
  instead: `phase` is authoritative on reads and on `invocation.result`, and
  the Session transcript stream does not deliver it reliably. A client on
  that stream derives the answer from facts it already holds: a turn has a
  final answer only when it settled `completed` with stop reason `end_turn`,
  and that answer is the turn's last assistant message. This is the same
  rule the runtime itself applies. See the decisions section for why `phase`
  survives at all.
- **I4:** where the runtime can publish the settling message and the terminal
  change in the same drain page, it should; the in-frame ordering rule
  (messages before changes) already makes that render correctly. Where it
  cannot, the documented lag stands.

### 4. Preview identity (I1)

**Current.** A preview and the message it becomes share no identifier. The
handoff is a blind replace: the preview row vanishes and a separately keyed
message row appears. The console papers over the seam by grouping previews to
match the shape of the message it predicts.

**Target.** `output_text.delta` and `thinking.delta` gain `message_id`: the
identifier the durable message will have when it lands. A client keys its
preview row by `message_id` and the handoff becomes an in-place update of a
row that already has its permanent identity. The I2 invariant stops being
something clients infer and becomes visible on the wire: deltas sharing a
`message_id` build one message.

The field is optional on arrival (area 1 makes that safe), so the runtime can
ship it when ready and old clients ignore it. Restarts and reconnects need no
new machinery: a runtime recovery raises `attempt`, the attempt rule already
discards earlier previews, and the retried iteration allocates a fresh ID.
The ID only needs to be stable within one attempt. This is still the most
expensive item in this document on the runtime side.

### 5. Tool-call visibility (I7)

**Current.** `ToolCallStatus` exists on the ToolCall resource and never
reaches either stream. Between a `tool_use` block and its `tool_result`,
failed, slow, and parked-on-a-host tools are indistinguishable. Spinners can
only end when a result block lands.

**Target.** The `Invocation` projection and `InvocationChange` gain
`tool_calls`: a summary of this turn's tool calls as id, status, and
timestamp, with a status transition bumping `revision`. Both streams receive
it through frames they already carry, and it replays correctly by revision
after a reconnect. That last property is why it rides the durable channel
rather than an ephemeral frame: a tool failure that happened while a client
was disconnected must still be visible when it returns.

### 6. Flow control (P8)

**Current.** A consumer that exceeds the 10 second write timeout is dropped
silently; it sees an ordinary disconnect and cannot distinguish backpressure
from a network blip.

**Target.** `stream.end` gains reason `slow_consumer`, sent best-effort
before the close. Additive after area 1. A client that sees it can shed load
or widen its buffer instead of reconnecting into the same failure.

### 7. The thinking story (N5 and the browser gap)

**Current.** `SessionContentBlock` declares a closed discriminator that omits
`thinking` and `redacted_thinking`. The runtime stores both; the console
renders both; a contract-trusting client cannot see either. Separately, the
runtime drops `thinking.delta` for client-token streams unconditionally.

**Target.** `ThinkingBlock` and `RedactedThinkingBlock` join the union and
its discriminator mapping, with field shapes taken from what the runtime
persists, additive for readers after area 1. And thinking flows to both
audiences the same way: client-token streams receive `thinking.delta` when
previews are on, and browser payloads carry the durable thinking blocks. The
unconditional drop was a gap, not a policy. What an end user sees is the
application's decision, made in the application. If a host later needs
server-side withholding, that is a client-token option to design then, not a
default to impose now.

### 8. Generated-name cleanups (N12, N13)

Names only, no wire change, bundled with whichever contract change touches
those schemas first: `Browser*` replaces `Client*` as the generated prefix,
matching the runtime's own vocabulary, and `TranscriptUpdate` becomes
`TranscriptUpdateEvent`, matching its siblings.

### 9. The transport stance

**Current.** Both streams are Server-Sent Events, and the contract describes
them in SSE terms. Nothing states whether the protocol is SSE or merely
carried by it.

**Target.** The contract states the layering: the protocol is the frames and
their durability rules; SSE is its default and, today, only binding. The
SSE-specific surface is three mechanics with obvious equivalents in any
framed transport: the `id:` line, the `retry:` opener, and comment
keepalives.

The stance: **no WebSocket binding now, and frame semantics stay
transport-independent so that one remains possible.** SSE fits what this
protocol is. The flow is one-directional, since everything a client sends
(admission, tool results, cancellation) is an ordinary HTTP request; SSE
survives plain HTTP infrastructure that mishandles upgrades; and
reconnect-with-cursor is native to it. A WebSocket would add a bidirectional
channel we do not use, and it invites clients to treat the connection as the
source of truth, which is the exact mistake this protocol exists to prevent.
The trigger for revisiting: a real client class that cannot consume streamed
HTTP responses, or a genuine need for client-to-server messages inside the
stream. A binding added then must carry identical frames, with the cursor in
an explicit subscribe message, and the durable log stays authoritative.

### 10. Bulk content stays off the stream (I13, promoted)

**Current.** Images and documents in a transcript travel as descriptor
blocks: media type, size, `sha256:` digest. Bytes are never inlined, but
nothing exposes the stored object either, so a client rebuilding a
conversation draws a chip where the picture was. The assessment filed this
as I13, an API question to track separately. It is promoted here because the
answer shapes the protocol's boundary.

**Provider reality, checked 2026-08-13.** Media-bearing tool results are not
hypothetical. Anthropic's `tool_result` content accepts `text`, `image`,
`document`, and `search_result` blocks, so a tool can return a PDF to the
model directly. OpenAI's Responses API accepts an array of image or file
objects as a function call output. MCP tool results carry text, image,
audio, resource links, and embedded resources, which means media can arrive
at nvoken from any MCP server today. xAI's Grok is the outlier: string-only
tool results. Two consequences. First, the bytes must flow on the provider
path regardless of what the stream carries; nvoken's `tool-results` endpoint
already passes a content block array through to the model unchanged, but its
limits (1 MiB body, 256 KiB per result) make that a thumbnail-sized path
today, which the retrieval store below could eventually relieve in both
directions. Second, a verification item for `nvoken-cloud`: what the MCP
client does with image content in a tool result today, given the silent-drop
precedent recorded in [004-mcp-apps](../research/004-mcp-apps.md).

**Target.** The boundary becomes a stated rule: **the stream carries
structure and text; bytes travel out of band.** A tool call that produces an
image or a PDF appears on the stream as its descriptor block, immediately
and at text cost, and the bytes become fetchable through a content-addressed
read scoped under something that already carries authorization, for example
`GET /v1/sessions/{session_id}/content/{digest}`: an ordinary HTTP response
with ordinary caching. Inlining was considered and rejected. Base64 bloats a
payload by a third, one large PDF would dwarf every frame-size and
write-timeout budget the connection has, and slow consumers would be dropped
on exactly the turns with the richest output. The open design question for
`nvoken-cloud` is the authorization scope of the read, not whether it
exists.

### 11. Streaming at the tool boundary (stance only, not scheduled)

Two gaps named on 2026-08-13, recorded as a stance so the eventual design
starts from a position rather than a blank page. Neither is scheduled, and
both become additive once area 1 lands.

**The model's side: tool arguments stream like text.** A large tool input, a
file to write, a big JSON argument, is generated token by token, and
providers stream it (Anthropic's `input_json_delta`, OpenAI's argument
deltas). Today nvoken shows nothing until the durable message lands with the
complete `tool_use` block, which is dead air exactly when the model is doing
its most visible work. The stance: tool arguments are the third kind of
model preview, beside `output_text.delta` and `thinking.delta`. Working
name `tool_use.delta`: ephemeral, carrying the preview identity tuple plus
`tool_call_id`, the tool `name` on every frame (denormalized, so a lost
frame cannot orphan a fragment), and an appendable JSON fragment. Every
existing preview rule applies unchanged: accumulate by tuple, discard on
attempt increase, on resync, when the durable message lands, and at
terminal status. The reducer grows a third accumulator, not a new
mechanism. Browser audiences receive it under decision 4's principle: the
application decides what its users see.

**The tool's side: progress is a replaceable snapshot, not appends.** While
a tool executes, useful texture exists: a fraction done, a status line, a
log tail. Today the wire carries nothing between call and result, and area
5's status transitions are only the durable skeleton. The stance: tool
progress is ephemeral, and unlike text previews it uses replace semantics.
Working name `tool_call.progress`: carrying `invocation_id`, `attempt`,
`tool_call_id`, and a snapshot (status line, optional fraction, optional
output tail) where each frame wholly replaces the previous one.
Replace-latest makes lossy delivery harmless by construction: a dropped
frame needs no resync because the next frame carries the entire current
state, and the live bus may coalesce or drop frames freely with no
correctness cost. This is the lesson A2UI's replace-at-an-address rule
teaches, applied to the one place our append-based preview design does not
fit. The durable record stays exactly what area 5 makes it: status
transitions and the final `tool_result`. Progress is never stored and never
replayed.

**Ingestion is deliberately unspecified.** Progress could arrive from MCP
progress notifications, from host tools posting to a progress endpoint, or
from the runtime's own builtin tools. Each is its own design with its own
authorization questions. The stance binds only the client-facing shape, so
whichever ingestion path comes first inherits it.

One coherence note: with area 4 (message identity on deltas), area 5 (tool
status), and this area, all three gaps the AG-UI mapping exercise surfaced
have answers. `TextMessageStart` gets its identity, `ToolCallArgs` gets its
deltas, and the silence between call and result gets both a durable
skeleton and live texture.

## Decisions

The four questions this document originally left open, resolved with Curtis
on 2026-08-13. Where the assessment recommended differently, this section
wins.

1. **`phase` keeps its meaning, loses its false promise, and gets no
   correction mechanism.** Deleting it entirely was considered. The facts
   argue for something smaller. "Last assistant message" is not a safe
   substitute on its own: the published guides warn against exactly that,
   because interrupted, incomplete, and cancelled turns end with a message
   that was never an answer. But the correct rule is fully derivable from
   data every client already holds: a final answer exists only when the turn
   settled `completed` with stop reason `end_turn`, and then it is the last
   assistant message. So instead of building settlement corrections, which
   is what the assessment recommended, the contract states that rule, keeps
   `phase` where it is already correct (reads and `invocation.result`), and
   stops implying the Session stream delivers it. Full removal joins the
   bundled-break candidate list rather than happening now, for two reasons:
   the published guides teach rendering by `phase`, so hosts may rely on it,
   and forking freezes `phase` onto copied history whose source turns are
   not in the transcript, the one case where it carries information a client
   cannot derive.
2. **Message IDs are allocated when the model starts writing** and stamped
   on every delta of that iteration (I1). Accepted as recommended. Restart
   and reconnect safety comes free from the attempt rule, as area 4 states.
3. **Tool-call status rides the lifecycle channel** (I7): a `tool_calls`
   summary on the `Invocation` projection and on `InvocationChange`, with
   status transitions bumping `revision`. Accepted as recommended.
4. **Thinking is not nvoken's to hide.** The runtime's unconditional drop of
   `thinking.delta` for client tokens is a gap, not a policy. Thinking
   reaches both audiences the same way, live and durable, and the
   application decides what its users see. This reverses the assessment's
   lean toward filtering both.

## What done looks like

The end state, as acceptance criteria:

- Running the four generators against the contract yields tagged stream event
  unions in all four languages, and no SDK hand-writes a `type` switch.
- The reference's frame documentation contains no behavioral claim the
  contract does not make. P1, S5, S6, and I2 read as citations, not
  discoveries.
- The console needs no `getSession` poll while a turn is parked, identifies
  the final answer from status and stop reason without trusting streamed
  `phase`, renders the preview-to-message handoff as an update to one row,
  and can show a failed tool call as failed before its result message
  arrives.
- A browser client and a host client watching the same turn see the same
  thinking content, live. Durable was ruled out: see Implementation notes.
- An image or PDF a tool produced is renderable: its descriptor arrives on
  the stream at text cost, and its bytes are one authorized HTTP read away.
- `reducer.json` exercises all seven frame types and every terminal status,
  and a loop fixture pins termination, cursor resume, and result re-emission
  (delivered by wave 1, assumed here). Still open: this change added the
  preview-identity case, not the missing frame types, so T1 and T2 stand.
- Adding the next field or enum value to any stream event breaks nothing.
- The rough edges resolved here are deleted from the reference, not
  annotated. The reference stays a description of what is.
- The protocol is one naming-and-versioning decision away from publishable,
  with the conformance suite as its compliance kit.

## Implementation notes

What landed, area by area. Areas 1 through 6, 8, and 9 are complete as
specified.

**Area 7 shipped its live half only.** The durable half is unimplementable as
written, and the reason is a policy decision that predates this document.
Reasoning is deliberately never stored as public transcript content: the
generator routes non-`{tool_use, text, server_tool_use}` blocks to the private
provider-artifact sidecar, the checkpoint refuses private block types in
visible content, and the reasoning-lineage rule has to be able to withhold
those artifacts from a later provider request when the reasoning
configuration changes. Adding `ThinkingBlock` to `SessionContentBlock` would
re-send reasoning the runtime must be free to drop, and would put a block in
the contract that no read can return. The live half did ship: browser callers
now receive `thinking.delta` on the same terms machine callers do, which is
what decision 4 asked for. The contract states the rule instead of promising
the block, and N5 in the reference was corrected rather than deleted.

**Area 5's revision bump is scoped to settlement.** A tool call reaching a
terminal state reserves a lifecycle revision, so its new state is delivered
and replayed. The `pending` to `running` transition does not, because
`StartBuiltinToolCall` and `StartMCPToolCall` deliberately take no Session row
lock, and reserving a revision there would either violate the Session-before-
Invocation lock order or add a lock to the hot path. Settlement is the
transition a client acts on; a call that started and a call that is about to
start look the same to a spinner.

**Area 10 landed its contract boundary, not its read endpoint.** The contract
states that frames carry descriptors and never inline bytes, which is what
bounds frame size. The content-addressed read that would make a transcript
image renderable is not implemented, so I13 stays open.

**One bug was found by the tests written for this work.** The first
implementation of area 5 reserved the lifecycle revision in a second
`UPDATE sessions` arm of `CommitToolResult`. Postgres applies at most one
update per row per statement, so that arm silently matched nothing and no
transition was ever written. Both counters are now reserved in one arm.

## Migration path

The assessment's waves, mapped onto this document's areas. Every area below
landed in one change rather than across the planned waves, which was possible
because area 1 was written first and the rest built on it.

| Area | Wave | Lands in | Status |
| --- | --- | --- | --- |
| 1. Compatibility policy | 3 | `nvoken-cloud` contract | Done |
| 2. Stated guarantees | 5 | `nvoken-cloud` contract prose | Done |
| 3. Lifecycle parity | 4 (wire); P3 lands as prose in 5 | `nvoken-cloud` runtime and contract | Done |
| 4. Preview identity | 4 | `nvoken-cloud` runtime and contract | Done |
| 5. Tool-call visibility | 4 | `nvoken-cloud` runtime and contract | Settlement only |
| 6. Flow control | 4 | `nvoken-cloud` runtime and contract | Done |
| 7. The thinking story | 4 | `nvoken-cloud` contract and runtime | Live half only |
| 8. Name cleanups | 6 | `nvoken-cloud` contract, names only | Done |
| 9. Transport stance | 5 | `nvoken-cloud` contract prose | Done |
| 10. Bulk content boundary | 4 | `nvoken-cloud` contract and runtime | Contract only |
| 11. Tool boundary streaming | stance only, unscheduled | `nvoken-cloud` contract and runtime, later | Unscheduled |

Waves 1 and 7, the SDK work, run independently of all of this in this
repository, and every contract change above reached this repository through
the ordinary sync described in
[SDK and contract development](../guides/sdk-development.md).

## Related

- [Design direction](DIRECTION.md), the standing direction this document
  predates and does not satisfy. It outranks this one.
- [Design 004](004-protocol-end-state.md), the end-state design DIRECTION
  called for. It is the target this document's file name claims to be.
- [The streaming protocol](../reference/streaming-protocol.md), the
  description of the current protocol and the source of every rough edge ID.
- [Streaming protocol assessment](../reference/streaming-protocol-assessment.md),
  the argument and the full 61-item disposition this document specifies the
  accepted core of.
- [Research on agent protocols and standards](../research/README.md), the
  external context for why the durable log model is the part that must not
  change.
- [SDK and contract development](../guides/sdk-development.md), the sync
  workflow every contract change here travels through.
