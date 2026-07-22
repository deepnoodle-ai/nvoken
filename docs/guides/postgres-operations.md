# Postgres operations

> **For deployment operators.** This documents how nvoken uses Postgres
> connections, how to keep autovacuum healthy as usage grows, and where the
> first database bottleneck appears. It is not required for the local Run
> guide. For storage growth and retention, see
> [Data retention and storage growth](data-retention.md).

## Connection topology

nvoken pools its own connections with pgxpool. Each process opens at most
`DATABASE_MAX_CONNS` connections and configures every connection with
session parameters: `statement_timeout` (120s default) and
`idle_in_transaction_session_timeout` (30s default). Cancellation wake-up
additionally holds one dedicated `LISTEN` connection per process as a
latency hint.

Point `DATABASE_URL` at the database directly, or at a pooler in session
mode. Do not use a pooler in transaction mode (for example pgbouncer
transaction mode, or a provider's transaction-pooled endpoint). Transaction
mode does not reliably apply session parameters and does not deliver
`LISTEN/NOTIFY`, so the runtime would lose its per-connection timeouts and
its low-latency cancellation hint. nvoken already is the connection pooler;
an external pooler adds value only if it runs in session mode.

Size `max_connections` for the sum across processes: each replica of each
process role can hold its configured `DATABASE_MAX_CONNS`, plus one `LISTEN`
connection per process, plus headroom for migrations, diagnostics, and
operator sessions.

## Autovacuum health

Postgres reclaims dead row versions with autovacuum. Five nvoken tables
rewrite live rows continuously during normal operation, so they produce dead
tuples much faster than their row count suggests:

| Table | Churn source |
| --- | --- |
| `sessions` | Message sequence and lifecycle counters advance on every append |
| `invocations` | Lifecycle transitions, checkpoint cursors, and one lease heartbeat per running Invocation per `ENGINE_HEARTBEAT_INTERVAL` |
| `tool_calls` | Attempt and status transitions |
| `execution_dispatches` | Publication claims, lease renewals, settlement |
| `callback_deliveries` | Delivery claims, retries, terminal transitions |

These updates always modify an indexed column, so Postgres cannot use HOT
updates: every update writes a new entry into every index on the table.
Migration `000016` therefore lowers the per-table thresholds to
`autovacuum_vacuum_scale_factor = 0.05` and
`autovacuum_analyze_scale_factor = 0.02`, so cleanup and planner statistics
start at 5% and 2% of each table instead of the 20% and 10% server
defaults. No operator action is needed for that baseline.

What to watch, with metadata-only queries against the nvoken database:

Autovacuum that runs for a very long time means it is not keeping up:

```sql
SELECT pid, state, now() - query_start AS duration, LEFT(query, 120) AS query
FROM pg_stat_activity
WHERE query ILIKE 'autovacuum:%'
ORDER BY duration DESC;
```

An autovacuum entry older than about an hour on an nvoken table deserves
attention. Check dead-tuple pressure and vacuum recency per table:

```sql
SELECT relname, n_live_tup, n_dead_tup, last_autovacuum, last_autoanalyze
FROM pg_stat_user_tables
WHERE schemaname = ANY (current_schemas(false))
ORDER BY n_dead_tup DESC
LIMIT 10;
```

Healthy churn tables cycle `n_dead_tup` back toward zero between vacuums.
If dead tuples grow monotonically, raise autovacuum throughput at the
instance level (`autovacuum_vacuum_cost_limit`, `autovacuum_max_workers`,
or your provider's equivalent knobs) before the table and its indexes
bloat. Recovering after the fact requires `REINDEX INDEX CONCURRENTLY` for
bloated indexes and is much more disruptive than staying ahead.

## The first ceiling: index maintenance on `invocations`

`invocations` is the hottest table and carries roughly fourteen indexes:
the claim and reaper partial indexes, the idempotency and identity unique
constraints, and five keyset list indexes. Because its updates are never
HOT, every lifecycle transition and every lease heartbeat writes new
entries into all of them, and every BEFORE UPDATE trigger runs as well.
This combination is the expected first CPU and autovacuum bottleneck at
high sustained throughput.

The baseline mitigation (migration `000016`) already ships. The remaining
levers are deliberate and should be pulled only on evidence from the
queries above or from database CPU profiles:

- Consolidate the two BEFORE UPDATE trigger functions on `invocations`
  into one to halve per-update trigger overhead.
- Revisit `invocations_account_status_created_keyset`. It is the one list
  index whose key column (`status`) churns on every transition; a partial
  variant may serve the list API at a fraction of the maintenance cost.
- Lengthen `ENGINE_HEARTBEAT_INTERVAL` if heartbeat writes dominate; the
  lease duration bounds how stale a crashed engine can look, and the
  interval must stay well inside it.

Record what you change and why. These levers trade observability or
recovery latency for write cost, and the right balance depends on the
installation.
