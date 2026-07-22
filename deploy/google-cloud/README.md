# Google Cloud Run paved deployment

This Terraform root deploys nvoken's recommended Google Cloud topology: one
public combined Cloud Run service for the Runtime API and durable control-plane
loops, plus a private request-bound executor reached through Cloud Tasks. Every
new Invocation and its dispatch intent commit together in Postgres; the task
request then exact-claims and runs one bounded generation segment.

This guide defines the topology and procedures; it does not independently make
a production-readiness claim. The `google_cloud` profile boundary, current
status, and required evidence live in the
[production-readiness profiles and evidence matrix](../../docs/testing/production-readiness-profiles.md).

Set `invocation_execution_mode = "embedded"` to roll back to the combined
service's Postgres polling runner. Public API semantics do not change. Neither
mode treats delivery as ownership: an abruptly lost model segment is requeued
from its last validated checkpoint after lease expiry, while Postgres fences
stale writers.

## What it creates

- Artifact Registry, a least-privilege Cloud Build identity, a private
  short-lived source-staging bucket, and the APIs required by this root;
- a dedicated VPC, subnet, and private services connection;
- a private-IP-only Cloud SQL for PostgreSQL 17 instance with backups and PITR;
- a private basic-tier Memorystore for Redis instance used only for lossy live
  output Pub/Sub between executor and Runtime replicas;
- generated database, initial Runtime, bootstrap Owner, and bounded credential-delivery secrets in Secret Manager;
- Secret Manager access for existing Anthropic and/or OpenAI key secrets,
  granted only to the configured generating role;
- optional access to an existing callback HMAC secret, granted only to the
  combined service that owns callback delivery;
- separate runtime and database-migration service accounts with no project-wide
  application role;
- a one-task Cloud Run migration Job and a synthetic dispatch smoke Job;
- a public Cloud Run service with the edge Invoker IAM check disabled, Runtime
  bearer authentication, a canonical device-approval origin, a startup probe,
  instance-based CPU, one minimum instance, and explicit capacity caps;
- a regional Cloud Tasks queue, transactional Postgres dispatch publisher and
  reconciler, and a dedicated OIDC caller identity; and
- an internal-ingress, IAM-protected executor Cloud Run service that receives
  the database and configured provider credentials, uses request-based CPU, and
  scales to zero;
- one Cloud Monitoring operations dashboard spanning Runtime, execution,
  callbacks, providers, Cloud Tasks, Cloud SQL, and Redis; and
- a public health uptime check plus conservative alert policies linked to
  [alert-specific runbooks](runbooks.md).

The default Cloud Tasks mode caps the queue at 40 concurrent requests, matching
ten executor instances at concurrency four. The combined service keeps one
minimum instance and instance-based CPU because publication, reconciliation,
queued-work repair, and lease/deadline reaping remain background duties. Size
Cloud SQL for both the combined and executor pool ceilings. These are capacity
limits, not an autoscaling guarantee.

## Prerequisites

Install `gcloud`, Terraform 1.9 or newer, `curl`, and `jq`. Authenticate `gcloud`
to a disposable or deliberately selected project with permission to enable
services and create IAM, networking, Cloud SQL, Secret Manager, Artifact
Registry, Cloud Storage, Cloud Build, and Cloud Run resources. The release
caller must also be allowed to act as the generated Cloud Build service account.
The automatically managed, same-project Cloud Build service agent already has
the token permissions required by Cloud Build; an extra cross-project service
agent grant is neither required nor created. Cloud SQL, Memorystore, and the
continuously allocated Cloud Run minimum instance incur ongoing cost.

Terraform state contains the generated database password, database URL,
initial Runtime bearer, bootstrap Owner secret, and credential-delivery key.
Keep it in a restricted, versioned GCS bucket; never commit
state or a secret-bearing `.tfvars` file. The bucket must be bootstrapped outside
the Terraform root because a root cannot safely own its own backend:

```bash
export TF_VAR_project_id='your-google-cloud-project'
export NVOKEN_TF_STATE_BUCKET='your-protected-terraform-state-bucket'

deploy/google-cloud/bootstrap-state.sh
```

The idempotent bootstrap enables the Cloud Storage API, creates the bucket when
absent, and enforces object versioning, uniform bucket-level access, and public
access prevention. `release.sh` calls it again before every `terraform init`,
so the explicit bootstrap command is useful for proving access ahead of the
first release but is not otherwise required. Restrict bucket IAM to the release
identity and the small set of administrators who may recover infrastructure.

Create at least one provider key as a Secret Manager version. The key value
travels over stdin and never enters Terraform state:

```bash
gcloud services enable secretmanager.googleapis.com --project="${TF_VAR_project_id}"
gcloud secrets create nvoken-anthropic-api-key \
  --project="${TF_VAR_project_id}" \
  --replication-policy=automatic
printf '%s' "${ANTHROPIC_API_KEY}" | gcloud secrets versions add \
  nvoken-anthropic-api-key \
  --project="${TF_VAR_project_id}" \
  --data-file=-

export TF_VAR_anthropic_api_key_secret_id='nvoken-anthropic-api-key'
```

Use `TF_VAR_openai_api_key_secret_id` the same way for OpenAI. Either provider
is sufficient and both may be configured together. Do not put the provider key
itself in a Terraform variable.

The public service receives `NVOKEN_PUBLIC_BASE_URL` as its canonical device
approval origin. By default Terraform computes Cloud Run's
[documented deterministic `run.app` URL](https://cloud.google.com/run/docs/triggering/https-request)
before the service exists. Set
`TF_VAR_public_base_url='https://nvoken.example'` when a custom domain is the
canonical user-facing origin. The paved service also trusts the Google Front
End's `X-Forwarded-For` client address for its bounded in-process device-flow
limits; self-managed deployments should enable that setting only behind a
trusted proxy.

To enable reusable or caller-ephemeral BYOK, create a separate Secret Manager version
containing a JSON object of base64-encoded 32-byte encryption keys, then set its
secret ID and the active nonsecret key ID together:

```bash
nvoken_credential_key="$(openssl rand -base64 32)"
gcloud secrets create nvoken-provider-credential-keys \
  --project="${TF_VAR_project_id}" \
  --replication-policy=automatic
printf '{"v1":"%s"}' "${nvoken_credential_key}" | gcloud secrets versions add \
  nvoken-provider-credential-keys \
  --project="${TF_VAR_project_id}" \
  --data-file=-
unset nvoken_credential_key

export TF_VAR_provider_credential_encryption_keys_secret_id='nvoken-provider-credential-keys'
export TF_VAR_provider_credential_active_key_id='v1'
```

The combined service can always read this keyring for admission and lifecycle
operations; the private executor can read it only in split `cloud_tasks` mode.
The migration job cannot read it. An absent keyring safely disables encrypted
BYOK in this self-hosted-in-your-project profile. Missing, malformed, or partial
keyring configuration fails service startup rather than persisting plaintext or
falling back to another credential source. Keep retired keys in the JSON object
while retained ciphertext may still reference them, and deploy a new revision
after changing a Secret Manager `latest` version.

To enable callback tools, create a separate random secret of at least 32 bytes
and set `TF_VAR_callback_signing_key_secret_id`. Set the nonsecret
`TF_VAR_callback_signing_key_id` and positive
`TF_VAR_callback_signing_key_version` to values receivers recognize. The
combined service alone can access the secret; the private executor and
migration job cannot. A Secret Manager version rotation must update the
receiver and key-version variable and deploy a new service revision together.
This HMAC rollout does not yet provide overlapping-key or JWKS rotation.

## Release

From a clean committed checkout, choose a short environment and run:

```bash
export TF_VAR_environment='dev'
deploy/google-cloud/release.sh
```

The script derives an immutable image tag from the current Git commit and
performs the release in this order:

1. bootstrap the Artifact Registry repository;
2. build and push the image with Cloud Build;
3. update only the migration Job and its prerequisites;
4. execute `nvokend migrate` and wait for success; then
5. plan and apply the service revision.

A migration failure exits before step 5, leaving serving traffic on the prior
image. The job has one task, no retries, a bounded migration timeout, and the
database advisory lock already enforced by `nvokend migrate`. Ordinary service
startup checks the exact schema and never runs DDL.

Cloud Build reads uploaded source only from its dedicated bucket, whose objects
expire after seven days; it has no project-wide Storage Viewer role. The
migration job likewise uses a distinct identity that can read only the database
URL secret. Direct private-IP Postgres connections require TLS, and the Cloud
SQL instance rejects unencrypted clients. This direct-IP `sslmode=require` path
encrypts transport but does not verify a DNS hostname against the server CA;
the dedicated VPC remains the peer-access boundary for this small-installation
topology.

Set `NVOKEN_DEPLOY_AUTO_APPROVE=1` only in a reviewed CI release. Override the
unique image tag with `TF_VAR_image_tag`; `latest` is rejected. Provider secret
versions use `latest`, so rotating a key requires a service revision to refresh
its environment.

The Terraform root uses a GCS backend prefix of
`nvoken/${TF_VAR_environment}`. Set `NVOKEN_TF_STATE_LOCATION` before the first
bootstrap to place the bucket somewhere other than `TF_VAR_region` (which
defaults to `us-central1`). Bucket location cannot be changed later. Keep each
environment isolated, and serialize release jobs against its state lock.

## Production baseline

The cheap development defaults are deliberately not a production claim. Before
the Google profile is marked production ready, record all required rows below
in the PRD 017 readiness matrix. Until that matrix and the PRD 020 restore drill
exist, the corresponding rows remain `pending`.

| Setting or proof | Production baseline | Classification |
| --- | --- | --- |
| Cloud SQL availability | `database_availability_type = "REGIONAL"` | Required safety setting |
| Cloud SQL deletion protection | `database_deletion_protection = true` | Required safety setting |
| Cloud Run deletion protection | `service_deletion_protection = true` | Required safety setting |
| Alert delivery | At least one channel in `monitoring_notification_channels`, tested end to end | Required safety setting |
| Capacity reconciliation | Review the `configured_capacity_totals` output against Cloud SQL connections, queue concurrency, quotas, and provider limits | Required safety setting |
| Backup and recovery | Exercise the PRD 020 Cloud SQL backup/PITR restore into isolation and record the verified recovery point | Required safety proof |
| Service, executor, queue, database, and Redis sizing | Choose from observed workload and qualification evidence | Workload-dependent advice |
| Alert thresholds and windows | Tune only from incident/volume evidence; preserve a sustained window for noisy provider and callback failures | Workload-dependent advice |

The defaults use zonal Cloud SQL, leave Cloud Run deletion protection off, and
permit an empty notification list so a disposable deployment stays usable.
For production, set the three required Terraform values explicitly even where a
current default happens to match:

```bash
export TF_VAR_database_availability_type='REGIONAL'
export TF_VAR_database_deletion_protection='true'
export TF_VAR_service_deletion_protection='true'
export TF_VAR_monitoring_notification_channels='["projects/PROJECT/notificationChannels/CHANNEL"]'
```

The default database connection alert opens above 80 connections. Confirm that
this remains below the selected instance's actual `max_connections`, with room
for the summed runtime/executor pool ceilings, migrations, and operator access.
The alert threshold is independent of `configured_capacity_totals`; Terraform
does not derive it from the declared pools or the selected Cloud SQL tier. Any
pool, instance-count, queue-concurrency, or database-tier change therefore
requires an explicit capacity reconciliation and threshold review.
Change `monitoring_alert_thresholds` and `monitoring_alert_windows_seconds` as
reviewed objects when the deployment needs different conservative bounds.

## End-to-end qualification

Use the one Python 3.11+ runner for live checks. Start with discovery-only mode;
it reads Terraform and Google metadata but makes no provider request or resource
mutation:

```bash
python3 deploy/google-cloud/qualify.py \
  --environment staging \
  --provider anthropic \
  --model your-current-model-name \
  --callback-fixture-url https://your-controlled-fixture.example/qualify \
  --notification-channel projects/PROJECT/notificationChannels/CHANNEL \
  --terraform-var-file /absolute/path/to/staging.tfvars \
  --dry-run
```

Remove `--dry-run` only in a disposable or staging environment. The runner
prints the resolved project, region, revisions, immutable image, queue, Redis
instance, bounds, Terraform plan status, and intended temporary mutations. It
then requires the exact project ID before changing queue state, Terraform
limits, Redis size, or known qualification tasks. It owns cleanup after success,
failure, or interruption and writes a scrubbed result under
`docs/testing/readiness/evidence/`.

Use repeated `--scenario` flags to rerun a failed scenario without inventing a
second entry point. For example:

```bash
python3 deploy/google-cloud/qualify.py \
  --environment staging \
  --provider anthropic \
  --model your-current-model-name \
  --scenario baseline
```

The runner reads the generated legacy Runtime bearer from Secret Manager without
printing or recording it. After credential cutover, set a managed token only for
the child process through `NVOKEN_QUALIFICATION_RUNTIME_TOKEN`; never pass it as
a command-line flag. Keep `retain_legacy_runtime_key = true` while the previous
release must remain startable, then revoke the imported credential before
setting it false.

The complete scenario contract, callback-fixture behavior, mutation boundaries,
and evidence semantics are in the
[Google Cloud qualification procedure](../../docs/testing/google-cloud-qualification.md).
The former `smoke.sh` and `dispatch-smoke.sh` wrappers are retired; their public
Runtime, authoritative readback, structured-log, and synthetic dispatch checks
are covered by `qualify.py`.

Cloud Tasks retries only a request for which nvoken could not make a durable
decision. Missing, terminal, malformed, or duplicate synthetic deliveries return
`204`. A live duplicate Invocation delivery returns `503` with `Retry-After`;
once the authoritative attempt is terminal, redelivery is a `204` no-op.
Repeated `503` responses can back off the shared queue, so treat a sustained
executor-retry alert as a capacity or durability incident rather than lost work.

## Dashboard, alerts, and notifications

Terraform creates one dashboard by default. Set
`enable_monitoring_dashboard = false` only when another managed view replaces
it. The dashboard shows public request volume/5xx/latency, uptime checks,
Invocation settlement outcomes, provider outcome/latency, callback activity,
aged dispatch evidence, dispatch/executor outcomes, Cloud Tasks depth/attempt
delay, Cloud SQL connections/storage, and Redis memory/clients. Empty charts
mean **unknown or no events**, not healthy. Log-based metrics begin collecting
only after Terraform creates them.

Google Cloud Tasks exposes queue depth, attempt count, and attempt delay but no
native oldest-task-age time series. The dashboard therefore composes runnable
delivery evidence from nvoken's `oldest_age_ms` on bounded aged-dispatch events,
Cloud Tasks depth, and p95 attempt delay. A published Invocation with a running,
unexpired Postgres lease is excluded from stale-dispatch warnings because the
dispatch row does not change while its request legitimately runs. Once that
lease expires, the dispatch becomes alertable until the reaper or executor
makes a durable decision.

The alert set covers:

- sustained public 5xx or failed `/health` uptime checks;
- aged pending/published work and dispatch publication failure;
- repeated executor/task rejection, including immediate executor `401`/`403`;
- sustained provider failure, with one ordinary failure below threshold;
- callback exhaustion or repeated worker failure; and
- imminent Cloud SQL connection/storage exhaustion plus sustained
  `FAILED`/`UNKNOWN_STATE` instance health.

Every policy links its procedure in [runbooks.md](runbooks.md). Terraform does
not add a backup-success alert because Cloud SQL does not expose a reliable
per-instance backup-success time series for this package; the exercised PRD 020
backup/PITR procedure remains the proof.

`monitoring_notification_channels` accepts existing full Monitoring channel
resource names. The default empty list still creates dashboards, incidents, and
runbook links but not operator notification. Confirm this boundary after apply:

```bash
terraform -chdir=deploy/google-cloud output monitoring_notifications_configured
terraform -chdir=deploy/google-cloud output monitoring_notification_channels
terraform -chdir=deploy/google-cloud output monitoring_dashboard_id
```

Cloud Monitoring has no generic test operation for every channel type. In a
disposable environment, use temporary safe thresholds or the controlled
failures in the [Google Cloud qualification procedure](../../docs/testing/google-cloud-qualification.md),
observe both the open and recovery notification, then restore the reviewed
threshold object through Terraform. Do not call a channel tested merely because
it appears in Terraform state. At least one attached channel must have recorded
delivery evidence before the profile's notification row can leave `pending`.

For planned rollback, first pause the execution queue and let active executor
requests drain when possible. Apply with
`TF_VAR_invocation_execution_mode=embedded`; the combined service then resumes
Postgres polling and newly admitted Invocations stop creating dispatches. Old
tasks may still race the embedded runner, but the Session/Invocation claim fence
allows only one generation owner. Keep the queue paused until active dispatches
are terminal or known harmless. Do not delete uncertain tasks, dispatch rows, or
Terraform state to force convergence.

For enablement in the other direction, apply Cloud Tasks mode before resuming
the queue. The combined service periodically creates one dispatch for any
queued Invocation admitted by an older embedded revision, so rollout overlap
cannot strand accepted work. Active uniqueness makes the repair idempotent.
Terminal dispatch diagnostics are pruned in bounded batches after seven days;
authoritative Invocation and transcript rows are retained.

## Retention and storage growth

The paved profile follows the shared
[retain-by-default policy](../../docs/guides/data-retention.md). Postgres in
Cloud SQL is authoritative; Memorystore previews and Cloud Tasks deliveries do
not replace its history. The combined service uses the documented seven-day,
100-row defaults for terminal execution-dispatch and callback-delivery
diagnostics unless their `DISPATCH_*` or `CALLBACK_*` environment settings are
explicitly overridden.

In Cloud Monitoring, select resource type **Cloud SQL Database**, filter to the
instance identified by the Terraform `cloud_sql_instance` output, and graph the
[Cloud SQL storage metric](https://cloud.google.com/sql/docs/postgres/admin-api/metrics)
`cloudsql.googleapis.com/database/disk/bytes_used`. The related
`cloudsql.googleapis.com/database/disk/utilization` signal shows the fraction of
the current disk quota in use. The Cloud SQL instance Overview and System
Insights pages expose the same storage-usage family. Terraform enables disk
autoresize, but operators should still watch absolute usage and its growth rate
so retention does not become an unplanned capacity or privacy boundary.

Use the metadata-only Postgres queries in the shared guide to identify the
largest nvoken tables. They complement the instance-level Cloud SQL signal and
do not read transcript content. This slice adds neither a per-tenant breakdown
nor an automatic scaling or alert policy.

Cloud SQL backups and point-in-time recovery retain older database versions
separately from live rows. A future Session or tenant deletion contract must
include backup expiry; deleting live data will not immediately erase it from
retained backups. Compaction, cursor behavior, and archive/export remain future
contracts.

## Capacity and shutdown

`request_concurrency`, `engine_concurrency`, `database_max_connections`, and
`max_instances` are separate combined-service limits. In Cloud Tasks mode,
`engine_concurrency` applies only if rolling back to embedded execution.
Executor request concurrency, queue concurrency, and executor instance/database
limits are also separate. Terraform rejects queue concurrency above declared
executor request capacity. Each executor pool reserves one connection for
cancellation notifications, so its configured maximum must be at least two.
Size Cloud SQL for
`max_instances * database_max_connections`; do not infer engine autoscaling
from API traffic, and add the executor connection ceiling before sizing the
database. At least one combined instance and instance-based CPU remain
correctness requirements for its Postgres publisher, reconciliation/repair,
and lease/deadline reaper after an admission request returns.

The executor request and Cloud Tasks dispatch deadline are 1,800 seconds. The
application attempt ceiling is 1,795 seconds. The default stored execution
segment deadline is 900 seconds and model work stops five seconds before that
deadline for settlement. Terraform rejects a segment ceiling beyond the
application attempt timeout and an application timeout that does not leave the
same reserve before the platform deadline.

The public Cloud Run request ceiling is 3,600 seconds and nvoken deliberately
rotates an active SSE stream after 3,300 seconds. Every stream write is bounded
to ten seconds, keepalive comments are sent every fifteen seconds, and a
one-second Postgres poll remains the correctness fallback. These values are
separate from Invocation and executor deadlines. The Runtime and executor both
receive the private TLS Memorystore endpoint and generated AUTH secret; Redis
carries no provider/runtime credentials, transcript authority, task ownership,
or execution fence.

Cloud Run currently provides a ten-second termination window. The paved service
sets `SHUTDOWN_TIMEOUT=8s`, `ENGINE_DRAIN_GRACE=7s`, and
`CALLBACK_DRAIN_GRACE=7s`. On `SIGTERM`, HTTP stops accepting work and both
workers stop claiming; cooperative model work and callback requests can finish
inside the shared budget before remaining calls are cancelled. Work that
outlives the platform process is protected by its lease fence and recovered
from durable checkpoints or delivery rows.

## Local validation

Validation uses mocked providers and creates no Google Cloud resources:

```bash
make check-deploy
```

Run `terraform plan` through `release.sh` against a disposable environment
before promoting changes. Destroying the production database is deliberately
blocked by default; changing deletion protection is a separate reviewed
operation.
