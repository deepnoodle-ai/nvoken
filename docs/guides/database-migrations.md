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
fails if that state is dirty or the database is ahead of the binary.

Committed `.up.sql` files are immutable. Fix a released migration by adding a
new forward migration rather than editing the old file or migrating down.

Normal `nvokend` or `nvokend serve` startup never runs migrations. On Cloud Run,
use this command from one release step or Cloud Run Job before shifting service
traffic; do not make every service replica perform schema work.
The serve path checks that the schema is present, clean, and at the exact
version expected by the binary; it exits rather than modifying an empty, dirty,
older, or newer database.

Use `nvokend diagnose` to report that same read-only compatibility verdict as a
bounded `database_schema` result before shifting traffic. See
[Operational signals and diagnostics](operational-signals.md) for the command
and result classes.

The [Google Cloud paved deployment](../../deploy/google-cloud/README.md) runs the
same immutable image as a single-task migration Job. Its release script updates
and executes that Job to success before the full Terraform apply can move the
service to the new image. A failed migration leaves serving traffic on the
prior revision.

For adapter integration tests, point `NVOKEN_TEST_DATABASE_URL` at a disposable
Postgres database and run:

```bash
make test-postgres
```

The tests create and remove isolated schemas. Authoritative runtime records use
restrictive foreign keys and have no deletion or pruning surface in this
implementation. Only terminal execution-dispatch and callback-delivery
diagnostics have bounded prune operations; see
[Data retention and storage growth](data-retention.md) for their defaults and
the governing inventory.

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
