# Package the single-daemon production profile

**Status:** Implemented; profile evidence and readiness matrix pending
**Sequence:** 022
**Depends on:** `017-prd-production-readiness-profiles.md`,
`018-prd-operational-signals-and-diagnostics.md`,
`019-prd-compatible-upgrades-and-rollback.md`,
`020-prd-backup-restore-and-recovery.md`, and
`021-prd-initial-retention-posture.md`

## ELI5

A small team should be able to run one nvoken daemon with Postgres by following
one guide. The guide proves installation, restart recovery, upgrade, backup, and
a modest workload without implying high availability. This slice does not add
clustering, Kubernetes, or an operator console.

## Why

Embedded execution is already a complete runtime mode, but the current guide is
primarily a feature walkthrough. Production use needs one supported
configuration, an environment reference, a smoke path, and a few failure drills.
Keeping the profile to one daemon and operator-managed Postgres makes the first
portable operating model useful without turning nvoken into a deployment
platform.

## Outcome

An unfamiliar operator can install, run, diagnose, restart, upgrade, back up,
and restore the documented single-daemon profile within its stated limits.

## Scope

**In:** combined role with embedded execution; one daemon; operator-provided
Postgres; in-process live events; binary/container guidance; configuration
reference; smoke and failure scripts; concise runbooks; measured reference load.

**Out:** high availability; multiple daemon replicas; Redis; Cloud Tasks;
Kubernetes/Helm; OS-specific service units; managed TLS/ingress; automated
backup scheduling; autoscaling; UI or console.

**Package:** [`deploy/single-daemon/`](../../deploy/single-daemon/README.md)

## Requirements

- **R1 — One canonical configuration.** The `single_daemon` profile implements
  the design packet's Self-contained topology as one `nvokend` process with the
  `combined` role, `embedded` execution, and in-process live events. Its
  configuration reference is the normative home for the supported Postgres
  version range. A machine-checked example lists every required setting,
  recommended safety bound, secret input, port, storage prerequisite, and the
  exact optional callback/provider settings without including secret values;
  it must pass the PRD 018 diagnostic in CI.

- **R2 — Reproducible lifecycle.** The guide must cover obtaining or building an
  immutable binary/container, migration, first start, smoke, graceful stop,
  restart, upgrade, rollback window, backup, restore, and removal of test data.
  It must separate daemon operations from operator-owned Postgres, ingress/TLS,
  secret storage, and process supervision.

- **R3 — Representative smoke.** One script or documented command sequence must
  prove health, authenticated durable admission, terminal authoritative read,
  restart readback, resumable transcript, host tool result submission, and—
  when configured—one callback delivery. Provider/model selection remains an
  explicit operator input because model availability changes independently.

- **R4 — Failure and recovery proof.** Disposable tests or drills must cover
  daemon termination during execution, restart with queued/waiting work,
  temporarily unavailable Postgres, provider failure, callback failure, and
  graceful shutdown. Accepted state must remain authoritative; uncertain model
  or callback work follows the existing checkpoint/idempotency caveats.

- **R5 — Small operating envelope.** A lightweight load script must exercise
  admissions, reads, streams, and bounded concurrent execution on one documented
  reference machine/database. The result records observed throughput, latency,
  memory, database connections, and queue age as a reference—not a universal
  guarantee. Queue age may come from a documented read or safe SQL query; no
  metrics service is required. Excess work must queue or fail through documented
  bounds rather than disappear.

- **R6 — Concise incident guide.** The profile must give first checks and safe
  actions for daemon down, database unavailable/incompatible, stuck or recovering
  Invocation, provider failure, callback retry/exhaustion, storage growth, and
  failed upgrade/restore. It must identify actions that are unsafe, including
  deleting uncertain rows or replaying external effects with new IDs.

- **R7 — Evidence linkage.** Smoke, failure, load, upgrade, and restore records
  must update the matching `single_daemon` rows in the PRD 017 readiness matrix
  with the tested revision; this guide must not maintain a second readiness
  status.

## Acceptance

- [ ] **A1 (R1–R3, R7):** From the documented prerequisites, a clean disposable
  installation reaches a completed Invocation and durable restart readback using
  only the checked profile example and guide, then links the smoke evidence from
  the matching readiness row.
- [ ] **A2 (R3, R4):** Client and configured callback smoke paths survive the
  documented restart/failure boundaries with one accepted result per ToolCall.
- [ ] **A3 (R4, R7):** Every listed failure boundary has a recorded outcome:
  checkpointed work recovers after a kill; queued and waiting work survives
  restart; provider failure settles durably; callback failure follows its retry
  contract; graceful shutdown leaves no orphaned claim; and Postgres loss creates
  no false settlement or erased acknowledgement. The matching readiness rows
  link these records.
- [ ] **A4 (R5, R7):** The reference load run publishes its environment and
  observed envelope, saturation leaves all acknowledged work durably queryable,
  and its evidence updates the matching readiness row.
- [ ] **A5 (R2, R6, R7):** A second operator follows the upgrade, rollback, backup,
  restore, and one incident procedure without undocumented database mutation;
  review confirms the guide covers every R6 class, names the unsafe actions, and
  links the procedure evidence from the readiness matrix.

## Follow-up

Portable HA, multiple replicas, Redis fan-out, packaged supervisors, Kubernetes,
and automatic backup scheduling are separate profiles to consider only after
single-daemon users need them.
