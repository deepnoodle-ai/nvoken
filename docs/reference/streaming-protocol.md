# The streaming protocol

**Status:** Descriptive. This is how the protocol behaves today, not a
specification we have committed to freezing. Rough edges are listed at the end
and we expect to resolve some of them, which will change parts of this
document.
**Verified against:** `nvoken-cloud@d8090df`
(`internal/adapters/httpapi/stream.go`), the contract snapshot pinned in
[`openapi/SOURCE.json`](../../openapi/SOURCE.json), and the four SDK
implementations in this repository.
**Date:** 2026-08-13
**Authority:** The runtime is the authority. Where this document and
`nvoken-cloud` disagree, the runtime is right and this document is stale.

## Who this is for

People implementing against the wire: SDK authors in this repository, and
anyone writing a client nvoken does not ship. It covers the frames, their
durability, and the client-side algorithm the SDKs implement.

If you are consuming nvoken through an SDK, read
[Streaming & recovery](https://nvoken.com/docs/guides/streaming-and-recovery)
instead. It is shorter and answers the questions callers actually have.

## The three entry points

| Route | Scope | Ends on |
| --- | --- | --- |
| `GET /v1/invocations/{invocation_id}/stream` | one turn | `invocation.result` |
| `GET /v1/sessions/{session_id}/transcript/stream` | every turn in a Session | `stream.end` with reason `terminal` |
| `POST /v1/invocations` with `Accept: text/event-stream` | one turn, inline | `invocation.result` |

The two `GET` routes are the supported pattern. Admit with a plain JSON `POST`,
then follow by ID. That separation is what makes retries safe: if the admission
response is lost you retry the exact body and idempotency key, and once you
hold an `invocation_id` no reconnect can create a second turn.

The inline `POST` form is a convenience. It depends on your deployment front
end streaming a non-`200` response without buffering, and some managed
platforms do not. Cloud Run buffers it until the turn settles, which strands
every host-tool turn at `waiting` with nobody watching. All four SDKs admit
separately for this reason. See the note in
[`sdk/typescript/src/stream.ts`](../../sdk/typescript/src/stream.ts) above
`admitInvocation`.

## Streams, connections, and scope

A stream is not a connection. The stream is a durable, ordered log addressed
by cursor; a connection is one HTTP response reading it for a while. Losing
that distinction is the most common way to misread this protocol, so the
vocabulary here is deliberate.

**Scope.** The Invocation stream is bounded to exactly one turn. It ends
when that turn settles and never carries another. The Session transcript
stream is the multi-turn surface: one connection follows every Invocation in
the Session, interleaved, which is why its reducer keys previews by
`invocation_id`. A client that wants to follow a conversation opens one
Session stream, not one Invocation stream per turn. Cursors are
Session-scoped on both routes, so a position taken from either stream
resumes either stream.

**Connections are disposable; the stream is not.** A connection ends by
rotation (`stream.end` reason `rotate`, at the 55 minute lifetime or on
server shutdown), by termination (`stream.end` reason `terminal`: the turn
settled, or on the Session stream, no turn is running), by a slow-consumer
drop, or by ordinary network failure. None of these says anything about the
turn. Work continues, the log keeps growing, and the next connection opened
with the last durable cursor sees everything the previous one did not. The
timing machinery in [Server behavior and timings](#server-behavior-and-timings),
the keepalive, the lifetime, the write timeout, all belongs to the
connection, never to the stream.

**Any number of connections may read one stream.** Readers are independent.
They can sit at different cursors, join mid-turn, or replay a turn that
settled last week, and none of them affects the turn or each other.
Disconnecting never cancels anything. A support console can watch a
customer's live turn while the customer's own client streams it, with no
fan-out machinery in the host application; the cost is the per-connection
queries noted in R1 and R2.

**The lifecycle of one connection**, in order: it opens with or without a
cursor; the server writes the `retry: 1000` opener; saved state replays,
from the cursor if one was given, otherwise from the Invocation's beginning
or the Session's origin, or, for an already settled Invocation, collapsing
to `invocation.result`; the connection goes live, interleaving durable
frames with previews; and it ends with `stream.end` or a silent drop. From
the client's side the whole algorithm is one loop: connect, fold frames,
remember the last durable `id`, reconnect with it until a terminal signal.

### The transport

Both streams speak Server-Sent Events today, and SSE is the only binding.
Almost nothing in the protocol depends on it: the semantics live in the
frames, durable frames carry a cursor, ephemeral frames do not, and resume
is an explicit position handed back at reconnect. The SSE-specific surface
is exactly three mechanics, the `id:` line, the `retry:` opener, and comment
keepalives, each of which has an obvious equivalent in any framed transport.
A WebSocket binding is possible in principle and does not exist; see
[design 003](../design/003-streaming-protocol-target.md) for the stance on
whether it should.

### What the stream carries

Frames are JSON, and their content is structure and text. Images and
documents in a transcript travel as descriptor blocks, carrying media type,
size, and `sha256:` digest, never inline bytes. That keeps frame sizes
bounded by text alone, which is why C9 is only about large replay pages, and
keeps the stream cheap to fan out to many readers. It also means a client
cannot render described media from the stream by itself; retrieving the
bytes is outside the protocol today, recorded as I13 and given a direction
in [design 003](../design/003-streaming-protocol-target.md).

## Durable and ephemeral frames

This is the organizing idea. Every frame is one or the other, and the
difference decides what you may store and what you must be prepared to lose.

**Durable frames carry an SSE `id`.** That ID is a resume cursor and the only
value a client needs to persist. Reconnect with it and the server replays
saved state from that position, then continues live.

**Ephemeral frames carry no `id`.** They are live previews and control
signals. They are not saved anywhere, they are never replayed, and a client
that treats them as state will show text that no transcript will ever confirm.

The server omits the `id:` line entirely rather than sending an empty one
(`writeSSEEvent`, `stream.go:900`).

### Invocation stream

| Frame | `id` | Meaning |
| --- | --- | --- |
| `invocation.accepted` | yes | Admission acknowledged. Inline `POST` only. |
| `invocation.update` | yes | Lifecycle snapshot plus this turn's new messages. |
| `invocation.result` | yes | Composed terminal result. Last durable frame. |
| `output_text.delta` | no | Live assistant text preview. |
| `thinking.delta` | no | Live reasoning preview. |
| `stream.resync` | no | Previews were lost. Discard them. |
| `stream.end` | no | Connection is closing. Read `reason`. |

### Session transcript stream

| Frame | `id` | Meaning |
| --- | --- | --- |
| `transcript.update` | yes | Ordered messages and Invocation changes. |
| `output_text.delta` | no | Live assistant text preview. |
| `thinking.delta` | no | Live reasoning preview. |
| `stream.resync` | no | Previews were lost. Discard them. |
| `stream.end` | no | Connection is closing. Read `reason`. |

There is no `invocation.update` on the Session stream. Lifecycle arrives as
`invocation_changes` inside `transcript.update`.

### Resuming

Send the last durable `id` as either the `cursor` query parameter or the
`Last-Event-ID` header. The query parameter wins when both are present
(`requestedStreamOptions`, `stream.go:27`). `Last-Event-ID` may appear at most
once and must not be blank.

Disconnecting never cancels a turn. It keeps running and you can reconnect or
read it later.

## Two frames that are not events

Both streams open with a bare `retry: 1000` control frame: no `event:`, no
`data:`. It sets the client's reconnect delay to 1000 ms.

Every 15 seconds an idle stream emits an SSE comment line, `: keepalive`.

Neither carries a payload. A parser that assumes every dispatched frame has
JSON `data` will fail on the very first frame of every stream. All four SDKs
special-case this, each with a comment saying so, because each one hit it
against the real runtime.

## Frame reference

### `invocation.accepted`

Written only by the inline `POST` path
(`streamAdmittedInvocation`, `stream.go:96`). Its SSE `id` is the transcript
cursor at admission, so a client that sees only this frame and then drops
resumes at the Invocation's own rows rather than at the Session origin.

The `GET` stream never emits it. SDKs that admit separately synthesize an
equivalent event locally so callers see the same first event either way.

### `invocation.update`

Carries a full `Invocation` projection plus `new_messages`, the messages this
turn appended since the last drained cursor.

Two behaviors worth knowing, neither of which is stated in the contract:

- **An update frame never carries a terminal status.** The server skips the
  frame entirely when the Invocation has settled (`stream.go:226`). Terminal
  state arrives as `invocation.result` and nowhere else on this stream.
- **The Invocation is re-read at write time**, so the projection is the current
  state, not the state at the drained cursor. It is a live snapshot with a
  durable cursor attached, not a point-in-time revision.

The projection includes `pending_tool_calls` when the turn is `waiting`. You do
not need a second request to learn what the turn is blocked on.

### `invocation.result`

The composed terminal result: the Invocation, its messages, and its output.

**The server re-emits this on every reconnect to a settled turn**, regardless
of your cursor (`tailInvocation` arms `settlementObserved` from the status at
open, then calls `emitTerminal`). Reconnecting to a turn that finished last
week yields `invocation.result` followed by `stream.end` terminal, and no
update frames at all, because updates are suppressed when the turn was already
terminal when the stream opened.

All four SDKs depend on this. Each exits its read loop only on
`invocation.result`, so if the server stopped re-emitting it they would
reconnect forever. The dependency is real and currently satisfied. It is not
written down in the contract, which is the actual problem. See rough edge P1.

### `transcript.update`

Ordered `messages` and `invocation_changes` for the whole Session, plus a
`resume_cursor` in the payload that matches the SSE `id`.

Empty pages are not sent. The server advances its internal watermark but only
writes a frame when there is at least one message or change
(`stream.go:601`), so every `transcript.update` you receive is non-empty.

Apply messages before lifecycle changes from the same frame. Otherwise a UI can
show a turn as complete before its final message exists.

### `output_text.delta` and `thinking.delta`

Live previews. Identity is the tuple:

```
(invocation_id, attempt, iteration, content_index)
```

All four fields matter. `content_index` separates concurrent content blocks
within one model iteration, and ignoring it interleaves two blocks into
nonsense. `attempt` is the retry discriminator described below.

Both frames also carry `message_id` when the runtime has already reserved it:
the identifier the saved assistant message will land under. Key a rendered
preview by it and the handoff below becomes an update to a row that already has
its permanent identity. It is optional, and stable only within one attempt.

The server validates every delta before forwarding and silently drops
malformed ones (`validGenerationDeltaEvent`, `stream.go:799`): `attempt` at
least 1, `iteration` at least 1, `content_index` at least 0, a non-zero
`emitted_at`, and exactly one of `text` or `thinking` non-empty.

On the Invocation stream, deltas are filtered to that Invocation. On the
Session stream, deltas from any Invocation in the Session pass through, which
is why the Session reducer keys previews by `invocation_id`.

### `stream.resync`

The live delivery path could not prove continuity, so previews were lost. The
only reason value today is `live_delivery_gap`. Treat one you do not recognize
the same way.

Discard accumulated preview text and wait for durable frames. The server drains
the transcript immediately after sending a resync, so the replacement is
already on its way.

`invocation_id` is set on the Invocation stream and null on the Session stream.
A null means discard previews for the whole Session.

You never receive this frame when `deltas=false`, because the server only
subscribes to the live bus when previews are enabled and resyncs originate from
that subscription.

### `stream.end`

Three reasons:

- **`rotate`** means the server is cycling the connection. Reconnect with your
  last durable cursor. It fires on server shutdown or when a connection reaches
  its 55 minute lifetime.
- **`terminal`** means nothing more is coming. On the Invocation stream the
  turn has settled. On the Session stream no turn is running.
- **`slow_consumer`** means this connection could not keep up with what was
  being written to it and was dropped. Reconnect, and read faster or buffer
  more. The frame is best effort: a consumer too slow to accept the frame
  before it may be too slow to accept this one.

Treat a reason you do not recognize as `rotate`. Reconnecting is safe even when
the turn is over, because a settled turn re-yields its result.

`resume_cursor` in the payload is the position to reconnect from. As with
resync, `invocation_id` is set on the Invocation stream and null on the Session
stream.

A connection that simply drops carries no meaning. Reconnect and resume.

## Previews: accumulate and discard

The rules, in the order a client should apply them:

1. **Accumulate** by the four-field identity tuple. Append `text` to that
   entry's output and `thinking` to its thinking.
2. **Discard on attempt increase.** A higher `attempt` for an Invocation means
   execution was claimed again after recovery. Everything provisional from
   earlier attempts is dead. Ignore deltas from a lower attempt than the
   highest one seen.
3. **Discard on resync.** Scoped to one Invocation, or the whole Session when
   `invocation_id` is null.
4. **Discard when the canonical message lands.** A durable assistant message
   supersedes the preview that was building it.
5. **Discard on terminal status**, and refuse later previews for that
   Invocation. A settled turn cannot produce more provisional output.

`attempt` is the durable anchor. It appears on both delta types and on the
`Invocation` itself, so a client can discard stale output even across a
reconnect where it never saw the resync frame.

Never store preview text as a transcript message, and never use it to decide
whether a turn succeeded.

## The reducer

Both Session-stream consumers and any UI that renders a live transcript need
the same fold: durable messages by sequence, Invocation changes by
`(invocation_id, revision)`, previews by the identity tuple, plus the resume
cursor. This repository implements it four times:

- [`sdk/go/stream.go`](../../sdk/go/stream.go) `Reducer`
- [`sdk/typescript/src/stream.ts`](../../sdk/typescript/src/stream.ts) `Reducer`
- [`sdk/python/src/nvoken/stream.py`](../../sdk/python/src/nvoken/stream.py) `Reducer`
- [`sdk/rust/src/stream.rs`](../../sdk/rust/src/stream.rs) `Reducer`

Behavior is pinned by
[`sdk/conformance/fixtures/reducer.json`](../../sdk/conformance/fixtures/reducer.json),
which all four test suites read. It covers preview accumulation, discard on
attempt increase, discard on resync, replacement by the canonical assistant
message, and the `message_id` a preview carries and holds once published, plus
one snapshot case asserting that an ephemeral frame's `id` is never adopted as
a resume cursor.

A behavior represented in more than one SDK belongs in that fixture rather than
in four unrelated tests. See
[SDK and contract development](../guides/sdk-development.md).

## Building a transcript incrementally

A stream is a sequence of appends. A transcript is not. Getting from one to the
other is where most of this protocol's friction lives, and the reducer only
covers the first third of it.

The nvoken console is a worked example. It lives in `nvoken-website` under
`app/components/console/chat/`, runs the TypeScript SDK in the browser against
the Session stream, and every claim in this section is visible there.

### Five kinds of state, five update rules

| State | Keyed by | Arrives on | Rule |
| --- | --- | --- | --- |
| Message | `sequence` | `transcript.update.messages`, `invocation.update.new_messages` | Append. The Session stream never re-sends one. |
| Lifecycle change | `(invocation_id, revision)` | `invocation_changes` on the same frames | Append to a log, then fold to the highest revision to get current state. |
| Preview | `(invocation_id, attempt, iteration, content_index)` | `output_text.delta`, `thinking.delta` | Concatenate, then discard wholesale on any of the five triggers above. |
| Tool call | `tool_use.id` | opened by one message, closed by a later one; status on `invocation_changes.tool_calls` | Retroactively update a message you already rendered. |
| Compaction | not streamed at all | `GET /v1/sessions/{id}/compactions` | Fetch separately, interleave by `covers_through`. |

Only the first is a plain append. That is the whole problem.

### The preview handoff can be a merge

Previews are keyed by `(invocation_id, attempt, iteration, content_index)`; the
message is keyed by `id` and ordered by `sequence`. Delta frames carry
`message_id`, the identifier that message will land under, which is the link
between the two. Key your preview row by it and the handoff is an update to a
row that already has its permanent identity rather than one row disappearing
and another taking its place.

The field is optional, so a client still needs the fallback: when it is absent,
the preview disappears when the durable message lands and a separate row
appears in its place.

Either way, group previews by `(invocation_id, attempt, iteration)`,
deliberately dropping `content_index` from the key, because one iteration is
one persisted assistant message and grouping that way makes the preview row the
same shape as the message. Without that, a turn that thinks before it answers
renders two rows that collapse into one, with two avatars and two name labels
disappearing at the swap. The contract states that invariant now.

### Tool results reach backwards

A `tool_use` block arrives in one assistant message. Its `tool_result` arrives
in a different, later message. So a client cannot render messages
independently: it needs a transcript-wide index from tool-use id to call, and
when a result lands it mutates a row it drew earlier.

One consequence falls out of that: a message whose blocks all fold into earlier
calls has nothing left to draw and must be dropped rather than rendered empty.

Progress between the call and its result does reach the stream. Lifecycle
changes carry `tool_calls`, a status and timestamp per call in the turn, and a
settling call reserves a revision of its own so the change is delivered and
replayed rather than inferred from a result block appearing. A tool that
failed, one that is slow, and one waiting on a host are distinguishable.

### Messages and the changes that settle them arrive together

One transcript page carries messages first and then the lifecycle changes that
settled them, under one shared budget, so the assistant message that ends a
turn and the terminal status usually reach you in the same frame. Apply
messages before lifecycle changes within a frame.

They can still split across frames when a page fills on messages alone: a turn
that produced more messages than your `limit` spends the whole budget on them
and the changes follow. So a client testing "is this Invocation still active"
can still say yes for a beat after the answer is on screen, and the fix is to
wait for the change rather than to re-derive completion from block inspection.

### What the Session stream will not tell you

`InvocationChange` carries the facts a status alone leaves open: `stop_reason`
for a turn that stopped short, `pending_tool_calls` for one parked on work you
own, `credit_block` for one waiting on an account, and `tool_calls` for where
each call in the turn got to. A Session-stream client can recover from the
change stream alone. Browser callers get the same set minus `credit_block`,
which is host-only.

Compactions never stream, though. They change what the model is shown from a
point backwards, and are only readable after the turn that produced them, so
the console fetches them at connect and again at every settlement.

And the stream itself ends when the Session goes idle. `stream.end` reason
`terminal` means no turn is running, not that you are still subscribed. A turn
started afterwards by anyone else is invisible until you look. The console
falls back to a 15 second `getSession` poll plus a window focus listener, and
reopens the stream when an active Invocation appears.

### Reconciling three sources of truth

A live client ends up holding three claims about what the Session is doing, and
each is stale in its own direction:

1. **The stream.** Authoritative, and first to know about any Invocation it has
   reported on. Blind to turns that started while it was closed.
2. **A Session read.** A point-in-time snapshot. Covers turns the stream has
   not seen, and goes stale immediately.
3. **A local claim.** After your own `createInvocation` returns, you know a turn
   exists before any stream frame proves it. Without holding that claim, a
   composer drops back to idle for a beat and invites the user to send twice.

The console resolves these in one place, letting the stream win for any
Invocation it has an opinion on and falling back to the other two only for
Invocations it has never seen. That reconciliation is unavoidable today and the
protocol offers nothing to help with it.

## Tool calls

**Tool calls have no frames of their own.** There is no `tool_call.started` or
`tool_call.completed`. This surprises people, so it is worth stating plainly.

A tool call reaches you four ways at once:

1. **As transcript content.** The assistant message carries a `tool_use` block;
   the answering message carries a `tool_result` block naming it by
   `tool_use_id`. This is the durable record and it arrives in
   `transcript.update` messages or `invocation.update` `new_messages`.
2. **As a status.** The Invocation moves to `waiting`, which other APIs call
   `requires_action`. Nothing is executing. The turn is parked.
3. **As pending work.** The `Invocation` projection and any lifecycle change
   that parked the turn carry `pending_tool_calls`, each with `id`, `name`,
   `input`, and `deadline_at`.
4. **As progress.** Both projections carry `tool_calls`, one `id`, `status`,
   and `updated_at` per call in the turn. A call reaching a terminal state
   reserves a lifecycle revision, so the change is delivered and replayed like
   any other.

To advance the turn, `POST /v1/invocations/{id}/tool-results` with each
`tool_call_id` returned verbatim. The turn returns to `queued` and picks up
where it left off. A partial batch leaves it waiting.

`waiting` is not terminal, so the stream stays open across it. A turn can also
return to `queued` on its own after runtime recovery. `attempt` tells the two
apart, and `revision` on each change orders them.

Builtin, MCP, and callback tools resolve without host involvement and never
park the turn this way. Only host tools and undelivered callbacks do. See
[Tools](https://nvoken.com/docs/guides/tools) for the mode taxonomy.

## Browser streams

Browser clients authenticate with a client token and receive the same frame
types as machine callers, including `thinking.delta`. What differs is the
payload: `Browser*` shapes on `invocation.accepted`, `invocation.update`,
`invocation.result`, and `transcript.update` omit Agent and user identity,
message phase, copy origin, host provenance, and the credit block.

Preview, resync, and end frames are identical for both audiences.

Runtime authentication is a bearer header, so the browser's built-in
`EventSource` cannot be used. It cannot set headers. Use an SSE client built on
`fetch`. Do not move the credential into the query string; query strings
survive in logs, history, and monitoring.

## Server behavior and timings

Defaults from `normalizedStreamConfig` (`server.go:3758`), all configurable:

| Setting | Default | Effect |
| --- | --- | --- |
| Poll interval | 1s | Durable drain cadence |
| Keepalive | 15s | `: keepalive` comment on an idle stream |
| Max lifetime | 55m | Then `stream.end` reason `rotate` |
| Write timeout | 10s | Slow consumer is dropped |
| Suggested retry | 1000ms | Sent as the opening `retry:` frame |

`deltas=false` turns off previews and nothing else. Replay, resumption, and
termination are unchanged. You also stop receiving `stream.resync`, since
there are no previews to invalidate.

Previews travel over a separate fan-out bus, not the database. In a
single-process deployment that bus is in memory. Across processes it is Redis,
and the daemon refuses to start in `cloud_tasks` execution mode without
`REDIS_URL` (`cmd/nvokend/config.go:358`), precisely so a multi-instance
deployment cannot silently serve durable frames with no previews. Durable
frames never depend on the bus.

## Errors are not frames

There is no `error` frame in this protocol.

Authentication, authorization, validation, and admission failures are ordinary
JSON HTTP error responses with the usual status codes. On the streaming routes
they are written before the SSE response begins, so a client must check the
response status before it starts parsing. On the inline `POST` path, admission
runs to completion before any SSE byte is written, so a refused admission is a
plain JSON error too.

Failures after the stream is open cannot be reported in band. The server logs
the reason and closes the connection. Reconnect and reconcile by reading the
Invocation. This is why every SDK ends with an authoritative read rather than
trusting the stream to have told it everything.

## Rough edges

Grouped by where a fix would land. Nothing here is on fire. The list exists so
nobody rediscovers any of it, and so we can choose deliberately instead of
patching whichever one surfaces first.

[Design 003](../design/003-streaming-protocol-target.md) closed twenty-five of
these. Resolved items are deleted rather than struck through, so the numbering
has gaps; the design document is the record of what changed and why. Reasoning
blocks (N5) were corrected rather than removed, because the answer turned out
to be that the contract was right and the note was wrong.

Items marked **(wire)** change what the server sends or what the contract
promises, so they land in `nvoken-cloud` and reach this repository through a
contract sync. Items marked **(names only)** change generated symbol names
without moving a byte on the wire, which makes them cheap. Everything else is
local to this repository.

### Protocol semantics

**P5. The same durable data ships in two envelopes.** A message arrives as
`transcript.update.messages` on one stream and `invocation.update.new_messages`
on the other, with different surrounding fields. A client watching both streams
for one Session receives every message twice in two shapes and has to
deduplicate by sequence.

**P7. Preview loss is only detectable when the server volunteers it.**
Ephemeral frames carry no sequence numbers, so a client cannot notice a gap on
its own. It is entirely dependent on `stream.resync` arriving. That is fine
while the gap detection is correct, and undetectable if it ever is not.

### Message schemas

**S4. `InvocationStreamResponse` is a union nothing can decide.** It is a
`oneOf` of two `oneOf`s whose branches overlap structurally, since the `Client*`
shapes are field subsets of the host shapes. No validator can pick a branch
from the payload. Which family you receive is decided by credential type at the
connection, which is not expressible where it currently sits.

### Naming and vocabulary

Judged against three questions: is the name short but still descriptive, is it
consistent with its siblings, and can somebody arriving with no context work
out what it means. Most field renames are breaking, so a good part of this is a
list of what we would choose differently rather than what we will change.
Schema type names are the exception and cost almost nothing.

**N3. `input` means two unrelated things.** (wire)
`CreateInvocationRequest.input` is the user's message.
`PendingHostToolCall.input` is the tool's arguments. Both are reachable inside
one turn, and a host tool handler receives the second while the first is what
started it. `arguments` on the tool call removes the collision and is no
longer.

**N4. Sibling delta frames name their payload differently.** (wire)
`OutputTextDeltaEvent.text` against `ThinkingDeltaEvent.thinking`. Every other
field on the two schemas is identical. Because that one field differs, nobody
can write a single accumulator: all four SDK reducers take `output_text` and
`thinking` as separate parameters and carry both on `StreamPreview`, where one
is always empty. A shared field name discriminated by `type` collapses that.

**N5. Reasoning is watchable and unstored, and only the contract says so.**
`SessionContentBlock` maps `text`, `image`, `document`, `tool_use`,
`server_tool_use`, `tool_result`, `reminder`, and `redacted`, and declares no
thinking block. That is deliberate: reasoning is a live preview and never a
stored block, so no read returns it and none should. The contract now states
the rule on `ThinkingDeltaEvent`. What remains is that the runtime does keep
provider reasoning artifacts for its own later calls, and the console renders
`thinking` and `redacted_thinking` blocks it receives from elsewhere, so a
reader who sees one in a payload has to know it is private evidence rather than
transcript content. See I11 for what that costs a client.

**N7. Four words for when something happened.** A single `transcript.update`
carries `created_at` on messages and `occurred_at` on changes. A delta carries
`emitted_at`. Admission records carry `attempted_at`. Each is defensible in
isolation. Together they are four words for one idea, and three of them appear
within one stream.

**N9. `pending` and `queued` are the same state in sibling enums.**
`ToolCallStatus.pending` and `InvocationStatus.queued` both mean not started.
Meanwhile `InvocationStatus.waiting` means blocked on tool calls, which is
exactly what a reader who just learned `pending` will assume `pending` means.

**N10. `new_messages` and `messages` are one concept.** (wire) Both carry
messages appended since the cursor. Every frame only ever carries new
messages, so `new_` does no work on `invocation.update`, and its absence on
`transcript.update` signals nothing.

**N11. Booleans use three conventions.** `is_error` and `is_partial` take a
prefix. `has_more` takes a different one. `deduplicated`, `cataloged`,
`replayed`, `pinned`, `deprecated`, `funded`, `supported`, `recommended`, and
`complete` take none.

**N14. The Invocation ID is reachable by one path or two, depending on the
frame.** `invocation.update` carries both `invocation_id` and `invocation.id`.
`invocation.result` carries `invocation_id` and `result.invocation.id`.
`invocation.accepted` carries only `invocation_id`. Three frames, three
shapes, one identifier.

**N15. Null `invocation_id` is scope information wearing an identifier's
name.** On `stream.resync` and `stream.end` the field is required but nullable,
and null means the frame applies to the whole Session. Everywhere else it is
required and non-null. A reader has to learn that an absent ID is a scope
declaration rather than missing data.

**N16. `limits` drops the word that matters.** The field is `limits`, the type
is `ResolvedLimits`, and the description exists to explain that these are the
limits after your installation's defaults and minimums were applied. "Resolved"
is the whole point of the field and it is the part that never reaches the wire.

**N17. `structured_output_provenance` is the longest name in the protocol** at
28 characters, sitting beside `structured_output` and `provenance` as separate
siblings. A fresh reader has to work out whether it is the provenance of the
structured output or a provenance that is itself structured.

### Incremental rendering

Everything a client hits building a live transcript out of an append-only
stream. Each item names where the nvoken console
(`nvoken-website/app/components/console/chat/`) absorbs the cost today.

**I3. `invocation_changes` advertises state and delivers history.** The reducer
keys by `(invocation_id, revision)` and returns every revision it has seen, so
every consumer folds it again to get current state (`activity.ts`,
`latestChanges`). Either the snapshot should expose the fold, or the field
should be named for the log it is.

**I6. Tool results reach backwards into an already-rendered message.** A
`tool_result` arrives in a later message than its `tool_use`, so a client needs
a transcript-wide index from tool-use id to call and must mutate an earlier row
when the result lands (`transcript-model.ts`, `foldMessages`). Delivery is
append-only; rendering is not.

**I8. Folding leaves messages with nothing to draw.** Once results fold into
their calls, a tool message can be empty and the client has to drop the row
rather than render a blank one. Every consumer reimplements that.

**I9. Compactions never stream.** They change what the model is shown from a
point backwards, so they belong in the reading order, but they are only
readable after the turn that produced them. The console fetches at connect and
at every settlement and interleaves by `covers_through`. A transcript
representation therefore contains an element that cannot arrive incrementally.

**I10. The Session stream is not a Session subscription.** `stream.end` reason
`terminal` means no turn is running, not that you remain attached. A turn
started afterwards by anyone else is invisible until you look, so the console
falls back to a 15 second poll plus a focus listener and reopens the stream
when an active Invocation appears. For a console watching a Session a host
application also drives, the steady state is polling.

**I11. Content block field casing is not uniform, so the generated type is
unusable.** The TypeScript SDK rewrites the blocks the contract models
(`tool_use_id` becomes `toolUseId`) and passes the rest through in the
runtime's snake_case, `thinking` among them. The console therefore types blocks
as `{ type?: string; [key: string]: unknown }` and reads every field through a
dual-name accessor (`transcript-model.ts`, `blockField`). The union is accurate
now (N5), so what is left is the casing: a camelCase facade over a snake_case
passthrough, which is enough to make the generated type unusable on its own.

**I12. A local claim has no place in the model.** Between `createInvocation`
returning and the reopened stream's first frame, nothing in the protocol says a
turn is live. A client that does not synthesize that state shows an idle
composer over a running turn and invites a second send. The console carries a
synthetic activity record and reconciles three sources in one function
(`activity.ts`, `resolveActivity`).

**I13. Media in a transcript cannot be rendered.** Content blocks describe
images and documents by media type, size, and `sha256:` digest rather than
carrying bytes, and nothing exposes the stored object. A client rebuilding a
conversation draws a chip where the picture was.

### Runtime behavior

**R1. Each drained page costs an extra Invocation read.** `drain` calls
`GetInvocation` per page whenever there are new messages or changes
(`stream.go:222`), so a busy stream issues one transcript query plus one
Invocation query per second per connected client.

**R2. The Session stream's terminal check runs three queries per tick.**
`terminalAfterReconcile` reads stream state, drains, then reads stream state
again to close a commit race (`stream.go:682`). Correct, and it happens every
poll interval for every connected client.

### SDK implementations

**C1. Go's reducer omits `incomplete` from its terminal set**
([`sdk/go/stream.go:129`](../../sdk/go/stream.go)). The other three include it.
A turn stopped by `max_iterations` keeps accepting preview text on a Go client
after it ended. T1 explains why this survived: no fixture has ever shown a Go
reducer an `incomplete` turn.

**C2. Python's Session stream never terminates**
([`sdk/python/src/nvoken/stream.py:151`](../../sdk/python/src/nvoken/stream.py)).
No `stream.end` terminal check at all, so it reconnects forever. Go and
TypeScript return correctly.

**C3. Rust has no Session transcript stream.** Only the Invocation stream is
implemented.

**C4. Three SDKs re-read the Invocation on every update.** Go, Python, and Rust
call `refresh()` on each `invocation.update` and `stream.end`
([`sdk/go/agent.go:265`](../../sdk/go/agent.go),
[`sdk/python/src/nvoken/agent.py:294`](../../sdk/python/src/nvoken/agent.py),
[`sdk/rust/src/agent.rs:459`](../../sdk/rust/src/agent.rs)). The frame already
carries the full projection including `pending_tool_calls`, which TypeScript
reads directly. This is an HTTP round trip per event, and it opens a window
where the refresh returns newer state than the frame the caller was just
handed.

**C5. Payloads are typed in TypeScript only.** Go returns `json.RawMessage`,
Python a raw dict, Rust a `serde_json::Value`. Three of four SDKs make callers
switch on a string and unmarshal by hand, including our own CLI at
[`cmd/nvoken/runtime.go:1972`](../../cmd/nvoken/runtime.go). The unions carry
discriminators now, so this could be mostly generated rather than
hand-written.

**C6. TypeScript's Session stream does not reconnect on transport errors.**
`streamInvocationLoop` wraps its connect in try/catch and retries
([`sdk/typescript/src/stream.ts:333`](../../sdk/typescript/src/stream.ts));
`streamSessionByID` does not (line 246), so a transport error propagates out of
the generator instead of reconnecting. The two loops sit in one file with
opposite behavior.

**C7. All four reconnect on a flat delay.** Everyone honors the server's
suggested 1000 ms and none applies backoff inside the read loop. TypeScript has
exponential backoff for the initial admission and first connect, then goes flat.
A hard-down server gets a steady 1 request per second per client from every
SDK.

**C8. Only Rust sends `Accept: text/event-stream` on the GET streams**
([`sdk/rust/src/stream.rs:224`](../../sdk/rust/src/stream.rs)). TypeScript sets
it on the Invocation stream but not the Session stream. Go and Python never set
it. The server only inspects `Accept` on the `POST` route, so this works today
and would break under any intermediary or future content negotiation.

**C9. Go caps a single frame at 2 MiB.** `readSSE` sets a bounded
`bufio.Scanner` buffer ([`sdk/go/stream.go:365`](../../sdk/go/stream.go)). A
`transcript.update` can carry up to 200 messages, so a large replay page can
exceed it and error the stream. The other three have no such limit. Media does
not contribute, since transcript content describes images and documents rather
than inlining them.

**C10. Python discards previews without a null check.**
[`sdk/python/src/nvoken/stream.py:77`](../../sdk/python/src/nvoken/stream.py)
passes `message.invocation_id` straight through for assistant messages. Seeded
history has a null `invocation_id`, and the other three guard for it. Harmless
today, by accident rather than design.

### Conformance coverage

**T1. The reducer fixture covers four frame types out of seven.**
[`reducer.json`](../../sdk/conformance/fixtures/reducer.json) exercises
`transcript.update`, both delta types, and `stream.resync`. It never sends
`stream.end`, `invocation.update`, or `invocation.result`, and the only
Invocation statuses it contains are `running` and `completed`. That is why C1
went unnoticed.

**T2. No fixture pins Invocation-stream loop behavior.** Termination,
reconnect with a cursor, and the result re-emission of P1 are all verified per
language against the fake server, or not at all. The one behavior every SDK
depends on has no shared fixture.

**T3. The conformance server's `invocation.accepted` carries no `id`**
([`sdk/conformance/server/onboarding.go:177`](../../sdk/conformance/server/onboarding.go)),
where the runtime sets it to the admission cursor. The stand-in is less
faithful than the thing it stands in for, so no SDK is tested against a
resumable accepted frame.

## Related

In this repository:

- [`openapi/nvoken.yaml`](../../openapi/nvoken.yaml). The contract snapshot.
  Frame schemas are `InvocationStreamEvent` and `TranscriptStreamEvent`.
- [`docs/guides/sdk-development.md`](../guides/sdk-development.md). How to sync
  the contract, and where the cross-language reliability rules live.
- [`sdk/conformance/fixtures/reducer.json`](../../sdk/conformance/fixtures/reducer.json).
  The pinned reducer behavior.
- [`sdk/conformance/server/main.go`](../../sdk/conformance/server/main.go). The
  fake runtime the cross-language gate streams against.
- [`docs/design/002-session-options-conflict-scope.md`](../design/002-session-options-conflict-scope.md).
  A production incident whose symptom appeared on a stream.

In `nvoken-website`, the only production client that builds a live transcript:

- `app/components/console/chat/transcript-model.ts`. Block folding, tool call
  indexing, preview grouping.
- `app/components/console/chat/activity.ts`. Reconciling stream, Session read,
  and local claim, and the completion heuristic behind I4.
- `app/components/console/chat/useSessionChat.ts`. Stream lifecycle, the idle
  poll of I10, and the compaction fetches of I9.

Product documentation:

- [Streaming & recovery](https://nvoken.com/docs/guides/streaming-and-recovery)
- [Tools](https://nvoken.com/docs/guides/tools)
- [HTTP API reference](https://nvoken.com/docs/reference/http-api)

In `nvoken-cloud`, which owns all of this:

- [`internal/adapters/httpapi/stream.go`](https://github.com/deepnoodle-ai/nvoken-cloud/blob/main/internal/adapters/httpapi/stream.go).
  Both stream handlers. The authority for everything above.
- [`internal/domain/streaming.go`](https://github.com/deepnoodle-ai/nvoken-cloud/blob/main/internal/domain/streaming.go).
  Frame payload types and reason constants.
- [`internal/ports/streaming.go`](https://github.com/deepnoodle-ai/nvoken-cloud/blob/main/internal/ports/streaming.go).
  The live event bus, and why previews are lossy by design.
- [`internal/adapters/postgres/repositories.go`](https://github.com/deepnoodle-ai/nvoken-cloud/blob/main/internal/adapters/postgres/repositories.go).
  Where `phase` is derived on read, which is what makes P3 possible.
