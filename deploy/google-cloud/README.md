# Google Cloud Run paved deployment

This Terraform root deploys nvoken's recommended Google Cloud topology: one
public combined Cloud Run service for the Runtime API and durable control-plane
loops, plus a private request-bound executor reached through Cloud Tasks. Every
new Invocation and its dispatch intent commit together in Postgres; the task
request then exact-claims and runs one bounded generation segment.

Set `invocation_execution_mode = "embedded"` to roll back to the combined
service's Postgres polling runner. Public API semantics do not change. Neither
mode provides checkpoint replay yet: an abruptly lost model segment becomes the
visible `execution_lost` failure after lease expiry.

## What it creates

- Artifact Registry, a least-privilege Cloud Build identity, a private
  short-lived source-staging bucket, and the APIs required by this root;
- a dedicated VPC, subnet, and private services connection;
- a private-IP-only Cloud SQL for PostgreSQL 17 instance with backups and PITR;
- generated database and Runtime credentials in Secret Manager;
- Secret Manager access for existing Anthropic and/or OpenAI key secrets,
  granted only to the configured generating role;
- separate runtime and database-migration service accounts with no project-wide
  application role;
- a one-task Cloud Run migration Job and a synthetic dispatch smoke Job;
- a public Cloud Run service with the edge Invoker IAM check disabled, Runtime
  bearer authentication, a startup probe, instance-based CPU, one minimum
  instance, and explicit capacity caps;
- a regional Cloud Tasks queue, transactional Postgres dispatch publisher and
  reconciler, and a dedicated OIDC caller identity; and
- an internal-ingress, IAM-protected executor Cloud Run service that receives
  the database and configured provider credentials, uses request-based CPU, and
  scales to zero.

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
agent grant is neither required nor created. Cloud SQL and the continuously
allocated Cloud Run minimum instance incur ongoing cost.

Terraform state contains the generated database password, database URL, and
Runtime bearer key. Keep it in a restricted, versioned GCS bucket; never commit
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
unique image tag with `TF_VAR_image_tag`; `latest` is rejected. For production,
consider `TF_VAR_database_availability_type=REGIONAL` and retain both database
and service deletion protection. Provider secret versions use `latest`, so
rotating a key requires a service revision to refresh its environment.

The Terraform root uses a GCS backend prefix of
`nvoken/${TF_VAR_environment}`. Set `NVOKEN_TF_STATE_LOCATION` before the first
bootstrap to place the bucket somewhere other than `TF_VAR_region` (which
defaults to `us-central1`). Bucket location cannot be changed later. Keep each
environment isolated, and serialize release jobs against its state lock.

## End-to-end smoke

Use a currently available model for the provider configured above:

```bash
export NVOKEN_SMOKE_PROVIDER='anthropic'
export NVOKEN_SMOKE_MODEL='your-current-model-name'
deploy/google-cloud/smoke.sh
```

The smoke test reads the generated Runtime bearer key directly from Secret
Manager without printing it, checks the Cloud Run-safe `/health` endpoint,
expects a `202` durable
acknowledgement, polls `GET /v1/invocations/{id}` to `completed`, performs a
second authoritative read, and confirms a structured Cloud Logging entry is
correlatable by Invocation ID. To prove restart readback during a release test,
deploy the next unique image revision and repeat the final `GET` with the same
ID; the state remains in Cloud SQL.

Cloud Run reserves some external paths ending in `z` at Google Front End and
can return a Google-generated `404` before the request reaches nvoken. nvoken
therefore uses `/health` consistently for local checks, Cloud Run startup and
liveness probes, and external smoke checks.

After the normal Runtime smoke passes, prove the private authenticated handoff:

```bash
deploy/google-cloud/dispatch-smoke.sh
```

The script executes the one-shot `dispatch-smoke` Job, reads its generated
dispatch ID from structured logs, and waits for the private executor to record
durable synthetic settlement. The job writes synthetic work and dispatch intent
in one transaction; the combined service publishes the named task, and the
executor reloads all authority from Postgres. No model or provider key is used.

To exercise recoverability, pause the queue, run the smoke Job, verify an aged
pending warning appears after `DISPATCH_STALE_AFTER`, and resume the queue. The
same Postgres dispatch then publishes and settles. Do not delete or manually
acknowledge an uncertain task. A published task that disappears is checked by
the reconciler; safely replayable synthetic work gets one new successor
dispatch, while already-settled work does not.

Use the Terraform `execution_queue` output with `gcloud tasks queues pause` and
`gcloud tasks queues resume`; always pass the Terraform `project_id` and
`region` outputs explicitly. To prove revision draining, temporarily set
`TF_VAR_synthetic_dispatch_delay_seconds=20`, start the dispatch smoke, and
deploy a new executor revision while its request is held. The old revision must
log `handler_outcome=settled` before it exits. Cancelling the held request
instead produces `503` and leaves both work and dispatch unsettled for delivery
retry. Return the delay to zero after the test.

Cloud Tasks retries only an HTTP request for which nvoken could not make a
durable decision. Missing, terminal, malformed, or duplicate synthetic
deliveries return `204`. A live duplicate Invocation delivery returns `503`
with `Retry-After`; once the authoritative attempt is terminal, redelivery is a
`204` no-op. Repeated `503` responses can back off the shared queue, so treat a
sustained executor-retry alert as a capacity or durability incident rather than
lost work.

Terraform creates alert policies for aged pending dispatches, stale published
dispatches, repeated publication failure, sustained executor retries, and
executor `401`/`403` responses.
A published Invocation with a running, unexpired Postgres lease is excluded
from stale-dispatch warnings because the dispatch row does not change while its
request legitimately runs. Once that lease expires, the dispatch becomes
alertable until the reaper or executor makes a durable decision.
Aged-state policies require five minutes of sustained observations, while
publication and authentication failures alert immediately. Set
`monitoring_notification_channels` to existing full Monitoring channel resource
names (for example,
`["projects/PROJECT/notificationChannels/CHANNEL"]`) before a production
rollout. The default empty list creates observable incidents but does not notify
an operator.

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

Cloud Run currently provides a ten-second termination window. The paved service
sets `SHUTDOWN_TIMEOUT=8s` and `ENGINE_DRAIN_GRACE=7s`. On `SIGTERM`, HTTP stops
accepting work and the engine stops claiming; cooperative work can finish inside
the shared budget before remaining calls are cancelled. Work that outlives the
platform process is still protected by the existing lease fence and reaper, but
is not resumable until the checkpoint PRD ships.

## Local validation

Validation uses mocked providers and creates no Google Cloud resources:

```bash
make check-deploy
```

Run `terraform plan` through `release.sh` against a disposable environment
before promoting changes. Destroying the production database is deliberately
blocked by default; changing deletion protection is a separate reviewed
operation.
