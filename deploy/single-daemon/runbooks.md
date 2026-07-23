# Single-daemon incident guide

These runbooks cover the exact one-daemon, embedded-execution profile. Postgres
is authoritative for accepted work, checkpoints, ToolCalls, transcript, and
terminal state. A process restart can prompt recovery but never grants execution
ownership; claims and fences in Postgres do.

Start every incident with four bounded checks:

1. Check `GET /health` for process liveness only.
2. Run `nvokend diagnose` with the deployed configuration.
3. Find the latest `process_started` or `process_start_failed` event and confirm
   build version, schema version, `combined`, `embedded`, and `in_process`.
4. Read the affected Invocation and Session by durable ID, then correlate logs
   by `invocation_id`, `session_id`, `tool_call_id`, or `delivery_id`.

Do not copy credentials, prompts, transcript content, callback bodies, or
database URLs into an incident record.

## Daemon down

**Meaning.** `/health` cannot connect and the supervisor reports no healthy
process. Runtime admission and reads are unavailable; this profile has no
second API replica.

**Safe actions.** Check host resources and the last `process_failed` or
`process_start_failed` event. Confirm the immutable image/binary and protected
environment still match the recorded deployment. Run `diagnose`, then restart
the one supervised process. Let Postgres claims, lease expiry, and the reaper
decide which work is eligible.

**Recovery.** `/health` returns, `diagnose` passes, `process_started` reports the
expected identity, and durable reads show accepted work still present. A
checkpointed Invocation may be queued, running under a newer fence, waiting, or
terminal.

**Unsafe.** Do not create replacement Invocations for acknowledged turns, clear
claims, edit leases, or delete rows to make work appear unstuck.

## Database unavailable or incompatible

**Meaning.** `/health` may remain healthy because it is dependency-free, while
requests fail and `diagnose` reports `database_connectivity` or
`database_schema` failure.

**Safe actions.** Check Postgres service health, storage, connection limits,
TLS, DNS/network reachability, and credential rotation. For schema failures,
compare the binary's expected version with the diagnostic's current/dirty
state. Restore connectivity or deploy the exactly compatible binary. Keep the
daemon stopped during an unresolved schema mismatch.

**Recovery.** All diagnostic components succeed, no acknowledgement disappeared,
and one known durable read returns the same Invocation and Session IDs.

**Unsafe.** Do not edit `nvoken_schema_migrations`, mark a dirty migration clean,
run down migrations, or point a binary at an unknown newer schema.

## Invocation stuck, queued, or recovering

**Meaning.** An Invocation remains nonterminal longer than its workload normally
requires. Queueing can be ordinary saturation. Recovery after abrupt loss waits
for the recorded lease boundary before a new fence can claim the work.

**Safe actions.** Read the Invocation's status, `deadline_at`, resolved total,
active, and waiting limits, pending host ToolCalls, and error. Inspect
`invocation_claimed`, `invocation_recovered`, `invocation_maintenance_failed`,
provider, and settlement events. Confirm Postgres is healthy,
`ENGINE_CONCURRENCY` is nonzero, and the lease/reaper intervals still match the
deployed example. Restore the dependency or capacity and allow the engine to
converge. Cancel through the public API only when the host intends cancellation.

**Recovery.** Queue age declines; a newer claim/fence progresses from the last
committed checkpoint; or the Invocation settles once with an authoritative
terminal state.

**Unsafe.** Do not shorten a live lease in SQL, delete a ToolCall or checkpoint,
force a lifecycle status, or assume a repeated provider request means a repeated
committed result. Work completed outside Postgres in the crash uncertainty
window may repeat.

## Provider failure

**Meaning.** `provider_call_completed` classifies configuration, throttling,
upstream rejection/outage, timeout/transport, invalid response, or success. A
provider dependency is not covered by nvoken availability.

**Safe actions.** Check the bounded `outcome_class`, selected provider/model,
credential source, provider status, and Invocation deadline and resolved limits.
Repair the selected credential or dependency. A terminal `provider_error` is
immutable; the host may admit a new intentional turn with a new idempotency key
only after deciding that another model effect is wanted.

**Recovery.** New bounded smoke work succeeds through the same explicitly
selected provider and existing failed Invocations remain readable as failed.

**Unsafe.** Do not mutate a failed Invocation, silently swap its credential
source, or reuse a new ID merely because a provider outcome was uncertain.

## Callback retry or exhaustion

**Meaning.** A callback is at-least-once delivery. Retryable transport or
upstream outcomes retain one delivery and ToolCall identity; bounded exhaustion
becomes a model-visible tool error.

**Safe actions.** Correlate `callback_delivery_*` events by `delivery_id` and
`tool_call_id`. Confirm the receiver is public HTTPS, verifies the signature,
and stores the first result by `Idempotency-Key`. Restore receiver availability
and let the persisted retry schedule run. If exhausted, inspect the durable
Invocation outcome before choosing a new host-level turn.

**Recovery.** The receiver reports one accepted effect for the stable ToolCall
ID, the delivery settles, and the parked Invocation continues or fails through
its existing contract.

**Unsafe.** Do not replay the external effect with a new ToolCall/Invocation ID,
delete delivery rows, follow redirects, or treat an HTTP retry count as the
effect identity.

## Storage growth

**Meaning.** Authoritative runtime history is retained by default. Only bounded
terminal transport diagnostics are pruned, so database and backup storage grow
with use.

**Safe actions.** Run the content-free database-size and largest-table queries
from the [retention guide](../../docs/guides/data-retention.md). Increase storage
before exhaustion, verify backup capacity, and record the growth rate and
largest relation names. Keep dispatch/callback retention and batch limits valid.

**Recovery.** Postgres has safe free capacity, writes and backups succeed, and
the cause of growth is recorded for later capacity or retention design.

**Unsafe.** Do not delete Session messages, Invocations, ToolCalls, checkpoints,
or their foreign-key parents. `VACUUM FULL` and ad hoc table rewrites require a
separate maintenance plan and outage.

## Failed upgrade

**Meaning.** Migration, diagnosis, startup, or smoke failed for the target
binary. The current implementation accepts only its exact schema version.

**Safe actions.** Keep ingress stopped. Record old/new build and schema
identities and the first attributable failure. If no migration ran, restart the
old immutable binary. If the schema changed, do not start the old binary until
the PRD 019 compatibility record explicitly permits it; choose forward repair
or a reviewed restore decision.

**Recovery.** One compatible binary/schema pair passes `diagnose`, starts, and
returns durable readback for retained work. The incident record names any work
excluded by a restore recovery point.

**Unsafe.** Do not edit migration state, run a hand-written down migration,
delete new rows, or call a pre-upgrade restore a lossless rollback.

## Failed restore

**Meaning.** The isolated logical restore failed to load, diagnose, or preserve
the expected schema. It is not production authority.

**Safe actions.** Leave production untouched. Record source and restore tool
versions, recovery point, target identity, and safe error class. Recreate a new
empty restore target and retry from a known successful backup. Clean up only the
exact isolated target after recording responsibility.

**Recovery.** The isolated database passes the PRD 020 non-mutating verifier and
terminal-only readback procedure once those are implemented. Until then, a
schema-only diagnostic is useful but does not prove restore readiness.

**Unsafe.** Do not point the daemon at a full restore with claimable work merely
to inspect it, merge restored rows into production, or promote a rewound restore
without reconciling external callbacks and model effects.

## Graceful shutdown does not complete

**Meaning.** The supervisor's `SHUTDOWN_TIMEOUT` expired before HTTP, engine,
callback, and database components joined.

**Safe actions.** Preserve logs, confirm the configured engine/callback drain
graces leave at least one second inside the total timeout, and identify the
component still active. If the host must terminate it, record the exact time and
let lease/fence recovery handle unfinished work after restart.

**Recovery.** The replacement process passes diagnosis, expired work recovers
under a new fence, and no stale owner commits a second terminal result.

**Unsafe.** Do not keep two daemons running to compensate for slow shutdown;
multiple replicas are outside this profile even though database fences protect
individual writes.
