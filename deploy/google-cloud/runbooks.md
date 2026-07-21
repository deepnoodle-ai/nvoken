# Google Cloud alert runbooks

These procedures are for alerts created by the paved deployment. Resolve the
Terraform outputs `project_id`, `region`, `service_name`,
`executor_service_name`, `execution_queue`, and `cloud_sql_instance_name`
before running a command. Use the instance name, not the connection name, for
`gcloud sql` commands. Keep the project and region explicit.

Postgres is authoritative for Sessions, Invocations, transcript messages,
ToolCalls, checkpoints, and execution ownership. Cloud Tasks is delivery only,
and Redis carries lossy live previews only. Never delete an uncertain task or
runtime row, rewrite lifecycle state, or submit a callback/tool result under a
new identity to make an alert disappear.

## Runtime unavailable or 5xx

**Meaning.** The public `/health` check failed from at least half of the selected
regions for the configured window, or the public Cloud Run service sustained
more 5xx responses than the configured threshold.

**First queries.** Check Cloud Run service/revision health, then inspect bounded
request records without request bodies:

```bash
gcloud run services describe SERVICE_NAME \
  --project=PROJECT_ID --region=REGION
gcloud logging read \
  'resource.type="cloud_run_revision" AND resource.labels.service_name="SERVICE_NAME" AND (httpRequest.status>=500 OR jsonPayload.msg="http request" AND jsonPayload.status>=500)' \
  --project=PROJECT_ID --freshness=30m --limit=100
```

**Correlate.** Use revision name, request ID, route, status, and latency. Do not
copy authorization headers, request bodies, prompts, or model output into an
incident.

**Safe mitigation.** Stop a bad rollout and return traffic to the last known
compatible revision. If the database is the failing dependency, preserve the
accepted rows and address Cloud SQL rather than restarting the service in a
loop. A health-check failure alone is not evidence that queued work is lost.

**Recovery.** `/health` passes from the configured regions, 5xx volume falls
below threshold, and durable Invocation reads still return the accepted work.

**Escalate.** Escalate when the prior compatible revision also fails, the
database is unavailable, or acknowledgements cannot be read back after service
recovery.

## Aged runnable work

**Meaning.** A pending dispatch has not published, or a published dispatch has
not reached a durable decision, for the configured sustained window. The
dashboard pairs these events with Cloud Tasks queue depth and attempt delay;
Cloud Tasks does not publish a native oldest-task-age metric.

**First queries.** Inspect the bounded aged event, queue state, and executor
attempts:

```bash
gcloud logging read \
  'resource.type="cloud_run_revision" AND (jsonPayload.event="dispatch_aged_pending" OR jsonPayload.event="dispatch_stale_published")' \
  --project=PROJECT_ID --freshness=1h --limit=100
gcloud tasks queues describe QUEUE_NAME \
  --project=PROJECT_ID --location=REGION
gcloud logging read \
  'resource.type="cloud_run_revision" AND resource.labels.service_name="EXECUTOR_SERVICE_NAME" AND (jsonPayload.event="dispatch_attempt_retry" OR jsonPayload.event="dispatch_attempt_decided")' \
  --project=PROJECT_ID --freshness=1h --limit=100
```

**Correlate.** Use `oldest_dispatch_id`, `oldest_dispatch_status`,
`oldest_age_ms`, dispatch ID, Invocation ID, lease attempt, and executor
`handler_outcome`. IDs are investigation fields, not metric labels.

**Safe mitigation.** If delivery is amplifying a dependency incident, pause the
queue with `gcloud tasks queues pause`, repair the dependency, then resume with
`gcloud tasks queues resume`. For a planned execution-mode rollback, pause and
drain the queue before applying `invocation_execution_mode = "embedded"` as
documented in the deployment guide. Let the reconciler and Postgres fences
converge duplicate delivery.

**Recovery.** Aged events stop, queue depth/attempt delay return to their normal
range, and the affected Invocation reaches a durable terminal or intentional
waiting state.

**Escalate.** Escalate when the queue is running and authenticated but aged work
persists through a publisher/reconciler interval, or when Postgres ownership and
the observed delivery outcome disagree.

## Dispatch publication failure

**Meaning.** The combined service could not create the deterministic Cloud Task
for a claimed Postgres dispatch intent.

**First queries.** Inspect publication failures and Cloud Tasks API errors:

```bash
gcloud logging read \
  'resource.type="cloud_run_revision" AND jsonPayload.event="dispatch_publish_failure"' \
  --project=PROJECT_ID --freshness=30m --limit=100
gcloud logging read \
  'resource.type="audited_resource" AND protoPayload.serviceName="cloudtasks.googleapis.com" AND severity>=ERROR' \
  --project=PROJECT_ID --freshness=30m --limit=100
```

**Correlate.** Use dispatch ID, dispatch kind, publish-attempt count, service
revision, and Cloud Tasks canonical error. Treat raw provider error text as
internal incident data.

**Safe mitigation.** Restore Cloud Tasks API, IAM, quota, or networking. Keep
the combined service running so its publisher and reconciler can retry. An
`AlreadyExists` response is normal convergence for a deterministic task name.

**Recovery.** Publication-failure events stop and the same authoritative
dispatch becomes published or terminal without creating another Invocation.

**Escalate.** Escalate on repeated permission errors after an unchanged
Terraform apply, or if a pending dispatch disappears from Postgres.

## Executor delivery rejections

**Meaning.** Cloud Tasks received repeated non-OK responses, nvoken repeatedly
returned a retryable `503`, or the private executor rejected task OIDC with
`401`/`403`.

**First queries.** Inspect executor request and bounded retry outcomes:

```bash
gcloud logging read \
  'resource.type="cloud_run_revision" AND resource.labels.service_name="EXECUTOR_SERVICE_NAME" AND (httpRequest.status>=400 OR jsonPayload.event="dispatch_attempt_retry")' \
  --project=PROJECT_ID --freshness=30m --limit=100
gcloud run services get-iam-policy EXECUTOR_SERVICE_NAME \
  --project=PROJECT_ID --region=REGION
gcloud tasks queues describe QUEUE_NAME \
  --project=PROJECT_ID --location=REGION
```

**Correlate.** Use dispatch ID, `retry_reason`, HTTP status, executor revision,
task caller service account, and OIDC audience. Never log or paste an identity
token.

**Safe mitigation.** Re-apply the reviewed Terraform IAM/audience configuration
for authentication failures. For capacity or database failures, pause the queue
if retries are amplifying load, restore the dependency or increase reviewed
capacity, then resume. Duplicate requests are safe only because the executor
reloads and fences against Postgres.

**Recovery.** Executor authentication succeeds, non-OK attempt rate falls below
threshold, and the original dispatch reaches a durable decision.

**Escalate.** Escalate when the configured task caller has `run.invoker` and the
audience matches the stable executor URL but requests remain unauthenticated, or
when a retrying request cannot load its authoritative row.

## Repeated provider failure

**Meaning.** Provider generation failures exceeded the conservative sustained
threshold. A single normal provider rejection does not open this alert.
Provider work interrupted by lease loss, client cancellation, or the execution
deadline is recorded with `outcome=canceled` for dashboard diagnosis and does
not feed this failure alert.

**First queries.** Group the bounded failure classes, then check the provider's
status and the configured Secret Manager version without reading the secret:

```bash
gcloud logging read \
  'resource.type="cloud_run_revision" AND jsonPayload.event="provider_generation" AND jsonPayload.outcome="failed"' \
  --project=PROJECT_ID --freshness=30m --limit=100
gcloud secrets versions list PROVIDER_SECRET_ID \
  --project=PROJECT_ID --filter='state=ENABLED'
```

**Correlate.** Use failure class, provider, requested model, Invocation ID,
lease attempt, executor revision, and latency. Never include the provider key,
prompt, tool payload, or remote response body.

**Safe mitigation.** Correct a disabled/missing deployment secret or roll back a
bad revision. For provider throttling/outage, reduce new admission or wait for
recovery; do not create replacement Invocations for acknowledged work. A
checkpointed Invocation remains authoritative and follows its existing retry,
deadline, and budget contract.

**Recovery.** Success outcomes resume, failure rate stays below threshold, and
affected Invocations settle durably or expose their typed failure.

**Escalate.** Escalate for invalid-response classes after a provider/API version
change, or when all configured providers fail despite valid deployment config.

## Callback exhaustion or worker failure

**Meaning.** A callback delivery reached durable `failed` status or the callback
worker repeatedly failed to claim, process, recover, or prune deliveries.

**First queries.** Inspect only bounded callback lifecycle fields:

```bash
gcloud logging read \
  'resource.type="cloud_run_revision" AND (jsonPayload.event="callback_delivery_settled" OR jsonPayload.event="callback_delivery_retry" OR jsonPayload.event="callback_claim_failed" OR jsonPayload.event="callback_process_failed" OR jsonPayload.event="callback_recovery_failed")' \
  --project=PROJECT_ID --freshness=1h --limit=100
```

**Correlate.** Use delivery ID, ToolCall ID, attempt, delivery status,
`reason_code`, Invocation ID, and service revision. Callback URLs, request/result
bodies, signatures, and signing material must stay out of logs and tickets.

**Safe mitigation.** Restore the receiver, DNS/TLS path, signing configuration,
or database dependency. Let the existing delivery ID retry. The receiver must
return the first stored result for an equal ToolCall retry; never replay an
external effect under a new ID.

**Recovery.** Retries stop, the original delivery settles once, and the parked
Invocation either resumes from the accepted result or exposes its durable tool
failure.

**Escalate.** Escalate when receiver evidence and nvoken's accepted result
disagree, signature verification fails after an unchanged key rollout, or a
stale callback owner appears able to commit.

## Cloud SQL capacity exhaustion

**Meaning.** PostgreSQL connections exceeded the configured operator threshold
or disk utilization remained above the configured ratio. The connection
threshold must be set below the instance's actual `max_connections` with room
for migrations and operator access.

**First queries.** Inspect Cloud SQL state/capacity and compare it with the
Terraform `configured_capacity_totals` output:

```bash
gcloud sql instances describe CLOUD_SQL_INSTANCE \
  --project=PROJECT_ID
terraform -chdir=deploy/google-cloud output configured_capacity_totals
```

**Correlate.** Use database instance, configured tier/storage, runtime and
executor revision counts, pool ceilings, queue depth, and alert condition. Do
not run content queries against transcript or tool payload columns.

**Safe mitigation.** Stop an accidental replica/traffic surge, reduce reviewed
pool or queue concurrency, or scale Cloud SQL storage/tier through Terraform.
Pause task delivery if retries amplify database pressure. Do not kill unknown
transactions, delete queued rows, or lower deletion protection as an incident
shortcut.

**Recovery.** Connection and storage utilization remain below threshold, queue
age recovers, and accepted Invocations are readable and progress normally.

**Escalate.** Escalate when storage cannot auto-grow, connection use remains
above the declared total, or integrity/readback checks fail after capacity
recovers.

## Cloud SQL failed or unknown

**Meaning.** Cloud Monitoring reported the Cloud SQL instance in `FAILED` or
`UNKNOWN_STATE` for the configured sustained window. Planned maintenance is not
included in this alert.

**First queries.** Inspect instance state, operations, and service-side database
failures:

```bash
gcloud sql instances describe CLOUD_SQL_INSTANCE \
  --project=PROJECT_ID
gcloud sql operations list \
  --project=PROJECT_ID --instance=CLOUD_SQL_INSTANCE --limit=20
gcloud logging read \
  'resource.type="cloud_run_revision" AND severity>=ERROR AND jsonPayload.msg:"database"' \
  --project=PROJECT_ID --freshness=30m --limit=100
```

**Correlate.** Use Cloud SQL operation, instance state, region, service revision,
and the time of the last successful durable read. Secrets and database URLs must
not enter incident records.

**Safe mitigation.** Follow Google Cloud recovery guidance and the exercised
PRD 020 backup/PITR procedure. Keep traffic off any isolated restore until the
restore verifier passes and an explicit promotion plan accounts for external
delivery state. Do not point production services at an unverified rewind.

**Recovery.** Cloud SQL returns to `RUNNING`, nvoken can read authoritative
records, and queued/checkpointed work progresses without manual state edits.

**Escalate.** Escalate immediately for regional failure, failed recovery
operations, corruption evidence, or any need to promote a restored database.
