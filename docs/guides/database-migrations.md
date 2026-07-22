# Database migrations

nvoken uses golang-migrate with embedded, forward-only Postgres migrations.
Apply them explicitly before starting a service revision:

```bash
DATABASE_URL='postgres://…' go run ./cmd/nvokend migrate
```

`MIGRATION_TIMEOUT` bounds connection, advisory-lock wait, and migration
statements and defaults to `5m`. golang-migrate's pgx/v5 driver pins one
connection and takes a session-scoped Postgres advisory lock, so concurrent
release jobs serialize. It records the current version and dirty state; nvoken
fails if that state is dirty or its compatibility declaration is absent or
unsafe for the binary.

Committed `.up.sql` files are immutable. Fix a released migration by adding a
new forward migration rather than editing the old file or migrating down.

Normal `nvokend` or `nvokend serve` startup never runs migrations. On Cloud Run,
use this command from one release step or Cloud Run Job before shifting service
traffic; do not make every service replica perform schema work.
The serve path requires a present, clean schema. An exact version passes. A
newer version passes only when its `nvoken_schema_compatibility` row declares
the binary's embedded schema version or an older one as its minimum; unknown or
unsafe newer schemas fail closed.

For an upgrade, the target binary runs `upgrade-preflight` before migration and
`migrate` repeats the same gate before calling golang-migrate. Both need the
currently serving build and embedded schema identity:

```bash
NVOKEN_CURRENT_BUILD_VERSION='CURRENT_BUILD' \
NVOKEN_CURRENT_SCHEMA_VERSION='CURRENT_SCHEMA' \
DATABASE_URL='postgres://…' \
./nvokend-next upgrade-preflight
```

See [Compatible upgrades and one-release rollback](compatible-upgrades.md) for
the migration-author rule, one-time transition, expand/contract example, and
single-daemon procedure.

Use `nvokend diagnose` to report that same read-only compatibility verdict as a
bounded `database_schema` result before shifting traffic. See
[Operational signals and diagnostics](operational-signals.md) for the command
and result classes.

The [Google Cloud paved deployment](../../deploy/google-cloud/README.md) runs the
same immutable image as a single-task migration Job. Its release script updates
and executes that Job to success before the full Terraform apply can move the
service to the new image. A failed migration leaves serving traffic on the
prior revision.

For adapter integration tests, run:

```bash
make test-postgres
```

When `NVOKEN_TEST_DATABASE_URL` is unset, the target starts PostgreSQL 17 in a
disposable Docker container on a random loopback port, waits for readiness,
runs the full Go suite, and removes the container. Set
`NVOKEN_TEST_POSTGRES_IMAGE` to exercise another Postgres image. If Docker is
unavailable, set `NVOKEN_TEST_DATABASE_URL` to an existing disposable database
whose user may create and drop schemas. The target uses that database without
starting a container.

The tests create and remove isolated schemas. Do not point the variable at a
production database. Authoritative runtime records use
restrictive foreign keys and have no deletion or pruning surface in this
implementation. Only terminal execution-dispatch and callback-delivery
diagnostics have bounded prune operations; see
[Data retention and storage growth](data-retention.md) for their defaults and
the governing inventory.

## Schema changes against large tables

Every migration statement runs under `MIGRATION_TIMEOUT` and takes ordinary
Postgres locks. Two operations deserve a second look once an installation
has large, busy tables:

- A plain `CREATE INDEX` blocks all writes to the target table for the
  duration of the build. On a large `session_messages` or `invocations`
  table that is downtime, not a migration. Do not raise `MIGRATION_TIMEOUT`
  to push a blocking index build through a busy table.
- Full-table backfills (`UPDATE` or `INSERT ... SELECT` over every row)
  must run as bounded batches, not as one migration transaction. One long
  transaction holds locks for its whole runtime and prevents autovacuum
  from reclaiming dead tuples anywhere in the database while it runs.

`CREATE INDEX CONCURRENTLY` avoids the write lock but cannot run inside a
transaction, and every nvoken migration updates the
`nvoken_schema_compatibility` singleton inside its transaction. Concurrent
index creation therefore needs a dedicated mechanism: a non-transactional
migration step plus a documented recovery for the invalid index and dirty
migration state a failed `CONCURRENTLY` leaves behind. That mechanism is
deliberately deferred until an installation first needs a new index on a
large table. This section records the constraint so that moment is
recognized as a design task, not handled ad hoc during a release.

For autovacuum monitoring and connection topology, see
[Postgres operations](postgres-operations.md).

## Queries and generated code

Postgres queries live in `internal/adapters/postgres/queries/`. sqlc reads those
queries and the ordered `.up.sql` migrations, then generates the private
database package in `internal/adapters/postgres/sqlc/`. The repository adapter
maps those generated rows into domain types; generated types do not cross the
adapter boundary.

Regenerate after changing a query or migration:

```bash
make sqlc
```

Generated code is checked in. `make check` runs `sqlc diff` with the pinned
sqlc version and fails if the checked-in output is stale.
