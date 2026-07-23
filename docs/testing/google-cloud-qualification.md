# Google Cloud qualification procedure

**Status:** Implemented; staging run pending. Governed by
[`024-prd-google-cloud-qualification.md`](../prds/024-prd-google-cloud-qualification.md).

## Purpose and claim

This procedure closes a known evidence gap: local tests prove nvoken's
correctness rules and Terraform intent, but only a real Google deployment can
show how the public Runtime, private executor, Cloud Tasks, Cloud SQL,
Memorystore, Secret Manager, callbacks, and Monitoring behave together.

A passing run makes the Google qualification row `proven` for the exact
recorded topology. It does not prove every Google failure mode, an availability
percentage, production traffic safety, backup/restore, sustained capacity, or
all future revisions. The
[production-readiness profiles and evidence matrix](production-readiness-profiles.md)
remains the status source.

## One runner, not a script collection

PRD 024 should add one Python 3.11+ entry point:

```text
python3 deploy/google-cloud/qualify.py \
  --environment staging \
  --provider PROVIDER \
  --model MODEL \
  --callback-fixture-url HTTPS_URL \
  --notification-channel projects/PROJECT/notificationChannels/CHANNEL \
  --terraform-var-file /absolute/path/to/staging.tfvars
```

The exact flags may evolve with implementation, but the operating rules do
not:

- one command owns preflight, scenarios, cleanup, and the scrubbed result;
- `--scenario` may select a failed scenario for a rerun without creating a new
  executable;
- a dry run performs discovery and prints planned mutations without changing
  Google resources or sending a provider request;
- a managed Runtime credential may be supplied through
  `NVOKEN_QUALIFICATION_RUNTIME_TOKEN`; it is never accepted as a command-line
  value or written to evidence;
- the runner prints the resolved project, environment, region, service names,
  image, and intended mutations, then requires exact project confirmation;
- new orchestration is Python, using Terraform, `gcloud`, and public Runtime
  interfaces directly; no new Bash wrapper or test-control service is added;
- the runner owns the live Runtime and dispatch checks directly instead of
  calling `smoke.sh` or `dispatch-smoke.sh`; remove those two wrappers after
  parity so the repository has one live qualification entry point;
- the runner qualifies an already-deployed environment. Replacing the existing
  release and state-bootstrap scripts is separate work; it is not required to
  make this acceptance exercise work.

Use Python's standard library where practical. A small Go-only fixture is
acceptable when the boundary must live inside nvoken, but it must not become a
general remote fault-injection API.

## Prerequisites and preflight

Use a disposable project or a staging environment with no production traffic.
The operator provides one current model, one controlled HTTPS callback receiver,
and one real Monitoring notification channel. The callback receiver must verify
the configured nvoken signature, return a retryable response on its first known
test delivery, then return one valid result on retry while retaining the stable
delivery and ToolCall IDs.

Preflight must stop before mutation unless all of the following are true:

1. The checkout is committed and the deployed immutable image and expected
   schema are identifiable.
2. Terraform outputs resolve one project, region, public service, private
   executor, execution queue, migration job, and Redis instance.
3. `invocation_execution_mode` is `cloud_tasks`; provider, callback-signing,
   database, Runtime, and Redis secrets are configured without reading their
   values into the result record.
4. The selected notification channel is attached to the executor-auth alert.
5. The operator can read relevant Cloud Run, Cloud Tasks, Logging, Monitoring,
   Redis, and Terraform state metadata and can perform the narrow mutations
   below. The runner must not grant itself broader roles.
6. The queue is running, both services are healthy, Memorystore is `READY`, and
   the starting Terraform plan and mutable resource settings have been captured
   for cleanup comparison.
7. A maximum provider-call count, wall-clock deadline, and cleanup deadline are
   set. Failure to finish cleanup makes the run fail.

Cloud Run resolves environment-variable secrets before starting an instance;
failure to retrieve one prevents startup. Positive service startup plus real
provider/callback work therefore proves the intended grants are usable. Static
Terraform tests remain the negative proof for identities that intentionally
lack grants; this procedure does not mutate every secret policy to retest it.
See Google's
[Cloud Run secret checks](https://cloud.google.com/run/docs/configuring/services/secrets)
and
[Cloud SQL connection guidance](https://cloud.google.com/sql/docs/postgres/connect-run).

## Required scenarios

Run each scenario with unique, nonsecret correlation IDs. Store only bounded
metadata and links to filtered platform evidence, never prompts, model output,
callback bodies, credentials, environment dumps, or Terraform state.

### 1. Public path, private execution, and stream resume

1. Call `/health` and the Runtime API through Terraform's public `service_url`,
   not an instance URL or local proxy.
2. Admit one tool-free real generation and require `202 Accepted`.
3. Open its Session stream, observe at least one live `output_text.delta`,
   retain a durable `transcript.update` SSE ID, close the connection, then reconnect
   with `Last-Event-ID`.
4. Require no replay of an already-acknowledged durable row. Ephemeral
   delta frames may be lost or repeated across the disconnect.
5. Reach terminal stream state and verify two JSON reads return the same
   terminal Invocation and canonical transcript state.
6. Correlate the Invocation with the public Runtime revision, Cloud Tasks task
   and attempt, private executor revision, and nvoken dispatch/lease logs.
7. Verify a direct unauthenticated request to the executor URL is rejected by
   ingress or IAM.

The successful generation is also the positive Cloud SQL and executor provider
secret proof. Service startup plus cross-service deltas supplies the positive
Redis AUTH/TLS proof; the interruption scenario below proves reconnect behavior.

### 2. Queue pause/resume and queued cancellation

1. Pause the exact Terraform-output queue.
2. Admit two Invocations. Cancel one while it remains queued; leave the other
   queued.
3. Confirm both acknowledgements remain visible in Postgres-backed reads and
   the cancelled Invocation is terminal without a provider result.
4. Resume the queue. Require the noncancelled Invocation to complete and the
   cancelled one to remain unchanged.

Keep the pause bounded. The aged-dispatch alert has a sustained window, so a
short pause should not page; if timing creates a collateral incident, record it
as expected evidence and wait for it to close.

### 3. Duplicate delivery, real retry, and active cancellation

After an Invocation dispatch is published and its first executor request is
known active, create one second, uniquely named Cloud Task that targets the same
dispatch with the authorized task-caller OIDC identity. The duplicate must
receive a retryable response while the first owner is live, then become a `204`
no-op after the authoritative decision. Cloud Tasks headers are observations,
not deduplication or ownership inputs.

Cancel a separate Invocation only after its authoritative state is `running`.
It must settle once as cancelled, reject a late stale-owner write, and add no
post-cancellation assistant output or accepted ToolCall effect. Do not rely on
arbitrary sleeps to reach either boundary. If current providers finish too
quickly, implementation may add one exact-ID, staging-disabled-by-default hold
inside the executor; absence of a deterministic boundary is a failed fixture,
not permission to add a general chaos endpoint.

Google documents Cloud Tasks as favoring guaranteed execution over eliminating
all duplicates and applying retry backoff to unsuccessful HTTP targets. The
handler must therefore be idempotent even though duplicate execution is rare:
[Cloud Tasks issues and limitations](https://cloud.google.com/tasks/docs/common-pitfalls),
[HTTP target authentication and retry headers](https://cloud.google.com/tasks/docs/creating-http-target-tasks).

### 4. Small backlog observation

Temporarily set executor instances, executor request concurrency, and queue
concurrency to one through Terraform, then admit no more than three minimal
Invocations. Require every `202` acknowledgement to remain visible, at most one
executor request to run at once, and excess work to stay durably queued before
all three reach terminal state. Restore the starting values afterward.

This is a bounded correctness observation, not a latency benchmark, autoscaling
claim, or reason to keep a load-test framework.

### 5. Executor revision replacement

Record the starting synthetic dispatch delay, apply a nonzero staging-only delay
through Terraform, and start one synthetic dispatch so its private request is
held. While it is in flight, restore the original delay through Terraform to
create the replacement executor revision. Record exactly one of these paths:

- the old revision drains and durably settles the request before exit; or
- the request is cut, Cloud Tasks retries it, and the new revision resumes from
  the committed checkpoint under a new fence.

Do not label the first outcome crash recovery. Google says ordinary traffic
migration preserves in-flight requests, while exceptional instance shutdown can
still send `SIGTERM` with a short termination window:
[Cloud Run traffic migration](https://cloud.google.com/run/docs/rollouts-rollbacks-traffic-migration),
[Cloud Run container contract](https://cloud.google.com/run/docs/container-contract).

### 6. Redis interruption and recovery

The paved path uses Basic Tier Memorystore, which has no manual failover. In
this disposable environment, record the starting size and use one approved
size update to restart/interrupt the instance; restore the original size during
cleanup. Do not run this scenario against production or while unrelated users
depend on the cache.

While Redis is unavailable, keep a Session stream open and complete an
Invocation. Preview deltas may disappear and `stream.resync` may appear, but
Postgres polling must still deliver the canonical transcript and terminal state.
After Memorystore is `READY`, a later stream must receive live fan-out without
restarting nvoken. Finish by restoring the original size and confirming the
Terraform plan has no new drift.

Google documents that Basic Tier maintenance is interrupting and that a
configuration update such as resize is the practical restart lever; it also
requires TLS clients to trust the instance CA and reconnect after certificate
or server interruption:
[Memorystore maintenance](https://cloud.google.com/memorystore/docs/redis/about-maintenance),
[Basic Tier restart guidance](https://cloud.google.com/memorystore/docs/redis/troubleshoot-issues),
[Memorystore in-transit encryption](https://cloud.google.com/memorystore/docs/redis/about-in-transit-encryption).

### 7. Signed callback retry

Admit one Invocation whose execution spec contains the controlled callback
tool. The receiver rejects or times out the first known delivery and accepts
the next valid signed delivery. Verify the delivery ID and ToolCall ID are
stable, one result is accepted, the Invocation resumes, and no callback payload
appears in operational logs or the committed evidence record.

The receiver is an operator-supplied staging fixture. This slice does not deploy
a public webhook product or embed a callback server into the qualification
runner.

### 8. Alert delivery

Create one known Cloud Task targeting a well-formed but nonexistent dispatch
path with an existing service identity that does **not** have `roles/run.invoker`
on the executor. Do not revoke the real task caller's binding. If the identity
is correctly unauthorized, Cloud Run returns `401` or `403` before nvoken can
act. If it is unexpectedly authorized, nvoken treats the missing dispatch as a
durable no-op and the scenario fails because IAM is broader than intended.

Require the executor-auth alert to open a real Monitoring incident, deliver to
the selected channel, and close after the known negative-test task is deleted or
its retry window ends. Deleting this task is safe because it names no durable
work; never delete an uncertain nvoken execution task to force convergence.

Cloud Monitoring has no general notification-channel test operation; Google
recommends satisfying a real alert policy condition and verifying delivery:
[Cloud Monitoring notification channels](https://cloud.google.com/monitoring/support/notification-options).

## Cleanup invariants

Cleanup runs after success, failure, or interruption and is part of the result:

1. Resume the execution queue if this run paused it.
2. Stop only known qualification tasks that name no uncertain durable work.
3. Restore the Redis size and wait for `READY`.
4. Restore every executor qualification override through Terraform and wait for
   a healthy serving revision.
5. Verify the test alert incident closes and record any expected collateral
   incidents.
6. Compare the ending Terraform plan and mutable resource settings with the
   captured start. Do not destroy the environment, database, task queue, or
   authoritative rows as cleanup.

Any incomplete restoration is a failed run with a named operator action. The
runner must make a best-effort cleanup pass on interruption, but it must never
hide a partially restored state behind a successful exit code.

## Evidence record

Write one concise record to
`docs/testing/readiness/evidence/YYYY-MM-DD-google-cloud-<short-revision>.md`
with this shape:

| Field | Required value |
| --- | --- |
| Target | Project, environment, region, and profile; no credentials |
| Build | Git revision, immutable image, schema expectation, Terraform revision |
| Bounds | Provider/model names, maximum calls, start/end times; no prompt or output |
| Scenarios | Pass, fail, or skipped plus duration and bounded log/incident links |
| Revision result | Graceful drain or checkpoint recovery, stated exactly |
| Alerts | Expected and unexpected incident IDs and notification observation |
| Cleanup | Queue, Redis, revision, task, alert, and Terraform-drift result |
| Deferred proof | Real-cloud forced process crash and commit/publish crash injection |

The readiness matrix links the newest all-pass record. A required skip is not a
pass. Refresh the evidence after a material change to public routing, executor
ingress/OIDC, dispatch/retry semantics, checkpoint recovery, cancellation,
streaming/Redis, Cloud SQL connectivity, secret grants, callback delivery, alert
wiring, or the qualification procedure itself. Ordinary application changes and
every release do not automatically require a rerun.

## What remains unknown after a pass

A pass resolves the listed live interaction unknowns for one recorded
environment. It deliberately leaves these as known, nonblocking follow-ups:

- real-cloud forced executor process loss at checkpoint boundaries;
- real commit/publish process-crash injection;
- provider, region, receiver, and IAM-matrix breadth;
- sustained load, soak, autoscaling efficiency, and numerical SLOs;
- backup/restore, cross-region recovery, and automatic rollback.

Add those only when incidents, traffic, platform changes, or customer
requirements make their maintenance cost worthwhile.
