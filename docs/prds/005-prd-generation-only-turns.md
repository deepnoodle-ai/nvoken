# Execute and Persist Generation-Only Turns

**Status:** Complete

**Sequence:** 005

**Depends on:** `001-prd-runtime-record-and-lifecycle-contract.md`,
`002-prd-postgres-runtime-spine.md`,
`003-prd-durable-invocation-admission.md`,
`004-prd-engine-claims-and-fencing.md`

## ELI5

An accepted turn can now call Anthropic or OpenAI and save the reply. The model
call runs only after the engine owns the durable work, and the reply becomes
visible state only if transcript and completion commit together. This slice has
no tools, streaming, public transcript endpoint, budgets, or crash resume.

## Why

PRD 004 proved ownership with a synthetic executor but deliberately left the
production daemon unable to claim work. The next useful slice must replace that
test double with Dive, rebuild each turn from the immutable spec and Session
transcript, and preserve the result without weakening the existing fence.

Mobius Cloud provides two relevant precedents: its executor reconstructs the
model conversation from canonical Session messages, and its terminal transaction
appends generated messages and completes the turn together. It also demonstrates
why the full provider response is not a second durable transcript: normalized
usage and model provenance are retained separately. nvoken keeps those mechanics
while using its smaller inline-spec and installation-BYOK boundary.

## Outcome

In self-contained mode, `nvokend` executes queued text-only Invocations through
Dive with an installation-configured Anthropic or OpenAI key. A successful turn
atomically appends normalized assistant messages, records usage and model
provenance, advances the lifecycle watermark, and completes the Invocation.

## Scope

**In:** strict reconstruction of the admitted inline spec; ordered text Session
history; explicit Anthropic and OpenAI Dive adapters; installation-level BYOK;
an explicit migration adding terminal aggregate `usage` and `provenance` objects
to the Invocation row; normalized assistant content; fenced atomic settlement;
safe failures; production self-contained runner wiring; provider-adapter and
optional live smoke tests.

**Out:** tools or multiple model iterations; spec references; structured output;
public transcript, output, usage, provenance, or capabilities endpoints; streaming
or token deltas; cancellation and budgets; per-Account credentials or platform
credits; Cloud Run deployment assets; Cloud Tasks; checkpoints or replay after an
engine crash. PRD 007 exposes the transcript, and PRD 014 makes an interrupted
provider call resumable.

## Requirements

- **R1 — Reconstruct only durable inputs.** After a fenced claim, the executor
  must load the Invocation's immutable spec snapshot and the Session's ordered
  canonical messages from Postgres. The snapshot must decode as exactly the
  launch inline schema: nonblank instructions plus provider and model, with no
  unknown fields. Session messages must be ordered by sequence, belong to the
  claimed Session scope, and decode into supported provider-neutral content.
  Request-handler memory, admission payloads, and delivery data must not be
  execution inputs.

- **R2 — One explicit tool-free Dive call.** The executor must construct a Dive
  agent with the snapshotted instructions, the requested model, the complete
  Session history including the already-durable caller message, and no tools.
  The only prompt addition is Dive's version-pinned static protocol priming;
  no host or process-memory instructions may enter the call.
  This slice supports canonical providers `anthropic` and `openai` only. It must
  use the corresponding installation API key explicitly rather than ambient
  provider discovery or cross-provider fallback. Missing credentials,
  unsupported providers, tool-bearing or otherwise deferred specs, and invalid
  durable history must fail without a provider call.

- **R3 — Canonical assistant transcript.** A completed Dive response must become
  one or more ordered assistant `SessionMessage` rows containing normalized Dive
  content blocks. The executor must reject a suspended response, ToolCall/tool
  result content, a non-assistant output, or an empty response in this slice.
  Provider response objects, duplicate output blobs, and terminal errors must
  not carry a second copy of assistant content.

- **R4 — Normalized usage and provenance.** An explicit golang-migrate
  migration must add nullable JSON-object `usage` and `provenance` columns to
  the Invocation row. They are one terminal aggregate per Invocation—not a
  model-call ledger—and nonterminal rows must keep both null. Successful
  settlement must retain nonnegative
  input, output, cache-read, cache-creation, and reasoning token counts; Dive's
  estimated cost breakdown when known; and bounded provenance containing the
  canonical provider, requested model, served model, and fixed credential source
  `installation_byok`. These fields are execution evidence, not transcript
  content or a billing ledger. The Invocation's first-terminal-write and
  immutability constraints make the aggregate unique and retry-safe. The runtime
  must not persist a raw provider response, request, API key, or provider
  credential identifier.

- **R5 — One fenced terminal transaction.** Successful settlement must lock the
  Session before the Invocation, revalidate owner, attempt, running state, and
  unexpired lease, allocate message sequences and one lifecycle revision, append
  all assistant messages and usage/provenance, update the Invocation to
  `completed`, and append the completed state with the final message sequence as
  its watermark in one Postgres transaction. Any fault or stale fence must roll
  the entire settlement back. Retrying settlement for the same in-memory result
  must not create duplicate messages or usage.

- **R6 — Safe terminal failure and cancellation.** A provider/configuration or
  valid-response failure while the lease remains owned must settle as `failed`
  with a generic safe message. The frozen public failure enum remains unchanged:
  corrupt or unsupported durable spec/history maps to `internal`; unsupported
  provider, missing credentials, provider-call failure, suspension, ToolCall
  output, non-assistant output, and empty output map to `provider_error`.
  Internal logs may distinguish those operational classes without adding them to
  the public error object. Executor context cancellation from drain or lease loss
  must return no semantic result, leaving the existing owner/reaper policy to
  decide the durable outcome. This slice does not retry a possibly charged model
  call after process loss.

- **R7 — Activate self-contained production execution.** Normal `nvokend serve`
  must compose the real generation executor, ownership service, bounded runner,
  wake signal, and HTTP server over one Postgres pool. The server and runner must
  supervise one another, preserve polling correctness when wakes are lost, and
  complete the runner's joined drain before the pool closes. The synthetic
  executor must remain test-only. Engine concurrency and lease/drain timing must
  have validated bounded installation configuration suitable for PRD 006.

- **R8 — Secret-safe operational evidence.** Logs must identify Invocation,
  attempt, provider, requested/served model, terminal class, latency, and normalized
  token counts where available. They must not contain instructions, message
  content, provider bodies, API keys, authorization headers, or spec JSON.
  Schema changes remain explicit under `nvokend migrate`; serve only verifies the
  required schema version.

## Acceptance

- [x] **A1 (R1, R2):** Given a multi-turn Session, a deterministic model double
  receives the exact ordered user/assistant history once, the current caller
  input is not duplicated, the snapshotted instructions plus Dive's static
  protocol priming win over any process memory, and the Dive agent receives zero
  tools.

- [x] **A2 (R2, R6):** Anthropic and OpenAI configurations select only their
  explicit Dive adapter and key. An unsupported provider, missing matching key,
  unknown spec field, tool declaration, or malformed historical content makes
  no model call and settles one typed failure that frees the Session.

- [x] **A3 (R3, R4):** A successful normalized response with multiple assistant
  content blocks persists them in order and records requested/served model,
  provider, credential source, all token categories, and known estimated cost.
  No Invocation, lifecycle state, log, or auxiliary row contains a duplicate
  assistant payload or raw provider request/response.

- [x] **A4 (R5):** Postgres fault injection after each assistant append, the
  terminal Invocation update containing usage/provenance, and lifecycle append
  leaves the original running row and caller-only transcript unchanged. A
  successful retry commits exactly one output set and a completed watermark; a
  stale owner or attempt commits none of it.

- [x] **A5 (R3, R6):** Provider error, missing credentials, unsupported provider,
  suspended response, ToolCall output, non-assistant output, and empty output
  settle with public code `provider_error`; corrupt durable input settles with
  `internal`. Drain or definitive lease loss cancels the model context and cannot
  be mislabeled as a semantic provider failure or append partial assistant
  content.

- [x] **A6 (R7):** Production daemon composition admits a request, returns `202`
  before generation, then reaches a durable terminal `GET` through the real
  generation executor path. Lost wakes still execute through polling, configured
  concurrency is never exceeded, and shutdown joins model and heartbeat work
  before closing Postgres.

- [x] **A7 (R4, R8):** Captured logs and persisted fixtures expose the identifiers,
  model provenance, terminal class, latency, and counts needed to diagnose a turn
  while searches for fixture instructions, messages, provider payloads, and API
  keys return no matches outside the canonical transcript and installation
  configuration/process environment boundary.

- [x] **A8 (R2, R3):** Deterministic adapter tests cover both providers without
  network access, and opt-in live smoke tests can run one minimal tool-free turn
  against each provider when its installation key is supplied. The standard test
  gate never requires external credentials or network access.

## Sequencing notes

- PRD 006 deploys this exact self-contained process shape to Cloud Run and proves
  the paved migration/startup/drain path.
- PRD 007 adds the public transcript and exposes the Invocation's aggregate
  usage/provenance in its richer recovery reads; this PRD deliberately adds no
  premature output endpoint.
- PRDs 009 and 010 move delivery to Cloud Tasks without changing generation or
  settlement semantics. PRD 014 later adds checkpoints and replay-safe usage
  receipts before an uncertain model call may resume.
