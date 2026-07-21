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
neither. New requests use fingerprint v2 with requested budgets as material
input; retained v1 budgetless work remains replay-compatible. This adopts
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
