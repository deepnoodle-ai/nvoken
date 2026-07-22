# Add portable operational signals and diagnostics

**Status:** Implemented
**Sequence:** 018
**Depends on:** `017-prd-production-readiness-profiles.md`

## ELI5

An operator should be able to tell whether nvoken is alive, connected, and
making progress without reading source code. This slice makes the existing logs
consistent and adds one bounded diagnostic check. It does not introduce a full
metrics, tracing, or observability platform.

## Why

nvoken already emits structured request, Invocation, dispatch, generation, and
callback logs. The events are sufficient groundwork, but names and failure
details are inconsistent, `/health` proves process liveness only, and provider
failures are grouped too broadly for incident diagnosis. A small portable signal
contract lets local operators use ordinary logs and lets Google Cloud derive
dashboards and alerts later.

## Outcome

Both production profiles expose the same safe startup identity, lifecycle event
vocabulary, liveness behavior, and one-shot dependency diagnostic.

## Scope

**In:** stable structured event names and bounded outcome classes; startup
identity; process liveness documentation; a one-shot diagnostic command; log
redaction and shape tests; a short event catalog.

**Out:** Prometheus or OpenTelemetry adoption; distributed traces; a metrics
database; dashboards or alert policies; a long-running admin endpoint; provider
health polling; an operator console.

## Requirements

- **R1 — Stable event vocabulary.** Structured logs must assign stable event
  names and bounded outcome fields to HTTP requests, Invocation claim/recovery/
  settlement, provider calls, callback delivery, dispatch publication/attempt,
  live-event publish/subscribe and stream degradation, and maintenance failures.
  Existing correlation IDs may remain high-cardinality log fields, but
  documented metric dimensions must be bounded.

- **R2 — Useful failure classes.** Provider outcomes must distinguish at least
  configuration/unsupported provider, throttling, upstream rejection or outage,
  timeout/transport failure, invalid response, and success when the underlying
  error exposes that distinction. Callback claim, processing, recovery, and
  pruning failures must include a safe reason or error class. Remote bodies,
  prompts, tool payloads, URLs, credentials, and signing material remain absent.

- **R3 — Startup identity.** A successful process start must log the nvoken
  build/version, expected database schema version, process role, execution mode,
  and enabled provider/callback capabilities without secret values. Fatal
  configuration or schema failures must identify the failed check before exit.
  Release builds inject the build identifier into the binary; local builds use
  a documented `devel` fallback.

- **R4 — Cheap liveness, explicit diagnostics.** `/health` remains an
  unauthenticated process-liveness response and must not trigger provider calls
  or restart loops during dependency incidents. A one-shot operator command must
  validate configuration, database connectivity and schema compatibility, and
  role-required dependencies using the same schema verdict as the serve path.
  It exits zero when every check passes and nonzero with safe per-component
  results when any check fails. It must not mutate durable state or send
  callbacks/model requests.

- **R5 — Portable catalog.** One concise guide in `docs/guides/` must map each
  event and diagnostic result to its meaning, useful correlation fields, and
  first operator check. Cloud-specific queries and policies remain in the
  Google profile.

## Acceptance

- [x] **A1 (R1, R2):** Tests capture representative success, retry, recovery,
  provider failure, and callback failure logs and prove stable events, bounded
  classes, useful error evidence, and absence of sensitive content.
- [x] **A2 (R3):** Combined and executor startup tests emit the correct role,
  mode, schema, version, and enabled-capability fields; invalid configuration
  exits with one attributable failure and no secret.
- [x] **A3 (R4):** Against healthy, unreachable, empty, dirty, behind, and ahead
  Postgres states, the diagnostic command exits zero only for the healthy
  compatible case and nonzero for each failed check, without writes. Once PRD
  019 lands, an ahead schema is reported as compatible or unsafe from its
  declared compatibility record; unknown ahead schemas fail. `/health` remains
  fast and independent of those dependency states after the process has started.
- [x] **A4 (R5):** An operator can use only the event catalog and captured logs
  to classify one Invocation recovery, provider, callback, dispatch, live-event,
  and database incident without inspecting source code.

## Follow-up

PRD 023 turns these portable events into the first Google dashboard and alerts.
Native metrics, tracing, exemplars, and an operator UI remain later options.
