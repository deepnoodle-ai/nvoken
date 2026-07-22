# Postgres operability hardening

**Status:** Implemented
**Sequence:** 031
**Depends on:** `002-prd-postgres-runtime-spine.md`,
`019-prd-compatible-upgrades-and-rollback.md`, and
`021-prd-initial-retention-posture.md`. The migration in this PRD is
numbered `000016` and follows PRD 030's migration `000015`.
**Source review:**
[`2026-07-22-postgres-operability-review.md`](../reviews/2026-07-22-postgres-operability-review.md)
(R1, R2, R3, R4, R6)

## ELI5

nvoken's busiest tables rewrite the same rows over and over: heartbeats,
sequence counters, lease flips. Postgres cleans up after those rewrites with
autovacuum, but its default settings wait until a fifth of a table is dead
before starting. This PRD tells Postgres to clean these specific tables
early, and writes down the operational knowledge an operator needs before
their installation is big: how to connect, what to watch, and what a schema
change may never do to a large table.

## Why

The operability review compared nvoken's Postgres usage against current
production guidance for Postgres-backed queues and durable execution. The
structural design already matches that guidance: SKIP LOCKED claims, READ
COMMITTED writes, selective partial indexes, lease columns instead of held
locks. What is missing is the operational layer around it.

Three gaps matter. First, the five churn tables run on default autovacuum
thresholds even though their update pattern defeats HOT optimization and
amplifies every heartbeat across every index; this is the documented first
bottleneck of comparable systems, and tuning it costs one metadata-only
migration. Second, nothing tells an operator that a transaction-mode pooler
silently degrades the runtime, or how to observe vacuum health at all.
Third, the migration conventions have no rule against blocking DDL or
unbatched backfills on large tables, which works only while every
installation is small.

Doing nothing keeps the runtime correct but plants three slow-burning
operational incidents in every growing installation.

## Goals

- Dead-tuple cleanup and planner statistics on churn tables stay fresh
  without operator intervention, at every installation size.
- An operator can answer "is autovacuum keeping up" and "is my connection
  topology safe" from nvoken's own documentation.
- The next engineer who writes a migration against a large table finds the
  policy before they find the outage.

Guardrails: no public API change, no schema shape change, no new
configuration surface, and the previous release binary remains safe against
the new schema (ordinary migration).

## Functional requirements

- FR-1: A migration `000016` must set `autovacuum_vacuum_scale_factor = 0.05`
  and `autovacuum_analyze_scale_factor = 0.02` on `sessions`, `invocations`,
  `tool_calls`, `execution_dispatches`, and `callback_deliveries`, and update
  the compatibility singleton to schema 16 with minimum binary schema 14 in
  the same transaction.
- FR-2: The migration must be declared `ordinary` in `compatibility.json`,
  and the embedded-migration tests must cover the new version and
  declaration.
- FR-3: An integration test must assert, after migration, that all five
  tables carry both storage parameters, so a future migration cannot drop
  them unnoticed.
- FR-4: A new operator guide, `docs/guides/postgres-operations.md`, must
  cover: connection topology (direct or session-mode only, with the
  per-role connection budget), autovacuum monitoring queries with
  interpretation, and the index-maintenance ceiling on `invocations` with
  its recorded levers.
- FR-5: `docs/guides/database-migrations.md` must gain a section on schema
  changes against large tables: which DDL blocks writes, how
  `MIGRATION_TIMEOUT` bounds it, the batched-backfill rule, and the recorded
  constraint that `CREATE INDEX CONCURRENTLY` conflicts with the
  compatibility-row convention and needs a dedicated mechanism when first
  required.
- FR-6: `internal/adapters/postgres/migrations/README.md` must state the
  same large-table rules where migration authors will see them.
- FR-7: `docs/guides/data-retention.md` must record that partition-drop
  retention is foreclosed by the composite foreign keys and that any future
  deletion contract will use bounded batched deletes.
- FR-8: The guides index must link the new guide under operator references.

## Non-goals

- No concurrent-index migration mechanism. The constraint is recorded; the
  mechanism ships when an installation first needs an index on a large
  table.
- No trigger consolidation on `invocations` and no index restructuring.
  These are recorded levers, pulled only on observed CPU or bloat evidence.
- No table partitioning and no change to the retention contract.
- No changes to pool sizing defaults, isolation levels, claim batching, or
  any query. The review confirmed these as correct.
- No metrics stack. The guide uses SQL against Postgres's own statistics
  views, consistent with PRD 018's signals posture.

## Risks

| Risk | Impact | Mitigation |
|------|--------|------------|
| Migration numbering collides with in-flight PRD 030 work | Merge conflict in `compatibility.json` and migration tests | Resolved: this PRD takes `000016` and landed after PRD 030's `000015` |
| Lower vacuum thresholds add vacuum frequency on tiny installations | Negligible background I/O | Thresholds scale with table size; on small tables the difference is invisible |
| `ALTER TABLE ... SET` lock waits behind long transactions | Brief migration stall | The statement takes SHARE UPDATE EXCLUSIVE, does not rewrite the table, and runs under `MIGRATION_TIMEOUT` |

## Open questions

None blocking. Whether `device_authorizations` (polled during device grants)
ever warrants the same treatment can wait for evidence; the table is small
and rows are short-lived.
