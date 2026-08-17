# The end-state protocol

**Status:** Standing target, and reached. This is the artifact
[DIRECTION](DIRECTION.md) calls for: the protocol we would design today with no
installed base. Part 1 is what every later change to the streaming protocol and
the contract is measured against. It changes only by a decision recorded here,
never by drift. Part 2 was the path, and all three of its steps landed on
2026-08-14; it is kept as the record of how this was sequenced.
**Author:** Claude Fable 5 with Curtis Myzie
**Date:** 2026-08-13
**Applies to:** `openapi/nvoken.yaml` and the runtime behind it; the SDKs,
conformance fixtures, and reference documentation in this repository.
**Reading order:** [DIRECTION](DIRECTION.md) says why this document exists.
[The streaming protocol](../reference/streaming-protocol.md) describes what
exists today. This document says what should exist, then how to get there.

## Part 1: the protocol, designed today

Nothing in this part is constrained by what is deployed. Every choice
answers one question: if we were starting now, with nothing to stay
compatible with, would we write it this way? Where something looks like it
is stated twice, this part carries the reason in writing, as DIRECTION
requires.

### What survives from today

Most of the current protocol's core passes the test unchanged. Naming it
matters, because Part 2 must not damage any of it.

- **The stream is a durable log. A connection is one read of it.** Durable
  frames carry a cursor, ephemeral frames do not, resume is an explicit
  position, disconnecting cancels nothing, and any number of readers can
  sit anywhere in the log without affecting the turn or each other.
- **Admission is separate from streaming.** `POST /v1/invocations` with a
  JSON body and an idempotency key, then follow by ID. A lost response is
  retried with the same body and key, and once a client holds an
  `invocation_id` no reconnect can create a second turn.
- **Previews are provisional and lossy by design.** They travel a separate
  bus, are never stored, never replayed, and are discarded on attempt
  increase, on resync, when the durable message lands, and at terminal
  status. `attempt` is the recovery discriminator; `revision` orders
  lifecycle changes.
- **Errors are not frames.** A failure before the stream opens is an
  ordinary HTTP error response. A failure after is a closed connection and
  an authoritative read. There is no `error` frame.
- **SSE is the default binding and the frames do not depend on it.** The
  layering stance of design 003 area 9 holds: the protocol is the frames
  and their durability rules, and a future binding must carry identical
  frames.
- **Bytes stay off the stream.** Transcript media travels as descriptor
  blocks, and the bytes are one authorized, content-addressed HTTP read
  away, per design 003 area 10. The authorization scope of that read is
  still the service's open question.
- **The compatibility policy.** Discriminated unions, open schemas,
  tolerant readers, and enums that grow, per design 003 area 1. The end
  state is not a license to close schemas again.

Everything below is what changes.

### One stream

There is one streaming route:

```
GET /v1/sessions/{session_id}/stream
```

Parameters: `cursor` resumes from a durable position, `invocation_id`
filters the stream to one turn, `deltas=false` turns previews off.

The Invocation stream and the Session transcript stream were always one
log. Cursors are Session-scoped on both current routes and a position from
either resumes the other, so the separate Invocation route was a filtered
view shipped as an endpoint with its own vocabulary. The filter becomes a
parameter.

The inline `POST` streaming form is gone. It was the one entry point that
was harmful rather than redundant: it required the deployment front end to
stream a non-200 response without buffering, our own deployment target
buffers it, and all four of our SDKs refused to use it. Admission is the
plain JSON `POST`, and the admission response is the acknowledgment, so
the `invocation.accepted` frame goes with it.

**The stream is a subscription.** A connection to the unfiltered stream
stays open while the Session is idle, keepalives and all, and a turn
started later by anyone appears on it. The server remains free to shed
idle connections, but shedding is a connection event with a name (see
`connection.closing` below), never a statement that the stream is over. Today's
protocol hangs up when no turn is running, and the only production client
compensates with a 15 second poll. A protocol whose steady state is
polling has a stream in name only.

On a filtered stream, the server closes the connection after delivering
the turn's terminal change. A client following the exit rule has already
left.

### One frame vocabulary

Five frame types. One is durable, two are ephemeral content, two are
ephemeral control.

| Frame | Durable | Meaning |
| --- | --- | --- |
| `transcript.update` | yes | Ordered messages and lifecycle changes, with a cursor. |
| `message.delta` | no | Live preview of a message the model is writing. |
| `tool_call.progress` | no | Replaceable snapshot of a running tool call. |
| `stream.resync` | no | Previews were lost. Discard them. |
| `connection.closing` | no | This connection is closing. The stream continues. |

`invocation.accepted`, `invocation.update`, and `invocation.result` do not
exist. They were the same facts as `transcript.update` in a second
vocabulary, plus one composed convenience a resource read already
provides.

**`transcript.update`** carries `cursor`, `messages`, and
`invocation_changes`. Messages append by `sequence` and are never re-sent.
Changes are an append log keyed by `(invocation_id, revision)`; fold to
the highest revision for current state. Within one frame, apply messages
before changes, so a turn is never marked settled before its final message
exists.

A change carries the lifecycle facts a status alone leaves open: `status`,
`stop_reason`, `tool_calls`, and `credit_block`. **A turn is over when a
change for it carries a terminal status.** That is the terminal signal,
the only one. It is durable, so it replays on reconnect like any other
change; no special re-emission machinery is needed, and the current
guarantee that a settled turn re-yields its result becomes an ordinary
property of replay. A client that wants the composed result reads
`GET /v1/invocations/{id}`.

There is no full Invocation projection on the stream and no live re-read
at write time. Changes carry deltas of lifecycle state; reads carry
snapshots. This also deletes a per-page Invocation query the server pays
today for every connected client.

**`message.delta`** is the one preview frame. Fields: `invocation_id`,
`attempt`, `message_id`, `content_index`, `kind`, `delta`, and for tool
arguments `tool_call_id` and `name`, denormalized on every frame so a lost
frame cannot orphan a fragment. `kind` is an open enum: `text`,
`thinking`, `tool_arguments`. The payload field is `delta` for every kind,
so one accumulator handles all three; today's split into
`output_text.delta` carrying `text` and `thinking.delta` carrying
`thinking` forces four SDK reducers to carry parallel fields where one is
always empty.

`message_id` is required. The runtime allocates the message identifier
when the model starts writing (design 003, decision 2), every delta of
that message carries it, and the preview handoff is an update to a row
that already has its permanent identity. `iteration` is gone from the
identity: one model iteration produces exactly one message, so
`message_id` already says which iteration this is. Accumulate by
`(message_id, content_index)`. Discard on attempt increase, on resync,
when the durable message with that ID lands, and on the terminal change.

**`tool_call.progress`** is the second preview frame, with replace
semantics rather than append, per design 003 area 11: `invocation_id`,
`attempt`, `tool_call_id`, and a snapshot where each frame wholly replaces
the previous one. A dropped frame needs no resync because the next frame
carries the entire current state. Never stored, never replayed.

**`stream.resync`** means live delivery lost previews. It carries a
`reason` (open enum, `live_delivery_gap` today) and an optional
`invocation_id`; absent means discard previews for the whole stream.
Optional, not required-and-nullable: an absent field is scope, a null
identifier was scope wearing an identifier's name.

**`connection.closing`** is a connection event and only a connection
event. Its reasons never speak about turns:

- `rotate`: the server is cycling this connection. Reconnect now with your
  cursor.
- `idle`: no turn is running and the server is reclaiming the connection.
  Reconnect when you next need to read; nothing is lost while you are
  away.
- `slow_consumer`: this connection could not keep up and was dropped.
  Reconnect, read faster or buffer more.

Whether a deployment ever sends `idle` is its own choice; the client rule
covers both. A connection that simply drops carries no meaning: reconnect
and resume. `connection.closing` carries no cursor, because a client must
already track its last durable cursor to survive a silent drop, and a
field that duplicates what the client must hold anyway is a second
spelling.

### One schema family

`Invocation`, `Session`, `Message`, `TranscriptUpdate`: each exists once.
There are no `Browser*` projections and no response unions.

Which fields a caller receives is decided by its credential, so the
contract says exactly that: audience-restricted fields (Agent and user
identity, copy origin, host provenance, `credit_block`) are optional,
marked with their audience, and omitted from responses to browser tokens.
A stranger holding only the contract can decode every payload, because
omission needs no discriminator. Today's design fails its own first
property: `InvocationResponse` is a union of two structurally overlapping
arms that no validator can tell apart, which is why
`sdk/scripts/generate.sh` carries a post-generation patch block that
hard-selects the machine arm in TypeScript. In the end state that patch
block does not exist, and neither do the eight wrapper unions it exists to
work around.

### One tool-call collection

The lifecycle projection and its changes carry `tool_calls`: one entry per
call in the turn, with `id`, `name`, `status`, `arguments`, and
`deadline_at` while a host call is pending. `pending_tool_calls` does not
exist; it was `tool_calls` filtered to host mode and pending status,
carried beside it as a second collection.

A tool call still reaches a client on three surfaces, and each earns its
place by carrying a different fact:

1. **Transcript content**: the `tool_use` and `tool_result` blocks, the
   durable record of what happened.
2. **`tool_calls` on the change log**: where each call stands now,
   replayable by revision, which is how a failure that happened while you
   were disconnected is still visible when you return.
3. **`tool_call.progress`**: live texture while a call runs, lossy and
   disposable.

What does not survive is two collections on one surface carrying the same
entries filtered two ways.

### One cursor

The resume position has one name in the protocol: `cursor`. It is the
field on `transcript.update` and the request parameter that resumes a
stream. The SSE binding mirrors the field onto the `id:` line and accepts
`Last-Event-ID` as the parameter's equivalent, because a faithful SSE
binding must; those are the binding's mechanics, not protocol vocabulary,
and a future binding carries the same one name. Today's `resume_cursor`
payload spelling is gone.

### Names

Renames that ride along free, because every frame is reshaped anyway: the
preview payload is `delta` for every kind; tool-call arguments are
`arguments`, never `input`, which currently means the user's message on
one schema and the tool's arguments on another; message lists are
`messages`, never `new_messages`, since a frame only ever carries new
ones. One rule for time: a resource has `created_at`, an event has
`occurred_at`. Naming is the least important part of this document, and it
is in scope only because the collapse makes it free.

### The client algorithm

The whole protocol, from a client's chair:

1. Admit with `POST /v1/invocations`, idempotency key attached. The
   response acknowledges the turn.
2. Open `GET /v1/sessions/{id}/stream`, with `cursor` if resuming and
   `invocation_id` if following one turn.
3. Fold: append messages by `sequence`; append changes and fold by highest
   `revision`; accumulate `message.delta` by `(message_id, content_index)`;
   hold the latest `tool_call.progress` per call. Discard previews on
   attempt increase, on resync, when the durable message lands, on the
   terminal change.
4. Exit when the change you care about carries a terminal status. On any
   connection end, reconnect with your cursor: immediately on `rotate`,
   lazily on `idle`, after widening your buffer on `slow_consumer`,
   immediately on a silent drop.
5. Read the Invocation if you want the composed result.

One loop, one exit condition, one reconnect rule.

### What this removes

| Surface | Today | End state |
| --- | --- | --- |
| Streaming routes | 3 | 1 |
| Frame types | 8 | 5 |
| Durable frame types | 4 | 1 |
| Schema families | 2 | 1 |
| Tool-call collections | 2 | 1 |
| Terminal signals | 2 | 1 |
| Protocol spellings of the cursor | 2 | 1 |
| Preview identity fields | 4 | 3 |

Every guarantee design 003 wrote down survives in spirit: the stream is
still implementable by a stranger from the contract alone and still
evolvable without breakage. What is new is the third property 003 named
and did not serve: the protocol says each thing once.

## Part 2: getting there

This part is the smaller problem, and unlike Part 1 it is allowed to
change as we learn.

### The rule for every step

Each collapse lands as one coordinated change: contract and runtime in the
service, then SDKs, conformance fixtures, console, CLI, and published guides
through the ordinary publish. No step leaves both the old
and new way live beyond the release that carries it. A step that cannot
retire what it replaces is not ready to land.

We are pre-1.0 and the SDKs are at 0.15.0. The installed base will never
be smaller, and the cost of every collapse rises monotonically from here.
The goal is that 1.0 is the end state, not today's protocol plus a
promise.

### The one input, answered

**There are no external users. Curtis is the only consumer of `/v1`.**
Answered on 2026-08-13, closing the one question the repository could not.

So every step below is a coordinated break in a version-bumped 0.x release,
noted in the changelog, with no deprecation window and no overlap. We do not
build `deprecated` markers, `Deprecation` and `Sunset` headers, or SDK
warnings, because there is nobody to signal. The old way is removed in the
same change that adds the new one, and a step that cannot do that is not
ready to land.

This is the cheapest this will ever be. It gets more expensive every week,
and 1.0 is the deadline.

### The steps, cheapest first

Each step is a strict subset of Part 1, so nothing landed early is churned
later.

**Step A, small: one tool-call collection and one cursor spelling.**
`tool_calls` entries gain `name`, `arguments`, and `deadline_at`;
`pending_tool_calls` and `PendingHostToolCall` are removed;
`resume_cursor` becomes `cursor`, and the contract states the cursor's one
name and the SSE binding's two mirrors as binding mechanics. Breaking for
any client reading the removed field or the old spelling.

**Step B, medium: one schema family.** The eight response wrapper unions
and the stream-event union collapse into single schemas with
audience-marked optional fields. The runtime's browser serialization
becomes field omission on one shape instead of a second shape. The
`generate.sh` patch block is deleted, which is the acceptance test:
generation runs clean with no post-processing. Breaking for generated
clients by type name, not by payload, for machine callers; browser payload
shapes are already the subset.

**Step C, large: one stream.** The route consolidates, `invocation_id`
becomes a filter, the inline `POST` form and the three `invocation.*`
frames are removed, `message.delta` replaces the two delta frames with
`message_id` required and `iteration` dropped, `stream.end` loses
`terminal` and gains `idle`, and the unfiltered stream becomes a
subscription. This is the bundled break design 003 anticipated, so the
name-only items on its candidate list ride along here. SDK read loops
change their exit condition to the terminal change, and `reducer.json` is
rewritten once for the new vocabulary.

`tool_call.progress` is additive and unscheduled, exactly as design 003
area 11 left it. It needs no break and can land whenever an ingestion
path exists. Step C landed the model-side half of it under a different
name: tool arguments now stream as `message.delta` frames of kind
`tool_arguments`, carrying their call on every fragment. What remains
unbuilt is progress from a running tool, which needs that ingestion path.

### What each step costs

| Step | Service | SDKs | Fixtures | Console | Guides |
| --- | --- | --- | --- | --- | --- |
| A | Contract and two projections | Regenerate, small reducer edits | Touch | Small | Tool and streaming pages |
| B | Contract and browser serialization | Regenerate, delete patch block | Touch | Type names | Little |
| C | Contract, both stream handlers, subscription lifecycle | All four read loops and reducers | Rewrite `reducer.json`, conformance server | Stream lifecycle, delete idle poll | Rewrite streaming pages |

The order is by cost, but A and B are independent and can land in either
order or together. C depends on neither, but doing it last means its
rewrite of fixtures and guides happens once, against the final shapes.

## Decisions

Choices this document makes that no earlier document made, recorded so a
later reader knows they were deliberate:

1. **The stream is a subscription.** Design 003 deferred what the Session
   stream is; Part 1 answers it. Idle disconnection becomes a named
   connection event, `idle`, that a deployment may send and a client
   reconnects from lazily. The 15 second poll dies.
2. **`iteration` leaves the preview identity.** `message_id` is required
   on every delta and one iteration is one message, so carrying both is
   two spellings of one fact.
3. **The preview frames merge.** Text, thinking, and tool arguments are
   one frame discriminated by `kind` with a shared `delta` field,
   absorbing the model-side stance of design 003 area 11.
4. **`connection.closing` never speaks about turns.** Terminal state is a
   durable change; connection close is a connection event. No reason value
   may ever couple them again. Recorded as `stream.end`; see decision 6.
5. **`phase` stays as design 003 decision 1 scoped it**: authoritative on
   reads and nowhere else, kept because forked history is the one place it
   carries underivable information. It is not restated here because it
   already says one thing once.
6. **`stream.end` is renamed `connection.closing`** (2026-08-17). Decision
   4 held, and the frame never once spoke about a turn. The name did. It
   was read as "the stream is over" often enough that the contract carried
   a correction in four places and the SDKs repeated it in their own doc
   comments, which is the shape of a name that is wrong. The new name
   states the frame's whole content, and most of those corrections are
   deleted rather than reworded. Nothing else moves: the reasons, the
   absent cursor, and the client rule are as decision 4 and the frame
   vocabulary above describe them.

## Related

- [Design direction](DIRECTION.md), the standing direction this document
  exists to satisfy. Part 1 is the artifact its final section calls for.
- [Design 003](003-streaming-protocol-target.md), the remediation work
  this document builds on. Its areas 1, 2, 9, 10, and 11 survive into the
  end state; its blessings of dual terminal signals and four cursor
  spellings do not.
- [The streaming protocol](../reference/streaming-protocol.md), the
  description of today, and the source of the rough-edge IDs cited here.
- [SDK and contract development](../guides/sdk-development.md), the sync
  workflow every step in Part 2 travels through.
