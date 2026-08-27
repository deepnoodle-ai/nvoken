# The streaming protocol

**Status:** Descriptive. This is how the published protocol behaves today:
two scopes, one saved frame, one terminal signal, one preview frame, and an
opaque cursor that can cross from a bound Turn stream to its Conversation.
**Verified against:** the runtime's stream handler, the contract in
[`openapi/nvoken.yaml`](../../openapi/nvoken.yaml), and the four SDK
implementations in this repository.
**Date:** 2026-08-26
**Authority:** The runtime is the authority. Where this document and the
runtime disagree, the runtime is right and this document is stale.

## Who this is for

People implementing against the wire: SDK authors in this repository, and
anyone writing a client nvoken does not ship. It covers the frames, their
durability, and the client-side algorithm the SDKs implement.

If you are consuming nvoken through an SDK, read
[Streaming & recovery](https://nvoken.com/docs/guides/streaming-and-recovery)
instead. It is shorter and answers the questions callers actually have.

## Two scopes, two routes

```
GET /v1/turns/{turn_id}/stream
GET /v1/conversations/{conversation_id}/stream
```

Both routes accept `cursor`. The Turn route also accepts `deltas=false` to
turn previews off and `Last-Event-ID` as a cursor when the query parameter is
absent. The Conversation route is a durable subscription to current and future
Turns and does not accept a Turn filter.

Admission is a separate request. `POST /v1/turns` returns the turn and
that response is the acknowledgment; then you open the stream. The separation
is what makes retries safe: if the admission response is lost you retry the
exact body and idempotency key, and once you hold a `turn_id` no
reconnect can create a second turn.

Use the Turn route for one execution, including a standalone Turn with no
public Conversation. It carries only that Turn and closes after its terminal
change. Use the Conversation route when you want an idle subscription that
also receives Turns created later. A cursor from a conversation-bound Turn is
accepted by its Conversation stream; a standalone Turn cursor is not.

## Streams, connections, and scope

A stream is not a connection. The stream is a durable, ordered log addressed by
cursor; a connection is one HTTP response reading it for a while. Losing that
distinction is the most common way to misread this protocol, so the vocabulary
here is deliberate.

**A Conversation stream is a subscription.** One connection follows every turn
in the Conversation, interleaved, and it stays open while the Conversation is idle. A
turn started later by anyone appears on it. There is nothing to poll.

**A Turn stream delivers one turn and closes.** The server hangs up once that
Turn's terminal change has gone out. It says nothing
on the way out, because a client following the exit rule has already left.

**Connections are disposable; the stream is not.** A connection ends by
rotation, by idle reclamation, by a slow-consumer drop, or by ordinary network
failure. None of these says anything about a turn. Work continues, the log
keeps growing, and the next connection opened with the last durable cursor sees
everything the previous one did not. The timing machinery in
[Server behavior and timings](#server-behavior-and-timings) — the keepalive,
the lifetime, the write timeout — all belongs to the connection, never to the
stream.

**Any number of connections may read one stream.** Readers are independent.
They can sit at different cursors, join mid-turn, or replay a turn that settled
last week, and none of them affects the turn or each other. Disconnecting never
cancels anything. A support console can watch a customer's live turn while the
customer's own client streams it, with no fan-out machinery in the host
application.

**The lifecycle of one connection**, in order: it opens with or without a
cursor; the server writes the `retry: 1000` opener; saved state replays, from
the cursor if one was given and otherwise from the selected scope's origin; the
connection goes live, interleaving durable frames with previews; and it ends
with `connection.closing` or a silent drop. From the client's side the whole algorithm
is one loop: connect, fold frames, remember the last durable `id`, reconnect
with it.

### The transport

The stream speaks Server-Sent Events today, and SSE is the only binding. Almost
nothing in the protocol depends on it: the semantics live in the frames,
durable frames carry a cursor, ephemeral frames do not, and resume is an
explicit position handed back at reconnect. The SSE-specific surface is exactly
three mechanics — the `id:` line, the `retry:` opener, and comment keepalives —
each of which has an obvious equivalent in any framed transport.

### What the stream carries

Frames are JSON, and their content is structure and text. Images and documents
in a transcript travel as descriptor blocks, carrying media type, size, and
`sha256:` digest, never inline bytes. That keeps frame sizes bounded by text
alone and keeps the stream cheap to fan out to many readers. It also means a
client cannot render described media from the stream by itself; retrieving the
bytes is outside the protocol today, recorded as I13.

Every frame carries `conversation_id` and `content_expires_at`. A
Conversation-bound frame carries its Conversation ID and has no content expiry.
A standalone Turn has no Conversation ID; it receives a non-null expiry after
settlement, and its frames and messages carry that same authority boundary.
This lets clients discard private content on schedule without first resolving
another resource.

## Durable and ephemeral frames

This is the organizing idea. Every frame is one or the other, and the
difference decides what you may store and what you must be prepared to lose.

**Durable frames carry an SSE `id`.** That ID is a resume cursor and the only
value a client needs to persist. Reconnect with it and the server replays saved
state from that position, then continues live.

**Ephemeral frames carry no `id`.** They are live previews and control signals.
They are not saved anywhere, they are never replayed, and a client that treats
them as state will show text that no transcript will ever confirm.

The server omits the `id:` line entirely rather than sending an empty one.

| Frame | `id` | Meaning |
| --- | --- | --- |
| `transcript.update` | yes | Ordered messages and lifecycle changes. |
| `message.delta` | no | Live preview of a message being written. |
| `stream.resync` | no | Previews were lost. Discard them. |
| `connection.closing` | no | Connection is closing. Read `reason`. |

### Resuming

Send the last durable `id` as either the `cursor` query parameter or the
`Last-Event-ID` header. The query parameter wins when both are present.
`Last-Event-ID` may appear at most once and must not be blank.

Disconnecting never cancels a turn. It keeps running and you can reconnect or
read it later.

## Two frames that are not events

The stream opens with a bare `retry: 1000` control frame: no `event:`, no
`data:`. It sets the client's reconnect delay to 1000 ms.

Every 15 seconds an idle stream emits an SSE comment line, `: keepalive`.

Neither carries a payload. A parser that assumes every dispatched frame has
JSON `data` will fail on the very first frame of every stream. All four SDKs
special-case this, each with a comment saying so, because each one hit it
against the real runtime.

## Frame reference

### `transcript.update`

Ordered `messages` and `turn_changes`, plus `conversation_id`,
`content_expires_at`, and a `cursor` in the payload that matches the SSE `id`.
On a Turn stream both arrays carry only that Turn's rows. On a Conversation
stream they may interleave rows from multiple Turns.

Empty pages are not sent. The server advances its internal watermark but only
writes a frame when there is at least one message or change, so every
`transcript.update` you receive is non-empty.

Apply messages before lifecycle changes from the same frame. Otherwise a UI can
show a turn as complete before its final message exists.

**A turn is over when a change for it carries `terminal: true`.** That is the
terminal signal, and there is no other. It is durable, so it replays on
reconnect like every other change, at any cursor: a turn that settled while you
were away is still settled when you return, with no re-emission machinery
anywhere. Read `GET /v1/turns/{turn_id}/result` if you want the composed result.

A change carries the facts a status alone leaves open: `terminal` for whether
this change is the ending, `stop_reason` for a turn that stopped short,
`credit_block` for one waiting on an account, and `tool_calls` for where each
call in the turn got to, with `mode` on every entry and `arguments` on the ones
you may settle.
A client can recover from the change stream alone.

Read `terminal` rather than testing `status` against a set of your own. There
are eight statuses and four are terminal, so the mistake worth designing out is
encoding the other four — `budget_hold`, a turn stopped on spending capacity
with its deadlines on hold, is not an ending, and a turn wrongly believed
finished is one nobody settles or reattaches to. TypeScript, Python, and Rust
export `isTurnOver`, `is_turn_over`, and `is_turn_over`, respectively. Go
callers read the required `TurnChange.Terminal` field directly.

`terminal` describes the change, not the turn. A replayed `running` change
stays `false` after the turn has ended, which is what keeps the rule above —
messages before changes — from marking a turn settled early.

### `message.delta`

The one preview frame. Identity is the pair:

```
(message_id, content_index)
```

`message_id` is the identifier the saved message will land under. It is
required and allocated when the model starts writing, so keying a rendered
preview by it makes the handoff an update to a row that already has its
permanent identity rather than one row disappearing and another taking its
place. `content_index` separates concurrent content blocks within one message.

`kind` says what the fragment is — `text`, `thinking`, or `tool_arguments` —
and `delta` carries it, for every kind. One accumulator handles all three.
`tool_arguments` fragments also carry `tool_call_id` and `name`, denormalized
onto every frame, because the block that named the call is a separate event a
live consumer may never have seen. Treat a `kind` you do not recognize as
content you do not render, and keep reading.

`attempt` is the retry discriminator described below. There is no `iteration`:
one model iteration produces exactly one message, so `message_id` already says
which iteration this is.

The server validates every delta before forwarding and silently drops
malformed ones: `attempt` at least 1, a non-empty `message_id` and `delta`,
`content_index` at least 0, a non-zero `emitted_at`, a kind it knows, and
`tool_call_id` and `name` present exactly on `tool_arguments`.

Reasoning is watchable and unstored. No content block carries it and no read
returns it, so the stream is the only place to see the model think. A turn
running under explicit `reasoning` controls emits no reasoning previews at all,
to any audience.

### `stream.resync`

The live delivery path could not prove continuity, so previews were lost. The
only reason value today is `live_delivery_gap`. Treat one you do not recognize
the same way.

Discard accumulated preview text and wait for durable frames. The server drains
the transcript immediately after sending a resync, so the replacement is
already on its way.

`turn_id` is present when only one Turn's previews are void and absent when
every preview in a Conversation is void. Absent is scope: discard previews for
the whole Conversation. It is an absent
field rather than a null identifier, because a null identifier is scope wearing
an identifier's name.

The Turn route never emits this frame when `deltas=false`, because the server only
subscribes to the live bus when previews are enabled and resyncs originate from
that subscription.

### `connection.closing`

**This frame never speaks about a turn.** Terminal state is a durable change;
connection close is a connection event; no reason value couples them. Three
reasons:

- **`rotate`** means the server is cycling the connection. Reconnect now with
  your last durable cursor. It fires on server shutdown, or when a connection
  that carried traffic reaches its 55 minute lifetime.
- **`idle`** means the server is reclaiming a connection nothing was waiting
  on. Reconnect when you next need to read; nothing is lost while you are away.
- **`slow_consumer`** means this connection could not keep up with what was
  being written to it and was dropped. Reconnect, and read faster or buffer
  more. The frame is best effort: a consumer too slow to accept the frame
  before it may be too slow to accept this one.

Treat a reason you do not recognize as `rotate`. Reconnecting is always safe.

There is no cursor on this frame. A client already tracks its last durable one,
because it needs that to survive a connection that drops without saying
anything, and a field that repeats what the client must hold anyway is a second
spelling of it.

A connection that simply drops carries no meaning. Reconnect and resume.

## Previews: accumulate and discard

The rules, in the order a client should apply them:

1. **Accumulate** by `(message_id, content_index)`. Append `delta` to that
   entry, whatever its `kind`.
2. **Discard on attempt increase.** A higher `attempt` for a Turn means
   execution was claimed again after recovery. Everything provisional from
   earlier attempts is dead. Ignore deltas from a lower attempt than the
   highest one seen.
3. **Discard on resync.** Scoped to one Turn, or the whole Conversation when
   `turn_id` is absent.
4. **Discard when the saved message lands.** A durable message supersedes the
   preview that was building it.
5. **Discard on terminal status**, and refuse later previews for that
   Turn. A settled turn cannot produce more provisional output.

`attempt` is the durable anchor. It appears on the delta and on the Turn
itself, so a client can discard stale output even across a reconnect where it
never saw the resync frame.

Never store preview text as a transcript message, and never use it to decide
whether a turn succeeded.

## The reducer

Any consumer that renders a live transcript needs the same fold: durable
messages by sequence, lifecycle changes by `(turn_id, revision)`,
previews by `(message_id, content_index)`, plus the resume cursor and the set
of turns a terminal change has arrived for. This repository implements it four
times:

- [`sdk/go/stream.go`](../../sdk/go/stream.go) `Reducer`
- [`sdk/typescript/src/stream.ts`](../../sdk/typescript/src/stream.ts) `Reducer`
- [`sdk/python/src/nvoken/stream.py`](../../sdk/python/src/nvoken/stream.py) `Reducer`
- [`sdk/rust/src/stream.rs`](../../sdk/rust/src/stream.rs) `Reducer`

Behavior is pinned by
[`sdk/conformance/fixtures/reducer.json`](../../sdk/conformance/fixtures/reducer.json),
which all four test suites read. It covers accumulation of every kind including
tool arguments, discard on attempt increase, discard on a scoped and an
unscoped resync, replacement by the saved message, and one snapshot case
asserting that an ephemeral frame's `id` is never adopted as a resume cursor.

A behavior represented in more than one SDK belongs in that fixture rather than
in four unrelated tests. See
[SDK and contract development](../guides/sdk-development.md).

## The client algorithm

The whole protocol, from a client's chair:

1. Admit with `POST /v1/turns`, idempotency key attached. The response
   acknowledges the turn.
2. Open `GET /v1/turns/{id}/stream` to follow that Turn, with `cursor` if
   resuming. Open `GET /v1/conversations/{id}/stream` instead when you want a
   subscription that remains alive for later Turns.
3. Fold: append messages by `sequence`; append changes and fold by highest
   `revision`; accumulate `message.delta` by `(message_id, content_index)`.
   Discard previews on attempt increase, on resync, when the saved message
   lands, and on the terminal change.
4. Exit when the change you care about carries a terminal status. On any
   connection end, reconnect with your cursor: immediately on `rotate`, lazily
   on `idle`, after widening your buffer on `slow_consumer`, immediately on a
   silent drop.
5. Read `GET /v1/turns/{id}/result` if you want the composed result.

One loop, one exit condition, one reconnect rule.

## Building a transcript incrementally

A stream is a sequence of appends. A transcript is not. Getting from one to the
other is where most of this protocol's remaining friction lives, and the
reducer only covers the first third of it.

The nvoken console is a worked example. It is not public, but it runs the
TypeScript SDK in the browser against the Conversation stream, and every claim in
this section came from it.

### Five kinds of state, five update rules

| State | Keyed by | Arrives on | Rule |
| --- | --- | --- | --- |
| Message | `sequence` | `transcript.update.messages` | Append. The stream never re-sends one. |
| Lifecycle change | `(turn_id, revision)` | `transcript.update.turn_changes` | Append to a log, then fold to the highest revision to get current state. |
| Preview | `(message_id, content_index)` | `message.delta` | Concatenate, then discard wholesale on any of the five triggers above. |
| Tool call | `tool_use.id` | opened by one message, closed by a later one; status on `turn_changes.tool_calls` | Retroactively update a message you already rendered. |
| Compaction | not streamed at all | `GET /v1/conversations/{id}/compactions` | Fetch separately, interleave by `covers_through`. |

Only the first is a plain append. That is the whole problem.

### The preview handoff is a merge

Previews are keyed by `(message_id, content_index)` and the message is keyed by
`id`, so they share their identity. Key your preview row by `message_id` and
the handoff is an update to a row that already has its permanent identity,
rather than one row disappearing and another taking its place. Group the
content indexes of one `message_id` into one row and the preview is the same
shape as the message it becomes, which is what stops a turn that thinks before
it answers from rendering two rows that collapse into one.

### Tool results reach backwards

A `tool_use` block arrives in one assistant message. Its `tool_result` arrives
in a different, later message. So a client cannot render messages
independently: it needs a transcript-wide index from tool-use id to call, and
when a result lands it mutates a row it drew earlier.

One consequence falls out of that: a message whose blocks all fold into earlier
calls has nothing left to draw and must be dropped rather than rendered empty.

Progress between the call and its result reaches the stream twice. Lifecycle
changes carry `tool_calls`, a status and timestamp per call in the turn, and a
settling call reserves a revision of its own so the change is delivered and
replayed rather than inferred from a result block appearing. Live, the model
writing its arguments arrives as `message.delta` frames of kind
`tool_arguments`, which are only valid JSON once complete.

### Messages and the changes that settle them arrive together

One transcript page carries messages first and then the lifecycle changes that
settled them, under one shared budget, so the assistant message that ends a
turn and the terminal status usually reach you in the same frame. Apply
messages before lifecycle changes within a frame.

They can still split across frames when a page fills on messages alone: a turn
that produced more messages than your `limit` spends the whole budget on them
and the changes follow. So a client testing "is this Turn still active"
can still say yes for a beat after the answer is on screen, and the fix is to
wait for the change rather than to re-derive completion from block inspection.

### What the stream will not tell you

Compactions never stream. They change what the model is shown from a point
backwards, and are only readable after the turn that produced them, so a client
fetches them at connect and again at every settlement.

Message `phase` is worked out on read, so a message delivered on the stream
before its turn settled carries `commentary` forever and no later frame
corrects it. Derive the answer instead: a turn has a final answer only once it
settled `completed` with stop reason `end_turn`, and that answer is the turn's
last assistant message.

### Reconciling two sources of truth

A live client holds two claims about what the Conversation is doing:

1. **The stream.** Authoritative, and now a subscription, so a turn started by
   anyone else appears on the connection that is already open.
2. **A local claim.** After your own `createTurn` returns, you know a turn
   exists before any stream frame proves it. Without holding that claim, a
   composer drops back to idle for a beat and invites the user to send twice.

There used to be a third, a periodic Conversation read, because the stream hung up
whenever no turn was running. The subscription removes it.

## Tool calls

**Tool calls have no lifecycle frames of their own.** There is no
`tool_call.started` or `tool_call.completed`. This surprises people, so it is
worth stating plainly.

A tool call reaches you four ways at once:

1. **As transcript content.** The assistant message carries a `tool_use` block;
   the answering message carries a `tool_result` block naming it by
   `tool_use_id`. This is the durable record and it arrives in
   `transcript.update` messages.
2. **As a status.** The Turn moves to `waiting`, which other APIs call
   `requires_action`. Nothing is executing. The turn is parked.
3. **As lifecycle state.** Every lifecycle change carries `tool_calls`, one
   entry per call in the turn with `id`, `name`, `mode`, `status`, and
   `updated_at`. The ones you may settle also carry `arguments` and
   `deadline_at`; the ones you must run yourself are those with `mode: host`.
   An acknowledged callback delivery is settleable by a machine credential but
   is nvoken's to deliver, which is why the mode and not just the arguments
   decides. A call reaching a terminal state reserves a lifecycle revision, so
   the change is delivered and replayed like any other.
4. **As live texture.** `message.delta` frames of kind `tool_arguments` show
   the model writing the call, lossy and disposable like every preview.

To advance the turn, `POST /v1/turns/{id}/tool-results` with each
`tool_call_id` returned verbatim. The turn returns to `queued` and picks up
where it left off. A partial batch leaves it waiting.

`waiting` is not terminal, so the stream stays open across it. A turn can also
return to `queued` on its own after runtime recovery. `attempt` tells the two
apart, and `revision` on each change orders them.

Builtin, MCP, and callback tools resolve without host involvement and never
park the turn this way. Only host tools and undelivered callbacks do. See
[Tools](https://nvoken.com/docs/guides/tools) for the mode taxonomy.

## Browser streams

Browser clients authenticate with a client token and receive the same frames as
machine callers, reasoning previews included. What differs is the payload: one
schema per resource, with the fields a browser may not see simply omitted —
Agent and user identity, message phase, copy origin, host provenance, and the
credit block. There are no parallel browser schemas.

Preview, resync, and end frames are identical for both audiences.

Runtime authentication is a bearer header, so the browser's built-in
`EventSource` cannot be used. It cannot set headers. Use an SSE client built on
`fetch`. Do not move the credential into the query string; query strings
survive in logs, history, and monitoring.

## Server behavior and timings

Defaults from `normalizedStreamConfig`, all configurable:

| Setting | Default | Effect |
| --- | --- | --- |
| Poll interval | 1s | Durable drain cadence |
| Keepalive | 15s | `: keepalive` comment on an idle stream |
| Max lifetime | 55m | Then `connection.closing`, `rotate` or `idle` |
| Write timeout | 10s | Slow consumer is dropped |
| Suggested retry | 1000ms | Sent as the opening `retry:` frame |

A connection that carried nothing for its whole lifetime is reclaimed as
`idle`; one that carried traffic rotates. Both mean reconnect, and the
difference is only how soon.

On a Turn stream, `deltas=false` turns off previews and nothing else. Replay,
resumption, and termination are unchanged. You also stop receiving
`stream.resync`, since there are no previews to invalidate. Conversation
streams always include the live preview path.

Previews travel over a separate fan-out bus, not the database. In a
single-process deployment that bus is in memory. Across processes it is Redis,
and the daemon refuses to start in `cloud_tasks` execution mode without
`REDIS_URL`, precisely so a multi-instance deployment cannot silently serve
durable frames with no previews. Durable frames never depend on the bus.

Per connected client per poll tick the server issues one transcript query and
nothing else. Terminal closure on the Turn route is driven by the durable
change it already read, not by a second Turn lookup.

## Errors are not frames

There is no `error` frame in this protocol.

Authentication, authorization, validation, and admission failures are ordinary
JSON HTTP error responses with the usual status codes. On either streaming route
they are written before the SSE response begins, so a client must check the
response status before it starts parsing. A Turn or Conversation outside the
caller's scope is answered with `404 not_found`.

Failures after the stream is open cannot be reported in band. The server logs
the reason and closes the connection. Reconnect and reconcile by reading the
Turn. This is why every SDK ends with an authoritative read rather than
trusting the stream to have told it everything.

## Rough edges

Grouped by where a fix would land. Nothing here is on fire. The list exists so
nobody rediscovers any of it, and so we can choose deliberately instead of
patching whichever one surfaces first.

[Design 003](../design/003-streaming-protocol-target.md) closed twenty-five of
these, and [design 004](../design/004-protocol-end-state.md) closed eighteen
more across its three steps. Resolved items are deleted rather than struck
through, so the numbering has gaps; the design documents are the record of what
changed and why.

Items marked **(wire)** change what the server sends or what the contract
promises, so they land in the service and reach this repository when the
contract is published. Everything else is local to this repository.

### Protocol semantics

**P7. Preview loss is only detectable when the server volunteers it.**
Ephemeral frames carry no sequence numbers, so a client cannot notice a gap on
its own. It is entirely dependent on `stream.resync` arriving. That is fine
while the gap detection is correct, and undetectable if it ever is not.

### Naming and vocabulary

Judged against three questions: is the name short but still descriptive, is it
consistent with its siblings, and can somebody arriving with no context work out
what it means.

**N7. Three words for when something happened.** (wire) A single
`transcript.update` carries `created_at` on messages and `occurred_at` on
changes; a delta carries `emitted_at`; admission records carry `attempted_at`.
The resource-versus-event split explains two of them. It does not explain four.

**N9. `pending` and `queued` are the same state in sibling enums.**
`ToolCallStatus.pending` and `TurnStatus.queued` both mean not started.
Meanwhile `TurnStatus.waiting` means blocked on tool calls, which is
exactly what a reader who just learned `pending` will assume `pending` means.

**N11. Booleans use three conventions.** `is_error` and `is_partial` take a
prefix. `has_more` takes a different one. `deduplicated`, `cataloged`,
`replayed`, `pinned`, `deprecated`, `funded`, `supported`, `recommended`, and
`complete` take none.

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
stream. Each item names where the nvoken console absorbs the cost today.

**I3. `turn_changes` advertises state and delivers history.** The reducer
keys by `(turn_id, revision)` and returns every revision it has seen, so
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

**I11. Content block field casing is not uniform, so the generated type is
unusable.** The TypeScript SDK rewrites the blocks the contract models
(`tool_use_id` becomes `toolUseId`) and passes the rest through in the
runtime's snake_case. The console therefore types blocks as
`{ type?: string; [key: string]: unknown }` and reads every field through a
dual-name accessor (`transcript-model.ts`, `blockField`).

**I12. A local claim has no place in the model.** Between `createTurn`
returning and the stream's first frame for that turn, nothing in the protocol
says it is live. A client that does not synthesize that state shows an idle
composer over a running turn and invites a second send.

**I13. Media in a transcript cannot be rendered.** Content blocks describe
images and documents by media type, size, and `sha256:` digest rather than
carrying bytes, and nothing exposes the stored object. A client rebuilding a
conversation draws a chip where the picture was.

### SDK implementations

**C5. Payloads are typed in TypeScript only.** Go returns `json.RawMessage`,
Python a raw dict, Rust a `serde_json::Value`. Three of four SDKs make callers
switch on a string and unmarshal by hand, including our own CLI at
[`cmd/nvoken/runtime.go`](../../cmd/nvoken/runtime.go). The union carries a
discriminator, so this could be mostly generated rather than hand-written.

**C7. All four reconnect on a flat delay.** Everyone honors the server's
suggested 1000 ms and none applies backoff inside the read loop. A hard-down
server gets a steady 1 request per second per client from every SDK. The
subscription raises the stakes: an idle connection now reconnects on the same
flat delay it used while a turn was running.

**C9. Go caps a single frame at 2 MiB.** `readSSE` sets a bounded
`bufio.Scanner` buffer ([`sdk/go/stream.go`](../../sdk/go/stream.go)). A
`transcript.update` can carry up to 200 messages, so a large replay page can
exceed it and error the stream. The other three have no such limit. Media does
not contribute, since transcript content describes images and documents rather
than inlining them.

**C11. A Turn stream still scans its durable carrier from the Turn's first
row.** That carrier is intentionally hidden for a standalone Turn, and the
route filters every emitted frame to the selected Turn. The public semantics
are direct even though the storage scan remains transcript-backed.

### Conformance coverage

**T4. No fixture exercises a Conversation subscription across turns.** The
shared server's stream ends after three attempts, so nothing pins the behavior
that a turn started later arrives on a connection that was already open. Python
tests it against a local fake; the other three do not test it at all.

## Related

In this repository:

- [`openapi/nvoken.yaml`](../../openapi/nvoken.yaml). The contract snapshot.
  The frame schema is `ConversationStreamEvent`.
- [`docs/design/004-protocol-end-state.md`](../design/004-protocol-end-state.md).
  The end state this document now describes, and the path that got here.
- [`docs/guides/sdk-development.md`](../guides/sdk-development.md). How a
  published contract is adopted, and where the cross-language reliability rules
  live.
- [`sdk/conformance/fixtures/reducer.json`](../../sdk/conformance/fixtures/reducer.json).
  The pinned reducer behavior.
- [`sdk/conformance/server/main.go`](../../sdk/conformance/server/main.go). The
  fake runtime every SDK transport decodes against in the cross-language gate.
  Streaming behavior remains covered by focused reducer and facade tests; T4
  above records the missing cross-Turn subscription case.

The nvoken console, not public, is the only production client that builds a
live transcript. Its transcript model does block folding, tool call indexing,
and preview grouping; its activity layer reconciles stream state with a local
claim; its conversation hook owns stream lifecycle and the compaction fetches of I9.

Product documentation:

- [Streaming & recovery](https://nvoken.com/docs/guides/streaming-and-recovery)
- [Tools](https://nvoken.com/docs/guides/tools)
- [HTTP API reference](https://nvoken.com/docs/reference/http-api)

The service owns all of this, and is not public. Behind the contract are one
stream handler — the authority for everything above — the frame payload types
and reason constants it serializes, and a live event bus whose previews are
lossy by design.
