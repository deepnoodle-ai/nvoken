# Make Tool Execution Durable Before Adding Host Tools

**Status:** Implemented

**Sequence:** 012

**Depends on:** `002-prd-postgres-runtime-spine.md`,
`004-prd-engine-claims-and-fencing.md`,
`005-prd-generation-only-turns.md`, and
`008-prd-invocation-controls-and-limits.md`

## ELI5

Before nvoken can safely call a tool, it must save what the model requested and
who owns that work. It must also save each model/tool boundary so a later
engine can tell what already happened. This PRD builds and proves those durable
records with a harmless deterministic builtin; host callbacks and crash resume
come later.

## Why

The current engine calls a model once and commits its final assistant message,
aggregate usage, and terminal Invocation together. Tool execution introduces
new crash windows: a model response can request a tool before the request is
saved, a tool can finish before its result is saved, or a stale engine can try
to publish progress after losing its lease. PRD 014 cannot resume those windows
without stable identities and persisted turn boundaries first.

Mobius Cloud proves that structured transcript messages and fenced turn writes
are useful foundations. It also shows what nvoken should not copy: tool state
embedded only in a mutable checkpoint blob required later repair for dangling
tool calls and provider-invalid replay. nvoken instead makes ToolCall lifecycle
first-class while keeping request and result content in the one canonical
`SessionMessage` transcript.

## Outcome

Every model iteration, builtin ToolCall request, accepted ToolCall result, and
normalized usage receipt can be committed as a fenced checkpoint. The durable
records unambiguously say what may run, what already ran, and where a future
engine may continue, without yet reclaiming a lost engine.

## Scope

**In:** ToolCall, attempt, model-usage-receipt, and Invocation-checkpoint
records; stable IDs and immutable scope; builtin/callback/host mode values;
canonical transcript references; persist-before-builtin execution; first
accepted result/error; iteration and checkpoint cursors; fenced checkpoint
writes; one deterministic test builtin; replay-safe transcript reconstruction;
schema, ports, services, Dive seam, design contract, and Postgres proof.

**Out:** public ToolCall endpoints; accepting tools in the public execution
spec; production builtin catalog; callback or client execution; `waiting` and
result submission; automatic reclaim after engine loss; checkpoint-and-chain;
structured output; usage billing; tool progress streaming; retrying an
uncertain external side effect.

## Requirements

- **R1 — Stable, scoped ToolCall identity.** A ToolCall must have an nvoken
  UUIDv7 ID and immutable Invocation, Session, Account, tenant partition, Agent,
  iteration, batch ordinal, provider-call identity, tool name, mode, request
  message reference and digest, and absolute deadline. Modes are exactly
  `builtin`, `callback`, and `client`, even though only a test-only builtin may
  execute in this slice. Provider-call identity is an adapter correlation key,
  not the public durable identity, and is unique within
  `(Invocation, iteration)` across lease attempts. Re-observing that composite
  key with the same immutable request must resolve to the original ToolCall;
  changed reuse must fail closed. Retrying in a later iteration is a new call.

- **R2 — One canonical copy of tool content.** The assistant message containing
  a tool request and the tool-role message containing its accepted result or
  error must be append-only `SessionMessage` rows. ToolCall and checkpoint rows
  may reference their IDs/sequences and retain hashes, status, and operational
  metadata, but must not copy request or result content. Transcript replay must
  preserve complete request/result pairs in provider-valid order; terminalizing
  an open call must append a bounded synthetic error result rather than leave a
  dangling request.

- **R3 — Persist before execution or delivery.** A complete model iteration
  that requests tools must atomically append its normalized assistant message,
  create every ToolCall in that batch, record the iteration's usage receipt,
  and advance the Invocation checkpoint before any builtin runs or any future
  callback/client delivery becomes eligible. If that transaction rolls back,
  no tool may run. Callback/client rows remain inert in this slice.

- **R4 — Fenced attempts and first accepted outcome.** Starting a builtin must
  append a numbered ToolCall attempt only while the owning Invocation lease,
  owner, attempt, and deadline remain current. Accepting its result or error
  must use the same fence, append one canonical tool result message, terminalize
  the attempt and ToolCall, and advance the checkpoint atomically. The first
  accepted outcome wins. An equal duplicate returns the stored outcome without
  another message; a changed duplicate, stale owner, expired deadline, or
  terminal Invocation cannot write. The other legitimate writer is the
  first-terminal Invocation transaction: cancellation, settlement, or reaping
  must atomically close every open ToolCall/attempt and append its synthetic
  result without pretending to own an expired lease. Its terminal lifecycle
  revision must cover the resulting transcript sequence.

- **R5 — Explicit checkpoint cursor.** Each Invocation must have a monotonic
  checkpoint sequence and iteration counter. Append-only checkpoints identify
  whether a model iteration or ToolCall outcome advanced progress, the fenced
  lease attempt that wrote it, the transcript sequence through which replay is
  complete, and the corresponding usage receipt or ToolCall. Checkpoints never
  contain provider process state or transcript content. A transaction may not
  move either cursor backward, skip its required evidence, or reference another
  Invocation's scope. After each model checkpoint, cumulative accepted receipts
  must be checked against iteration, output-token, cost, active-execution, and
  segment deadlines before a requested tool or another model iteration runs. A
  stop retains the committed transcript prefix and closes its prepared calls in
  the terminal transaction.

- **R6 — Replay-safe usage receipts.** Every completed model request must write
  one immutable normalized usage/provenance receipt keyed by Invocation and
  iteration in the same checkpoint transaction as its assistant message.
  Equal replay converges; changed reuse conflicts. Terminal aggregate usage
  remains the public projection. When a completed or provider-result failure
  carries that projection, it must equal the sum of accepted receipts.
  Cancellation and `execution_lost` retain the existing no-aggregate rule even
  when internal receipts exist; the receipts still let future recovery avoid
  applying recorded model work twice.

- **R7 — Deterministic builtin proof without a product surface.** A test-only
  builtin must execute through the same Dive callback and ToolCall coordinator
  used by future trusted builtins. It performs a deterministic, side-effect-free
  transformation, records request before execution and result before the next
  model iteration, and can be enabled only by injected test configuration—not
  by the public Runtime request. Existing public specs containing `tools` remain
  rejected and production generation remains tool-free.

- **R8 — Pre-resume failure semantics stay honest.** This foundation must not
  make an expired Invocation claim runnable again. Until PRD 014, engine loss
  still settles as `execution_lost`; cancellation, deadline reaping, and failed
  settlement must close open ToolCalls without permitting a stale checkpoint.
  The retained transcript, ToolCalls, attempts, receipts, and latest checkpoint
  are evidence for later recovery, not a current resume promise.

## Acceptance

- [x] **A1 (R1, R2, R5):** Migration and repository tests create one scoped
  ToolCall batch and prove ID formats, composite foreign keys, immutable
  request identity, monotonic cursors, append-only checkpoints/receipts, and
  rejection of cross-Account, cross-Session, cross-Invocation, or mismatched
  transcript references. ToolCall rows contain no request/result payload.

- [x] **A2 (R3, R5, R6):** A two-iteration fake model requests the deterministic
  builtin and then answers. Before the builtin is released, Postgres already
  contains the assistant request message, ToolCall, first usage receipt, and
  model checkpoint in one committed cut. Injected failure at each write rolls
  the whole cut back and the builtin execution count remains zero.

- [x] **A3 (R4, R5):** Releasing the builtin writes one running attempt; its
  deterministic result atomically appends one tool message, terminalizes the
  attempt and ToolCall, and advances one tool checkpoint before the second
  model request. Twenty equal concurrent outcome submissions converge on that
  result and one transcript row; changed or stale submissions fail without a
  write.

- [x] **A4 (R2, R3, R6):** The second model iteration and terminal settlement
  leave a provider-valid transcript ordered as user → assistant tool request →
  tool result → final assistant response. There is one usage receipt per model
  iteration, the public aggregate equals their sum, and reloading the transcript
  plus latest cursor after a process restart reconstructs the same completed
  prefix without provider envelopes or a checkpoint content blob.

- [x] **A5 (R2, R4, R8):** Cancellation, deadline expiry, and lease reaping race
  a prepared or running builtin. The first Invocation terminal decision wins,
  open ToolCalls and attempts become terminal with one canonical synthetic
  error result, and a stale tool completion cannot append, change a cursor, or
  overwrite the terminal outcome. Lease loss still produces
  `execution_lost`, not a new claim. The terminal lifecycle watermark includes
  the synthetic result. A cancellation or reap after a model receipt retains
  that receipt internally while exposing no terminal aggregate usage.

- [x] **A6 (R1, R4, R7):** Unit tests prove equal provider-call replay resolves
  the stored ToolCall, changed reuse conflicts, the builtin cannot start before
  its request checkpoint, and neither callback nor host mode executes.
  Public JSON validation and OpenAPI still reject `spec.tools`; the production
  daemon registers no test builtin. The same coordinator is exercised through
  embedded claim execution and the exact Cloud Tasks attempt handler.

- [x] **A7 (R1–R8):** `make check` and the full Postgres integration suite pass.
  The architecture, API design, and decision log freeze the ToolCall,
  receipt/checkpoint, terminal-closure, usage-projection, and retention
  contracts. Operator documentation distinguishes checkpoint evidence from
  crash resumption, states that ToolCall content remains canonical only in
  transcript messages, and leaves PRD 013/014/015 responsibilities explicit.
  New records are retained and deleted only with their owning
  Invocation/Session trace; this PRD adds no independent pruning path.

## Risks and open decisions

- Provider calls can finish before their checkpoint transaction commits. No
  database design can make that external request atomic; PRD 014 must resume
  only from the last accepted checkpoint and preserve the stable next-iteration
  identity rather than silently claim exact-once provider charging.
- This PRD intentionally validates one sequential builtin call. The schema
  carries batch ordinals, but parallel execution policy and partial-batch
  recovery require their own proof before production builtins use it.
- PRD 013 uses this boundary for structured output without an external side
  effect. PRD 014 consumes these records for engine replacement. PRD 015 adds
  public host ToolCalls and the narrow result command.
