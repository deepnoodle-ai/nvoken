# Cloud execution dispatch foundation

**Status:** Implemented; Google Cloud staging proof pending
**Sequence:** 009
**Depends on:** PRDs 002, 004, 006, and 008

## ELI5

Before real agent turns can run in a separate Cloud Run service, nvoken needs a
durable handoff that cannot lose work between Postgres and Cloud Tasks. This PRD
builds and proves that handoff with harmless synthetic work. Real Invocations
stay on the existing embedded runner until PRD 010.

## Why

Postgres and Cloud Tasks cannot commit atomically. Calling Cloud Tasks directly
after a database commit can lose a runnable transition when the process dies;
calling it before commit can deliver rolled-back work. Mobius Cloud's draft
request-bound execution design reaches the same conclusion: persist dispatch
intent with domain state, publish it independently, and let the executor reload
all authority from Postgres.

nvoken already has fenced Invocation ownership and bounded execution segments.
This slice adds the delivery foundation and Google Cloud topology without
combining that infrastructure change with the correctness-sensitive cutover of
real Invocation execution.

## Outcome

A committed synthetic dispatch converges through an authenticated Cloud Tasks
request to one durable synthetic settlement despite publisher crashes,
duplicate delivery, or process restarts. Operators can observe and reconcile
stuck handoffs. The same image can run as the public combined service or as a
private executor service with no public application routes.

## Scope

**In:** An internal `ExecutionDispatch` outbox; synthetic dispatch creation and
settlement; fenced publication; deterministic Cloud Tasks identities; stale
dispatch reconciliation and terminal retention; process-role isolation; and
the Cloud Run, Cloud Tasks, IAM, OIDC, concurrency, timeout, and observability
configuration needed to prove the path.

**Out:** Creating dispatches for real Invocations; changing the public API or
OpenAPI contract; replacing Postgres claims with Cloud Tasks; semantic model
retries; crash-resuming model work; tools; and non-Google queue adapters. PRD
010 owns Invocation routing and rollout between embedded and external modes.

## Requirements

- **R1 — Postgres-authoritative dispatch intent.** `ExecutionDispatch` must
  identify one work item and kind, carry only routing and publication state,
  and never duplicate business inputs or become an execution state machine.
  This slice uses a minimal durable synthetic work record with a unique work ID
  and atomic unsettled-to-settled transition. Creation of runnable task-routed
  work and its active dispatch must be one transaction. At most one `pending`,
  `publishing`, or `published` dispatch may exist for a work identity;
  successors use new dispatch IDs.

- **R2 — Fenced, recoverable publication.** A publisher must claim due
  dispatches with a renewable owner/attempt fence, create Cloud Tasks outside
  the database transaction, and mark publication only while holding that fence
  and while the row remains in the expected nonterminal publication state. A
  terminal dispatch must never move backward, even under a live publisher
  fence. Task names must derive from the immutable dispatch ID.
  `AlreadyExists` must converge as successful publication, including when the
  task was delivered before Postgres was marked `published`. Failed publication
  must return the dispatch to a retryable state with bounded diagnostic text
  and backoff.

- **R3 — Delivery is not ownership.** The private attempt endpoint must accept
  only a dispatch ID and an empty body, then load kind, work identity, scope,
  and eligibility from Postgres. A duplicate, settled, abandoned, missing, or
  permanently invalid dispatch must be an idempotent acknowledged no-op. Only
  the inability to reach a durable decision may request transport retry. The
  synthetic target must demonstrate one atomic durable settlement and no
  external side effect. Acknowledging a concurrent duplicate is safe only for
  this atomic synthetic target; PRD 010 must keep a real duplicate delivery
  retryable while another live attempt owns a long-running segment.

- **R4 — Reconciliation and finite history.** A periodic reconciler must expose
  aged pending and stale published dispatches. After a conservative age it must
  check the named Cloud Task: an existing task remains published; a missing task
  whose authoritative work is settled or not runnable settles the dispatch
  without a successor; a missing task whose authoritative work is safely
  replayable settles the old dispatch and atomically creates one successor.
  Terminal rows must be prunable after a configurable retention period without
  deleting active rows.

- **R5 — Isolated executor role.** The same binary must support a private
  executor process role that serves only health and the internal attempt route;
  it must not start the public API, embedded polling runner, publisher, reaper,
  dispatch reconciler, retention sweep, or migration logic. It must validate
  the exact schema before listening. The existing combined role must remain the
  default, run the dispatch background components, and continue running real
  Invocations unchanged. Shutdown must stop new executor requests, allow the
  Cloud Run grace window for in-flight requests, and leave any undecided
  delivery retryable.

- **R6 — Paved Google Cloud topology.** Terraform must provision one regional
  queue, a private internal-ingress executor Cloud Run service, and a dedicated
  OIDC caller identity with only executor invocation rights. The combined
  service may enqueue and inspect tasks and act as that identity; Cloud Tasks
  must attach its OIDC token. The executor must not be publicly invokable and
  may use request-based CPU with zero minimum instances because all of its work
  is request-bound. Tasks must target the stable executor service URL and
  audience. Queue concurrency must not exceed declared executor capacity, and
  the task/request deadline must leave the application settlement reserve
  established by PRD 008.

- **R7 — Operable and secret-safe.** Structured logs and metrics must make
  dispatch ID, kind, status, publication attempts, age, handler outcome, and
  reconciliation action diagnosable without recording bearer tokens, task
  bodies, model input, or secrets. Operator documentation must cover queue
  pause, retry/reconciliation behavior, retention, rollback while real
  Invocation routing is disabled, and a synthetic staging proof. The paved
  deployment must provide alerting for aged pending and stale published work,
  repeated task publication failure, and executor authentication rejection.

## Acceptance

- [x] **A1 (R1):** In Postgres tests, synthetic work and its dispatch commit or
  roll back together; concurrent creation produces one active dispatch; and
  active uniqueness plus all state constraints survive restart.

- [x] **A2 (R2):** Concurrent publishers produce one named task. Tests kill or
  fault the publisher before task creation, after task creation, and before the
  published commit; retry, expired-fence takeover, and `AlreadyExists` converge
  without two active dispatches or a stale publisher write. If the handler
  settles before the final publication commit, the publisher's later write is
  a no-op and the row remains terminal.

- [x] **A3 (R3):** A task delivered while the row is `pending`, `publishing`, or
  `published` settles the synthetic work once. Repeated and concurrent delivery,
  including delivery before the publisher's final commit, returns success with
  the same durable result. Missing/terminal poison deliveries are acknowledged;
  an injected database failure returns a retryable response and no false
  settlement.

- [x] **A4 (R4):** Reconciliation leaves an existing stale task alone, creates
  no successor when missing work is already settled, creates exactly one
  successor for missing safely replayable synthetic work, and emits
  aged-dispatch evidence. Retention removes only sufficiently old settled or
  abandoned rows in bounded batches.

- [x] **A5 (R5):** Route and process tests prove the executor exposes health and
  the dispatch attempt only, while the combined role's public routes and
  embedded Invocation execution remain unchanged. A shutdown test proves a
  completed handler persists before exit and an interrupted undecided handler
  remains eligible for delivery retry; role tests prove dispatch background
  components run only in the combined process.

- [x] **A6 (R6):** Terraform validation and plan inspection prove internal
  ingress, no unauthenticated executor invoker, dedicated OIDC identity, stable
  service URL/audience, explicit queue and service capacity, bounded deadlines,
  distinct combined/executor process roles, and alert policies for the required
  failure signals.

- [ ] **A7 (R1-R7):** In the Google Cloud staging procedure, creating one
  synthetic dispatch leads to an authenticated executor request and durable
  settlement; duplicate delivery is harmless, logs contain no sensitive input,
  and a new executor revision drains an in-flight request within configured
  bounds. Pausing the queue leaves Postgres intent recoverable and resuming it
  completes the dispatch.

## Risks and open decisions

- Cloud Tasks task-name deduplication is only a publication convergence aid;
  Postgres constraints and fences provide correctness.
- The synthetic exactly-once database transition does not claim exactly-once
  external effects. That stronger safety depends on later ToolCall identity and
  checkpoint work.
- Staging credentials and billable deployment are required for A7; automated
  tests and Terraform plans do not substitute for that proof.
