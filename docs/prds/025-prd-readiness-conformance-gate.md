# Keep production-readiness evidence current

**Status:** Draft
**Sequence:** 025
**Depends on:** `022-prd-single-daemon-production-profile.md` and
`024-prd-google-cloud-qualification.md`

## ELI5

Production readiness should not be a claim we prove once and forget. One small
command and checklist will show which automated checks pass and which manual
drills are current for each profile. This is not a certification platform or a
new control plane.

## Why

The preceding PRDs create tests, smoke scripts, runbooks, and drill evidence in
different places. Without a final lightweight gate, those artifacts can drift
while README and release claims remain unchanged. The repository needs one
summary that is cheap to run, honest about manual work, and easy to adjust as
the young service learns.

## Outcome

Each release can produce a concise, versioned readiness result for
`single_daemon` or `google_cloud`, with automated failures and missing manual
evidence visible before a production-ready claim is made.

## Scope

**In:** profile-selectable check command; readiness matrix updates; automated
test orchestration; manual evidence references; documentation consistency;
secret-free result artifact.

**Out:** hosted certification; enforcement service; deployment approval UI;
remote fleet inventory; automatic incident management; immutable compliance
archives; a mandatory calendar cadence unsupported by operator needs.

## Requirements

- **R1 — One profile-selectable gate.** A checked-in `make readiness` entry
  point, backed by one small Python script, must run the safe automated checks
  for one selected profile by invoking existing build, Postgres, deployment,
  smoke, and diagnostic commands. It may delegate an explicitly selected live
  Google step to PRD 024's Python entry point. It must not provision, deploy,
  restore, send model requests, or mutate a live environment by default.

- **R2 — Manual work stays explicit.** Restore, rollback, cloud failure, and
  load drills that cannot run in ordinary CI must remain named checklist rows
  with their latest evidence link and tested revision. Each row carries a short
  list of evidence-sensitive repository paths and an optional explicit
  invalidation. It is stale when those paths changed after the tested revision
  or it was invalidated. Manual evidence is `proven` only while current; stale
  or missing evidence leaves the row `pending`.

- **R3 — Small evidence artifact.** A run must produce human-readable output and
  an optional machine-readable summary containing profile, nvoken revision,
  schema expectation, time, checks, evidence references, and result. It must not
  contain environment variables, credentials, request bodies, transcripts, or
  Terraform state. Committed manual evidence records live under
  `docs/testing/readiness/evidence/`; ordinary local results need not be
  committed.

- **R4 — Claim gate.** The command must compare its computed result with the
  profile status recorded in the PRD 017 matrix and exit nonzero when the claim
  is stronger than the evidence. Primary docs link that status rather than
  owning copies. A pending optional/follow-up capability must not block the
  profile when it is explicitly outside that profile's contract. With automatic
  release blocking deferred, this command is the enforcement seam.

- **R5 — Documentation consistency.** The matrix must name the authoritative
  repository source for each checked fact: profile names, configured provider
  registry, OpenAPI tool modes/version, migration head, and document links. The
  gate compares only those facts across README, OpenAPI, guides, and the matrix;
  explicitly labeled design-scope aspirations are not implementation claims. It
  need not perform broad prose linting.

## Acceptance

- [ ] **A1 (R1):** On a clean checkout with an operator-supplied disposable
  Postgres, the single-daemon gate runs its safe automated checks without Google
  credentials; the Google gate includes Terraform/deployment checks and clearly
  skips live integration steps unless explicitly enabled.
- [ ] **A2 (R2–R4):** Missing restore evidence, a stale rollback record, and a
  failing automated smoke each produce a non-ready result naming the exact row;
  an explicitly deferred optional capability does not.
- [ ] **A3 (R3):** With machine-readable output enabled, the human and machine
  results identify the same revision, profile, checks, and outcome, and a
  secret-content test finds no credential, payload, transcript, or Terraform-
  state value.
- [ ] **A4 (R4, R5):** A fixture documentation contradiction or failed required
  check makes the command reject a stronger recorded claim; an explicitly
  labeled design aspiration is ignored. Fixing the mismatch returns the summary
  to ready without modifying runtime state.

## Follow-up

CI-provider integration, signed attestations, compliance retention, fleet-wide
status, and automatic release blocking can be added later if release volume or
customer requirements justify them.
