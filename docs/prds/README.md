# PRDs

Product requirement documents, one per discrete step. Each step is small
enough to design and build with focus, in days rather than weeks.

Conventions:

- Sequence naming: `001-prd-name.md`. The zero-padded prefix is the PRD's
  implementation sequence number; planned PRDs below are listed by slug and
  get their prefix when written.
- PRDs elaborate the design packet in [docs/design/](../design/README.md).
  Where a PRD and the packet disagree, the packet governs, and contract
  changes go through the decision log.
- Write each PRD when it is next, not in advance. The sequence below is
  ordered by dependency and will change as we learn.
- Contract and schema PRDs are design work products in their own right. They
  are not implementation tickets, and they come before the code that
  implements them.

## Sequencing rules

The first implementation arc follows these rules:

- **One durable conversation record.** Transcript content has one canonical
  persisted representation. Streaming and change feeds project that state; they
  do not persist a second copy of messages or tool results.
- **Admission is one transaction.** Agent-anchor resolution, Session
  resolution or creation, execution-spec snapshotting, Invocation creation, and
  the caller input either commit together or do not become claimable.
- **Execution never belongs to the public admission request.** An accepted
  Invocation is durable work. The public API may wait for it, but only an
  engine claim drives it. An authenticated internal delivery request may host
  one bounded, durably claimed execution segment.
- **Ownership precedes model execution.** Claims, leases, heartbeats, fencing,
  terminal-write rules, and a reaper are in place before Dive or a provider is
  called.
- **Delivery is not ownership.** Embedded polling and Cloud Tasks are adapters
  over the same exact-Invocation claim/execute/settle service. Postgres remains
  authoritative; an external delivery retry never grants execution authority.
- **Cloud Run is exercised early.** The first real generation-only vertical
  slice is deployed through the paved Google Cloud path before recovery,
  streaming, or tools add more moving parts.
- **Reads precede streams.** Recovery is first proven through authoritative
  JSON reads. SSE is then a resumable projection over the same durable state;
  live token deltas remain explicitly ephemeral.
- **Durable admission and crash resumption are different guarantees.** Early
  Invocations survive disconnects and cannot wedge, but a lost engine may fail
  visibly. The stronger resume-after-crash claim is not confirmed until the
  checkpoint PRD ships.
- **No automatic external effect before recovery is safe.** Callback tools do
  not ship until stable ToolCall identities, persist-before-dispatch, result
  deduplication, checkpoints, and crash-resume behavior are proven.

## Planned sequence

| #  | PRD | Kind | Scope | Status |
| -- | --- | --- | --- | --- |
| 1 | [`001-prd-runtime-record-and-lifecycle-contract.md`](001-prd-runtime-record-and-lifecycle-contract.md) | Contract | Freeze the minimum launch contract before schema work: Agent identity anchors; Account/`tenant_ref` partitioning; Session resolution and key uniqueness; Invocation states, terminal rules, and idempotency scopes; the canonical transcript and change-feed model; and the background JSON acknowledgement/read error model. Declare self-contained and external execution as topology choices with identical public semantics; the internal delivery protocol and Cloud Tasks identities are not public API. Explicitly resolve whether `tenant_ref` participates in the Session namespace and replace any content-bearing second event log in the design packet. Deliverables: focused OpenAPI, worked retry/recovery examples, and decision-log updates. Tools, SSE, spec references, and structured output are deferred to their own PRDs. | Complete |
| 2 | [`002-prd-postgres-runtime-spine.md`](002-prd-postgres-runtime-spine.md) | Schema + foundation | Choose pgx, sqlc, and golang-migrate over Ent and establish versioned, deployment-safe migrations, transactions, UUIDv7 IDs, clock/ID ports, and the minimum Agent, internal tenant partition, Session, SessionMessage, Invocation, InvocationState, and spec-snapshot tables. Define checks, partial unique indexes, deletion/retention posture, repository ports, and transaction boundaries that can later commit runnable state with a dispatch intent. Provide an explicit serialized migration operation suitable for multi-instance Cloud Run rollout; do not rely on unconstrained per-replica startup migration. No HTTP or model execution. | Complete |
| 3 | [`003-prd-durable-invocation-admission.md`](003-prd-durable-invocation-admission.md) | Contract + build | Implement authenticated `POST /v1/invocations` plus the minimum `GET` reads needed to inspect accepted work. Strictly validate and fingerprint the request; then, in one transaction, resolve the installation Account and tenant partition, auto-create the Agent anchor, resolve/create and lock the Session, deduplicate before single-flight, snapshot the inline spec, append caller input, and insert a queued Invocation and state. Return `202`; no engine runs yet. | Complete |
| 4 | [`004-prd-engine-claims-and-fencing.md`](004-prd-engine-claims-and-fencing.md) | Build | Make the Invocation row the authoritative queue. Define an exact-Invocation claim/execute/settle service independent of delivery, then add the self-contained bounded polling adapter, heartbeats, lease attempts/fencing, stale-writer rejection on every execution write, joined drain shutdown, a polling correctness fallback, and a reaper. Prove with a synthetic executor that accepted work is never invisible or permanently wedged and that a duplicate exact attempt cannot create a second owner. A lost running engine fails visibly at this stage; it does not yet resume. | Complete |
| 5 | [`005-prd-generation-only-turns.md`](005-prd-generation-only-turns.md) | Build | First real end-to-end turn: reconstruct the snapshotted inline spec, run a tool-free Dive turn through Anthropic or OpenAI using BYOK installation config, append canonical transcript messages, normalize usage/provenance, and settle transcript plus Invocation atomically. Specs declaring tools are rejected. JSON callers observe through the durable acknowledgement and reads, not handler-owned execution. | Complete |
| 6 | [`006-prd-google-cloud-run-paved-deployment.md`](006-prd-google-cloud-run-paved-deployment.md) | Deployment | Ship the first reproducible Google Cloud vertical slice: one `nvokend` image in the self-contained process role, Cloud Run, Cloud SQL/Postgres, Secret Manager, service identity, a public health endpoint and startup probe, structured logs, explicit request and engine concurrency, instance limits, and bounded shutdown. The combined service uses instance-based CPU and nonzero minimum capacity because work continues after admission returns. Exercise the serialized migration operation and prove an end-to-end `POST` acknowledgement followed by durable terminal `GET`. No Cloud Tasks, Redis, or autoscaling claim beyond the documented combined-mode limits yet. | Complete |
| 7 | [`007-prd-recovery-and-transcript-reads.md`](007-prd-recovery-and-transcript-reads.md) | Contract + build | Complete the authoritative recovery surface: Invocation and Session get/list filters, aggregate usage/provenance, pending/active state, cursor-paginated transcript, and a fixed-cut incremental transcript snapshot that orders message changes before terminal Invocation changes. Recovery requires durable IDs only. This read model is the source for SDK reducers and the later stream. | Complete |
| 8 | [`008-prd-invocation-controls-and-budgets.md`](008-prd-invocation-controls-and-budgets.md) | Build | Add idempotent cancellation, cooperative cross-process wake-up with lease/fence fallback, wall-clock and active-execution deadlines, token/cost/iteration budgets, partial-output rules, and atomic terminal settlement. Distinguish the logical Invocation budget from a bounded execution-segment ceiling so a Cloud Tasks request retains time to persist settlement before its platform deadline. Keep the one-nonterminal-Invocation rule from admission under concurrent requests. | Complete |
| 9 | [`009-prd-cloud-execution-dispatch-foundation.md`](009-prd-cloud-execution-dispatch-foundation.md) | Schema + deployment | Add an internal `ExecutionDispatch` transactional outbox without routing real Invocations yet. Every task-routed transition into runnable state writes domain state and dispatch intent in one Postgres transaction. Add fenced publication claims, deterministic task names, `AlreadyExists` convergence, a polling publisher and reconciler, retention and alerts, and a synthetic exact-attempt target. Deploy the same image in a separate private executor process role with a dispatch-ID-only endpoint, internal ingress, Cloud Run IAM/OIDC, explicit queue/service concurrency, and no public application routes. Prove commit/publish crash windows, duplicate delivery, authentication, and revision request draining in staging. Cloud Tasks is delivery only; Postgres remains authoritative. | Implemented; cloud proof pending |
| 10 | `prd-cloud-tasks-invocation-execution` | Build + rollout | Route generation-only Invocation segments through the dispatch outbox and private Cloud Tasks-to-Cloud Run executor. Keep the task request open for one bounded segment; load tenant scope and all inputs from Postgres; acquire the exact fenced Invocation claim; and return `2xx` only after a durable no-op or domain settlement. Cloud Tasks retries transport or settlement uncertainty, never semantic model failure. Prove duplicate delivery, Session single-flight, cancellation, queue/executor capacity bounds, a segment ceiling below the platform deadline, and canary/rollback between external and self-contained modes without double claim. A lost engine still fails visibly until crash resume ships. This split topology becomes the recommended production Google Cloud path. | Planned |
| 11 | `prd-resumable-streaming` | Contract + build | Add SSE as a projection of the transcript/change read model: durable replay by opaque cursor, cross-process Redis fan-out for ephemeral generation deltas, DB-derived terminal state, deliberate stream rotation, slow-consumer behavior, and reconnect/terminal reconciliation. A disconnect never affects execution and no stream frame is a second source of truth. The self-contained mode may use the same fan-out port with an in-process adapter. | Planned |
| 12 | `prd-durable-toolcall-and-checkpoint-model` | Contract + schema | Introduce ToolCall as the universal execution boundary: stable identity, immutable request, mode, deadline, attempts, first accepted result/error, and Invocation ownership. Define the iteration/checkpoint cursor and persist-before-dispatch rule, including how transcript prefixes, ToolCall results, and usage receipts make replay safe. Prove the storage transitions with a deterministic builtin test tool; no host-side tool mode yet. | Planned |
| 13 | `prd-structured-output` | Contract + build | Add structured output as a reserved builtin ToolCall against the host schema. Validate the supported JSON Schema subset, persist value and provenance in the same terminal transaction, and fail clearly when the contract is unsatisfied. This exercises the durable ToolCall path without an external side effect. | Planned |
| 14 | `prd-crash-resume` | Build | Replace the temporary engine-loss failure policy with checkpoint-based recovery. Reclaim expired claims, rebuild Dive from transcript plus cursor, reuse accepted ToolCall results, preserve ToolCall IDs across uncertain retries, and make usage receipts idempotent. Kill the engine at model, tool-dispatch, result-persist, and terminal-commit boundaries in both execution modes to prove stale engines cannot commit and completed work is not re-applied. This is the gate for confirming the public resume-after-failure claim. | Planned |
| 15 | `prd-client-tools` | Contract + build | Add client ToolCalls and durable result submission. Persist and expose the call before delivery, park the Invocation in `waiting` with no goroutine, accept batchable/idempotent results, enforce deadlines and cancellation, and resume on any engine process. In external mode, result acceptance writes the successor execution dispatch in the same transaction. Decide the narrow command endpoint here rather than assuming a generic Session event POST. | Planned |
| 16 | `prd-callback-tools` | Build | Add signed host callbacks through a durable delivery worker/outbox: egress policy, shared-secret signing first and JWKS rotation next, bounded retries, stable delivery and ToolCall idempotency identities, response validation, and recovery into the same parked-Invocation path. Port the small proven signing seam from Mobius Cloud, not its broader action/integration resource model. | Planned |

## Beyond the first implementation arc

Deferred until the sequence above is real, in no committed order: the full
identity/admin API (credential CRUD, device authorization, OIDC), spec digest
caching and spec-by-reference, additional builtin tools, agent memory, custom
tool CRUD, retention/compaction/forking, indexed metadata expansion, operator
console views, SDKs, non-Google deployment targets, and additional distribution
conveniences beyond the paved Cloud Run path. A minimal installation-resolved
Account/runtime-auth context is still required by admission; the deferred work
is the portable management surface for issuing and administering credentials.
