# Keep one-release upgrades and rollback safe

**Status:** Draft
**Sequence:** 019
**Depends on:** `017-prd-production-readiness-profiles.md` and
`018-prd-operational-signals-and-diagnostics.md`

## ELI5

Applying a database migration must not strand the currently running nvoken
binary. The previous release should remain startable until the new release is
proven. This slice supports one-release rollback with forward migrations; it
does not build arbitrary downgrade or blue-green database machinery.

## Why

The paved release migrates before updating services, while the current binary
requires the exact schema version. A successful migration followed by a failed
service rollout can therefore leave the previous revision unable to restart.
Both production profiles need one small, explicit compatibility window before
their operational guides can promise safe upgrades.

## Outcome

Every release either preserves one prior binary's ability to serve the migrated
database or declares and stages an expand/contract sequence before production.
The guarantee begins with the release after a one-time compatibility transition;
it is not retroactively claimed for binaries that require an exact schema.

## Scope

**In:** previous-release schema compatibility; migration classification;
expand/contract rules; preflight checks; single-daemon and Cloud Run upgrade and
rollback procedures; retained-work verification.

**Out:** down migrations; arbitrary historical downgrades; automated data
rewrites outside migrations; dual databases; zero-downtime guarantees for a
single daemon; generalized feature flags.

## Requirements

- **R1 — One-release compatibility window.** After an ordinary forward
  migration, the immediately previous production binary must be able to start
  and serve its supported contract until the new binary is proven. Schema
  validation must distinguish a compatible newer schema from an unknown or
  unsafe schema instead of accepting every version. A transition release must
  first teach the binary to read this compatibility state while retaining exact
  matching; the following release is the first that may claim the window.

- **R2 — Declared compatibility record.** Each migration after the transition
  must author a small machine-readable database record containing the minimum
  binary schema version that may serve the resulting schema. Migrations update
  it transactionally, and startup and release preflight read it; compatibility
  is declared by the migration author and tested, never inferred from SQL. The
  combined and executor roles use the same validation.

- **R3 — Explicit incompatible path.** A migration that removes or changes
  behavior needed by the previous binary must not ship as an ordinary release.
  Its PRD or release note must define an expand release, a compatible cutover,
  and a later contract migration. Committed migration files remain immutable and
  forward-only.

- **R4 — Release preflight.** Before mutating a target database, both profiles
  must identify the current schema, target schema, current image/version, target
  image/version, and whether the ordinary compatibility window is satisfied.
  An unsupported combination fails before traffic or durable data changes.

- **R5 — Retained work survives version overlap.** Queued, running, waiting,
  callback-bearing, and terminal records created by the previous release must
  remain readable and safely claimable or terminal under the new release.
  Rolling back within the supported window must not duplicate model work,
  ToolCall results, callbacks, or terminal settlement.

- **R6 — Small profile-specific procedures.** The single-daemon guide must cover
  stop, migrate, start, verify, and rollback with its expected interruption. The
  Google guide must cover migration success followed by service failure,
  revision rollback, and execution-mode overlap without deleting uncertain
  tasks or rows. Both procedures emit versioned evidence through PRD 018 events.

## Acceptance

- [ ] **A1 (R1, R2):** After the transition release, an N+1-compatible database
  accepts pinned-schema N and N+1 test binaries at runtime. An unsafe or unknown
  schema fails startup before serving. The test harness pins the prior schema
  contract without requiring CI to fetch a historical release image.
- [ ] **A2 (R3, R4):** A fixture migration that would break N cannot pass the
  ordinary release preflight before database mutation and has a documented
  expand/contract example.
- [ ] **A3 (R5):** Integration tests seed each nonterminal/terminal work shape
  with N, migrate, run N+1, then exercise the N rollback window; authoritative
  state and first-writer/idempotency invariants remain intact.
- [ ] **A4 (R6):** Disposable single-daemon and Google drills cover successful
  upgrade, failed migration, successful migration plus failed deploy, and
  rollback, recording the final binary/schema pair, build or image identity, and
  durable readback.

## Follow-up

Longer support windows, automated canaries, online backfills, and release-train
policy should wait until nvoken has enough releases and users to justify them.
