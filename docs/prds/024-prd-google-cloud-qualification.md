# Qualify the Google Cloud execution path

**Status:** Implemented; staging proof pending
**Sequence:** 024
**Depends on:** `009-prd-cloud-execution-dispatch-foundation.md`,
`010-prd-cloud-tasks-invocation-execution.md`,
`011-prd-resumable-streaming.md`,
`014-prd-checkpoint-crash-recovery.md`,
`016-prd-durable-callback-tools.md`, and
`023-prd-google-cloud-operations.md`

**Procedure:**
[`google-cloud-qualification.md`](../testing/google-cloud-qualification.md)

## ELI5

The Google deployment is assembled, but local tests cannot prove that its real
cloud services work together. We will run one small staging exercise through
the public API, private executor, database, Redis, callbacks, and alerts, then
record what happened. This is a launch confidence check, not a certification
platform, load lab, or exhaustive failure program.

## Why

PRDs 009–011 implemented the paved topology while correctly leaving real-cloud
proof pending. Terraform and automated tests establish configuration intent;
they do not establish the behavior of Google Front End routing, Cloud Tasks
delivery, Cloud Run revision changes, Cloud SQL connections, Memorystore
reconnection, Secret Manager access, or an operator's notification channel.

This is a known evidence gap. The live outcomes are unknown until the exercise
runs. The first qualification should resolve the highest-value unknowns without
turning a young service into its own test-control product.

## Outcome

One disposable or staging deployment has a secret-free record showing that the
current Google Cloud topology completes real work, preserves durable authority
through representative delivery and dependency failures, and notifies an
operator. Until that record exists, the profile remains implemented but not
proven by a staging qualification.

A passing run supplies the current live evidence for PRD 009 A7 and PRD 011
A8, and replaces PRD 010 A8 as the post-checkpoint-recovery qualification
procedure. PRD 010's older `execution_lost` expectation was superseded by PRD
014; its real process-crash case remains an explicit follow-up rather than a
hidden requirement of this launch exercise.

## Scope

**In:** one region; one real model provider; public Runtime routing; private
Cloud Tasks/OIDC dispatch; positive Cloud SQL and Secret Manager use; Redis
AUTH/TLS and one interruption; duplicate delivery and retry; queued and active
cancellation; queue pause/resume; one executor revision replacement; signed
callback retry; one real alert notification; concise evidence and cleanup.

**Out:** production traffic; every provider, model, region, retry boundary, IAM
denial, or callback receiver; backup/restore from PRD 020; upgrade compatibility
and rollback policy from PRD 019; throughput benchmarks; long soaks; chaos
infrastructure; multi-region failover; automatic rollback; scheduled or
per-release execution; a hosted qualification service.

## Requirements

- **R1 — Small, explicit qualification profile.** The procedure must target an
  operator-selected disposable or staging environment, region, provider/model,
  callback receiver, and notification channel. It must show the selected
  project and environment and require confirmation before any deployment, IAM,
  queue, Redis, or alert mutation. Provider calls, duration, and resource
  changes must be bounded, and cleanup must restore the starting configuration.

- **R2 — One Python orchestration path.** New qualification orchestration must
  use `deploy/google-cloud/qualify.py` as one Python 3.11-or-newer entry point,
  with standard-library code where practical. It must invoke existing
  Terraform, `gcloud`, and Runtime interfaces rather than create one script per
  scenario. It may use a narrow Go fixture or command when an in-process nvoken
  seam is the safer test boundary. It must add no Bash scripts, framework,
  daemon, scheduler, or cloud control plane. `make check-deploy` must validate
  its offline logic without Google credentials. Once the Python path covers
  their checks, retire `smoke.sh` and `dispatch-smoke.sh` instead of retaining
  duplicate live-test entry points. Replacing the unrelated release and state
  bootstrap scripts is outside this PRD.

- **R3 — Baseline real path and trust seams.** Through the Terraform output
  service URL, the exercise must check `/health`, admit a real generation,
  receive a durable stream cursor, disconnect, and reconnect with
  `Last-Event-ID`. The resumed stream must deliver no already-acknowledged
  durable row, reach terminal state, and agree with a second authoritative JSON
  read; ephemeral previews may be lost or repeated across the break. Before or
  after that break, at least one live generation delta must cross from the
  executor to the Runtime. Correlated platform and nvoken evidence must show the
  public Runtime revision, private OIDC-authenticated executor revision, Cloud
  Tasks attempt, Cloud SQL-backed state, provider-secret access by the executor,
  and Redis AUTH/TLS connection.
  A direct public unauthenticated executor request must be rejected by ingress
  or IAM; the successful task proves the authorized OIDC path. Static Terraform
  tests remain sufficient for identities that intentionally lack a secret
  grant; this slice does not build a live negative-IAM matrix.

- **R4 — Delivery, control, and revision behavior.** Deterministic staging
  drills must cover at least one real Cloud Tasks retry, which also supplies two
  deliveries for one durable dispatch, queue pause/admit/resume, cancellation
  while queued, cancellation while executing, and replacement of an executor
  revision while a request is in flight. Each drill must converge through
  Postgres claims, checkpoints, leases, and fences: no second terminal result,
  no repeated accepted ToolCall effect, no lost acknowledged Invocation, and
  no stale-owner commit. Revision replacement may either finish the segment on
  the draining revision or cut the request and resume a retried delivery from
  its committed checkpoint on the new revision under a new fence; evidence must
  identify the observed path and must not label graceful drain as crash
  recovery. A minimal opt-in test seam is allowed only if no existing Runtime
  or Google control can make a required boundary deterministic; it must be
  disabled by default and scoped to an exact test Invocation or dispatch.
  For one short batch, the exercise may temporarily reduce executor and queue
  concurrency to one and admit no more than three Invocations, proving every
  acknowledgement either completes or remains durably queued. Observed timing
  is evidence, not a throughput target.

- **R5 — Redis, callback, and notification behavior.** An operator-approved
  interruption of the disposable Basic Tier Memorystore instance must drop or
  resynchronize only ephemeral previews; canonical transcript and terminal
  reads must continue through Postgres and live fan-out must recover after
  reconnect. One signed callback receiver must reject or time out its first
  delivery and accept a retry with stable delivery and ToolCall identities.
  One safe executor-authentication failure must open the PRD 023 task-delivery
  rejection alert, reach the selected notification channel, and close after
  cleanup.

- **R6 — Evidence, claims, and reruns.** A run must create one concise,
  date-stamped Markdown record under `docs/testing/readiness/evidence/` naming
  time, operator, project/environment, region, git revision,
  immutable image, schema expectation, Terraform revision, nonsecret overrides,
  scenario outcomes, relevant log/incident references, observed durations,
  expected collateral incidents, and cleanup result. It must not contain
  credentials, provider prompts or outputs, callback bodies, transcripts, or
  Terraform state. Qualification expires only after a material change to the
  topology or behavior it proves; it is not a release-by-release gate. The PRD
  017 readiness matrix is the only home for the qualification row and links this
  record. Failed or skipped required scenarios remain visible and keep that row
  `pending`.

## Acceptance

- [ ] **A1 (R1–R3):** One confirmed staging run completes a real generation
  from the public service URL and records correlated evidence for the Runtime,
  private executor/OIDC task, Cloud SQL, provider secret, and at least one live
  delta over the Redis TLS path; reconnecting the stream from its last durable
  cursor reaches the same terminal state without replaying an acknowledged
  durable row, and direct unauthenticated executor access is rejected.
- [ ] **A2 (R4):** Duplicate/retried delivery, queue pause/resume, queued and
  active cancellation, and in-flight executor revision replacement each reach
  the documented durable outcome with one authoritative terminal state and no
  repeated accepted effect. Revision evidence identifies graceful drain or
  checkpoint recovery without conflating them. The short concurrency batch
  accounts for every acknowledged Invocation.
- [ ] **A3 (R5):** During a disposable Redis interruption, canonical streaming
  and terminal readback remain correct and preview fan-out recovers. In a
  separate drill, the callback retry preserves stable identities and accepts one
  result.
- [ ] **A4 (R5):** A safe authentication-failure drill creates a real Monitoring
  incident, delivers one notification to the selected channel, and closes after
  the known test task is removed or stops retrying.
- [ ] **A5 (R2, R6):** The implementation adds one Python qualification entry
  point, adds no Bash scripts, and retires the two superseded smoke wrappers
  after parity. It restores every temporary mutation and produces a secret-free
  evidence record whose required scenarios are all pass, fail, or explicitly
  skipped. Only an all-pass required run linked from the PRD 017 readiness
  matrix marks the qualification row `proven`; the record also lists the
  deliberately deferred PRD 010 process-crash case.

## Follow-up

Add another provider, deeper IAM denial tests, forced crash boundaries,
capacity tests, scheduled runs, or more automation only when a platform change,
incident, deployment frequency, or real workload makes the extra proof worth
its maintenance cost.
