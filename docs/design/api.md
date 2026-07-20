# nvoken Runtime: API Surface

**Status:** Proposed contract
**Date:** 2026-07-20
**Level:** Endpoint and purpose. The frozen background launch schemas,
operation IDs, examples, and errors are in
[`openapi/runtime.yaml`](../../openapi/runtime.yaml).
**Companions:** `vision.md`, `architecture.md`

---

## Scope

Thesis, law, and noun definitions are canonical in `vision.md`; runtime
semantics in `architecture.md`. The API surface has three categories:
the **Runtime API** with its Session transcript projections, the
**identity/admin API**, and **internal** surfaces (engine dispatch, deployment operation,
nvoken Cloud control plane) that are never published. This document
catalogs the first two; the execution plane is internal (section 7). A host
only needs the Runtime contract to invoke an agent.

Not enumerated here: internal surfaces; nvoken Cloud commerce; invitations,
custom roles, membership CRUD, or end-user directories; private provider
adapters. Those are separately versioned, not prerequisites for integrating
or self-hosting.

## Contract conventions

Authentication and tenancy:

- The host authenticates with a Runtime-profile machine credential; Account
  is inferred from the credential, never a request parameter. Human
  operator authentication is section 8.
- Embedded end-users are delegated actor references, not nvoken users; the
  host backend calls nvoken on their behalf (`vision.md` section 7).
- Each request resolves one fixed access profile plus optional narrowing
  constraints; credential types are not interchangeable.
- Agent references are Account-wide. An Invocation may carry a
  host-controlled `tenant_ref` (`vision.md` section 7), but it creates no
  public Tenant or Project resource.
- A tenant-constrained credential fixes the effective partition. Supplying a
  different explicit `tenant_ref` returns `403 forbidden` before resource
  lookup. For Session-key resolution or creation, an Account-wide credential
  uses an explicit `tenant_ref` or the default partition. For Session-ID
  access, an Account-wide credential may omit `tenant_ref` and use the
  Session's stored partition.

Idempotency and concurrency:

- Invocation creation requires a body `idempotency_key`, scoped to Account,
  effective tenant partition, and Agent reference. An equal replay returns the
  original records before the concurrency check; a materially changed replay
  returns `409 idempotency_conflict`.
- At most one Invocation in `queued`, `running`, or `waiting` may exist for a
  Session. A distinct request while that slot is occupied returns
  `409 session_invocation_active` before appending input.
- A host session key resolves or creates the Session within `(Account,
  effective tenant partition, Agent, session_key)`; ID format is canonical in
  `vision.md` section 5.
- Agent and Session resolution or creation, the inline spec snapshot, one
  caller-input message, and one queued Invocation commit in one Postgres
  transaction before acknowledgement or execution claim.

Admission accepts at most 1,048,576 encoded JSON bytes and 64 text blocks.
`agent_ref`, `tenant_ref`, `session_key`, `idempotency_key`, model provider, and
model name are each limited to 255 Unicode characters. Fingerprint v1 is the
SHA-256 of compact UTF-8 JSON in fixed member order: `version`,
`session_selector` (`kind` then `value`), `spec` (`instructions`, then `model`
with `provider` then `name`), and `input` (`content`, with each block encoded as
`type` then `text`). The selector kind is `none`, `id`, or `key`; `none` uses an
empty value. JSON strings escape quotation mark, reverse solidus, and control
characters only, using the usual short escapes and lowercase `\\u%04x` for
remaining controls; all other Unicode is emitted directly. Source-object order
therefore does not matter, while array order and exact string values do. The
language-neutral canonical bytes and digests in
[`admission-fingerprint-v1.json`](admission-fingerprint-v1.json) are the
compatibility fixtures.

Streaming and recovery:

- Background JSON admission and authoritative JSON reads are the frozen launch
  contract. SSE and change-feed endpoints are added only after the durable read
  model is specified.
- Ordered `SessionMessage` rows are the sole durable representation of caller,
  agent, and future ToolCall content. Append-only Invocation state revisions
  record lifecycle without copying message payloads.
- A later incremental view composes message sequence and Invocation revision;
  a stream projects that view plus optional ephemeral deltas. Neither transport
  is authoritative or persists a second content representation.

## 1. Service discovery

| Method | Endpoint                 | Purpose                                                                                            |
| ------ | ------------------------ | -------------------------------------------------------------------------------------------------- |
| `GET`  | `/healthz`               | Process liveness probe.                                                                            |
| `GET`  | `/readyz`                | Verify this instance can serve authoritative requests.                                             |
| `GET`  | `/metrics`               | Private deployment metrics scrape.                                                                 |
| `GET`  | `/v1/capabilities`       | Supported protocol versions, model providers, tool modes, streaming modes, and installed adapters. |
| `GET`  | `/.well-known/jwks.json` | Active verification keys for runtime-signed envelopes.                                             |

Capabilities describe what this installation can execute — no plan, agent,
or integration catalog. `/healthz`, `/readyz`, and `/metrics` belong to
deployment operation and are listed only because every installation exposes
them.

## 2. Invocations

The frozen background launch slice is deliberately smaller than the eventual
Runtime surface:

| Method | Endpoint                          | Purpose                                                                                                             |
| ------ | --------------------------------- | ------------------------------------------------------------------------------------------------------------------- |
| `POST` | `/v1/invocations`                 | Atomically admit one background Invocation and return its stable Agent, Session, and Invocation identity.          |
| `GET`  | `/v1/invocations/{invocation_id}` | Read authoritative identity and lifecycle state, including any typed post-admission terminal failure.              |

The launch create request carries `agent_ref`, body `idempotency_key`, one or
more text input blocks, an optional `tenant_ref`, at most one of `session_id`
and `session_key`, and an inline spec containing instructions plus model and
provider selection. Unknown and deferred fields — including spec references,
tools, structured output, budgets, retention, indexed metadata, delegated actor,
and delivery mode — are rejected rather than ignored. The admitted spec is an
immutable Invocation snapshot, never mutable Agent configuration.

The background response is always `202 Accepted` after commit for both new
admission and equal replay. It returns `agent_id`, `session_id`,
`invocation_id`, current status, and `deduplicated`; a terminal replay also
returns `202`. The request handler never owns model execution. A 5xx, timeout,
disconnect, or otherwise missing acknowledgement is recovered by retrying the
same request and key.

Public states are exactly `queued`, `running`, `waiting`, `completed`, `failed`,
and `cancelled`. The last three are terminal and immutable; deadline or budget
exhaustion is a typed `failed` outcome. `waiting` is reserved for later durable
ToolCalls.

There is no public retry or resume endpoint. Terminal Invocations stay
terminal; the host creates a new Invocation for another turn.

Invocation history, cancellation, usage, output, provenance, tool summaries,
and streaming are later extensions. They must preserve this admission identity,
lifecycle, idempotency, and background acknowledgement contract.

## 3. Sessions

| Method | Endpoint                    | Purpose                                                                                             |
| ------ | --------------------------- | --------------------------------------------------------------------------------------------------- |
| `GET`  | `/v1/sessions/{session_id}` | Read immutable Session identity, partition and host key, plus the current nonterminal Invocation.  |

There is no public create: Invocation creates Sessions. A Session key is unique
within Account, effective tenant partition, Agent, and key, so the same key in
two tenant partitions resolves two Sessions. An Account-wide credential may
read either by ID; a tenant-constrained credential can read only its own
partition. Agent and partition are immutable.

Session lists, transcript pagination, incremental changes, streaming, metadata,
retention, and destructive operations are later contracts. The transcript
surface will read canonical `SessionMessage` rows; the incremental surface will
compose their sequence with Invocation revisions. There is no generic public
Session-event append endpoint. Client ToolCall results and future steering use
narrow commands defined with those features.

## 4. ToolCalls

`ToolCall` is the universal durable trace resource for the three modes
(`builtin`, `callback`, `client` — semantics in `architecture.md`). Tool
definitions travel in the execution specification or reference named custom
tool definitions (section 5); there is no integration connection or OAuth
resource.

ToolCalls have no endpoints in this contract. When the client-tool slice ships,
the canonical transcript carries each request and result and the Session and
Invocation reads report pending calls. A client ToolCall parks the Invocation
in `waiting` with no engine capacity; the host submits the result through a
narrow, batchable, idempotent command chosen in that slice. The first result per
ToolCall ID wins. No connection stays open for correctness.

Callback wire rules: definitions supplied per Invocation or by custom-tool
reference; URLs must satisfy the credential's deployment-configured egress
policy; requests are runtime-signed with stable Invocation, ToolCall,
tenant, delegated actor, and idempotency identities; hosts verify with the
runtime JWKS or a signing secret shared between nvoken and the host. No
per-tool signing-key CRUD.

The runtime stores no host integrations or business credentials; hosts use
client tools, callback tools, or a credential broker.

## 5. Custom tools

| Method   | Endpoint              | Purpose                                                            |
| -------- | --------------------- | ------------------------------------------------------------------ |
| `GET`    | `/v1/tools`           | List named custom tool definitions.                                |
| `POST`   | `/v1/tools`           | Register a named tool definition: schema, mode, callback endpoint. |
| `GET`    | `/v1/tools/{tool_id}` | One definition and its metadata.                                   |
| `PUT`    | `/v1/tools/{tool_id}` | Replace a definition.                                              |
| `DELETE` | `/v1/tools/{tool_id}` | Remove a definition.                                               |

Registration is a convenience: execution specs reference registered tools
by name instead of resending definitions on every Invocation. The registry
stores tool contracts, never business credentials.

## 6. Agent memory (optional)

| Method   | Endpoint                 | Purpose                       |
| -------- | ------------------------ | ----------------------------- |
| `GET`    | `/v1/memory`             | List memory records in scope. |
| `POST`   | `/v1/memory`             | Create a memory record.       |
| `GET`    | `/v1/memory/{memory_id}` | One memory record.            |
| `PUT`    | `/v1/memory/{memory_id}` | Replace a memory record.      |
| `DELETE` | `/v1/memory/{memory_id}` | Delete a memory record.       |

Agent-memory storage is opt-in. A host may instead keep memory entirely on
its side and inject it through the execution spec — either mode is
supported. The data model and scoping are open questions
(`architecture.md`).

## 7. Execution plane (internal)

There is no public runner or worker protocol. The turn engine's dispatch
seam is an internal contract version-locked to the release, with two
runtime modes — in-process with `nvokend`, or dispatched via Google Cloud
Tasks in nvoken Cloud — and the model gateway is internal
(`architecture.md`, "Durable execution").

## 8. Identity/admin API

A separately generated portable contract for the optional console, the CLI,
and account administrators: the current Account, API credentials, usage
monitoring, observability. It adds no steps to the Runtime golden path and
is not merged into the Runtime SDK.

### Operator browser authentication

Browser OIDC login, callback, and logout are installation plumbing, not
contract: `nvokend` serves Authorization Code with PKCE against the
configured operator OIDC provider, binds `(issuer, subject)`, and manages
the browser session, but these endpoints are excluded from the generated
identity/admin OpenAPI — no SDK calls them, and the callback URL registered
with the identity provider must stay stable across API versions. External
OIDC is optional; self-hosted installations without it use the bootstrap
admin credential. OIDC configuration belongs to installation configuration
or the internal deployment surface. No MFA, recovery, or identity-provider
CRUD.

### Current Account and membership

| Method | Endpoint      | Purpose                                                                                                                                                 |
| ------ | ------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `GET`  | `/v1/account` | Whoami: Account identity and portable settings, plus the caller's resolved subject, role or profile, constraints, authentication method, and assurance. |

There is no portable members CRUD. Operators are keyed by
`(issuer, subject)` with a local membership and one fixed role
(`architecture.md`, "Identity and access"); memberships are provisioned
by a declarative operator allowlist in installation configuration or by the
nvoken Cloud control plane. Allowlist entries match issuer and email claim
and bind the subject at first login — never by exact OIDC subject, which is
unknowable before a user's first login. Invitations and directory sync are
Cloud or deployment extensions. Account creation is not a portable
pre-invoke workflow: self-hosting bootstraps one default Account; Cloud
creates Accounts through its own control plane.

### API credentials

| Method   | Endpoint                                         | Purpose                                                                                                                            |
| -------- | ------------------------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------- |
| `GET`    | `/v1/account/credentials`                        | List credentials: kind, owner, profile or cap, constraints, status, prefix, expiry, last use; no raw secrets; filterable by owner. |
| `POST`   | `/v1/account/credentials`                        | Create a machine credential with an Operator, Viewer, or Runtime profile; opaque secret returned once.                             |
| `GET`    | `/v1/account/credentials/{credential_id}`        | Kind, owner, profile, constraints, rotation lineage, status, audit metadata.                                                       |
| `POST`   | `/v1/account/credentials/{credential_id}/rotate` | Replacement secret with controlled overlap, linked for audit.                                                                      |
| `DELETE` | `/v1/account/credentials/{credential_id}`        | Revoke while retaining historical actor identity; operators may always revoke credentials they own.                                |

API credentials are one resource with two kinds. A `machine` credential
carries one fixed profile — Operator, Viewer, or Runtime — plus narrowing
constraints; host backends and CI use these. A `user` credential is issued
through device authorization (below), belongs to the approving operator,
and resolves its effective role at authentication time — the owner's
current membership role intersected with an optional Operator/Viewer cap —
so demotion, removal, expiry, or revocation takes effect immediately.
Owner is never assignable to any API credential. Provider keys, business
secrets, callback-signing keys, and browser sessions are not API
credentials. Direct end-user credentials are deferred (`vision.md`
section 7).

### CLI device authorization

| Method | Endpoint                  | Purpose                                                                                                                 |
| ------ | ------------------------- | ----------------------------------------------------------------------------------------------------------------------- |
| `POST` | `/v1/auth/device/code`    | Begin an RFC 8628 device grant: device code, user code, verification URL, interval, expiry.                             |
| `POST` | `/v1/auth/device/token`   | Poll the grant; after approval, return the one-time opaque secret of a new user credential.                             |
| `POST` | `/v1/auth/device/confirm` | Browser-authenticated operator approves a user code for one Account, with optional Operator/Viewer cap and constraints. |

Implemented by nvoken even when the provider has no device flow (semantics
in `architecture.md`). Approval issues a user-kind API credential; the
CLI lists and revokes it through `/v1/account/credentials`.

### Usage monitoring

| Method | Endpoint           | Purpose                                                                                                         |
| ------ | ------------------ | --------------------------------------------------------------------------------------------------------------- |
| `GET`  | `/v1/usage-events` | Incrementally export normalized usage events filtered by tenant reference, Session, Invocation, model, or time. |

Usage events are an accounting ledger, distinct from the Session transcript and
Invocation lifecycle projection; they serve reconciliation and rebilling.

## 9. Explicitly absent from the Runtime API

| Removed surface                                     | Replacement                                                                                                             |
| --------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------- |
| Projects and project ensure                         | `tenant_ref` on Invocation; internal partitioning is automatic.                                                         |
| Agent configuration resources                       | Auto-created identity anchor only; the execution specification arrives on every Invocation.                             |
| Registry, Releases, pins, deployment tracks         | Host Git/CI selects an immutable spec reference and digest.                                                             |
| Skills and toolkits                                 | Resolved into the execution specification; named custom tool definitions (section 5) are the only registration surface. |
| Integration and OAuth resources                     | Host-owned integrations as client or callback tools.                                                                    |
| Runtime secret store                                | Deployment secrets, short-lived input, or a host credential broker.                                                     |
| Environments, Session compute, runner inventory     | Host-owned sandboxes as host-executed tools; Environment concept deferred to a possible future version.                 |
| Loops, schedules, triggers, source events, waits    | Host scheduler invokes nvoken.                                                                                          |
| Tables, artifacts, files                            | Host application storage; agent memory is an optional runtime resource (section 6).                                     |
| Interactions, inboxes, approvals, channels          | Client ToolCalls and host UI.                                                                                           |
| Webhook endpoint CRUD                               | Authoritative reads, later change projections, and signed callback tools.                                               |
| End-user members and generic user CRUD              | Host identity via `tenant_ref` and delegated actor reference.                                                           |
| Custom roles, permission CRUD, portable invitations | Fixed Account roles; optional Cloud/deployment extensions.                                                              |
| Plans, subscriptions, credits, checkout, invoices   | nvoken Cloud control plane (internal).                                                                                  |
| General admin and support APIs                      | Internal surfaces.                                                                                                      |
| Anthropic `/messages` compatibility                 | Not provided.                                                                                                           |

## 10. OpenAPI and SDK outputs

Three focused outputs: the **Runtime OpenAPI/SDK** (capabilities,
Invocations, Sessions, custom tools, agent memory); the **Session transcript
and change catalog** (canonical message blocks, lifecycle projections, SSE
conventions, delta previews); and the **identity/admin OpenAPI/SDK** (section
8). [`openapi/runtime.yaml`](../../openapi/runtime.yaml) is the frozen first
slice of the Runtime output. Internal surfaces — engine dispatch, deployment
operation, nvoken Cloud control plane — produce no public spec or SDK.

`make openapi-check` validates the focused contract with a pinned,
OpenAPI-3.1-aware Redocly CLI. The repository's `make check` gate includes it.

## 11. Golden path

```text
POST /v1/invocations
  agent_ref: host Agent identity
  idempotency_key: host retry identity
  spec: inline instructions + model/provider
  input: one or more text blocks
  session_id | session_key | neither
  tenant_ref: optional host partition key

202 Accepted
  agent_id, session_id, invocation_id, status, deduplicated
```

Recovery requires only durable IDs:

```text
GET /v1/invocations/{invocation_id}
GET /v1/sessions/{session_id}
```

No provisioning call precedes the first Invocation.

## 12. Launch contract examples

These examples describe semantics. Public Agent, Session, and Invocation IDs
are prefixed UUIDv7 values using `agnt_`, `sesn_`, and `invk_` respectively.

### Identity and Session resolution

1. An Account-wide credential posts `agent_ref=support-triage`,
   `tenant_ref=tenant-a`, and `session_key=ticket-7`. nvoken creates the
   Account-wide Agent anchor and the tenant-a Session in the admission
   transaction.
2. The same Account and Agent post `tenant_ref=tenant-b` with the same
   `session_key`. A distinct Session is created because tenant partition is in
   the key namespace. The Account-wide credential can read both Sessions by ID.
3. A credential constrained to tenant-a can read only the first Session. If it
   explicitly supplies `tenant_ref=tenant-b`, nvoken returns `403 forbidden`
   before lookup. A by-ID lookup outside the constraint returns `404 not_found`.
4. When an Account-wide caller supplies `session_id` and omits `tenant_ref`, the
   stored Session supplies the partition. An explicit but different tenant
   returns `404 not_found`. A Session ID for another Agent also returns
   `404 not_found`.
5. Supplying neither selector creates a new Session in the credential-bound,
   explicit, or default partition, in that precedence order.

### Lost acknowledgement and equal replay

1. A caller posts a request with neither Session selector and idempotency key
   `ticket-7:first`. The Agent anchor, newly generated Session, normalized spec
   snapshot, one input message, and queued Invocation commit, but the connection
   drops before the `202` arrives.
2. The caller retries the same logical request and key. Deduplication runs
   before the one-nonterminal check and returns the original IDs with
   `deduplicated=true`; no second Session, Invocation, or input message exists.
3. The same result applies after the Invocation settles: the background reply
   is still `202`, with the current terminal status.
4. If normalized input, selector kind or value, or inline spec differs, nvoken
   returns `409 idempotency_conflict` and leaves the original records unchanged.
   Object member order is immaterial; array order remains material. The same
   key in another tenant partition is distinct work, including when each
   request supplies an existing Session ID and its stored partition establishes
   the scope before deduplication.

### Concurrent distinct work

Two different idempotency keys race for one idle Session. The Session is locked
inside admission, so one transaction commits the input and queued Invocation.
The loser receives `409 session_invocation_active` without an input message.
After the winner becomes terminal, a new key can use the slot.

### Transaction and process boundaries

The admission transaction covers these crash windows:

- Failure after staging any Agent, Session, spec snapshot, input message, or
  Invocation write but before commit rolls every staged write back. No partial
  Agent-only or Session-only admission, orphan input, or claimable Invocation
  remains.
- Failure after commit but before the `202` is written or received leaves the
  complete admission readable. A same-key replay returns its original records
  and does not append input.
- Failure after acknowledgement or a later API restart cannot erase the
  committed records. Execution still requires a separate authoritative claim.

The executing process is separate from admission in both topologies; delivery
identities, claim owners, leases, and fences never appear in the public
acknowledgement or reads. Until checkpoint recovery ships, engine loss may
produce a durable `execution_lost` failure rather than continuation. The same
OpenAPI request, acknowledgement, read, replay, and conflict fixtures apply
unchanged to the self-contained and split Cloud Run modes.

### One transcript record

Caller and agent content is reconstructed only from ordered `SessionMessage`
rows. Invocation state and append-only state revisions contribute lifecycle and
may reference message sequences, but never contain message or ToolCall-result
payloads. The recovery slice will define the fixed-cut composite cursor that
merges those two ordered sources into one incremental view.
