# Google Cloud Run paved deployment

This Terraform root deploys the current self-contained nvoken runtime to one
public Cloud Run service. The service admits HTTP requests and polls Postgres
for durable Invocation work in the same process. It therefore deliberately uses
instance-based CPU and keeps at least one instance running.

This is the small-installation path, not the final production scaling model.
Cloud Run request or CPU autoscaling does not track Postgres queue depth, and a
revision shutdown can interrupt a background turn that exceeds Cloud Run's
termination window. Such a turn becomes the visible pre-checkpoint
`execution_lost` failure after lease expiry. Deploy during quiet periods until
the later request-bound Cloud Tasks executor is available.

## What it creates

- Artifact Registry, a least-privilege Cloud Build identity, a private
  short-lived source-staging bucket, and the APIs required by this root;
- a dedicated VPC, subnet, and private services connection;
- a private-IP-only Cloud SQL for PostgreSQL 17 instance with backups and PITR;
- generated database and Runtime credentials in Secret Manager;
- Secret Manager access for existing Anthropic and/or OpenAI key secrets;
- separate runtime and database-migration service accounts with no project-wide
  application role;
- a one-task Cloud Run migration Job; and
- a public Cloud Run service with the edge Invoker IAM check disabled, Runtime
  bearer authentication, a startup probe, instance-based CPU, one minimum
  instance, and explicit capacity caps.

The defaults cap the installation at three service instances, four model turns
per instance, and ten Postgres connections per instance: at most 12 executing
turns and 30 pooled database connections. These are configuration ceilings, not
an autoscaling guarantee.

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

## Capacity and shutdown

`request_concurrency`, `engine_concurrency`, `database_max_connections`, and
`max_instances` are separate limits. Size Cloud SQL for
`max_instances * database_max_connections`; do not infer engine autoscaling
from API traffic. At least one instance and instance-based CPU are correctness
requirements for the Postgres poller after an admission request returns.

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
