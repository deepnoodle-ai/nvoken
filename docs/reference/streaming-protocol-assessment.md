# Streaming protocol assessment

**Status:** Assessment, and spent. This document argues for changes and decides
nothing. Everything in it has since graduated or been superseded:
[design 003](../design/003-streaming-protocol-target.md) specified the
remediation target, and
[design 004](../design/004-protocol-end-state.md) replaced that with the end
state and landed it in three steps. Where any of the three disagree, 004 wins.
This document is kept as the record of the argument, and it names frames and
routes that no longer exist.
The companion [reference](streaming-protocol.md) describes the protocol as it
is; this document says what we would change, in what order, and why. When a
recommendation here is accepted, it graduates into `docs/design/` and then into
implementation.
**Inputs:** the [reference](streaming-protocol.md), verified against the
runtime; the protocol research in [`docs/research/`](../research/README.md);
and market research that is not public.
**Date:** 2026-08-13

## The verdict

The architecture is sound, and the sound part is the part that matters
commercially. The protocol models the stream as a durable log addressed by ID
and cursor. Every adjacent protocol we assessed models it as a transient pipe
with one consumer, and each of them said in writing that durable delivery is
the application's problem. MCP deleted SSE resumability. AG-UI never had a
cursor. A2A tells clients not to trust the stream. The property this protocol
provides is the one none of them do, and it is the distinguishing property of
the product. See [the research summary](../research/README.md).

Nothing here recommends changing that shape. The recommendations are about a
different gap: the protocol currently works because we control all four
clients. It becomes a leading protocol when a stranger can implement it
correctly from the contract alone, and when it can evolve without breaking the
clients that already exist. Today neither is true.

The evidence that it is not implementable from the contract is internal. Our
own four SDKs disagree with each other (rough edges C1, C2, C3) despite
sharing a conformance fixture. The console had to re-derive turn completion
from content inspection (I4) and re-learn an undocumented invariant (I2). The
guarantee every SDK's read loop depends on exists only in the runtime's
behavior (P1). Each of these is a place where a third-party implementer would
guess, and some of their guesses would be wrong.

The evidence that it cannot evolve is structural. Every event schema is closed
to extension (S2) and both reason enums are closed without a
forward-compatibility note (S3), so adding one field or one enum value is a
breaking change for strict generated clients, and the population of those
clients grows every day the contract stays public.

## What is load-bearing and should not change

Four decisions worth defending against any future proposal to revisit them:

1. **The durable and ephemeral frame split.** One rule, the presence of an SSE
   `id`, decides what a client may store. It is clean, teachable, and the
   honest version of a distinction most streaming protocols leave implicit.
2. **Admission separated from streaming.** The idempotency key exists before
   the model starts thinking, so a lost response is retried safely and a
   reconnect can never create a second turn. The inline `POST` exists as a
   convenience, and the SDKs are right to avoid it.
3. **The preview identity tuple and the attempt discard rule.** Clients
   recover from retries and reconnects without the server keeping per-client
   state. This is also what makes any number of readers able to attach to one
   live turn, at any position, live or later. Every stack we compared would
   need a fan-out bus plus a separate history buffer to approximate that, and
   still would not have replay.
4. **Previews lossy by design.** Durable frames never depend on the live bus.
   This is the same scope decision MCP, AG-UI, and A2A made for their whole
   protocol; we made it only for the part that is genuinely ephemeral and kept
   the durable half they gave up. Do not accept proposals for durable delta
   delivery.

## Recommendations, ranked

### 1. Make evolution possible before changing anything else on the wire

Fix S2 (open the event schemas and state an ignore-unknown-fields norm), S3
(forward-compatibility notes on both reason enums), and S1 (declare the
discriminators the unions already satisfy). This is one contract change in
the service, and it is the gate for everything else: after it, adding a
field, a frame type, a union member, or an enum value is non-breaking. Before
it, every improvement below is a breaking change. C5 largely falls out of S1
through regeneration.

### 2. Then improve additively, not by breaking

Once recommendation 1 lands, the highest-value improvements are all additive:

- **N5.** Add `thinking` and `redacted_thinking` to the content block union.
  The contract currently declares a closed union the runtime does not honor,
  which is worse than an open one because it invites clients to trust the
  generated type.
- **I5.** Enrich `InvocationChange` with `stop_reason`, `pending_tool_calls`,
  and `credit_block`. This removes the console's 15 second poll while parked,
  and removes the two streams' unexplained disagreement about how much
  lifecycle detail a client deserves.
- **I1.** Carry the eventual message identity on delta frames. This turns the
  preview handoff from a blind replace into an in-place transition, and it is
  one of the three gaps the AG-UI mapping exercise independently confirmed.
  Requires the runtime to know message identity at delta time, so it is the
  most expensive item in this group.
- **I7.** Put tool-call status transitions on the wire, so failed, slow, and
  parked tools stop looking identical between call and result. Needs a small
  design first: as `invocation_changes` entries or as a new durable frame.
- **P8.** Add a `slow_consumer` end reason so a dropped slow client can tell
  backpressure from a network blip.

### 3. Move the load-bearing guarantees into the contract

Pure prose changes to the contract, no wire change, cheap, and most of the
distance to "implementable by strangers": state P1 (result re-emission on
reconnect to a settled turn) and with it bless `stream.end` terminal as an
equally valid termination signal (P2); document P4 (`invocation.accepted` is
inline-only and SDKs synthesize it); enumerate the Invocation stream's durable
frames (S5); state the two unstated `invocation.update` behaviors (S6); state
the one-iteration-one-assistant-message invariant (I2); gloss "turn" as
Invocation once (N1); document the cursor's several names (N2) and the counter
bases (N6); document cursor interchangeability between streams as supported
(P6).

### 4. Fix P3 in the service

The sharpest correctness item on the list. `phase` is derived at read time and
delivered once on a strictly forward stream, so whether a Session-stream
client holds the right value is a race against the 1 second poll, permanently.
The honest fix is to deliver a correction when the turn settles, for example
as an `invocation_changes` entry, which becomes additive after recommendation
1. The interim fix is to document `phase` as provisional on the Session
stream. The drain-timing discussion should also cover I4, the lifecycle
trailing the transcript, since both are settlement-timing semantics.

### 5. Decide what the Session stream is

Today it is a drain, not a subscription (I10): `stream.end` terminal means no
turn is running, and a turn started later by anyone else is invisible until
the client polls. Related, a client's own `createInvocation` has no
protocol-level representation until a frame confirms it (I12), so every UI
synthesizes a local claim. These two together mean the steady state of any
console watching a shared Session is polling.

This matters more than it looks. The strongest product demonstrations of this
protocol are exactly the ones these edges undercut: multiple clients attached
to one live turn, and a supervisor surface following a whole Session. A
demonstration whose steady state is a 15 second poll shows a weaker property
than the protocol actually has. The fix is a real product decision, either a
follow mode on the Session stream or a documented stance that Session
subscription is the host application's job, and it should be made
deliberately rather than inherited from the current implementation.

### 6. Take at most one deliberate break, and bundle it

Some fixes are renames or restructurings that cannot be additive: N3
(`arguments` for tool input), N4 (a shared delta payload field), N10
(`messages` everywhere), N15 (an explicit scope field instead of a nullable
`invocation_id`), N14 (one path to the Invocation ID), S4 (response unions
split by audience). None is individually worth a break. If a break ever
happens, it should be one versioned event that bundles all of them, after
recommendations 1 through 4 have shipped and alongside something clients
want. It is a legitimate outcome for this break to never happen.

## The strategic move

Treat the protocol as a product surface. Name it, version it, and publish the
specification once recommendations 1 and 3 land, with the conformance suite as
the compliance kit. The reference is already most of that specification; what
it lacks is exactly the contract hardening above.

Two reasons to do this rather than keep the protocol as an internal behavior:

- **Nobody adjacent can follow.** A conformance suite for durability semantics
  requires durability semantics. The protocols that occupy the neighboring
  layers each declined that property in writing, so a published, testable spec
  here is not a feature race we can lose to a bigger vendor's next release.
- **The layer is being approached from below.** MCP's tasks extension now
  specifies durable handles for long-running work, without a transcript, a
  session, or a resumable stream. The research
  ([001-mcp](../research/001-mcp.md)) records both readings: encroachment and
  opportunity. Either way, the response is the same: publish the fuller
  semantics while the layer is still unoccupied, rather than after someone
  else's minimal version becomes the default vocabulary.

This also reframes the conformance gaps. T1 and T2 stop being test debt and
become holes in a compliance kit, which is a different priority.

## Rough edge disposition

Every rough edge in the reference, assigned to an action wave. Waves 1 and 2
can start now and are independent. Wave 3 gates wave 4. Waves 5 through 7 can
land anytime. The last group is deliberate inaction, recorded so it reads as
a decision rather than an omission.

### Wave 1: fix the defects (this repository, one PR with fixtures)

| ID | Action |
| --- | --- |
| C1 | Add `incomplete` to Go's terminal set. |
| C2 | Terminate Python's Session stream on `stream.end` terminal. |
| C6 | Reconnect TypeScript's Session stream on transport errors. |
| C10 | Guard the null `invocation_id` in Python's preview discard. |
| T1 | Extend `reducer.json` to all seven frame types and all terminal statuses, `incomplete` included. |
| T2 | Add an Invocation-stream loop fixture: termination, cursor resume, result re-emission. |
| T3 | Set the SSE `id` on the conformance server's `invocation.accepted`. |

The fixtures land in the same change as the fixes, so the gap that let C1
survive closes with it.

### Wave 2: start in the service now (longest lead time)

| ID | Action |
| --- | --- |
| P3 | Decide the `phase` correction mechanism. Interim: document `phase` as provisional on the Session stream. |
| I4 | Cover in the same settlement-timing discussion. |
| R3 | Count or log dropped deltas so a misbehaving provider adapter is visible. |

### Wave 3: the evolution bundle (one contract change, gates wave 4)

| ID | Action |
| --- | --- |
| S1 | Declare discriminators on both stream event unions. |
| S2 | Open the event schemas; state the ignore-unknown-fields norm. |
| S3 | Forward-compatibility notes on both reason enums. |
| C5 | Falls out of S1 through regeneration; typed payloads in all four SDKs. |

### Wave 4: additive improvements (after wave 3, each independent)

| ID | Action |
| --- | --- |
| N5 | Add `thinking` and `redacted_thinking` to the content block union. Most urgent: the contract is wrong today. |
| I5 | Enrich `InvocationChange` with `stop_reason`, `pending_tool_calls`, `credit_block`. |
| I1 | Message identity on delta frames. Largest runtime cost in this wave. |
| I7 | Tool-call status transitions on the wire. Needs a small design first. |
| P8 | `slow_consumer` end reason. |

### Wave 5: contract prose hardening (anytime, no wire change)

P1, P2, P4, S5, S6, S7, I2, N1, N2, N6, P6. Described under recommendation 3.

### Wave 6: names only (cheap, bundle with any contract touch)

| ID | Action |
| --- | --- |
| N8 | Name both reason enums; give both `x-enum-varnames`. |
| N12 | `Browser*` over `Client*` for generated symbols. |
| N13 | `TranscriptUpdateEvent`, matching its siblings. |

### Wave 7: SDK hygiene batch (this repository)

| ID | Action |
| --- | --- |
| C4 | Trust the frame's projection; drop the per-event `refresh()` in Go, Python, Rust. |
| C7 | Backoff inside the reconnect loops. |
| C8 | Send `Accept: text/event-stream` on the GET streams everywhere. |
| C9 | Raise or make configurable Go's 2 MiB frame cap. |
| I11 | One casing policy for content blocks in TypeScript, after N5 lands. |
| C3 | Rust Session stream: implement on demand, or document as unsupported. |

### Decide deliberately (no default action)

| ID | Decision |
| --- | --- |
| I10, I12 | What the Session stream is. Recommendation 5. |
| I9 | Whether compactions ever stream. Revisit if a client needs live compaction awareness. |
| I13 | Media retrieval is an API surface question, not a protocol one. Track separately. |
| N3, N4, N10, N14, N15, S4 | The bundled break of recommendation 6, if ever. |

### Accept, documented (deliberate inaction)

| ID | Why |
| --- | --- |
| P5 | Two envelopes for one message is annoying, not harmful. Dedup by sequence works. |
| P7 | Server-side gap detection is the design; previews are not worth client-side sequencing. |
| N7, N9, N11, N16, N17 | Renames are breaking and the confusion is survivable. The reference now explains each. |
| I3 | A change log that clients fold is fine; revisit only if a snapshot need appears. |
| I6, I8 | Inherent to a transcript with tool calls. The reference documents the required client behavior. |
| R1, R2 | Real costs, not measured problems. Optimize when load says so. |

## Related

- [The streaming protocol](streaming-protocol.md), the reference this assesses.
- [Research on agent protocols and standards](../research/README.md), the
  external context: what MCP, AG-UI, A2UI, MCP Apps, and A2A do instead, and
  why the durable log is the property to keep.
- [Session options conflict scope](../design/002-session-options-conflict-scope.md),
  the house style for arguing a change, for when a recommendation here
  graduates into a decision.
- A pain-point inventory and a competitive landscape, neither public, are the
  market context this assessment factors in.
