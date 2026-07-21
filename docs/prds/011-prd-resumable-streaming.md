# Stream Resumable Session Output Without Owning Execution

**Status:** Implemented; cloud proof pending

**Sequence:** 011

**Depends on:** `005-prd-generation-only-turns.md`,
`007-prd-recovery-and-transcript-reads.md`,
`008-prd-invocation-controls-and-budgets.md`,
`010-prd-cloud-tasks-invocation-execution.md`

## ELI5

A host can watch an agent answer live, reconnect after a network break, and
finish from the real saved transcript. Token previews may be lost, but saved
messages and final status may not be. Closing the stream never cancels or owns
the Invocation; tools and crash-resumable execution still come later.

## Why

nvoken already accepts work in the background and exposes a fixed-cut JSON
transcript for recovery. Hosts must currently poll that endpoint and receive no
in-flight model output. Streaming must now improve latency without weakening
the existing rule that Postgres—not an HTTP connection, Redis, or a provider
stream—is authoritative.

Mobius Cloud provides useful precedent: subscribe before the first durable
drain, put resume IDs only on authoritative state frames, treat generation
deltas as live-only, poll as a correctness fallback, and deliberately end
long-lived streams for reconnect. nvoken keeps those properties but omits
Mobius's separate live-transcript accumulator, interaction model, and legacy
event surfaces. Lost previews reconcile directly to canonical
`SessionMessage` and Invocation-lifecycle rows.

## Outcome

An authenticated host can open one Session SSE stream, receive live
provider-neutral generation deltas when available, and reduce authoritative
transcript snapshots from an opaque cursor. Reconnect, replica changes, Redis
loss, slow consumers, deploys, and normal stream rotation cannot lose or
misstate committed transcript or terminal Invocation state.

## Scope

**In:** one Session transcript SSE endpoint; PRD 007 cursor replay; fixed-cut
snapshot frames; `Last-Event-ID`; ephemeral generation deltas from Dive;
bounded in-process and Redis Pub/Sub fan-out; gap signaling; database polling;
keepalives; write bounds; terminal reconciliation; deliberate rotation; the
private Redis dependency and Cloud Run request timeout for the Google paved
path; OpenAPI and operator guidance; the governing API, architecture, and
decision-log updates that freeze the replay contract.

**Out:** persistence or replay of token deltas; a second message/event table;
live-preview snapshots; WebSockets; stream-owned generation or cancellation;
new admission modes; tools or structured output; provider-native event
envelopes; SDK reducers; resume after engine loss; multi-region Redis or a
durability claim for Redis Pub/Sub.

## Requirements

- **R1 — One resumable SSE contract.**
  `GET /v1/sessions/{session_id}/transcript/stream` must authenticate and scope
  the Session exactly like the JSON transcript read. It accepts an optional
  opaque `cursor`; when that parameter is absent, `Last-Event-ID` is the
  cursor. An explicitly supplied query parameter takes precedence. Malformed,
  ahead-of-head, cross-Account, cross-Session, and unauthorized cursors retain
  the JSON endpoint's `400`, `403`, and `404` behavior before SSE headers are
  committed.

- **R2 — Durable frames project the recovery model.**
  Before tailing live work, and after every durable wake or polling interval,
  the server must completely drain PRD 007 fixed-cut pages from its last
  delivered cursor. Each nonempty `transcript.snapshot` frame contains the
  canonical messages and Invocation changes from one JSON snapshot page and
  carries `id: <resume_cursor>`. Messages therefore precede terminal lifecycle
  evidence, and reconnecting with the last ID may replay no committed row or
  only rows after that exact delivered watermark. Empty drains do not invent a
  new durable ID.

- **R3 — Live deltas are useful but explicitly ephemeral.**
  The model-generator port must support a normalized streaming callback while
  retaining the same complete response result used by fenced settlement. Both
  embedded and private-executor generation use this seam for every supported
  streaming provider, whether or not a subscriber is currently present. While
  Dive streams a response, nvoken may publish
  provider-neutral `generation.delta` frames containing Session and Invocation
  identity, the fenced lease attempt, an attempt-local delta sequence,
  content-block index, emitted time, and supported text or thinking content.
  These frames carry no SSE `id`, are never written to Postgres, never advance
  a transcript cursor, never contain credentials or raw provider envelopes,
  and cannot make model execution fail when publication is unavailable.
  Streamed and non-streamed provider paths must normalize to identical
  canonical messages, usage, provenance, budget checks, and typed
  provider/deadline failure settlement. The eventual canonical assistant
  message replaces any client preview.

- **R4 — Subscribe before drain; Postgres repairs every race.**
  The handler must establish its Session fan-out subscription before the first
  database drain so a commit or delta cannot fall silently between bootstrap
  and tailing. Bus events are latency hints; a bounded database poll remains
  the correctness path for missed publication, Redis disconnect, cancellation,
  settlement, and reaper transitions. Duplicate hints may cause empty drains
  but cannot duplicate a durable row beyond cursor replay semantics.

- **R5 — Loss and backpressure are bounded and visible.**
  Publication must never block a provider or transaction on a slow stream.
  Every publisher and subscriber has a bounded buffer. Overflow or a Redis
  subscription gap may discard live-only deltas, but the stream stays usable,
  emits one id-less `stream.resync` instruction, clears its preview assumption,
  and re-drains Postgres. Each network write has a bounded deadline; a client
  that cannot consume within it is disconnected without changing Invocation
  state. Delta content and credentials must not appear in operational logs.

- **R6 — Database-derived ending and deliberate rotation.**
  SSE comments keep an otherwise quiet connection alive. A stream may emit
  id-less `stream.end` with reason `terminal` only after drain → authoritative
  Session read → final drain → authoritative Session re-read shows no
  nonterminal Invocation. Opening an already-idle Session therefore replays its
  retained state and ends rather than waiting for a future admission. An active
  stream must emit reason `rotate` before its configured maximum lifetime. Both
  deliberate reasons instruct the client to retain the last durable ID;
  rotation is reconnectable. A process shutdown should emit `rotate` when its
  remaining drain budget permits, but a forced or abnormal close has no
  synthetic terminal meaning. Disconnecting, rotating, timing out, or shutting
  down a stream never cancels execution.

- **R7 — The two process topologies have the same public semantics.**
  Self-contained mode uses the fan-out port's in-process adapter when no Redis
  URL is configured. Split Cloud Tasks execution requires Redis Pub/Sub so the
  private executor can publish deltas to any public runtime replica. The Google
  paved path must provision private Memorystore connectivity with Redis AUTH
  and server-authenticated TLS, configure both roles with the generated secret
  and active instance CAs, and give the public Cloud Run request enough time
  for application-led rotation. Redis contains only short-lived Pub/Sub
  envelopes and grants no execution ownership.

- **R8 — The wire contract is reducer-friendly and operable.**
  OpenAPI must document the SSE media type, exact event names and schemas,
  cursor precedence, id/no-id rule, retry guidance, terminal/rotation reasons,
  and the fact that bearer-capable streaming clients are required. Operators
  must be able to distinguish normal terminal close, rotation, client
  disconnect/write timeout, subscriber gap, Redis failure, and durable-drain
  failure through bounded metadata-only logs or metrics.

- **R9 — The governing contract records the replay decision.**
  `docs/design/api.md` must list the Session stream and its durable/live frame
  classes, `docs/design/decisions.md` must record that cursor-bearing transcript
  snapshots are replayable while generation deltas are live-only, and the
  corresponding architecture open question must be closed. These documents and
  OpenAPI must agree that Redis is not a durable record or execution fence.

## Acceptance

- [x] **A1 (R1, R2):** Starting from no cursor replays a multi-page transcript
  in message-before-lifecycle order. Reconnecting with the final SSE ID returns
  no duplicate durable rows; commits made after the original cut appear once
  on the next drain. Query `cursor` precedence and invalid/scope-bound cursor
  failures are covered before a `200 text/event-stream` response begins.

- [x] **A2 (R2, R3):** A streaming fake model emits several deltas and then a
  completed assistant message. The client sees ordered id-less
  `generation.delta` frames, followed by a cursor-bearing snapshot containing
  the exact canonical message and terminal change. Restarting the server and
  reconnecting from the cursor reconstructs the same terminal state without
  any delta store. A mid-stream provider failure and a blocking-provider
  fallback produce the same typed settlement, message/usage/provenance rules,
  and budget behavior as the existing generation path.

- [x] **A3 (R3, R4):** A commit or delta deliberately released in the
  subscribe-before-drain window is observed either live or by the first durable
  drain. Lost, duplicate, and out-of-order wake hints do not skip or duplicate
  committed messages or lifecycle revisions, and polling alone discovers a
  cancellation or terminal settlement.

- [x] **A4 (R3, R5):** With buffers forced to one, a burst overflows without
  blocking model completion. The stream emits `stream.resync` without an ID,
  later delivers the authoritative terminal snapshot, and logs neither delta
  text nor request/spec secrets. A blocked response writer is closed within the
  configured write bound while the Invocation continues to settlement.

- [x] **A5 (R6):** An active stream rotates at a short test lifetime after a
  keepalive and emits `stream.end {reason:"rotate"}` with no ID. A terminal
  Invocation closes only after its assistant message and terminal lifecycle
  change have been delivered and the terminal double-check completes, using
  reason `terminal`. Opening an already-idle Session drains and ends the same
  way. Client disconnect and forced shutdown leave the durable Invocation
  unchanged.

- [x] **A6 (R7):** In-process integration proves embedded generation and SSE
  share the port. Redis integration proves an executor-side publisher and a
  separate runtime-side subscriber exchange deltas while Postgres-only polling
  still delivers settlement during a simulated Redis outage or reconnect.

- [x] **A7 (R7, R8):** Terraform tests prove private Memorystore, Redis URL
  routing to both combined and executor services, Redis AUTH and verified TLS,
  no Redis public exposure, and Cloud Run timeout greater than stream lifetime.
  OpenAPI validation and an SSE contract test cover every public frame, ID
  rule, and reconnect instruction.

- [ ] **A8 (R1–R9):** In a disposable split-mode Google environment, first
  complete the still-pending PRD 009/010 Cloud Tasks and revision-drain proofs;
  then a real
  authenticated Invocation streams at least one live delta across Cloud Tasks
  and separate Cloud Run services, survives one forced stream reconnect, and
  finishes with transcript and terminal status matching JSON recovery. This
  cloud proof may remain unchecked at merge, but production readiness may not.

- [x] **A9 (R9):** API design, architecture, decision log, OpenAPI, and
  operator guidance state one consistent durable-versus-live replay contract
  and contain no remaining claim that Session streaming is undefined.

## Risks and open decisions

- Redis Pub/Sub deliberately cannot replay a missed delta. Clients that receive
  `stream.resync` must discard provisional output and wait for canonical state;
  adding a shared live-preview accumulator would be a separate PRD.
- Thinking deltas are included only when the provider/Dive normalized stream
  emits them and remain subject to the same ephemeral treatment as text. No raw
  provider metadata or signatures cross the public boundary. Partial-JSON
  deltas remain deferred until the ToolCall or structured-output PRD supplies a
  source and acceptance proof.
