# Recover Sessions, Invocations, and Transcript State

**Status:** Complete

**Sequence:** 007

**Depends on:** `001-prd-runtime-record-and-lifecycle-contract.md`,
`002-prd-postgres-runtime-spine.md`,
`003-prd-durable-invocation-admission.md`,
`004-prd-engine-claims-and-fencing.md`,
`005-prd-generation-only-turns.md`

## ELI5

After a disconnect or deploy, a host can rebuild what happened using only the
Session and Invocation IDs it already received. It can page through the real
transcript and lifecycle changes without missing or reordering terminal state.
This adds durable JSON recovery, not live streaming, cancellation, or tools.

## Why

nvoken durably admits and executes generation-only turns, but its public reads
still expose only one Invocation's basic status and one Session's identity.
Assistant messages, normalized usage, provenance, Invocation history, and an
incremental reducer input are present in Postgres but not recoverable through
the Runtime API.

Mobius Cloud's current transcript snapshot provides the relevant precedent: it
uses independent message and turn-revision watermarks, fixes the upper cut when
pagination begins, and drains durable messages before terminal turn state so a
client never removes a live preview before receiving its replacement. nvoken
keeps that ordering property while projecting its smaller canonical
`SessionMessage` plus `InvocationState` model. It does not port Mobius's second
turn model, interactions, live previews, compaction, or project namespace.

## Outcome

An authenticated host can list and inspect Sessions and Invocations, read the
canonical transcript, and repeatedly drain a fixed-cut incremental Session
snapshot. Restarting any API or engine process does not change the result.

## Scope

**In:** richer Invocation and Session reads; bounded keyset-paginated lists;
exact tenant filtering; forward transcript pagination; opaque scope-bound
cursors; a fixed-cut incremental snapshot over message sequence and lifecycle
revision; terminal usage, provenance, and error projection; read-path indexes;
OpenAPI and recovery examples. The existing `active_invocation_id` field
remains compatible; this slice adds its status as a sibling field and records
that additive contract evolution in the decision log.

**Out:** SSE and ephemeral token deltas; cancellation and budgets; ToolCalls or
pending interactions; structured output; spec disclosure; usage-event
accounting; retention and cursor expiry; transcript compaction, reverse paging,
search, or metadata filters; SDK generation. PRD 011 projects this read model as
SSE, and PRD 015 adds recoverable pending client ToolCalls.

## Requirements

- **R1 — Authoritative Invocation reads and history.**
  `GET /v1/invocations/{invocation_id}` must expose the current public
  Invocation identity, status, error, timestamps, and nullable aggregate
  `usage` and `provenance`. `GET /v1/invocations` must return the same shape in
  newest-first keyset order and support optional exact filters for `session_id`,
  `agent_id`, `tenant_ref`, and `status`. Filters combine with AND. Public reads
  must never expose the spec snapshot, idempotency material, request
  fingerprint, lease owner, lease expiry, or fencing attempt.

- **R2 — Authoritative Session reads and history.**
  `GET /v1/sessions/{session_id}` and `GET /v1/sessions` must expose immutable
  Session identity, tenant reference, host key, timestamps, and the current
  nonterminal Invocation through the existing nullable `active_invocation_id`
  plus a new nullable `active_invocation_status`; the two fields must be present
  or null together. The Session list must use newest-first keyset order and
  support optional exact filters for `agent_id`, `tenant_ref`, and
  `session_key`. Reads must derive active state from authoritative Invocation
  rows; lifecycle notifications or process memory are not inputs.

- **R3 — Bounded collection cursors.** Invocation and Session lists must accept
  `limit` with a default of 100 and maximum of 200 and return `items`,
  `has_more`, and nullable `next_cursor`. Cursors must be opaque, versioned, and
  bound to the authenticated Account, collection kind, sort position, and
  normalized filters, including the effective tenant scope. For a constrained
  credential, omission and a matching `tenant_ref` both mean that credential's
  partition. For an Account-wide credential, omission means all partitions,
  `tenant_ref` selects one non-default partition, and `default_tenant=true`
  selects only the default partition; `tenant_ref` and `default_tenant` are
  mutually exclusive. Reusing a cursor with another Account, collection, or
  normalized filter set—including a different effective tenant scope—must
  return `400 invalid_request`. Pagination must use stable `(created_at, id)`
  keysets rather than offsets and must not duplicate an item while traversing an
  unchanged collection. The single `status` filter accepts any one public
  Invocation status, including reserved `waiting`.

- **R4 — Canonical transcript pagination.**
  `GET /v1/sessions/{session_id}/messages` must return `SessionMessage` rows in
  ascending sequence order with stable message, Session, Invocation, role,
  content, sequence, and creation fields. Content must be the stored canonical
  block array, not a provider response or lifecycle copy. The endpoint must use
  an opaque Account-and-Session-bound forward cursor, the same 100/200 limits,
  and `items`/`has_more`/`next_cursor`. Concurrent appends may appear on a later
  page but cannot change or duplicate already delivered sequences.

- **R5 — Fixed-cut incremental transcript snapshot.**
  `GET /v1/sessions/{session_id}/transcript` must accept either a prior
  `cursor`, a `page_token`, or neither. A cursor represents the last delivered
  message sequence and lifecycle revision. Starting a drain captures the
  Session's committed high-water marks; every `page_token` must retain that
  exact upper cut even while new writes commit. The response must contain
  `messages`, `invocation_changes`, `has_more`, `resume_cursor`, and an optional
  `next_page_token`. Omitting both inputs starts from zero, so a new consumer can
  reconstruct the complete retained Session before following incrementally.
  Cursors and page tokens must be opaque, versioned, and bound to Account and
  Session; `cursor` and `page_token` are mutually exclusive. The two high-water
  marks must come from one consistent read of the Session counters, never
  separate aggregate queries, and a supplied position cannot exceed the
  committed head. `limit` uses the 100 default and 200 maximum and bounds one
  phase per page: messages first, then lifecycle changes.

- **R6 — Messages precede lifecycle settlement.** A fixed-cut drain must emit
  all message changes through its captured message high-water mark before it
  emits any remaining Invocation lifecycle changes through its captured
  revision high-water mark. Each lifecycle change must expose Invocation ID,
  revision, public status, optional `through_message_sequence`, occurrence
  time, and terminal error/usage/provenance when applicable, without message
  content or lease fields. Consequently a terminal change that references an
  assistant-message watermark cannot be delivered before that message. The
  final `resume_cursor` must equal the captured cut; writes after the cut remain
  available from that cursor on the next drain.

- **R7 — Scope, validation, and failure semantics.** Every resource and cursor
  read must re-authorize the authenticated Account and optional tenant
  constraint. Cross-Account, cross-tenant, missing, and undisclosable resources
  must return `404 not_found`; an explicit `tenant_ref` conflicting with a
  credential constraint must return `403 forbidden` before listing. Malformed
  IDs, limits, booleans, statuses, cursors, page tokens, duplicate query
  parameters, and incompatible cursor/filter combinations must return
  `400 invalid_request`. Empty collections and completed drains return `200`
  with empty arrays rather than `404`.

- **R8 — Postgres remains the recovery authority.** Reads must work after API
  and engine restart using only committed Postgres rows. A migration may add
  indexes for bounded keyset scans but must add no content-bearing event table
  or duplicate transcript column. The transcript snapshot must be a projection
  over `SessionMessage`, `InvocationState`, and terminal Invocation aggregates.
  Cursor envelopes need not be signed because they grant no authority and every
  read re-authorizes scope, but malformed, mismatched, or ahead-of-head
  positions must be rejected. Normal `serve` continues to verify, not mutate,
  the schema.

## Acceptance

- [x] **A1 (R1, R2, R7):** Two Accounts and two tenant partitions contain
  overlapping Agent and Session keys. Get and list calls return only authorized
  Sessions and Invocations; exact filters combine correctly; a conflicting
  explicit tenant returns `403`; undisclosable IDs return `404`; and Session
  active summaries track queued, running, and terminal transitions from
  Postgres.

- [x] **A2 (R1):** A completed generation-only Invocation returns normalized
  usage and provenance on both get and list, while queued, running, failed, and
  rows return the contractually correct null aggregates and typed terminal
  error. No response contains spec, fingerprint, idempotency, lease,
  API-key, provider-request, or provider-response data.

- [x] **A3 (R3):** More than one page of equal-timestamp Sessions and
  Invocations traverses without gaps or duplicates. Changing any bound filter,
  collection, or Account rejects the cursor. Limits of 1, 100, and 200 work;
  zero, negative, over-200, repeated, and malformed values are rejected.

- [x] **A4 (R4):** A multi-turn Session's message pages reproduce every stored
  block and stable ID exactly once in ascending sequence order. A cursor from
  another Session or Account is rejected, and a concurrent append appears only
  after the previously delivered sequence without changing earlier pages.

- [x] **A5 (R5, R6):** With a page size of one, a drain starting before a
  completed turn delivers its user and assistant messages before the completed
  Invocation change. Its final cursor equals the original fixed cut. Writes
  committed after page one are absent from every continuation page and appear
  exactly once when draining again from the final cursor.

- [x] **A6 (R5, R6):** Lifecycle-only changes, including queued, running, and a
  zero-output failure, advance the revision watermark even when no message is
  appended. A poll from the prior cursor returns those changes, and polling
  again from the returned cursor produces no duplicate durable change.

- [x] **A7 (R7, R8):** After admitting and settling work, closing the original
  API and engine processes and opening a new API process yields the same get,
  list, message, and incremental results from durable IDs and cursors. Dropped
  wake signals do not affect recovery, and repository inspection finds no
  second persisted copy of transcript content.

- [x] **A8 (R3, R4, R5, R8):** Query plans use the Session sequence/revision and
  new collection keyset indexes for bounded scans. Standard tests need no
  network or provider credentials; Postgres integration tests prove concurrent
  writes, tenant isolation, fixed cuts, and restart readback; OpenAPI lint and
  the full repository gate pass.

## Sequencing notes

- PRD 008 can add cancellation and budgets without inventing a separate status
  channel; their durable transitions flow through this snapshot.
- PRD 011 must replay these same durable messages and lifecycle changes before
  live deltas and use `resume_cursor` as its reconciliation boundary.
