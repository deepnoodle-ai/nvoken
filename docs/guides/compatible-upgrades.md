# Compatible upgrades and one-release rollback

nvoken keeps database migrations forward-only and applies them with
`golang-migrate`. It does not migrate down during application rollback. Instead,
each post-transition migration declares the oldest binary schema version that
may safely serve its result.

The guarantee is deliberately narrow: after an ordinary migration, the
immediately previous production binary remains startable. Unknown schemas and
schemas whose declared minimum is newer than that binary fail closed. Migration
14 is the one-time transition that introduces this record; binaries older than
that transition are not retroactively covered.

## Migration author rule

For every migration from 14 onward:

1. add its target and minimum binary schema versions to
   `internal/adapters/postgres/migrations/compatibility.json`;
2. update the singleton `nvoken_schema_compatibility` row to the same values in
   the `.up.sql` migration; and
3. classify the migration as `ordinary` only when the currently serving binary
   remains safe against the result.

The preflight reads the current database and the embedded declaration before
`golang-migrate` runs. Startup reads the database record afterward. Tests require
the manifest and migration set to remain aligned.

A breaking change uses expand/contract releases. For example, first add a
nullable replacement column while retaining the old column and declare the old
binary compatible. Deploy code that can read both and writes the replacement.
Only a later migration may remove the old column, once the immediately previous
binary no longer needs it; that migration declares that previous binary's schema
version as its minimum. Committed migrations are never edited.

## Single-daemon procedure

The currently running `process_started` event supplies its `build_version` and
embedded `schema_version`. Record those values, the target build identity, and
the current authoritative Invocation IDs used for readback.

Run the target binary's read-only gate while the current daemon is still
serving:

```bash
export DATABASE_URL='postgres://…'
export NVOKEN_CURRENT_BUILD_VERSION='CURRENT_BUILD'
export NVOKEN_CURRENT_SCHEMA_VERSION='CURRENT_SCHEMA'

./nvokend-next upgrade-preflight
```

For the one-time migration to schema 14, also set
`NVOKEN_MIGRATION_MODE=transition`. This explicitly acknowledges that a
pre-transition binary cannot restart after that migration. Do not use transition
mode again.

After a successful ordinary preflight:

1. stop the daemon and wait for it to exit;
2. run `./nvokend-next migrate` with the same environment;
3. start `./nvokend-next serve` under the supervisor;
4. run `./nvokend-next diagnose`; and
5. read the retained Invocation IDs and confirm the new `process_started`
   binary/database schema pair.

This profile has an expected interruption between stop and successful start. A
failed migration must not be worked around with a down migration or an edited
file; preserve the database, inspect its dirty state, and repair forward. If the
migration succeeds but the new daemon fails, stop it and start the previous
binary against the same database. Its startup check must report
`compatible_newer`; no database change is part of that rollback.

Queued or waiting work stays in Postgres and may be claimed after restart.
Running work remains governed by its lease and fence. Callback deliveries and
Cloud Tasks are not deleted to force progress, and terminal work remains
terminal.

## Evidence to retain

For an upgrade or rollback record, keep the `upgrade_preflight`,
`process_started`, and any `process_start_failed` events; the current and target
build identities; current, target, and minimum binary schema versions; final
binary/database pair; and authoritative readback of the chosen retained work.
Do not include database URLs, credentials, transcript content, or callback
payloads.
