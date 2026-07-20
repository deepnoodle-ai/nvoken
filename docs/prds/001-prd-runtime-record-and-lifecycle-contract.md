# Freeze the Runtime Record and Lifecycle Contract

**Status:** Complete

**Sequence:** 001

**Depends on:** None

## ELI5

Before we create tables or run a model, we need one answer for what an agent
call means after retries, crashes, or process changes. This PRD fixes those
identity, lifecycle, and durability rules and makes the transcript the one
lasting conversation record. It produces a contract, not a working runtime.

## Why

The first schema and admission work will harden whichever identity, lifecycle,
and durability rules it encodes. Those rules need to be explicit before tables
or handlers make them expensive to change.

Mobius Cloud provides useful precedent, but not a contract to copy. Its current
direct-invocation service locks a Session and performs idempotency lookup,
active-turn enforcement, turn creation, and input append in one transaction.
Its current turn schema also separates lifecycle state and revisions from the
ordered Session messages. Most importantly, its transcript source-of-truth
design reverses an earlier co-equal `session_events` log because two durable
encodings of conversation content can diverge. nvoken adopts those invariants,
not Mobius Cloud's organization/project hierarchy or broader API surface.

## Outcome

nvoken has one reviewable launch contract for creating durable background
Invocations and reading their identity and state. The contract fixes the
Account, Agent, tenant, Session, idempotency, lifecycle, transcript, and topology
semantics that the next PRDs may safely encode. Completion is a contract work
product; it does not implement admission or execution.

## Scope

**In:** Agent identity anchors; effective tenant partitioning; Session
resolution; Invocation idempotency and lifecycle; the admission transaction;
canonical transcript and change-feed responsibilities; background JSON
acknowledgement, reads, and errors; topology-neutral semantics; focused OpenAPI,
examples, and design-decision updates.

**Out:** Database choices and detailed table design; model execution; leases and
fencing; cancellation endpoints and budgets; SSE and live deltas; tools;
structured output; spec references; indexed metadata and retention management;
the private delivery protocol; Cloud Tasks resource identities; SDKs.

## Requirements

- **R1 — Account, tenant, and Agent identity.** Every Invocation must execute
  within its authenticated Account and name one stable, caller-controlled
  `agent_ref`. nvoken must resolve or auto-create an Agent identity anchor for
  that reference, unique within the Account; the anchor stores identity, not
  mutable execution configuration or tenant data. The Agent namespace is
  deliberately Account-wide, so its ID may be shared across tenant partitions.
  For an Account-wide credential, explicit `tenant_ref` selects a partition;
  omission uses the default partition for Session-key resolution or creation,
  while by-ID access may resolve any partition in the Account. A
  tenant-constrained credential is confined to its constraint; an explicit
  mismatch must return `403 forbidden` before resource lookup.

- **R2 — Unambiguous Session resolution.** A request must supply at most one of
  `session_id` and `session_key`. A Session ID must resolve an existing Session
  for the same Account and Agent and must match an explicit or credential-bound
  tenant. If an Account-wide caller omits `tenant_ref`, the stored Session
  supplies the partition; if it supplies a mismatching `tenant_ref`, the result
  is `404 not_found`. A Session key must resolve or create one Session in
  `(Account, effective tenant partition, Agent, session_key)`. Omitting both
  must create a new Session. `tenant_ref` and Agent identity are immutable on a
  Session. The same key in two tenant partitions resolves different Sessions.

- **R3 — Small, terminal lifecycle.** Public Invocation states must be
  `queued`, `running`, `waiting`, `completed`, `failed`, and `cancelled`.
  `completed`, `failed`, and `cancelled` are terminal and immutable; the first
  valid terminal settlement wins. Deadline or budget exhaustion must be a
  typed terminal failure, not a separate `expired` state. At most one
  Invocation in `queued`, `running`, or `waiting` may exist for a Session.
  `waiting` is reserved now for later durable ToolCalls.

- **R4 — Stable admission idempotency.** The request must carry a body
  `idempotency_key`, scoped to `(Account, effective tenant partition, agent_ref,
  idempotency_key)`. For a Session key or no selector, the partition comes from
  the credential constraint, explicit `tenant_ref`, or default, in that order.
  For a Session ID, nvoken must authorize and read the Session's immutable
  partition before deduplication, without mutating records. This keeps the key
  usable when no Session existed while preserving per-tenant namespaces.
  Replaying the same logical request must return the original records without
  appending input. Material equality covers the Session selector kind and value,
  normalized inline spec, and normalized input; object order is ignored, array
  order is significant, and documented defaults are applied. Agent and tenant
  are scope fields, so the same key in a different tenant is separate work. A
  changed material field in the same scope returns `409 idempotency_conflict`.
  Within admission, same-key deduplication must run before the one-nonterminal
  check, so an equal replay of queued, running, or waiting work returns the
  original acknowledgement. The guarantee lasts while the Invocation is
  retained. Unlike Mobius Cloud's per-Session behavior, a changed replay is not
  silently accepted.

- **R5 — One durable admission transaction.** Agent resolution or creation,
  Session resolution or creation, the immutable inline execution-spec
  snapshot, one caller-input message, and one queued Invocation must commit in
  one Postgres transaction. The queued Invocation is the initial durable work
  intent. No execution process may claim it before all related records are
  visible. A response may acknowledge the Invocation only after commit; a
  rollback must leave no partial state. Input must contain at least one
  supported content block; this contract has no input-less continuation turn.

- **R6 — One durable content record.** Ordered `SessionMessage` records must be
  the sole durable representation of transcript content, including caller and
  agent content and, later, ToolCall requests and results. Invocation records
  and append-only state revisions may record lifecycle, provenance, cursors,
  and references to message sequences, but must not persist a second copy of
  message content. The record model must let the recovery PRD
  (`prd-recovery-and-transcript-reads`) later compose message sequence and
  Invocation state revisions into an incremental view; this PRD defines no feed
  endpoint or cursor. A generic public Session event-append API is not part of
  this contract.

- **R7 — Durable background JSON contract.** `POST /v1/invocations` must accept
  caller input, `agent_ref`, `idempotency_key`, the optional tenant and Session
  selectors, and a typed inline execution spec. The launch spec subset is
  instructions plus model/provider selection; fields deferred to later PRDs,
  including references, tools, structured output, and budgets, must be rejected
  rather than ignored. In this background JSON mode, the endpoint must return
  `202 Accepted` for a new admission or idempotent replay, with `agent_id`,
  `session_id`, `invocation_id`, current status, and `deduplicated`. The handler
  must never own model execution; a replay still returns `202` when the current
  status is terminal. `GET /v1/invocations/{invocation_id}` and
  `GET /v1/sessions/{session_id}` return authoritative identity and state;
  post-admission failures appear on the Invocation. A later SSE mode may extend
  this endpoint without changing the background contract.

- **R8 — Stable errors and ambiguous-outcome recovery.** HTTP errors must use
  one JSON envelope with stable `code`, human-readable `message`, `request_id`,
  and optional structured `details`. It must define `invalid_request` (400),
  `unauthenticated` (401), `forbidden` (403), `not_found` (404),
  `idempotency_conflict` (409), `session_invocation_active` (409),
  `rate_limited` (429), `internal` (500), and `unavailable` (503). A caller that
  receives a 5xx or no acknowledgement must retry with the same body
  idempotency key. Missing and incompatible resources share `not_found` when
  needed to avoid revealing existence outside authorized partitions.

- **R9 — Topology-neutral durability.** Self-contained polling and a separate
  delivery-triggered executor must expose identical public admission and read
  behavior. Process roles, delivery IDs, Cloud Tasks names, claims, leases, and
  fencing tokens must not appear in the public contract. A committed
  Invocation must remain readable across API-process loss. Until checkpointed
  recovery ships, engine loss may settle the Invocation as a visible typed
  failure; this contract must not promise continuation from the interrupted
  point.

- **R10 — Coherent contract artifacts.** A focused Runtime OpenAPI document
  must define the create acknowledgement, minimum Invocation and Session reads,
  request and error schemas, enums, and examples required above. Worked
  examples must cover normal admission, an ambiguous disconnect followed by
  replay, a conflicting replay, and a distinct request racing an active
  Invocation. The design packet and decision log must be updated to supersede
  the event-log model in `api.md` and decision 10; add tenant partitioning to
  Session uniqueness in `vision.md`, `claims.md`, and `architecture.md`; remove
  `expired` as a state in `architecture.md`; replace “active” with the
  one-nonterminal-slot rule in `api.md`, `claims.md`, and `architecture.md`; and
  calibrate crash-resume claims in `vision.md`, `claims.md`, and
  `architecture.md`. The decision log must also record that a terminal
  idempotent replay deliberately returns the background acknowledgement's
  `202`, not `200`.

## Acceptance

- [x] **A1 (R1, R2):** Contract examples show first-use Agent and Session
  creation, existing ID and key resolution, omission of both selectors, tenant
  constraint inheritance, and two tenant references resolving the same
  `session_key` to distinct Session IDs. An Account-wide credential can read
  either Session by ID; a constrained credential can read only the Session in
  its partition, and an explicit constraint mismatch returns `403` without
  resource lookup.
- [x] **A2 (R3):** The OpenAPI enum and lifecycle table contain exactly the six
  states above, identify the three terminal states, classify deadline expiry as
  `failed`, reject terminal rewrites, and reserve one nonterminal slot per
  Session.
- [x] **A3 (R4, R5, R7):** A worked retry begins with no Session selector, loses
  the first `202`, and replays the same key while the original is nonterminal.
  Both acknowledgements identify one Session, one Invocation, and one
  caller-input message.
- [x] **A4 (R4, R8):** Within one deduplication scope, changing normalized input,
  Session selector, or execution spec produces `409 idempotency_conflict` and
  does not modify the original records. The same key in a different tenant
  partition admits distinct work, including when its partition is obtained
  from a supplied Session ID.
- [x] **A5 (R3, R5, R8):** A distinct key racing a nonterminal Invocation
  produces one durable winner and `409 session_invocation_active` for the
  loser, with no losing input message. A new key is admissible after the winner
  is terminal.
- [x] **A6 (R5):** The admission transaction boundary is documented with
  failure points before commit and after commit: every pre-commit failure
  leaves no claimable work or orphan input, while every acknowledged result is
  fully readable after API restart.
- [x] **A7 (R6):** The record model and recovery example reconstruct all
  durable content from Session messages plus lifecycle from state revisions;
  no second record stores message or ToolCall-result payloads, and no generic
  Session event POST remains in the launch surface.
- [x] **A8 (R4, R7, R8):** The focused OpenAPI validates and includes the
  minimal typed inline spec, material-equality rules, background `202`,
  authoritative reads, common error envelope, and examples for every named
  conflict and ambiguous-outcome path. The ambiguous example covers a 5xx or
  lost response followed by same-key replay. Deferred spec fields and empty
  input are rejected.
- [x] **A9 (R9):** The same public contract fixtures apply unchanged to
  self-contained and external execution modes and expose no delivery-system
  fields. Engine-loss documentation promises a durable visible outcome, not
  crash resumption.
- [x] **A10 (R10):** `vision.md`, `claims.md`, `architecture.md`, `api.md`, and
  `decisions.md` agree with the accepted OpenAPI on Session namespace,
  lifecycle, transcript authority, idempotency, topology, and durability; a
  repository search finds no remaining normative contradiction.

## Deferred decisions

- Resolved by PRD 002: runtime records use prefixed UUIDv7 keys; Agent and
  Invocation use `agnt_` and `invk_`; request fingerprints are 32-byte SHA-256;
  partial unique indexes enforce nonterminal and idempotency scopes; and no
  idempotency or runtime-history cleanup exists in the initial store.
- The recovery PRD will finalize the opaque composite cursor and fixed-cut
  pagination semantics for transcript changes.
- The client-tools PRD will choose narrow commands for ToolCall results instead
  of reviving a generic event append endpoint.
