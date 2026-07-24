# nvoken Runtime: Durable Execution Architecture

**Status:** Proposed
**Date:** 2026-07-20
**Scope:** Product boundary, durable execution model, and launch architecture
**Companions:** `vision.md`, `decisions.md`, `api.md`

---

## Decision summary

Thesis, product law, and the public nouns — Agent, Session, Invocation,
ToolCall — are canonical in `vision.md`. The design is organized around:

```text
invoke(execution_spec, input, optional_session, optional_tenant_key)
  -> durable invocation
```

Architectural consequences:

- Agent configuration is not a stored resource; the host supplies an
  execution specification on every Invocation. Agents exist only as
  lightweight identity anchors, auto-created on first Invocation.
- Project is not a provisioning resource; the host supplies an optional
  `tenant_key` and nvoken partitions internal state automatically.
- Registry, Release, deployment-track, integration, OAuth, skill, toolkit,
  and general secret APIs are not part of Runtime; hosts may register named
  custom tool definitions, opt into agent-memory storage, and supply or select
  narrowly scoped model-provider credentials.
- nvoken hosts no execution environments and executes no host or end-user
  code. Application side effects normally execute host-side. A host-supplied
  remote MCP server is the narrow opt-in exception and is reached only through
  guarded public-only egress. An Environment sandbox concept is deferred to a
  possible future version.
- Identity/admin and internal surfaces are separate from the Runtime API.

State is admitted when durable execution requires it, plus a small set of
opt-in conveniences: agent memory, custom tool definitions, model-provider
credentials, and indexed request metadata. Everything explicitly cut from the
runtime is listed in `api.md` ("Explicitly absent from the Runtime API").

## Goals

1. First agent response with one Runtime API operation and no provisioning.
2. An admitted Invocation survives API deploys, API crashes, connection loss,
   and execution-owner loss. A replacement resumes the same Invocation from
   its last committed checkpoint under a new fence.
3. A Session preserves transcript across Invocations.
4. Host tools return like generation tool calls; a durable result
   submission resumes the parked turn.
5. Callback tools retain signed, durable server-to-server delivery.
6. The host remains source of truth for definitions, versions, integrations,
   non-model business credentials, tenancy, orchestration, and application
   data.
7. The same Runtime API works self-hosted and as nvoken Cloud.
8. The Runtime, identity/admin, and internal API categories are
   independently understandable and generated; the engine dispatch seam
   stays internal.
9. A small fixed authorization model across console, CLI, and API
   credentials; bootstrap admin plus optional external OIDC for humans.

## Product boundary

### nvoken owns

| Area                | Responsibility                                                                                      |
| ------------------- | --------------------------------------------------------------------------------------------------- |
| Agent anchors       | Auto-created identity records grouping Sessions and Invocations                                     |
| Session state       | Canonical transcript, retention, one nonterminal Invocation, host key, tenant partition, indexed metadata |
| Invocation state    | Admission, status, structured output, errors, spec snapshot/digest, usage, provenance               |
| turn execution      | Model calls, tool selection, checkpointing, continuation, cancellation, settlement                  |
| Tool exchange       | Durable ToolCalls across builtin, callback, host, and remote MCP modes                            |
| Recovery            | Leases, fencing, checkpoints, replay cursors, retry policy, stale-engine rejection                  |
| Trust boundary      | Runtime credentials, signed callbacks, model gateway, limits, normalized metering                  |
| Opt-in conveniences | Agent memory, named custom tool definitions, narrowly scoped model-provider credentials            |

### The host application owns

| Area                         | Responsibility                                                  |
| ---------------------------- | --------------------------------------------------------------- |
| Agent specification          | Instructions, model preference, tools, output contract, limits  |
| Versioning and rollout       | Git history, CI, environment selection, canaries, rollback      |
| Tenants and end users        | Authentication, authorization, lifecycle, entitlements          |
| Integrations and credentials | OAuth clients, connections, refresh tokens, and non-model business secrets |
| Orchestration                | Schedules, triggers, workflows, retries between Invocations     |
| Execution environments       | Sandboxes keyed to Sessions, exposed as host-executed tools     |
| Product state and UX         | Database records, files, host-side memory, chat and approval UI |
| Rebilling                    | Mapping normalized usage to the host's plans and invoices       |

Deployment and nvoken Cloud control planes own runtime credentials and
operator identity, provider and database configuration, engine capacity,
egress and callback policy, and commerce. They may share storage and code
with the Runtime without becoming Runtime resources.

## Core vocabulary

### Agent

An Account-wide lightweight identity anchor, auto-created when an Invocation
first names its caller-controlled `agent_key` — there is no agent provisioning
call. The reference is unique within the Account. The anchor groups Sessions
and Invocations across tenant partitions for lookup and observability and
stores neither execution configuration nor tenant data.
Its primary key is UUIDv7 with an `agnt_` prefix.

### Session

A UUIDv7 primary key with a `sesn_` prefix; optional host session key, unique
per (Account, effective tenant partition, Agent, session key); immutable Agent
and tenant partition; indexed host metadata; ordered `SessionMessage`
transcript; at most one queued, running, or waiting Invocation; retention
policy. Invocation creates or resolves Sessions — there is no separate create
workflow. An Account-wide credential may resolve any Session in its Account by
ID when no tenant is asserted. A tenant-constrained credential is confined to
its partition.

### Invocation

One admitted agent turn, identified by UUIDv7 with an `invk_` prefix. Its
public state is exactly `queued`, `running`,
`waiting`, `completed`, `failed`, or `cancelled`; the last three are terminal
and immutable, and the first valid terminal settlement wins. Deadline and
limit exhaustion are typed failures, not additional lifecycle states.
`waiting` means durable host ToolCalls are pending while the Invocation owns
no engine lease or active segment. The Invocation owns caller
context; resolved spec bytes and digest; attempts, leases, and checkpoints;
model requests and normalized usage; ToolCalls and results; output — including
structured output, produced by an internal tool call against a host-provided
schema — and terminal error. Input and conversational output content live only
in the Session transcript. Validated structured output is the sole exception:
terminal settlement may project the accepted reserved ToolCall request onto the
Invocation after proving equality and recording its ToolCall/schema provenance.
The transcript remains canonical for replay. Read-time projections over
canonical rows are permitted: the composed Invocation result read returns the
turn's messages and an `output_text` concatenation without persisting either.
A new turn is a new Invocation.

| State | Terminal | Meaning |
| --- | :---: | --- |
| `queued` | No | Durably admitted and available for a future execution claim. |
| `running` | No | A fenced engine owns the current execution segment. |
| `waiting` | No | Durable host ToolCalls are pending; no execution owner or engine is held. |
| `completed` | Yes | The turn settled successfully. |
| `failed` | Yes | The turn settled unsuccessfully, including deadline, limit, or temporary pre-recovery engine-loss outcomes. |
| `cancelled` | Yes | Cancellation won terminal settlement. |

Every terminal write is conditional on the stored state remaining nonterminal;
the first terminal settlement wins and later rewrites are rejected.

### ToolCall

Stable identity, immutable input, execution mode, deadline,
delivery/attempt history, and exactly one accepted terminal result or
error — durable because disconnects, retries, engine replacement, and
crashes can occur while the model waits.

### Execution specification

Supplied inline or by an immutable reference plus expected content digest.
Includes instructions; model and provider selection, including routing
steps across providers; tool schemas and modes, inline or referencing named
custom tool definitions; output requirements, including a structured-output
schema; output-token, estimated-cost, iteration, total-time, active-time, and
waiting-time limits; host metadata.
nvoken resolves, validates, and digests the spec, may retain the snapshot
as provenance, and caches by digest so hosts avoid resending large specs.
There is no registry, publish, pin, or mutable definition API.

## Tool execution model

Every tool declares one mode:

- **`builtin`** — a deliberately small trusted runtime capability executed
  by the turn engine in process. Broad integrations do not become builtins.
- **`callback`** — a signed request to a host endpoint with durable result
  consumption. Delivery carries stable Invocation, ToolCall, tenant, Agent,
  and idempotency identities; its versioned context reserves delegated actor
  identity until admission owns such a claim. URLs satisfy public-only
  dial-time egress policy. The first implementation uses one installation HMAC
  secret with explicit key ID/version; JWKS signing remains a later scheme.
- **`host`** — the ToolCall is persisted in the canonical transcript before
  projection through Invocation and Session reads. The Invocation parks in
  `waiting` until the host submits a bounded batch through
  `POST /v1/invocations/{invocation_id}/tool-results`. The first result per
  ToolCall ID wins; equal duplicates return the recorded outcome, and the
  transaction that closes the last call queues the same Invocation and its
  successor external dispatch when configured.
- **`mcp`** — the host supplies a public streamable-HTTP server descriptor and
  optional one-Invocation headers. Concurrent bounded discovery commits one
  ordered projected catalog before the first provider call. Each selected call
  commits its ToolCall, checkpoint, and attempt before guarded egress; result
  acceptance is fenced. After owner loss, only an explicitly read-only or
  idempotent, non-destructive call may run once more. Other uncertain calls
  settle with a canonical unknown-outcome result and no second dispatch.

### Two roles, two runtime modes

| Role                      | Responsibility                                                                   | Deploy cadence                        |
| ------------------------- | -------------------------------------------------------------------------------- | ------------------------------------- |
| `nvokend` (control plane) | API, admission, Session projections, ToolCall delivery, signing, model gateway, reads | Continuous                            |
| turn engine               | Claims admitted Invocations and executes the turn end to end                     | Rare — only when harness code changes |

The engine deploys by drain when its hosting platform keeps the execution
segment alive: stop claiming, finish in-flight turns, and exit while the new
version claims fresh work. A turn segment executes entirely on one harness
version. Self-contained Cloud Run remains operationally less predictable:
background turns are not request-bound, so revision shutdown or scale-in can
interrupt work that exceeds the platform termination window. The lease/reaper
then requeues the Invocation for checkpoint-based continuation.

Two runtime modes implement the internal dispatch seam (claim, lease,
heartbeat, checkpoint, settle — a version-locked Go interface, never a
published protocol). The public admission handler owns no model execution:

- **Self-contained** — the engine runs in the same process as the nvoken
  API; suited for development and small self-hosted installations. One
  binary plus Postgres (and optionally Redis) is a complete installation.
- **Split Cloud Run** — engine attempts run outside the API process and Cloud
  Tasks delivers an authenticated request to a private Cloud Run service. The
  delivery identifies exact durable work but grants no ownership: the executor
  must acquire the Postgres claim and fence before model execution. A task may
  host one bounded execution segment; its delivery identity never appears in
  the public Runtime contract. Redis may fan ephemeral live previews to API
  replicas; Postgres remains the only authority.

Both modes share identical semantics; moving between them is configuration
only.

The two initial operational shapes and the evidence required before either may
be called production ready are defined in the
[production-readiness profiles](../testing/production-readiness-profiles.md).
That matrix adds readiness constraints without changing these runtime modes or
treating a delivery service as execution authority.

Live output is a projection, not an execution or storage boundary. A Session
SSE handler subscribes to bounded fan-out before draining the fixed-cut
Postgres transcript, then polls that read model as its correctness fallback.
Durable `transcript.update`, `invocation.update`, and `invocation.result` frames
carry opaque cursors. Provider-normalized `output_text.delta` and
`thinking.delta` frames are best-effort, id-less previews; buffer overflow or
Redis loss asks clients to discard provisional output and reconcile to
canonical `SessionMessage` plus Invocation lifecycle state. An Invocation
stream follows one turn; a Session stream uses the same event vocabulary
across turns. Self-contained mode may use an in-process adapter. Split execution
uses private Redis Pub/Sub between the executor and API replicas; the paved
path authenticates it and verifies its TLS server certificate. Redis never
grants a claim, advances a cursor, or determines terminal state.

An executing turn is an I/O-bound state machine: one goroutine per active
Invocation, thousands per process; a parked Invocation — waiting on a tool
result — is durable rows and no goroutine. Memory, not CPU, is the scaling
dimension. There is no isolated vehicle anywhere in the system, because
nvoken executes no untrusted code.

### Leases and fencing

Bounded lease plus fencing token per claim; heartbeats extend only the current
lease; checkpoint, ToolCall, usage, and terminal commits verify the fence; a
stale instance may finish local computation but cannot commit. The reaper
accrues the abandoned segment only through its recorded lease/deadline boundary,
clears ownership, and makes the same Invocation queued. Total-time,
active-time, cancellation, and already-terminal outcomes still win.

Cancellation uses the same first-terminal transaction and Session-before-
Invocation lock order as settlement. A committed cancellation may notify other
instances through PostgreSQL LISTEN/NOTIFY, but notification grants no
authority; a missed wake is recovered when renewal or settlement loses its
fence. Each claim also persists one active-execution segment and a deadline
chosen from total-time remainder, active-time remainder, and the
installation segment ceiling. Model work stops before that deadline to reserve
settlement time. Segment accrual and terminal state commit atomically. Queue
time consumes total time only. A healthy owner that reaches its segment cutoff
settles a typed deadline failure; if ownership itself expires before settlement,
the replacement resumes from the durable prefix instead.

### Checkpoints and resume

The checkpoint evidence spine is durable now. Every accepted model iteration
appends its normalized assistant message and usage/provenance receipt together;
prepared ToolCalls and their stable IDs commit in that same cut. A ToolCall
outcome appends the canonical tool-role result and its next checkpoint in one
fenced transaction. The Invocation stores only monotonic iteration and
checkpoint counters, while append-only checkpoints reference the transcript
watermark and corresponding receipt or ToolCall. They never contain transcript
content, a provider envelope, or restorable process state.

Canonical transcript reads are lossless, including checkpoints retained before
a later terminal failure. Provider-context reconstruction is intentionally
narrower: assistant and tool messages owned by a failed or cancelled Invocation
are excluded, while its user input remains eligible. This preserves paid-call
evidence without allowing rejected output to steer a later Invocation.

Tool request and result payloads remain exclusively in `SessionMessage`.
ToolCall rows retain immutable scope, provider correlation, mode, request hash,
deadline, attempt count, status, and message references. Model usage receipts
are immutable per Invocation iteration. If cancellation, deadline expiry, or
terminal settlement wins while calls are open, that transaction appends a
bounded synthetic error result, closes the calls and running attempts, advances
checkpoints, and publishes a lifecycle watermark covering the result. Lease
recovery deliberately leaves open calls intact.

Host ToolCall result submission uses Session, Invocation, then ToolCall lock
order. A partial batch appends one tool-role message and one checkpoint per new
result while leaving the Invocation waiting. The final batch also appends a
queued lifecycle revision through that message. Durable result messages retain
host submission order; before the next provider call the engine coalesces the
batch and projects blocks in the original model ToolCall order. Wall-clock time
continues while parked, active-execution time does not. Each terminal ToolCall
also records whether its result came from a host, builtin, or nvoken system
settlement so late host submissions never compare against cancellation or
deadline evidence.

After owner loss or a host-tool resume, a replacement validates the
append-only transcript, checkpoints, receipts, and ToolCalls before new work. A
committed final model checkpoint settles without another provider call; a
pending or abandoned
builtin or safely retryable MCP work continues under the same ToolCall ID and
a new attempt. An uncertain possibly mutating MCP call settles as an unknown
outcome without egress. Inconsistent evidence fails with the bounded public
`internal` error. This is crash recovery,
not a public retry API, arbitrary process snapshots, external-effect safety, or
intentional checkpoint-and-chain at the segment ceiling.

## Identity and access

The pillars:

- `Account` is the top-level customer and hard security boundary. It is
  inferred from the authenticated subject, never a request parameter.
  Self-hosting bootstraps one default Account; nvoken Cloud hosts many.
- Humans authenticate through a bootstrap admin credential or an optionally
  configured external OIDC provider (Clerk may serve nvoken Cloud).
  Operators are keyed by `(issuer, subject)` with a local membership and
  one fixed role — Owner, Operator, or Viewer — so removal or demotion
  takes effect even while a provider token remains valid. Owner is
  human-only; recovery-sensitive operations require an interactive session.
  Memberships are provisioned by a declarative operator allowlist in
  installation configuration — matched by issuer and email claim, with the
  subject bound at first login — or by the nvoken Cloud control plane;
  there is no portable members CRUD. Browser OIDC login, callback, and
  logout are installation plumbing outside the generated identity/admin
  contract.
- API credentials are one resource with two kinds. A machine credential
  carries one fixed profile — Operator, Viewer, or Runtime — plus
  constraints that only narrow (Account, `tenant_key`, Session, operation
  subset, expiry). A user credential is issued through device authorization
  and resolves its effective role at authentication time — the owner's
  current membership role intersected with an optional Operator/Viewer cap.
  Raw secrets are returned once; revoked records are retained for
  attribution.
- The host backend uses a Runtime-profile credential, Account-wide or
  pinned to one `tenant_key`. `tenant_key` partitions and attributes within
  the Account and becomes an authorization boundary only when a credential
  is constrained to it. A delegated actor reference is attribution only,
  never an authorization input.
- Embedded end-users do not authenticate to nvoken; the host calls nvoken
  on their behalf. Direct end-user access with a new credential form is
  deferred.
- The CLI uses the OAuth 2.0 Device Authorization Grant, brokered by nvoken
  even when the provider has no device flow. Approval issues a user-kind
  API credential representing the approving human, so demotion, removal,
  expiry, or revocation takes effect immediately. CI uses machine
  credentials.

## Data and retention

Authoritative data: agent anchors; Sessions and `SessionMessage` transcript
items; indexed request metadata; Invocations, append-only lifecycle state
revisions, and spec snapshots/digests; ToolCalls, attempts, immutable normalized
model-usage receipts, and append-only checkpoints; change view cursors; leases;
usage and provenance; opt-in agent memory records; named custom tool
definitions; reusable model-provider credential metadata and encrypted
versions; and per-Invocation provider-credential bindings. Lifecycle revisions
and change views may reference transcript sequence numbers but never store
another copy of message or ToolCall-result content, except the equality-proven
terminal structured-output projection described under Invocation. That
single-representation rule constrains storage only; read-time projections such
as the composed result read derive from canonical rows and store nothing. Tool
lifecycle records have no independent
pruning path and remain with the owning Invocation/Session trace. No host
tables, business records, OAuth connections, non-model business credentials,
release catalogs, or durable user files.
Spec snapshots live no longer than the Invocation/Session trace.

The initial production profiles retain this authoritative data indefinitely.
They expose no automatic compaction or deletion path until a later ordered
contract defines Session and tenant deletion, cursor behavior, backup expiry,
and any archive/export boundary. Only terminal `execution_dispatches` and
`callback_deliveries` are pruned as finite transport diagnostics; pruning them
does not remove their authoritative owners or evidence. Redis previews and Cloud
Tasks requests are ephemeral delivery mechanisms, not retention stores. The
operator policy, defaults, and storage-growth queries are documented in the
[data-retention guide](../guides/data-retention.md).

Runtime is not a general credential vault: hosts use host tools, callback
tools, remote MCP, or a credential-broker tool for integrations and business credentials,
and custom-tool registration stores tool contracts, never secrets. The narrow
exception is model-provider access. Each provider used by an Invocation binds
exactly one source: an Invocation-supplied ephemeral credential, reusable
Account BYOK, reusable tenant BYOK, or a platform-funded credential. Existing
self-hosted installation BYOK remains deployment configuration. Durable
bindings make the selected source available to any fenced execution owner;
there is no silent fallback to another source.
Remote MCP headers form a separate per-Invocation encrypted binding. They are
excluded from fingerprints, specs, logs, reads, streams, and errors, and are
destroyed in the terminal transaction or by the expiry sweeper.

## Heritage

nvoken is built by porting proven internals from its predecessor runtime
(Mobius) rather than starting from scratch: Dive integration, transcripts,
callback signing, durable work claims, usage accounting, and fencing all
have working ancestors there. The predecessor's resource model does not
carry over; the mapping below records how its concepts land in nvoken.

| Predecessor concept     | nvoken equivalent                                                    |
| ----------------------- | -------------------------------------------------------------------- |
| Project                 | Host `tenant_key`                                                    |
| Agent config            | Serialize resolved behavior into an execution specification          |
| Session + turn          | Session + Invocation                                                 |
| Custom action           | Callback, host, or remote MCP tool                                  |
| Integration             | Host-owned implementation, remote MCP server, or credential broker  |
| Worker action/model job | Engine work claim or host-owned callback tool                        |
| Environment             | Host-owned sandbox exposed as a host tool                            |
| Loop                    | Not recreated; hosts bring their own scheduler                       |
| Usage events            | Normalized usage on Invocations plus identity/admin usage monitoring |

## Rollout

- **Phase 0 — contract reset:** freeze noun, ID, spec, transcript, lifecycle,
  and identity vocabulary; separate Runtime, identity/admin, and internal
  specs.
- **Phase 1 — one-call vertical slice:** `POST /v1/invocations` with inline
  specs and implicit agent/Session creation; drain-deployed engine; status,
  canonical transcript, cancellation, usage; SDK `invoke()` golden path.
- **Phase 2 — durable tool exchange:** ToolCall normalization; callback and
  host modes over the canonical transcript and narrow result commands;
  idempotency, deadline, and
  cancellation hardening.
- **Phase 3 — engine durability:** hardened dispatch seam;
  transcript-based resume for crashes, parking, and checkpoint-and-chain;
  prove accepted results and usage never re-apply across recovery.
- **Phase 4 — distribution:** self-hosted BYOK with no runtime billing
  dependency; bootstrap admin, operator allowlist, and optional OIDC; CLI
  device authorization issuing user credentials; docs and SDKs split by
  audience.

## Open questions

1. Which spec reference schemes ship at launch: signed HTTPS, object
   storage, OCI artifact, or a subset?
2. Which safety limits are installation configuration versus credential
   claims?
3. What credential form should deferred direct end-user access take, and
   when would host-issued JWT federation justify its complexity?
4. How many metadata items are indexed per request, and what query surface
   do they get?
5. What are the agent-memory data model and scoping (per agent, per
   `tenant_key`, per Session)?
6. How long are idempotency records retained?
7. Do portable operators need multi-Account selection, or is single-Account
   operation sufficient outside nvoken Cloud?
8. Which minimal builtins belong in the portable runtime?
9. Which operator views does the console need first: session viewer,
    invocation trace, usage, health?
10. How far does observability extend beyond the trace: an Account-wide
    activity feed, OpenTelemetry projection?
