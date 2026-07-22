# Data retention and storage growth

> **For deployment operators and privacy owners.** This documents current
> storage behavior; it is not required to complete the local Run guide.

nvoken initially retains authoritative runtime data indefinitely. The governing
inventory is the design packet's
[Data and retention](../design/architecture.md#data-and-retention) section; this
guide does not define a second or narrower inventory. Neither production profile
has a background task or public API that deletes or compacts that data.

This is a conservative young-service default, not a promise that every operator
should retain agent history forever. It preserves recovery, idempotency, and
audit evidence while the deletion contract is unresolved, but it also means
storage grows with use and sensitive conversation history remains in Postgres
and its backups. Operators must size, secure, and monitor Postgres accordingly.

Redis live events are lossy previews, and Cloud Tasks requests are delivery
attempts. They are not transcript, execution, or business-history stores.
`execution_dispatches` and `callback_deliveries` are finite transport diagnostics,
not alternate copies of their owning Invocation or ToolCall evidence.

## Bounded transport diagnostics

Only terminal transport diagnostics have automated pruning. Each maintenance
pass deletes at most its configured batch size; it never follows a cascade into
an authoritative owner.

| Diagnostic rows | Eligible terminal states | Age | Batch | Pass interval |
| --- | --- | --- | --- | --- |
| Execution dispatches | `settled`, `abandoned` | `DISPATCH_RETENTION=168h` | `DISPATCH_BATCH_LIMIT=100` | `DISPATCH_RETENTION_INTERVAL=1h` |
| Callback deliveries | `succeeded`, `failed`, `abandoned` | `CALLBACK_RETENTION=168h` | `CALLBACK_BATCH_LIMIT=100` | `CALLBACK_RETENTION_INTERVAL=1h` |

The callback settings take effect only when callback tools are enabled with
`CALLBACK_SIGNING_KEY`. Dispatch settings are validated at every startup;
callback settings are validated when callbacks are enabled. Retention durations
and intervals must be positive, dispatch retention must exceed
`DISPATCH_STALE_AFTER`, and both batch limits must be between 1 and 1,000.
Invalid active settings stop startup with a component-specific error.

Reducing a retention duration can make an existing backlog eligible on the next
pass. Keep batches small enough to bound each transaction and run passes often
enough to catch up. Prune logs report counts and failures without reading or
logging transcript content.

## Observe Postgres growth

Run these metadata-only queries through a read-only connection to the nvoken
database. They return byte counts and relation names, not transcript or tool
payloads.

Total database size:

```sql
SELECT
    current_database() AS database_name,
    pg_database_size(current_database()) AS total_bytes,
    pg_size_pretty(pg_database_size(current_database())) AS total_size;
```

Largest nvoken tables, including their indexes and TOAST storage:

```sql
SELECT
    schemaname,
    relname AS table_name,
    pg_total_relation_size(relid) AS total_bytes,
    pg_size_pretty(pg_total_relation_size(relid)) AS total_size
FROM pg_catalog.pg_stat_user_tables
WHERE schemaname = ANY (current_schemas(false))
ORDER BY total_bytes DESC, schemaname, table_name
LIMIT 20;
```

Record these results on a regular cadence appropriate to the installation. The
absolute size helps with capacity planning; the rate of change reveals whether
retained history is approaching an operator-defined storage limit. This slice
does not define a quota, alert threshold, or automatic scaling policy.

The paved Google deployment exposes the corresponding Cloud SQL signal and
instance lookup in its [deployment guide](../../deploy/google-cloud/README.md#retention-and-storage-growth).

## Backups and future deletion

A future live-data deletion feature will not retroactively remove bytes from
already retained database backups. Any deletion promise must define both the
ordered removal of live records and when every backup containing those records
expires or is destroyed. Operators should therefore treat backup access and
retention as part of the current privacy boundary.

Session and tenant deletion, transcript compaction, cursor behavior after
deletion or compaction, and archive/export remain unresolved contracts. Legal
holds, per-tenant quotas, and automatic pruning of authoritative data are also
out of scope. Until those contracts ship, do not manually delete related rows:
the restrictive foreign keys and canonical transcript invariants are designed
for retained traces, not ad hoc partial erasure.
