# Prove a minimal backup and restore path

**Status:** Draft
**Sequence:** 020
**Depends on:** `017-prd-production-readiness-profiles.md`,
`018-prd-operational-signals-and-diagnostics.md`, and
`019-prd-compatible-upgrades-and-rollback.md`

## ELI5

A backup is useful only if an operator can restore it and nvoken can read the
result. Each profile gets one documented, repeatable restore path with simple
integrity checks. This slice does not build a backup service, cross-region
failover, or continuous disaster-recovery automation.

## Why

Google Cloud already enables backups and PITR, while a portable operator can use
ordinary Postgres tools. Neither path is packaged as a drill that proves schema,
Sessions, Invocations, transcripts, ToolCalls, and checkpoints remain usable.
The first readiness bar is a successful isolated restore with recorded recovery
point and elapsed time, not an elaborate disaster-recovery platform.

## Outcome

An operator can restore either production profile into an isolated database and
verify durable runtime invariants and binary compatibility without causing
outbound effects.

## Scope

**In:** backup responsibility and prerequisites; one logical Postgres path for
single-daemon installations; existing Cloud SQL backup/PITR path; isolated
restore; bounded integrity checks; durable readback; drill record.

**Out:** backup scheduling for portable operators; cross-region replication;
automatic failover; tenant-level restore; backup encryption key management;
long-term archival; a recovery coordinator service.
Promoting a rewound restore to production authority is also deferred because its
queues and delivery state may differ from external systems.

## Requirements

- **R1 — Complete durable scope.** The guides must state that Postgres contains
  all authoritative nvoken runtime state and that Redis, Cloud Tasks, local
  files, and in-memory events are not restored as authority. Runtime/provider/
  callback secrets and Terraform state are configuration recovery concerns and
  must not be embedded in a database backup.

- **R2 — Single-daemon recipe.** The portable profile must document one supported
  logical backup and restore procedure using standard Postgres tooling, including
  version prerequisites, quiescence/consistency expectations, credential-safe
  invocation, and restore into a new empty database. Scheduling remains the
  operator's responsibility.

- **R3 — Google recipe.** The paved profile must document how to identify a
  successful Cloud SQL backup or PITR timestamp, restore to an isolated instance,
  construct a temporary secret-safe connection, and avoid changing production
  traffic or Terraform state during verification.

- **R4 — Bounded verification.** A checked, non-mutating restore script must
  reuse the PRD 018 schema verdict and confirm required tables and constraints,
  one-nonterminal-Invocation-per-Session, terminal state consistency, and
  checkpoint/transcript cursor bounds. It must read representative Session,
  Invocation, transcript, ToolCall, and checkpoint records without starting
  execution components. A compatible daemon start/read test is limited to a
  separate terminal-only fixture; no daemon is started against the full restore
  merely to inspect claimable work.

- **R5 — Drill evidence.** Each drill records profile, source revision/schema,
  recovery point, restored revision/schema, start/end time, verification result,
  and cleanup responsibility using the PRD 018 event vocabulary. The readiness
  matrix links the latest record but does not require a new evidence service.

## Acceptance

- [ ] **A1 (R1, R2, R4):** A disposable single-daemon database containing
  completed, queued, waiting, and checkpointed work is backed up, restored under
  a new database name, and returns the same authoritative records through the
  non-mutating verifier. A separate terminal-only fixture proves compatible
  daemon startup and readback without outbound effects.
- [ ] **A2 (R1, R3, R4):** A disposable Cloud SQL backup or PITR restore passes
  the same verification while the source deployment remains unchanged.
- [ ] **A3 (R4):** Corrupt, dirty, incomplete, and incompatible restore fixtures
  fail the checked verifier with a specific safe diagnosis, and no daemon is
  started against them.
- [ ] **A4 (R5):** Both drills produce the documented lightweight record and the
  readiness matrix can point to it without copying credentials or data.

## Follow-up

Operators may later need automated scheduled restore tests, cross-region copies,
tenant-scoped recovery, or stricter RPO/RTO targets. Those should be driven by
real deployment requirements.
