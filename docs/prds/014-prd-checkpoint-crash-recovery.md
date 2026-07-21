# Resume Invocations after engine loss

**Status:** Implemented
**Sequence:** 014
**Depends on:** `004-prd-engine-claims-and-fencing.md`,
`008-prd-invocation-controls-and-budgets.md`,
`010-prd-cloud-tasks-invocation-execution.md`,
`012-prd-durable-toolcall-and-checkpoint-model.md`, and
`013-prd-structured-output.md`

## ELI5

If an nvoken engine disappears, another engine should continue the same
Invocation from its last saved model or tool boundary. Saved work is reused,
while work that finished outside Postgres but was never checkpointed may run
again. This does not add a public retry button, client tools, or arbitrary
mid-request snapshots.

## Why

Today an expired execution lease becomes the terminal `execution_lost` failure,
even when the transcript, ToolCalls, usage receipts, and checkpoint cursor show
exactly where execution can continue. PRD 012 deliberately created that durable
evidence before changing the failure policy. This slice consumes it so process
loss and routine revision replacement no longer discard a valid turn.

Mobius Cloud supplies useful caution rather than a complete implementation. Its
current turn reaper still fails an expired running turn, while its suspend/resume
paths depend on a mutable checkpoint blob and have accumulated transcript,
usage, and duplicate-continuation hazards. nvoken instead resumes only from its
append-only transcript and first-class checkpoint, ToolCall, and receipt rows.

## Outcome

An expired Invocation owner is fenced out and the same Invocation becomes
claimable again. A replacement engine validates and reuses its committed prefix,
continues at the next incomplete model or builtin boundary, and reaches one
ordinary terminal result in embedded and Cloud Tasks execution modes.

## Scope

**In:** expired-lease recovery; active-segment accrual; queued lifecycle
revision; transcript/checkpoint validation; checkpointing tool-free model
responses; cumulative receipt replay; pending/running builtin recovery; stable
ToolCall reuse; structured-output recovery; existing-dispatch redelivery;
crash-boundary tests and operational logs.

**Out:** public retry or resume endpoints; resuming terminal Invocations;
callback/client tools; arbitrary provider-process snapshots; cooperative
checkpoint-and-chain at the intentional execution-segment ceiling; exactly-once
model requests or provider charges; external-side-effect reconciliation; replay
after retention or corrupt evidence.

## Requirements

- **R1 — Expired ownership becomes queued work.** When a `running` Invocation's
  lease expires while wall-clock and active-execution budget remain, the reaper
  must atomically accrue the abandoned segment once, clear ownership and segment
  fields, advance the Session lifecycle revision, and move the same Invocation
  to `queued`. Accrual ends at the earlier recorded lease expiry or execution
  deadline, never at a later reaper observation. An elapsed segment-scoped
  deadline does not turn an abandoned owner into a logical timeout; a healthy
  owner's deliberate segment cutoff still settles normally. Recovery must
  retain transcript, checkpoint, receipt, ToolCall, output-contract, and
  dispatch evidence and must not close open ToolCalls or write a new
  `execution_lost` result. Cancellation, wall/active deadline exhaustion, or an
  existing terminal result continues to win and never requeues. Public reads
  and streams may therefore observe `running → queued → running`; lifecycle
  revision, not presumed status monotonicity, orders those transitions.

- **R2 — Recovery is fenced like first execution.** A replacement claim must
  increment `lease_attempt`; every checkpoint, ToolCall result, renewal, and
  terminal write remains conditional on that new fence. The expired owner may
  finish local work but cannot append or settle. Concurrent reapers and claims
  must converge on one queued transition and one next owner without weakening
  the Session-before-Invocation lock order.

- **R3 — Postgres defines the resumable prefix.** Before doing new model or
  builtin work, the engine must load the Invocation's latest checkpoint,
  checkpoint-bounded Session transcript, ToolCalls, and usage receipts and
  verify their scope, monotonic cursors, message watermarks, and required
  request/result pairs. It must reconstruct provider input from canonical
  `SessionMessage` content, never a provider envelope or process snapshot.
  Missing or inconsistent recovery evidence must fail the Invocation with the
  existing bounded public `internal` error and internal reason class
  `recovery_invalid` rather than loop, guess, or skip content. The public
  `execution_lost` code remains readable for retained historical rows but is no
  longer written for recoverable lease expiry.

- **R4 — Every accepted model boundary is reusable.** All production model
  responses, including a tool-free final assistant response, must commit their
  assistant message, normalized receipt, and model checkpoint before tool
  execution or terminal settlement. Recovery after that commit must reuse the
  recorded response: a final model checkpoint settles without another provider
  call, while a tool-request checkpoint continues with its saved ToolCall IDs.
  If the engine dies after a provider response but before that transaction
  commits, the provider request may run again; no uncommitted response or charge
  may be presented as durable.

- **R5 — Builtins resume by stable ToolCall identity.** A pending builtin must
  start from its existing ToolCall. A builtin left `running` by the expired
  owner must terminalize that old attempt and create a new attempt bound to the
  replacement Invocation lease without changing the ToolCall ID or immutable
  request. A previously accepted result must be reused without running the
  builtin again. The deterministic test builtin and reserved structured-output
  builtin must both satisfy this path; callback and client modes remain
  non-runnable.

- **R6 — Receipts and budgets span owners.** Recovery must initialize iteration
  and aggregate usage from accepted receipts, continue at the next iteration,
  and apply output-token, cost, iteration, wall-clock, active-execution, and
  segment checks to cumulative evidence before more work. Receipt uniqueness
  and equality rules remain unchanged. Any terminal usage projection must equal
  the sum of accepted receipts, regardless of how many owners participated.

- **R7 — Delivery remains topology-neutral.** Embedded polling must claim the
  requeued row normally. In `cloud_tasks` mode a retry of the existing task is
  the fast path. If that delivery is exhausted or stale, the dispatch reconciler
  must atomically settle the old active dispatch and create a successor intent
  for the still-queued Invocation. Recovery requires no new public request, and
  neither an original nor successor task grants ownership. Duplicate or late
  deliveries must converge through Invocation and dispatch fences, and the
  executor returns success only after a durable no-op or terminal settlement.

- **R8 — Recovery is observable and bounded.** Structured logs must distinguish
  lease recovery, resumed model iteration, resumed builtin attempt, terminal
  replay, stale-owner rejection, and invalid evidence using IDs, attempts,
  cursor values, and reason classes only. They must not contain prompts,
  transcript content, schemas, ToolCall payloads, or provider secrets. Recovery
  uses the existing reaper batch limit and claim concurrency controls.

## Acceptance

- [x] **A1 (R1, R2):** An expired running lease with zero, model, or tool
  checkpoints transitions once to `queued`, accrues from segment start through
  the earlier lease/execution deadline exactly once, publishes one lifecycle
  revision through the latest durable message, and is claimed with the next
  lease attempt. This remains true when the segment deadline elapsed before the
  lease; wall or active exhaustion remains terminal. Twenty concurrent
  reapers/claimers produce one replacement owner. The old owner loses renewal,
  checkpoint, ToolCall-result, and settlement races.

- [x] **A2 (R1, R3, R4):** Killing an engine before its first model checkpoint
  causes a replacement provider call. Killing it after a committed tool-free
  final model checkpoint causes no second provider call; the replacement
  validates the stored prefix and settles the original Invocation with that
  message and receipt.

- [x] **A3 (R2, R4, R5):** At each tool boundary—request checkpoint committed,
  attempt started, result committed, and next model checkpoint committed—a
  forced lease loss resumes with the same ToolCall ID. Only the incomplete
  deterministic builtin attempt may run again; an accepted result and its tool
  message are never duplicated, and the final transcript remains provider-valid.

- [x] **A4 (R3–R6):** Structured-output recovery handles an invalid submission
  followed by correction, an accepted submission before final text, and a final
  checkpoint before terminal commit. It preserves first-valid-wins provenance,
  does not rerun an accepted submit call, and exposes output only in completed
  settlement. Iteration and usage projections equal the unique receipts across
  owners and cumulative budget exhaustion remains terminal.

- [x] **A5 (R1–R3, R6):** Cancellation and wall/active deadline races beat
  recovery and close open calls through the existing terminal transaction. A
  healthy owner reaching its model cutoff settles the existing segment-scoped
  deadline failure; an owner whose lease subsequently expires is recovered per
  A1. A forged, missing, cross-scope, ahead-of-cursor, or message-mismatched
  recovery prefix fails once with public `internal` and logged
  `recovery_invalid`; it neither invokes a provider nor becomes claimable again.

- [x] **A6 (R1, R2, R7):** Embedded and exact Cloud Tasks integration tests
  kill the executor at the model, tool-start, tool-result, and terminal-commit
  windows. Polling, original-task redelivery, and stale-dispatch reconciliation
  to one successor each reclaim the same Invocation. Concurrent original and
  successor deliveries do not double-claim; the original converges to a no-op,
  the active successor settles with the final Invocation, and no recoverable
  crash ends as `execution_lost` or a segment-scoped deadline failure.

- [x] **A7 (R1, R3, R8):** `make check` and the full Postgres integration suite pass.
  Logs and documentation state the checkpoint guarantee and its uncertainty
  window without payload leakage. OpenAPI and API/stream documentation describe
  the recovery-visible status regression, retain `execution_lost` only for
  historical compatibility, and keep corrupt evidence under public `internal`.
  The design claims, architecture, decision log, operator guide, and PRD roadmap
  distinguish crash recovery from public retry, external-effect safety, and
  intentional segment chaining.

## Risks and open decisions

- A model call that completed but did not checkpoint may be repeated and billed
  twice. This slice makes that boundary explicit; provider-specific request
  idempotency is not assumed.
- Recovery currently has only side-effect-free builtins. PRD 016 must add
  delivery receipts and host idempotency before callback effects can rely on the
  same reclaim policy.
- A corrupt durable prefix fails visibly because guessing is more dangerous than
  losing progress. Automated evidence repair is outside the launch arc.
