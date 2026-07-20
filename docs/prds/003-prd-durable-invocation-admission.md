# Build Durable Invocation Admission

**Status:** Complete

**Sequence:** 003

**Depends on:** `001-prd-runtime-record-and-lifecycle-contract.md`,
`002-prd-postgres-runtime-spine.md`

## ELI5

A host can submit one agent turn and safely retry if the network drops. nvoken
stores the identity, input, spec, and queued work together before replying, so
there is never half an Invocation. This PRD admits and reads work; it does not
run a model yet.

## Why

PRDs 001 and 002 fixed the public contract and built the Postgres records that
can uphold it. The next useful slice is the service and HTTP boundary that turn
one authenticated request into durable, inspectable work without accidentally
starting execution.

Mobius Cloud's direct-invocation service provides the closest precedent: lock
the Session, check idempotency before active work, and commit the queued turn
with its input. nvoken keeps those invariants but makes changed replays conflict,
snapshots the spec per Invocation, and includes first-use identity records in
the transaction.

## Outcome

An authenticated host can call `POST /v1/invocations`, receive a durable `202`
acknowledgement, retry ambiguous outcomes safely, and inspect the resulting
Invocation and Session through authoritative JSON reads. No goroutine, model
provider, delivery adapter, or execution claim is involved.

## Scope

**In:** Minimal installation runtime auth and Account bootstrap; strict
validation and fingerprinting; transactional identity/Session resolution and
admission; the frozen POST and minimum GET routes; stable errors, database
wiring, and concurrency proof.

**Out:** Model execution; polling; claims, leases, fencing, heartbeats, and
reaping; dispatch intent and Cloud Tasks; transcript list/recovery endpoints;
SSE; cancellation; tools; structured output; budgets; rate-limit enforcement;
portable credential issuance or administration; multi-Account provisioning;
SDK generation.

## Requirements

- **R1 — Authenticated installation context.** Runtime routes must require an
  `Authorization: Bearer` credential and receive an adapter-neutral auth context
  containing the Account ID, an optional `tenant_ref` constraint, and permitted
  runtime operations; Account identity must never come from request JSON. A
  narrow self-hosted adapter must map a configured secret to one default
  Account, optionally constrain one tenant, and serialize first-start bootstrap
  under a lock with a post-lock recheck. Later starts must resolve the sole
  Account, and fail closed if the database contains more than one. Missing
  database or credential configuration must fail startup; secrets must be
  compared without timing leaks and never logged. Health remains public;
  credential records and management APIs remain deferred.

- **R2 — Strict, bounded request contract.** The HTTP adapter must accept only
  the launch request shape already frozen in `openapi/runtime.yaml`: one
  `agent_ref`, one body `idempotency_key`, one or more text input blocks, an
  inline instructions-plus-model spec, optional `tenant_ref`, and at most one
  Session selector. It must reject unknown or deferred fields, duplicate JSON
  member names, trailing values, blank required strings, invalid IDs, and
  invalid UTF-8 before writes. The encoded body must be capped at 1 MiB, input
  at 64 blocks, and `agent_ref`, `tenant_ref`, `session_key`,
  `idempotency_key`, `spec.model.provider`, and `spec.model.name` at 255 Unicode
  characters each. The build must add these limits to OpenAPI, document the body cap
  on the operation, and record the contract refinement in the decision log.
  Failures return `invalid_request` and write nothing.

- **R3 — Stable material-request fingerprint.** Admission must compute a
  SHA-256 fingerprint from a fixed v1 canonical representation of Session
  selector kind and value, inline spec, and input after documented defaults.
  JSON object member order must not matter, array order must matter, and string
  values must not be silently trimmed or rewritten. Account, effective tenant
  partition, Agent reference, and idempotency key define the lookup scope and
  are not duplicated in the fingerprint. The v1 encoding must be documented
  with language-neutral vectors under `docs/design/`; together they are a
  compatibility contract across restarts and releases while the Invocation is
  retained.

- **R4 — Atomic identity and Session resolution.** Within one admission
  transaction, nvoken must resolve or conflict-safely create the Account-wide
  Agent anchor and the effective tenant partition. A credential constraint
  wins over request selection; an explicit mismatch returns `forbidden` before
  resource lookup. Otherwise an explicit nonempty `tenant_ref` resolves or
  creates its internal partition and omission uses the default partition. A
  `session_key` conflict-safely resolves or creates one Session; no selector
  creates a Session. A `session_id` must exist in the Account, belong to the
  Agent, and match any asserted tenant; an Account-wide caller omitting tenant
  adopts its stored partition. Missing or incompatible IDs return the same
  `not_found` without disclosing why.

- **R5 — Idempotency before single-flight.** Once Account, Agent, and effective
  tenant scope are known, same-key lookup must precede the Session's
  one-nonterminal check and, for keyless Session creation, precede creation of a
  replacement Session. An equal replay returns the original Agent, Session,
  and Invocation without another durable row; a changed fingerprint returns
  `idempotency_conflict`. Concurrent equal requests, including no selector or a
  first-use `session_key`, must converge on one admission; uniqueness races
  must not surface as `500`.

- **R6 — Session-serialized admission.** A fresh admission must hold a
  Postgres row lock on the resolved Session across the final idempotency check,
  nonterminal check, sequence allocation, and writes. Existing queued, running,
  or waiting work returns `session_invocation_active` with its ID/status and no
  input append. Two distinct requests racing for an idle existing or newly
  keyed Session must yield one `202` and one typed `409`; the indexes remain
  backstops rather than the normal response path.

- **R7 — One durable admission transaction.** A fresh request must atomically
  persist exactly one immutable spec snapshot, one queued Invocation, one
  caller `SessionMessage`, and one initial queued `InvocationState`, along with
  first-use identity rows and allocated sequences. The initial state must
  reference the input message sequence. Commit must precede `202`; every error
  must roll back the complete admission. A visible queued Invocation must have
  its input and snapshot. Admission must neither signal nor execute work.

- **R8 — Durable acknowledgement and reads.** A new admission and every equal
  replay must return `202` with `agent_id`, `session_id`, `invocation_id`, the
  current status, and an accurate `deduplicated` flag, including replay after
  terminal settlement. `GET /v1/invocations/{id}` must return authoritative
  identity, current state, typed error, and timestamps. `GET /v1/sessions/{id}`
  must return immutable identity, effective `tenant_ref`, optional Session key,
  and the current nonterminal Invocation ID if present. Reads must enforce the
  same auth constraints and remain correct after restart. These minimum reads
  omit transcript content and lifecycle history.

- **R9 — Stable errors and operable serve path.** Every Runtime API error must
  use the contract envelope with a nonempty request ID; expected validation,
  auth, lookup, uniqueness, serialization, and active-work outcomes must map to
  stable public codes without internal text. `nvokend serve` must open a bounded
  pool, verify a present, clean, compatible schema, and never migrate. Logs may
  contain request/route/status/latency and committed IDs, but no credentials,
  input, instructions, or specs. Shutdown closes HTTP and database resources
  without creating or claiming work.

## Acceptance

- [x] **A1 (R1, R9):** Starting two service replicas concurrently against a
  migrated empty database creates one Account/default partition. A valid token
  reaches Runtime routes; missing/wrong tokens return `401`; health stays
  public; logs contain no secrets. An HTTP adapter test with an injected auth
  context that excludes the requested operation returns `403`.

- [x] **A2 (R2, R3):** Table-driven HTTP and fingerprint fixtures prove every
  accepted field and limit, reject unknown/deferred fields, duplicate keys,
  trailing values, invalid UTF-8, two Session selectors, and oversized input
  before writes. Object-key reordering hashes equally; array/string changes do
  not; the documented v1 vectors pass across fresh processes. The OpenAPI
  declares the same field/block limits and body cap, the decision log records
  them, and the pinned OpenAPI lint passes in `make check`.

- [x] **A3 (R4):** End-to-end cases prove Account-wide Agent reuse across two
  tenant references; automatic partition creation; default-partition use;
  same-key Session isolation by tenant; no-selector creation; and by-ID lookup
  with omitted tenant. Credential mismatch returns `403` before lookup;
  cross-Account, cross-Agent, and incompatible IDs share one `404` envelope.

- [x] **A4 (R5, R7, R8):** After a fresh request commits but its connection is
  dropped before the acknowledgement, an equal retry returns `202` with the
  original three IDs and `deduplicated=true`. Database readback contains one
  snapshot, Invocation, caller message, and queued state whose watermark names
  that message. Replay after terminal settlement still returns `202` with no
  new row.

- [x] **A5 (R3, R5):** Reusing a key in the same scope with a changed selector,
  input, or spec returns `409 idempotency_conflict` and does not alter the
  original admission. The same key in another tenant partition admits distinct
  work. Twenty concurrent equal first-use requests converge on one Session,
  Invocation, input, and identity set without an internal error.

- [x] **A6 (R4, R6):** Two distinct keys racing for one idle existing Session
  and one first-use `session_key` each yield one `202` and one
  `session_invocation_active` `409`; only the winner appends input. After it is
  terminal, another key is accepted.

- [x] **A7 (R7, R9):** Fault injection at each repository write and at commit,
  plus cancellation while waiting on a Session lock, leaves no partial
  admission. A commit succeeds as one unit, and no execution, delivery, or
  notification callback occurs.

- [x] **A8 (R1, R8, R9):** Black-box HTTP tests admit work, restart the API
  process, and read identical state with the documented JSON/auth behavior.
  Every failure has a request ID and stable code; logs redact content and auth.

- [x] **A9 (R9):** `nvokend serve` succeeds only against the expected clean
  schema, reports not-ready or exits on an empty, dirty, or newer schema, and
  creates no schema objects there. `nvokend migrate` remains the only schema
  mutation path.

## Risks and sequencing notes

- The installation authenticator is intentionally a narrow bootstrap adapter,
  not the deferred portable identity/admin product. Keeping the auth context as
  a port lets nvoken Cloud replace it without changing admission semantics.
- This slice extends the PRD 002 repository ports with a locked Session read
  and a current-nonterminal-Invocation-by-Session lookup; the existing ports do
  not yet expose either operation.
- PRD 004 owns claiming the queued row. This PRD must leave accepted work
  inert; proving that it is eventually claimed or reaped begins only after
  ownership and fencing exist.
