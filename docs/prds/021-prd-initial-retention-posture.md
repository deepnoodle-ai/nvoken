# Make the initial retention posture explicit

**Status:** Implemented
**Sequence:** 021
**Depends on:** `017-prd-production-readiness-profiles.md` and
`018-prd-operational-signals-and-diagnostics.md`

## ELI5

nvoken should not silently delete conversation history before its deletion
contract is designed. For now, authoritative Session and Invocation evidence is
retained, short-lived transport diagnostics keep their existing bounds, and
operators can see storage growth. This slice documents and observes that simple
posture rather than building archival, compaction, or tenant deletion.

## Why

The schema intentionally retains authoritative runtime history and prunes only
terminal dispatch and callback-delivery diagnostics. That is a defensible young-
service default, but it is not yet presented as an operational policy and it can
produce unbounded storage growth. The lightweight next step is to make the data
classes and consequences explicit and give operators basic growth evidence.

## Outcome

Both profiles publish one conservative retain-by-default policy, preserve the
existing bounded diagnostic pruning, and expose enough storage information to
plan the later lifecycle contract.

## Scope

**In:** data-class inventory; current default retention; diagnostic retention
configuration; storage-growth observation; backup/deletion caveats; bounded
prune verification.

**Out:** Session or tenant deletion; legal-hold policy; transcript compaction;
archive/export; cursor expiry; per-tenant quotas; automatic authoritative-data
pruning; changing the public API.

## Requirements

- **R1 — Authoritative classes.** Documentation must reference the design
  packet's Data and retention section as the authoritative inventory rather than
  restating a partial list. Redis previews, Cloud Tasks, and execution-dispatch/
  callback transport rows are not alternate transcript or business-history
  stores.

- **R2 — Conservative default.** The initial profiles must retain authoritative
  data indefinitely until a later contract defines ordered deletion. No new
  background task may delete or compact that data in this slice. The policy must
  plainly state its storage and privacy tradeoff.

- **R3 — Bounded diagnostics.** Operator docs must name the finite defaults and
  configuration for execution-dispatch and callback-delivery retention and
  bounded prune batches. Pruning must not delete authoritative Invocation,
  ToolCall, attempt, result, structured-output, or transcript evidence. Invalid
  dispatch settings, and invalid callback settings when callbacks are enabled,
  fail startup with an attributable error.

- **R4 — Growth visibility.** The portable guide must document a safe query for
  database size and the largest nvoken tables. The paved Google deployment guide
  must identify the corresponding Cloud SQL storage signal. No per-tenant
  analytics or automated scaling policy is required.

- **R5 — Backup and future deletion boundary.** The guides must explain that
  deleting live data in a future release will not instantly remove it from
  retained backups and that backup expiry is part of any future deletion
  promise. They must name Session/tenant deletion, compaction, cursor behavior,
  and archive/export as unresolved follow-up contracts.

## Acceptance

- [x] **A1 (R1, R2):** Schema, migration, API, and operator docs agree on which
  rows are authoritative and confirm that no production path destructively
  prunes them.
- [x] **A2 (R3):** Integration tests age terminal dispatch and callback rows,
  prune them in bounded batches, and prove their authoritative owners and
  evidence remain readable. Invalid dispatch settings and invalid enabled-
  callback settings fail startup with an attributable error; operator docs name
  the corresponding configuration and defaults.
- [x] **A3 (R4):** The portable guide's safe query and the paved Google
  deployment guide's Cloud SQL signal identify total database size and the
  largest runtime tables without reading transcript content.
- [x] **A4 (R5):** The readiness matrix records retain-by-default as an explicit
  limitation and links to the deferred deletion/compaction questions.

## Follow-up

A later PRD should design Session and tenant deletion only when actual host
requirements can settle retention duration, cursor expiry, backup behavior, and
whether archive or compaction is needed.
