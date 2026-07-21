# Invocation controls and bounded execution

**Status:** Complete
**Sequence:** 008
**Depends on:** PRDs 004, 005, and 007

## ELI5

A host can stop an Invocation and put clear limits on how long and how much
model work it may consume. Postgres decides whether cancellation, success, or a
limit wins, while every executor cooperatively stops and stale work is fenced
out. This does not add tools, checkpoint resume, Cloud Tasks routing, or SSE.

## Why

nvoken can durably execute generation-only turns, but hosts cannot cancel work
or bound its time and model consumption. The next Cloud Tasks slice also needs
an execution segment that ends early enough to persist its outcome.

Mobius Cloud's useful precedent is idempotent first-writer-wins cancellation,
durable active-time segments, and fenced timeout settlement. nvoken applies
those invariants without importing Mobius waits, jobs, or turn/run records.

## Outcome and scope

Hosts can call `POST /v1/invocations/{invocation_id}/cancel`, and an inline
execution spec may carry optional `budgets` for wall-clock time, active
execution time, output tokens, estimated model cost, and model iterations.
Reads expose resolved limits, committed active time, and the wall deadline.
The same semantics hold in embedded and future split execution.

Out of scope: automatic retry, provider preauthorization, billing, tools,
checkpoints, segment continuation, Cloud Tasks dispatch, streaming, and a
synchronous guarantee that the provider has stopped when cancellation returns.

## Requirements

- **R1 — Idempotent scoped cancellation.** `POST
  /v1/invocations/{invocation_id}/cancel` must authenticate and authorize the
  Invocation's Account and tenant scope. Under Session-before-Invocation lock
  order, a nonterminal row must atomically become immutable `cancelled`, clear
  its lease, accrue its active segment, append one lifecycle revision, and free
  the Session. The response is `200` with the authoritative row. Cancelling any
  terminal row returns it unchanged without another revision.

- **R2 — Prompt cooperative stop with a durable fallback.** After a committed
  cancellation, a coalescable PostgreSQL `LISTEN/NOTIFY` wake must tell the
  runner holding that exact claim to cancel its local executor. The wake grants
  no authority and may be lost; renew and settle still reject the terminal row.
  A repeated cancellation may republish the wake without changing state.

- **R3 — Explicit, validated budget contract.** `spec.budgets` may specify
  positive `wall_clock_timeout_seconds`, `active_execution_timeout_seconds`,
  `max_output_tokens`, `max_estimated_cost_usd`, and `max_iterations`.
  Defaults bound time and iterations; token and cost limits are absent unless
  requested. Resolved values persist at admission, while reads omit absent
  limits. Installation maxima and fixed safety limits reject rather than clamp;
  USD accepts at most six decimal places. Requested values are idempotency
  input, including explicit-default versus omitted. Legacy budgetless records
  must still deduplicate only budgetless replays.

- **R4 — Durable logical time accounting.** Admission must persist all resolved
  limits and wall deadline. Queue time consumes wall time only. Each claim
  starts a durable active segment whose deadline is the earliest wall remainder,
  active-time remainder, or installation segment ceiling. Cancel, settle, and
  reap accrue the segment once. Independently indexed reaper scans must cover
  queued wall deadlines and running logical/segment deadlines; deadlines are
  not leases.

- **R5 — Segment ceiling reserves settlement time.** The runner must stop model
  execution before its stored deadline by a positive reserve while heartbeats
  continue through settlement. Wall, active, and segment expiry settle `failed`
  with `deadline_exceeded` and bounded scope detail. Until checkpoints exist,
  segment exhaustion is terminal. The reaper provides the crash fallback;
  lease-only expiry remains `execution_lost`.

- **R6 — Enforce generation budgets without false guarantees.** The output-token
  limit must reach Dive before the call. Each model request consumes an
  iteration, and none may start after its time or iteration limit. Before
  assistant commit, normalized usage must satisfy token and estimated-cost
  limits. A breach fails with `budget_exceeded`, discards assistant output, and
  retains paired usage/provenance; schemas and validators must permit the same
  evidence on post-response deadline failure. Pre-response failure and
  cancellation retain neither. Missing cost evidence under a cost limit fails
  closed. Cost is a post-call guardrail, not preauthorization.

- **R7 — One atomic terminal race.** Success, semantic failure, cancellation,
  deadline, budget failure, and reaping must use Session-before-Invocation lock
  order and one transaction for allowed transcript output, the current row,
  and lifecycle revision. The first terminal commit wins; stale owners write
  nothing; rollback restores the prior durable state.

- **R8 — Recovery and operational evidence.** Invocation get/list and OpenAPI
  must expose public budgets, accrued active milliseconds, wall deadline,
  terminal evidence, and typed error—not lease or internal segment fields.
  Bounded logs must distinguish cancellation request/wake, deadline scope, and
  budget kind without request content or credentials.

- **R9 — Contract artifacts move together.** The implementation must update
  OpenAPI, API design, and decisions for cancellation, budgets, failure
  evidence, and fingerprint evolution. Language-neutral v2 fixtures must add a
  budget case while preserving every v1 digest.

## Acceptance

- [x] **A1 (R1, R7):** Concurrent cancellation, successful settlement, and
  provider failure produce exactly one terminal row and lifecycle revision.
  Cancellation that wins returns `cancelled`, appends no assistant output, and
  immediately permits a new Invocation on the Session; repeated cancellation
  returns the same row without another revision.

- [x] **A2 (R1, R2):** A running model double on another service instance
  observes the committed PostgreSQL cancellation wake and stops promptly. With the wake dropped, its
  next renewal or settlement loses the fence, it cannot write, and the durable
  Invocation remains cancelled. Cross-Account and conflicting tenant
  credentials receive `404` without signalling the target.

- [x] **A3 (R3, R4):** Omitted budgets resolve to documented installation
  defaults; valid lower budgets persist and appear on get/list after restart;
  zero, negative, non-finite, over-precision, or excessive values are rejected
  before admission. Equal retries deduplicate, changed budgets conflict, and a
  legacy budgetless fingerprint still deduplicates only a budgetless replay.

- [x] **A4 (R4, R5):** Controlled-clock tests prove queued time consumes only
  wall clock, repeated active segments cannot replenish their allowance, and
  the earliest logical/segment limit wins. Work finishing just before the model
  cutoff settles normally; blocking work is cancelled with enough time for a
  fenced terminal write before the stored segment deadline.

- [x] **A5 (R5, R7):** After process loss, the reaper converts expired queued
  and running logical deadlines to one `deadline_exceeded` failure and an
  expired segment to the scoped deadline failure; a renewed lease or competing
  terminal write is preserved. Lease-only expiry remains `execution_lost`.

- [x] **A6 (R6, R7):** Deterministic Anthropic and OpenAI adapter tests receive
  the requested output-token ceiling and report one iteration. Token or cost
  breach, and unavailable cost evidence under a cost limit, commit no assistant
  message but retain normalized usage/provenance with `budget_exceeded`.
  Deadline failure retains evidence only when a provider result produced it;
  cancellation and pre-response failure do not fabricate evidence.

- [x] **A7 (R7):** Postgres fault injection across cancellation, deadline, and
  budget settlement rolls back the row, active accounting, transcript, and
  lifecycle together. A stale attempt and an executor returning after cancel
  cannot commit any subset.

- [x] **A8 (R8, R9):** JSON recovery and captured logs provide limits,
  consumption, deadline scope, IDs, and terminal reason needed to diagnose each
  control path. Searches find no fixture prompt, message content, credentials,
  provider payload, lease owner, or internal segment deadline in the public
  contract or operational logs. OpenAPI, the API design, decision log, and v1
  plus v2 fingerprint fixtures agree with the shipped contract.

## Risks and sequencing notes

- Provider-estimated cost is useful guardrail evidence, not authoritative
  billing or a reservation. A later metering slice may add a stricter ledger.
- If fenced settlement exhausts its reserve, the reaper records an
  evidence-free deadline failure because uncommitted provider evidence is not
  durable. Operators should watch this signal before tuning the reserve.
- PRD 009 builds dispatch intent on these durable controls. PRD 010 configures
  the segment ceiling below Cloud Tasks/Cloud Run request limits. PRD 014 can
  replace terminal segment exhaustion with checkpoint-and-chain continuation.
