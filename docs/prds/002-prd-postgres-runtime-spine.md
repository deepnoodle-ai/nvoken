# Build the Postgres Runtime Spine

**Status:** Complete

**Sequence:** 002

**Depends on:** `001-prd-runtime-record-and-lifecycle-contract.md`

## ELI5

Give nvoken a small, trustworthy database foundation before it accepts real
work. The database itself must prevent duplicate active turns and preserve one
canonical transcript, while a deliberate migration command makes Cloud Run
rollouts safe. This PRD builds storage and its proof, not HTTP admission or
model execution.

## Why

PRD 001 fixed the identities, transaction boundary, and lifecycle that durable
admission will depend on. Encoding those rules now makes the next PRD a service
workflow over enforced invariants instead of a race-prone collection of CRUD
calls.

Mobius Cloud is useful precedent and a warning. Its turn and Session schemas use
checks and partial unique indexes for lifecycle correctness, and its transaction
manager keeps multi-repository work atomic. It also combines generated Ent
schema, automatic migration, and many hand-authored migration steps; concurrent
Cloud Run startup later required a dedicated Postgres advisory lock. nvoken has
a much smaller runtime schema, so it will use pgx, sqlc-generated query code,
and explicit versioned SQL applied by golang-migrate, with migration as a
serialized operation rather than ordinary replica startup.

## Outcome

nvoken can create, migrate, and access the minimum Postgres records required by
durable Invocation admission. Database constraints enforce the accepted
identity, transcript, lifecycle, and single-flight rules, and multiple migration
runners converge safely on one schema.

## Scope

**In:** pgx; sqlc-generated Postgres query code; golang-migrate; prefixed
UUIDv7 IDs; clock and ID ports; versioned forward migrations; a serialized
migration command; transaction and repository ports; Accounts, internal tenant
partitions, Agents, Sessions, execution-spec snapshots, SessionMessages,
Invocations, and InvocationStates; schema and adapter tests; retention and
deletion posture.

**Out:** Public HTTP handlers; authentication; the admission service; model or
tool execution; execution claims, leases, dispatch, or reaping; down migrations;
runtime deletion or compaction; Cloud Run and Cloud SQL provisioning.

## Requirements

- **R1 — Explicit Postgres foundation.** The runtime store must use pgx against
  supported Postgres, with sqlc generating adapter-internal query code and
  golang-migrate applying schema changes. It must not use Ent or automatic
  schema diffing, and generated database types must not cross the adapter
  boundary. Ordered, forward-only SQL migrations must be embedded in the
  service artifact and recorded by version and dirty state. A committed
  migration version is immutable; a dirty database or version newer than the
  binary must fail closed.

- **R2 — Stable time-based identities.** Application code must generate
  RFC 9562 UUIDv7 values using an injectable clock and cryptographic randomness.
  Stored IDs must use the fixed prefixes `acct_`, `tprt_`, `agnt_`, `sesn_`,
  `spec_`, `smsg_`, `invk_`, and `ivst_` for the eight record types in scope.
  The database must reject malformed prefixed IDs. Ordering within a Session
  must use explicit message sequence and state revision fields, never ID order.

- **R3 — Tenant and Session identity invariants.** An Account must have exactly
  one default tenant partition and may have at most one partition per nonempty
  `tenant_ref`. An Agent must be unique by `(account_id, agent_ref)`. A Session
  must permanently reference one Account, tenant partition, and Agent, and a
  non-null `session_key` must be unique within that tuple. Composite foreign
  keys or equivalent constraints must prevent cross-Account, cross-partition,
  or cross-Agent record combinations even when IDs are valid independently.

- **R4 — One canonical transcript and lifecycle history.** SessionMessages must
  be append-oriented, uniquely sequenced within a Session, contain a nonempty
  JSON array of content blocks, and be the only records in this schema that
  store transcript content. InvocationStates must be append-only lifecycle
  revisions, uniquely and monotonically numbered within their Session, with an
  optional message-sequence watermark but no message payload. Sessions must
  hold the next message sequence and next lifecycle revision needed for atomic
  allocation. Invocation and snapshot records must not duplicate transcript
  content.

- **R5 — Invocation correctness in the database.** Invocation status must be
  exactly `queued`, `running`, `waiting`, `completed`, `failed`, or `cancelled`.
  The database must allow at most one nonterminal Invocation per Session. An
  idempotency key must be unique within `(Account, tenant partition, Agent)` and
  pair with a 32-byte SHA-256 material-request fingerprint. Each Invocation
  must reference one immutable execution-spec snapshot, and its initial queued
  row plus initial state revision must be representable in the same transaction
  as the caller SessionMessage.

- **R6 — Atomic, adapter-neutral access.** Consumer-oriented repository ports
  must expose the minimum reads and writes needed by the next admission slice,
  using domain types rather than pgx or generated sqlc types. A transaction
  manager must bind sqlc queries to the same database transaction for every
  participating repository, commit only after the callback succeeds, and roll
  back completely on an error or panic. Nested use must join the existing
  transaction rather than partially commit.

- **R7 — Deliberate, serialized migration operation.** `nvokend migrate` must
  be an explicit bounded operation suitable for a Cloud Run Job or release
  step. golang-migrate's pgx/v5 driver must hold its session-scoped Postgres
  advisory lock on one dedicated connection while inspecting and applying
  migrations, so concurrent runners serialize and a dead runner releases
  ownership with its connection. Normal `nvokend` startup must never apply
  migrations. Logs must identify migration start, completion, and each applied
  version without exposing credentials.

- **R8 — Preserve history by default.** Foreign keys must not cascade-delete
  Sessions, transcript messages, Invocations, state history, or spec snapshots.
  This slice must expose no deletion, pruning, or idempotency cleanup operation;
  retained Invocations therefore retain their deduplication guarantee. A future
  retention PRD may add explicit, ordered compaction or deletion without
  weakening this launch default.

## Acceptance

- [x] **A1 (R1, R7):** Against an empty supported Postgres database,
  `nvokend migrate` creates the complete schema and records its version and
  clean state; a second run performs no DDL and exits successfully. Ordinary
  server startup against an empty database does not create tables.
- [x] **A2 (R1):** Presenting a dirty migration state or database version newer
  than the binary causes migration to fail before later DDL is applied, with a
  diagnostic naming the version.
- [x] **A3 (R2):** Deterministic tests using a fixed clock verify every prefix,
  UUID version, variant, and embedded timestamp. Database tests reject malformed
  IDs, and same-millisecond records remain correctly ordered by Session sequence
  or revision rather than ID text.
- [x] **A4 (R3):** Constraint tests prove one default partition per Account,
  tenant-ref isolation, Account-wide Agent uniqueness, Session-key uniqueness
  per partition and Agent, and rejection of every cross-boundary composite
  reference.
- [x] **A5 (R4, R5):** One transaction persists a spec snapshot, caller message,
  queued Invocation, and initial state revision. Readback reconstructs content
  only from the SessionMessage and lifecycle only from InvocationState; empty
  message content, duplicate message sequence, duplicate state revision, or an
  unknown status is rejected.
- [x] **A6 (R5):** Concurrent inserts for two nonterminal Invocations in one
  Session produce exactly one durable winner. Reusing an idempotency key within
  its scope or supplying a non-32-byte fingerprint is rejected, while the same
  key in another tenant partition is accepted and a new Invocation is accepted
  after the first becomes terminal.
- [x] **A7 (R6):** Repository integration tests show all writes become visible
  after commit, no writes survive a returned error or panic, and nested
  transaction use has one commit/rollback outcome. Domain and service packages
  import no pgx, sqlc, or database adapter packages; regenerating sqlc output
  from the checked-in schema and queries produces no diff.
- [x] **A8 (R7):** Two migration processes started together against one empty
  database both exit successfully, logs show only one execution of each
  version, and the version table contains one clean current-state row.
  Terminating the lock holder releases the advisory lock so a later runner can
  proceed within its configured timeout.
- [x] **A9 (R8):** Direct parent deletion is rejected while dependent runtime
  history exists; repository and CLI surfaces contain no runtime-history delete
  or prune operation, and the retention posture is documented with the schema.

## Deferred decisions

- PRD 003 owns admission ordering, normalization, fingerprint construction,
  locking, and conflict-to-public-error mapping. This schema only makes the
  accepted outcome atomic and enforceable.
- The serve path currently opens no database connection, so its no-migration
  behavior is structural and was also exercised with an empty schema. When PRD
  003 first wires the runtime pool into server bootstrap, it must add a
  regression test that the serve path remains migration-free.
- Claims, lease attempts, fencing columns, and dispatch intent arrive with the
  execution-ownership PRDs; this slice does not claim work or make queued work
  externally deliverable.
- Down migrations and destructive retention are intentionally absent until a
  concrete operational policy can define safe ordering and recovery.
