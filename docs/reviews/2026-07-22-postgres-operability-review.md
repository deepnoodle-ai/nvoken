# Postgres Operability Review: Production Scaling Posture

**Status:** Review for action
**Date:** 2026-07-22
**Scope:** nvoken's Postgres usage reviewed against current production
guidance for Postgres-backed queues and durable execution: locking clauses,
transaction isolation, index selectivity and maintenance cost, autovacuum,
connection management, and migration safety.
**Inputs:** All fifteen committed migrations, `queries/runtime.sql`,
`transaction.go`, `database.go`, `migrate.go`, the daemon pool configuration,
`docs/guides/database-migrations.md`, `docs/guides/data-retention.md`, the
DBOS posts on scaling Postgres queues and durable execution, and Hatchet's
production Postgres guide.
**Lens:** Find the first bottleneck this schema will hit in production and
close the gaps that are cheap to close before it arrives.

---

## 1. Summary

The core queue and durability design already implements the hard lessons
these sources teach. Claims use `FOR UPDATE SKIP LOCKED`. The write path runs
at READ COMMITTED, with REPEATABLE READ reserved for read-only snapshots that
cannot produce write serialization failures. Claim and reaper queries are
served by partial indexes whose columns match each query's filter and sort
exactly, so index entries disappear the moment a row leaves its hot state.
External work happens under lease columns, never inside a database
transaction. Diagnostics pruning is batched and bounded. None of that needs
to change, and this review records it so nobody "fixes" it later.

The gaps are operational, not structural. Autovacuum is untuned and
undocumented for a schema whose hottest tables rewrite the same rows
continuously. There is no operator guidance on connection topology, and a
transaction-mode pooler would silently degrade the runtime. There is no
policy for blocking DDL or large-table backfills, and the migration
conventions that exist today would not survive the first index build on a
large `session_messages` table. Each gap is cheap to close now and expensive
to discover in production.

## 2. Findings

### R1. Churn tables run on default autovacuum thresholds

Five tables rewrite live rows as part of normal operation: `sessions` (the
`next_message_sequence` and `next_lifecycle_revision` counters advance on
every append), `invocations` (lifecycle transitions, checkpoint cursors, and
a lease heartbeat every `ENGINE_HEARTBEAT_INTERVAL`, default 10s, per running
Invocation), `tool_calls` (attempt and status transitions),
`execution_dispatches`, and `callback_deliveries` (lease and status flips).

These updates are never HOT because every one of them changes an indexed
column or a partial-index predicate column. Each update therefore writes a
new entry into every index on the table and leaves a dead tuple behind.
`invocations` carries roughly fourteen indexes, so a single heartbeat is
amplified fourteen times. Postgres's default autovacuum trigger waits for
dead tuples to reach 20% of the table. On a large `invocations` table that
means millions of dead tuples, stale planner statistics, and bloat in every
index, before the first vacuum starts.

**Action:** Lower per-table autovacuum thresholds for the five churn tables
in a migration, and document how to observe autovacuum health. This is the
"tune autovacuum before you are bloated" lesson, applied while every
installation is still small.

### R2. Connection topology is undocumented and pooler-sensitive

nvoken pools with pgxpool and configures each connection with session
runtime parameters (`statement_timeout`, `idle_in_transaction_session_timeout`).
Cancellation wake-up uses a dedicated `LISTEN` connection as a latency hint.
Both mechanisms assume a direct connection or a session-mode pooler. Behind a
transaction-mode pooler (pgbouncer transaction mode, Neon's pooled endpoint),
startup parameters are not reliably applied and `LISTEN` silently stops
delivering. Nothing in the operator documentation says so.

**Action:** Document the requirement: point `DATABASE_URL` at the direct
endpoint or a session-mode pooler. nvoken's pgxpool is the pooler. Include
the connection budget arithmetic per process role.

### R3. No policy for blocking DDL or large-table backfills

`MIGRATION_TIMEOUT` (default 5m) bounds migration statements server-side,
which is correct. But the conventions are silent on what a migration may do
to a large, busy table. A plain `CREATE INDEX` takes a lock that blocks all
writes to the table for the duration of the build. Migrations 000005 and
000013 ran full-table backfills inside one transaction; fine at the size
they ran against, unacceptable as a pattern once tables are large. And
`CREATE INDEX CONCURRENTLY` cannot run inside a transaction, which conflicts
structurally with the convention that every migration updates the
compatibility singleton in its own transaction. That conflict should be
recorded now, not discovered during an urgent index addition.

**Action:** Write the policy into the migration docs: what blocks writes,
how `MIGRATION_TIMEOUT` interacts with it, that future backfills on large
tables must be batched, and that a concurrent-index migration mechanism is
deferred but its constraint is known.

### R4. The first predictable ceiling is index maintenance on `invocations`

When throughput grows, the first CPU and autovacuum bottleneck will be the
combination described in R1: heartbeat-driven non-HOT updates against
roughly fourteen indexes. The levers, in order: per-table autovacuum tuning
(R1), consolidating the two BEFORE UPDATE trigger functions on `invocations`
into one, and revisiting `invocations_account_status_created_keyset`, the
one observability index whose key column churns on every transition. Only
the first is worth doing now. The others should be recorded as monitored
levers so the response is ready when the signal appears.

**Action:** Document the ceiling and its levers in the operations guide.
Change nothing else yet.

### R5. Confirmed correct; do not change

- `FOR UPDATE SKIP LOCKED` on every claim path, with the Session row as the
  serialization root for Invocation claims.
- READ COMMITTED on the write path; REPEATABLE READ only for read-only
  snapshots and restore.
- Partial indexes matched exactly to each claim, reaper, and pruner query.
- Lease columns instead of held locks or transactions around external work.
- Batched, index-served pruning for terminal diagnostics.
- `LISTEN/NOTIFY` as a coalescable latency hint, never load-bearing.
- Single-row claims (`LIMIT 1`): claim overhead is noise against work units
  that run for seconds to minutes. Batch claiming is not worth its
  complexity here.

### R6. Partition-drop retention is foreclosed; deletion will be batched

Hatchet-style partition-and-drop retention cannot apply to this schema. The
composite tenant-boundary foreign keys, including references to
`(session_id, sequence)`, do not survive partitioning `session_messages`.
That is an acceptable consequence of the integrity design, but it should be
written down: when the deletion contract ships, its mechanism will be
bounded batched deletes in dependency order, the pattern the diagnostics
pruners already use.

**Action:** Record this constraint in the retention guide.

## 3. Disposition

Findings R1, R2, R3, R4, and R6 are scoped into
[`031-prd-postgres-operability-hardening.md`](../prds/031-prd-postgres-operability-hardening.md).
R5 requires no change and is recorded here as the review's negative space.
