# Production-readiness profiles

**Contract version:** `readiness-v1`
**Status source:** This document
**Defined by:**
[`017-prd-production-readiness-profiles.md`](../prds/017-prd-production-readiness-profiles.md)

This is the only repository status matrix for nvoken production readiness. It
defines the two initial open-source deployment profiles, the correctness floor
they share, and the evidence still required before either profile may be called
production ready. Passing ordinary repository tests, deploying the Terraform
root, or using a managed cloud service does not by itself change a profile's
status.

## Profiles and availability boundaries

| Profile | Normative topology | Operator boundary | Honest availability claim |
| --- | --- | --- | --- |
| `single_daemon` | One `nvokend` combined process, embedded execution, in-process live events, and operator-provided Postgres. This is the [self-contained runtime mode](../design/architecture.md#two-roles-two-runtime-modes) with no external Redis or additional daemon replicas. | nvoken owns the binary, schema, and runtime correctness. The operator owns Postgres, process supervision and restart, ingress/TLS, secret storage, capacity, and backup scheduling. | Durable accepted work can survive daemon restart and resume from committed Postgres checkpoints. One daemon is a single point of service availability: this profile does not claim high availability or uninterrupted service during restart, host loss, or Postgres loss. |
| `google_cloud` | The [paved Terraform topology](../../deploy/google-cloud/README.md): a public combined Runtime/control service, a private request-bound executor, Cloud Tasks, Cloud SQL, and Memorystore. | nvoken owns the Terraform and runtime behavior. The operator owns the Google project, IAM and release access, protected Terraform state, selected availability/capacity settings, notification routing, provider and callback secrets, and recovery operations. | The profile may claim only behavior exercised with its recorded configuration. Cloud Run, Cloud Tasks, Cloud SQL, Memorystore, and model-provider availability are dependencies, not nvoken guarantees. This is an open-source deployment in the operator's project, not managed nvoken Cloud. |

Both profiles expose the same Runtime contract. Moving work through an in-process
poller or Cloud Tasks changes delivery topology, not admission, execution
authority, or recovery semantics.

## Current profile status

| Profile | Status | Current evidence summary |
| --- | --- | --- |
| `single_daemon` | **Pending** | The checked profile package, diagnostic configuration proof, smoke/load tooling, failure drills, incident guidance, and initial retention posture are implemented. Live smoke, restart/failure, upgrade/rollback, restore, diagnosis, capacity, and operator secret-handling evidence has not been recorded. |
| `google_cloud` | **Pending** | The paved topology, offline Terraform checks, and initial retention posture exist, but live qualification, upgrade/rollback, restore, operational, and secret-handling evidence has not been recorded for this profile. |

A profile is ready only when every required row is `proven` and `current`.
Optional capabilities outside the exact profile do not add implicit rows.
The Google retention row proves the checked shared pruning behavior and the
documented Cloud SQL metric mapping; it does not record a live Cloud SQL
observation or qualify the Google profile.

## Evidence rules

- **Evidence mode:** `automated` means a checked-in, repeatable command produces
  the result without operator interpretation. `manual` means an operator runs a
  bounded procedure and records the observed result. Automation may support a
  manual procedure without changing its mode.
- **Readiness state:** `proven` requires passing repository or recorded
  environment evidence that names the profile and exact tested Git revision.
  A design, implementation, procedure, or unrecorded local run is `pending`.
- **Freshness:** `current` means the evidence still covers the relevant
  topology and behavior; `stale` means a material covered path or contract
  changed or the evidence was explicitly invalidated; `missing` means no
  qualifying record exists. Evidence does not expire merely because time
  passes.
- Evidence records must be concise and secret-free. They may identify an
  environment, image, schema, command, bounded result, and external log or
  incident reference, but must not copy credentials, prompts, transcripts,
  callback bodies, or Terraform state.
- `proven` plus `stale` is not a passing row. Changing a row or profile claim
  requires updating this matrix in the same change; linked guides and product
  docs do not own separate readiness status.

## Shared correctness floor

Profile exercises must preserve these invariants. The linked repository tests
are supporting correctness evidence, not substitutes for the profile-specific
proof matrix.

| Invariant | Authority and supporting repository proof |
| --- | --- |
| Durable admission | Agent/Session resolution, spec snapshot, input, Invocation, and any external dispatch intent commit atomically in Postgres. See [admission integration tests](../../internal/adapters/postgres/admission_integration_test.go) and [dispatch admission tests](../../internal/adapters/postgres/invocation_dispatch_integration_test.go). |
| Postgres authority and canonical transcript | Session messages are the sole durable content transcript. Redis, live events, process memory, and Cloud Tasks are projections or delivery only. See the [data and retention contract](../design/architecture.md#data-and-retention) and [recovery integration tests](../../internal/adapters/postgres/recovery_integration_test.go). |
| Fenced execution ownership | A Postgres claim and fence precede model execution; lease, checkpoint, ToolCall, usage, and settlement writes reject a stale owner. See [execution claim and recovery tests](../../internal/adapters/postgres/execution_integration_test.go). |
| Checkpoint recovery | A replacement owner validates and continues the committed transcript/checkpoint prefix. A committed final checkpoint settles without another provider call; work completed outside Postgres may repeat. See [checkpoint and ToolCall tests](../../internal/adapters/postgres/toolcalls_integration_test.go). |
| First terminal writer | Completion, failure, cancellation, and deadline settlement race through one immutable terminal boundary. See [control concurrency tests](../../internal/adapters/postgres/controls_integration_test.go). |
| ToolCall idempotency | Stable ToolCall identities, immutable requests, first accepted results, and durable callback delivery state prevent accepted results from being applied twice. Hosts must make uncertain external effects idempotent by ToolCall ID. See [ToolCall tests](../../internal/adapters/postgres/toolcalls_integration_test.go). |
| Delivery is not authority | Polling, Cloud Tasks, a supervisor, and a process restart may prompt an attempt but never grant execution ownership or determine terminal state. See [dispatch crash-window tests](../../internal/adapters/postgres/dispatch_integration_test.go). |

## Readiness evidence matrix

Every minimum dimension has exactly one required row per profile. A pending row
links the procedure or PRD that must create its first qualifying evidence.

### `single_daemon`

| Dimension | Observable proof | Owner boundary | Mode | State | Freshness | Evidence or procedure |
| --- | --- | --- | :---: | :---: | :---: | --- |
| Installation | From an empty supported Postgres, apply migrations, start one combined/embedded daemon with in-process events, pass health and schema checks, and make an authenticated request. | nvoken: binary, migration, configuration validation. Operator: Postgres, supervisor, ingress/TLS, secrets. | automated | pending | missing | [Profile install and diagnostic procedure](../../deploy/single-daemon/README.md#3-prepare-postgres-and-migrate), [checked configuration](../../cmd/nvokend/single_daemon_profile_test.go), [PRD 022 A1](../prds/022-prd-single-daemon-production-profile.md#acceptance) |
| Normal execution | Admit work, reach one durable terminal result, read it twice, resume the transcript, and complete client-tool and configured callback paths without duplicate accepted results. | nvoken: runtime and durable tool exchange. Operator: model key and optional callback receiver. | automated | pending | missing | [Profile smoke and restart procedure](../../deploy/single-daemon/README.md#5-smoke-and-restart-readback), [Python smoke tool](../../deploy/single-daemon/smoke.py), [PRD 022 A1-A2](../prds/022-prd-single-daemon-production-profile.md#acceptance) |
| Process/dependency failure | Terminate the daemon during execution, restart with queued/waiting work, and interrupt Postgres/provider/callback dependencies. Accepted state remains in Postgres; recovery continues from a checkpoint under a new fence and a stale owner cannot settle. | nvoken: claims, checkpoints, fences, durable failure. Operator: restart and dependency recovery. | manual | pending | missing | [Single-daemon failure drills](../../deploy/single-daemon/failure-drills.md), [PRD 022 A3](../prds/022-prd-single-daemon-production-profile.md#acceptance) |
| Upgrade/rollback | Migrate, start the new binary, exercise retained queued/running/waiting/callback/terminal work, and roll back within the declared one-release compatibility window with authoritative readback. | nvoken: compatibility declaration and preflight. Operator: serialized migration, binary rollout, rollback decision. | manual | pending | missing | [Profile upgrade boundary](../../deploy/single-daemon/README.md#7-upgrade-and-rollback), [PRD 019 A3-A4](../prds/019-prd-compatible-upgrades-and-rollback.md#acceptance) |
| Backup/restore | Make a logical Postgres backup, restore it to a new empty database, run the non-mutating verifier, and read representative terminal data without starting claimable work. | nvoken: verifier and schema/runtime invariants. Operator: backup scheduling, storage, isolated restore, cleanup. | manual | pending | missing | [Profile logical backup procedure](../../deploy/single-daemon/README.md#8-logical-backup-and-isolated-restore), [PRD 020 A1 and A4](../prds/020-prd-backup-restore-and-recovery.md#acceptance) |
| Diagnosis | Using only safe startup identity, the one-shot dependency diagnostic, the event catalog, and profile incident guidance, identify representative database, execution-recovery, provider, callback, and stuck-work incidents without sensitive content. | nvoken: bounded events and diagnostic. Operator: log retention/querying and incident response. | manual | pending | missing | [Single-daemon incident guide](../../deploy/single-daemon/runbooks.md), [Operational signals](../guides/operational-signals.md), [PRD 022 A5](../prds/022-prd-single-daemon-production-profile.md#acceptance) |
| Capacity | On one recorded host/database shape, exercise admissions, reads, streams, and bounded concurrent execution; record observed limits and show saturation queues or rejects work without losing an acknowledgement. | nvoken: explicit concurrency/backpressure bounds. Operator: machine, database sizing, and workload envelope. | manual | pending | missing | [Reference-load procedure](../../deploy/single-daemon/README.md#9-measure-the-local-envelope), [Python load recorder](../../deploy/single-daemon/load.py), [PRD 022 A4](../prds/022-prd-single-daemon-production-profile.md#acceptance) |
| Retention | Retain-by-default is an explicit storage/privacy limitation; authoritative rows remain readable while only terminal transport diagnostics prune in bounded batches, and content-free queries report database and largest-table growth. | nvoken: retain-by-default schema and bounded diagnostic pruning. Operator: storage monitoring, capacity, backups, and future deletion policy. | automated | proven | current | [Retention policy and queries](../guides/data-retention.md), [dispatch proof](../../internal/adapters/postgres/invocation_dispatch_integration_test.go), [callback proof](../../internal/adapters/postgres/toolcalls_integration_test.go) |
| Secret handling | Start from a secret-free checked example with externally supplied Runtime, provider, database, and optional callback credentials; verify invalid configuration fails safely and logs/evidence expose no secret or payload. | nvoken: bounded config validation and redaction. Operator: generation, storage, rotation, and ingress protection for secrets. | automated | pending | missing | [Canonical secret-free environment](../../deploy/single-daemon/nvoken.env.example), [checked configuration](../../cmd/nvokend/single_daemon_profile_test.go), [PRD 018 A1-A3](../prds/018-prd-operational-signals-and-diagnostics.md#acceptance), [PRD 022 A1](../prds/022-prd-single-daemon-production-profile.md#acceptance) |

### `google_cloud`

| Dimension | Observable proof | Owner boundary | Mode | State | Freshness | Evidence or procedure |
| --- | --- | --- | :---: | :---: | :---: | --- |
| Installation | From a deliberately selected project, bootstrap protected state, build an immutable image, run the serialized migration job, deploy the paved topology, pass `/health`, and verify the executor rejects direct unauthenticated access. | nvoken: Terraform and release order. Operator: project/IAM, state bucket, approvals, provider secrets, production settings. | manual | pending | missing | [Paved release procedure](../../deploy/google-cloud/README.md#release), [PRD 024 A1](../prds/024-prd-google-cloud-qualification.md#acceptance) |
| Normal execution | Through the public URL, admit a real generation, traverse Cloud Tasks and the private executor, observe a live delta, resume from a durable SSE cursor, and match terminal JSON/transcript reads. | nvoken: Runtime, dispatch, executor, and reconciliation. Operator: Google services, provider/model, and qualification environment. | manual | pending | missing | [Google qualification scenario 1](google-cloud-qualification.md#1-public-path-private-execution-and-stream-resume) |
| Process/dependency failure | Exercise duplicate/retried delivery, queue pause/resume, queued and active cancellation, executor revision replacement, and Redis interruption. Every acknowledgement converges through Postgres claims, checkpoints, and fences; Redis loss affects previews only. | nvoken: recovery and stale-owner rejection. Operator: bounded Google mutations, dependency recovery, and cleanup. | manual | pending | missing | [Google qualification scenarios 2-6](google-cloud-qualification.md#required-scenarios) |
| Upgrade/rollback | Prove migration-before-service rollout, retained work across the supported compatibility window, service failure after migration, revision rollback, and safe embedded/Cloud Tasks overlap without deleting uncertain tasks or rows. | nvoken: migration compatibility, fences, and repair. Operator: release serialization, traffic/queue control, rollback decision. | manual | pending | missing | [PRD 019 A3-A4](../prds/019-prd-compatible-upgrades-and-rollback.md#acceptance), [paved release ordering](../../deploy/google-cloud/README.md#release), and [current rollback notes](../../deploy/google-cloud/README.md#end-to-end-smoke) |
| Backup/restore | Restore a Cloud SQL backup or PITR point to an isolated instance, run the non-mutating verifier, and record schema, durable readback, recovery point, and cleanup without changing production traffic or Terraform state. | nvoken: verifier and durable invariants. Operator: backup/PITR policy, isolated restore, access, and cleanup. | manual | pending | missing | [PRD 020 A2 and A4](../prds/020-prd-backup-restore-and-recovery.md#acceptance) |
| Diagnosis | Using safe startup identity, the one-shot dependency diagnostic, portable events, and existing Google logs, identify representative public, dispatch, executor, provider, callback, database, and Redis incidents without sensitive content. | nvoken: bounded events and diagnostic. Operator: Cloud Logging retention/querying and incident classification. | manual | pending | missing | [Operational signals](../guides/operational-signals.md), [Google Cloud runbooks](../../deploy/google-cloud/runbooks.md) |
| Capacity | Record combined/executor/database/queue limits, run a bounded one-at-a-time backlog, and account for every accepted Invocation as terminal or durably queued without converting observations into an autoscaling guarantee. | nvoken: validated capacity relationships and durable queueing. Operator: selected limits, Cloud SQL sizing, workload envelope. | manual | pending | missing | [Google qualification scenario 4](google-cloud-qualification.md#4-small-backlog-observation) |
| Retention | Retain-by-default is an explicit storage/privacy limitation; authoritative rows remain readable, terminal diagnostics prune in bounded batches, and the paved guide identifies Cloud SQL total-growth signals without transcript content. | nvoken: retain-by-default schema and diagnostic pruning. Operator: Cloud SQL storage/backup capacity and future deletion policy. | automated | proven | current | [Retention policy and deferred contracts](../guides/data-retention.md), [Cloud SQL signal](../../deploy/google-cloud/README.md#retention-and-storage-growth), [dispatch proof](../../internal/adapters/postgres/invocation_dispatch_integration_test.go), [callback proof](../../internal/adapters/postgres/toolcalls_integration_test.go) |
| Secret handling | Static Terraform checks prove scoped Secret Manager grants and no secret-bearing variables; a live run proves the intended Runtime/executor/callback identities can use only their configured secrets while records and logs remain secret-free. | nvoken: least-privilege wiring, config validation, redaction. Operator: secret creation, rotation, IAM/release access, protected Terraform state. | manual | pending | missing | [Paved prerequisites](../../deploy/google-cloud/README.md#prerequisites), [PRD 024 A1 and A5](../prds/024-prd-google-cloud-qualification.md#acceptance) |

## Claim and document ownership

The [root README](../../README.md),
[architecture](../design/architecture.md),
[Runtime admission guide](../guides/runtime-admission.md), and
[Google deployment guide](../../deploy/google-cloud/README.md) link here rather
than maintaining parallel production-readiness status. PRDs 018-025 may add
proof, procedures, and evidence records, but a readiness state changes only in
this matrix.

This contract deliberately does not define numeric SLOs or error budgets,
additional platforms, portable multi-daemon or Redis profiles, Kubernetes,
managed nvoken Cloud, a certification service, or a release-blocking gate.
