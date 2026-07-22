# Backup, restore, and recovery drills

nvoken's durable authority is Postgres. A restore is acceptable only after it
is isolated, checked by the matching nvoken binary, and shown to preserve the
runtime records needed for recovery.

Redis, Cloud Tasks, local files, and in-memory live events are delivery or
projection state; never restore them as authority. Runtime credentials,
provider keys, callback signing material, Terraform state, and other deployment
configuration are separate secret/configuration recovery concerns. Do not put
them in a logical database archive or a drill record.

This guide proves restore and readback. It does not schedule backups, promote a
rewound database into production, reconcile rewound queues with external
systems, or provide tenant-level recovery.

## Safety rules

1. Restore only into a new database name or a new Cloud SQL instance. Do not
   overwrite the source.
2. Keep the restored target outside production traffic and Terraform authority.
   Never change a Runtime or executor `DATABASE_URL` to the target during a
   drill.
3. Run `nvokend verify-restore` before starting any daemon. The verifier opens
   one repeatable-read, read-only transaction, uses the same schema verdict as
   `serve` and `diagnose`, and emits only bounded metadata under
   `event=restore_verification`.
4. Do not start execution components against a full restore. Queued, running,
   or waiting rows may cause model calls, callbacks, or task publication. The
   checked repository drill starts a daemon only against a separate
   terminal-only fixture.
5. Record the recovery point, elapsed time, versions, result, and cleanup owner
   without credentials or application content. Use the
   [drill record contract](../testing/backup-restore/README.md).

The verifier defaults to a two-minute total timeout. Set
`RESTORE_VERIFY_TIMEOUT` up to `30m` only for a large isolated restore. It checks
the embedded schema version, required tables and validated constraints, the
one-nonterminal-Invocation-per-Session index and data invariant, terminal state
consistency, Session transcript/lifecycle cursor bounds, Invocation checkpoint
bounds, and one metadata-only sample of a Session, Invocation, message,
ToolCall, and checkpoint. Missing representative rows means the selected drill
fixture is insufficient, not necessarily that an otherwise empty installation
is corrupt.

## Single-daemon logical backup and restore

Use the `pg_dump` client from the source server's major version or newer. A
client older than the source server is unsupported, and a dump is not
guaranteed to load into an older target server. The supported drill restores to
the same Postgres major version as the source. See the
[PostgreSQL `pg_dump` compatibility notes](https://www.postgresql.org/docs/current/app-pgdump.html).

`pg_dump` takes a transactionally consistent snapshot without stopping
`nvokend`; concurrent durable work after that snapshot is intentionally absent
from the archive. Quiesce admission and execution only when an operational
recovery point requires a coordinated application cutoff. Quiescence does not
make it safe to start a daemon against the full restored target.

Put source and target connection settings in a mode-`0600` PostgreSQL service
file or equivalent secret manager. Keep passwords out of command arguments and
shell history:

```ini
[nvoken_source]
host=source-db.internal
port=5432
dbname=nvoken
user=nvoken_backup
sslmode=require

[nvoken_restore]
host=restore-db.internal
port=5432
dbname=nvoken_restore_20260721
user=nvoken_restore
sslmode=require
```

Supply the password through a mode-`0600` `PGPASSFILE`. Then create a custom
archive and restore it into a new empty database:

```bash
export PGSERVICEFILE=/secure/path/nvoken-pg-service.conf
export PGPASSFILE=/secure/path/nvoken-pgpass

PGSERVICE=nvoken_source pg_dump \
  --format=custom \
  --no-owner \
  --no-privileges \
  --file=/secure/backup/nvoken.dump

PGSERVICE=nvoken_restore pg_restore \
  --exit-on-error \
  --no-owner \
  --no-privileges \
  --dbname=nvoken_restore_20260721 \
  /secure/backup/nvoken.dump
```

The target database must exist and contain no application schema before
`pg_restore`. Use a target role that can create the restored objects but cannot
reach the source database.

Run the verifier with a password-free URI; pgx reads the same `PGPASSFILE`:

```bash
PGPASSFILE=/secure/path/nvoken-pgpass \
DATABASE_URL='postgres://nvoken_restore@restore-db.internal:5432/nvoken_restore_20260721?sslmode=require' \
nvokend verify-restore
```

A successful run exits zero. Any `outcome=failed` check or nonzero exit makes
the restore unusable until the specific bounded diagnosis is understood. Do
not migrate or repair the target merely to make verification pass; an empty,
dirty, behind, ahead, missing-table, missing-constraint, cursor, terminal, or
representative-record failure is evidence about the archive or binary pairing.

The checked local exercise is:

```bash
make test-restore
```

Its Python runner starts disposable Postgres 17, creates completed, queued,
waiting, ToolCall, and checkpointed work, performs a credential-safe custom
dump, restores under a different database name, runs the verifier, compares
authoritative IDs/counts, and removes both databases and the container. A
separate terminal-only fixture starts the compatible daemon and reads the
terminal Invocation through the authenticated HTTP API without making a model
or callback request.

## Google Cloud SQL backup or PITR restore

Run this procedure only in a disposable or deliberately selected project. Set
the source from the Terraform output and choose a unique target that no Runtime
or executor references:

```bash
export NVOKEN_GCP_PROJECT='your-project'
export NVOKEN_GCP_SOURCE='nvoken-staging-postgres'
export NVOKEN_GCP_TARGET='nvoken-restore-20260721'
```

Record the source instance's connection name, region, database version, backup
configuration, PITR configuration, and current Terraform-managed identity
without reading Terraform state or secret values:

```bash
gcloud sql instances describe "${NVOKEN_GCP_SOURCE}" \
  --project="${NVOKEN_GCP_PROJECT}" \
  --format='yaml(connectionName,region,databaseVersion,state,settings.backupConfiguration)'
```

For a standard backup, select only a successful run and record its ID,
start/end timestamps, and location. Create an on-demand backup first when the
drill needs a new recovery point:

```bash
gcloud sql backups create \
  --instance="${NVOKEN_GCP_SOURCE}" \
  --project="${NVOKEN_GCP_PROJECT}" \
  --description='nvoken restore drill'

gcloud sql backups list \
  --instance="${NVOKEN_GCP_SOURCE}" \
  --project="${NVOKEN_GCP_PROJECT}" \
  --filter='status=SUCCESSFUL' \
  --sort-by='~endTime' \
  --format='table(id,status,startTime,endTime,location)'
```

Create the new empty target with the same database version and compatible
edition, region, tier, storage, and connectivity settings recorded from the
source. Keep it outside Terraform and production traffic. For example, after
setting the values from the source inspection:

```bash
gcloud sql instances create "${NVOKEN_GCP_TARGET}" \
  --project="${NVOKEN_GCP_PROJECT}" \
  --database-version="${NVOKEN_GCP_DATABASE_VERSION}" \
  --edition="${NVOKEN_GCP_EDITION}" \
  --region="${NVOKEN_GCP_REGION}" \
  --tier="${NVOKEN_GCP_TIER}" \
  --no-deletion-protection
```

Wait until the target is `RUNNABLE`, then restore the selected backup into it.
The target must exist and have the same database version as the source. Never
use the source instance as `--restore-instance`:

```bash
gcloud sql backups restore "BACKUP_ID" \
  --backup-instance="${NVOKEN_GCP_SOURCE}" \
  --restore-instance="${NVOKEN_GCP_TARGET}" \
  --project="${NVOKEN_GCP_PROJECT}"
```

Cloud SQL also supports an isolated point-in-time clone when PITR is enabled
and the chosen RFC 3339 timestamp is within the retained log window:

```bash
gcloud sql instances clone \
  "${NVOKEN_GCP_SOURCE}" \
  "${NVOKEN_GCP_TARGET}" \
  --point-in-time='2026-07-21T20:00:00Z' \
  --project="${NVOKEN_GCP_PROJECT}"
```

These forms follow Google's current
[backup restore](https://cloud.google.com/sql/docs/postgres/backup-recovery/restoring)
and [PITR clone](https://cloud.google.com/sql/docs/postgres/backup-recovery/pitr)
procedures. Wait for the target instance and restore operation to become
`RUNNABLE`/`DONE`, then confirm the source service revisions, source instance,
traffic routing, and Terraform plan remain unchanged.

For the paved private-IP topology, run the current Cloud SQL Auth Proxy from an
approved host with VPC reachability and `--private-ip`; bind it to loopback on a
temporary port. Reuse the restored database user/password through a temporary
mode-`0600` `PGPASSFILE`. Do not print or copy the secret into the drill record:

```bash
cloud-sql-proxy --private-ip --address=127.0.0.1 --port=55432 \
  "${NVOKEN_GCP_PROJECT}:REGION:${NVOKEN_GCP_TARGET}"

PGPASSFILE=/secure/path/temporary-pgpass \
DATABASE_URL='postgres://nvoken@127.0.0.1:55432/nvoken?sslmode=disable' \
nvokend verify-restore
```

Loopback `sslmode=disable` applies only between the verifier and the Auth Proxy;
the proxy owns the authenticated Cloud SQL transport. Stop the proxy and remove
its temporary password file after verification.

## Record and cleanup

Create one record per profile under `docs/testing/backup-restore/`. Link its
`restore_verification` events or bounded component list, not raw logs containing
environment details. The cleanup owner must delete the restored database or
Cloud SQL target after evidence is retained and confirm that production
traffic, Cloud Tasks, Redis, secrets, and Terraform state were unchanged.

Deleting an isolated target is destructive. Resolve its exact name from the
record, verify that no service references it, and delete only that target. An
unremoved target keeps incurring storage/instance cost and must leave the drill
result marked incomplete.
