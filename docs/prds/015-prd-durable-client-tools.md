# Park Invocations for durable client tools

**Status:** Implemented
**Sequence:** 015
**Depends on:** `007-prd-recovery-and-transcript-reads.md`,
`008-prd-invocation-controls-and-budgets.md`,
`010-prd-cloud-tasks-invocation-execution.md`,
`011-prd-resumable-streaming.md`,
`012-prd-durable-toolcall-and-checkpoint-model.md`, and
`013-prd-structured-output.md`, and
`014-prd-checkpoint-crash-recovery.md`

## ELI5

An agent may ask the host application to run a tool and then go to sleep. The
host can reconnect, find the pending call, submit its result once, and let any
nvoken engine continue the same Invocation. This does not make nvoken call host
URLs; signed callback delivery belongs to PRD 016.

## Why

Host applications need to expose product data and user-side capabilities to an
agent without giving nvoken their credentials or keeping an HTTP connection or
engine goroutine alive. The durable ToolCall, transcript, checkpoint, fencing,
and crash-recovery spine now exists, so a client wait can be represented as an
ordinary committed boundary instead of a process-local suspension.

Mobius Cloud proves that a suspended turn can resume on another replica and
that partial multi-call results must survive another suspension. It also shows
the cost of a mutable transcript-checkpoint blob and separately managed waits.
nvoken will instead recover from canonical Session messages plus first-class
ToolCall and checkpoint rows, with one narrow result command.

## Outcome

An inline spec may declare client-executed tools. When the model selects one,
nvoken durably exposes the request and parks the Invocation in `waiting` without
an execution owner. Authenticated, batchable result submission records the
first result for each call and makes the same Invocation runnable again on any
engine.

## Scope

**In:** inline client-tool definitions; admission validation and fingerprinting;
Anthropic/OpenAI projection through Dive; fenced parking; authoritative pending
ToolCall reads; `POST /v1/invocations/{invocation_id}/tool-results`; atomic,
partial, and idempotent batch acceptance; wall deadlines and cancellation;
embedded wake-up and same-transaction Cloud Tasks dispatch; recovery, stream,
OpenAPI, and operational evidence.

**Out:** callback delivery, URLs, signing, egress, retries, or callback secrets;
generic Session event append; host steering; custom-tool resources or spec
references; client-side schema-result validation by nvoken; binary results;
per-tool deadlines; public retry/resume; SDK generation; cloud staging proof.

## Requirements

- **R1 — Immutable client-tool declarations.** `spec.tools` may contain at most
  32 ordered definitions with exactly `name`, `description`, `input_schema`,
  and `mode: "client"`. Names must be unique, contain 1–64 ASCII letters,
  digits, underscores, or hyphens, and may not use the `nvoken_` reserved
  prefix. Descriptions are required and bounded to 4,096 Unicode characters.
  Each input schema must be a nonempty object-root schema no larger than 32 KiB
  and use the bounded self-contained subset already accepted for structured
  output. Unknown modes and fields are rejected before durable writes. Client
  tools may coexist with `spec.output`; callback mode remains unsupported. A
  tools-bearing spec requires at least two model iterations; omission resolves
  to three or the lower installation maximum, matching structured output.

- **R2 — Tool declarations are admission identity.** New admissions must use
  fingerprint v4. It preserves v3 order and inserts `tools` after `output`;
  each ordered item encodes `name`, `description`, `mode`, then recursively
  canonical `input_schema`. Reordered schema members and equivalent JSON
  numbers are equal, while changing tool order or content conflicts. A
  tools-bearing request cannot replay a retained v1–v3 row; a tools-free
  request remains comparable by that row's recorded version. The exact spec
  snapshot, not mutable Agent configuration, drives every segment. The
  compatibility vectors live in `docs/design/admission-fingerprint-v4.json`.

- **R3 — Persist before park or exposure.** A selected client tool must pass
  through the existing model-checkpoint transaction: canonical assistant
  `tool_use` content, at most 32 stable nvoken ToolCall IDs in one model batch,
  request digests, usage receipt, and checkpoint commit under the current
  Invocation fence before any host can observe the call. After Dive reports
  suspension, settlement must atomically advance the Session lifecycle
  revision, accrue the active segment, settle an attached execution dispatch,
  clear owner/lease/segment fields, and move `running` to `waiting` through the
  same fence. Any still-open builtin sibling from that model batch is closed
  with canonical system-owned error evidence before parking. A crash after the
  model checkpoint but before parking lets a replacement park from that durable
  prefix without calling the model again. Parking failure leaves the owner
  responsible for retrying settlement; it must not expose a `waiting`
  Invocation without its requests or abandon a running lease early.

- **R4 — Waiting owns no compute.** A waiting Invocation has no goroutine,
  request, lease, execution deadline, or active segment. Waiting time counts
  against the Invocation wall-clock deadline but not active-execution time.
  Pollers and Cloud Tasks deliveries cannot claim it. Cancellation and wall
  deadline settlement close every open call with the existing canonical error
  result and remain first-terminal-wins. No result at or after its ToolCall or
  Invocation deadline may resume work.

- **R5 — Pending calls are recoverable public state.** Invocation get and
  Session get must expose pending client calls for the active Invocation in
  batch order with stable `id`, `name`, canonical `input`, and `deadline_at`.
  Scope comes from the authenticated Account and optional tenant constraint;
  cross-account, cross-tenant, wrong-Invocation, callback, and builtin calls are
  indistinguishable from not found. The canonical assistant request remains in
  Session messages and fixed-cut/SSE snapshots before the `waiting` lifecycle
  change; the pending projection is not a second content record.

- **R6 — One narrow, atomic result command.**
  `POST /v1/invocations/{invocation_id}/tool-results` accepts 1–32 results,
  each with a unique `tool_call_id`, one bounded JSON `content` value, and
  optional `is_error`. The encoded request is limited to 1 MiB and each content
  value to 256 KiB and 32 nesting levels. Under Session → Invocation → ToolCall
  locks, one transaction validates the whole batch, appends one canonical tool
  message for newly accepted blocks, settles those calls, and appends one tool
  checkpoint per call. Any invalid, mismatched, expired, or conflicting item
  rolls back the entire submission. Success returns `202` with
  `invocation_id`, `session_id`, the resulting Invocation `status`, an ordered
  `results` array containing `tool_call_id`, terminal call `status`, and
  `deduplicated`, plus the remaining `pending_tool_calls`. This is not a
  generic event endpoint.

- **R7 — First accepted result wins.** The first committed result for a
  ToolCall ID is immutable. An equal replay succeeds and reports deduplication
  without a second message, checkpoint, lifecycle revision, wake, or dispatch;
  a semantically changed replay returns `409 tool_result_conflict`. A batch may
  combine equal replays with new results. Partial batches leave the Invocation
  `waiting` while any client call from the current model batch remains open;
  hosts may submit calls in any order across batches. The ToolCall records
  whether a terminal result came from a client, builtin, or nvoken system
  settlement; only client-owned results participate in equality replay, so a
  cancellation or deadline error is never mistaken for host input. Durable
  messages retain submission order, while the engine projection must coalesce
  and reorder current-batch result blocks into original model batch order for
  Dive.

- **R8 — Resume is a durable queue transition.** When a result transaction
  closes the final open client call, it must also move the same Invocation from
  `waiting` to `queued`, append a lifecycle revision through the accepted tool
  message, and preserve every checkpoint and usage receipt. Embedded mode sends
  only a post-commit wake. Cloud Tasks mode creates the successor Invocation
  dispatch in that transaction before returning `202`; publication may happen
  later. Any engine may claim the queued row, validate the durable prefix, feed
  the accepted results back to the model, and park again or settle normally.

- **R9 — Stable errors and bounded operations.** Malformed requests return
  `400 invalid_request`; undisclosable resources return `404 not_found`; a
  non-waiting Invocation with an unresolved requested call returns
  `409 invocation_not_waiting`; changed replay returns
  `409 tool_result_conflict`; a submission at/after the durable deadline,
  including after deadline settlement, returns `409 tool_result_expired`;
  transient storage failure returns `503` without acknowledgement. A call
  closed by cancellation or another non-deadline terminal settlement returns
  `invocation_not_waiting`, not a comparison against nvoken's synthetic
  result. Structured logs distinguish park, partial result, resume
  enqueue, deduplication, stale fence, cancellation, and deadline using IDs and
  counts only—never tool inputs/results, schemas, prompts, or secrets.

## Acceptance

- [x] **A1 (R1, R2):** Strict admission tests accept 32 unique client tools at
  every stated boundary and reject the next count/size/depth, reserved or
  duplicate names, callback mode, unknown fields, invalid schemas, and a
  one-iteration tools request without writes. Omitted iterations resolve to
  three. `docs/design/admission-fingerprint-v4.json` fixtures prove schema
  canonicalization, ordered-tool materiality, equal replay, changed replay
  conflict, and v1–v3 compatibility.

- [x] **A2 (R3–R5):** A scripted model requests two client tools. Postgres and
  transcript/SSE reads expose both stable nvoken IDs and inputs before a
  `waiting` revision; the Invocation then has no lease, owner, execution
  deadline, active segment, running goroutine, or active dispatch. Restarting
  every process leaves the same pending projection readable.

- [x] **A3 (R6, R7):** Partial acceptance of one call appends one message and
  checkpoint and remains waiting. A later batch containing its equal replay
  plus the second result appends only the second evidence and queues once.
  Concurrent equal submissions converge; changed, duplicate-ID, cross-scope,
  wrong-mode, oversized, too-deep, and mixed valid/invalid batches commit
  nothing and return the documented stable code. Every success asserts the
  `202` acknowledgement, per-item deduplication, resulting status, and
  remaining pending projection.

- [x] **A4 (R4, R7–R9):** Cancellation and wall-deadline races against result
  acceptance yield exactly one winner. A winning terminal transaction closes
  all calls and never queues; a winning valid result transaction queues once.
  Waiting time does not increase active execution. Results at/after deadline
  return `tool_result_expired`; post-cancellation results return
  `invocation_not_waiting`. Neither is accepted, and stale engine or delivery
  attempts cannot append or resume.

- [x] **A5 (R3, R7, R8):** After the final result, a different engine process
  reconstructs the provider-valid transcript, continues with cumulative usage
  and iteration budgets, and either completes or parks on another client batch.
  A crash after a committed client-tool model checkpoint but before parking
  parks on the replacement owner without another provider call. Crashes after
  result message, call settlement, lifecycle, dispatch creation, and
  acknowledgement converge without duplicate model-visible results or a lost
  runnable Invocation.

- [x] **A6 (R6, R8):** Embedded integration wakes only after commit. Cloud
  Tasks integration proves result evidence, `waiting → queued`, lifecycle
  revision, and successor dispatch are one transaction; delayed publication,
  `AlreadyExists`, duplicate delivery, and reconciler repair still produce one
  next claim and one active dispatch at most. A lost embedded wake converges
  through the existing Postgres polling fallback.

- [x] **A7 (R1–R9):** `make check` and the full Postgres suite pass. OpenAPI,
  `docs/design/api.md` sections 2/4, architecture, recovery/stream docs, and
  examples describe the declarations, pending projection, result command,
  status transitions, errors, deadline semantics, and transcript authority.
  `docs/design/decisions.md` records the declaration, fingerprint-v4, and
  command decisions. Logs contain no client-tool schema, input, or result
  payloads.

## Risks and open decisions

- Providers differ in validation and ordering of parallel tool results. nvoken
  preserves model batch order and must coalesce consecutive durable tool-result
  messages when necessary rather than weakening its append-only record.
- The result command validates shape and bounds, not business correctness. The
  host owns tool execution and may deliberately return `is_error: true` so the
  model can recover.
- Callback tools reuse this parked-Invocation path only after PRD 016 adds
  durable delivery identity, signing, egress controls, and retry receipts.
