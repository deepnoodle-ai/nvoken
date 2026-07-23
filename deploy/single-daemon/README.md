# Single-daemon production profile

> **Production deployment guide.** If you are evaluating nvoken or building
> your first integration, start with [Run nvoken locally](../../docs/guides/run-locally.md).
> If you intend to change the repository, use [Develop nvoken](../../docs/guides/developing-nvoken.md).

This package is the canonical operating guide for nvoken's `single_daemon`
profile: one `nvokend` process in the `combined` role, embedded execution,
in-process live events, and one operator-provided PostgreSQL database. It is a
small production shape with durable restart recovery. It is not highly
available: while the daemon, its host, or Postgres is unavailable, the Runtime
API is unavailable too.

The authoritative readiness state remains in the
[production-readiness matrix](../../docs/testing/production-readiness-profiles.md).
Having this package does not make an unexercised installation production ready.

## Before you start

This guide is for the person who will operate nvoken, Postgres, ingress, and
backups. It assumes you already completed the local Run guide and now have:

- a PostgreSQL 17 database with durable storage and a tested backup plan;
- a process supervisor plus HTTPS ingress;
- a secret store and at least one active provider API key;
- Python 3.11+ on an operator workstation for smoke and load checks; and
- authority to stop, migrate, restart, and restore this installation.

For a first deployment, work through sections 1–5. Sections 6 onward are the
day-two stop, upgrade, backup, capacity, and incident procedures that must be
owned before production traffic. They are detailed because those choices
cannot be made safely by a local convenience command.

## Supported boundary

- PostgreSQL **17.x** is the supported and CI-tested database range for this
  initial profile. Postgres is operator-owned and must have durable storage,
  working backups, TLS or an equivalently protected local connection, and
  enough connection capacity for `DATABASE_MAX_CONNS` plus operator access.
- Run exactly one immutable `nvokend` binary or container with
  `NVOKEN_PROCESS_ROLE=combined` and `INVOCATION_EXECUTION_MODE=embedded`.
- Do not configure Redis, Cloud Tasks, an executor role, or another daemon
  replica. Those create a different, currently unsupported self-hosted profile.
- The operator owns process supervision, host replacement, ingress and TLS,
  secret storage and rotation, Postgres availability and sizing, backup
  scheduling, and the workload envelope.
- nvoken owns schema migrations, configuration validation, durable admission,
  execution claims and fences, checkpoints, ToolCall state, and authoritative
  reads.

The package contains:

| Artifact | Purpose |
| --- | --- |
| [`nvoken.env.example`](nvoken.env.example) | Secret-free, machine-checked canonical configuration. |
| [`smoke.py`](smoke.py) | Normal, restart, host ToolCall, and optional callback smoke paths. |
| [`load.py`](load.py) | Bounded admissions, reads, stream, queue, memory, and connection recorder. |
| [`failure-drills.md`](failure-drills.md) | Disposable process and dependency failure procedure. |
| [`runbooks.md`](runbooks.md) | First checks, safe actions, and recovery signals for profile incidents. |

Both Python tools use only the Python 3.11-or-newer standard library. They run
from an operator workstation, not inside the minimal daemon container.

## 1. Obtain one immutable build

Install the official Homebrew release on macOS or Linux and record exactly what
the supervisor will run:

```bash
brew install deepnoodle-ai/tap/nvoken
nvokend --version
command -v nvokend
```

The same checksummed archives are attached to the corresponding
[GitHub Release](https://github.com/deepnoodle-ai/nvoken/releases) for hosts
without Homebrew. Pin one version; do not allow an unattended package upgrade
to replace a running production binary.

When testing an unreleased source revision instead, check out and record it:

```bash
git checkout --detach <revision>
go build -trimpath -ldflags="-s -w -X main.buildVersion=<revision>" -o nvokend ./cmd/nvokend
sha256sum nvokend
```

Build the container with the same identity and pin its resulting digest in the
supervisor:

```bash
docker build --build-arg NVOKEN_BUILD_VERSION=<revision> -t nvoken:<revision> .
docker image inspect --format '{{index .RepoDigests 0}}' nvoken:<revision>
```

Do not deploy a mutable tag without separately recording its digest. A
successful start emits `process_started` with `build_version`, schema version,
role, mode, and enabled capabilities.

## 2. Configure the installation

Copy the checked example into the operator's secret store or a mode-0600
environment file. Never commit the populated copy.

```bash
install -m 600 deploy/single-daemon/nvoken.env.example /etc/nvoken/nvoken.env
openssl rand -hex 32
openssl rand -base64 32 | tr '+/' '-_' | tr -d '='
```

Generate independent values for `BOOTSTRAP_OWNER_SECRET`, the optional static
`RUNTIME_API_KEY`, and `CREDENTIAL_DELIVERY_KEY`; the last command produces the
required unpadded base64url form for a 32-byte delivery key. Supply at least one
of `ANTHROPIC_API_KEY` or `OPENAI_API_KEY` while
`INVOCATION_DEFAULT_CREDENTIAL_SOURCE=installation_byok`. Provider, callback,
database, identity, and encryption keys belong in an external secret store or
the protected environment file, never an image layer or command-line argument.

`NVOKEN_PUBLIC_BASE_URL` is the externally reachable HTTPS origin used by the
device authorization flow. Keep `NVOKEN_TRUST_FORWARDED_CLIENT_IP=false` unless
one trusted ingress overwrites the forwarded client-IP headers. The example's
connection, concurrency, timeout, drain, stream, budget, and retention values
are the initial safety bounds; change them only with a recorded load or
operational reason.

For reusable Account or tenant BYOK, configure both
`PROVIDER_CREDENTIAL_ACTIVE_KEY_ID` and
`PROVIDER_CREDENTIAL_ENCRYPTION_KEYS` as described in the
[credential guide](../../docs/guides/credentials-and-cli-auth.md). For callback
tools, configure `CALLBACK_SIGNING_KEY` and retain the exact key ID and version
alongside the receiver. Leaving the signing key blank disables callbacks.

## 3. Prepare Postgres and migrate

Create a dedicated database and role with permission to own nvoken's tables.
Use a percent-encoded URL and require certificate verification when the
connection crosses a host boundary. PostgreSQL service files and `.pgpass` are
recommended for interactive `psql`, backup, and load commands so credentials do
not appear in process arguments.

Apply the forward migrations as a serialized release operation before starting
the daemon. Normal startup never migrates:

```bash
set -a
. /etc/nvoken/nvoken.env
set +a
./nvokend migrate
./nvokend diagnose
```

`diagnose` is read-only and must report successful `configuration`,
`database_connectivity`, and `database_schema` checks. It never calls a model,
sends a callback, or mutates state. Stop if the schema is empty, dirty, behind,
or ahead of this binary.

## 4. Start and supervise the daemon

Give a real supervisor the populated environment and run exactly:

```bash
./nvokend serve
```

For the container, mount or inject the same environment and publish the port
only to the intended ingress:

```bash
docker run --name nvoken --env-file /etc/nvoken/nvoken.env \
  --publish 127.0.0.1:8080:8080 nvoken@sha256:<digest>
```

Configure the supervisor to send `SIGTERM`, wait at least `SHUTDOWN_TIMEOUT`,
and restart on unexpected exit. Do not use an aggressive liveness restart loop:
`GET /health` proves only that the process can serve HTTP and intentionally does
not query Postgres or providers. Put authentication, request-size controls, and
TLS at the ingress; do not expose Postgres publicly.

## 5. Smoke and restart readback

Run the read-only diagnostic first, then provide a currently available model
and a Runtime machine or static credential to the Python smoke tool. The tested
revision must be the exact Git revision or immutable image digest:

```bash
export NVOKEN_BASE_URL=https://nvoken.example.com
export NVOKEN_API_KEY=<runtime-credential>
export NVOKEN_TESTED_REVISION=<git-revision-or-image-digest>
export NVOKEN_SMOKE_PROVIDER=anthropic
export NVOKEN_SMOKE_MODEL=<current-model-name>
python3 deploy/single-daemon/smoke.py run
```

The run checks health, durable authenticated admission, terminal readback,
canonical transcript content, and an empty incremental read from the returned
resume cursor. It writes a mode-0600, secret-free state file containing only
profile, revision, durable IDs, cursor, and timestamps.

Send `SIGTERM` to the exact supervised process, wait for it to exit, start the
same immutable build, rerun `diagnose`, and verify Postgres readback:

```bash
python3 deploy/single-daemon/smoke.py verify-restart
```

Exercise a durable host ToolCall, including equal result replay:

```bash
python3 deploy/single-daemon/smoke.py host-tool
```

When callbacks are enabled, point the smoke at a public HTTPS receiver that
implements the [signed receiver contract](../../docs/guides/callback-receivers.md):

```bash
export NVOKEN_SMOKE_CALLBACK_URL=https://callbacks.example.com/nvoken/smoke
python3 deploy/single-daemon/smoke.py callback
```

Model/tool selection is probabilistic. A model that ignores an explicit smoke
tool instruction is a failed exercise, not evidence that the durable ToolCall
path ran.

## Day-two operations

The remaining sections are not extra first-run setup. They define how this
production profile is stopped, upgraded, recovered, measured, and investigated.

## 6. Stop, restart, and remove disposable data

For a graceful stop, send `SIGTERM` to the one exact PID or ask the supervisor
to stop its named unit. Do not use broad `pkill` patterns. The daemon stops
admitting work, drains owned execution and callback work within the configured
graces, releases claims, and exits inside `SHUTDOWN_TIMEOUT`. After restart,
Postgres polling and the reaper make retained queued or expired-lease work
eligible; a waiting host ToolCall remains parked without a goroutine.

There is intentionally no API for deleting authoritative Sessions or
Invocations. Put smoke and drill data in a dedicated disposable installation or
database. To remove it, stop the daemon, verify the exact disposable database
target and its backup policy, then have the Postgres owner drop that database.
Never delete individual uncertain runtime rows or reuse new IDs to replay an
external effect.

## 7. Upgrade and rollback

Record the current binary/image, build identity, schema verdict, active work,
and a fresh backup before an upgrade. Stop the one daemon, apply migrations with
the new immutable binary, run `diagnose`, start it, and repeat the smoke and
restart readback. A single daemon has an expected service interruption.

The current schema check requires an exact binary/schema match. Until
[PRD 019](../../docs/prds/019-prd-compatible-upgrades-and-rollback.md) supplies
and proves the declared one-release compatibility window, an ordinary rollback
is safe only when the migration version did not change. If it changed, do not
start the old binary against the newer database and do not edit migration rows.
Keep traffic stopped and follow the recorded restore decision instead. Restoring
a pre-upgrade backup discards work accepted after its recovery point and is not
an automatic rollback mechanism.

## 8. Logical backup and isolated restore

Postgres contains all authoritative nvoken runtime state. In-process events,
local files, and any future delivery adapter are not restore authorities.
Provider, callback, identity, and encryption secrets remain configuration
recovery concerns and are not part of the logical database dump.

Use PostgreSQL 17 host tools and credential-safe `PGSERVICE` entries. For the
initial drill, gracefully stop the daemon so no external effect or claim can
advance while the recovery point is taken:

```bash
PGSERVICE=nvoken-source pg_dump --format=custom --no-owner --no-acl \
  --file=/protected-backups/nvoken-<timestamp>.dump
PGSERVICE=nvoken-restore createdb nvoken_restore_<timestamp>
PGSERVICE=nvoken-restore pg_restore --single-transaction --no-owner --no-acl \
  --dbname=nvoken_restore_<timestamp> /protected-backups/nvoken-<timestamp>.dump
```

Point `nvokend diagnose` at the isolated restored database to check connectivity
and schema without starting execution components. Do not start the daemon
against a full restore containing claimable work merely to inspect it. The
bounded invariant verifier and qualifying restore evidence remain owned by
[PRD 020](../../docs/prds/020-prd-backup-restore-and-recovery.md); until that
lands, the readiness matrix correctly keeps backup/restore pending.

Backup scheduling, encryption, retention, restore access, and cleanup are the
operator's responsibility. Future live-data deletion cannot remove bytes from
retained backups immediately; backup expiry is part of any future deletion
promise.

## 9. Measure the local envelope

Run the bounded recorder on the exact machine/database shape you intend to use.
Create a libpq service and `.pgpass` entry for the database, then supply only a
nonsecret description to the evidence:

```bash
export PGSERVICE=nvoken-load
export NVOKEN_DAEMON_PID=<exact-pid>
export NVOKEN_LOAD_MACHINE='2 vCPU, 4 GiB RAM, Linux <version>'
export NVOKEN_LOAD_DATABASE='PostgreSQL 17.x, local SSD, <connection limit>'
export NVOKEN_LOAD_PROVIDER=anthropic
export NVOKEN_LOAD_MODEL=<current-model-name>
export NVOKEN_LOAD_REQUESTS=12
export NVOKEN_LOAD_CONCURRENCY=4
export NVOKEN_ENGINE_CONCURRENCY=8
export NVOKEN_DATABASE_MAX_CONNS=20
python3 deploy/single-daemon/load.py
```

The tool exercises bounded concurrent admissions, authoritative reads, one
transcript stream, and execution. It records admission/read latency, admission
throughput, maximum resident memory, database connections, queue age, terminal
status counts, and whether every acknowledgement remains queryable. Its output
is a reference observation, not an SLO or universal capacity guarantee. Increase
load gradually; saturation is acceptable only when acknowledged work remains
durably queued/active or settles through a documented bound.

## 10. Record evidence without duplicating readiness state

Use the
[single-daemon evidence template](../../docs/testing/readiness/evidence/single-daemon/README.md)
for smoke, failure, load, upgrade, and restore exercises. Records must name the
exact tested revision and stay free of credentials, prompts, transcript content,
callback bodies, and database URLs. Commit a reviewed record under that folder,
then update only the matching `single_daemon` row in the readiness matrix.

Procedures and scripts can be implemented while evidence remains pending. Mark
a row `proven` and `current` only after the linked exercise actually passed.

For incidents, use the [single-daemon runbooks](runbooks.md) and the shared
[event catalog](../../docs/guides/operational-signals.md).
