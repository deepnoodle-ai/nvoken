# Runtime schema migrations

Migrations are ordered, embedded, and forward-only. golang-migrate records the
current version and dirty state in `nvoken_schema_migrations`; nvoken fails on a
dirty state or a version the binary does not know. An applied file must never be
edited. Correct a released migration with a new `.up.sql` migration.

sqlc parses this directory in lexical order as the schema source, so migration
filenames stay zero-padded and their numeric and lexical ordering must agree.

Runtime history is preserved by default. Foreign keys use `ON DELETE RESTRICT`,
Session messages and Invocation states are append-only, and this adapter exposes
no deletion or pruning method. A future retention design must add an explicit,
ordered migration and operation rather than relying on cascades.
