# nvoken Context and Decision History

Standing context and the decision log for the design packet. The early
entries originate in the design program for nvoken's predecessor runtime
(Mobius) and carry over; dated entries are decisions in their own right.

1. nvoken should focus more on the "embedded" use case, where it is used as the agentic backend for other products.

2. We need to support being the backend for multi-tenant SaaS apps really well.

3. Only a small list of integrations for built-in tools at launch. Mostly, the host app is responsible for implementing integrations and tools.

4. Focus on nvoken as a reusable, robust, flexible agentic HARNESS. One that is more stable than competing projects. Address the concerns that people have about Claude Managed Agents: vendor lockin, lack of reliability, no private network reach, no native trigger/scheduler surface, cost control, pricing.

5. Clean, constrained API surface. A sprawling API would be a failure mode.

6.  Learn from Claude Managed Agents (CMA) and open source CMA competitors, factor in what the market is saying about them. But be discerning about which projects are truly competitors and which aren't. The area of focus is so nuanced yet important. Multica for example is probably NOT a competitor - they are focused on AI agent teammates - but our nvoken to-date I've thought of in that capacity. But our focus is potentially now the embedded use case.

7.  In order to support the self-hosting option more fully, we need a paved path for alternate authentication. Clerk is great for us when we are deploying it, but deploys of nvoken as open source may not want to use Clerk. What should we do instead, in that case?

8.  Make sure we fully understand, "if we 100% focused on the embedded case, what should the product look like?"

9. Terminology: the word "loop" is reserved for the predecessor runtime's Loops feature (scheduled automations), which nvoken does not recreate. The model-and-tool-call cycle within one Invocation is a "turn" (agent turn, turn engine). The positioning phrase is "agent runtime as a service".

10. Interaction model: the Session event log is the single interaction medium — agent output, ToolCalls, lifecycle, and host input are durable, cursor-ordered events. Live delivery is SSE with cursor resume; host input (tool results, directions) is an HTTP POST of typed events; there is no WebSocket. A client ToolCall parks the Invocation in a waiting state, holding no compute, until the result event arrives. Chosen over a duplex WebSocket for client flexibility and ops/debuggability, informed by the CMA event model (which we beat on cursor resume).

11. Credential unification (2026-07-20): CLI credentials are no longer a separate resource. API credentials are one resource at `/v1/account/credentials` with two kinds: `machine` (one fixed Operator, Viewer, or Runtime profile plus narrowing constraints) and `user` (issued through the device authorization flow; effective role resolved at authentication time as the owner's current membership role intersected with an optional Operator/Viewer cap). The parallel `/v1/auth/cli-credentials` CRUD is removed; the CLI lists and revokes its credential through the unified collection. Auth-time role resolution preserves the property that demoting or removing the human takes effect immediately.

12. Portable members CRUD deferred (2026-07-20): the `/v1/account/members` endpoints are removed from the launch contract. Self-hosted operator provisioning is the bootstrap admin credential plus a declarative operator allowlist in installation configuration; nvoken Cloud manages membership through its internal control plane. The local membership mechanism keyed by `(issuer, subject)` is unchanged. Any future portable members API grants by issuer and email claim with the subject bound at first login — granting by exact OIDC subject was rejected because subjects are opaque and unknowable before a user's first login.

13. Browser OIDC endpoints reclassified as installation plumbing (2026-07-20): OIDC login, callback, and logout are implemented by the deployment but excluded from the generated identity/admin OpenAPI — no SDK calls them, and the callback URL registered with an identity provider must stay stable across API versions. `/v1/auth/context` is removed; `GET /v1/account` now returns the caller's resolved subject, role or profile, constraints, authentication method, and assurance alongside Account identity. Net effect of decisions 11–13: the launch identity/admin contract is roughly ten operations — account whoami, device authorization (three), API credentials (five), usage events — with no change to the embedded golden path.

14. Canonical transcript record (2026-07-20): decision 10's co-equal durable
Session event log is superseded. Ordered `SessionMessage` rows are the sole
durable representation of caller, agent, and ToolCall content. Invocation state
and append-only lifecycle revisions may reference message sequence numbers but
cannot copy message payloads. Incremental reads and SSE are projections over
those records plus explicitly ephemeral live deltas. A generic Session event
append endpoint is removed; later client ToolCall results and steering use
narrow commands. This follows the Mobius transcript source-of-truth reversal,
where duplicate durable message and event encodings had already diverged under
retention and live reconciliation.

15. Account-wide Agent and tenant-partitioned Session namespace (2026-07-20):
`agent_ref` resolves an identity-only Agent anchor unique within the Account and
shared across tenant partitions. Session keys are unique within `(Account,
effective tenant partition, Agent, session_key)`, and a Session's Agent and
partition are immutable. Tenant-constrained credentials reject an explicit
mismatch before lookup. Account-wide by-ID access may infer the stored
partition; explicit incompatible or undisclosable resources use `not_found`.

16. Invocation admission and lifecycle contract (2026-07-20): public states are
exactly `queued`, `running`, `waiting`, `completed`, `failed`, and `cancelled`;
the last three are immutable and deadline or budget exhaustion is a typed
failure. One Session has at most one nonterminal Invocation. Admission commits
Agent and Session resolution or creation, the inline spec snapshot, one input
message, and one queued Invocation in one Postgres transaction. Body
idempotency is scoped to Account, effective tenant partition, Agent reference,
and caller key. Equal replay precedes the nonterminal check and returns the
original work; materially different reuse conflicts. Background admission and
equal replay return `202` after commit, including replay of a terminal
Invocation; `200` was rejected so one operation keeps one acknowledgement
contract.

17. Topology-neutral durable admission (2026-07-20): the public Runtime contract
is identical whether an engine polls inside `nvokend` or an authenticated Cloud
Tasks request reaches a separate Cloud Run executor. Delivery is never
ownership; Postgres claims, leases, and fencing remain authoritative and their
identities are private. Durable admission does not by itself promise
checkpoint-based crash continuation. Until checkpoint and replay safety ship,
engine loss may settle an Invocation as a durable typed failure.

18. Explicit pgx/sqlc runtime store and serialized golang-migrate migrations
(2026-07-20): nvoken uses pgx for Postgres access, sqlc-generated query code
inside the Postgres adapter, and ordered embedded SQL migrations applied by
golang-migrate. It does not use Ent or automatic schema diffing.
Correctness-critical checks, composite foreign keys, partial unique indexes,
and queries remain directly reviewable in SQL; generated database types do not
cross the adapter boundary. Migrations run only through the bounded `nvokend
migrate` operation. golang-migrate's pgx/v5 driver pins one connection and
holds a session-scoped Postgres advisory lock so concurrent release jobs
serialize; ordinary service replicas never migrate on startup. Migration
versions are immutable repository artifacts, while the database records the
current version and dirty state and the command rejects a dirty or newer
schema. This incorporates Mobius Cloud's useful transaction and advisory-lock
precedent while avoiding the generated schema and concurrent-startup migration
surface that accumulated there.

19. Bounded admission and stable request fingerprint (2026-07-20): the
background Invocation request is limited to 1 MiB of encoded JSON and 64 text
blocks. `agent_ref`, `tenant_ref`, `session_key`, `idempotency_key`, and the
model provider/name are each limited to 255 Unicode characters; whitespace-only
required strings are invalid. Idempotency comparison uses the documented v1
SHA-256 canonical representation under `docs/design/`, so JSON object order is
irrelevant, array and string changes remain material, and retained work stays
comparable across service releases.

20. Self-contained Cloud Run drain limitation (2026-07-20): instance-based CPU
and a nonzero minimum let the combined API and engine continue work after an
admission response, but they do not make a background turn request-bound.
Cloud Run revision shutdown and scale-in provide only the platform termination
window, so a longer running turn may lose its engine and settle through the
pre-checkpoint `execution_lost` policy. The process stops claims and cooperatively
drains within one configured shutdown budget, and operators deploy combined mode
during quiet periods. The stronger drain-without-interruption behavior applies
to request-bound split execution; checkpoint recovery later makes process loss
resumable in either topology.

21. Fixed-cut JSON recovery model (2026-07-21): public Session recovery composes
the existing `SessionMessage.sequence` and `InvocationState.revision`
watermarks rather than creating a durable event log. A drain captures both
committed high-water marks from the Session row in one read, retains that cut in
page tokens, and finishes message pages before lifecycle-change pages. The
final composite cursor becomes the next incremental starting point. Collection,
message, and transcript cursors are opaque and scope-bound but not signed;
authorization is re-evaluated on every read and forged positions can only alter
the caller's own traversal. Session reads retain the frozen nullable
`active_invocation_id` and add nullable `active_invocation_status` as a sibling
field, present and null together. This carries forward Mobius Cloud's useful
fixed-cut ordering while omitting its separate turn model, interactions, live
preview state, and project namespace.

22. Durable Invocation controls and bounded execution (2026-07-21): a host may
idempotently cancel an Invocation, and an inline spec may request wall-clock,
active-execution, output-token, estimated-cost, and iteration limits. Admission
resolves and persists all limits and a wall deadline; claims persist one active
segment whose deadline is the earliest logical or installation segment limit.
Postgres terminal settlement is first-writer-wins and accrues the active segment
once. PostgreSQL LISTEN/NOTIFY lowers cross-instance cancellation latency but
grants no authority; lease renewal and settlement fences remain the loss-safe
fallback. Failed budget or deadline outcomes may retain paired normalized usage
and provenance when a provider result produced them, while cancellation retains
neither. A requested estimated-cost limit fails before a provider call when the
adapter knows USD pricing is unavailable, and exposes
`estimated_cost_unavailable` as safe public detail. Canonical failed checkpoints
remain readable evidence, but failed/cancelled assistant and tool messages are
excluded from future provider context. New requests use fingerprint v2 with
requested budgets as material input; retained v1 budgetless work remains
replay-compatible. This adopts
Mobius Cloud's cancellation and active-segment invariants without importing its
turn/run ownership or wait/job tables.

23. Request-bound Google Cloud execution default (2026-07-21): installations
choose Invocation execution explicitly as `embedded` or `cloud_tasks`; the
public Runtime contract is unchanged. Local and generic self-hosted operation
defaults to embedded polling. The paved Google Cloud deployment defaults to a
private Cloud Tasks-to-Cloud Run executor because its in-flight HTTP request
allows normal revision draining. Admission in that mode commits an Invocation
dispatch intent atomically, while Postgres exact claims, leases, fences,
cancellation, budgets, and terminal writes remain authoritative. The combined
service continues non-request-bound publication, reconciliation, repair, and
reaping with instance CPU and nonzero minimum capacity. Until checkpoint replay
ships, an abruptly lost model segment fails visibly as `execution_lost` rather
than being regenerated.

24. Resumable Session streaming and ephemeral delta boundary (2026-07-21):
the Session transcript SSE endpoint projects the fixed-cut JSON recovery model.
Each nonempty authoritative snapshot frame carries its opaque composite
`resume_cursor` as the SSE ID, while provider-normalized generation deltas,
resync instructions, and deliberate end frames carry no ID and are never
replayed or persisted. The handler subscribes before its first Postgres drain,
polls Postgres as a correctness fallback, and derives terminal close through a
drain/read/drain/read reconciliation. Buffer overflow or Redis loss may discard
provisional output and triggers client resync; it cannot lose committed state or
affect execution. Embedded mode uses the same fan-out port in process, while the
split Google path uses private Redis Pub/Sub between executor and API replicas.
That paved Redis trust boundary requires private VPC access, Redis AUTH, and
server-authenticated TLS; its generated AUTH string is exposed to only the two
service identities through Secret Manager, and clients trust all active
instance CAs so rotation can overlap safely.
This carries forward Mobius Cloud's cursor, subscribe-before-drain, and rotation
precedent while omitting its separate live-transcript accumulator and legacy
event surfaces. It resolves architecture open question 2: token/thinking deltas
have no replay guarantee; canonical messages and Invocation lifecycle changes
do.

25. Durable ToolCall and checkpoint evidence precedes resume (2026-07-21):
each accepted model iteration now atomically appends its normalized assistant
message, immutable normalized usage/provenance receipt, prepared ToolCalls, and
one monotonic Invocation checkpoint before any trusted builtin runs. ToolCall
identity is nvoken-owned and stable; provider call IDs are correlation keys
unique within an Invocation iteration. Request and result payloads exist only
in canonical `SessionMessage` rows, while ToolCall and checkpoint rows retain
scope, hashes, status, attempts, transcript references, and watermarks. A
current Invocation fence controls builtin start/result writes; the competing
legitimate writer is the first terminal Invocation transaction, which closes
open calls with one bounded synthetic tool-result message and advances the
terminal lifecycle watermark. Equal result replay converges and changed or
stale replay fails closed. Normalized receipts sum to any terminal usage
projection that is present, but cancellation and `execution_lost` keep their
existing no-aggregate public contract. This establishes evidence, not crash
resume: expired claims still fail as `execution_lost` until the later recovery
slice makes recorded prefixes reclaimable. It replaces Mobius Cloud's mutable
checkpoint blob with first-class lifecycle records and keeps all new business
evidence under the owning Invocation/Session retention trace.

26. Equality-proven structured output projection (2026-07-21): a host may
attach one bounded, self-contained object schema to an Invocation. nvoken
projects that schema through the reserved `nvoken_submit_output` builtin,
persists every request and result through the durable ToolCall/checkpoint path,
and independently validates submissions. Only completed terminal settlement
may copy the first accepted value plus ToolCall/schema provenance onto the
Invocation and its lifecycle revision, in the same transaction and after
proving semantic equality with the canonical ToolCall request. This is the one
sanctioned content projection outside `SessionMessage`: it exists for direct
machine-readable host recovery, while the transcript remains canonical for
conversation replay. Final text, including JSON-looking text, is not a fallback.
New admissions use fingerprint v3 so adding, removing, or changing the output
contract changes idempotency identity; retained v1/v2 schema-free rows remain
comparable by their recorded algorithm. Crash recovery remains deferred: a
lost engine still settles `execution_lost` and publishes no output.

27. Checkpoint-based recovery supersedes lease-loss failure (2026-07-21): an
expired execution owner no longer terminalizes otherwise viable work. The
reaper accrues its active segment only through the earlier recorded lease or
execution deadline, clears ownership, publishes a queued lifecycle revision,
and leaves the same Invocation, transcript, receipts, checkpoints, ToolCalls,
and dispatch evidence intact. A replacement increments the Invocation fence,
validates the append-only prefix, initializes cumulative usage and iteration,
and continues from the next incomplete boundary. Every production model
response, including a tool-free final response, is checkpointed before tool
execution or settlement; therefore a committed final checkpoint settles
without a second provider call. Pending builtins reuse their ToolCall, and an
abandoned running builtin closes only its old attempt before starting a new one
under the same ToolCall ID. Accepted results are never rerun. Corrupt evidence
fails once with public `internal` and internal class `recovery_invalid`.
`execution_lost` remains readable for retained historical rows but is no longer
written for recoverable lease expiry. This decision does not add a public retry
endpoint, arbitrary provider snapshots, exactly-once billing or external
effects, or cooperative checkpoint-and-chain at the intentional segment limit.

28. Client tools park and resume through one Invocation command (2026-07-21):
an inline immutable spec may declare up to 32 ordered `mode: client` tools with
bounded object schemas. New admissions use fingerprint v4, which makes the
ordered declarations material while recursively canonicalizing schema objects;
retained tools-free v1-v3 rows remain comparable by their recorded algorithm.
When Dive selects a client tool, nvoken commits the assistant request, stable
ToolCall identities, receipt, and checkpoint before atomically parking the
Invocation in `waiting` with no lease or active segment. Invocation and Session
reads project unresolved calls from that evidence. The only public write is
`POST /v1/invocations/{invocation_id}/tool-results`: under Session, Invocation,
then ToolCall locks, the first accepted result wins, equal replay deduplicates,
partial batches remain waiting, and the final batch queues the same Invocation
and its Cloud Tasks dispatch in one transaction. Durable result origin prevents
client replay comparison against nvoken-owned cancellation or deadline
evidence. A replacement owner can also recognize a checkpointed-but-not-yet-
parked client batch and park it without replaying the provider. The canonical
transcript keeps submission order; provider projection coalesces and restores
original model batch order. This deliberately avoids Mobius Cloud's mutable
suspension blob and does not add generic Session append, callback delivery, or
host credentials to nvoken.

29. Callback tools use a blocked outbox and installation HMAC v1 (2026-07-21):
an inline callback declaration adds one public HTTPS URL to the immutable tool
spec and fingerprint v5. Each selected callback ToolCall commits exactly one
blocked `CallbackDelivery` in its model-checkpoint transaction; only the
fenced transition that parks the Invocation activates those rows. A
combined-role Postgres worker claims with an expiring attempt fence, signs the
exact compact body as `v1.<delivery_id>.<timestamp>.<body>`, refuses redirects
and nonpublic dial targets, and retries bounded transport/408/425/429/5xx
outcomes. Delivery ID stays stable across retries, while `Idempotency-Key` is
the ToolCall ID because the receiver must make uncertain external effects
idempotent at that boundary. A valid or bounded failure response settles the
delivery, ToolCall, transcript result, checkpoint, and possible waiting-to-
queued transition atomically. The one installation HMAC key is deployment
configuration and never durable data; its nonsecret ID/version are headers.
The v1 body reserves optional delegated actor context, omitted until admission
owns that claim. Per-tool credentials, private egress, JWKS/public-key signing,
and automated key rotation remain separate future decisions.

30. Model credentials bind per Invocation and provider (2026-07-21): the API
supports four explicit sources for the model provider referenced by an
Invocation: `caller_ephemeral`, `account_byok`, `tenant_byok`, and `platform`.
Existing self-hosted `installation_byok` remains a deployment source. A
self-hosted installation may enable caller, Account, and tenant BYOK when it
configures application-layer credential encryption, but cannot select
`platform`; nvoken Cloud may enable the four API sources but cannot select
`installation_byok`. Reusable Account and tenant BYOK live as encrypted,
versioned model-provider credential resources; tenant scope is the effective
internal partition resolved from the host-controlled `tenant_ref`, not an
end-user identity or new Tenant resource. An `InvocationProviderCredential`
binding records exactly one source per Invocation and canonical provider. The
current spec names one provider, so the current binding set has one row; the
key remains provider-scoped for future specs without introducing multi-model
behavior here. Caller-ephemeral ciphertext lives only on that binding and is
cleared at terminal settlement or bounded expiry; reusable sources bind an
immutable credential version, while platform and installation sources bind
nonsecret deployment selectors. The binding commits with durable admission,
is excluded from the execution-spec snapshot, and records no secret bytes in
the idempotency fingerprint. Fingerprint v6 records the request's literal
nonsecret source selection, including omission, rather than any materialized
installation default. Equal admission replay therefore returns the original
binding and never replaces its credential or changes source after a default
change. Explicit source failure, revocation, expiry, or decryption failure
settles visibly as credential
unavailable; nvoken never silently changes source or charges platform credits.
This is a narrow model-gateway exception to the no-secret-store boundary, not a
general integration, OAuth, per-tool, or business-credential vault.

31. One-release forward schema compatibility supersedes exact newer-schema
rejection (2026-07-21): golang-migrate remains the sole forward migration
engine, but an exact schema match is no longer the only safe startup state.
Migration 14 introduces a singleton database record containing the resulting
schema version and the minimum binary schema version allowed to serve it. Each
later migration declares the same values in an embedded manifest for
pre-mutation release checks and updates the database record in its SQL
transaction for startup checks. An ordinary release must keep the currently
serving binary within that declared minimum; otherwise preflight fails before
DDL and the change requires an expand release followed by a later contract
migration. A clean exact schema or explicitly compatible newer schema may
serve; dirty, behind, unknown, and declared-unsafe schemas fail closed. The
guarantee starts after the transition release and covers only the immediately
previous production binary. There are no down migrations or arbitrary
historical downgrade claims.
