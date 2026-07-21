# Add the minimum Google Cloud operations package

**Status:** Implemented; Google Cloud proof and readiness matrix pending
**Sequence:** 023
**Depends on:** `010-prd-cloud-tasks-invocation-execution.md`,
`011-prd-resumable-streaming.md`,
`016-prd-durable-callback-tools.md`,
`017-prd-production-readiness-profiles.md`,
`018-prd-operational-signals-and-diagnostics.md`,
`020-prd-backup-restore-and-recovery.md`, and
`021-prd-initial-retention-posture.md`

## ELI5

The paved Google deployment should tell an operator when its main paths are
unhealthy and where to look first. This slice adds one practical dashboard, a
small high-signal alert set, and short runbooks. It does not build a full SRE
program, formal error budgets, or an operator console.

## Why

Terraform already provisions the topology and several dispatch alerts, while
runtime logs contain most of the remaining signals. Operators still lack a
single view across API, execution, callbacks, providers, Cloud SQL, and the
queue, and notification channels may be empty. A compact package using existing
Cloud Monitoring and log-based metrics is enough for the first production
deployment.

## Outcome

The Google profile provides one Terraform-managed dashboard, actionable alerts
with notification wiring, and a runbook for every alert.

## Scope

**In:** one dashboard; a minimum alert set; production configuration guidance;
notification channels; safe log queries; alert-specific runbooks; database and
queue platform signals.

**Out:** multiple persona dashboards; formal SLO/error-budget resources;
PagerDuty or ticketing automation; distributed tracing; a data warehouse;
cross-project fleet views; a web console.

## Requirements

- **R1 — One useful dashboard.** Terraform must create or optionally enable one
  dashboard showing public request volume/errors/latency, runnable-delivery age
  and terminal outcomes, provider outcomes/latency, callback retries/failures,
  dispatch health, executor attempts, Cloud Tasks depth/age, and essential Cloud
  SQL/Redis resource health. For this profile, runnable-delivery age is composed
  from existing aged-dispatch events, Cloud Tasks queue depth, and Cloud Tasks
  attempt delay; Cloud Tasks does not expose a native oldest-task-age metric, and
  this slice does not add a direct Invocation-age metric. Empty or unavailable
  signals must be labeled rather than interpreted as success.

- **R2 — Small high-signal alert set.** The profile must alert on sustained
  public 5xx/unavailability, aged runnable work, repeated provider failure,
  callback exhaustion or worker failure, existing dispatch/executor failures,
  repeated task-delivery rejection including executor authentication failure,
  and imminent Cloud SQL connection/storage exhaustion. Add backup/instance
  health alerting only where Google exposes a reliable signal. Thresholds and
  windows must be configurable and conservative enough to avoid paging on one
  normal provider or callback error.

- **R3 — Production notification boundary.** The guide must require at least one
  tested notification channel before the profile is marked production ready.
  Terraform may still permit an empty list for disposable environments, but its
  outputs and documentation must make the non-notifying state obvious.

- **R4 — Alert-specific runbooks.** Every alert must link to a short procedure
  in `deploy/google-cloud/runbooks.md` containing meaning, first queries,
  correlation fields, safe mitigations, recovery signal, and escalation
  boundary. Queue pause/resume, execution-mode rollback, and database actions
  must preserve Postgres authority and must not recommend deleting uncertain
  tasks or rows.

- **R5 — Production baseline.** The guide must identify the initial production
  settings that differ from cheap development defaults, including regional
  database availability, database and service deletion protection, notification
  channels, capacity totals, and the exercised PRD 020 backup/PITR procedure. It
  must distinguish required safety settings from workload-dependent sizing
  advice.

## Acceptance

- [ ] **A1 (R1):** Terraform validation proves the dashboard is wired to the
  deployed resource names and bounded event classes, and a disposable deployment
  displays signals from one successful and one failed Invocation and callback.
- [ ] **A2 (R2–R4):** Controlled log-derived failures open their alerts, notify a
  test channel, link the correct runbook, and close after recovery. Platform-
  metric policies are tested with temporary safe thresholds or documented
  condition simulation; one ordinary provider failure does not open a sustained
  alert.
- [ ] **A3 (R3, R5):** The readiness matrix keeps the Google notification and
  production-baseline rows `pending` until evidence exists, while development
  Terraform remains usable. Automated claim enforcement remains PRD 025 work.
- [ ] **A4 (R4):** Following the aged-work and provider/callback runbooks in a
  disposable environment preserves authoritative rows and produces the stated
  recovery signal.

## Follow-up

Numeric SLOs, burn-rate alerts, tracing, richer cost views, and an operator
console should follow real incident and volume evidence rather than precede it.
