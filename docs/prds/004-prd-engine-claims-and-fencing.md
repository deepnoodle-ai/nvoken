# Claim and Fence Durable Invocation Execution

**Status:** Complete

**Sequence:** 004

**Depends on:** `001-prd-runtime-record-and-lifecycle-contract.md`,
`002-prd-postgres-runtime-spine.md`,
`003-prd-durable-invocation-admission.md`

## ELI5

Accepted turns are rows in Postgres, not promises held by a web process. An
engine must win a short-lived claim before doing work, and an old engine cannot
save anything after losing that claim. This PRD builds and proves that safety
loop with fake execution; real model generation comes next.

## Why

PRD 003 can durably admit an Invocation but intentionally leaves it queued.
Before any provider call, nvoken needs one ownership contract that works for
both in-process polling and the later Cloud Tasks delivery path.

Mobius Cloud proves the core shape across its durable-work seams: durable rows
remain authoritative, wake signals only reduce polling latency, heartbeats keep
live work owned, and a reaper makes dead work visible. Its agent-turn settlement
is not attempt-fenced, however. nvoken deliberately closes that gap and avoids
Mobius's parallel implementations by putting exact-Invocation claim, renewal,
and settlement behind one service rather than each dispatcher.

## Outcome

A bounded engine can discover or be handed one durable Invocation, atomically
claim it, keep the claim alive, and settle it only while its fence is current.
Queued work survives restarts, duplicate delivery cannot create a second owner,
and a lost running engine becomes a typed failure instead of wedging its
Session.

## Scope

**In:** Invocation lease fields and indexes; exact and next-queued claim paths;
lease attempts, renewal, fenced settlement, lifecycle states, in-process wake
signals with polling fallback, a bounded engine runner, joined shutdown, an
expired-lease reaper, redacted operational logs, and integration proof with an
injected deterministic executor.

**Out:** Model or tool execution; transcript output; checkpoint/resume;
cancellation and limits; public API changes; Cloud Tasks, dispatch outbox, or
private executor HTTP protocol; retrying a semantically failed execution. The
production daemon must not run the synthetic executor or claim work until PRD
005 supplies the real generation executor.

## Requirements

- **R1 — Invocation is the queue.** A committed `queued` Invocation must remain
  eligible until claimed; no transient signal, goroutine, or delivery record
  may be required for correctness. Claims must select queued work in stable
  creation order with concurrent-safe skipping and must atomically transition
  one row to `running`, increment its positive lease attempt, stamp a bounded
  owner and expiry, and append the matching lifecycle revision. Every lifecycle
  transition must lock the Session before the Invocation, allocate its revision
  from the Session counter, and commit the current row plus append-only state
  together. A queued row has no lease; a running row has exactly one owner,
  expiry, and positive attempt; terminal rows retain the attempt for audit but
  hold no lease.

- **R2 — One topology-neutral ownership service.** The same service must support
  claiming the next queued Invocation for self-contained polling and claiming
  an exact Invocation for future external delivery. Exact claim of a missing,
  terminal, or already-owned Invocation must return a durable no-work outcome,
  not execute it. Concurrent next or exact claims must yield at most one owner;
  delivery identity itself grants no authority.

- **R3 — Fence every owner write.** Renewal and terminal settlement must match
  Invocation ID, `running` state, owner, exact lease attempt, and an unexpired
  stored lease. A successful renewal extends only that attempt. Completion or
  failure must atomically append one lifecycle state, update the Invocation's
  current revision, error, `completed_at`, and `updated_at`, clear the lease,
  and preserve first-terminal-write wins. A wrong owner, old attempt, or lapsed
  lease returns a typed lease-lost result and changes no durable row. The
  existing ID-only status-update repository operation must be removed; future
  execution writes must go through this fenced service.

- **R4 — Heartbeat fails closed at the deadline.** While execution is active,
  the engine must renew early enough to leave retry margin. A transient renewal
  error may be retried while the last confirmed lease is still valid; definite
  lease loss or passage of that deadline must cancel local execution and forbid
  settlement. Executor cancellation caused by process drain or lease loss must
  not be recorded as a semantic model failure by the departing owner.

- **R5 — Bounded, wakeable polling.** The self-contained adapter must subscribe
  before its first database check, fill only its configured positive concurrency
  capacity, and keep polling at a bounded interval even if notification is
  absent, duplicated, or unavailable. After a fresh, non-deduplicated admission
  commits, the configured admission path must emit one coalescable wake; it must
  never notify inside the transaction, and signal failure must not change the
  successful admission result. Claim, renewal, or scan errors must be logged and
  retried without stopping the process or exceeding the bound. This post-commit
  hint supersedes PRD 003's temporary no-notification rule; admission still
  neither executes work nor grants ownership.

- **R6 — Visible engine-loss recovery.** A startup-and-periodic reaper must scan
  expired `running` leases in bounded batches and conditionally fail each exact
  attempt with stable error code `execution_lost`. Reaping must use the same
  Session-then-Invocation lock order, append the terminal lifecycle state,
  stamp `completed_at`, clear the lease, and free the Session in one
  transaction. It must not reap queued work, resume work, or overwrite a
  renewed or terminal row.

- **R7 — Joined lifecycle and safe composition.** Engine shutdown must stop new
  claims and begin a configurable bounded drain in which active execution and
  heartbeats continue so work can finish on its original engine version. After
  the grace expires, it must cancel remaining executor contexts, stop their
  heartbeats, and join every started goroutine before database dependencies
  close. The runner must be composable with either an embedded or request-scoped
  delivery adapter, but this slice may activate it only when an executor is
  explicitly injected; the synthetic executor is test infrastructure, never
  the production default.

- **R8 — Durable operational evidence.** Structured logs must distinguish
  claim, renewal trouble, lease loss, settlement, and reaping using Invocation
  ID, owner/attempt, status, queue age, and latency where applicable. They must
  never include input, instructions, spec JSON, credentials, or provider
  payloads. Schema checks and migrations must preserve the PRD 002 rule:
  `nvokend migrate` applies changes; normal serve startup only verifies them.

## Acceptance

- [x] **A1 (R1, R2):** Twenty concurrent poll and exact-claim contenders for
  one queued Invocation produce one `running` owner and one running lifecycle
  revision with attempt 1; every loser reports no work. Multiple queued rows are
  claimed in `(created_at, id)` order without double ownership.

- [x] **A2 (R1, R3):** A live owner renews and settles successfully. Calls with
  the wrong owner, an old attempt, or a lease whose stored expiry has passed
  return lease lost and leave the Invocation, state history, error, and Session
  revision unchanged. Fault injection at claim and settlement write boundaries
  leaves no partial transition.

- [x] **A3 (R4):** A synthetic execution spanning several lease periods keeps
  one attempt alive. Transient renewal failures before its confirmed deadline
  recover; a definite loss or failures through the deadline cancel the executor,
  prevent its settlement, and leave no heartbeat goroutine running.

- [x] **A4 (R5):** With notifications enabled, fresh committed work wakes an
  already-subscribed runner promptly. With the notification dropped and after a
  full process restart, polling still discovers and settles the row. Duplicate
  notifications and a backlog larger than capacity never exceed the configured
  executor concurrency.

- [x] **A5 (R6):** Killing a synthetic owner leaves one `running` row. After its
  lease expires, a fresh reaper writes one `failed` state with
  `error.code=execution_lost`, clears the lease, and permits a new Invocation
  for that Session; the old owner can no longer renew or settle. A renewal or
  terminal race won before the reaper's conditional write is preserved.

- [x] **A6 (R7):** During shutdown, an execution that finishes within the drain
  grace settles normally while no new row is claimed. A blocking execution is
  cancelled only after the grace, then all claimed goroutines and heartbeat
  loops join before the pool closes. Its row remains `running` for a later
  reaper rather than being mislabeled a model failure.

- [x] **A7 (R7):** Production daemon construction without an injected executor
  does not claim or alter queued work. Component-level composition with the
  synthetic executor exercises the complete poll/claim/heartbeat/settle loop
  without exposing a public synthetic-execution switch.

- [x] **A8 (R8):** Migration concurrency and serve-with-stale-schema tests keep
  schema changes explicit and fail closed. Captured engine/reaper logs provide
  IDs and attempt outcomes needed to diagnose a run while containing none of
  the admitted input, spec, or credential fixtures.

## Sequencing notes

- PRD 005 supplies the real Dive executor and canonical assistant output, then
  activates this runner in self-contained `nvokend` mode.
- PRDs 009 and 010 add durable Cloud Tasks dispatch and a private exact-work
  adapter over this ownership service; they do not create another claim model.
- PRD 014 may change expired work from visible failure to checkpoint-based
  resume. Until then, retry means admitting a new Invocation after the failed
  one, not replaying a possibly charged model call.
