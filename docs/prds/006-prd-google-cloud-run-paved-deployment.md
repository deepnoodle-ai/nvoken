# Deploy the Self-Contained Runtime on Google Cloud Run

**Status:** Implemented and verified in Google Cloud

**Sequence:** 006

**Depends on:** `002-prd-postgres-runtime-spine.md`,
`003-prd-durable-invocation-admission.md`,
`004-prd-engine-claims-and-fencing.md`,
`005-prd-generation-only-turns.md`

## ELI5

An operator can deploy one nvoken image plus Postgres to Google Cloud and run a
real turn. Cloud Run must keep CPU and one instance available after the API says
`202`, because generation happens in the background. This does not add Cloud
Tasks, streaming, tools, or a claim that combined-mode autoscaling follows queue
depth.

## Why

The first real turn now works in one process, but the repository has no image,
Google Cloud resources, or release path. The default Cloud Run service settings
would be incorrect: request-based CPU can stop background work after admission
returns, and scaling to zero leaves Postgres work with no poller.

Mobius Cloud is useful precedent for a dedicated service identity, Secret
Manager injection, direct VPC access to Cloud SQL's private address, explicit
probes, bounded scaling, and Cloud Logging-compatible JSON. nvoken keeps those
operational seams while tightening two boundaries: Cloud SQL has no public IP,
and its existing serialized `nvokend migrate` operation runs as an explicit
release job before the service revision, never during replica startup.

## Outcome

From a clean Google Cloud project, an operator can follow one documented release
path to build an immutable `nvokend` image, provision the paved infrastructure,
run serialized migrations, deploy the self-contained Cloud Run service, and
prove a generation-only Invocation reaches a durable terminal read.

## Scope

**In:** a production container image; reproducible Google Cloud infrastructure;
a protected GCS Terraform backend bootstrap; Artifact Registry; one public
Cloud Run service; one migration Cloud Run Job; private-IP Cloud SQL for
Postgres; VPC connectivity; Secret Manager; a dedicated service identity;
explicit CPU, request, engine, database, instance, probe, and shutdown settings;
release and smoke-test commands; structured operational logs.

**Out:** Cloud Tasks or a separate executor service; Redis; custom domains and
load balancers; multi-region or zero-downtime database migration guarantees;
automatic scaling from durable queue depth; tools, streams, controls, budgets,
or crash continuation; a general Terraform platform for every Google Cloud
topology. PRDs 009 and 010 add the split execution path.

## Requirements

- **R1 — One immutable image, two commands.** The repository must build a
  non-root Linux container containing only the `nvokend` runtime and required
  trust data. The same immutable image must run `nvokend serve` in the Cloud Run
  service and `nvokend migrate` in the migration job. Releases must use a unique
  image tag or digest, never a mutable `latest` workflow.

- **R2 — Reproducible least-privilege foundation.** The paved infrastructure
  must declaratively provision the required Google APIs, Artifact Registry,
  a dedicated VPC and subnet, private services access, a private-IP Cloud SQL
  Postgres database, Secret Manager configuration, purpose-specific build,
  migration, and runtime service accounts, the migration job, and the public
  Cloud Run service. Each identity must receive only the access required by its
  process role; database credentials must not be exposed through a public IP,
  and database clients must require TLS while Cloud SQL rejects unencrypted
  connections. Derived account IDs must remain valid for every accepted
  `name`/`environment` combination.
  An idempotent pre-Terraform bootstrap must create or harden the GCS backend
  bucket with object versioning, uniform bucket-level access, and public access
  prevention; the root must use a distinct prefix per environment.

- **R3 — Migrate before service release.** The release workflow must first make
  the new immutable image available to the migration job, execute that job to
  success, and only then update service traffic to the new image. Migration
  execution must remain bounded and advisory-lock serialized. A failed, dirty,
  older, or newer schema must prevent the service revision from becoming ready;
  service startup must never apply schema changes.

- **R3a — Build source is narrowly scoped.** The release caller must be able to
  act as the same-project Cloud Build identity. Uploaded source must be staged
  in a non-public, uniform-access bucket with automatic expiry, and the build
  identity's source read access must be scoped to that bucket rather than every
  bucket in the project. The deployment must not add cross-project service-agent
  impersonation grants to the same-project paved path.

- **R4 — Background execution has capacity.** The combined service must use
  instance-based CPU allocation and a service-level minimum of at least one
  instance. Request concurrency, engine concurrency, database pool size, and
  maximum instances, and the total shutdown budget must all be explicit,
  conservative, and operator-tunable.
  Documentation must state that the minimum poller is required for liveness and
  that HTTP or CPU autoscaling is not a promise to scale with Postgres backlog.

- **R5 — Honest health and startup.** The container must listen on Cloud Run's
  `PORT`. `GET /health` and the local-compatible `GET /healthz` alias must
  remain unauthenticated, cheap, and contain no sensitive or dynamic content.
  The public Cloud Run endpoint must disable its edge Invoker IAM check and
  leave application requests authenticated by nvoken's Runtime bearer
  credential; it must not depend on an `allUsers` IAM binding.
  The process must not begin listening until configuration, database
  connectivity, exact schema validation, installation bootstrap, and engine
  construction have succeeded. Because Google Front End reserves the exact
  external `/healthz` path, Cloud Run must use `/health` as its HTTP startup and
  liveness probe, so a bad configuration or schema never receives application
  traffic and the external smoke follows a routable path.
  The fuller dynamic `/readyz` surface in the governing API design remains
  deferred; at this stage the process never listens in a not-ready state.

- **R6 — Secrets stay configuration.** The database URL, Runtime bearer key,
  and configured provider keys must reach the container through Secret Manager,
  not image layers, command arguments, source-controlled variable files, or
  logs. The paved deployment must require at least one supported provider key
  and allow both. Generated database and Runtime credentials may exist in
  protected, versioned GCS Terraform state, and that state-sensitivity
  constraint and required bucket IAM restriction must be explicit. Local state
  is not the paved release path.

- **R7 — Shutdown fits the platform boundary.** One operator-tunable total
  shutdown budget must cover HTTP request drain, engine drain grace, component
  join, and Postgres pool close. The paved value must be strictly below Cloud
  Run's termination window, and engine drain grace must leave time inside that
  total. On `SIGTERM`, the process must stop accepting requests and claiming new
  Invocations, allow cooperative in-flight completion, then cancel and join
  remaining work when dependencies honor cancellation. A routine combined-mode
  revision rollout or scale-in can therefore interrupt a longer running turn;
  lease expiry resolves it as the existing durable `execution_lost` failure.
  This slice does not promise checkpoint continuation or interruption-free
  self-contained deploys.

- **R8 — Operable and provable.** Application logs must be single-line JSON
  compatible with Cloud Logging severity and message fields and retain safe
  request, Invocation, owner, lease, state, and migration evidence without
  prompts, transcript content, bearer values, provider keys, or provider
  bodies. The paved path must include a smoke test that checks health, admits
  one real provider-backed turn, and polls the acknowledged Invocation ID until
  a successful durable terminal `GET`.

## Acceptance

- [x] **A1 (R1):** A clean source checkout builds the image for `linux/amd64`;
  inspection shows a non-root runtime user, and the same image successfully
  invokes both `serve` and `migrate` without build tools in the final layer.

- [x] **A2 (R2, R3a, R4, R5, R6):** Automated Terraform validation plans the VPC,
  private-only Cloud SQL instance, generated database/Runtime secrets, provider
  secret bindings, purpose-specific service accounts, migration job, private
  build-source bucket, and public service. The plan proves database TLS is
  enforced, source access is bucket-scoped, and every accepted long resource
  name produces valid service-account IDs.
  The plan proves instance-based CPU, minimum instances `>= 1`, bounded maximum
  instances, explicit request/engine/database concurrency, and the `/health`
  startup and liveness probes. Validation rejects zero provider secrets and
  accepts Anthropic, OpenAI, or both, and proves the public endpoint disables
  the Cloud Run IAM check while retaining Runtime authentication. Automated
  release tests prove the GCS backend bootstrap is
  ordered before `terraform init` and enforces versioning, uniform access, and
  public access prevention without attempting to manage its own bucket in that
  state.

- [x] **A3 (R3):** Against an empty Postgres database, the release operation
  runs the migration job to the expected schema version before deploying the
  service. Two concurrent migration executions serialize; a failed migration
  stops the release before service update, and ordinary service startup performs
  no DDL.

- [x] **A4 (R3, R5):** A service started with an empty, dirty, behind, or
  ahead-of-binary schema exits before listening. With the exact schema and valid
  configuration, `/health` and `/healthz` return `200` without authentication
  or database, account, prompt, or secret content.

- [x] **A5 (R4):** The default paved plan keeps exactly one background-capable
  instance warm, caps horizontal instances and per-instance engine claims, and
  documents the resulting upper bounds on engine and Postgres concurrency. No
  test or guide claims queue-depth autoscaling.

- [x] **A6 (R7):** A termination test proves new claims and HTTP admission stop
  on cancellation, a cooperative in-flight execution may settle during the
  configured sub-ten-second total budget, and every component is joined before
  the database pool closes. HTTP shutdown and engine drain share that bound.
  An interrupted attempt remains fenced and is resolved by the existing
  lease/reaper policy.

- [x] **A7 (R6, R8):** Repository, image, local filesystem, Terraform plan
  output, and structured application logs contain no supplied Runtime or
  provider secret values or persisted Terraform state. Cloud Logging parses
  `severity` and `message`, and operational entries can be correlated by request
  or Invocation ID without logging model input/output.

- [x] **A8 (R3, R5, R8):** In a disposable Google Cloud environment, the
  documented paved workflow builds and deploys a unique image, completes the
  migration job, passes the startup probe, returns a `202` acknowledgement for
  a real Anthropic or OpenAI Invocation, and later returns `completed` for that
  same durable Invocation ID. Re-reading after a service revision restart
  returns the same terminal state.

  Verified on 2026-07-21 in the `nvoken` project: the migration-gated release
  deployed immutable image `1fd0f3f0d6309537eda94c34ff375fde06953fc5`,
  OpenAI Invocation `invk_019f82b7-8d59-75e4-be8d-6f0671157dfb` completed, the
  same terminal row was read after a new Cloud Run revision took traffic, and
  the final Terraform plan reported no changes.

## Risks and open decisions

- A nonzero minimum plus instance-based billing intentionally incurs continuous
  Cloud Run cost. It is the honest combined-mode tradeoff until Cloud Tasks
  execution becomes the recommended production topology in PRD 010.
- The paved release orders migrations before service rollout. Future migrations
  that are not compatible with the still-running prior revision require their
  own expand/contract rollout; this PRD does not claim otherwise.
- Direct-IP Postgres uses required TLS without hostname verification. The
  dedicated private VPC is still the peer-access boundary; authenticated Cloud
  SQL connectors or DNS-verified server identity remain a future hardening path.
- Combined-mode Cloud Run cannot drain a turn longer than the platform's
  termination window. Routine revision rollouts and scale-in above the minimum
  may therefore produce durable `execution_lost` failures; operators should
  release during quiet periods until the request-bound split topology and later
  checkpoint recovery remove this limitation.
