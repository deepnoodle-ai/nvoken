# Run Invocations in request-bound Cloud Tasks executors

**Status:** Implemented; cloud proof pending
**Sequence:** 010
**Depends on:** PRDs 005, 008, and 009

## ELI5

Google Cloud deployments should run each agent turn inside a private Cloud Run
request so normal deploys can drain active work. Postgres still decides who may
execute and whether the turn finished; Cloud Tasks only delivers the request.
This PRD routes existing generation-only turns through that path, but does not
add checkpoints, crash replay, streaming, or tools.

## Why

PRD 009 proved the Postgres outbox, task publisher, private executor, and
delivery crash windows with synthetic work. Real Invocations still run in a
background goroutine in the combined API process, which Cloud Run cannot see as
an active request after admission returns. A routine revision replacement can
therefore interrupt a long turn and leave the lease reaper to record
`execution_lost`.

Mobius Cloud provides useful but incomplete precedent. Its implemented
`agent_turn` dispatcher uses queued rows, renewable leases, cancellation, and a
reaper, while its July 2026 Cloud Tasks design remains a draft. The durable
lesson is to put one bounded, exact-claimed segment inside the task request and
keep transport retry separate from model semantics. nvoken can adopt that seam
without Mobius's multiple turn origins, waits, tools, or loop progression.

## Outcome

An nvoken installation can select embedded or Cloud Tasks Invocation execution
without changing the public Runtime API. The Google Cloud paved path defaults
to Cloud Tasks: admission commits a runnable Invocation and dispatch intent
together, the private executor reloads and claims the exact Invocation from
Postgres, and it acknowledges delivery only after a durable terminal or no-op
decision.

## Scope

**In:** Generation-only Invocation dispatch intent; exact request-bound claim,
heartbeat, generation, cancellation, and terminal settlement; executor provider
credentials; an independently running lease/deadline reaper; explicit execution
mode configuration; missing-task reconciliation; Google Cloud defaults,
capacity bounds, rollout, rollback, logs, and staging proof.

**Out:** Public API changes; ToolCalls or waiting/resume transitions;
checkpoint-and-chain; replay after uncertain model execution; SSE/Redis;
semantic model retries beyond existing provider behavior; additional queues or
non-Google deployment automation.

## Requirements

- **R1 — Explicit topology with identical public semantics.** Invocation
  execution mode must be an installation setting with exactly `embedded` and
  `cloud_tasks` values. Local/self-hosted defaults remain embedded. The paved
  Google Cloud path must default to `cloud_tasks`, which must fail startup when
  its queue or OIDC configuration is incomplete. The explicit mode replaces
  inference from the presence of Cloud Tasks settings. Admission
  acknowledgements, reads, cancellation, transcript records, lifecycle states,
  and failure codes must not expose or vary by delivery topology.

- **R2 — Atomic runnable intent.** In `cloud_tasks` mode, the transaction that
  first admits a queued Invocation must also insert exactly one active
  `invocation` ExecutionDispatch containing the authoritative Account, tenant
  partition, Invocation ID, queue, and availability time. A rollback leaves
  neither record claimable; an equal idempotent replay creates no second
  dispatch. In `embedded` mode admission must not create an Invocation dispatch.

- **R3 — One exact, Postgres-authorized request.** The private endpoint must
  continue accepting only a syntactically valid dispatch ID and an empty body.
  For an Invocation dispatch it must load the immutable dispatch and all model
  inputs, scope, limits, and state from Postgres, acquire the existing exact
  Session-before-Invocation claim and new lease attempt, and execute at most one
  bounded generation segment. Cloud Tasks headers, task names, bodies, and
  delivery attempts never grant authority or select tenant work.

- **R4 — Existing execution controls remain authoritative.** Request-bound
  execution must use the same heartbeat, lease fence, cancellation fallback,
  wall-clock deadline, active-execution accrual, output-token/cost/iteration
  budgets, transcript append, and first-terminal rules as embedded execution.
  A task retry must not replenish a logical budget. A stale or cancelled owner
  may finish local computation but cannot append output or settle.

- **R5 — Durable acknowledgement and transport-only retry.** The endpoint must
  return `204` only after one of these durable decisions: Invocation result and
  its dispatch settle atomically; an already-terminal/missing/permanently
  ineligible Invocation leaves the dispatch terminal; or the dispatch was
  already terminal. A live duplicate owner or infrastructure/settlement
  uncertainty must return `503` with bounded `Retry-After`. This includes a
  running Invocation whose lease expired but whose reaper decision has not yet
  committed; delivery must not reclaim it. Provider or model failure is a
  semantic terminal result and returns `204` after commit, never a request for
  Cloud Tasks to repeat generation.

- **R6 — Duplicate delivery and failure recovery.** Concurrent delivery of one
  dispatch must produce at most one generation call. A response lost after
  commit must become an acknowledged no-op on redelivery. If an executor dies
  after claim, a control-plane reaper independent of embedded claiming must
  retain the pre-checkpoint policy: terminalize the expired Invocation as
  `execution_lost`, settle its active dispatch, and never replay the uncertain
  model call. Missing-task reconciliation may create a successor only for an
  Invocation still safely queued; terminal work settles without a successor,
  and live/uncertain running work is retained for the reaper. Cloud mode must
  also periodically create a dispatch for any queued Invocation that has none,
  covering work admitted by an embedded revision during enablement; active
  uniqueness and row locking must make the repair idempotent.

- **R7 — No competing schedulers and safe rollback.** In `cloud_tasks` mode the
  combined process must publish/reconcile dispatches and reap leases/deadlines
  but must not poll-claim Invocations. The executor process must serve private
  attempts only and must not poll, publish, reconcile, migrate, or expose public
  routes. In `embedded` mode the existing polling runner remains the sole normal
  scheduler. During a mode change, queued tasks and the embedded runner may
  race, but the exact Postgres claim must permit one owner and stale deliveries
  must converge without a second generation.

- **R8 — Paved executor capacity and secrets.** The Google deployment must give
  provider secrets to the process role that performs generation, keep executor
  ingress/IAM and queue-scoped permissions from PRD 009, and bound queue
  concurrency by executor instance/request and database capacity. The model
  execution cutoff must leave a configured settlement reserve inside both the
  executor attempt timeout and the 1,800-second Cloud Tasks/Cloud Run deadline.
  Executor scale-to-zero remains valid because all executor work is
  request-bound. The combined service must retain instance-based CPU and a
  nonzero minimum while publication, reconciliation, dispatch repair, and
  lease/deadline reaping remain background work.

- **R9 — Operable rollout evidence.** Structured logs must identify dispatch,
  Invocation, lease attempt, handler outcome, terminal reason, and settlement
  margin without prompts, model output, credentials, or idempotency material.
  Sustained executor `503` responses and Cloud Tasks queue backoff must be
  alertable because repeated retry responses can reduce delivery for unrelated
  tasks on the same queue.
  Deployment guidance must cover enablement, queue pause/resume, draining,
  rollback to embedded mode, and the continuing `execution_lost` limitation.
  Cloud staging must prove authenticated generation, duplicate delivery,
  cancellation, capacity saturation, revision drain, and abrupt-loss behavior.

## Acceptance

- [x] **A1 (R1, R2):** Postgres tests admit the same request in each mode.
  Embedded mode has no Invocation dispatch; Cloud Tasks mode has one queued
  Invocation and one correctly scoped active dispatch from the same commit.
  Injected failure at dispatch insertion rolls back all admission writes, and
  an equal replay retains one dispatch.

- [x] **A2 (R3, R4, R5):** A task delivery carrying only the dispatch-ID path
  and an empty body reloads the snapshot and transcript, exact-claims the
  Invocation, records one running revision, performs one generation, and
  atomically commits the assistant message, usage/provenance, terminal revision,
  and settled dispatch before returning `204`.

- [x] **A3 (R4, R5, R6):** Twenty concurrent deliveries for one dispatch call
  the generator once. Live losers receive retryable `503`; delivery after the
  winner commits returns `204` without another message, state, usage record, or
  generation call. A semantic provider failure durably fails the Invocation and
  settles the dispatch while returning `204`.

- [x] **A4 (R4, R5, R6):** Cancellation racing generation wins the existing
  terminal fence, stops the request through notification or lease-renewal
  fallback, and leaves no assistant output. Injected settlement failure returns
  retryable `503` with neither a false terminal dispatch nor an unfenced write.
  Delivery against an expired but unreaped running lease also returns `503`. A
  lost request is reaped to `execution_lost`; its later delivery returns `204`
  with no repeated model call.

- [x] **A5 (R6):** Reconciliation tests show that an absent task for queued work
  atomically settles the old dispatch and creates one successor; terminal work
  settles without a successor; and running work with a live or uncertain lease
  is not replayed. Switching from embedded to cloud mode repairs a queued
  Invocation with no dispatch exactly once. Publisher crash/`AlreadyExists`
  behavior remains unchanged.

- [x] **A6 (R7):** Role and concurrency tests prove cloud mode starts the public
  API, publisher/reconciler, and reaper without an embedded claimant; embedded
  mode starts its polling runner; and the executor starts only the private
  request surface. A forced embedded/task race yields one claim and one model
  call, then leaves the stale dispatch terminal.

- [x] **A7 (R8, R9):** Terraform plan tests prove Cloud Tasks is the paved
  default, provider-secret access follows the generating role, queue capacity
  cannot exceed declared executor capacity, and timeout nesting preserves the
  settlement reserve. The combined service retains minimum capacity and
  instance CPU for its background duties. Deployment documentation gives exact
  enable, rollback, pause/resume, and diagnostic procedures.

- [ ] **A8 (R1–R9):** `make check`, Postgres integration tests, and Terraform
  validation pass. In the `nvoken` Google project, an authenticated real
  Invocation runs on the executor; duplicate delivery and cancellation are
  observable; a revision replacement drains a held request; an abrupt kill
  ends as `execution_lost`; and saturating configured concurrency queues excess
  work rather than exceeding executor capacity.

## Risks and open decisions

- Staging proof requires billable Cloud Run and Cloud SQL resources and remains
  unchecked until an explicitly authorized deployment is running.
- PRD 010's cloud proof exercises and therefore subsumes PRD 009's pending
  synthetic foundation proof; it is not a reason to skip the real Invocation
  drain and abrupt-loss cases here.
- This slice deliberately prefers visible `execution_lost` over replaying an
  uncertain provider call. PRD 014 changes that only after durable checkpoints
  and replay-safe identities exist.
