# Define two lightweight production-readiness profiles

**Status:** Draft
**Sequence:** 017
**Depends on:** `006-prd-google-cloud-run-paved-deployment.md`,
`010-prd-cloud-tasks-invocation-execution.md`,
`011-prd-resumable-streaming.md`,
`014-prd-checkpoint-crash-recovery.md`, and
`016-prd-durable-callback-tools.md`

## ELI5

nvoken can run as one daemon or through its paved Google Cloud deployment. We
need a small, honest checklist for calling either shape production ready. This
PRD defines those two profiles and the evidence they need; it does not build the
operational features or promise enterprise availability.

## Why

The runtime already preserves the same public semantics in embedded and Cloud
Tasks execution modes, but “production ready” is not defined. Without a shared
definition, local and Google documentation can drift into unrelated claims.
nvoken is young, so the first contract should identify the minimum useful proof
and leave formal SLO programs, additional platforms, and mature operations
machinery for later evidence to justify.

## Outcome

The repository has one authoritative readiness matrix for a single-daemon
self-hosted profile and the paved Google Cloud profile, with clear boundaries,
required proof, and current status.

## Scope

**In:** profile definitions; shared correctness claims; operator/nvoken
responsibility boundaries; minimum readiness dimensions; evidence status and
documentation ownership.

**Out:** new metrics or commands; dashboards; deployment changes; numeric
availability promises or error budgets; a certification service; nvoken Cloud;
Kubernetes, AWS, or portable multi-daemon profiles.

## Requirements

- **R1 — Exact profiles.** The readiness matrix must reference the normative
  topologies in `docs/design/architecture.md` and the paved Google deployment
  guide, then add only readiness-specific constraints. The initial
  `single_daemon` profile is one combined `nvokend` process using embedded
  execution, operator-provided Postgres, and in-process live events; external
  Redis and additional self-hosted shapes are follow-up profiles. The initial
  `google_cloud` profile is the Terraform-paved combined Runtime, private
  request-bound executor, Cloud Tasks, Cloud SQL, and Memorystore topology. Both
  are open-source deployments in the operator's environment and expose the same
  Runtime contract.

- **R2 — Honest availability boundary.** The single-daemon profile must claim
  safe restart and durable recovery, not high availability; host and Postgres
  availability remain operator responsibilities. The Google profile may claim
  only the availability, scaling, and recovery properties actually configured
  and exercised. Neither profile may turn provider availability into an nvoken
  guarantee.

- **R3 — Minimum readiness dimensions.** Each profile must cover installation,
  normal execution, process/dependency failure, upgrade/rollback, backup/restore,
  diagnosis, capacity, retention, and secret handling. Each dimension records
  its observable proof, evidence mode (`automated` or `manual`), readiness state
  (`proven` or `pending`), and evidence freshness (`current`, `stale`, or
  `missing`). Formal percentages and error budgets are follow-up work;
  this slice requires measurable events and bounds only where the implementation
  already supplies or needs one.

- **R4 — Shared correctness floor.** Both profiles must preserve durable
  admission, Postgres authority, checkpoint recovery, first-terminal-writer
  semantics, canonical transcript storage, ToolCall idempotency, and stale-owner
  fencing. A delivery adapter, process supervisor, or cloud service must never
  be described as execution authority.

- **R5 — Evidence and claim discipline.** `docs/testing/` must contain the
  versioned readiness matrix and link to the proof or procedure for every row.
  A production claim may be marked proven only with repository or recorded
  environment evidence naming the profile and tested revision. Product and
  operator docs must link to this matrix rather than restating a different
  readiness definition.

## Acceptance

- [ ] **A1 (R1, R2):** The matrix describes both profiles from a clean install
  through execution and restart, and a review finds no implied single-daemon HA
  or conflation of the Google paved path with managed nvoken Cloud.
- [ ] **A2 (R3):** Every minimum readiness dimension has one profile-specific
  proof row, owner boundary, evidence mode, readiness state, and freshness; no
  row requires a dashboard, console, or policy service merely to satisfy this
  PRD.
- [ ] **A3 (R4):** The matrix's failure and recovery rows trace durable outcomes
  to Postgres claims, checkpoints, and fences in both execution modes.
- [ ] **A4 (R5):** The root README, architecture, runtime-admission guide,
  Google deployment guide, and PRD roadmap identify the matrix as the readiness
  evidence source and do not make a stronger production claim than it records.

## Follow-up

PRDs 018–025 supply the missing evidence. Numeric availability objectives,
additional self-hosted topologies, and a formal release-support policy should be
added only after real operators and workload measurements make them useful.
